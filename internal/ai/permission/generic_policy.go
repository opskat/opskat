package permission

import (
	"context"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// 通用资产（asset_entity.AssetTypeGeneric）的权限判定（docs/specs/2026-09-28-generic-asset.md
// 「策略、审批与审计」）：规则存在资产 / 组链的 CommandPolicy 上（与权限组共用命令型策略），
// 判定顺序 deny → allow → grant → confirm。不同执行方式匹配的对象不同，所以按资产所属
// 自定义类型的执行方式查 genericModeChecks：
//
//   - HTTP：`<大写方法> <路径>`（路径不含 query），按 MatchPlainGlob 普通 glob 匹配，不做
//     shell 解析；取值的 `secret:<字段>` 同样是普通 glob。
//   - 本地命令：由命令执行方式在这张表里登记自己的判定（本文件之外）。
//
// 表里查不到的执行方式一律 NeedConfirm——不猜匹配方式，也不放行。ct 是 checkGeneric
// Permission 已经查到的资产所属类型，登记的判定不必再查一次。
var genericModeChecks = map[string]func(ctx context.Context, asset *asset_entity.Asset, ct *custom_type_entity.CustomType, subject string) aictx.CheckResult{
	custom_type_entity.ExecModeHTTP: func(ctx context.Context, asset *asset_entity.Asset, _ *custom_type_entity.CustomType, subject string) aictx.CheckResult {
		return checkPlainGlobPolicy(ctx, asset, subject)
	},
}

// SecretSubjectPrefix is the match-object prefix for get_asset_secret (spec 「策略、审批与
// 审计」：取值 `secret:<字段>`). Exported so package tool (which already imports permission,
// not the other way around — no cycle) builds the same match object with this constant
// instead of a second, independently-typed "secret:" literal.
const SecretSubjectPrefix = "secret:"

func checkGenericPermission(ctx context.Context, assetID int64, subject string) aictx.CheckResult {
	asset := resolveAssetForPolicy(ctx, assetID)
	if asset == nil {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	// 取值适用于所有执行方式的通用资产——判定与类型的 exec mode 无关，在查 genericModeChecks
	// 之前短路，直接走跟 HTTP 共用的普通 glob 匹配器。新建类型从不预填 secret:* 的默认允许
	// 规则（Design decision 9），所以这条路径没有额外的"默认放行"要处理。
	if strings.HasPrefix(subject, SecretSubjectPrefix) {
		return checkPlainGlobPolicy(ctx, asset, subject)
	}
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		logger.Ctx(ctx).Warn("read generic config for permission check", zap.Int64("assetID", assetID), zap.Error(err))
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	ct, err := custom_type_svc.CustomType().GetBySlug(ctx, cfg.CustomType)
	if err != nil {
		logger.Ctx(ctx).Warn("resolve custom type for permission check", zap.Int64("assetID", assetID), zap.Error(err))
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	check, ok := genericModeChecks[ct.ExecMode]
	if !ok {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	return check(ctx, asset, ct, subject)
}

// checkPlainGlobPolicy 用 MatchPlainGlob 按 deny → allow → grant → confirm 判定一个
// 匹配对象。规则来自资产与组链的 CommandPolicy 以及它们引用的权限组（collectPolicies，
// 与 SSH 同源），grant 与策略用同一个匹配器，保存格式就是匹配对象本身。
func checkPlainGlobPolicy(ctx context.Context, asset *asset_entity.Asset, subject string) aictx.CheckResult {
	var groups []*group_entity.Group
	if asset.GroupID > 0 {
		groups = policy.ResolveGroupChain(ctx, asset.GroupID)
	}
	policies := collectPolicies(ctx, asset, groups)
	denyRules := collectDenyRules(policies)
	allowRules := collectAllowRules(policies)

	for _, rule := range denyRules {
		if MatchPlainGlob(rule, subject) {
			reason := policy.PolicyMsg(ctx, "command blocked by policy", "命令被策略禁止执行")
			return aictx.CheckResult{
				Decision:       aictx.Deny,
				Message:        policy.FormatDenyMessage(ctx, asset.Name, subject, reason, allowRules),
				HintRules:      allowRules,
				DecisionSource: aictx.SourcePolicyDeny,
				MatchedPattern: rule,
			}
		}
	}
	for _, rule := range allowRules {
		if MatchPlainGlob(rule, subject) {
			return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow, MatchedPattern: rule}
		}
	}
	approvalType := ApprovalTypeFor(asset_entity.AssetTypeGeneric)
	if pattern := matchGrantPatternsWith(ctx, asset.ID, groups, []string{subject}, approvalType, MatchPlainGlob); pattern != "" {
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceGrantAllow, MatchedPattern: pattern}
	}
	return aictx.CheckResult{Decision: aictx.NeedConfirm, HintRules: allowRules}
}

// genericGrantPatterns 是通用资产的 grant 归一化：匹配对象整串存一条。系统给出的主体
// 是具体的一次请求（`GET /api/a*b` 里的 `*` 是路径里的字面字符），落成规则前把普通 glob
// 的元字符转义成只匹配它自己；用户在审批弹窗里手写的通配就是他要的授权范围，原样保存。
func genericGrantPatterns(command string, origin GrantOrigin) []string {
	if origin == GrantOriginSystem {
		return []string{escapeGlobMeta(command)}
	}
	return []string{command}
}

// MatchPlainGlob 是通用资产的普通 glob 匹配器：`*` 匹配任意长度（含空串、跨 `/` 与空格）
// 的任意字符，`?` 匹配恰好一个字符，`\` 转义下一个字符使之按字面匹配；其余字符（含 `[`）
// 一律按字面比较，没有字符类。整串匹配，大小写敏感。
//
// 与 path.Match 的区别是有意的：HTTP 规则 `GET /api/*` 必须覆盖 `/api/a/b`，而
// path.Match 的 `*` 不跨 `/`；它也不经 shell 解析（MatchCommandRule 会把 `GET` 当程序名、
// 把路径当参数）。结尾孤立的 `\` 按字面 `\` 处理，不会让整条规则失效。
func MatchPlainGlob(pattern, subject string) bool {
	p := []rune(pattern)
	s := []rune(subject)
	pi, si := 0, 0
	// star 记录最近一个 `*` 在模式里的位置与它当时吞到的主体位置，失配时回溯到那里多吞一个字符。
	starP, starS := -1, 0
	for si < len(s) {
		if pi < len(p) {
			switch c := p[pi]; {
			case c == '*':
				starP, starS = pi, si
				pi++
				continue
			case c == '?':
				pi++
				si++
				continue
			case c == '\\' && pi+1 < len(p):
				if p[pi+1] == s[si] {
					pi += 2
					si++
					continue
				}
			case c == s[si]:
				pi++
				si++
				continue
			}
		}
		if starP < 0 {
			return false
		}
		starS++
		pi, si = starP+1, starS
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// customTypeSlugExists 报告 name 是否是一个已定义的自定义类型标识。
//
// 类型断言（--type / batch 前缀 / exec 的 type 参数）没有 ctx 可传，而标识只在数据库里：
// 这里用 context.Background() 查一次。仓储未注册（只在不涉及自定义类型的单元测试里出现，
// 与本包 grant_repo 的处理一致）时没有任何自定义类型可查，按"不是标识"回答；除"未找到"
// 之外的查询错误记一条 Warn 再按"不是标识"回答——调用方随后会报 unknown type 并列出可用
// 类型，不会因此放行任何东西。
func customTypeSlugExists(name string) bool {
	if custom_type_repo.CustomType() == nil {
		return false
	}
	_, err := custom_type_svc.CustomType().GetBySlug(context.Background(), name)
	switch {
	case err == nil:
		return true
	case custom_type_svc.IsNotFound(err):
		return false
	default:
		logger.Default().Warn("lookup custom type slug for type assertion", zap.String("slug", name), zap.Error(err))
		return false
	}
}

// genericSlugOf 返回通用资产引用的自定义类型标识；非通用资产返回 ok=false。
func genericSlugOf(asset *asset_entity.Asset) (string, bool, error) {
	if !asset.IsGeneric() {
		return "", false, nil
	}
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		return "", true, fmt.Errorf("asset %q: %w", asset.Name, err)
	}
	return cfg.CustomType, true, nil
}
