package command

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// 指令状态常量。
const (
	StatusQueued    = "queued"    // 已入队待发送
	StatusSending   = "sending"   // 正在发送
	StatusAcked     = "acked"     // 已收到确认
	StatusFailed    = "failed"    // 发送失败
	StatusCancelled = "cancelled" // 已取消
	StatusTimeout   = "timeout"   // 发送超时
)

// CommandItem 是优先队列中的一个指令项。
type CommandItem struct {
	// Command 待发送的指令。
	Command plugin.Command
	// Priority 优先级，数值越大优先级越高。
	Priority int
	// Status 当前状态。
	Status string
	// Ack 收到的确认响应，未确认时为 nil。
	Ack *plugin.Ack
	// EnqueuedAt 入队时间。
	EnqueuedAt time.Time
	// SentAt 发送时间。
	SentAt *time.Time
	// AckedAt 确认时间。
	AckedAt *time.Time
	// RetryCount 已重试次数。
	RetryCount int
	// Error 错误信息。
	Error string
}

// PriorityQueue 是基于 map 的优先队列。
//
// 同时承担两个职责：
//  1. 按优先级排序的待发送队列（Dequeue 取出优先级最高的 queued 项）
//  2. 所有指令的状态存储（Get/List 用于状态查询）
//
// 优先级排序规则：Priority 大的先出；同优先级按 EnqueuedAt 先入先出。
type PriorityQueue struct {
	items map[string]*CommandItem
	mu    sync.Mutex
}

// NewPriorityQueue 创建优先队列。
func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		items: make(map[string]*CommandItem),
	}
}

// Enqueue 将指令项入队。
//
// 若 commandID 已存在则返回错误。
func (q *PriorityQueue) Enqueue(item *CommandItem) error {
	if item == nil {
		return fmt.Errorf("item 不能为 nil")
	}
	if item.Command.CommandID == "" {
		return fmt.Errorf("commandId 不能为空")
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if _, exists := q.items[item.Command.CommandID]; exists {
		return fmt.Errorf("指令 %s 已存在", item.Command.CommandID)
	}
	if item.Status == "" {
		item.Status = StatusQueued
	}
	if item.EnqueuedAt.IsZero() {
		item.EnqueuedAt = time.Now()
	}
	q.items[item.Command.CommandID] = item
	return nil
}

// Dequeue 取出优先级最高的 queued 状态指令项。
//
// 取出后将该项状态置为 sending，但不会从 map 中移除（保留用于状态查询）。
// 队列为空时返回 nil。
func (q *PriorityQueue) Dequeue() *CommandItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	var best *CommandItem
	for _, item := range q.items {
		if item.Status != StatusQueued {
			continue
		}
		if best == nil {
			best = item
			continue
		}
		// 优先级高的优先；同优先级按入队时间早的优先
		if item.Priority > best.Priority {
			best = item
		} else if item.Priority == best.Priority && item.EnqueuedAt.Before(best.EnqueuedAt) {
			best = item
		}
	}

	if best != nil {
		best.Status = StatusSending
		now := time.Now()
		best.SentAt = &now
	}
	return best
}

// Get 查询指定 commandID 的指令项。
func (q *PriorityQueue) Get(commandID string) (*CommandItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[commandID]
	return item, ok
}

// Update 更新指令项的状态。
func (q *PriorityQueue) Update(item *CommandItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.items[item.Command.CommandID]; exists {
		q.items[item.Command.CommandID] = item
	}
}

// Remove 从队列中移除指定 commandID 的指令项。
func (q *PriorityQueue) Remove(commandID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.items, commandID)
}

// Len 返回队列中所有指令项的总数（含所有状态）。
func (q *PriorityQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// List 返回所有指令项的快照（按入队时间升序排列）。
func (q *PriorityQueue) List() []*CommandItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]*CommandItem, 0, len(q.items))
	for _, item := range q.items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].EnqueuedAt.Before(out[j].EnqueuedAt)
	})
	return out
}

// ListByStatus 返回指定状态的指令项列表。
func (q *PriorityQueue) ListByStatus(status string) []*CommandItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]*CommandItem, 0)
	for _, item := range q.items {
		if item.Status == status {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].EnqueuedAt.Before(out[j].EnqueuedAt)
	})
	return out
}

// PendingCount 返回 queued 状态的指令数量。
func (q *PriorityQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for _, item := range q.items {
		if item.Status == StatusQueued {
			count++
		}
	}
	return count
}
