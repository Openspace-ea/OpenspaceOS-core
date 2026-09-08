package core

import "strings"

// rebind 是一个可导出别名，main 装配时可注入 KGService 事务 SQL 的占位符适配。
//
// RebindIdentity 返回原 SQL，用于 SQLite；RebindPostgres 转换为 $N 用于 PostgreSQL。
var (
	// RebindIdentity 是 SQLite 的占位符实现，SQL 保持不变。
	RebindIdentity = rebindIdentity
	// RebindPostgres 将 SQLite `?` 占位符转换为 PostgreSQL 的 `$N` 形式。
	RebindPostgres = rebindPostgres
)

// rebind func 将一段以 SQLite `?` 占位符编写的 SQL，按出现顺序转换为目标驱动的占位符格式。
//
// - SQLite 驱动使用 `?`（返回原样即可）；
// - PostgreSQL（pq/pgx）使用 `$1, $2, ...`。
//
// 实现按顺序扫描并把每个 `?` 替换为 `$N`。要求原 SQL 中不存在字面量 `?`（仅参数占位符）。
type rebindFunc func(sql string) string

// rebindIdentity 是 SQLite 的占位符实现，SQL 保持不变。
func rebindIdentity(sql string) string {
	return sql
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
			b.WriteByte('$')
			writeInt(&b, n)
			continue
		}
		b.WriteByte(sql[i])
	}
	return b.String()
}

// writeInt 将非负整数写入 builder（不依赖 strconv，无额外负担）。
func writeInt(b *strings.Builder, v int) {
	if v == 0 {
		b.WriteByte('0')
		return
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	b.Write(buf[pos:])
}