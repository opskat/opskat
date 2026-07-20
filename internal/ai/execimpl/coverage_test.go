package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
)

// exemptFromExec 是尚未接入统一 exec 的资产类型。
//
// 这个清单**只可缩短，不可增长**——与 internal/archtest 的 legacy 豁免清单同一惯例。
// 新增资产类型时不要往这里加，而应实现执行器。
//
//   - local / oss：spec §2 明确列为非目标，另开 issue 跟踪
//   - mongodb / etcd / kafka：Plan B 补齐
//   - vnc / rdp：交互式协议，无可脚本化命令面，PolicyKind 为空因而本就不在检查范围
var exemptFromExec = map[string]string{
	"local":   "spec §2 非目标：有 PolicyKind 却无 permission 注册",
	"oss":     "spec §2 非目标：仅扩展可达",
	"mongodb": "Plan B",
	"etcd":    "Plan B",
	"kafka":   "Plan B",
}

func TestEveryPolicyKindTypeHasExecutor(t *testing.T) {
	for _, h := range assettype.All() {
		if h.PolicyKind() == "" {
			continue // vnc / rdp：无策略种类，不在统一 exec 范围内
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

// 豁免清单只可缩短：数量增长即失败，逼迫增改者正视。
func TestExemptionListDoesNotGrow(t *testing.T) {
	const maxExemptions = 5
	if len(exemptFromExec) > maxExemptions {
		t.Fatalf("exemptFromExec grew to %d entries (max %d); "+
			"the list may only shrink — implement the executor instead",
			len(exemptFromExec), maxExemptions)
	}
}
