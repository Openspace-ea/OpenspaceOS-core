package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func TestClientService_CreateExchangeRevoke(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	defer db.Close()

	ctx := context.Background()
	store := NewSQLiteClientStore(db)
	require.NotNil(t, store)
	require.NoError(t, store.InitSchema(ctx))

	tm := NewTokenManager("test-secret", time.Hour)
	svc := NewClientService(store, tm, nil)

	// 创建客户端
	client := &Client{Name: "ssa-watcher", ModuleName: "SSA-Watcher", CommunityID: "comm-1", Roles: []string{RoleOperator}}
	apiKey, err := svc.CreateClient(ctx, client)
	require.NoError(t, err)
	require.NotEmpty(t, apiKey)
	require.NotEmpty(t, client.ClientID)
	require.NotEmpty(t, client.APIKeyHash)
	require.Equal(t, ClientStatusActive, client.Status)

	// 用 API Key 换取 JWT
	token, got, err := svc.ExchangeAPIKey(ctx, apiKey)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, client.ClientID, got.ClientID)
	require.NotEmpty(t, token)

	// 解析 JWT：应标记为 machine client
	claims, err := tm.Parse(token)
	require.NoError(t, err)
	require.Equal(t, SubjectTypeClient, claims.SubjectType)
	require.Equal(t, client.ClientID, claims.ClientID)
	require.Equal(t, "comm-1", claims.CommunityID)
	require.Equal(t, []string{RoleOperator}, claims.Roles)

	// 伪造 API Key 应失败
	_, _, err = svc.ExchangeAPIKey(ctx, "aos_invalid-key-token-000000000000")
	require.ErrorIs(t, err, ErrInvalidCredentials)

	// 吊销后无法换取
	require.NoError(t, svc.RevokeClient(ctx, client.ClientID))
	_, _, err = svc.ExchangeAPIKey(ctx, apiKey)
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestGenerateAPIKey(t *testing.T) {
	plain, hash, err := GenerateAPIKey()
	require.NoError(t, err)
	require.True(t, len(plain) > len("aos_"))
	require.NotEmpty(t, hash)
	require.True(t, VerifyAPIKey(plain, hash))
	require.False(t, VerifyAPIKey(plain+"x", hash))
	require.False(t, VerifyAPIKey("", hash))
}