package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// 默认管理员账户配置。
const (
	defaultAdminUsername = "admin"
	defaultAdminPassword = "admin123"
	defaultAdminRole     = RoleAdmin
)

// Service 是认证与用户管理的领域服务。
//
// 封装用户 CRUD、登录鉴权、权限校验等业务逻辑。
type Service struct {
	store  UserStore
	tm     *TokenManager
	logger *slog.Logger
}

// NewService 创建认证 Service。
//
// logger 为 nil 时使用 slog.Default()。tm 为 nil 时会 panic（必填依赖）。
func NewService(store UserStore, tm *TokenManager, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if tm == nil {
		panic("auth: TokenManager 不能为 nil")
	}
	return &Service{
		store:  store,
		tm:     tm,
		logger: logger,
	}
}

// EnsureDefaultAdmin 确保默认 admin 用户存在。
//
// 当用户表为空时创建默认 admin 用户（用户名 admin，密码 admin123，角色 admin）。
// 已有用户时不做任何操作。store 为 nil 时直接返回。
func (s *Service) EnsureDefaultAdmin(ctx context.Context) error {
	if s.store == nil {
		return errors.New("user store 未初始化")
	}
	users, err := s.store.List(ctx)
	if err != nil {
		return fmt.Errorf("查询用户列表失败: %w", err)
	}
	if len(users) > 0 {
		return nil
	}

	hashed, err := HashPassword(defaultAdminPassword)
	if err != nil {
		return fmt.Errorf("哈希默认密码失败: %w", err)
	}
	now := time.Now()
	admin := &User{
		UserID:      NewUserID(),
		Username:    defaultAdminUsername,
		Password:    hashed,
		Roles:       []string{defaultAdminRole},
		CommunityID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.Create(ctx, admin); err != nil {
		return fmt.Errorf("创建默认 admin 用户失败: %w", err)
	}
	s.logger.Info("已创建默认 admin 用户",
		"userId", admin.UserID,
		"username", admin.Username,
	)
	return nil
}

// Login 验证用户名密码并返回 JWT。
//
// 用户不存在或密码错误均返回统一的认证失败错误（避免泄露用户是否存在）。
func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	if s.store == nil {
		return "", errors.New("user store 未初始化")
	}
	user, err := s.store.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", ErrInvalidCredentials
		}
		return "", fmt.Errorf("查询用户失败: %w", err)
	}
	if err := CheckPassword(user.Password, password); err != nil {
		return "", ErrInvalidCredentials
	}
	token, err := s.tm.Generate(user)
	if err != nil {
		return "", fmt.Errorf("生成 token 失败: %w", err)
	}
	s.logger.Info("用户登录成功",
		"userId", user.UserID,
		"username", user.Username,
	)
	return token, nil
}

// CreateUser 创建用户。
//
// plainPassword 为明文密码，内部会进行哈希后存储。
// UserID 为空时自动生成；时间戳为空时自动设置。
func (s *Service) CreateUser(ctx context.Context, user *User, plainPassword string) error {
	if s.store == nil {
		return errors.New("user store 未初始化")
	}
	if user == nil {
		return errors.New("user 不能为 nil")
	}
	if user.Username == "" {
		return errors.New("username 不能为空")
	}
	if plainPassword == "" {
		return errors.New("password 不能为空")
	}

	hashed, err := HashPassword(plainPassword)
	if err != nil {
		return fmt.Errorf("哈希密码失败: %w", err)
	}
	if user.UserID == "" {
		user.UserID = NewUserID()
	}
	user.Password = hashed
	now := time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now

	if err := s.store.Create(ctx, user); err != nil {
		return fmt.Errorf("创建用户失败: %w", err)
	}
	return nil
}

// GetUser 查询用户。
func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	if s.store == nil {
		return nil, errors.New("user store 未初始化")
	}
	return s.store.GetByID(ctx, userID)
}

// ListUsers 列出所有用户。
func (s *Service) ListUsers(ctx context.Context) ([]*User, error) {
	if s.store == nil {
		return nil, errors.New("user store 未初始化")
	}
	return s.store.List(ctx)
}

// DeleteUser 删除用户。
func (s *Service) DeleteUser(ctx context.Context, userID string) error {
	if s.store == nil {
		return errors.New("user store 未初始化")
	}
	return s.store.Delete(ctx, userID)
}

// HasPermission 检查给定角色列表是否拥有指定权限。
func (s *Service) HasPermission(roles []string, permission string) bool {
	return HasPermission(roles, permission)
}

// CanAccessNode 检查用户是否有权访问指定 Node。
//
// admin 角色可访问所有 Node；其他角色只能访问与自己 CommunityID 相同的 Node。
// CommunityID 为空的非 admin 用户无法访问任何 Node。
func (s *Service) CanAccessNode(claims *Claims, node *model.Node) bool {
	if claims == nil || node == nil {
		return false
	}
	if containsString(claims.Roles, RoleAdmin) {
		return true
	}
	return claims.CommunityID != "" && claims.CommunityID == node.OwnerCommunityID
}

// ErrInvalidCredentials 表示用户名或密码错误。
var ErrInvalidCredentials = errors.New("用户名或密码错误")
