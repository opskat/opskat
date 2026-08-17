package execimpl

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
)

var configFieldRow = regexp.MustCompile(`(?m)^\| \x60([^\x60]+)\x60 \|`)

// 每个注册了的资产类型都必须有 help 文档——**没有豁免清单**。
//
// 理由与 exec 的豁免清单（coverage_test.go）不同：exec 覆盖不全只是"这个类型还不能跑命令"，
// 而 help 覆盖不全会让 put_asset 变成不可用——Plan C 删掉了 add_asset 那张按类型枚举的
// 巨型 schema，config 的形状此后**只**由这份文档承载。少一份文档 = 模型无从知道该类型
// 要填什么字段，且不会报错，只会瞎猜。
func TestEveryAssetTypeHasHelpDoc(t *testing.T) {
	for _, h := range assettype.All() {
		if _, ok := permission.HelpFor(h.Type()); !ok {
			t.Errorf("asset type %q has no help doc; add internal/ai/skills/%s/SKILL.md and register it "+
				"(RegisterExecutor if it has a command surface, RegisterHelpDoc if it only has config)",
				h.Type(), h.Type())
		}
	}
}

// The registered handler is the executable source of truth for generic automation fields;
// the embedded help is the independently consumed public contract used by AI and opsctl.
// Compare their parsed field sets instead of locking prose or implementation entries.
func TestHelpConfigFieldsMatchAutomationContracts(t *testing.T) {
	for _, h := range assettype.All() {
		doc, ok := permission.HelpFor(h.Type())
		if !ok {
			continue // TestEveryAssetTypeHasHelpDoc reports the missing document.
		}
		_, configSection, found := strings.Cut(doc, "## Asset config (for put_asset)")
		if !found {
			t.Errorf("asset type %q help has no put_asset config section", h.Type())
			continue
		}
		if next := strings.Index(configSection, "\n## "); next >= 0 {
			configSection = configSection[:next]
		}
		matches := configFieldRow.FindAllStringSubmatch(configSection, -1)
		documented := make([]string, 0, len(matches))
		for _, match := range matches {
			documented = append(documented, match[1])
		}
		sort.Strings(documented)
		expected := append([]string(nil), h.AutomationContract().ConfigFields...)
		sort.Strings(expected)
		if strings.Join(documented, "\x00") != strings.Join(expected, "\x00") {
			t.Errorf("asset type %q config fields differ: handler=%v help=%v", h.Type(), expected, documented)
		}
	}
}

// doc-only 类型有文档但**没有**执行器：help 能查，exec 必须明确报"尚不支持"，
// 而不是查到一个 nil 执行器后 panic。
//
// oss 曾经在这份清单里（"There is no command surface"）；它现在有 exec DSL、
// 执行器与策略种类，因此归 coverage_test.go 的 TestEveryPolicyKindTypeHasExecutor 管。
func TestDocOnlyTypesHaveNoExecutor(t *testing.T) {
	for _, docOnly := range []string{"rdp", "vnc", "local"} {
		if _, ok := permission.HelpFor(docOnly); !ok {
			t.Errorf("doc-only type %q must have a help doc", docOnly)
		}
		if _, ok := permission.ExecutorFor(docOnly); ok {
			t.Errorf("doc-only type %q must not report an executor", docOnly)
		}
	}
	// 且不得混进 exec 的类型清单（它会进模型看到的 exec 工具描述）。
	for _, listed := range permission.RegisteredExecTypes() {
		switch listed {
		case "rdp", "vnc", "local":
			t.Errorf("doc-only type %q must not appear in RegisteredExecTypes()", listed)
		}
	}
}
