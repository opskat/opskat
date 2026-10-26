package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/extensions/jev-command-review/jevreview"
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// 测试经 SDK 的 TestHost 跑：Settings() 读它预置的设置，HTTP 走它的 mock。宿主在真实运行时
// 会把 {{setting.api_key}} 换成密钥；TestHost 不替换，正好能看到扩展写进请求头的就是占位。

const placeholder = "{{setting.api_key}}"

// configured 是用户在设置页保存过后 settings_get 交给扩展的样子：密钥只有占位。
func configured(extra map[string]string) map[string]string {
	s := map[string]string{"api_key": placeholder, "base_url": "https://api.typesafe.ai/", "model": "jev-latest", "threshold": "0.5"}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

// jevAnswer 回一组是/否题的概率。
func jevAnswer(t *testing.T, w http.ResponseWriter, probs map[string]float64) {
	t.Helper()
	answers := map[string]any{}
	for id, p := range probs {
		answers[id] = map[string]any{"type": "noul", "noul": p}
	}
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"model": "jev-latest", "answers": answers}))
}

func allQuestions(p float64) map[string]float64 {
	return map[string]float64{jevreview.QuestionDestructive: p, jevreview.QuestionDisruptive: p, jevreview.QuestionRemoteCode: p}
}

var item = opskat.ReviewItem{AssetType: "ssh", Command: "rm -rf /data", Syntax: "shell"}

func TestReviewRejectsAboveThresholdAndSendsTheCommand(t *testing.T) {
	var got struct {
		auth, path string
		body       map[string]any
	}
	host := opskat.NewTestHost(opskat.WithMockHTTP(func(w http.ResponseWriter, r *http.Request) {
		got.auth, got.path = r.Header.Get("Authorization"), r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got.body))
		probs := allQuestions(0.01)
		probs[jevreview.QuestionDestructive] = 0.93
		jevAnswer(t, w, probs)
	}))
	defer host.Close()
	host.SetSettings(configured(nil))

	out, err := review(nil, []opskat.ReviewItem{item})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, opskat.ReviewReject, out[0].Outcome)
	assert.Equal(t, []string{jevreview.QuestionDestructive}, out[0].Failed)
	assert.Equal(t, map[string]float64{jevreview.QuestionDestructive: 0.93}, out[0].Scores)

	assert.Equal(t, "Bearer "+placeholder, got.auth, "扩展只写占位，密钥由宿主替换")
	assert.Equal(t, "/v1/systemone", got.path)
	assert.Equal(t, map[string]any{"asset_type": "ssh", "command": "rm -rf /data"}, got.body["state"])
}

func TestReviewPassesBelowThreshold(t *testing.T) {
	host := opskat.NewTestHost(opskat.WithMockHTTP(func(w http.ResponseWriter, r *http.Request) {
		jevAnswer(t, w, allQuestions(0.2))
	}))
	defer host.Close()
	host.SetSettings(configured(map[string]string{"threshold": "0.3"}))

	out, err := review(nil, []opskat.ReviewItem{item})
	require.NoError(t, err)
	assert.Equal(t, opskat.ReviewPass, out[0].Outcome)
}

// 设置不完整时不发请求，每条都按"未配置"失败，宿主据此提醒用户去填设置。
func TestReviewWithoutUsableSettingsIsNotConfigured(t *testing.T) {
	cases := map[string]map[string]string{
		"从没保存过设置":    {},
		"没填 API key": configured(map[string]string{"api_key": ""}),
		"阈值不是 0~1":   configured(map[string]string{"threshold": "1.5"}),
		"阈值不是数":      configured(map[string]string{"threshold": "high"}),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			host := opskat.NewTestHost(opskat.WithMockHTTP(func(http.ResponseWriter, *http.Request) { called = true }))
			defer host.Close()
			host.SetSettings(s)

			out, err := review(nil, []opskat.ReviewItem{item, item})
			require.NoError(t, err)
			for _, r := range out {
				assert.Equal(t, opskat.ReviewError, r.Outcome)
				assert.Equal(t, "not_configured", r.Reason)
			}
			assert.False(t, called)
		})
	}
}

func TestReviewMapsServiceErrors(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        jevreview.ReasonInvalidAPIKey,
		http.StatusInternalServerError: jevreview.ReasonUnavailable,
	}
	for status, reason := range cases {
		t.Run(http.StatusText(status), func(t *testing.T) {
			host := opskat.NewTestHost(opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer host.Close()
			host.SetSettings(configured(nil))

			out, err := review(nil, []opskat.ReviewItem{item})
			require.NoError(t, err)
			assert.Equal(t, opskat.ReviewError, out[0].Outcome)
			assert.Equal(t, reason, out[0].Reason)
		})
	}
}

// 扩展里不重试：超时由宿主控制（到时取消这次调用、重试一次）。扩展自己按 Retry-After 等，
// 会一直等到宿主超时，结果是"超时"，而不是很快报"服务不可用"。
func TestReviewDoesNotRetryInsideTheExtension(t *testing.T) {
	calls := 0
	host := opskat.NewTestHost(opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer host.Close()
	host.SetSettings(configured(nil))

	start := time.Now()
	out, err := review(nil, []opskat.ReviewItem{item})
	require.NoError(t, err)
	assert.Equal(t, opskat.ReviewError, out[0].Outcome)
	assert.Equal(t, jevreview.ReasonUnavailable, out[0].Reason)
	assert.Equal(t, 1, calls)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// 只按声明的题目判定：兼容服务多回一个没问过的答案，不能算进未通过——
// 宿主会把带未声明题目的结果判成无效，整批审核失败。
func TestReviewJudgesOnlyTheDeclaredQuestions(t *testing.T) {
	host := opskat.NewTestHost(opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) {
		probs := allQuestions(0.01)
		probs["extra"] = 0.99
		jevAnswer(t, w, probs)
	}))
	defer host.Close()
	host.SetSettings(configured(nil))

	out, err := review(nil, []opskat.ReviewItem{item})
	require.NoError(t, err)
	assert.Equal(t, opskat.ReviewPass, out[0].Outcome)
	assert.Empty(t, out[0].Failed)
}
