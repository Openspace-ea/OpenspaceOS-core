// Package main 提供 Openspace OS CLI 工具的 HTTP 客户端封装。
//
// Client 封装了与 Openspace OS Core REST API 交互的常用 HTTP 方法，
// 自动处理 JSON 序列化/反序列化、认证头注入与统一错误处理。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client 是与 Openspace OS Core REST API 交互的 HTTP 客户端。
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient 创建新的 HTTP 客户端。
// baseURL 为 Core 服务地址（如 http://localhost:8080），token 为 JWT 令牌（可为空）。
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// do 构造并发送 HTTP 请求，自动添加 Content-Type 与 Authorization 头。
// body 为 nil 时不发送请求体。
func (c *Client) do(method, path string, body any) (*http.Response, error) {
	url := c.baseURL + path
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.httpClient.Do(req)
}

// Get 发送 GET 请求。
func (c *Client) Get(path string) (*http.Response, error) {
	return c.do(http.MethodGet, path, nil)
}

// Post 发送 POST 请求，body 会被序列化为 JSON。
func (c *Client) Post(path string, body any) (*http.Response, error) {
	return c.do(http.MethodPost, path, body)
}

// Put 发送 PUT 请求，body 会被序列化为 JSON。
func (c *Client) Put(path string, body any) (*http.Response, error) {
	return c.do(http.MethodPut, path, body)
}

// Delete 发送 DELETE 请求。
func (c *Client) Delete(path string) (*http.Response, error) {
	return c.do(http.MethodDelete, path, nil)
}

// Stream 发送 GET 请求并返回流式响应体（用于 SSE 订阅）。
// 该请求不设置超时，以便持续接收事件；调用方负责关闭返回的 ReadCloser。
func (c *Client) Stream(path string) (io.ReadCloser, error) {
	url := c.baseURL + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// 流式请求使用独立的 http.Client，不设置超时
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("服务器返回 %d: %s", resp.StatusCode, string(data))
	}
	return resp.Body, nil
}

// readResponse 读取响应体并按状态码校验。
// 当状态码不在 2xx 范围时返回包含响应体的错误。
// v 不为 nil 且响应体非空时，将响应体反序列化到 v。
func readResponse(resp *http.Response, v any) error {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	if v != nil && len(data) > 0 {
		if err := json.Unmarshal(data, v); err != nil {
			return fmt.Errorf("解析响应 JSON 失败: %w", err)
		}
	}
	return nil
}