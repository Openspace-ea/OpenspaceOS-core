// Package storage 提供 Openspace OS Core 的存储迁移机制。
//
// 采用内置轻量迁移器（不依赖外部迁移工具），通过 schema_migrations
// 版本表追踪已应用的迁移，支持 SQLite 与 PostgreSQL 双后端：
//   - 迁移 SQL 一律以 SQLite 占位符 `?` 编写，运行时经 rebind 转换为目标驱动格式；
//   - DDL 本身不使用占位符，可直接在双方执行；
//   - 每条迁移可包含多条 SQL 语句，迁移器按分号拆分后逐条执行，兼容两个驱动。
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// timeNowRFC3339 返回当前时间（UTC，RFC3339Nano 格式），与存量表时间字段存储格式一致。
func timeNowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// schemaMigrationsTable 是迁移版本表（幂等创建，兼容 SQLite/PostgreSQL）。
const schemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    applied_at TEXT NOT NULL
);
`

// Migration 定义一条迁移：版本号、名称与向上执行的 SQL。
//
// Up 可由多条语句组成（以分号分隔）。DDL 不应包含占位符；
// 需要参数化的语句本迁移机制暂不涉及（保持 DDL 纯净）。
type Migration struct {
	Version int64
	Name    string
	Up      string
}

// Migrator 是内置 schema 迁移器。
//
// 持有一个 *sql.DB 与占位符转换函数。Migrate 执行所有未应用的迁移。
type Migrator struct {
	db         *sql.DB
	rebind     func(string) string
	migrations []Migration
	logger     *slog.Logger
}

// Option 配置 Migrator 的选项函数。
type Option func(*Migrator)

// WithRebind 设置占位符转换函数（PostgreSQL 需传 core.RebindPostgres）。
func WithRebind(f func(string) string) Option {
	return func(m *Migrator) {
		if f != nil {
			m.rebind = f
		}
	}
}

// WithLogger 设置日志器。
func WithLogger(l *slog.Logger) Option {
	return func(m *Migrator) {
		if l != nil {
			m.logger = l
		}
	}
}

// WithMigrations 追加额外迁移（在默认迁移之后按版本排序）。
func WithMigrations(ms []Migration) Option {
	return func(m *Migrator) {
		m.migrations = append(m.migrations, ms...)
	}
}

// NewMigrator 创建迁移器。opts 可选。
func NewMigrator(db *sql.DB, opts ...Option) *Migrator {
	m := &Migrator{
		db:     db,
		rebind: func(s string) string { return s }, // 默认 SQLite（原样）
		logger: slog.Default(),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Migrate 执行所有未应用的迁移（升序）。
//
// 首次运行时创建 schema_migrations 版本表；后续按版本号跳过已应用项。
// 每条迁移在单个事务中执行多条语句，异常时回滚。
func (m *Migrator) Migrate(ctx context.Context) error {
	if _, err := m.db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		return fmt.Errorf("创建 schema_migrations 表失败: %w", err)
	}

	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return err
	}

	m.sortMigrations()
	for _, mig := range m.migrations {
		if _, ok := applied[mig.Version]; ok {
			continue
		}
		if err := m.apply(ctx, mig); err != nil {
			return err
		}
		m.logger.Info("已应用 schema 迁移",
			"version", mig.Version, "name", mig.Name)
	}
	return nil
}

// appliedVersions 返回当前已应用的版本号集合。
func (m *Migrator) appliedVersions(ctx context.Context) (map[int64]struct{}, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("查询已应用迁移失败: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]struct{})
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("扫描迁移版本失败: %w", err)
		}
		applied[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历迁移版本失败: %w", err)
	}
	return applied, nil
}

// apply 在单个事务中执行一条迁移的全部语句，并记录版本。
func (m *Migrator) apply(ctx context.Context, mig Migration) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启迁移事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmts := splitStatements(mig.Up)
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("迁移 %d (%s) 执行失败: %w", mig.Version, mig.Name, err)
		}
	}

	// 记录版本（rebind 处理 ? 占位符）
	txRebind := m.rebind
	_, err = tx.ExecContext(ctx, txRebind(
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)"),
		mig.Version, mig.Name, timeNowRFC3339(),
	)
	if err != nil {
		return fmt.Errorf("记录迁移版本失败: %w", err)
	}
	return tx.Commit()
}

// sortMigrations 按版本号升序排序（稳定），确保依赖顺序正确。
func (m *Migrator) sortMigrations() {
	for i := 1; i < len(m.migrations); i++ {
		for j := i; j > 0 && m.migrations[j-1].Version > m.migrations[j].Version; j-- {
			m.migrations[j-1], m.migrations[j] = m.migrations[j], m.migrations[j-1]
		}
	}
}

// splitStatements 按分号拆分多语句 SQL，忽略空与纯空白语句。
func splitStatements(sql string) []string {
	raw := strings.Split(sql, ";")
	stmts := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		stmts = append(stmts, s)
	}
	return stmts
}