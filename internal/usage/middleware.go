package usage

import (
	"context"
	"net/http"
	"time"

	"github.com/openspace-os/openspace-os-core/internal/auth"
)

// pathBase 取 URL 路径，忽略查询参数，作为计量端点标识。
func pathBase(r *http.Request) string {
	return r.URL.Path
}

// resolveTenant 从 context 的 Claims 中解析租户与客户端身份。
//
// 未认证（无 Claims）时返回空身份，便于在未启用认证的开发模式下仍可计量
// （租户/客户端为空，归到匿名一档）。
func resolveTenant(ctx context.Context) (tenantID, clientID string) {
	claims := auth.ClaimsFromContext(ctx)
	if claims == nil {
		return "", ""
	}
	return claims.CommunityID, claims.ClientID
}

// HTTPMiddleware 返回一个 HTTP 用法计量中间件（T3.2）。
//
// 在每个被采样的请求处理完成后：解析主体（tenant/client）与端点，
// 投递一条 UnitCall 计量记录到 Collector，并同步埋点 OTel metrics。
//
// collector 或 metrics 为 nil 时对应能力自动禁用。
func HTTPMiddleware(collector *Collector, metrics *Metrics) func(http.Handler) http.Handler {
	if collector == nil && metrics == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r)

			endpoint := pathBase(r)
			op := methodToOperation(r.Method)
			tenantID, clientID := resolveTenant(r.Context())
			rc := Record{
				TenantID:  tenantID,
				ClientID:  clientID,
				Endpoint:  endpoint,
				Operation: op,
				Unit:      UnitCall,
				Count:     1,
				At:        start,
			}
			if (collector != nil) {
				_ = collector.Record(context.Background(), rc)
			}
			if metrics != nil {
				metrics.RecordTotal(r.Context(), rc)
			}
		})
	}
}

// methodToOperation 将 HTTP 方法映射为计量端的操作类型。
func methodToOperation(method string) string {
	switch method {
	case http.MethodGet:
		return "read"
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return "other"
	}
}

// statusWriter 包装 ResponseWriter 以记录响应状态码。
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}