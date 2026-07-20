package runner

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
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
