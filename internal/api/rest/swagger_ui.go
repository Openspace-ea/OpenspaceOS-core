package rest

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed openapi.yaml
var openapiFS embed.FS

// swaggerUITmpl 是 Swagger UI 页面 HTML 模板，使用 CDN 加载 swagger-ui-dist。
// 通过将 specURL 注入模板，避免对外部地址的硬编码。
const swaggerUITmpl = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Openspace OS Core REST API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    html { box-sizing: border-box; overflow: -moz-scrollbars-vertical; overflow-y: scroll; }
    *, *:before, *:after { box-sizing: inherit; }
    body { margin: 0; background: #fafafa; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: "__SPEC_URL__",
        dom_id: '#swagger-ui',
        deepLinking: true,
        docExpansion: 'none',
        operationsSorter: 'alpha',
        tagsSorter: 'alpha',
        persistAuthorization: true,
        displayRequestDuration: true
      });
    };
  </script>
</body>
</html>
`

// specURL 是 Swagger UI 加载 OpenAPI 规范文件的相对路径。
const specURL = "/api/v1/openapi.yaml"

// ServeSwaggerUI 提供 Swagger UI 页面。
// GET /api/v1/docs
//
// 返回内嵌的 Swagger UI HTML 页面，页面通过 CDN 加载 swagger-ui-dist 资源，
// 并指向 /api/v1/openapi.yaml 作为规范文件来源。此端点无需认证。
func (h *Handler) ServeSwaggerUI(w http.ResponseWriter, r *http.Request) {
	// 仅接受 GET 请求
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET 方法")
		return
	}
	html := strings.Replace(swaggerUITmpl, "__SPEC_URL__", specURL, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

// ServeOpenAPISpec 提供 OpenAPI YAML 规范文件。
// GET /api/v1/openapi.yaml
//
// 返回内嵌的 openapi.yaml 文件原始内容。此端点无需认证。
func (h *Handler) ServeOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	// 仅接受 GET 请求
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET 方法")
		return
	}
	data, err := openapiFS.ReadFile("openapi.yaml")
	if err != nil {
		// 内嵌文件理论上不会读取失败，此处兜底处理
		h.log.Error("读取内嵌 openapi.yaml 失败", "error", err)
		writeError(w, http.StatusInternalServerError, "读取 OpenAPI 规范文件失败")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
