package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenDurationDefault 是默认的 Token 有效期（24 小时）。
const TokenDurationDefault = 24 * time.Hour

// tokenIssuer 是 JWT 的签发者标识。
const tokenIssuer = "openspace-os-core"

// Claims 是 JWT 的自定义声明结构。
//
// 在标准声明之外扩展了用户/客户端身份相关字段。
type Claims struct {
	// SubjectType 主体类型："user"（自然人）或 "client"（机器接入方）。
	SubjectType string `json:"subjectType,omitempty"`
	// UserID 用户唯一标识（SubjectType=user 时有效）。
	UserID string `json:"userId"`
	// ClientID 客户端唯一标识（SubjectType=client 时有效）。
	ClientID string `json:"clientId,omitempty"`
	// Username 登录用户名。
	Username string `json:"username"`
	// Roles 主体拥有的角色列表。
	Roles []string `json:"roles"`
	// CommunityID 所属社区/租户 ID。
	CommunityID string `json:"communityId,omitempty"`
	// RegisteredClaims JWT 标准声明（过期时间、签发者等）。
	jwt.RegisteredClaims
}

// 主体类型常量。
const (
	// SubjectTypeUser 表示自然人士体。
	SubjectTypeUser = "user"
	// SubjectTypeClient 表示机器接入方主体。
	SubjectTypeClient = "client"
)

// TokenManager 负责 JWT 的生成、解析与刷新。
type TokenManager struct {
	secretKey []byte
	issuer    string
	duration  time.Duration
}

// NewTokenManager 创建 TokenManager。
//
// secretKey 为签名密钥，duration 为 Token 有效期，
// duration <= 0 时使用默认值 24 小时。
func NewTokenManager(secretKey string, duration time.Duration) *TokenManager {
	if duration <= 0 {
		duration = TokenDurationDefault
	}
	return &TokenManager{
		secretKey: []byte(secretKey),
		issuer:    tokenIssuer,
		duration:  duration,
	}
}

// Generate 为指定用户生成 JWT。
func (tm *TokenManager) Generate(user *User) (string, error) {
	if user == nil {
		return "", errors.New("user 不能为 nil")
	}
	now := time.Now()
	claims := Claims{
		SubjectType: SubjectTypeUser,
		UserID:      user.UserID,
		Username:    user.Username,
		Roles:       user.Roles,
		CommunityID: user.CommunityID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tm.issuer,
			Subject:   user.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tm.duration)),
		},
	}
	return tm.sign(claims)
}

// GenerateClientToken 为机器接入方（Client）生成 JWT。
//
// 若客户端为吊销状态则返回错误。
func (tm *TokenManager) GenerateClientToken(client *Client) (string, error) {
	if client == nil {
		return "", errors.New("client 不能为 nil")
	}
	if client.Status == ClientStatusRevoked {
		return "", errors.New("客户端已吊销")
	}
	now := time.Now()
	claims := Claims{
		SubjectType: SubjectTypeClient,
		ClientID:    client.ClientID,
		Username:    client.Name,
		Roles:       client.Roles,
		CommunityID: client.CommunityID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tm.issuer,
			Subject:   client.ClientID,
			Audience:  []string{"openspace-os-core-api"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tm.duration)),
		},
	}
	return tm.sign(claims)
}

// sign 用当前密钥签发给定 claims 的 JWT。内部复用签发逻辑。
func (tm *TokenManager) sign(claims Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(tm.secretKey)
	if err != nil {
		return "", fmt.Errorf("签发 token 失败: %w", err)
	}
	return signed, nil
}

// Parse 解析并验证 JWT 字符串，返回 Claims。
//
// 验证签名与过期时间，无效 token 返回错误。
func (tm *TokenManager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("意外的签名方法: %v", t.Header["alg"])
		}
		return tm.secretKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("解析 token 失败: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("token 无效")
	}
	// 显式校验过期时间，确保过期 token 一定被拒绝
	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("解析 token 失败: token 已过期")
	}
	return claims, nil
}

// Refresh 刷新 Token：解析旧 Token 并签发一个新的（重置过期时间）。
//
// 旧 Token 必须仍可被解析（签名正确）；过期 Token 也可刷新，
// 以避免用户在过期后被强制重新登录。如需禁止过期刷新，
// 可在 Parse 失败时直接返回错误。
func (tm *TokenManager) Refresh(tokenString string) (string, error) {
	// 允许过期 Token 刷新：解析时跳过 claims 校验（含过期校验），
	// 但仍验证签名以确保 token 确由本服务签发
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("意外的签名方法: %v", t.Header["alg"])
		}
		return tm.secretKey, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		return "", fmt.Errorf("解析 token 失败: %w", err)
	}

	// 用解析出的身份信息重新签发
	now := time.Now()
	claims.Issuer = tm.issuer
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(tm.duration))

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(tm.secretKey)
	if err != nil {
		return "", fmt.Errorf("签发 token 失败: %w", err)
	}
	return signed, nil
}

// Duration 返回 Token 有效期，供 API 返回 expiresIn 使用。
func (tm *TokenManager) Duration() time.Duration {
	return tm.duration
}
