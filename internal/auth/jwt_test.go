package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHashAndCheckPassword 测试密码哈希与校验：正确密码通过，错误密码失败。
func TestHashAndCheckPassword(t *testing.T) {
	plain := "mySecretPassword123!"

	hashed, err := HashPassword(plain)
	require.NoError(t, err)
	assert.NotEmpty(t, hashed)
	assert.NotEqual(t, plain, hashed)

	// 正确密码校验通过
	err = CheckPassword(hashed, plain)
	assert.NoError(t, err)

	// 错误密码校验失败
	err = CheckPassword(hashed, "wrongPassword")
	assert.Error(t, err)

	// 相同明文哈希结果不同（bcrypt 自带随机盐）
	hashed2, err := HashPassword(plain)
	require.NoError(t, err)
	assert.NotEqual(t, hashed, hashed2)
}

// TestTokenGenerateAndParse 测试 JWT 生成与解析的往返流程。
func TestTokenGenerateAndParse(t *testing.T) {
	tm := NewTokenManager("test-secret-key", time.Hour)
	user := &User{
		UserID:      "u-1",
		Username:    "alice",
		Roles:       []string{"admin"},
		CommunityID: "comm-1",
	}

	token, err := tm.Generate(user)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := tm.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, "u-1", claims.UserID)
	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, []string{"admin"}, claims.Roles)
	assert.Equal(t, "comm-1", claims.CommunityID)
	assert.Equal(t, "openspace-os-core", claims.Issuer)
	assert.False(t, claims.ExpiresAt.Time.IsZero())
}

// TestTokenParseExpired 测试过期 token 解析失败。
func TestTokenParseExpired(t *testing.T) {
	// 有效期设为 1 秒，等待过期后解析
	tm := NewTokenManager("test-secret-key", 1*time.Second)
	user := &User{
		UserID:   "u-2",
		Username: "bob",
		Roles:    []string{"viewer"},
	}
	token, err := tm.Generate(user)
	require.NoError(t, err)

	// 等待 token 过期（JWT 时间精度为秒，需等待超过 1 秒）
	time.Sleep(1100 * time.Millisecond)

	// 过期 token 解析应失败
	_, err = tm.Parse(token)
	assert.Error(t, err)
}

// TestTokenParseInvalid 测试无效 token（签名错误、格式错误）解析失败。
func TestTokenParseInvalid(t *testing.T) {
	tm := NewTokenManager("correct-secret", time.Hour)

	// 用不同密钥签发的 token
	otherTM := NewTokenManager("wrong-secret", time.Hour)
	token, err := otherTM.Generate(&User{UserID: "u-3", Username: "carol", Roles: []string{"viewer"}})
	require.NoError(t, err)

	// 用正确密钥解析应失败（签名不匹配）
	_, err = tm.Parse(token)
	assert.Error(t, err)

	// 格式错误的 token
	_, err = tm.Parse("not.a.valid.token")
	assert.Error(t, err)

	// 空字符串
	_, err = tm.Parse("")
	assert.Error(t, err)
}

// TestTokenRefresh 测试 Token 刷新：生成 → 刷新 → 解析新 token。
func TestTokenRefresh(t *testing.T) {
	tm := NewTokenManager("refresh-secret", time.Hour)
	user := &User{
		UserID:      "u-4",
		Username:    "dave",
		Roles:       []string{"operator"},
		CommunityID: "comm-2",
	}
	token, err := tm.Generate(user)
	require.NoError(t, err)

	// 刷新 token
	newToken, err := tm.Refresh(token)
	require.NoError(t, err)
	assert.NotEmpty(t, newToken)

	// 解析新 token 验证内容一致
	claims, err := tm.Parse(newToken)
	require.NoError(t, err)
	assert.Equal(t, "u-4", claims.UserID)
	assert.Equal(t, "dave", claims.Username)
	assert.Equal(t, []string{"operator"}, claims.Roles)
	assert.Equal(t, "comm-2", claims.CommunityID)

	// 新 token 的过期时间应晚于旧 token
	oldClaims, err := tm.Parse(token)
	require.NoError(t, err)
	assert.True(t, claims.ExpiresAt.Time.After(oldClaims.ExpiresAt.Time) || claims.ExpiresAt.Time.Equal(oldClaims.ExpiresAt.Time))
}

// TestTokenRefreshExpired 测试过期 token 仍可刷新。
func TestTokenRefreshExpired(t *testing.T) {
	// 生成 1 秒过期的 token
	tm := NewTokenManager("refresh-secret", 1*time.Second)
	user := &User{
		UserID:   "u-5",
		Username: "eve",
		Roles:    []string{"viewer"},
	}
	token, err := tm.Generate(user)
	require.NoError(t, err)

	// 等待 token 过期
	time.Sleep(1100 * time.Millisecond)

	// 过期 token 解析失败
	_, err = tm.Parse(token)
	assert.Error(t, err)

	// 但可以刷新（Refresh 跳过 claims 校验）
	newTM := NewTokenManager("refresh-secret", time.Hour)
	newToken, err := newTM.Refresh(token)
	require.NoError(t, err)

	// 刷新后的 token 可正常解析
	claims, err := newTM.Parse(newToken)
	require.NoError(t, err)
	assert.Equal(t, "u-5", claims.UserID)
}

// TestTokenManagerDefaultDuration 测试 duration <= 0 时使用默认有效期。
func TestTokenManagerDefaultDuration(t *testing.T) {
	tm := NewTokenManager("secret", 0)
	assert.Equal(t, TokenDurationDefault, tm.Duration())

	tm2 := NewTokenManager("secret", -1)
	assert.Equal(t, TokenDurationDefault, tm2.Duration())
}

// TestTokenGenerateNilUser 测试对 nil user 生成 token 返回错误。
func TestTokenGenerateNilUser(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	_, err := tm.Generate(nil)
	assert.Error(t, err)
}
