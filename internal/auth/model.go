package auth

import "time"

// User 表示系统中的一个用户身份。
//
// Password 字段存储的是 bcrypt 哈希后的密码，JSON 序列化时会被忽略，
// 避免在 API 响应或日志中泄露密码哈希。
type User struct {
	// UserID 用户唯一标识。
	UserID string `json:"userId"`
	// Username 登录用户名，全局唯一。
	Username string `json:"username"`
	// Password 哈希后的密码，不参与 JSON 序列化。
	Password string `json:"-"`
	// Roles 用户拥有的角色名列表。
	Roles []string `json:"roles"`
	// CommunityID 所属社区 ID，用于 Node 级权限校验。
	CommunityID string `json:"communityId,omitempty"`
	// CreatedAt 创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// Role 表示一个角色及其拥有的权限集合。
type Role struct {
	// Name 角色名称，唯一标识一个角色。
	Name string `json:"name"`
	// Permissions 该角色拥有的权限列表。
	Permissions []string `json:"permissions"`
}
