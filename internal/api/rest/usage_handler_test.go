package rest

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/auth"
	"github.com/openspace-os/openspace-os-core/internal/storage"
	"github.com/openspace-os/openspace-os-core/internal/usage"

	// 匿名导入 modernc.org/sqlite 驱动
	_ "modernc.org/sqlite"
)

// newTestUsageStore 创建内存 SQLite 的 usage store（含迁移）。
func newTestUsageStore(t *testing.T) *usage.SQLStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mig := storage.NewMigrator(db, storage.WithMigrations(storage.DefaultMigrations()))
	require.NoError(t, mig.Migrate(context.Background()))
	return usage.NewSQLStore(db)
}

// TestParseUsageQuery_DefaultWindow 验证未提供时间时默认最近 24 小时。
func TestParseUsageQuery_DefaultWindow(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/usage", nil)
	q, err := parseUsageQuery(req)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), q.End, 5*time.Second)
	assert.WithinDuration(t, time.Now().Add(-24*time.Hour), q.Start, 5*time.Second)
}

// TestParseUsageQuery_CustomWindow 验证自定义时间与过滤条件。
func TestParseUsageQuery_CustomWindow(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/billing/usage?tenantId=comm-a&clientId=c1&operation=create&unit=call&start=2026-08-28T00:00:00Z&end=2026-08-28T23:59:59Z",
		nil)
	q, err := parseUsageQuery(req)
	require.NoError(t, err)
	assert.Equal(t, "comm-a", q.TenantID)
	assert.Equal(t, "c1", q.ClientID)
	assert.Equal(t, "create", q.Operation)
	assert.True(t, q.HasUnit)
	assert.Equal(t, usage.UnitCall, q.Unit)
}

// TestParseUsageQuery_Invalid 验证非法时间与单位返回错误。
func TestParseUsageQuery_Invalid(t *testing.T) {
	// 非法 start
	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/usage?start=bad", nil)
	_, err := parseUsageQuery(req)
	assert.Error(t, err)

	// 非法 unit
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/billing/usage?unit=foo", nil)
	_, err = parseUsageQuery(req2)
	assert.Error(t, err)
}

// TestListUsage_Endpoint 验证查询 API 端点的完整链路。
func TestListUsage_Endpoint(t *testing.T) {
	store := newTestUsageStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	_, err := store.InsertBatch(ctx, []usage.Bucket{
		{TenantID: "comm-a", ClientID: "c1", Endpoint: "/api/v1/nodes", Operation: "create", Unit: usage.UnitCall, Count: 7, BucketStart: now.Add(-time.Minute), BucketEnd: now},
	})
	require.NoError(t, err)

	// 构建带 usageStore 的 Handler
	tm := auth.NewTokenManager("test-secret", time.Hour)
	h := NewHandler(nil, nil, nil, nil)
	h.SetAuth(nil, tm, false)
	h.SetUsageStore(store)

	router := NewRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/usage", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, float64(1), body["count"])
	_ = strings.TrimSpace
}