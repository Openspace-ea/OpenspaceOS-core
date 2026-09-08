package core

import (
	"testing"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/stretchr/testify/require"
)

// newTestEvent 构造一个合法的测试事件。
//
// payload 应为事件类型对应的 Payload 结构体；若为 nil 则使用 NodeDeletedPayload。
func newTestEvent(id string, et event.EventType, source string, ts time.Time, payload any) *event.Event {
	e := &event.Event{
		EventID:      id,
		EventType:    et,
		Timestamp:    ts,
		SourceNodeID: source,
		TraceID:      "trace-" + id,
	}
	if payload == nil {
		payload = event.NodeDeletedPayload{NodeID: source}
	}
	_ = e.SetPayload(payload)
	return e
}

// newSimpleEvent 构造一个 NodeDeleted 类型的简单测试事件。
func newSimpleEvent(id, source string, ts time.Time) *event.Event {
	return newTestEvent(id, event.EventNodeDeleted, source, ts, event.NodeDeletedPayload{NodeID: source})
}

// recvEvent 从 channel 接收一个事件，超时则 fatal。
func recvEvent(t *testing.T, ch <-chan *event.Event, timeout time.Duration) *event.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(timeout):
		t.Fatal("接收事件超时")
		return nil
	}
}

// recvNoEvent 断言在 timeout 内不会收到事件。
func recvNoEvent(t *testing.T, ch <-chan *event.Event, timeout time.Duration) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("不应收到事件，但收到: eventId=%s eventType=%s", e.EventID, e.EventType)
	case <-time.After(timeout):
		// 预期超时，正常
	}
}

// recvAllEvents 从 channel 接收 n 个事件，超时则 fatal。
func recvAllEvents(t *testing.T, ch <-chan *event.Event, n int, timeout time.Duration) []*event.Event {
	t.Helper()
	result := make([]*event.Event, 0, n)
	for i := 0; i < n; i++ {
		select {
		case e := <-ch:
			result = append(result, e)
		case <-time.After(timeout):
			t.Fatalf("接收第 %d 个事件超时（已收到 %d 个）", i+1, len(result))
		}
	}
	return result
}

// newMemoryBus 创建使用内存 EventStore 的 LocalBus（用于测试）。
func newMemoryBus(t *testing.T) *LocalBus {
	t.Helper()
	bus := NewLocalBus(nil, nil, nil)
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// requireNoError 断言无错误。
func requireNoError(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}
