package telemetry

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Receiver 限量保护相关的默认配置常量（T5.3）。
const (
	// defaultMaxConns 最大并发连接数，超过则拒绝新连接。
	defaultMaxConns = 64
	// defaultPerConnRate 每连接每秒允许的最大帧数（令牌桶速率）。
	defaultPerConnRate = 200.0
	// defaultPerConnBurst 每连接令牌桶容量（突发上限）。
	defaultPerConnBurst = 20
	// defaultIdleTimeout 单连接空闲超时，超过则熔断断开（超时熔断）。
	defaultIdleTimeout = 5 * time.Minute
)

// ReceiverConfig 是 TCP 接收器的限量保护配置（T5.3）。
//
// 三个维度：最大连接数、每连接令牌桶限流、空闲超时熔断。
type ReceiverConfig struct {
	// MaxConns 最大并发连接数。<=0 时使用默认值。
	MaxConns int
	// PerConnRate 每连接每秒帧数（令牌桶速率）。<=0 时使用默认值。
	PerConnRate float64
	// Burst 每连接令牌桶容量（突发上限）。<=0 时使用默认值。
	Burst int
	// IdleTimeout 单连接空闲超时。<=0 时使用默认值。
	IdleTimeout time.Duration
}

func (c ReceiverConfig) applyDefaults() ReceiverConfig {
	if c.MaxConns <= 0 {
		c.MaxConns = defaultMaxConns
	}
	if c.PerConnRate <= 0 {
		c.PerConnRate = defaultPerConnRate
	}
	if c.Burst <= 0 {
		c.Burst = defaultPerConnBurst
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = defaultIdleTimeout
	}
	return c
}

// Receiver 是遥测数据的 TCP 接收器。
//
// 监听指定 TCP 端口，按行（'\n' 分隔）读取每个连接的数据，
// 将每行数据通过 handler 回调上交给上层处理。
// 支持多连接并发处理与优雅关闭。
// 自 v2 起内置限量保护：最大连接数、每连接令牌桶限流、空闲超时熔断（T5.3）。
type Receiver struct {
	listener net.Listener
	handler  func([]byte)
	logger   *slog.Logger

	cfg    ReceiverConfig
	active atomic.Int64

	mu       sync.Mutex
	wg       sync.WaitGroup
	closed   bool
	stopOnce sync.Once
}

// NewReceiver 创建 TCP 接收器。
//
// logger 为 nil 时使用 slog.Default()。默认启用限量保护（见常量）。
func NewReceiver(logger *slog.Logger) *Receiver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Receiver{
		logger: logger,
		cfg:    ReceiverConfig{}.applyDefaults(),
	}
}

// Configure 设置限量保护配置（T5.3）。在 Start 之前调用生效。
func (r *Receiver) Configure(cfg ReceiverConfig) {
	r.cfg = cfg.applyDefaults()
}

// Start 在指定端口启动 TCP 监听。
//
// handler 回调用于处理每行数据（不含行尾换行符）。
// 该方法非阻塞：启动监听后立即返回，连接处理在后台 goroutine 中进行。
func (r *Receiver) Start(port int, handler func([]byte)) error {
	if handler == nil {
		return fmt.Errorf("handler 不能为 nil")
	}
	r.handler = handler

	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 TCP 端口 %d 失败: %w", port, err)
	}
	r.listener = ln
	r.logger.Info("遥测 TCP 接收器已启动",
		"port", port,
		"maxConns", r.cfg.MaxConns,
		"perConnRate", r.cfg.PerConnRate,
		"idleTimeout", r.cfg.IdleTimeout,
	)

	// 后台接受连接循环
	r.wg.Add(1)
	go r.acceptLoop()

	return nil
}

// acceptLoop 接受新连接的循环。
func (r *Receiver) acceptLoop() {
	defer r.wg.Done()
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			r.mu.Lock()
			closed := r.closed
			r.mu.Unlock()
			if closed {
				// 正常关闭
				return
			}
			r.logger.Error("接受连接失败", "error", err)
			return
		}
		// 最大连接数限制：超出则直接拒绝
		if int(r.active.Load()) >= r.cfg.MaxConns {
			r.logger.Warn("达到最大连接数限制，拒绝连接",
				"remote", conn.RemoteAddr().String(),
				"maxConns", r.cfg.MaxConns,
			)
			conn.Close()
			continue
		}
		r.wg.Add(1)
		go r.handleConn(conn)
	}
}

// handleConn 处理单个 TCP 连接：按行读取并回调 handler。
//
// 每个连接一个令牌桶（每连接限流），并按行刷新读 deadline（空闲超时熔断）。
func (r *Receiver) handleConn(conn net.Conn) {
	defer r.wg.Done()
	defer conn.Close()
	r.active.Add(1)
	defer r.active.Add(-1)

	remoteAddr := conn.RemoteAddr().String()
	r.logger.Debug("遥测连接已建立", "remote", remoteAddr)

	// 每连接令牌桶限流
	limiter := rate.NewLimiter(rate.Limit(r.cfg.PerConnRate), r.cfg.Burst)
	rateCtx := context.Background()

	scanner := bufio.NewScanner(conn)
	// 增大单行缓冲区上限，避免长行（如 TLE 三行 + 名称）被截断
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	for scanner.Scan() {
		// 空闲超时熔断：刷新读 deadline，超过未读到新行即断开
		_ = conn.SetReadDeadline(time.Now().Add(r.cfg.IdleTimeout))

		// 令牌桶限流（阻塞等待直到获得令牌）
		if err := limiter.Wait(rateCtx); err != nil {
			// context 永不取消，理论上不会走到；防御性处理
			r.logger.Debug("限流等待被打断", "remote", remoteAddr, "error", err)
			return
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// 复制一份再回调，避免 Scanner 复用底层缓冲区导致数据被覆盖
		data := make([]byte, len(line))
		copy(data, line)
		r.handler(data)
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, net.ErrClosed) {
			r.logger.Debug("连接已关闭", "remote", remoteAddr)
		} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
			r.logger.Debug("连接空闲超时，熔断断开", "remote", remoteAddr, "idleTimeout", r.cfg.IdleTimeout)
		} else {
			r.logger.Debug("连接读取结束", "remote", remoteAddr, "error", err)
		}
	} else {
		r.logger.Debug("遥测连接已关闭", "remote", remoteAddr)
	}
}

// Stop 停止接收器，关闭监听并等待所有连接处理完毕。
func (r *Receiver) Stop() error {
	var err error
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		if r.listener != nil {
			err = r.listener.Close()
		}
		// 等待所有连接 goroutine 退出
		r.wg.Wait()
		r.logger.Info("遥测 TCP 接收器已停止")
	})
	return err
}