package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/openspace-os/openspace-os-core/internal/auth"
)

// loginRequest 是登录请求体。
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse 是登录成功响应体。
type loginResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expiresIn"` // 秒
}

// createUserRequest 是创建用户请求体。
type createUserRequest struct {
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	Roles       []string `json:"roles"`
	CommunityID string   `json:"communityId,omitempty"`
}

// createClientRequest 是创建机器接入方（Client）的请求体。
type createClientRequest struct {
	Name        string   `json:"name"`
	ModuleName  string   `json:"moduleName,omitempty"`
	CommunityID string   `json:"communityId,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

// createClientResponse 是创建客户端的响应体。
//
// ApiKey 为明文的机器接入方可用的 API Key，仅此一次返回，请妥善保存。
type createClientResponse struct {
	Client *auth.Client `json:"client"`
	ApiKey string       `json:"apiKey"`
}

// exchangeTokenRequest 是 API Key 换取 JWT 的请求体。
type exchangeTokenRequest struct {
	ApiKey string `json:"apiKey"`
}

// ListClients 列出所有机器接入方。
// GET /api/v1/clients （需 client.manage 或 admin 权限）
func (h *Handler) ListClients(w http.ResponseWriter, r *http.Request) {
	if h.clientSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "客户端服务未启用")
		return
	}
	clients, err := h.clientSvc.ListClients(r.Context())
	if err != nil {
		h.log.Error("查询客户端列表失败", "error", err)
		writeError(w, http.StatusInternalServerError, "查询客户端列表失败")
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

// CreateClient 创建机器接入方并返回 API Key。
// POST /api/v1/clients （需 admin 权限）
func (h *Handler) CreateClient(w http.ResponseWriter, r *http.Request) {
	if h.clientSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "客户端服务未启用")
		return
	}
	var req createClientRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	for _, role := range req.Roles {
		if _, ok := auth.DefaultRoles[role]; !ok {
			writeError(w, http.StatusBadRequest, "未知角色: "+role)
			return
		}
	}
	client := &auth.Client{
		Name:        req.Name,
		ModuleName:  req.ModuleName,
		CommunityID: req.CommunityID,
		Roles:       req.Roles,
	}
	apiKey, err := h.clientSvc.CreateClient(r.Context(), client)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, createClientResponse{
		Client: client,
		ApiKey: apiKey,
	})
}

// GetClient 查询指定机器接入方。
// GET /api/v1/clients/{clientId}
func (h *Handler) GetClient(w http.ResponseWriter, r *http.Request) {
	if h.clientSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "客户端服务未启用")
		return
	}
	clientID := chi.URLParam(r, "clientId")
	if clientID == "" {
		writeError(w, http.StatusBadRequest, "clientId 不能为空")
		return
	}
	client, err := h.clientSvc.GetClient(r.Context(), clientID)
	if err != nil {
		if errors.Is(err, auth.ErrClientNotFound) {
			writeError(w, http.StatusNotFound, "客户端不存在")
			return
		}
		h.log.Error("查询客户端失败", "error", err)
		writeError(w, http.StatusInternalServerError, "查询客户端失败")
		return
	}
	writeJSON(w, http.StatusOK, client)
}

// RevokeClient 吊销指定的机器接入方。
// DELETE /api/v1/clients/{clientId}
func (h *Handler) RevokeClient(w http.ResponseWriter, r *http.Request) {
	if h.clientSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "客户端服务未启用")
		return
	}
	clientID := chi.URLParam(r, "clientId")
	if clientID == "" {
		writeError(w, http.StatusBadRequest, "clientId 不能为空")
		return
	}
	if err := h.clientSvc.RevokeClient(r.Context(), clientID); err != nil {
		if errors.Is(err, auth.ErrClientNotFound) {
			writeError(w, http.StatusNotFound, "客户端不存在")
			return
		}
		h.log.Error("吊销客户端失败", "error", err)
		writeError(w, http.StatusInternalServerError, "吊销客户端失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ExchangeToken API Key 换取 JWT。
// POST /api/v1/auth/token  body: {"apiKey":"aos_..."}
//
// 允许机器接入方用 API Key 换取短期 JWT（无需认证即可调用）。
func (h *Handler) ExchangeToken(w http.ResponseWriter, r *http.Request) {
	if h.clientSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "客户端服务未启用")
		return
	}
	var req exchangeTokenRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		return
	}
	if req.ApiKey == "" {
		writeError(w, http.StatusBadRequest, "apiKey 不能为空")
		return
	}
	token, _, err := h.clientSvc.ExchangeAPIKey(r.Context(), req.ApiKey)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "apiKey 无效或已吊销")
			return
		}
		h.log.Error("API Key 换取 token 失败", "error", err)
		writeError(w, http.StatusInternalServerError, "换取 token 失败")
		return
	}
	expiresIn := 0
	if h.tokenMgr != nil {
		expiresIn = int(h.tokenMgr.Duration().Seconds())
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		ExpiresIn: expiresIn,
	})
}

// Login 处理用户登录请求。
// POST /api/v1/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "认证服务未启用")
		return
	}
	var req loginRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username 和 password 不能为空")
		return
	}
	token, err := h.authSvc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		h.log.Error("登录失败", "error", err)
		writeError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	expiresIn := 0
	if h.tokenMgr != nil {
		expiresIn = int(h.tokenMgr.Duration().Seconds())
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		ExpiresIn: expiresIn,
	})
}

// RefreshToken 刷新 Token。
// POST /api/v1/auth/refresh
//
// 需要携带有效的 Authorization header。
func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	if h.tokenMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "认证服务未启用")
		return
	}
	tokenStr := extractToken(r)
	if tokenStr == "" {
		writeError(w, http.StatusUnauthorized, "缺少认证 token")
		return
	}
	newToken, err := h.tokenMgr.Refresh(tokenStr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "token 无效")
		return
	}
	expiresIn := int(h.tokenMgr.Duration().Seconds())
	writeJSON(w, http.StatusOK, loginResponse{
		Token:     newToken,
		ExpiresIn: expiresIn,
	})
}

// GetMe 返回当前登录用户的信息。
// GET /api/v1/auth/me
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "未认证")
		return
	}
	// 优先从存储中查询完整用户信息（含时间戳）
	if h.authSvc != nil {
		user, err := h.authSvc.GetUser(r.Context(), claims.UserID)
		if err == nil {
			writeJSON(w, http.StatusOK, user)
			return
		}
	}
	// 回退：返回 Claims 中的信息
	writeJSON(w, http.StatusOK, map[string]any{
		"userId":      claims.UserID,
		"username":    claims.Username,
		"roles":       claims.Roles,
		"communityId": claims.CommunityID,
	})
}

// ListUsers 列出所有用户。
// GET /api/v1/users
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "认证服务未启用")
		return
	}
	users, err := h.authSvc.ListUsers(r.Context())
	if err != nil {
		h.log.Error("查询用户列表失败", "error", err)
		writeError(w, http.StatusInternalServerError, "查询用户列表失败")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// CreateUser 创建用户（仅 admin 可调用，权限由路由中间件保障）。
// POST /api/v1/users
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "认证服务未启用")
		return
	}
	var req createUserRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username 不能为空")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password 不能为空")
		return
	}
	if len(req.Roles) == 0 {
		writeError(w, http.StatusBadRequest, "roles 不能为空")
		return
	}
	// 校验角色是否合法
	for _, role := range req.Roles {
		if _, ok := auth.DefaultRoles[role]; !ok {
			writeError(w, http.StatusBadRequest, "未知角色: "+role)
			return
		}
	}

	user := &auth.User{
		Username:    req.Username,
		Roles:       req.Roles,
		CommunityID: req.CommunityID,
	}
	if err := h.authSvc.CreateUser(r.Context(), user, req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// GetUser 查询指定用户。
// GET /api/v1/users/{userId}
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "认证服务未启用")
		return
	}
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userId 不能为空")
		return
	}
	user, err := h.authSvc.GetUser(r.Context(), userID)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		h.log.Error("查询用户失败", "error", err)
		writeError(w, http.StatusInternalServerError, "查询用户失败")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// DeleteUser 删除指定用户。
// DELETE /api/v1/users/{userId}
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "认证服务未启用")
		return
	}
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userId 不能为空")
		return
	}
	if err := h.authSvc.DeleteUser(r.Context(), userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSONBody 解码 JSON 请求体到目标结构体。
// 解码失败时直接写入 400 错误响应，返回 error 供调用方判断。
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return err
	}
	return nil
}

// extractToken 从 Authorization header 提取 Bearer token。
func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(authHeader) <= len(prefix) {
		return ""
	}
	if authHeader[:len(prefix)] != prefix {
		return ""
	}
	return authHeader[len(prefix):]
}
