// Package typesafe 是 TypeSafe System One API（Jev 模型）的最小 HTTP 客户端。
//
// 只覆盖命令审核用到的部分：POST /v1/systemone，noul（是/否）题。
// API 文档：https://docs.typesafe.ai/api
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// DefaultBaseURL 是 TypeSafe 官方 API 地址。
const DefaultBaseURL = "https://api.typesafe.ai"

const (
	defaultMaxRetries = 3
	// statusOverloaded 是 TypeSafe 过载时返回的非标准状态码。
	statusOverloaded = 529
	// maxErrorBody 限制错误响应体的读取长度，只用于错误信息展示。
	maxErrorBody = 4096
)

// QuestionNoul 是是/否题：答案为"是"的概率（0~1）。
const QuestionNoul = "noul"

// Question 是一道题。Instructions 可以是字符串，也可以是结构化对象。
type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// Request 是一次评估请求：同一份 State 上并行回答多道题。
type Request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Answer 是一道题的答案。
type Answer struct {
	Type string  `json:"type"`
	Noul float64 `json:"noul"`
}

// UnmarshalJSON 要求是/否题的答案带着概率：缺了这个字段时解码失败，不能当成概率 0。
func (a *Answer) UnmarshalJSON(b []byte) error {
	var raw struct {
		Type string   `json:"type"`
		Noul *float64 `json:"noul"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	a.Type = raw.Type
	if raw.Type == QuestionNoul {
		if raw.Noul == nil {
			return fmt.Errorf("noul answer without a probability")
		}
		a.Noul = *raw.Noul
	}
	return nil
}

// check 检查答案和题目对得上：题型一致，是/否题的概率在 0~1 之间。
func (a Answer) check(q Question) error {
	if a.Type != q.Type {
		return fmt.Errorf("answer type %q, want %q", a.Type, q.Type)
	}
	if a.Type == QuestionNoul && (a.Noul < 0 || a.Noul > 1) {
		return fmt.Errorf("noul probability %v out of range", a.Noul)
	}
	return nil
}

// Usage 是一次请求消耗的 token。
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response 是评估结果。Model 是实际作答的版本号（别名会被解析成具体版本）。
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// APIError 是非 2xx 响应。
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("typesafe: HTTP %d: %s", e.StatusCode, e.Body)
}

// IsAuth 报告是否为 API key 无效或无权限。
func (e *APIError) IsAuth() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// Client 调用 TypeSafe System One API。
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	maxRetries int
	backoff    func(attempt int) time.Duration
}

// Option 配置 Client。
type Option func(*Client)

// WithBaseURL 替换 API 地址，可指向兼容 System One API 的服务。
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient 替换底层 http.Client。
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.httpClient = hc } }

// WithBackoff 替换重试间隔（attempt 从 1 开始）。
func WithBackoff(f func(attempt int) time.Duration) Option {
	return func(c *Client) { c.backoff = f }
}

// New 创建客户端。超时由调用方通过 ctx 控制。
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:     apiKey,
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{Transport: http.DefaultTransport},
		maxRetries: defaultMaxRetries,
		backoff:    func(attempt int) time.Duration { return time.Duration(attempt) * 500 * time.Millisecond },
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Evaluate 发送一次评估请求。429 / 529 按退避重试；其余非 2xx 直接返回 *APIError。
// 返回的 Response 保证包含请求里的每一道题，且每个答案的题型和取值都合法——服务地址可以是
// 任意兼容的服务，答案不合法时报错，不交给调用方当成正常的评分。
func (c *Client) Evaluate(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("typesafe: encode request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		resp, retryAfter, err := c.post(ctx, body)
		if err == nil {
			for id, q := range req.Questions {
				a, ok := resp.Answers[id]
				if !ok {
					return nil, fmt.Errorf("typesafe: missing answer %q in response", id)
				}
				if err := a.check(q); err != nil {
					return nil, fmt.Errorf("typesafe: invalid answer %q: %w", id, err)
				}
			}
			return resp, nil
		}
		apiErr, ok := err.(*APIError)
		if !ok || !retryable(apiErr.StatusCode) || attempt >= c.maxRetries {
			return nil, err
		}
		wait := c.backoff(attempt + 1)
		if retryAfter > 0 {
			wait = retryAfter
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == statusOverloaded
}

// post 发送一次请求；失败时返回 *APIError 或传输错误，以及服务端建议的重试间隔。
func (c *Client) post(ctx context.Context, body []byte) (*Response, time.Duration, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("typesafe: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("typesafe: send request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(httpResp.Body, maxErrorBody))
		return nil, parseRetryAfter(httpResp.Header.Get("Retry-After")), &APIError{StatusCode: httpResp.StatusCode, Body: string(msg)}
	}

	var out Response
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("typesafe: decode response: %w", err)
	}
	return &out, 0, nil
}

// parseRetryAfter 只识别秒数形式；无法识别时返回 0，由退避策略决定间隔。
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
