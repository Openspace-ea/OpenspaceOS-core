package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/pkg/model"

	// 匿名导入 modernc.org/sqlite 驱动
	_ "modernc.org/sqlite"
)

// newTestUserStore 创建基于内存 SQLite 的 UserStore 用于测试。
func newTestUserStore(t *testing.T) *SQLiteUserStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLiteUserStore(db)
	require.NoError(t, store.InitSchema(context.Background()))
	return store
}

// newTestService 创建用于测试的认证 Service（内存存储 + 短有效期 token）。
func newTestService(t *testing.T) *Service {
	t.Helper()
	store := newTestUserStore(t)
	tm := NewTokenManager("test-secret", time.Hour)
	return NewService(store, tm, nil)
}

// TestDefaultAdminUser 测试 EnsureDefaultAdmin：初始化后 admin 用户存在，再次调用不重复创建。
func TestDefaultAdminUser(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// 首次初始化应创建 admin 用户
	require.NoError(t, svc.EnsureDefaultAdmin(ctx))

	// admin 用户应存在
	user, err := svc.store.GetByUsername(ctx, "admin")
	require.NoError(t, err)
	assert.Equal(t, "admin", user.Username)
	assert.Contains(t, user.Roles, "admin")
	assert.False(t, user.CreatedAt.IsZero())

	// 再次调用不应重复创建
	require.NoError(t, svc.EnsureDefaultAdmin(ctx))
	users, err := svc.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)
}

// TestDefaultAdminLogin 测试默认 admin 用户可登录。
func TestDefaultAdminLogin(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	require.NoError(t, svc.EnsureDefaultAdmin(ctx))

	// 正确密码登录成功
	token, err := svc.Login(ctx, "admin", "admin123")
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// 错误密码登录失败
	_, err = svc.Login(ctx, "admin", "wrongpassword")
	assert.ErrorIs(t, err, ErrInvalidCredentials)

	// 不存在的用户登录失败
	_, err = svc.Login(ctx, "nonexistent", "whatever")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

// TestUserCRUD 测试用户 CRUD：创建 → 查询 → 列表 → 删除。
func TestUserCRUD(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// 创建用户
	user := &User{
		Username:    "operator1",
		Roles:       []string{"operator"},
		CommunityID: "comm-a",
	}
	require.NoError(t, svc.CreateUser(ctx, user, "password123"))
	assert.NotEmpty(t, user.UserID)
	assert.False(t, user.CreatedAt.IsZero())
	assert.False(t, user.UpdatedAt.IsZero())

	// 按用户名查询（通过 store）
	got, err := svc.store.GetByUsername(ctx, "operator1")
	require.NoError(t, err)
	assert.Equal(t, user.UserID, got.UserID)
	assert.Equal(t, "operator1", got.Username)
	assert.Equal(t, []string{"operator"}, got.Roles)
	assert.Equal(t, "comm-a", got.CommunityID)

	// 按 ID 查询
	got, err = svc.GetUser(ctx, user.UserID)
	require.NoError(t, err)
	assert.Equal(t, user.UserID, got.UserID)

	// 列表
	users, err := svc.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)

	// 删除
	require.NoError(t, svc.DeleteUser(ctx, user.UserID))

	// 删除后查询返回 ErrUserNotFound
	_, err = svc.GetUser(ctx, user.UserID)
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// TestCreateUserValidation 测试创建用户的校验逻辑。
func TestCreateUserValidation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// 用户名为空
	err := svc.CreateUser(ctx, &User{Roles: []string{"viewer"}}, "pass")
	assert.Error(t, err)

	// 密码为空
	err = svc.CreateUser(ctx, &User{Username: "user1", Roles: []string{"viewer"}}, "")
	assert.Error(t, err)

	// nil user
	err = svc.CreateUser(ctx, nil, "pass")
	assert.Error(t, err)
}

// TestHasPermission 测试权限检查：admin 有所有权限，viewer 只有读权限。
func TestHasPermission(t *testing.T) {
	svc := newTestService(t)

	// admin 拥有所有权限
	assert.True(t, svc.HasPermission([]string{"admin"}, "node.create"))
	assert.True(t, svc.HasPermission([]string{"admin"}, "node.delete"))
	assert.True(t, svc.HasPermission([]string{"admin"}, "user.manage"))
	assert.True(t, svc.HasPermission([]string{"admin"}, "plugin.manage"))

	// operator 拥有读写权限但不能删除节点/管理用户
	assert.True(t, svc.HasPermission([]string{"operator"}, "node.create"))
	assert.True(t, svc.HasPermission([]string{"operator"}, "node.update"))
	assert.False(t, svc.HasPermission([]string{"operator"}, "node.delete"))
	assert.False(t, svc.HasPermission([]string{"operator"}, "user.manage"))
	assert.False(t, svc.HasPermission([]string{"operator"}, "plugin.manage"))

	// viewer 只有读权限
	assert.True(t, svc.HasPermission([]string{"viewer"}, "node.read"))
	assert.True(t, svc.HasPermission([]string{"viewer"}, "graph.query"))
	assert.False(t, svc.HasPermission([]string{"viewer"}, "node.create"))
	assert.False(t, svc.HasPermission([]string{"viewer"}, "node.update"))
	assert.False(t, svc.HasPermission([]string{"viewer"}, "node.delete"))

	// 多角色取并集
	assert.True(t, svc.HasPermission([]string{"viewer", "operator"}, "node.create"))
	assert.True(t, svc.HasPermission([]string{"viewer", "operator"}, "node.read"))

	// 未知角色无权限
	assert.False(t, svc.HasPermission([]string{"unknown"}, "node.read"))

	// 空角色无权限
	assert.False(t, svc.HasPermission(nil, "node.read"))
}

// TestCanAccessNode 测试 Node 级权限。
//
// admin 可访问所有 Node；operator 只能访问自己 Community 的 Node。
func TestCanAccessNode(t *testing.T) {
	svc := newTestService(t)

	nodeInCommA := &model.Node{NodeID: "n-1", OwnerCommunityID: "comm-a"}
	nodeInCommB := &model.Node{NodeID: "n-2", OwnerCommunityID: "comm-b"}

	// admin 可访问所有 Node
	adminClaims := &Claims{UserID: "u-admin", Roles: []string{"admin"}, CommunityID: "comm-a"}
	assert.True(t, svc.CanAccessNode(adminClaims, nodeInCommA))
	assert.True(t, svc.CanAccessNode(adminClaims, nodeInCommB))

	// operator 只能访问自己 Community 的 Node
	opClaims := &Claims{UserID: "u-op", Roles: []string{"operator"}, CommunityID: "comm-a"}
	assert.True(t, svc.CanAccessNode(opClaims, nodeInCommA))  // 同社区
	assert.False(t, svc.CanAccessNode(opClaims, nodeInCommB)) // 跨社区

	// CommunityID 为空的非 admin 用户无法访问任何 Node
	emptyCommClaims := &Claims{UserID: "u-x", Roles: []string{"operator"}, CommunityID: ""}
	assert.False(t, svc.CanAccessNode(emptyCommClaims, nodeInCommA))

	// nil claims 或 nil node 返回 false
	assert.False(t, svc.CanAccessNode(nil, nodeInCommA))
	assert.False(t, svc.CanAccessNode(adminClaims, nil))
}

// TestUserStoreCreateDuplicate 测试创建重复用户名失败。
func TestUserStoreCreateDuplicate(t *testing.T) {
	store := newTestUserStore(t)
	ctx := context.Background()

	user1 := &User{
		UserID:    "u-1",
		Username:  "dup",
		Password:  "hashed",
		Roles:     []string{"viewer"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, store.Create(ctx, user1))

	// 相同用户名再次创建应失败
	user2 := &User{
		UserID:    "u-2",
		Username:  "dup",
		Password:  "hashed",
		Roles:     []string{"viewer"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err := store.Create(ctx, user2)
	assert.Error(t, err)
}

// TestUserStoreGetNotFound 测试查询不存在的用户返回 ErrUserNotFound。
func TestUserStoreGetNotFound(t *testing.T) {
	store := newTestUserStore(t)
	ctx := context.Background()

	_, err := store.GetByID(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrUserNotFound)

	_, err = store.GetByUsername(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// TestUserStoreUpdate 测试用户更新。
func TestUserStoreUpdate(t *testing.T) {
	store := newTestUserStore(t)
	ctx := context.Background()

	now := time.Now()
	user := &User{
		UserID:      "u-upd",
		Username:    "upduser",
		Password:    "oldhash",
		Roles:       []string{"viewer"},
		CommunityID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	require.NoError(t, store.Create(ctx, user))

	// 更新
	user.Roles = []string{"operator"}
	user.CommunityID = "comm-new"
	user.UpdatedAt = time.Now()
	require.NoError(t, store.Update(ctx, user))

	// 验证更新结果
	got, err := store.GetByID(ctx, user.UserID)
	require.NoError(t, err)
	assert.Equal(t, []string{"operator"}, got.Roles)
	assert.Equal(t, "comm-new", got.CommunityID)
}

// TestUserStoreList 测试用户列表查询。
func TestUserStoreList(t *testing.T) {
	store := newTestUserStore(t)
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Create(ctx, &User{
			UserID:    "u-list-" + string(rune('a'+i)),
			Username:  "listuser" + string(rune('a'+i)),
			Password:  "hash",
			Roles:     []string{"viewer"},
			CreatedAt: now.Add(time.Duration(i) * time.Second),
			UpdatedAt: now,
		}))
	}

	users, err := store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 3)
}
