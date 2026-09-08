package command

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// makeItem 创建测试用 CommandItem。
func makeItem(id string, priority int, enqueuedAt time.Time) *CommandItem {
	return &CommandItem{
		Command: plugin.Command{
			CommandID:   id,
			SatelliteID: "sat-1",
			CommandType: "test",
		},
		Priority:   priority,
		Status:     StatusQueued,
		EnqueuedAt: enqueuedAt,
	}
}

// TestQueueEnqueueDequeue 测试入队后按优先级出队。
func TestQueueEnqueueDequeue(t *testing.T) {
	q := NewPriorityQueue()

	// 低优先级先入队
	t0 := time.Now()
	require.NoError(t, q.Enqueue(makeItem("cmd-low", 1, t0)))
	require.NoError(t, q.Enqueue(makeItem("cmd-high", 10, t0.Add(1*time.Millisecond))))
	require.NoError(t, q.Enqueue(makeItem("cmd-mid", 5, t0.Add(2*time.Millisecond))))

	// 高优先级应先出队
	item := q.Dequeue()
	require.NotNil(t, item)
	assert.Equal(t, "cmd-high", item.Command.CommandID)
	assert.Equal(t, StatusSending, item.Status)

	// 中优先级其次
	item = q.Dequeue()
	require.NotNil(t, item)
	assert.Equal(t, "cmd-mid", item.Command.CommandID)

	// 低优先级最后
	item = q.Dequeue()
	require.NotNil(t, item)
	assert.Equal(t, "cmd-low", item.Command.CommandID)

	// 队列空
	item = q.Dequeue()
	assert.Nil(t, item)
}

// TestQueueFIFOSamePriority 测试同优先级按入队时间 FIFO。
func TestQueueFIFOSamePriority(t *testing.T) {
	q := NewPriorityQueue()
	t0 := time.Now()

	// 同优先级，按入队时间先后
	require.NoError(t, q.Enqueue(makeItem("first", 5, t0)))
	require.NoError(t, q.Enqueue(makeItem("second", 5, t0.Add(1*time.Millisecond))))
	require.NoError(t, q.Enqueue(makeItem("third", 5, t0.Add(2*time.Millisecond))))

	item := q.Dequeue()
	require.NotNil(t, item)
	assert.Equal(t, "first", item.Command.CommandID)

	item = q.Dequeue()
	require.NotNil(t, item)
	assert.Equal(t, "second", item.Command.CommandID)

	item = q.Dequeue()
	require.NotNil(t, item)
	assert.Equal(t, "third", item.Command.CommandID)
}

// TestQueueGet 测试按 commandID 查询。
func TestQueueGet(t *testing.T) {
	q := NewPriorityQueue()
	require.NoError(t, q.Enqueue(makeItem("cmd-1", 5, time.Now())))

	item, ok := q.Get("cmd-1")
	require.True(t, ok)
	assert.Equal(t, "cmd-1", item.Command.CommandID)

	_, ok = q.Get("nonexistent")
	assert.False(t, ok)
}

// TestQueueUpdate 测试更新指令状态。
func TestQueueUpdate(t *testing.T) {
	q := NewPriorityQueue()
	require.NoError(t, q.Enqueue(makeItem("cmd-1", 5, time.Now())))

	item, ok := q.Get("cmd-1")
	require.True(t, ok)
	item.Status = StatusAcked
	item.Error = "test error"
	q.Update(item)

	updated, ok := q.Get("cmd-1")
	require.True(t, ok)
	assert.Equal(t, StatusAcked, updated.Status)
	assert.Equal(t, "test error", updated.Error)
}

// TestQueueRemove 测试移除指令。
func TestQueueRemove(t *testing.T) {
	q := NewPriorityQueue()
	require.NoError(t, q.Enqueue(makeItem("cmd-1", 5, time.Now())))
	assert.Equal(t, 1, q.Len())

	q.Remove("cmd-1")
	assert.Equal(t, 0, q.Len())

	_, ok := q.Get("cmd-1")
	assert.False(t, ok)
}

// TestQueueLen 测试队列长度。
func TestQueueLen(t *testing.T) {
	q := NewPriorityQueue()
	assert.Equal(t, 0, q.Len())

	require.NoError(t, q.Enqueue(makeItem("cmd-1", 5, time.Now())))
	require.NoError(t, q.Enqueue(makeItem("cmd-2", 5, time.Now())))
	assert.Equal(t, 2, q.Len())

	q.Remove("cmd-1")
	assert.Equal(t, 1, q.Len())
}

// TestQueueList 测试列出所有指令。
func TestQueueList(t *testing.T) {
	q := NewPriorityQueue()
	t0 := time.Now()

	require.NoError(t, q.Enqueue(makeItem("cmd-3", 1, t0.Add(2*time.Millisecond))))
	require.NoError(t, q.Enqueue(makeItem("cmd-1", 1, t0)))
	require.NoError(t, q.Enqueue(makeItem("cmd-2", 1, t0.Add(1*time.Millisecond))))

	list := q.List()
	require.Len(t, list, 3)
	// List 按入队时间升序
	assert.Equal(t, "cmd-1", list[0].Command.CommandID)
	assert.Equal(t, "cmd-2", list[1].Command.CommandID)
	assert.Equal(t, "cmd-3", list[2].Command.CommandID)
}

// TestQueueListByStatus 测试按状态过滤。
func TestQueueListByStatus(t *testing.T) {
	q := NewPriorityQueue()
	require.NoError(t, q.Enqueue(makeItem("cmd-1", 5, time.Now())))
	require.NoError(t, q.Enqueue(makeItem("cmd-2", 5, time.Now().Add(1*time.Millisecond))))

	// 将 cmd-1 标记为 acked
	item, _ := q.Get("cmd-1")
	item.Status = StatusAcked
	q.Update(item)

	queued := q.ListByStatus(StatusQueued)
	require.Len(t, queued, 1)
	assert.Equal(t, "cmd-2", queued[0].Command.CommandID)

	acked := q.ListByStatus(StatusAcked)
	require.Len(t, acked, 1)
	assert.Equal(t, "cmd-1", acked[0].Command.CommandID)
}

// TestQueueEnqueueDuplicate 测试重复 commandID 入队返回错误。
func TestQueueEnqueueDuplicate(t *testing.T) {
	q := NewPriorityQueue()
	require.NoError(t, q.Enqueue(makeItem("cmd-1", 5, time.Now())))

	err := q.Enqueue(makeItem("cmd-1", 5, time.Now()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已存在")
}

// TestQueueEnqueueEmptyID 测试空 commandID 入队返回错误。
func TestQueueEnqueueEmptyID(t *testing.T) {
	q := NewPriorityQueue()
	item := &CommandItem{
		Command: plugin.Command{CommandID: ""},
	}
	err := q.Enqueue(item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commandId")
}

// TestQueueEnqueueNil 测试 nil 入队返回错误。
func TestQueueEnqueueNil(t *testing.T) {
	q := NewPriorityQueue()
	err := q.Enqueue(nil)
	require.Error(t, err)
}

// TestQueueDequeueSkipsNonQueued 测试 Dequeue 跳过非 queued 状态的指令。
func TestQueueDequeueSkipsNonQueued(t *testing.T) {
	q := NewPriorityQueue()
	t0 := time.Now()

	// 高优先级但已发送
	item1 := makeItem("cmd-sent", 10, t0)
	item1.Status = StatusSending
	require.NoError(t, q.Enqueue(item1))

	// 低优先级但 queued
	require.NoError(t, q.Enqueue(makeItem("cmd-queued", 1, t0.Add(1*time.Millisecond))))

	// Dequeue 应返回低优先级的 queued 项
	deq := q.Dequeue()
	require.NotNil(t, deq)
	assert.Equal(t, "cmd-queued", deq.Command.CommandID)
}

// TestQueuePendingCount 测试待发送指令计数。
func TestQueuePendingCount(t *testing.T) {
	q := NewPriorityQueue()
	require.NoError(t, q.Enqueue(makeItem("cmd-1", 5, time.Now())))
	require.NoError(t, q.Enqueue(makeItem("cmd-2", 5, time.Now())))
	assert.Equal(t, 2, q.PendingCount())

	// 取出一个
	q.Dequeue()
	assert.Equal(t, 1, q.PendingCount())
}
