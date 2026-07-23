package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
)

// exemptFromExec 是尚未接入统一 exec 的资产类型；新增资产类型应实现执行器，
// 不应扩大这份豁免清单。
//
//   - local：spec §2 明确列为非目标，另开 issue 跟踪
//   - vnc / rdp / oss：PolicyKind 为空，下面的循环在到达豁免检查之前就已 continue，
//     本就不在检查范围内，因此不需要（也不能通过）豁免条目——列在这里仅供交叉核对。
//     这三个类型现在都有 doc-only 的 help 文档（RegisterHelpDoc，help_coverage_test.go
//     的 TestEveryAssetTypeHasHelpDoc / TestDocOnlyTypesHaveNoExecutor 锁住），但仍然
//     没有执行器——help 覆盖与 exec 覆盖是两件事，补文档不代表补执行器。
var exemptFromExec = map[string]string{
	"local": "spec §2 非目标：有 PolicyKind 却无 permission 注册",
}

func TestEveryPolicyKindTypeHasExecutor(t *testing.T) {
	for _, h := range assettype.All() {
		if h.PolicyKind() == "" {
			continue // vnc / rdp / oss：无策略种类，不在统一 exec 范围内
		}
		if reason, exempt := exemptFromExec[h.Type()]; exempt {
			t.Logf("skipping %s (%s)", h.Type(), reason)
			continue
		}
		if _, ok := permission.ExecutorFor(h.Type()); !ok {
			t.Errorf("asset type %q has PolicyKind %q but no exec executor registered; "+
				"implement one or justify an entry in exemptFromExec",
				h.Type(), h.PolicyKind())
		}
	}
}
