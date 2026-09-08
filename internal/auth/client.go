package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// apiKeyPrefix 是 API Key 的可读前缀，便于识别与审计。
const apiKeyPrefix = "aos_"

// apiKeyTokenBytes 是 API Key 随机部分（明文）的字节数；
// 明文 Key 对外完整下发，库中仅存哈希。
const apiKeyTokenBytes = 32

// Client 表示一个机器接入方（模块/租户）身份。
//
// 用于承载上层模块对 Core 接口的调用，与自然人 User 区分。
// APIKeyHash 不参与 JSON 序列化，避免泄露。
type Client struct {
	// ClientID 客户端唯一标识。
	ClientID string `json:"clientId"`
	// Name 客户端名称（模块名）。
	Name string `json:"name"`
	// ModuleName 客户端所属模块名，便于追溯调用来源。
	ModuleName string `json:"moduleName,omitempty"`
	// CommunityID 所属社区/租户 ID，用于 Node 级权限隔离与用量归集。
	CommunityID string `json:"communityId,omitempty"`
	// Roles 客户端拥有的角色列表（沿用 RBAC permission）。
	Roles []string `json:"roles"`
	// APIKeyHash 存储的 API Key 哈希，不参与 JSON 序列化。
	APIKeyHash string `json:"-"`
	// Status 客户端状态："active" / "revoked"。
	Status string `json:"status"`
	// CreatedAt 创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// ClientStatus 客户端状态常量。
const (
	// ClientStatusActive 表示客户端可正常调用。
	ClientStatusActive = "active"
	// ClientStatusRevoked 表示客户端已吊销，token 校验时拒绝。
	ClientStatusRevoked = "revoked"
)

// NewClientID 生成新的客户端 ID（UUID v4）。
func NewClientID() string {
	return uuid.NewString()
}

// GenerateAPIKey 生成一个新的 API Key。
//
// 返回明文 Key 与其哈希。明文 Key 仅此一次返回，调用方需下发令牌。
// 格式为 `aos_<base64url(32 随机字节)>`。
func GenerateAPIKey() (plain, hash string, err error) {
	buf := make([]byte, apiKeyTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("生成随机 API Key 失败: %w", err)
	}
	plain = apiKeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	hash, err = hashAPIKey(plain)
	if err != nil {
		return "", "", err
	}
	return plain, hash, nil
}

// hashAPIKey 对明文 API Key 做 bcrypt 哈希。
func hashAPIKey(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("哈希 API Key 失败: %w", err)
	}
	return string(h), nil
}

// VerifyAPIKey 校验明文 API Key 与存储哈希是否匹配。
//
// bcrypt.CompareHashAndPassword 为常数时间比较，避免时序侧信道。
func VerifyAPIKey(plain, hash string) bool {
	if plain == "" || hash == "" {
		return false
	}
	if err := validateAPIKeyFormat(plain); err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// validateAPIKeyFormat 校验明文 API Key 是否符合预期格式。
func validateAPIKeyFormat(plain string) error {
	if !strings.HasPrefix(plain, apiKeyPrefix) {
		return errors.New("API Key 格式无效")
	}
	return nil
}

// normalizeClient 填充客户端缺省字段（ID、时间戳、状态）。
func normalizeClient(c *Client) {
	if c == nil {
		return
	}
	if c.ClientID == "" {
		c.ClientID = NewClientID()
	}
	if c.Status == "" {
		c.Status = ClientStatusActive
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
}