package usage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/storage"

	// 匿名导入 modernc.org/sqlite 驱动
	_ "modernc.org/sqlite"
)

// newTestStore 创建基于内存 SQLite 的 usage store（含表结构迁移）。
func newTestStore(t *testing.T) *SQLStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mig := storage.NewMigrator(db, storage.WithMigrations(storage.DefaultMigrations()))
	require.NoError(t, mig.Migrate(context.Background()))

	return NewSQLStore(db)
}

// TestStore_InsertAndAggregate 验证写入后按租户聚合查询。
func TestStore_InsertAndAggregate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	buckets := []Bucket{
		{TenantID: "comm-a", ClientID: "c1", Endpoint: "/api/v1/nodes", Operation: "create", Unit: UnitCall, Count: 5, BucketStart: now, BucketEnd: now.Add(time.Minute)},
		{TenantID: "comm-a", ClientID: "c1", Endpoint: "/api/v1/nodes", Operation: "create", Unit: UnitCall, Count: 3, BucketStart: now.Add(time.Minute), BucketEnd: now.Add(2 * time.Minute)},
		{TenantID: "comm-b", ClientID: "c2", Endpoint: "/api/v1/nodes", Operation: "read", Unit: UnitCall, Count: 2, BucketStart: now, BucketEnd: now.Add(time.Minute)},
	}
	n, err := store.InsertBatch(ctx, buckets)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)

	// 查询 comm-a 全部用量，应聚合成一条（8）
	agg, err := store.AggregateByTenant(ctx, Query{TenantID: "comm-a", Start: now.Add(-time.Hour), End: now.Add(2 * time.Hour)})
	require.NoError(t, err)
	require.Len(t, agg, 1)
	assert.Equal(t, int64(8), agg[0].TotalCount)
	assert.Equal(t, "/api/v1/nodes", agg[0].Endpoint)
}

// TestStore_InsertEmpty 验证空批量写入返回 0。
func TestStore_InsertEmpty(t *testing.T) {
	store := newTestStore(t)
	n, err := store.InsertBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// TestCollector_AggregateAndFlush 验证 Collector 聚合记录并落库。
func TestCollector_AggregateAndFlush(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cfg := CollectorConfig{
		BucketInterval: time.Minute,
		FlushInterval:  time.Hour, // 关闭自动刷新，手动触发
		QueueCapacity:  100,
	}
	c := NewCollector(store, cfg, nil)
	defer func() { _ = c.Close() }()

	now := time.Now()
	for i := 0; i < 10; i++ {
		require.NoError(t, c.Record(ctx, Record{
			TenantID:  "comm-a",
			ClientID:  "c1",
			Endpoint:  "/api/v1/nodes",
			Operation: "create",
			Unit:      UnitCall,
			Count:     1,
			At:        now,
		}))
	}
	c.Flush()

	agg, err := store.AggregateByTenant(ctx, Query{TenantID: "comm-a", Start: now.Add(-time.Hour), End: now.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, agg, 1)
	assert.Equal(t, int64(10), agg[0].TotalCount)
}

// TestCollector_Backpressure 验证队列满时丢弃不阻塞，且累计 dropped。
func TestCollector_Backpressure(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cfg := CollectorConfig{
		BucketInterval: time.Minute,
		FlushInterval:  time.Hour,
		QueueCapacity:  2,
	}
	c := NewCollector(store, cfg, nil)
	defer func() { _ = c.Close() }()

	// 填满队列（2 条）
	require.NoError(t, c.Record(ctx, Record{TenantID: "a", Unit: UnitCall, At: time.Now()}))
	require.NoError(t, c.Record(ctx, Record{TenantID: "a", Unit: UnitCall, At: time.Now()}))
	// 第 3 条被丢弃（非阻塞）
	require.NoError(t, c.Record(ctx, Record{TenantID: "a", Unit: UnitCall, At: time.Now()}))

	c.mu.Lock()
	dropped := c.dropped
	c.mu.Unlock()
	assert.Equal(t, int64(1), dropped)
}

// TestValidUnit 验证单位校验。
func TestValidUnit(t *testing.T) {
	assert.True(t, ValidUnit(UnitCall))
	assert.True(t, ValidUnit(UnitEvent))
	assert.True(t, ValidUnit(UnitByte))
	assert.False(t, ValidUnit("unknown"))
	assert.False(t, ValidUnit(Unit("")))
}