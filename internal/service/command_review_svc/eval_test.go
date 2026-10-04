package command_review_svc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/typesafe"
	"github.com/stretchr/testify/require"
)

// 公开评估样本：每行一条命令，标好应有的结果。expect=pass 表示可以自动执行，
// expect=ask 表示应该问人（辅助审批）或拒绝（Autopilot）。不含任何人的真实数据。
const evalSamplesPath = "testdata/eval_commands.jsonl"

type evalSample struct {
	AssetType string `json:"asset_type"`
	Command   string `json:"command"`
	Expect    string `json:"expect"`
	Category  string `json:"category"`
}

func loadEvalSamples(t *testing.T) []evalSample {
	t.Helper()
	f, err := os.Open(evalSamplesPath)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var out []evalSample
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var s evalSample
		require.NoError(t, json.Unmarshal(sc.Bytes(), &s), "line %d", line)
		require.NotEmpty(t, s.AssetType, "line %d", line)
		require.NotEmpty(t, s.Command, "line %d", line)
		require.Contains(t, []string{"pass", "ask"}, s.Expect, "line %d", line)
		out = append(out, s)
	}
	require.NoError(t, sc.Err())
	return out
}

// 样本要覆盖所有接入了权限检查的类型，模型在哪一类上判错才能在评估时暴露出来。
func TestEvalSamplesAreWellFormedAndCoverAssetTypes(t *testing.T) {
	samples := loadEvalSamples(t)
	seen := map[string]map[string]bool{}
	for _, s := range samples {
		if seen[s.AssetType] == nil {
			seen[s.AssetType] = map[string]bool{}
		}
		seen[s.AssetType][s.Expect] = true
	}
	for _, typ := range []string{"ssh", "serial", "k8s", "database", "redis", "mongodb", "kafka", "etcd", "oss", "cp:read", "cp:write"} {
		require.True(t, seen[typ]["pass"] && seen[typ]["ask"], "资产类型 %s 要同时有 pass 和 ask 的样本", typ)
	}
}

type noCache struct{}

func (noCache) Get(context.Context, string) (*Result, error)             { return nil, nil }
func (noCache) Put(context.Context, string, Result, time.Duration) error { return nil }

// TestEvalAgainstJev 用真实的 Jev 审核全部样本并输出指标。需要 API key，平时跳过：
//
//	OPSKAT_TYPESAFE_EVAL_KEY=<key> go test ./internal/service/command_review_svc/ -run TestEvalAgainstJev -v -count=1
//
// 可选：OPSKAT_TYPESAFE_EVAL_BASE_URL（默认 DefaultBaseURL）、OPSKAT_TYPESAFE_EVAL_MODEL（默认 DefaultModel）、OPSKAT_TYPESAFE_EVAL_THRESHOLD（默认 DefaultThreshold）。
func TestEvalAgainstJev(t *testing.T) {
	key := os.Getenv("OPSKAT_TYPESAFE_EVAL_KEY")
	if key == "" {
		t.Skip("设置 OPSKAT_TYPESAFE_EVAL_KEY 后运行模型评估")
	}
	threshold := DefaultThreshold
	if v := os.Getenv("OPSKAT_TYPESAFE_EVAL_THRESHOLD"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		require.NoError(t, err)
		threshold = f
	}
	cfg := NewConfig(Settings{
		APIKey:    key,
		BaseURL:   os.Getenv("OPSKAT_TYPESAFE_EVAL_BASE_URL"),
		Model:     os.Getenv("OPSKAT_TYPESAFE_EVAL_MODEL"),
		TimeoutMs: int((30 * time.Second).Milliseconds()),
		Threshold: threshold,
	})
	svc := New(func() (Config, error) { return cfg, nil }, noCache{}, func(k, baseURL string) Evaluator { return typesafe.New(k, typesafe.WithBaseURL(baseURL)) })

	samples := loadEvalSamples(t)
	inputs := make([]Input, len(samples))
	for i, s := range samples {
		inputs[i] = Input{AssetType: s.AssetType, Command: s.Command}
	}
	results := svc.ReviewBatch(context.Background(), inputs)

	var askTotal, askPassed, passTotal, passPassed, failed int
	var durations []time.Duration
	var falsePasses, falseAsks []string
	for i, r := range results {
		s := samples[i]
		if r.Outcome == OutcomeFail {
			failed++
			t.Logf("审核失败 [%s] %s: %s", s.AssetType, s.Command, r.Reason)
			continue
		}
		durations = append(durations, r.Duration)
		passed := r.Outcome == OutcomePass
		if s.Expect == "ask" {
			askTotal++
			if passed {
				askPassed++
				falsePasses = append(falsePasses, fmt.Sprintf("[%s/%s] %s %v", s.AssetType, s.Category, s.Command, r.Scores))
			}
		} else {
			passTotal++
			if passed {
				passPassed++
			} else {
				falseAsks = append(falseAsks, fmt.Sprintf("[%s/%s] %s failed=%v", s.AssetType, s.Category, s.Command, r.Failed))
			}
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	pct := func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return 100 * float64(a) / float64(b)
	}
	quantile := func(q float64) time.Duration {
		if len(durations) == 0 {
			return 0
		}
		return durations[int(q*float64(len(durations)-1))]
	}

	t.Logf("模型 %s，阈值 %.2f，样本 %d 条，审核失败 %d 条", cfg.Model, threshold, len(samples), failed)
	t.Logf("本该问人却审核通过：%d / %d（%.1f%%）", askPassed, askTotal, pct(askPassed, askTotal))
	t.Logf("可以自动执行且审核通过：%d / %d（%.1f%%）", passPassed, passTotal, pct(passPassed, passTotal))
	t.Logf("单条耗时 p50 %s，p95 %s", quantile(0.5), quantile(0.95))
	for _, s := range falsePasses {
		t.Logf("误放：%s", s)
	}
	for _, s := range falseAsks {
		t.Logf("多问：%s", s)
	}
}
