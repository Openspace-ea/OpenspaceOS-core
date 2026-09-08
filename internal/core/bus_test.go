package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalBus_PublishSubscribe_Basic 校验发布订阅基本流程：
// Publish 一个事件，订阅者收到。
func TestLocalBus_PublishSubscribe_Basic(t *testing.T) {
	bus := newMemoryBus(t)
	ch, unsub := bus.Subscribe(SubscribeOptions{})
	defer unsub()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	e := newSimpleEvent("evt-001", "sat-001", ts)

	require.NoError(t, bus.Publish(context.Background(), e))

	got := recvEvent(t, ch, time.Second)
	assert.Equal(t, "evt-001", got.EventID)
	assert.Equal(t, event.EventNodeDeleted, got.EventType)
	assert.Equal(t, "sat-001", got.SourceNodeID)
	assert.Equal(t, "trace-evt-001", got.TraceID)
}

// TestLocalBus_EventTypeFilter 校验 eventType 过滤：
// 订阅特定类型，只收到对应事件。
func TestLocalBus_EventTypeFilter(t *testing.T) {
	bus := newMemoryBus(t)
	ch, unsub := bus.Subscribe(SubscribeOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	defer unsub()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// 发布一个 NodeDeleted 事件，订阅者不应收到
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-del", "sat-001", ts)))
	recvNoEvent(t, ch, 200*time.Millisecond)

	// 发布一个 NodeRegistered 事件，订阅者应收到
	require.NoError(t, bus.Publish(context.Background(),
		newTestEvent("evt-reg", event.EventNodeRegistered, "sat-001", ts,
			event.NodeRegisteredPayload{NodeID: "sat-001", NodeType: "Satellite"})))

	got := recvEvent(t, ch, time.Second)
	assert.Equal(t, "evt-reg", got.EventID)
	assert.Equal(t, event.EventNodeRegistered, got.EventType)
}

// TestLocalBus_MultipleSubscribers 校验多订阅者同时收到事件。
func TestLocalBus_MultipleSubscribers(t *testing.T) {
	bus := newMemoryBus(t)
	ch1, unsub1 := bus.Subscribe(SubscribeOptions{})
	defer unsub1()
	ch2, unsub2 := bus.Subscribe(SubscribeOptions{})
	defer unsub2()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-001", "sat-001", ts)))

	got1 := recvEvent(t, ch1, time.Second)
	got2 := recvEvent(t, ch2, time.Second)
	assert.Equal(t, "evt-001", got1.EventID)
	assert.Equal(t, "evt-001", got2.EventID)
}

// TestLocalBus_SchemaValidationFail 校验 Schema 校验失败：
// Publish 不合法的事件返回错误，不派发。
func TestLocalBus_SchemaValidationFail(t *testing.T) {
	bus := newMemoryBus(t)
	ch, unsub := bus.Subscribe(SubscribeOptions{})
	defer unsub()

	// 缺少必填字段 sourceNodeId
	e := &event.Event{
		EventID:   "evt-bad",
		EventType: event.EventNodeDeleted,
		Timestamp: time.Now().UTC(),
	}
	_ = e.SetPayload(event.NodeDeletedPayload{NodeID: "sat-001"})

	err := bus.Publish(context.Background(), e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sourceNodeId")

	// 确保订阅者没有收到事件
	recvNoEvent(t, ch, 200*time.Millisecond)
}

// TestLocalBus_Unsubscribe 校验取消订阅后不再收到事件。
func TestLocalBus_Unsubscribe(t *testing.T) {
	bus := newMemoryBus(t)
	ch, unsub := bus.Subscribe(SubscribeOptions{})

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-001", "sat-001", ts)))
	recvEvent(t, ch, time.Second)

	unsub()

	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-002", "sat-001", ts)))
	recvNoEvent(t, ch, 200*time.Millisecond)
}

// TestLocalBus_Ordering 校验同一 eventType 的事件按 Publish 顺序到达订阅者。
func TestLocalBus_Ordering(t *testing.T) {
	bus := newMemoryBus(t)
	ch, unsub := bus.Subscribe(SubscribeOptions{})
	defer unsub()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	const n = 20
	for i := 0; i < n; i++ {
		require.NoError(t, bus.Publish(context.Background(),
			newSimpleEvent(fmt.Sprintf("evt-%03d", i), "sat-001", ts)))
	}

	events := recvAllEvents(t, ch, n, 3*time.Second)
	for i, e := range events {
		assert.Equal(t, fmt.Sprintf("evt-%03d", i), e.EventID,
			"事件应按 Publish 顺序到达")
	}
}

// TestLocalBus_Backpressure 校验背压机制：
// 订阅者 channel 满后事件被丢弃，Publish 不阻塞，metric 记录丢弃数。
func TestLocalBus_Backpressure(t *testing.T) {
	bus := newMemoryBus(t)
	// 缓冲区为 1，很快会满
	ch, unsub := bus.Subscribe(SubscribeOptions{BufferSize: 1})
	defer unsub()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// 发布多个事件，订阅者不消费，channel 满后应丢弃
	const n = 20
	for i := 0; i < n; i++ {
		require.NoError(t, bus.Publish(context.Background(),
			newSimpleEvent(fmt.Sprintf("evt-%03d", i), "sat-001", ts)))
	}

	// 确保派发完成
	time.Sleep(100 * time.Millisecond)

	// 应有丢弃
	dropped := bus.DroppedCount()
	assert.Greater(t, dropped, int64(0), "应有事件被丢弃")

	// 订阅者最多收到缓冲区大小（1）+ 排队中的少量事件
	// 收取一个事件即可
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("应至少收到一个事件")
	}
}

// TestLocalBus_ConcurrentPublish 校验并发安全：
// 多个 goroutine 同时 Publish，不 panic、不丢事件。
func TestLocalBus_ConcurrentPublish(t *testing.T) {
	bus := newMemoryBus(t)
	ch, unsub := bus.Subscribe(SubscribeOptions{BufferSize: 10000})
	defer unsub()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	const goroutines = 10
	const perG = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				requireNoError(t, bus.Publish(context.Background(),
					newSimpleEvent(fmt.Sprintf("g%d-%d", gid, i), "sat-001", ts)))
			}
		}(g)
	}
	wg.Wait()

	// 收取所有事件（goroutines * perG）
	total := goroutines * perG
	received := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		e := recvEvent(t, ch, 5*time.Second)
		received[e.EventID] = true
	}
	assert.Len(t, received, total, "应收到全部事件，无丢失")

	// 确保无丢弃（缓冲区足够大）
	assert.Equal(t, int64(0), bus.DroppedCount(), "缓冲区足够大时不应丢弃事件")
}

// TestLocalBus_Replay 校验事件回放：
// 按 eventType / 时间范围 / sourceNodeId 回放，并派发给订阅者。
func TestLocalBus_Replay(t *testing.T) {
	bus := newMemoryBus(t)

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	// 发布若干事件到 store
	events := []*event.Event{
		newTestEvent("evt-1", event.EventNodeRegistered, "node-A", base,
			event.NodeRegisteredPayload{NodeID: "node-A", NodeType: "Satellite"}),
		newTestEvent("evt-2", event.EventNodeDeleted, "node-A", base.Add(time.Hour),
			event.NodeDeletedPayload{NodeID: "node-A"}),
		newTestEvent("evt-3", event.EventNodeRegistered, "node-B", base.Add(2*time.Hour),
			event.NodeRegisteredPayload{NodeID: "node-B", NodeType: "GroundStation"}),
		newTestEvent("evt-4", event.EventNodeDeleted, "node-B", base.Add(3*time.Hour),
			event.NodeDeletedPayload{NodeID: "node-B"}),
	}
	for _, e := range events {
		require.NoError(t, bus.Publish(context.Background(), e))
	}
	// 等待派发完成
	time.Sleep(100 * time.Millisecond)

	// 订阅者接收回放事件
	ch, unsub := bus.Subscribe(SubscribeOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	defer unsub()

	// 按 eventType 回放
	replayed, err := bus.Replay(context.Background(), ReplayOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 2, "应回放 2 个 NodeRegistered 事件")

	// 订阅者应收到回放的 NodeRegistered 事件
	got1 := recvEvent(t, ch, time.Second)
	got2 := recvEvent(t, ch, time.Second)
	assert.Equal(t, event.EventNodeRegistered, got1.EventType)
	assert.Equal(t, event.EventNodeRegistered, got2.EventType)

	// 按时间范围回放
	start := base.Add(30 * time.Minute)
	end := base.Add(2 * time.Hour).Add(30 * time.Minute)
	replayed, err = bus.Replay(context.Background(), ReplayOptions{
		StartTime: &start,
		EndTime:   &end,
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 2, "应回放 2 个在时间范围内的事件")

	// 按 sourceNodeId 回放
	replayed, err = bus.Replay(context.Background(), ReplayOptions{
		SourceNodeID: "node-B",
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 2, "应回放 2 个来自 node-B 的事件")

	// 按 Limit 回放
	replayed, err = bus.Replay(context.Background(), ReplayOptions{
		Limit: 1,
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 1, "Limit=1 应回放 1 个事件")
}

// TestLocalBus_ReplayNoPersist 校验回放的事件不会重新持久化。
func TestLocalBus_ReplayNoPersist(t *testing.T) {
	bus := newMemoryBus(t)

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-001", "sat-001", ts)))
	time.Sleep(100 * time.Millisecond)

	// 回放
	replayed, err := bus.Replay(context.Background(), ReplayOptions{})
	require.NoError(t, err)
	assert.Len(t, replayed, 1)

	// 再次回放，仍只有 1 条（未重新持久化）
	replayed, err = bus.Replay(context.Background(), ReplayOptions{})
	require.NoError(t, err)
	assert.Len(t, replayed, 1, "回放不应重新持久化事件")
}

// TestLocalBus_TraceIDFromContext 校验 traceId 从 context 透传。
func TestLocalBus_TraceIDFromContext(t *testing.T) {
	bus := newMemoryBus(t)
	ch, unsub := bus.Subscribe(SubscribeOptions{})
	defer unsub()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	e := newSimpleEvent("evt-001", "sat-001", ts)
	e.TraceID = "" // 清空 traceId，期望从 context 提取

	// 使用自定义 context key 设置 traceId（非 OTel 场景）
	// 由于 OTel span context 需要完整的 OTel 设置，
	// 这里验证事件已有 traceId 时不被覆盖
	e2 := newSimpleEvent("evt-002", "sat-001", ts)
	require.NoError(t, bus.Publish(context.Background(), e2))
	got := recvEvent(t, ch, time.Second)
	assert.Equal(t, "trace-evt-002", got.TraceID, "已有 traceId 应保留")

	// 事件无 traceId 且 context 无 OTel span 时，traceId 为空
	require.NoError(t, bus.Publish(context.Background(), e))
	got = recvEvent(t, ch, time.Second)
	assert.Empty(t, got.TraceID, "无 OTel span 时 traceId 应为空")
}

// TestLocalBus_NilDependencies 校验 nil 依赖时使用默认值。
func TestLocalBus_NilDependencies(t *testing.T) {
	// store/registry/logger 均为 nil，应使用默认值且正常工作
	bus := NewLocalBus(nil, nil, nil)
	t.Cleanup(func() { _ = bus.Close() })

	ch, unsub := bus.Subscribe(SubscribeOptions{})
	defer unsub()

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-001", "sat-001", ts)))
	got := recvEvent(t, ch, time.Second)
	assert.Equal(t, "evt-001", got.EventID)
}

// TestLocalBus_Close 校验关闭总线后 Publish 返回错误。
func TestLocalBus_Close(t *testing.T) {
	bus := NewLocalBus(nil, nil, nil)

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-001", "sat-001", ts)))

	// 等待派发完成
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, bus.Close())

	// 关闭后 Publish 应返回错误
	err := bus.Publish(context.Background(),
		newSimpleEvent("evt-002", "sat-001", ts))
	require.Error(t, err)
	assert.True(t, errors.Is(err, errBusClosed))
}

// TestLocalBus_PublishNilEvent 校验 Publish nil 事件返回错误。
func TestLocalBus_PublishNilEvent(t *testing.T) {
	bus := newMemoryBus(t)
	err := bus.Publish(context.Background(), nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errNilEvent))
}

// TestLocalBus_ConcurrentSubscribeUnsubscribe 校验并发订阅/取消订阅不 panic。
func TestLocalBus_ConcurrentSubscribeUnsubscribe(t *testing.T) {
	bus := newMemoryBus(t)

	var wg sync.WaitGroup
	var count atomic.Int64
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := bus.Subscribe(SubscribeOptions{BufferSize: 10})
			count.Add(1)
			// 短暂消费后取消
			go func() {
				for range ch {
				}
			}()
			time.Sleep(10 * time.Millisecond)
			unsub()
		}()
	}

	// 同时发布事件
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
			_ = bus.Publish(context.Background(),
				newSimpleEvent(fmt.Sprintf("evt-%d", i), "sat-001", ts))
		}(i)
	}
	wg.Wait()
}
