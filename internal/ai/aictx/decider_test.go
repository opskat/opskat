package aictx

import (
	"slices"
	"testing"
)

// TestDeciderOf 锁住 spec 决策 4 的归组：审计页与 opsctl 按这几组筛选，某个来源掉出
// 分组就意味着它的审计行再也筛不出来。
func TestDeciderOf(t *testing.T) {
	cases := map[string]string{
		SourcePolicyAllow:           DeciderPolicy,
		SourcePolicyDeny:            DeciderPolicy,
		SourceUserAllow:             DeciderUser,
		SourceUserDeny:              DeciderUser,
		SourceGrantAllow:            DeciderGrant,
		SourceGrantDeny:             DeciderGrant,
		SourceAssistedAllow:         DeciderAssisted,
		SourceAutopilotAllow:        DeciderAutopilot,
		SourceAutopilotDeny:         DeciderAutopilot,
		SourceExecUnsupportedType:   DeciderExecGate,
		SourceExecGateBlocked:       DeciderExecGate,
		SourceExecPrecheckFailed:    DeciderExecGate,
		SourceExecCanonicalizeError: DeciderExecGate,
		SourceExecTypeMismatch:      DeciderExecGate,
		SourceExecResolveFailed:     DeciderExecGate,
		SourceExecInvalidInput:      DeciderExecGate,
		SourceBatchComplete:         DeciderBatch,
		SourceBatchPartialFailure:   DeciderBatch,
		SourceBatchFailed:           DeciderBatch,
		"":                          DeciderNone,
	}
	for source, want := range cases {
		got, ok := DeciderOf(source)
		if !ok || got != want {
			t.Errorf("DeciderOf(%q) = %q, %v; want %q", source, got, ok, want)
		}
	}
	if got, ok := DeciderOf("future_source"); ok {
		t.Errorf("unknown source must not be grouped, got %q", got)
	}
}

// TestDeciderSources 断言按分组展开的来源集合与 DeciderOf 互逆：查询用的 IN 列表
// 与行内归组必须是同一份定义。
func TestDeciderSources(t *testing.T) {
	for _, group := range DeciderGroups {
		sources, ok := DeciderSources(group)
		if !ok || len(sources) == 0 {
			t.Fatalf("group %q has no sources", group)
		}
		for _, s := range sources {
			if got, _ := DeciderOf(s); got != group {
				t.Errorf("source %q listed under %q but DeciderOf says %q", s, group, got)
			}
		}
	}
	none, _ := DeciderSources(DeciderNone)
	if !slices.Equal(none, []string{""}) {
		t.Errorf("none must expand to the empty decision source, got %v", none)
	}
	if _, ok := DeciderSources("bogus"); ok {
		t.Error("unknown group must be rejected")
	}
}
