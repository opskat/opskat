package jevreview

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/extensions/jev-command-review/typesafe"
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

// TestEvalAgainstJev 用真实的 Jev 审核全部样本并输出指标。需要 API key，平时跳过：
//
//	OPSKAT_TYPESAFE_EVAL_KEY=<key> go test ./internal/pkg/jevreview/ -run TestEvalAgainstJev -v -count=1
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
	baseURL := cmp.Or(os.Getenv("OPSKAT_TYPESAFE_EVAL_BASE_URL"), DefaultBaseURL)
	model := cmp.Or(os.Getenv("OPSKAT_TYPESAFE_EVAL_MODEL"), DefaultModel)
	client := typesafe.New(key, typesafe.WithBaseURL(baseURL))

	samples := loadEvalSamples(t)
	type result struct {
		failed   []string
		scores   map[string]float64
		err      error
		duration time.Duration
	}
	results := make([]result, len(samples))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, s := range samples {
		wg.Add(1)
		go func(i int, s evalSample) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := time.Now()
			resp, err := client.Evaluate(ctx, Request(model, s.AssetType, s.Command))
			r := result{err: err, duration: time.Since(start)}
			if err == nil {
				r.scores = map[string]float64{}
				for id, a := range resp.Answers {
					r.scores[id] = a.Noul
				}
				r.failed = Failed(r.scores, threshold)
			}
			results[i] = r
		}(i, s)
	}
	wg.Wait()

	var askTotal, askPassed, passTotal, passPassed, failed int
	var durations []time.Duration
	var falsePasses, falseAsks []string
	for i, r := range results {
		s := samples[i]
		if r.err != nil {
			failed++
			t.Logf("审核失败 [%s] %s: %s", s.AssetType, s.Command, FailReason(r.err))
			continue
		}
		durations = append(durations, r.duration)
		passed := len(r.failed) == 0
		if s.Expect == "ask" {
			askTotal++
			if passed {
				askPassed++
				falsePasses = append(falsePasses, fmt.Sprintf("[%s/%s] %s %v", s.AssetType, s.Category, s.Command, r.scores))
			}
		} else {
			passTotal++
			if passed {
				passPassed++
			} else {
				falseAsks = append(falseAsks, fmt.Sprintf("[%s/%s] %s failed=%v", s.AssetType, s.Category, s.Command, r.failed))
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

	t.Logf("模型 %s，阈值 %.2f，样本 %d 条，审核失败 %d 条", model, threshold, len(samples), failed)
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
