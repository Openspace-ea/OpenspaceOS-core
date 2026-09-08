package command

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// newTestSchedulerBus 创建用于测试的 LocalBus。
func newTestSchedulerBus(t *testing.T) core.MessageBus {
	t.Helper()
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// TestSchedulerSendSuccess 测试发送成功闭环：入队 → 调度 → 收到 Ack → 验证事件。
func TestSchedulerSendSuccess(t *testing.T) {
	bus := newTestSchedulerBus(t)
	queue := NewPriorityQueue()
	metrics := &Metrics{}

	// 订阅事件
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventCommandSent, event.EventCommandAcked},
	})
	defer unsubscribe()

	// 使用快速 Mock 适配器
	adapter := NewMockAdapter(10*time.Millisecond, 1.0)
	scheduler := NewScheduler(
		queue,
		func() plugin.CommandAdapter { return adapter },
		bus,
		metrics,
		nil,
		SchedulerConfig{MaxConcurrent: 2, Timeout: 5 * time.Second, MaxRetries: 1, RetryBackoff: 100 * time.Millisecond},
	)
	scheduler.Start()
	defer scheduler.Stop()

	// 入队一条指令
	item := &CommandItem{
		Command: plugin.Command{
			CommandID:   "cmd-test-1",
			SatelliteID: "sat-1",
			CommandType: "attitude",
			Parameters:  map[string]any{"mode": "point"},
		},
		Priority:   5,
		Status:     StatusQueued,
		EnqueuedAt: time.Now(),
	}
	require.NoError(t, queue.Enqueue(item))

	// 等待 CommandSent 事件
	select {
	case e := <-ch:
		assert.Equal(t, event.EventCommandSent, e.EventType)
		assert.Equal(t, "sat-1", e.SourceNodeID)
		var sentPayload event.CommandSentPayload
		require.NoError(t, e.GetPayload(&sentPayload))
		assert.Equal(t, "cmd-test-1", sentPayload.CommandID)
		assert.Equal(t, "attitude", sentPayload.CommandType)
	case <-time.After(3 * time.Second):
		t.Fatal("等待 CommandSent 事件超时")
	}

	// 等待 CommandAcked 事件
	select {
	case e := <-ch:
		assert.Equal(t, event.EventCommandAcked, e.EventType)
		var ackedPayload event.CommandAckedPayload
		require.NoError(t, e.GetPayload(&ackedPayload))
		assert.Equal(t, "cmd-test-1", ackedPayload.CommandID)
		assert.True(t, ackedPayload.Success)
	case <-time.After(3 * time.Second):
		t.Fatal("等待 CommandAcked 事件超时")
	}

	// 验证指令状态变为 acked
	status, ok := queue.Get("cmd-test-1")
	require.True(t, ok)
	assert.Equal(t, StatusAcked, status.Status)
	require.NotNil(t, status.Ack)
	assert.True(t, status.Ack.Success)

	// 验证 metrics
	assert.GreaterOrEqual(t, metrics.TotalSent.Load(), int64(1))
	assert.GreaterOrEqual(t, metrics.TotalAcked.Load(), int64(1))
}

// TestSchedulerTimeoutRetry 测试超时后自动重试。
func TestSchedulerTimeoutRetry(t *testing.T) {
	bus := newTestSchedulerBus(t)
	queue := NewPriorityQueue()
	metrics := &Metrics{}

	// Mock 适配器延迟远大于超时时间，触发超时
	adapter := NewMockAdapter(5*time.Second, 1.0)
	scheduler := NewScheduler(
		queue,
		func() plugin.CommandAdapter { return adapter },
		bus,
		metrics,
		nil,
		SchedulerConfig{
			MaxConcurrent: 1,
			Timeout:       200 * time.Millisecond, // 超时 200ms
			MaxRetries:    2,
			RetryBackoff:  50 * time.Millisecond,
		},
	)
	scheduler.Start()
	defer scheduler.Stop()

	item := &CommandItem{
		Command: plugin.Command{
			CommandID:   "cmd-timeout",
			SatelliteID: "sat-1",
			CommandType: "test",
		},
		Priority:   5,
		Status:     StatusQueued,
		EnqueuedAt: time.Now(),
	}
	require.NoError(t, queue.Enqueue(item))

	// 等待重试（超时 200ms + 退避 50ms + 超时 200ms + 退避 100ms + 超时 200ms = ~750ms）
	// 达到 MaxRetries 后标记为 failed
	require.Eventually(t, func() bool {
		item, ok := queue.Get("cmd-timeout")
		if !ok {
			return false
		}
		return item.Status == StatusFailed
	}, 10*time.Second, 100*time.Millisecond, "指令应最终标记为 failed")

	// 验证超时计数
	assert.GreaterOrEqual(t, metrics.TotalTimeout.Load(), int64(1))
	assert.GreaterOrEqual(t, metrics.TotalFailed.Load(), int64(1))
}

// TestSchedulerMaxRetriesFailed 测试达到 MaxRetries 后标记 failed。
func TestSchedulerMaxRetriesFailed(t *testing.T) {
	bus := newTestSchedulerBus(t)
	queue := NewPriorityQueue()
	metrics := &Metrics{}

	// 成功率为 0，始终失败
	adapter := NewMockAdapter(10*time.Millisecond, 0)
	scheduler := NewScheduler(
		queue,
		func() plugin.CommandAdapter { return adapter },
		bus,
		metrics,
		nil,
		SchedulerConfig{
			MaxConcurrent: 1,
			Timeout:       2 * time.Second,
			MaxRetries:    2,
			RetryBackoff:  50 * time.Millisecond,
		},
	)
	scheduler.Start()
	defer scheduler.Stop()

	item := &CommandItem{
		Command: plugin.Command{
			CommandID:   "cmd-fail",
			SatelliteID: "sat-1",
			CommandType: "test",
		},
		Priority:   5,
		Status:     StatusQueued,
		EnqueuedAt: time.Now(),
	}
	require.NoError(t, queue.Enqueue(item))

	// 等待最终失败
	require.Eventually(t, func() bool {
		item, ok := queue.Get("cmd-fail")
		if !ok {
			return false
		}
		return item.Status == StatusFailed
	}, 10*time.Second, 100*time.Millisecond, "指令应最终标记为 failed")

	final, ok := queue.Get("cmd-fail")
	require.True(t, ok)
	assert.Equal(t, StatusFailed, final.Status)
	// MaxRetries=2，应重试 2 次
	assert.Equal(t, 2, final.RetryCount)
}

// TestSchedulerConcurrentSend 测试多个指令并发发送不 panic。
func TestSchedulerConcurrentSend(t *testing.T) {
	bus := newTestSchedulerBus(t)
	queue := NewPriorityQueue()
	metrics := &Metrics{}

	adapter := NewMockAdapter(20*time.Millisecond, 1.0)
	scheduler := NewScheduler(
		queue,
		func() plugin.CommandAdapter { return adapter },
		bus,
		metrics,
		nil,
		SchedulerConfig{MaxConcurrent: 5, Timeout: 5 * time.Second, MaxRetries: 0, RetryBackoff: 100 * time.Millisecond},
	)
	scheduler.Start()
	defer scheduler.Stop()

	// 入队 10 条指令
	for i := 0; i < 10; i++ {
		item := &CommandItem{
			Command: plugin.Command{
				CommandID:   fmt.Sprintf("cmd-concurrent-%d", i),
				SatelliteID: "sat-1",
				CommandType: "test",
			},
			Priority:   5,
			Status:     StatusQueued,
			EnqueuedAt: time.Now(),
		}
		require.NoError(t, queue.Enqueue(item))
	}

	// 等待所有指令完成
	require.Eventually(t, func() bool {
		return queue.PendingCount() == 0
	}, 10*time.Second, 100*time.Millisecond, "所有指令应完成发送")

	// 验证全部 acked
	acked := queue.ListByStatus(StatusAcked)
	assert.Len(t, acked, 10)
}

// TestSchedulerNilAdapter 测试未设置适配器时指令标记为 failed。
func TestSchedulerNilAdapter(t *testing.T) {
	bus := newTestSchedulerBus(t)
	queue := NewPriorityQueue()
	metrics := &Metrics{}

	scheduler := NewScheduler(
		queue,
		func() plugin.CommandAdapter { return nil }, // 无适配器
		bus,
		metrics,
		nil,
		SchedulerConfig{MaxConcurrent: 1, Timeout: 2 * time.Second, MaxRetries: 1, RetryBackoff: 50 * time.Millisecond},
	)
	scheduler.Start()
	defer scheduler.Stop()

	item := &CommandItem{
		Command: plugin.Command{
			CommandID:   "cmd-noadapter",
			SatelliteID: "sat-1",
			CommandType: "test",
		},
		Priority:   5,
		Status:     StatusQueued,
		EnqueuedAt: time.Now(),
	}
	require.NoError(t, queue.Enqueue(item))

	require.Eventually(t, func() bool {
		item, ok := queue.Get("cmd-noadapter")
		if !ok {
			return false
		}
		return item.Status == StatusFailed
	}, 10*time.Second, 100*time.Millisecond, "无适配器时指令应标记为 failed")

	final, _ := queue.Get("cmd-noadapter")
	assert.Contains(t, final.Error, "适配器")
}

// TestSchedulerConfigDefaults 测试 SchedulerConfig 默认值。
func TestSchedulerConfigDefaults(t *testing.T) {
	cfg := SchedulerConfig{}.withDefaults()
	assert.Equal(t, 10, cfg.MaxConcurrent)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Equal(t, 2*time.Second, cfg.RetryBackoff)
}

// TestSchedulerContextNotExpired 验证 sendItem 使用的 context 能正确传递。
func TestSchedulerContextNotExpired(t *testing.T) {
	// 确保快速发送不会因 context 过期误判超时
	bus := newTestSchedulerBus(t)
	queue := NewPriorityQueue()
	metrics := &Metrics{}

	adapter := NewMockAdapter(10*time.Millisecond, 1.0)
	scheduler := NewScheduler(
		queue,
		func() plugin.CommandAdapter { return adapter },
		bus,
		metrics,
		nil,
		SchedulerConfig{MaxConcurrent: 1, Timeout: 5 * time.Second, MaxRetries: 0, RetryBackoff: 100 * time.Millisecond},
	)
	scheduler.Start()
	defer scheduler.Stop()

	item := &CommandItem{
		Command: plugin.Command{
			CommandID:   "ctx-test",
			SatelliteID: "sat-ctx",
			CommandType: "test",
		},
		Priority:   5,
		Status:     StatusQueued,
		EnqueuedAt: time.Now(),
	}
	require.NoError(t, queue.Enqueue(item))

	require.Eventually(t, func() bool {
		item, ok := queue.Get("ctx-test")
		return ok && item.Status == StatusAcked
	}, 5*time.Second, 50*time.Millisecond, "指令应成功 acked")

	// 确认没有超时计数
	assert.Equal(t, int64(0), metrics.TotalTimeout.Load())
}
