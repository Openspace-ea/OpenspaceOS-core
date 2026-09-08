package usage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SQLStore 是基于 database/sql 的 usage Store 实现，兼容 SQLite 与 PostgreSQL。
//
// 时间以 RFC3339Nano 文本存储；通过 rebind 适配两种驱动占位符。
type SQLStore struct {
	db     *sql.DB
	rebind func(string) string
}

// NewSQLStore 创建基于 SQLite 的 usage Store。
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db, rebind: func(s string) string { return s }}
}

// NewPostgresStore 创建基于 PostgreSQL 的 usage Store。
func NewPostgresStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db, rebind: rebindPostgres}
}

// rebindPostgres 将 SQLite `?` 占位符转换为 PostgreSQL 的 `$N` 形式。
func rebindPostgres(sql string) string {
	if !strings.Contains(sql, "?") {
		return sql
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(sql); i++ {
		if sql[i] == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteByte(sql[i])
	}
	return b.String()
}

// InsertBatch 批量写入用量记录。返回实际写入条数。
func (s *SQLStore) InsertBatch(ctx context.Context, buckets []Bucket) (int64, error) {
	if len(buckets) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(buckets) == 0 {
		return 0, nil
	}

	stmt := s.rebind(`
INSERT INTO usage (id, tenant_id, client_id, endpoint, operation, unit, count, bucket_start, bucket_end)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("开启 usage 写入事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var written int64
	for _, b := range buckets {
		id := b.ID
		if id == "" {
			id = uuid.NewString()
		}
		res, err := tx.ExecContext(ctx, stmt,
			id,
			b.TenantID,
			b.ClientID,
			b.Endpoint,
			b.Operation,
			string(b.Unit),
			b.Count,
			b.BucketStart.UTC().Format(time.RFC3339Nano),
			b.BucketEnd.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return written, fmt.Errorf("写入 usage 记录失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			written++
		}
	}
	if err := tx.Commit(); err != nil {
		return written, fmt.Errorf("提交 usage 写入事务失败: %w", err)
	}
	return written, nil
}

// AggregateByTenant 按租户/客户端/端点/操作，在时间范围内聚合用量。
func (s *SQLStore) AggregateByTenant(ctx context.Context, q Query) ([]Aggregate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var conditions []string
	var args []any

	conditions = append(conditions, "bucket_end >= ?")
	args = append(args, q.Start.UTC().Format(time.RFC3339Nano))
	conditions = append(conditions, "bucket_start <= ?")
	args = append(args, q.End.UTC().Format(time.RFC3339Nano))

	if q.TenantID != "" {
		conditions = append(conditions, "tenant_id = ?")
		args = append(args, q.TenantID)
	}
	if q.ClientID != "" {
		conditions = append(conditions, "client_id = ?")
		args = append(args, q.ClientID)
	}
	if q.Operation != "" {
		conditions = append(conditions, "operation = ?")
		args = append(args, q.Operation)
	}
	if q.HasUnit {
		conditions = append(conditions, "unit = ?")
		args = append(args, string(q.Unit))
	}

	where := strings.Join(conditions, " AND ")
	query := fmt.Sprintf(`
SELECT tenant_id, client_id, endpoint, operation, unit,
       MIN(bucket_start) AS bucket_start,
       MAX(bucket_end)   AS bucket_end,
       SUM(count)        AS total_count
FROM usage
WHERE %s
GROUP BY tenant_id, client_id, endpoint, operation, unit
ORDER BY total_count DESC`, where)

	rows, err := s.db.QueryContext(ctx, s.rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("查询 usage 聚合失败: %w", err)
	}
	defer rows.Close()

	var out []Aggregate
	for rows.Next() {
		var (
			a           Aggregate
			startStr    string
			endStr      string
			unitStr     string
			totalCount  int64
		)
		if err := rows.Scan(
			&a.TenantID, &a.ClientID, &a.Endpoint, &a.Operation, &unitStr,
			&startStr, &endStr, &totalCount,
		); err != nil {
			return nil, fmt.Errorf("扫描 usage 聚合行失败: %w", err)
		}
		a.Unit = Unit(unitStr)
		a.TotalCount = totalCount
		if t, err := time.Parse(time.RFC3339Nano, startStr); err == nil {
			a.BucketStart = t
		}
		if t, err := time.Parse(time.RFC3339Nano, endStr); err == nil {
			a.BucketEnd = t
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 usage 聚合结果失败: %w", err)
	}
	return out, nil
}

// Close 释放资源。当前不持有独立连接，为空操作。
func (s *SQLStore) Close() error {
	return nil
}

// 编译期断言。
var _ Store = (*SQLStore)(nil)