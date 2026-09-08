package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// 默认缓冲队列容量。
const defaultBufferSize = 1024

// Pipeline 是遥测数据处理流水线。
//
// 数据流：TCP Receiver → BufferQueue → Parser → Normalizer → MessageBus。
//
// 接收器将原始数据推入缓冲队列，工作 goroutine 从队列取出数据，
// 调用当前活跃解析器解析为 TelemetryFrame，再标准化为事件发布到总线。
type Pipeline struct {
	receiver *Receiver
	parser   plugin.TelemetryParser // 当前活跃解析器
	manager  *plugin.Manager        // 插件管理器（用于查找解析器）
	bus      core.MessageBus
	logger   *slog.Logger
	metrics  *Metrics
	buffer   *BufferQueue
	stopCh   chan struct{}

	mu       sync.RWMutex
	parserMu sync.RWMutex
	wg       sync.WaitGroup
	stopped  bool
}

// NewPipeline 创建遥测流水线。
//
// manager 用于按名称查找解析器；bus 用于发布事件；logger 为 nil 时使用 slog.Default()。
func NewPipeline(manager *plugin.Manager, bus core.MessageBus, logger *slog.Logger) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	metrics := &Metrics{}
	return &Pipeline{
		receiver: NewReceiver(logger),
		manager:  manager,
		bus:      bus,
		logger:   logger,
		metrics:  metrics,
		buffer:   NewBufferQueue(defaultBufferSize, metrics),
		stopCh:   make(chan struct{}),
	}
}

// Start 启动流水线：启动 TCP 接收器与处理工作 goroutine。
//
// port 为 TCP 监听端口。若未设置解析器，将使用 "json" 作为默认解析器。
func (p *Pipeline) Start(ctx context.Context, port int) error {
	// 若未设置解析器，尝试默认使用 json
	p.parserMu.RLock()
	hasParser := p.parser != nil
	p.parserMu.RUnlock()
	if !hasParser {
		if err := p.SetParser("json"); err != nil {
			p.logger.Warn("未设置默认解析器，请通过 SetParser 设置", "error", err)
		}
	}

	// 启动处理工作 goroutine
	p.wg.Add(1)
	go p.processLoop(ctx)

	// 启动 TCP 接收器，handler 将原始数据推入缓冲队列
	if err := p.receiver.Start(port, p.handleRaw); err != nil {
		close(p.stopCh)
		p.buffer.Close()
		p.wg.Wait()
		return fmt.Errorf("启动遥测接收器失败: %w", err)
	}
	p.logger.Info("遥测流水线已启动", "port", port, "parser", p.GetParserName())
	return nil
}

// handleRaw 将原始数据推入缓冲队列并更新入口计数。
func (p *Pipeline) handleRaw(data []byte) {
	p.metrics.IngressQPS.Add(1)
	p.buffer.Push(data)
}

// processLoop 是处理工作主循环：从缓冲队列取出数据并处理。
func (p *Pipeline) processLoop(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}
		data, ok := p.buffer.Pop()
		if !ok {
			return
		}
		p.process(ctx, data)
	}
}

// process 处理一条原始数据：解析 → 标准化 → 发布。
func (p *Pipeline) process(ctx context.Context, raw []byte) {
	p.parserMu.RLock()
	parser := p.parser
	p.parserMu.RUnlock()

	if parser == nil {
		p.logger.Warn("未设置遥测解析器，丢弃数据", "size", len(raw))
		p.metrics.DropCount.Add(1)
		return
	}

	start := time.Now()
	frames, err := parser.Parse(raw)
	elapsed := time.Since(start)
	p.metrics.ParseLatency.Store(elapsed.Microseconds())

	if err != nil {
		p.logger.Error("遥测数据解析失败", "parser", parser.Name(), "error", err, "size", len(raw))
		p.metrics.DropCount.Add(1)
		return
	}

	for _, frame := range frames {
		e, err := Normalize(&frame)
		if err != nil {
			p.logger.Error("遥测帧标准化失败", "satelliteId", frame.SatelliteID, "error", err)
			p.metrics.DropCount.Add(1)
			continue
		}
		if err := p.bus.Publish(ctx, e); err != nil {
			p.logger.Error("遥测事件发布失败", "eventId", e.EventID, "error", err)
			p.metrics.DropCount.Add(1)
			continue
		}
		p.logger.Debug("遥测事件已发布",
			"eventId", e.EventID,
			"satelliteId", frame.SatelliteID,
			"parser", parser.Name(),
		)
	}
}

// SetParser 设置当前活跃解析器。
//
// 通过名称从 PluginManager 查找对应解析器。
func (p *Pipeline) SetParser(name string) error {
	if p.manager == nil {
		return fmt.Errorf("插件管理器未初始化")
	}
	parser, ok := p.manager.GetParser(name)
	if !ok {
		return fmt.Errorf("未找到名为 %q 的遥测解析器", name)
	}
	p.parserMu.Lock()
	p.parser = parser
	p.parserMu.Unlock()
	p.logger.Info("已切换遥测解析器", "name", name)
	return nil
}

// GetParserName 返回当前活跃解析器的名称，未设置时返回空字符串。
func (p *Pipeline) GetParserName() string {
	p.parserMu.RLock()
	defer p.parserMu.RUnlock()
	if p.parser == nil {
		return ""
	}
	return p.parser.Name()
}

// Process 手动处理一条原始数据（供 REST API 测试使用）。
//
// 与 TCP 接收路径共用同一处理逻辑。
func (p *Pipeline) Process(ctx context.Context, raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("原始数据为空")
	}
	p.metrics.IngressQPS.Add(1)
	p.process(ctx, raw)
	return nil
}

// Metrics 返回流水线指标实例。
func (p *Pipeline) Metrics() *Metrics {
	return p.metrics
}

// Stop 停止流水线：关闭接收器与缓冲队列，等待处理 goroutine 退出。
func (p *Pipeline) Stop() error {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil
	}
	p.stopped = true
	p.mu.Unlock()

	// 关闭接收器（停止接受新连接与数据）
	if err := p.receiver.Stop(); err != nil {
		p.logger.Error("停止遥测接收器失败", "error", err)
	}
	// 通知处理 goroutine 退出
	close(p.stopCh)
	// 关闭缓冲队列
	p.buffer.Close()
	// 等待处理 goroutine 退出
	p.wg.Wait()
	p.logger.Info("遥测流水线已停止")
	return nil
}
