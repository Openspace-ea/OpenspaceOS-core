package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"

	// 匿名导入 modernc.org/sqlite 驱动，driverName 为 "sqlite"。
	_ "modernc.org/sqlite"
)

// SQLiteEventStore 是基于 SQLite 的 EventStore 实现。
//
// 使用 modernc.org/sqlite（纯 Go，无 CGO 依赖），
// 开启 WAL 模式以提高并发写入性能。支持 TTL 与数量上限清理。
type SQLiteEventStore struct {
	db        *sql.DB
	cfg       StoreConfig
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

// sqliteSchemaSQL 是建表与建索引的 SQL。
const sqliteSchemaSQL = `
CREATE TABLE IF NOT EXISTS events (
    event_id        TEXT PRIMARY KEY,
    event_type      TEXT NOT NULL,
    timestamp       TEXT NOT NULL,
    source_node_id  TEXT NOT NULL,
    trace_id        TEXT NOT NULL,
    payload         TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_type    ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_time    ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_source  ON events(source_node_id);
CREATE INDEX IF NOT EXISTS idx_events_created ON events(created_at);
`

// NewSQLiteEventStore 创建 SQLite EventStore。
//
// dbPath 为 SQLite 数据库文件路径。cfg 为零值时使用默认配置。
// 初始化时会开启 WAL 模式并创建表与索引。
func NewSQLiteEventStore(ctx context.Context, dbPath string, cfg StoreConfig) (*SQLiteEventStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开 sqlite 数据库失败: %w", err)
	}

	// 限制连接数，避免 SQLite 并发写锁冲突。
	db.SetMaxOpenConns(1)

	s := &SQLiteEventStore{
		db:  db,
		cfg: cfg.applyDefaults(),
	}

	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// init 执行数据库初始化：WAL 模式、表结构与索引。
func (s *SQLiteEventStore) init(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := s.db.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("执行 PRAGMA 失败 (%s): %w", p, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, sqliteSchemaSQL); err != nil {
		return fmt.Errorf("创建表结构失败: %w", err)
	}
	return nil
}

// Append 追加一个事件到 SQLite 存储。
func (s *SQLiteEventStore) Append(ctx context.Context, e *event.Event) error {
	if e == nil {
		return errNilEvent
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return errStoreClosed
	}

	payloadJSON, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("序列化 payload 失败: %w", err)
	}

	createdAt := s.cfg.Now()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO events (event_id, event_type, timestamp, source_node_id, trace_id, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.EventID,
		string(e.EventType),
		e.Timestamp.UTC().Format(time.RFC3339Nano),
		e.SourceNodeID,
		e.TraceID,
		string(payloadJSON),
		createdAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入事件失败: %w", err)
	}
	return nil
}

// Query 按条件查询历史事件，按事件 Timestamp 升序排列。
func (s *SQLiteEventStore) Query(ctx context.Context, opts QueryOptions) ([]*event.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return nil, errStoreClosed
	}

	var (
		where []string
		args  []any
	)
	if len(opts.EventTypes) > 0 {
		placeholders := make([]string, len(opts.EventTypes))
		for i, et := range opts.EventTypes {
			placeholders[i] = "?"
			args = append(args, string(et))
		}
		where = append(where, "event_type IN ("+strings.Join(placeholders, ", ")+")")
	}
	if opts.SourceNodeID != "" {
		where = append(where, "source_node_id = ?")
		args = append(args, opts.SourceNodeID)
	}
	if opts.StartTime != nil {
		where = append(where, "timestamp >= ?")
		args = append(args, opts.StartTime.UTC().Format(time.RFC3339Nano))
	}
	if opts.EndTime != nil {
		where = append(where, "timestamp <= ?")
		args = append(args, opts.EndTime.UTC().Format(time.RFC3339Nano))
	}

	query := "SELECT event_id, event_type, timestamp, source_node_id, trace_id, payload FROM events"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY timestamp ASC"
	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}
	if opts.Offset > 0 {
		if opts.Limit <= 0 {
			// SQLite 要求 LIMIT 存在才能使用 OFFSET，用 -1 表示无限制
			query += " LIMIT -1 OFFSET ?"
		} else {
			query += " OFFSET ?"
		}
		args = append(args, opts.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询事件失败: %w", err)
	}
	defer rows.Close()

	var result []*event.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历查询结果失败: %w", err)
	}
	return result, nil
}

// Cleanup 清理过期与超额事件，返回被清理的事件数量。
func (s *SQLiteEventStore) Cleanup(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return 0, errStoreClosed
	}

	now := s.cfg.Now()
	cutoff := now.Add(-s.cfg.TTL).UTC().Format(time.RFC3339Nano)
	deleted := 0

	// 清理 TTL 过期事件
	res, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE created_at < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("清理过期事件失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		deleted += int(n)
	}

	// 清理超额事件（保留最新的 MaxEvents 条）
	cnt, err := s.countEvents(ctx)
	if err != nil {
		return deleted, err
	}
	if cnt > s.cfg.MaxEvents {
		excess := cnt - s.cfg.MaxEvents
		_, err := s.db.ExecContext(ctx, `
DELETE FROM events WHERE event_id IN (
    SELECT event_id FROM events ORDER BY timestamp ASC LIMIT ?
)`, excess)
		if err != nil {
			return deleted, fmt.Errorf("清理超额事件失败: %w", err)
		}
		deleted += excess
	}
	return deleted, nil
}

// countEvents 返回当前事件总数。
func (s *SQLiteEventStore) countEvents(ctx context.Context) (int, error) {
	var cnt int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&cnt); err != nil {
		return 0, fmt.Errorf("统计事件数量失败: %w", err)
	}
	return cnt, nil
}

// Close 关闭存储。
func (s *SQLiteEventStore) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		err = s.db.Close()
	})
	return err
}

// scanEvent 从 rows 扫描出一个事件。
func scanEvent(rows *sql.Rows) (*event.Event, error) {
	var (
		eventID      string
		eventType    string
		timestampStr string
		sourceNodeID string
		traceID      string
		payloadStr   string
	)
	if err := rows.Scan(&eventID, &eventType, &timestampStr, &sourceNodeID, &traceID, &payloadStr); err != nil {
		return nil, fmt.Errorf("扫描事件行失败: %w", err)
	}

	ts, err := time.Parse(time.RFC3339Nano, timestampStr)
	if err != nil {
		return nil, fmt.Errorf("解析时间戳失败: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return nil, fmt.Errorf("反序列化 payload 失败: %w", err)
	}

	return &event.Event{
		EventID:      eventID,
		EventType:    event.EventType(eventType),
		Timestamp:    ts,
		SourceNodeID: sourceNodeID,
		TraceID:      traceID,
		Payload:      payload,
	}, nil
}

// 编译期断言：SQLiteEventStore 实现 EventStore 与 Cleaner 接口。
var (
	_ EventStore = (*SQLiteEventStore)(nil)
	_ Cleaner    = (*SQLiteEventStore)(nil)
)
