package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
)

// exemptFromExec 是尚未接入统一 exec 的资产类型。
//
// 这个清单**只可缩短，不可增长**。新增资产类型时不要往这里加，而应实现执行器。
//
// 与 internal/archtest 的 legacy 豁免清单出发点相同，但注意二者的强制方式不同：
// archtest 完全靠注释与评审纪律约束，没有任何机械检查；本文件下方多了一个
// TestExemptionListDoesNotGrow。别高估它——同一次改动里把 map 加一条、再把
// maxExemptions 加一，两个测试仍会绿。它拦不住**有意**扩张，只拦得住**顺手忘了**
// 扩张，作用是逼出一处显眼的常量 diff 供评审注意。真正的约束仍是评审。
//
//   - local：spec §2 明确列为非目标，另开 issue 跟踪
//   - mongodb / kafka：Plan B 补齐
//   - vnc / rdp / oss：PolicyKind 为空，下面的循环在到达豁免检查之前就已 continue，
//     本就不在检查范围内，因此不需要（也不能通过）豁免条目——列在这里仅供交叉核对。
var exemptFromExec = map[string]string{
	"local":   "spec §2 非目标：有 PolicyKind 却无 permission 注册",
	"mongodb": "Plan B",
	"kafka":   "Plan B",
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

// 豁免清单只可缩短：条目数超出常量即失败。见上方说明——这只拦得住忘记同步常量的
// 情形，拦不住有意的同步扩张。
func TestExemptionListDoesNotGrow(t *testing.T) {
	const maxExemptions = 3
	if len(exemptFromExec) > maxExemptions {
		t.Fatalf("exemptFromExec grew to %d entries (max %d); "+
			"the list may only shrink — implement the executor instead",
			len(exemptFromExec), maxExemptions)
	}
}
