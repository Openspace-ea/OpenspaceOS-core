package command

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/openspace-os/openspace-os-core/internal/auth"
	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// CommandStatus 是指令状态的查询响应。
type CommandStatus struct {
	CommandID   string         `json:"commandId"`
	SatelliteID string         `json:"satelliteId"`
	CommandType string         `json:"commandType"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Priority    int            `json:"priority"`
	Status      string         `json:"status"`
	EnqueuedAt  time.Time      `json:"enqueuedAt"`
	SentAt      *time.Time     `json:"sentAt,omitempty"`
	AckedAt     *time.Time     `json:"ackedAt,omitempty"`
	RetryCount  int            `json:"retryCount"`
	Error       string         `json:"error,omitempty"`
	Ack         *plugin.Ack    `json:"ack,omitempty"`
}

// Pipeline 是指令流水线核心。
//
// 数据流：API 接收 → 校验 → 入队（按优先级）→ 调度 → Adapter 发送 → 等待确认 → 发布事件。
type Pipeline struct {
	queue       *PriorityQueue
	adapter     plugin.CommandAdapter // 当前活跃适配器
	adapterName string                // 当前活跃适配器的注册名称
	manager     *plugin.Manager
	bus         core.MessageBus
	logger      *slog.Logger
	metrics     *Metrics
	validator   *Validator
	scheduler   *Scheduler
	config      SchedulerConfig
	stopCh      chan struct{}

	mu       sync.RWMutex
	wg       sync.WaitGroup
	stopped  bool
	stopOnce sync.Once
}

// NewPipeline 创建指令流水线。
//
// manager 用于按名称查找适配器；bus 用于发布事件；logger 为 nil 时使用 slog.Default()。
func NewPipeline(manager *plugin.Manager, bus core.MessageBus, logger *slog.Logger) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		queue:     NewPriorityQueue(),
		manager:   manager,
		bus:       bus,
		logger:    logger,
		metrics:   &Metrics{},
		validator: NewValidator(nil),
		config:    SchedulerConfig{}.withDefaults(),
		stopCh:    make(chan struct{}),
	}
}

// Start 启动调度器 goroutine。
func (p *Pipeline) Start() {
	p.mu.Lock()
	if p.scheduler != nil {
		p.mu.Unlock()
		return
	}
	p.scheduler = NewScheduler(
		p.queue,
		p.getCurrentAdapter,
		p.bus,
		p.metrics,
		p.logger,
		p.config,
	)
	p.mu.Unlock()

	p.scheduler.Start()
	p.logger.Info("指令流水线已启动")
}

// Stop 停止调度和发送。
func (p *Pipeline) Stop() {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopped = true
		scheduler := p.scheduler
		p.mu.Unlock()

		close(p.stopCh)
		if scheduler != nil {
			scheduler.Stop()
		}
		p.logger.Info("指令流水线已停止")
	})
}

// SetAdapter 设置当前活跃的适配器。
//
// 通过名称从 PluginManager 查找对应适配器。
func (p *Pipeline) SetAdapter(name string) error {
	if p.manager == nil {
		return fmt.Errorf("插件管理器未初始化")
	}
	adapter, ok := p.manager.GetAdapter(name)
	if !ok {
		return fmt.Errorf("未找到名为 %q 的指令适配器", name)
	}
	p.mu.Lock()
	p.adapter = adapter
	p.adapterName = name
	p.mu.Unlock()
	p.logger.Info("已切换指令适配器", "name", name)
	return nil
}

// SetValidator 设置指令校验器（可注入带 auth 服务的校验器）。
func (p *Pipeline) SetValidator(v *Validator) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.validator = v
}

// SetSchedulerConfig 设置调度器配置（需在 Start 之前调用）。
func (p *Pipeline) SetSchedulerConfig(config SchedulerConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = config.withDefaults()
}

// getCurrentAdapter 返回当前活跃适配器（供 Scheduler 回调使用）。
func (p *Pipeline) getCurrentAdapter() plugin.CommandAdapter {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.adapter
}

// GetAdapterName 返回当前活跃适配器的注册名称，未设置时返回空字符串。
func (p *Pipeline) GetAdapterName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.adapterName
}

// Send 入队并异步发送指令。
//
// 流程：校验 → 生成 CommandID（为空时）→ 入队 → 调度器异步发送。
func (p *Pipeline) Send(ctx context.Context, cmd plugin.Command, priority int) error {
	// 生成 CommandID（为空时自动生成）
	if cmd.CommandID == "" {
		cmd.CommandID = uuid.NewString()
	}

	// 校验指令
	p.mu.RLock()
	validator := p.validator
	p.mu.RUnlock()
	if validator != nil {
		if err := validator.Validate(&cmd); err != nil {
			return fmt.Errorf("指令校验失败: %w", err)
		}
	}

	// 入队
	item := &CommandItem{
		Command:     cmd,
		Priority:    priority,
		Status:      StatusQueued,
		EnqueuedAt:  time.Now(),
		RetryCount:  0,
	}
	if err := p.queue.Enqueue(item); err != nil {
		return fmt.Errorf("指令入队失败: %w", err)
	}

	p.logger.Info("指令已入队",
		"commandId", cmd.CommandID,
		"satelliteId", cmd.SatelliteID,
		"commandType", cmd.CommandType,
		"priority", priority,
	)
	return nil
}

// GetStatus 查询指令状态。
func (p *Pipeline) GetStatus(commandID string) (*CommandStatus, error) {
	item, ok := p.queue.Get(commandID)
	if !ok {
		return nil, fmt.Errorf("指令 %s 不存在", commandID)
	}
	return itemToStatus(item), nil
}

// Cancel 取消待发送的指令。
//
// 仅 queued 状态的指令可取消；已发送或已确认的指令不可取消。
func (p *Pipeline) Cancel(commandID string) error {
	item, ok := p.queue.Get(commandID)
	if !ok {
		return fmt.Errorf("指令 %s 不存在", commandID)
	}
	if item.Status != StatusQueued {
		return fmt.Errorf("指令 %s 当前状态为 %s，无法取消", commandID, item.Status)
	}
	item.Status = StatusCancelled
	p.queue.Update(item)
	p.logger.Info("指令已取消", "commandId", commandID)
	return nil
}

// ListCommands 列出所有指令，可选按状态过滤。
func (p *Pipeline) ListCommands(status string) []*CommandStatus {
	var items []*CommandItem
	if status != "" {
		items = p.queue.ListByStatus(status)
	} else {
		items = p.queue.List()
	}
	out := make([]*CommandStatus, 0, len(items))
	for _, item := range items {
		out = append(out, itemToStatus(item))
	}
	return out
}

// Metrics 返回流水线指标实例。
func (p *Pipeline) Metrics() *Metrics {
	return p.metrics
}

// ListAdapters 列出所有已注册的指令适配器名称。
func (p *Pipeline) ListAdapters() []string {
	if p.manager == nil {
		return []string{}
	}
	return p.manager.ListAdapters()
}

// SetAuthService 设置认证服务，用于构建带权限校验的 Validator。
func (p *Pipeline) SetAuthService(authSvc *auth.Service) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.validator = NewValidator(authSvc)
}

// itemToStatus 将 CommandItem 转换为 CommandStatus。
func itemToStatus(item *CommandItem) *CommandStatus {
	return &CommandStatus{
		CommandID:   item.Command.CommandID,
		SatelliteID: item.Command.SatelliteID,
		CommandType: item.Command.CommandType,
		Parameters:  item.Command.Parameters,
		Priority:    item.Priority,
		Status:      item.Status,
		EnqueuedAt:  item.EnqueuedAt,
		SentAt:      item.SentAt,
		AckedAt:     item.AckedAt,
		RetryCount:  item.RetryCount,
		Error:       item.Error,
		Ack:         item.Ack,
	}
}
