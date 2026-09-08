package core

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== 内存 EventStore 测试 =====

// TestMemoryEventStore_AppendQuery 校验内存 EventStore 的 Append 与 Query 基本流程。
func TestMemoryEventStore_AppendQuery(t *testing.T) {
	store := NewMemoryEventStore(StoreConfig{})
	t.Cleanup(func() { _ = store.Close() })

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	e := newSimpleEvent("evt-001", "sat-001", ts)

	require.NoError(t, store.Append(context.Background(), e))

	got, err := store.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "evt-001", got[0].EventID)
	assert.Equal(t, event.EventNodeDeleted, got[0].EventType)
	assert.Equal(t, "sat-001", got[0].SourceNodeID)
}

// TestMemoryEventStore_QueryFilters 校验内存 EventStore 的查询过滤条件。
func TestMemoryEventStore_QueryFilters(t *testing.T) {
	store := NewMemoryEventStore(StoreConfig{})
	t.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	events := []*event.Event{
		newTestEvent("e1", event.EventNodeRegistered, "node-A", base,
			event.NodeRegisteredPayload{NodeID: "node-A", NodeType: "Satellite"}),
		newTestEvent("e2", event.EventNodeDeleted, "node-A", base.Add(time.Hour),
			event.NodeDeletedPayload{NodeID: "node-A"}),
		newTestEvent("e3", event.EventNodeRegistered, "node-B", base.Add(2*time.Hour),
			event.NodeRegisteredPayload{NodeID: "node-B", NodeType: "GroundStation"}),
		newTestEvent("e4", event.EventNodeDeleted, "node-B", base.Add(3*time.Hour),
			event.NodeDeletedPayload{NodeID: "node-B"}),
	}
	for _, e := range events {
		require.NoError(t, store.Append(context.Background(), e))
	}

	// 按 eventType 过滤
	got, err := store.Query(context.Background(), QueryOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按 sourceNodeId 过滤
	got, err = store.Query(context.Background(), QueryOptions{
		SourceNodeID: "node-B",
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按时间范围过滤
	start := base.Add(30 * time.Minute)
	end := base.Add(2 * time.Hour).Add(30 * time.Minute)
	got, err = store.Query(context.Background(), QueryOptions{
		StartTime: &start,
		EndTime:   &end,
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按 Limit 过滤
	got, err = store.Query(context.Background(), QueryOptions{
		Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按 Offset 分页
	got, err = store.Query(context.Background(), QueryOptions{
		Limit:  2,
		Offset: 2,
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "e3", got[0].EventID)
	assert.Equal(t, "e4", got[1].EventID)
}

// TestMemoryEventStore_TTLCleanup 校验内存 EventStore 的 TTL 清理。
func TestMemoryEventStore_TTLCleanup(t *testing.T) {
	fakeNow := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	store := NewMemoryEventStore(StoreConfig{
		TTL: time.Hour,
		Now: func() time.Time { return fakeNow },
	})
	t.Cleanup(func() { _ = store.Close() })

	// 在 t=0 追加事件（createdAt = t=0）
	require.NoError(t, store.Append(context.Background(),
		newSimpleEvent("old-1", "sat-001", fakeNow)))
	require.NoError(t, store.Append(context.Background(),
		newSimpleEvent("old-2", "sat-001", fakeNow)))
	assert.Equal(t, 2, store.Len())

	// 时间推进 2 小时（超过 TTL=1h），旧事件应被清理
	fakeNow = fakeNow.Add(2 * time.Hour)
	// 追加新事件（createdAt = t=2h，未过期）
	require.NoError(t, store.Append(context.Background(),
		newSimpleEvent("new-1", "sat-001", fakeNow)))

	deleted, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "应清理 2 个过期事件")
	assert.Equal(t, 1, store.Len(), "应保留 1 个未过期事件")
}

// TestMemoryEventStore_MaxEventsCleanup 校验内存 EventStore 的数量上限清理。
func TestMemoryEventStore_MaxEventsCleanup(t *testing.T) {
	store := NewMemoryEventStore(StoreConfig{
		MaxEvents: 3,
	})
	t.Cleanup(func() { _ = store.Close() })

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		require.NoError(t, store.Append(context.Background(),
			newSimpleEvent(
				"evt-"+string(rune('a'+i)),
				"sat-001",
				ts.Add(time.Duration(i)*time.Second)),
		))
	}

	deleted, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "应清理 2 个超额事件")
	assert.Equal(t, 3, store.Len(), "应保留最新 3 个事件")

	// 确认保留的是最新的 3 个（按 Append 顺序，后追加的保留）
	got, err := store.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	assert.Len(t, got, 3)
	// 最旧的两个被清理
	ids := []string{got[0].EventID, got[1].EventID, got[2].EventID}
	assert.NotContains(t, ids, "evt-a")
	assert.NotContains(t, ids, "evt-b")
}

// TestMemoryEventStore_ConcurrentAppend 校验内存 EventStore 并发追加的安全性。
func TestMemoryEventStore_ConcurrentAppend(t *testing.T) {
	store := NewMemoryEventStore(StoreConfig{})
	t.Cleanup(func() { _ = store.Close() })

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	const goroutines = 10
	const perG = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				require.NoError(t, store.Append(context.Background(),
					newSimpleEvent(
						"g"+string(rune('a'+gid%26))+"-"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26)),
						"sat-001", ts)))
			}
		}(g)
	}
	wg.Wait()

	got, err := store.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	assert.Len(t, got, goroutines*perG, "应全部追加成功")
}

// ===== SQLite EventStore 测试 =====

// newSQLiteStore 创建临时 SQLite EventStore 用于测试。
func newSQLiteStore(t *testing.T, cfg StoreConfig) *SQLiteEventStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_events.db")
	store, err := NewSQLiteEventStore(context.Background(), dbPath, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestSQLiteEventStore_AppendQuery 校验 SQLite EventStore 的 Append 与 Query 基本流程。
func TestSQLiteEventStore_AppendQuery(t *testing.T) {
	store := newSQLiteStore(t, StoreConfig{})

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	e := newSimpleEvent("evt-001", "sat-001", ts)

	require.NoError(t, store.Append(context.Background(), e))

	got, err := store.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "evt-001", got[0].EventID)
	assert.Equal(t, event.EventNodeDeleted, got[0].EventType)
	assert.Equal(t, "sat-001", got[0].SourceNodeID)
	// 验证 Payload 正确反序列化
	assert.Equal(t, "sat-001", got[0].Payload["nodeId"])
}

// TestSQLiteEventStore_QueryFilters 校验 SQLite EventStore 的查询过滤条件。
func TestSQLiteEventStore_QueryFilters(t *testing.T) {
	store := newSQLiteStore(t, StoreConfig{})

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	events := []*event.Event{
		newTestEvent("e1", event.EventNodeRegistered, "node-A", base,
			event.NodeRegisteredPayload{NodeID: "node-A", NodeType: "Satellite"}),
		newTestEvent("e2", event.EventNodeDeleted, "node-A", base.Add(time.Hour),
			event.NodeDeletedPayload{NodeID: "node-A"}),
		newTestEvent("e3", event.EventNodeRegistered, "node-B", base.Add(2*time.Hour),
			event.NodeRegisteredPayload{NodeID: "node-B", NodeType: "GroundStation"}),
		newTestEvent("e4", event.EventNodeDeleted, "node-B", base.Add(3*time.Hour),
			event.NodeDeletedPayload{NodeID: "node-B"}),
	}
	for _, e := range events {
		require.NoError(t, store.Append(context.Background(), e))
	}

	// 按 eventType 过滤
	got, err := store.Query(context.Background(), QueryOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按 sourceNodeId 过滤
	got, err = store.Query(context.Background(), QueryOptions{
		SourceNodeID: "node-B",
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按时间范围过滤
	start := base.Add(30 * time.Minute)
	end := base.Add(2 * time.Hour).Add(30 * time.Minute)
	got, err = store.Query(context.Background(), QueryOptions{
		StartTime: &start,
		EndTime:   &end,
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按 Limit 过滤
	got, err = store.Query(context.Background(), QueryOptions{
		Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// 按 Offset 分页
	got, err = store.Query(context.Background(), QueryOptions{
		Limit:  2,
		Offset: 2,
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "e3", got[0].EventID)
	assert.Equal(t, "e4", got[1].EventID)
}

// TestSQLiteEventStore_RestartRecovery 校验重启恢复：
// 关闭后重新创建（复用同一个 SQLite 文件），历史事件可查询。
func TestSQLiteEventStore_RestartRecovery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "restart.db")

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// 第一次创建 store，写入事件
	store1, err := NewSQLiteEventStore(context.Background(), dbPath, StoreConfig{})
	require.NoError(t, err)
	require.NoError(t, store1.Append(context.Background(),
		newSimpleEvent("evt-001", "sat-001", ts)))
	require.NoError(t, store1.Append(context.Background(),
		newSimpleEvent("evt-002", "sat-001", ts.Add(time.Hour))))
	require.NoError(t, store1.Close())

	// 第二次创建 store，复用同一个 db 文件
	store2, err := NewSQLiteEventStore(context.Background(), dbPath, StoreConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store2.Close() })

	got, err := store2.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	require.Len(t, got, 2, "重启后历史事件应可查询")
	assert.Equal(t, "evt-001", got[0].EventID)
	assert.Equal(t, "evt-002", got[1].EventID)
}

// TestSQLiteEventStore_TTLCleanup 校验 SQLite EventStore 的 TTL 清理。
func TestSQLiteEventStore_TTLCleanup(t *testing.T) {
	fakeNow := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	store := newSQLiteStore(t, StoreConfig{
		TTL: time.Hour,
		Now: func() time.Time { return fakeNow },
	})

	// 在 t=0 追加事件（createdAt = t=0）
	require.NoError(t, store.Append(context.Background(),
		newSimpleEvent("old-1", "sat-001", fakeNow)))
	require.NoError(t, store.Append(context.Background(),
		newSimpleEvent("old-2", "sat-001", fakeNow)))

	// 时间推进 2 小时（超过 TTL=1h）
	fakeNow = fakeNow.Add(2 * time.Hour)
	// 追加新事件（createdAt = t=2h，未过期）
	require.NoError(t, store.Append(context.Background(),
		newSimpleEvent("new-1", "sat-001", fakeNow)))

	deleted, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "应清理 2 个过期事件")

	got, err := store.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	assert.Len(t, got, 1, "应保留 1 个未过期事件")
	assert.Equal(t, "new-1", got[0].EventID)
}

// TestSQLiteEventStore_MaxEventsCleanup 校验 SQLite EventStore 的数量上限清理。
func TestSQLiteEventStore_MaxEventsCleanup(t *testing.T) {
	store := newSQLiteStore(t, StoreConfig{
		MaxEvents: 3,
	})

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		require.NoError(t, store.Append(context.Background(),
			newSimpleEvent(
				"evt-"+string(rune('a'+i)),
				"sat-001",
				ts.Add(time.Duration(i)*time.Second))))
	}

	deleted, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "应清理 2 个超额事件")

	got, err := store.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	assert.Len(t, got, 3, "应保留最新 3 个事件")
	ids := []string{got[0].EventID, got[1].EventID, got[2].EventID}
	assert.NotContains(t, ids, "evt-a")
	assert.NotContains(t, ids, "evt-b")
}

// TestSQLiteEventStore_PayloadRoundTrip 校验 SQLite EventStore 中 Payload 的序列化往返。
func TestSQLiteEventStore_PayloadRoundTrip(t *testing.T) {
	store := newSQLiteStore(t, StoreConfig{})

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	e := newTestEvent("evt-001", event.EventTelemetryReceived, "sat-001", ts,
		event.TelemetryReceivedPayload{
			SatelliteID: "sat-001",
			Timestamp:   ts,
			Parameters:  map[string]any{"temp": 42.5, "voltage": 28.1},
			Quality:     "good",
		})

	require.NoError(t, store.Append(context.Background(), e))

	got, err := store.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	require.Len(t, got, 1)

	// 验证 Payload 可正确反序列化回强类型结构体
	var payload event.TelemetryReceivedPayload
	require.NoError(t, got[0].GetPayload(&payload))
	assert.Equal(t, "sat-001", payload.SatelliteID)
	assert.Equal(t, "good", payload.Quality)
	assert.Equal(t, 42.5, payload.Parameters["temp"])
}

// TestLocalBus_WithSQLiteStore 校验 LocalBus 使用 SQLite EventStore 的完整流程。
func TestLocalBus_WithSQLiteStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bus.db")

	store, err := NewSQLiteEventStore(context.Background(), dbPath, StoreConfig{})
	require.NoError(t, err)

	bus := NewLocalBus(store, nil, nil)

	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-001", "sat-001", ts)))
	require.NoError(t, bus.Publish(context.Background(),
		newSimpleEvent("evt-002", "sat-001", ts.Add(time.Hour))))

	// 等待派发完成
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, bus.Close())

	// 重新打开 store 验证持久化
	store2, err := NewSQLiteEventStore(context.Background(), dbPath, StoreConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store2.Close() })

	got, err := store2.Query(context.Background(), QueryOptions{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "evt-001", got[0].EventID)
	assert.Equal(t, "evt-002", got[1].EventID)
}

// TestEventStore_AppendNil 校验 Append nil 事件返回错误。
func TestEventStore_AppendNil(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		store := NewMemoryEventStore(StoreConfig{})
		t.Cleanup(func() { _ = store.Close() })
		err := store.Append(context.Background(), nil)
		require.Error(t, err)
	})

	t.Run("sqlite", func(t *testing.T) {
		store := newSQLiteStore(t, StoreConfig{})
		err := store.Append(context.Background(), nil)
		require.Error(t, err)
	})
}
