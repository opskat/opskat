package runner

import (
	"context"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestPromptBuilderBuild(t *testing.T) {
	Convey("PromptBuilder.Build", t, func() {
		Convey("无 OpenTabs 时输出语言提示（角色描述已搬到 system_template.go）", func() {
			got := NewPromptBuilder("zh-cn", AIContext{}).Build()
			So(got, ShouldContainSubstring, "Chinese")
			So(got, ShouldNotContainSubstring, "OpsKat AI assistant")
		})

		Convey("OpenTabs 渲染包含每个 tab 名和 ID", func() {
			got := NewPromptBuilder("en", AIContext{
				OpenTabs: []TabInfo{
					{Type: "ssh", AssetID: 42, AssetName: "prod-db"},
					{Type: "database", AssetID: 43, AssetName: "metrics"},
				},
			}).Build()
			So(got, ShouldContainSubstring, "SSH Terminal")
			So(got, ShouldContainSubstring, "prod-db")
			So(got, ShouldContainSubstring, "Database Query")
			So(got, ShouldContainSubstring, "metrics")
		})

		Convey("输出内联 mention 语义提示", func() {
			got := NewPromptBuilder("en", AIContext{}).Build()
			So(got, ShouldContainSubstring, "<mention")
			So(got, ShouldContainSubstring, "asset-id")
			So(got, ShouldContainSubstring, "database")
			So(got, ShouldContainSubstring, "table")
		})

		Convey("Extension SKILL.md 被注入", func() {
			b := NewPromptBuilder("en", AIContext{})
			b.SetExtensionSkillMDs(map[string]string{"k8s": "k8s skill body"})
			got := b.Build()
			So(got, ShouldContainSubstring, "From extension: k8s")
			So(got, ShouldContainSubstring, "k8s skill body")
		})
	})
}

func TestBuild_ListsBuiltinAssetTypeSkills(t *testing.T) {
	b := NewPromptBuilder("en", AIContext{})
	b.SetAssetTypeSkills(map[string]string{
		"redis": "Run Redis commands against a Redis asset via exec.",
	})
	got := b.Build()
	if !strings.Contains(got, "redis") {
		t.Fatalf("prompt should list the redis skill, got:\n%s", got)
	}
	// 只列描述，不内联正文——正文由 help 按需加载（spec §3.3）。
	if strings.Contains(got, "## Command syntax") {
		t.Fatal("prompt must not inline the full SKILL.md body")
	}
}

// TestBuild_ExplainsExecAndHelpUnconditionally locks the requirement that exec/help is
// documented as the primary path for asset operations even when no per-type listing was
// supplied. Gating this section on "a matching tab is open" (which is what feeding the
// listing used to depend on) left the model with only the legacy per-type tools in every
// session that happened to have no relevant tab.
func TestBuild_ExplainsExecAndHelpUnconditionally(t *testing.T) {
	b := NewPromptBuilder("en", AIContext{})
	got := b.Build()
	for _, want := range []string{"exec(asset, command)", "help(asset)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt should explain %q with no skills set, got:\n%s", want, got)
		}
	}
}

// TestBuild_SeparatesExecCoveredFromConfigOnlyTypes locks the fix for the Important
// review finding on task 3: doc-only types (rdp/vnc/oss/local in production) used to be
// listed under the single heading "Asset types exec currently covers:" alongside real
// exec-capable types, even though each doc-only type's own one-line description says
// "exec is not supported for this type" — self-contradictory from the model's point of
// view (a heading claiming coverage, immediately followed by a line disclaiming it).
// buildAssetTypeSkills must split the listing using permission.ExecutorFor so a type with
// no registered executor never appears under the "currently covers" heading, and instead
// appears under a heading that itself says exec is NOT supported.
//
// Uses synthetic types registered directly (not real ones like ssh/rdp) so the test does
// not depend on execimpl's init() having run in this package's test binary.
func TestBuild_SeparatesExecCoveredFromConfigOnlyTypes(t *testing.T) {
	const execType = "test-exec-covered-type"
	const docOnlyType = "test-config-only-type"

	permission.RegisterExecutor(execType,
		func(context.Context, *asset_entity.Asset, string, string) (string, error) { return "", nil },
		"usage doc")
	t.Cleanup(func() { permission.UnregisterExecutorForTest(execType) })

	permission.RegisterHelpDoc(docOnlyType, "config-only doc")
	t.Cleanup(func() { permission.UnregisterExecutorForTest(docOnlyType) })

	b := NewPromptBuilder("en", AIContext{})
	b.SetAssetTypeSkills(map[string]string{
		execType:    "Run shell commands over a terminal asset.",
		docOnlyType: "Remote desktop assets; no command surface — exec is not supported for this type.",
	})
	got := b.Build()

	execHeadingIdx := strings.Index(got, "exec currently covers")
	if execHeadingIdx == -1 {
		t.Fatalf("prompt should contain the exec-covers heading, got:\n%s", got)
	}
	configHeadingIdx := strings.Index(got, "exec is NOT supported")
	if configHeadingIdx == -1 {
		t.Fatalf("prompt should contain a config-only heading that itself says exec is NOT supported, got:\n%s", got)
	}
	if configHeadingIdx < execHeadingIdx {
		t.Fatalf("the config-only heading should come after the exec-covers heading, got:\n%s", got)
	}

	execListItemIdx := strings.Index(got, "- "+execType+":")
	docOnlyListItemIdx := strings.Index(got, "- "+docOnlyType+":")
	if execListItemIdx == -1 || docOnlyListItemIdx == -1 {
		t.Fatalf("prompt should list both types, got:\n%s", got)
	}
	if execHeadingIdx >= execListItemIdx || execListItemIdx >= configHeadingIdx {
		t.Fatalf("%q (has an executor) should be listed under the exec-covers heading, before the config-only heading, got:\n%s",
			execType, got)
	}
	if docOnlyListItemIdx < configHeadingIdx {
		t.Fatalf("%q (no executor) must be listed under the config-only heading, not the exec-covers one, got:\n%s",
			docOnlyType, got)
	}
}

// TestBuild_PrefersExecOverPerTypeTools locks the FIX-4 decision: the old guidance told
// the model to "pick the dedicated tool for each asset type" and escalated that to a
// "you MUST use that asset's dedicated remote tool", which contradicts a branch whose
// whole point is that exec dispatches on the asset's real type.
func TestBuild_PrefersExecOverPerTypeTools(t *testing.T) {
	got := NewPromptBuilder("en", AIContext{}).Build()
	if strings.Contains(got, "Pick the dedicated tool for each asset type") {
		t.Fatalf("prompt still routes per asset type to a dedicated tool, got:\n%s", got)
	}
	// The local-vs-remote distinction must survive the rewrite — it is orthogonal to
	// which remote tool is preferred, and still correct.
	if !strings.Contains(got, "operates ONLY on the USER'S OWN MACHINE") {
		t.Fatalf("prompt must keep the local_* vs remote warning, got:\n%s", got)
	}
}
