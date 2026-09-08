package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	// 匿名导入 modernc.org/sqlite 驱动，driverName 为 "sqlite"。
	_ "modernc.org/sqlite"
)

// ErrUserNotFound 表示按 ID 或用户名查询时未找到对应用户。
//
// 上层可通过 errors.Is(err, ErrUserNotFound) 判断是否为"用户不存在"场景。
var ErrUserNotFound = errors.New("用户不存在")

// userSchemaSQL 是 users 表的建表 SQL。
const userSchemaSQL = `
CREATE TABLE IF NOT EXISTS users (
    user_id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password TEXT NOT NULL,
    roles TEXT NOT NULL DEFAULT '[]',
    community_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_community ON users(community_id);
`

// UserStore 是用户数据的持久化抽象接口。
//
// 提供 User 的 CRUD 能力。当前提供 SQLite 实现，
// 后续可通过实现此接口替换为其他存储后端。
type UserStore interface {
	// Create 创建一个用户。如果 userID 或 username 已存在则返回错误。
	Create(ctx context.Context, user *User) error
	// GetByID 按用户 ID 查询用户。不存在时返回 ErrUserNotFound。
	GetByID(ctx context.Context, userID string) (*User, error)
	// GetByUsername 按用户名查询用户。不存在时返回 ErrUserNotFound。
	GetByUsername(ctx context.Context, username string) (*User, error)
	// List 列出所有用户。
	List(ctx context.Context) ([]*User, error)
	// Update 更新一个用户。不存在时返回 ErrUserNotFound。
	Update(ctx context.Context, user *User) error
	// Delete 按用户 ID 删除用户。
	Delete(ctx context.Context, userID string) error
	// Close 释放存储相关资源（如有）。
	Close() error
}

// SQLiteUserStore 是基于 database/sql 的 UserStore 实现。
//
// Roles 字段以 JSON 数组字符串存储。时间字段以 RFC3339Nano 字符串存储。
// 通过 rebind 适配不同驱动占位符（SQLite 原样 / PostgreSQL 转 $N）。
type SQLiteUserStore struct {
	db     *sql.DB
	rebind func(string) string
}

// NewSQLiteUserStore 创建 SQLite UserStore。
//
// 调用方需确保 db 已完成表结构初始化（InitSchema 会创建 users 表）。
// 若 db 为 nil 则返回 nil。
func NewSQLiteUserStore(db *sql.DB) *SQLiteUserStore {
	if db == nil {
		return nil
	}
	return &SQLiteUserStore{db: db, rebind: func(s string) string { return s }}
}

// NewPostgresUserStore 创建基于 PostgreSQL 的 UserStore。
//
// 复用同一套以 SQLite 占位符编写的 SQL，运行时转换为 PG 的 $N 占位符。
// 调用方需确保数据库用户的表格已在 PG 中初始化。
func NewPostgresUserStore(db *sql.DB) *SQLiteUserStore {
	if db == nil {
		return nil
	}
	return &SQLiteUserStore{db: db, rebind: func(sql string) string { return rebindPostgres(sql) }}
}

// rebindPostgres 将 SQLite `?` 占位符转换为 PostgreSQL 的 `$N` 形式（本地实现，避免与 core 包耦合）。
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
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(sql[i])
	}
	return b.String()
}

// InitSchema 创建 users 表与索引。在数据库已初始化的情况下调用是幂等的。
func (s *SQLiteUserStore) InitSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, s.rebind(userSchemaSQL)); err != nil {
		return fmt.Errorf("创建 users 表失败: %w", err)
	}
	return nil
}

// Create 插入一个用户。如果 userID 或 username 已存在则返回错误。
func (s *SQLiteUserStore) Create(ctx context.Context, user *User) error {
	if user == nil {
		return errors.New("user 不能为 nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rolesJSON, err := marshalRoles(user.Roles)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, s.rebind(`
INSERT INTO users (user_id, username, password, roles, community_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`),
		user.UserID,
		user.Username,
		user.Password,
		rolesJSON,
		user.CommunityID,
		user.CreatedAt.UTC().Format(time.RFC3339Nano),
		user.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入用户失败: %w", err)
	}
	return nil
}

// GetByID 按用户 ID 查询用户。不存在时返回 ErrUserNotFound。
func (s *SQLiteUserStore) GetByID(ctx context.Context, userID string) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, s.rebind(`
SELECT user_id, username, password, roles, community_id, created_at, updated_at
FROM users WHERE user_id = ?`), userID)
	return scanUser(row)
}

// GetByUsername 按用户名查询用户。不存在时返回 ErrUserNotFound。
func (s *SQLiteUserStore) GetByUsername(ctx context.Context, username string) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, s.rebind(`
SELECT user_id, username, password, roles, community_id, created_at, updated_at
FROM users WHERE username = ?`), username)
	return scanUser(row)
}

// List 列出所有用户，按创建时间升序排列。
func (s *SQLiteUserStore) List(ctx context.Context) ([]*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, s.rebind(`
SELECT user_id, username, password, roles, community_id, created_at, updated_at
FROM users ORDER BY created_at ASC, user_id ASC`))
	if err != nil {
		return nil, fmt.Errorf("查询用户列表失败: %w", err)
	}
	defer rows.Close()

	var result []*User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描用户行失败: %w", err)
		}
		result = append(result, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历用户列表失败: %w", err)
	}
	return result, nil
}

// Update 更新一个用户。不存在时返回 ErrUserNotFound。
func (s *SQLiteUserStore) Update(ctx context.Context, user *User) error {
	if user == nil {
		return errors.New("user 不能为 nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rolesJSON, err := marshalRoles(user.Roles)
	if err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, s.rebind(`
UPDATE users
SET username = ?, password = ?, roles = ?, community_id = ?, updated_at = ?
WHERE user_id = ?`),
		user.Username,
		user.Password,
		rolesJSON,
		user.CommunityID,
		user.UpdatedAt.UTC().Format(time.RFC3339Nano),
		user.UserID,
	)
	if err != nil {
		return fmt.Errorf("更新用户失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: userId=%s", ErrUserNotFound, user.UserID)
	}
	return nil
}

// Delete 按用户 ID 删除用户。
func (s *SQLiteUserStore) Delete(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, s.rebind("DELETE FROM users WHERE user_id = ?"), userID)
	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
	}
	return nil
}

// Close 释放存储相关资源。当前 SQLiteUserStore 不持有独立连接，
// Close 为空操作，db 的关闭由调用方负责。
func (s *SQLiteUserStore) Close() error {
	return nil
}

// userRowScanner 是 *sql.Row 和 *sql.Rows 共同实现的接口。
type userRowScanner interface {
	Scan(dest ...any) error
}

// scanUser 从 *sql.Row 或 *sql.Rows 扫描出一个 User。
func scanUser(s userRowScanner) (*User, error) {
	var (
		userID      string
		username    string
		password    string
		rolesStr    string
		communityID string
		createdStr  string
		updatedStr  string
	)
	if err := s.Scan(
		&userID,
		&username,
		&password,
		&rolesStr,
		&communityID,
		&createdStr,
		&updatedStr,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("扫描用户行失败: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return nil, fmt.Errorf("解析 createdAt 失败: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("解析 updatedAt 失败: %w", err)
	}

	roles, err := unmarshalRoles(rolesStr)
	if err != nil {
		return nil, err
	}

	return &User{
		UserID:      userID,
		Username:    username,
		Password:    password,
		Roles:       roles,
		CommunityID: communityID,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

// marshalRoles 将角色列表序列化为 JSON 字符串。
// nil 或空列表序列化为 "[]"。
func marshalRoles(roles []string) (string, error) {
	if len(roles) == 0 {
		return "[]", nil
	}
	data, err := json.Marshal(roles)
	if err != nil {
		return "", fmt.Errorf("序列化 roles 失败: %w", err)
	}
	return string(data), nil
}

// unmarshalRoles 将 JSON 字符串反序列化为角色列表。
func unmarshalRoles(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var roles []string
	if err := json.Unmarshal([]byte(s), &roles); err != nil {
		return nil, fmt.Errorf("反序列化 roles 失败: %w", err)
	}
	return roles, nil
}

// NewUserID 生成一个新的用户 ID（UUID v4）。
func NewUserID() string {
	return uuid.NewString()
}

// 编译期断言：SQLiteUserStore 实现 UserStore 接口。
var _ UserStore = (*SQLiteUserStore)(nil)
