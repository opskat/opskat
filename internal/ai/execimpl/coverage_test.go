package execimpl

import (
	"maps"
	"slices"
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// exemptFromExec 是尚未接入统一 exec 的资产类型；新增资产类型应实现执行器，
// 不应扩大这份豁免清单。
//
//   - local：spec §2 明确列为非目标，另开 issue 跟踪
//   - vnc / rdp：PolicyKind 为空，下面的循环在到达豁免检查之前就已 continue，
//     本就不在检查范围内，因此不需要（也不能通过）豁免条目——列在这里仅供交叉核对。
//     这两个类型都有 doc-only 的 help 文档（RegisterHelpDoc，help_coverage_test.go
//     的 TestEveryAssetTypeHasHelpDoc / TestDocOnlyTypesHaveNoExecutor 锁住），但仍然
//     没有执行器——help 覆盖与 exec 覆盖是两件事，补文档不代表补执行器。
//     （oss 曾经也在这一列：它此前 PolicyKind 为空且只有 doc-only 文档，现在两者都有。）
var exemptFromExec = map[string]string{
	"local": "spec §2 非目标：有 PolicyKind 却无 permission 注册",
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

// policyStringDerivingTypes 是"一条命令请求多项权限"的资产类型：它们的权限检查靠
// permission.RegisterPolicyStrings 注册的派生函数把一条命令展开成 N 条策略串
// （OSS 的 `object move` 同时读源、写目的、删源，只按其中一条判定就等于放行
// "把受限对象复制到可读位置再读"的绕过路径，设计 D7）。
//
// 这份清单要跟着新类型一起维护——permission 侧问不出"这个类型需不需要派生函数"，
// 而漏掉 RegisterPolicyStrings **既不会编译报错，也不会 panic**：checkOSSPermission
// 会把"派生不出来"当成解析失败并退回 NeedConfirm（fail-closed，但 §4.1 的 allow/deny
// 两种判定从此永远到不了），ossGrantPatterns 则一条 grant 都落不下来——"始终允许"于是
// 永远不生效。两者都静默，所以这条测试是接线唯一的守卫，与 TestEveryPolicyKindTypeHasExecutor
// 守住执行器是同一个位置。
//
// 断言用的是**行为**而不是"注册表里有没有这个 key"：NormalizeGrantPatterns 是接线
// 真正的下游消费者（SaveGrantPatternsForApproval → 落库的常驻授权），没接线时它交出
// 空列表。期望值里的 key 不含通配元字符（`* ? [ \`），因此不受落 grant 时的转义
// 规则（D21 更正）影响。
var policyStringDerivingTypes = map[string]struct {
	command string
	want    []string
}{
	asset_entity.AssetTypeOSS: {
		command: "object move mybucket/a.txt --to=archive/a.txt",
		want: []string{
			"object.read mybucket/a.txt",
			"object.write archive/a.txt",
			"object.delete mybucket/a.txt",
		},
	},
}

func TestEveryPolicyStringDerivingTypeHasItsDeriver(t *testing.T) {
	for _, assetType := range slices.Sorted(maps.Keys(policyStringDerivingTypes)) {
		c := policyStringDerivingTypes[assetType]
		if _, ok := permission.ExecutorFor(assetType); !ok {
			t.Errorf("asset type %q is listed as deriving policy strings but has no executor; "+
				"either register one or drop it from policyStringDerivingTypes", assetType)
			continue
		}
		got := permission.NormalizeGrantPatterns(permission.ApprovalTypeFor(assetType), c.command, permission.GrantOriginSystem)
		if !slices.Equal(got, c.want) {
			t.Errorf("NormalizeGrantPatterns(%q, %q) = %q, want %q; "+
				"this is what an unregistered permission.RegisterPolicyStrings(%q, …) looks like — "+
				"add the call in register.go next to the executor",
				assetType, c.command, got, c.want, assetType)
		}
	}
}

// OSS 的第二条强制接线：canonicalize 必须注册，而且必须是那个只认 DSL 的规范化函数。
//
// permission.checkOSSPermission 靠"首词含 '.'"区分已派生的策略串（cp 的端点主体、用户
// 在弹窗里手写的 pattern）与 DSL 命令，所以一条策略串形状的输入从 exec 面递进去会绕过
// DSL 解析器直接参与规则匹配——`object.read mybucket/*` 能撞上一条整桶 allow 规则被判成
// Allow 并落成 grant，之后才在执行器里因为解析不了而失败。handleExec 的顺序是
// canonicalize → precheck → 权限检查，所以这一步就是把非 DSL 输入挡在规则匹配之外的
// 那道闸门；不注册它，handleExec 会把原串直接送去做权限判定。
func TestOSSCanonicalizerKeepsNonDSLInputOutOfTheMatcher(t *testing.T) {
	canonicalize, ok := permission.CanonicalizeFor(asset_entity.AssetTypeOSS)
	if !ok {
		t.Fatal("oss has no canonicalizer registered; handleExec would hand the raw command " +
			"straight to the permission check")
	}

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	if _, err := canonicalize(asset, "object.read mybucket/*"); err == nil {
		t.Error("the registered oss canonicalizer accepted a policy-string-shaped command; " +
			"it must reject anything that is not the exec DSL")
	}
	if got, err := canonicalize(asset, "object list mybucket"); err != nil || got != "object list mybucket/" {
		t.Errorf("canonicalize(%q) = (%q, %v), want (%q, nil) — the registered function must be "+
			"the OSS DSL canonicalizer", "object list mybucket", got, err, "object list mybucket/")
	}
}
