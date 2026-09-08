package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ClientService 是机器接入方（Client）的领域服务。
//
// 封装客户端的创建、吊销、列表，以及基于 API Key 换取 JWT 的逻辑。
type ClientService struct {
	store  ClientStore
	tm     *TokenManager
	logger *slog.Logger
}

// NewClientService 创建 ClientService。
//
// store 与 tm 不能为 nil。
func NewClientService(store ClientStore, tm *TokenManager, logger *slog.Logger) *ClientService {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil {
		panic("auth: ClientStore 不能为 nil")
	}
	if tm == nil {
		panic("auth: TokenManager 不能为 nil")
	}
	return &ClientService{store: store, tm: tm, logger: logger}
}

// CreateClient 创建客户端并返回其 API Key 明文。
//
// 明文 Key 仅在创建时返回一次，后续只能通过重新生成获取。
func (s *ClientService) CreateClient(ctx context.Context, c *Client) (string, error) {
	if c == nil {
		return "", errors.New("client 不能为 nil")
	}
	if c.Name == "" {
		return "", errors.New("name 不能为空")
	}
	if len(c.Roles) == 0 {
		// 默认只读角色，显式授予更高权限由调用方控制
		c.Roles = []string{RoleOperator}
	}

	plain, hash, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}
	c.APIKeyHash = hash
	normalizeClient(c)

	if err := s.store.Create(ctx, c); err != nil {
		return "", fmt.Errorf("创建客户端失败: %w", err)
	}
	s.logger.Info("已创建机器客户端",
		"clientId", c.ClientID,
		"name", c.Name,
		"module", c.ModuleName,
		"community", c.CommunityID,
	)
	return plain, nil
}

// ExchangeAPIKey 校验 APIKey 并为对应客户端签发 JWT。
//
// 客户端不存在、已吊销或 Key 不匹配时均返回认证失败错误。
func (s *ClientService) ExchangeAPIKey(ctx context.Context, apiKey string) (string, *Client, error) {
	if apiKey == "" {
		return "", nil, ErrInvalidCredentials
	}
	// 目前按 ClientID 前缀查找不现实，因为 Key 为随机无标识——退化说明：
	// 需遍历匹配哈希。为 O(1)，CLI 场景建议直接签发；此处先实现遍历，
	// 命中后校验 bcrypt。
	clients, err := s.store.List(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("查询客户端列表失败: %w", err)
	}
	var matched *Client
	for _, c := range clients {
		if c.Status == ClientStatusRevoked {
			continue
		}
		if c.APIKeyHash != "" && VerifyAPIKey(apiKey, c.APIKeyHash) {
			matched = c
			break
		}
	}
	if matched == nil {
		return "", nil, ErrInvalidCredentials
	}
	token, err := s.tm.GenerateClientToken(matched)
	if err != nil {
		return "", nil, fmt.Errorf("签发客户端 token 失败: %w", err)
	}
	return token, matched, nil
}

// GetClient 按 ID 查询客户端。
func (s *ClientService) GetClient(ctx context.Context, clientID string) (*Client, error) {
	return s.store.GetByID(ctx, clientID)
}

// ListClients 列出所有客户端。
func (s *ClientService) ListClients(ctx context.Context) ([]*Client, error) {
	return s.store.List(ctx)
}

// RevokeClient 吊销客户端，使其 API Key 与已签发 token 全部失效。
//
// 吊销后客户端无法再换取新 token；已签发 token 由校验端按入库状态拒绝。
func (s *ClientService) RevokeClient(ctx context.Context, clientID string) error {
	c, err := s.store.GetByID(ctx, clientID)
	if err != nil {
		return err
	}
	c.Status = ClientStatusRevoked
	c.APIKeyHash = ""
	if err := s.store.Update(ctx, c); err != nil {
		return fmt.Errorf("吊销客户端失败: %w", err)
	}
	s.logger.Info("已吊销机器客户端", "clientId", clientID)
	return nil
}

// DeleteClient 删除客户端。
func (s *ClientService) DeleteClient(ctx context.Context, clientID string) error {
	return s.store.Delete(ctx, clientID)
}

// ClientStoreFor returns 该服务的 ClientStore，供装配/测试使用。
func (s *ClientService) ClientStore() ClientStore {
	return s.store
}