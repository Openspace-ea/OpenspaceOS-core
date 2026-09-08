package command

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// SchedulerConfig 是调度器的配置参数。
type SchedulerConfig struct {
	// MaxConcurrent 最大并发发送数，默认 10。
	MaxConcurrent int
	// Timeout 单次发送超时，默认 30s。
	Timeout time.Duration
	// MaxRetries 最大重试次数，默认 3。
	MaxRetries int
	// RetryBackoff 重试退避基数，默认 2s。
	RetryBackoff time.Duration
}

// withDefaults 填充零值为默认值。
func (c SchedulerConfig) withDefaults() SchedulerConfig {
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 10
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = 2 * time.Second
	}
	return c
}

// Scheduler 是指令调度器。
//
// 从优先队列中取出待发送指令，并发发送（受 MaxConcurrent 限制），
// 发送前发布 CommandSent 事件，收到 Ack 后发布 CommandAcked 事件。
// 超时后自动重试（指数退避），达到 MaxRetries 后标记为 failed。
type Scheduler struct {
	queue   *PriorityQueue
	adapter func() plugin.CommandAdapter // 获取当前适配器的回调
	bus     core.MessageBus
	logger  *slog.Logger
	metrics *Metrics
	config  SchedulerConfig
	stopCh  chan struct{}
	wg      sync.WaitGroup
	sem     chan struct{} // 并发信号量
	stopped bool
	mu      sync.Mutex
}

// NewScheduler 创建调度器。
func NewScheduler(
	queue *PriorityQueue,
	adapterFn func() plugin.CommandAdapter,
	bus core.MessageBus,
	metrics *Metrics,
	logger *slog.Logger,
	config SchedulerConfig,
) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	config = config.withDefaults()
	return &Scheduler{
		queue:   queue,
		adapter: adapterFn,
		bus:     bus,
		logger:  logger,
		metrics: metrics,
		config:  config,
		stopCh:  make(chan struct{}),
		sem:     make(chan struct{}, config.MaxConcurrent),
	}
}

// Start 启动调度器主循环。
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.dispatchLoop()
	s.logger.Info("指令调度器已启动",
		"maxConcurrent", s.config.MaxConcurrent,
		"timeout", s.config.Timeout,
		"maxRetries", s.config.MaxRetries,
	)
}

// Stop 停止调度器，等待所有在途发送完成。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	close(s.stopCh)
	s.wg.Wait()
	s.logger.Info("指令调度器已停止")
}

// dispatchLoop 是调度器主循环：从队列取指令 → 并发发送。
func (s *Scheduler) dispatchLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		// 非阻塞尝试获取信号量
		select {
		case s.sem <- struct{}{}:
		case <-s.stopCh:
			return
		}

		// 阻塞式取下一个待发送指令
		item := s.queue.Dequeue()
		if item == nil {
			// 队列暂无待发送指令，释放信号量后短暂等待
			<-s.sem
			select {
			case <-s.stopCh:
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}

		s.wg.Add(1)
		go func(item *CommandItem) {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			s.sendItem(item)
		}(item)
	}
}

// sendResult 是适配器发送的异步结果。
type sendResult struct {
	ack plugin.Ack
	err error
}

// sendItem 发送单条指令：发布 CommandSent → 调用 Adapter → 等待 Ack 或超时 → 发布 CommandAcked。
//
// 使用 goroutine + select 模式确保超时能正确触发，即使适配器 Send 阻塞。
func (s *Scheduler) sendItem(item *CommandItem) {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.Timeout)
	defer cancel()

	// 发布 CommandSent 事件
	if err := s.publishCommandSent(ctx, &item.Command); err != nil {
		s.logger.Error("发布 CommandSent 事件失败",
			"commandId", item.Command.CommandID,
			"error", err,
		)
	}

	s.metrics.TotalSent.Add(1)
	sendStart := time.Now()

	// 获取当前适配器
	adapter := s.adapter()
	if adapter == nil {
		s.logger.Warn("指令发送失败：未设置适配器",
			"commandId", item.Command.CommandID,
			"retryCount", item.RetryCount,
		)
		s.retryOrFail(item, "未设置指令适配器")
		return
	}

	// 异步调用适配器发送，通过 select 等待结果或超时
	resultCh := make(chan sendResult, 1)
	go func() {
		ack, err := adapter.Send(item.Command)
		resultCh <- sendResult{ack: ack, err: err}
	}()

	select {
	case <-ctx.Done():
		// 发送超时
		s.metrics.TotalTimeout.Add(1)
		s.logger.Warn("指令发送超时",
			"commandId", item.Command.CommandID,
			"retryCount", item.RetryCount,
		)
		s.retryOrFail(item, "发送超时")
		return

	case res := <-resultCh:
		// 记录发送到确认的延迟
		elapsed := time.Since(sendStart)
		s.metrics.RecordLatency(elapsed.Microseconds())

		if res.err != nil {
			s.logger.Warn("指令发送失败",
				"commandId", item.Command.CommandID,
				"retryCount", item.RetryCount,
				"error", res.err,
			)
			s.retryOrFail(item, res.err.Error())
			return
		}

		if !res.ack.Success {
			// 适配器返回失败确认，视为发送失败并重试
			s.logger.Warn("指令发送失败（适配器返回失败）",
				"commandId", item.Command.CommandID,
				"retryCount", item.RetryCount,
				"message", res.ack.Message,
			)
			s.retryOrFail(item, res.ack.Message)
			return
		}

		// 成功：更新状态并发布 CommandAcked 事件
		item.Ack = &res.ack
		now := time.Now()
		item.AckedAt = &now
		item.Status = StatusAcked
		s.queue.Update(item)

		if err := s.publishCommandAcked(ctx, &item.Command, res.ack); err != nil {
			s.logger.Error("发布 CommandAcked 事件失败",
				"commandId", item.Command.CommandID,
				"error", err,
			)
		}

		s.metrics.RecordAck(true)
		s.logger.Info("指令已确认",
			"commandId", item.Command.CommandID,
			"success", true,
			"latency", elapsed,
		)
	}
}

// retryOrFail 根据重试次数决定重试或标记失败。
//
// 重试采用指数退避：RetryBackoff * 2^retryCount。
func (s *Scheduler) retryOrFail(item *CommandItem, errMsg string) {
	if item.RetryCount < s.config.MaxRetries {
		item.RetryCount++
		item.Error = errMsg

		// 指数退避等待
		backoff := s.config.RetryBackoff * time.Duration(1<<uint(item.RetryCount-1))
		select {
		case <-s.stopCh:
			// 调度器停止，标记为失败
			item.Status = StatusFailed
			s.queue.Update(item)
			s.metrics.TotalFailed.Add(1)
			return
		case <-time.After(backoff):
		}

		// 重新入队等待发送
		item.Status = StatusQueued
		item.SentAt = nil
		s.queue.Update(item)
		s.logger.Info("指令已重新入队等待重试",
			"commandId", item.Command.CommandID,
			"retryCount", item.RetryCount,
			"backoff", backoff,
		)
		return
	}

	// 达到最大重试次数，标记为失败
	item.Status = StatusFailed
	item.Error = errMsg
	s.queue.Update(item)
	s.metrics.TotalFailed.Add(1)
	s.logger.Error("指令发送失败，已达最大重试次数",
		"commandId", item.Command.CommandID,
		"maxRetries", s.config.MaxRetries,
	)
}

// publishCommandSent 发布 CommandSent 事件。
func (s *Scheduler) publishCommandSent(ctx context.Context, cmd *plugin.Command) error {
	e := &event.Event{
		EventID:      uuid.NewString(),
		EventType:    event.EventCommandSent,
		Timestamp:    time.Now(),
		SourceNodeID: cmd.SatelliteID,
	}
	payload := event.CommandSentPayload{
		CommandID:   cmd.CommandID,
		SatelliteID: cmd.SatelliteID,
		CommandType: cmd.CommandType,
	}
	if err := e.SetPayload(payload); err != nil {
		return fmt.Errorf("设置 payload 失败: %w", err)
	}
	return s.bus.Publish(ctx, e)
}

// publishCommandAcked 发布 CommandAcked 事件。
func (s *Scheduler) publishCommandAcked(ctx context.Context, cmd *plugin.Command, ack plugin.Ack) error {
	e := &event.Event{
		EventID:      uuid.NewString(),
		EventType:    event.EventCommandAcked,
		Timestamp:    time.Now(),
		SourceNodeID: cmd.SatelliteID,
	}
	payload := event.CommandAckedPayload{
		CommandID: cmd.CommandID,
		Success:   ack.Success,
		Message:   ack.Message,
	}
	if err := e.SetPayload(payload); err != nil {
		return fmt.Errorf("设置 payload 失败: %w", err)
	}
	return s.bus.Publish(ctx, e)
}
