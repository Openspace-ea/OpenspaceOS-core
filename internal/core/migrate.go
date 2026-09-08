package core

import (
	"context"
	"database/sql"
	"fmt"
)

// MigrationOptions 控制 SQLite→PostgreSQL 数据迁移的行为。
type MigrationOptions struct {
	// BatchSize 每批提交的行数（事务粒度），默认 500。
	// 仅影响导入吞吐，不影响最终结果。
	BatchSize int
}

// MigrationStats 汇报一次迁移的计数结果。
type MigrationStats struct {
	// NodesNew 写入 PostgreSQL 的新节点数。
	NodesNew int64
	// NodesSkipped 目标库已存在而被跳过的节点数（幂等）。
	NodesSkipped int64
	// RelsNew 写入 PostgreSQL 的新关系数。
	RelsNew int64
	// RelsSkipped 目标库已存在而被跳过的关系数（幂等）。
	RelsSkipped int64
}

// migrateNodeSelect 读取 SQLite nodes 全量列。
const migrateNodeSelect = `SELECT node_id, node_type, name, status, owner_community_id, created_at, updated_at, shard_key, federation_id, properties FROM nodes ORDER BY node_id`

// migrateRelSelect 读取 SQLite relationships 全量列。
const migrateRelSelect = `SELECT rel_id, from_node_id, to_node_id, rel_type, properties, created_at FROM relationships ORDER BY rel_id`

// nodeInsertSQL 以 SQLite `?` 占位符编写，运行前经 rebindPostgres 转成 $N。
// ON CONFLICT DO NOTHING 保证幂等：已存在的主键被跳过而非报错。
const nodeInsertSQL = `INSERT INTO nodes (node_id, node_type, name, status, owner_community_id, created_at, updated_at, shard_key, federation_id, properties)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (node_id) DO NOTHING`

// relInsertSQL 针对 relationships 表，语义同上。
const relInsertSQL = `INSERT INTO relationships (rel_id, from_node_id, to_node_id, rel_type, properties, created_at)
VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (rel_id) DO NOTHING`

// MigrateSQLiteToPostgres 将 SQLite 中的 nodes 与 relationships 导入 PostgreSQL。
//
// 这是 T1.7 提供的"可选择导入"能力：按需显式调用，不随启动自动执行。
// src 与 dst 均需已完成各自表结构初始化（见 InitDB / InitPostgresDB）。
//
// 数据以文本形式逐列读取并写回目标库，与既有 scan/存储格式完全一致（无损）。
// 使用 ON CONFLICT DO NOTHING 保证幂等：已存在的主键行会被跳过而非失败，
// 因此可安全重复执行。以每 BatchSize 行为一事务提交，避免逐行提交的开销。
func MigrateSQLiteToPostgres(ctx context.Context, src, dst *sql.DB, opts MigrationOptions) (MigrationStats, error) {
	var stats MigrationStats
	batch := opts.BatchSize
	if batch < 1 {
		batch = 500
	}

	nodeRows, err := src.QueryContext(ctx, migrateNodeSelect)
	if err != nil {
		return stats, fmt.Errorf("读取 SQLite nodes 失败: %w", err)
	}
	defer nodeRows.Close()
	nodesNew, nodesSkip, err := copyRows(ctx, dst, rebindPostgres(nodeInsertSQL), 10, nodeRows, batch)
	if err != nil {
		return stats, fmt.Errorf("导入 nodes 失败: %w", err)
	}
	stats.NodesNew, stats.NodesSkipped = nodesNew, nodesSkip

	relRows, err := src.QueryContext(ctx, migrateRelSelect)
	if err != nil {
		return stats, fmt.Errorf("读取 SQLite relationships 失败: %w", err)
	}
	defer relRows.Close()
	relsNew, relsSkip, err := copyRows(ctx, dst, rebindPostgres(relInsertSQL), 6, relRows, batch)
	if err != nil {
		return stats, fmt.Errorf("导入 relationships 失败: %w", err)
	}
	stats.RelsNew, stats.RelsSkipped = relsNew, relsSkip

	return stats, nil
}

// copyRows 从 rows 中逐行读取，以每 batch 行为一事务插入目标库。
//
// 返回 (新建行数, 跳过行数)。以每行 Exec 的 RowsAffected 判定：
// ON CONFLICT DO NOTHING 下 RowsAffected=1 表示插入，=0 表示跳过。
// 列以任意类型读入后转为字符串写回 PG 文本列，与既有存储格式保持一致。
func copyRows(ctx context.Context, db *sql.DB, insertSQL string, columnCount int, rows *sql.Rows, batch int) (newCount int64, skipped int64, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	prep, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		_ = tx.Rollback()
		return 0, 0, err
	}

	rowInTx := 0
	// commitBatch 提交当前事务并开启下一批的新事务。提交后必须关闭旧 prep，
	// 否则会向连接池泄漏一个挂起的事务与 prepared statement（此前实现的 bug）。
	commitBatch := func() error {
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return err
		}
		_ = prep.Close()
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		prep, err = tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		rowInTx = 0
		return nil
	}

	for rows.Next() {
		raw := make([]any, columnCount)
		dests := make([]any, columnCount)
		for i := 0; i < columnCount; i++ {
			dests[i] = &raw[i]
		}
		if err := rows.Scan(dests...); err != nil {
			_ = tx.Rollback()
			_ = prep.Close()
			return newCount, skipped, err
		}
		args := make([]any, columnCount)
		for i := 0; i < columnCount; i++ {
			args[i] = anyToString(raw[i])
		}

		res, err := prep.ExecContext(ctx, args...)
		if err != nil {
			_ = tx.Rollback()
			_ = prep.Close()
			return newCount, skipped, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			_ = prep.Close()
			return newCount, skipped, err
		}
		if affected > 0 {
			newCount++
		} else {
			skipped++
		}

		rowInTx++
		if rowInTx >= batch {
			if err := commitBatch(); err != nil {
				return newCount, skipped, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback()
		_ = prep.Close()
		return newCount, skipped, err
	}
	// 提交剩余批次；空源或恰好被 batch 整除时，回滚末尾的空事务并释放 prep。
	if rowInTx > 0 {
		if err := tx.Commit(); err != nil {
			return newCount, skipped, err
		}
	} else {
		_ = tx.Rollback()
	}
	_ = prep.Close()
	return newCount, skipped, nil
}

// anyToString 将扫描到的任意单元格值转为字符串。
// nil 转为空字符串；[]byte/string 直接转换；其他类型用 fmt.Sprint。
// 该库的 nodes/relationships 列均为文本（或可安全转文本），满足无损写回。
func anyToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}