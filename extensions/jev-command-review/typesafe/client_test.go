package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New("test-key", WithBaseURL(srv.URL))
}

func TestEvaluateSendsRequestAndParsesNoulAnswers(t *testing.T) {
	var got struct {
		Model     string                     `json:"model"`
		State     map[string]string          `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/systemone", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"model":"jev-latest","answers":{"read_only":{"type":"noul","noul":0.93}},"usage":{"input_tokens":120,"output_tokens":5}}`))
	})

	resp, err := c.Evaluate(context.Background(), Request{
		Model: "jev-latest",
		State: map[string]string{"command": "ls -la"},
		Questions: map[string]Question{
			"read_only": {Type: QuestionNoul, Instructions: "Does `command` only read?"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "jev-latest", got.Model)
	assert.Equal(t, "ls -la", got.State["command"])
	assert.JSONEq(t, `{"type":"noul","instructions":"Does `+"`command`"+` only read?"}`, string(got.Questions["read_only"]))

	assert.Equal(t, "jev-latest", resp.Model)
	assert.InDelta(t, 0.93, resp.Answers["read_only"].Noul, 1e-9)
	assert.Equal(t, 120, resp.Usage.InputTokens)
}

func TestEvaluateReturnsAPIErrorsWithoutRetrying(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid api key"}`))
	})

	_, err := c.Evaluate(context.Background(), Request{Model: "m", State: "x", Questions: map[string]Question{"q": {Type: QuestionNoul, Instructions: "?"}}})
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.Contains(t, apiErr.Body, "invalid api key")
	assert.True(t, apiErr.IsAuth())
	assert.Equal(t, int32(1), calls.Load())
}

func TestEvaluateStopsWhenContextCancelled(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// 读完请求体后服务端才会检测到客户端断开，r.Context() 才会被取消。
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := c.Evaluate(ctx, Request{Model: "m", State: "x", Questions: map[string]Question{"q": {Type: QuestionNoul, Instructions: "?"}}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}

// 兼容服务（Base URL 可以是自建的）返回的答案不合法时报错，不能当成"是"的概率很低：
// 调用方据此放行命令，宁可审核失败。
func TestEvaluateRejectsInvalidAnswer(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","answers":{"q":{"type":"noul","noul":1.5}}}`,
		`{"model":"m","answers":{"q":{"type":"noul","noul":-0.1}}}`,
		`{"model":"m","answers":{"q":{"type":"noul"}}}`,
		`{"model":"m","answers":{"q":{"type":"bool","value":true}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			})

			_, err := c.Evaluate(context.Background(), Request{Model: "m", State: "x", Questions: map[string]Question{"q": {Type: QuestionNoul, Instructions: "?"}}})
			assert.Error(t, err)
		})
	}
}

func TestEvaluateRejectsAnswerMissingFromResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"jev-latest","answers":{}}`))
	})

	_, err := c.Evaluate(context.Background(), Request{Model: "m", State: "x", Questions: map[string]Question{"q": {Type: QuestionNoul, Instructions: "?"}}})
	assert.ErrorContains(t, err, `missing answer "q"`)
}
