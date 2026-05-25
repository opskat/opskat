package policy

import (
	"context"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/group_svc"
)

// MatchFunc 通用命令匹配函数签名
type MatchFunc func(rule, command string) bool

// PolicyTestInput 策略测试入参
type PolicyTestInput struct {
	PolicyType string // "ssh" | "database" | "redis" | "k8s" | "etcd"
	AssetID    int64  // 资产ID（从资产的 groupID 开始解析组链）
	GroupID    int64  // 资产组ID（从父组开始解析，当前组策略由 Current* 字段提供）

	// 当前编辑中的策略（来自前端 UI state，可能未保存）
	CurrentSSH   *asset_entity.CommandPolicy
	CurrentQuery *asset_entity.QueryPolicy
	CurrentRedis *asset_entity.RedisPolicy
	CurrentK8s   *asset_entity.K8sPolicy
	CurrentEtcd  *asset_entity.EtcdPolicy
}

// PolicyTestOutput 策略测试结果
type PolicyTestOutput struct {
	Decision       aictx.Decision
	MatchedPattern string
	MatchedSource  string // "" 当前策略, "default" 默认规则, 组名
	Message        string
}

// taggedRule 带来源标签的规则
type taggedRule struct {
	Rule, Source string
}

// TestPolicy 统一的策略测试入口，解析资产组链并合并策略后检查命令。
func TestPolicy(ctx context.Context, input PolicyTestInput, command string) PolicyTestOutput {
	groups := resolveGroupChainForTest(ctx, input.AssetID, input.GroupID)

	switch input.PolicyType {
	case "ssh":
		return testSSHPolicy(ctx, input.CurrentSSH, groups, command)
	case "database":
		return testQueryPolicy(ctx, input.CurrentQuery, groups, command)
	case "redis":
		return testRedisPolicy(ctx, input.CurrentRedis, groups, command)
	case "k8s":
		return testK8sPolicy(ctx, input.CurrentK8s, groups, command)
	case "etcd":
		return testEtcdPolicy(ctx, input.CurrentEtcd, groups, command)
	}
	return PolicyTestOutput{Decision: aictx.NeedConfirm}
}

// --- 通用组规则收集 ---

// collectGroupGenericRules 从组链的 CmdPolicy（通用策略）中收集 deny/allow 规则。
// 组的 CmdPolicy 是通用类型，适用于所有资产类型。
func collectGroupGenericRules(ctx context.Context, groups []*group_entity.Group) (deny, allow []taggedRule) {
	for _, g := range groups {
		p, err := g.GetCommandPolicy()
		if err != nil || p == nil {
			continue
		}
		// 解析引用的权限组
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := ResolveCommandGroups(ctx, p.Groups)
			for _, r := range grpAllow {
				allow = append(allow, taggedRule{r, g.Name})
			}
			for _, r := range grpDeny {
				deny = append(deny, taggedRule{r, g.Name})
			}
		}
		for _, r := range p.DenyList {
			deny = append(deny, taggedRule{r, g.Name})
		}
		for _, r := range p.AllowList {
			allow = append(allow, taggedRule{r, g.Name})
		}
	}
	return
}

// checkGenericDeny 用指定的 matcher 检查 deny 规则
func checkGenericDeny(rules []taggedRule, command string, matchFn MatchFunc) *PolicyTestOutput {
	for _, tr := range rules {
		if matchFn(tr.Rule, command) {
			return &PolicyTestOutput{
				Decision:       aictx.Deny,
				MatchedPattern: tr.Rule,
				MatchedSource:  tr.Source,
			}
		}
	}
	return nil
}

// checkGenericAllow 用指定的 matcher 检查 allow 规则
func checkGenericAllow(rules []taggedRule, command string, matchFn MatchFunc) *PolicyTestOutput {
	for _, tr := range rules {
		if matchFn(tr.Rule, command) {
			return &PolicyTestOutput{
				Decision:       aictx.Allow,
				MatchedPattern: tr.Rule,
				MatchedSource:  tr.Source,
			}
		}
	}
	return nil
}

// --- SSH ---

func testSSHPolicy(ctx context.Context, current *asset_entity.CommandPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	subCmds, err := ExtractSubCommands(command)
	if err != nil || len(subCmds) == 0 {
		// 不能整串 fallback，否则与真实路径不一致
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}

	var denyRules, allowRules []taggedRule

	// 当前编辑的策略（资产自身）
	if current != nil {
		// 解析引用的权限组
		if len(current.Groups) > 0 {
			grpAllow, grpDeny := ResolveCommandGroups(ctx, current.Groups)
			for _, r := range grpAllow {
				allowRules = append(allowRules, taggedRule{r, ""})
			}
			for _, r := range grpDeny {
				denyRules = append(denyRules, taggedRule{r, ""})
			}
		}
		for _, r := range current.DenyList {
			denyRules = append(denyRules, taggedRule{r, ""})
		}
		for _, r := range current.AllowList {
			allowRules = append(allowRules, taggedRule{r, ""})
		}
	}
	// 组链通用策略（SSH 直接用 MatchCommandRule）
	groupDeny, groupAllow := collectGroupGenericRules(ctx, groups)
	denyRules = append(denyRules, groupDeny...)
	allowRules = append(allowRules, groupAllow...)

	// flat allow rules
	allowFlat := make([]string, 0, len(allowRules))
	for _, r := range allowRules {
		allowFlat = append(allowFlat, r.Rule)
	}

	// deny 检查
	for _, cmd := range subCmds {
		for _, tr := range denyRules {
			if MatchCommandRule(tr.Rule, cmd) {
				hints := FindHintRules(cmd, allowFlat)
				return PolicyTestOutput{
					Decision:       aictx.Deny,
					MatchedPattern: tr.Rule,
					MatchedSource:  tr.Source,
					Message:        FormatDenyMessage(ctx, "", command, PolicyMsg(ctx, "command blocked by policy", "命令被策略禁止执行"), hints),
				}
			}
		}
	}

	// allow 检查
	if ok, _ := AllSubCommandsAllowed(subCmds, allowFlat); len(allowFlat) > 0 && ok {
		source := ""
		for _, cmd := range subCmds {
			for _, tr := range allowRules {
				if MatchCommandRule(tr.Rule, cmd) {
					source = tr.Source
					break
				}
			}
			if source != "" {
				break
			}
		}
		return PolicyTestOutput{Decision: aictx.Allow, MatchedSource: source}
	}

	return PolicyTestOutput{Decision: aictx.NeedConfirm}
}

// --- Database ---

func testQueryPolicy(ctx context.Context, current *asset_entity.QueryPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	// 先解析 SQL，按每条语句各自送进组通用策略；整串送会让 `SELECT *` 这种宽规则一次性
	// 放行 `SELECT 1; UPDATE users ...`，同时 `UPDATE *` 类 deny 也命中不到尾部。
	stmts, err := ClassifyStatements(command)
	if err != nil {
		return PolicyTestOutput{
			Decision: aictx.Deny,
			Message:  PolicyFmt(ctx, "SQL parse failed, execution denied: %v", "SQL 解析失败，拒绝执行: %v", err),
		}
	}

	stmtTexts := StmtRawTexts(stmts)
	if len(stmtTexts) == 0 {
		stmtTexts = []string{command}
	}

	groupDeny, groupAllow := collectGroupGenericRules(ctx, groups)

	// 组通用 deny：任一语句命中即拒
	for _, stmtText := range stmtTexts {
		if out := checkGenericDeny(groupDeny, stmtText, MatchCommandRule); out != nil {
			out.Message = PolicyFmt(ctx, "SQL statement denied by group policy: %s", "SQL 语句被组策略禁止: %s", stmtText)
			return *out
		}
	}

	merged := mergeQueryPoliciesForTest(ctx, current, groups)
	result := checkQueryPolicyRules(ctx, EffectiveQueryPolicy(ctx, merged), stmts)
	if result.Decision == aictx.Deny {
		return PolicyTestOutput{
			Decision:       aictx.Deny,
			MatchedPattern: result.MatchedPattern,
			MatchedSource:  "", // 当前资产策略
			Message:        result.Message,
		}
	}

	// 与 runtime checkDatabasePermission 对齐：组通用 allow 只用来把 aictx.NeedConfirm 升为 aictx.Allow，
	// 资产策略已是 aictx.Allow 时不能再被组规则"抢走"决策来源；多语句必须每条都命中。
	if result.Decision == aictx.NeedConfirm {
		if out := groupAllowAllStmts(groupAllow, stmtTexts, MatchCommandRule); out != nil {
			return *out
		}
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}
	return PolicyTestOutput{Decision: aictx.Allow}
}

// groupAllowAllStmts 返回组通用 allow 对所有语句的命中结果；任一未命中则 nil。
func groupAllowAllStmts(rules []taggedRule, stmts []string, matchFn MatchFunc) *PolicyTestOutput {
	if len(rules) == 0 || len(stmts) == 0 {
		return nil
	}
	var firstSource, firstPattern string
	for _, s := range stmts {
		hit := false
		for _, tr := range rules {
			if matchFn(tr.Rule, s) {
				hit = true
				if firstSource == "" {
					firstSource = tr.Source
					firstPattern = tr.Rule
				}
				break
			}
		}
		if !hit {
			return nil
		}
	}
	return &PolicyTestOutput{Decision: aictx.Allow, MatchedSource: firstSource, MatchedPattern: firstPattern}
}

// --- Redis ---

func testRedisPolicy(ctx context.Context, current *asset_entity.RedisPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	// 先检查组通用规则（用 MatchRedisRule）
	groupDeny, groupAllow := collectGroupGenericRules(ctx, groups)
	if out := checkGenericDeny(groupDeny, command, MatchRedisRule); out != nil {
		out.Message = PolicyFmt(ctx, "Redis command denied by group policy: %s", "Redis 命令被组策略禁止: %s", command)
		return *out
	}

	merged := mergeRedisPoliciesForTest(ctx, current, groups)
	result := checkRedisPolicyRules(ctx, EffectiveRedisPolicy(ctx, merged), command)

	// deny 结果映射
	if result.Decision == aictx.Deny {
		return PolicyTestOutput{
			Decision:       aictx.Deny,
			MatchedPattern: result.MatchedPattern,
			MatchedSource:  "", // 当前资产策略
			Message:        result.Message,
		}
	}

	// 与 runtime checkRedisPermission 对齐：组通用 allow 只用来把 aictx.NeedConfirm 升为 aictx.Allow，
	// 资产策略已是 aictx.Allow 时不能再被组规则改写 MatchedSource。
	if result.Decision == aictx.NeedConfirm {
		if out := checkGenericAllow(groupAllow, command, MatchRedisRule); out != nil {
			return *out
		}
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}
	return PolicyTestOutput{Decision: aictx.Allow}
}

// --- Etcd ---

func testEtcdPolicy(ctx context.Context, current *asset_entity.EtcdPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	// EtcdPolicy 是 RedisPolicy 的类型别名，规则匹配复用 MatchRedisRule / checkRedisPolicyRules。
	// 先检查组通用规则（用 MatchRedisRule，空格分隔的子串匹配同样适用于 etcd 命令）
	groupDeny, groupAllow := collectGroupGenericRules(ctx, groups)
	if out := checkGenericDeny(groupDeny, command, MatchRedisRule); out != nil {
		out.Message = PolicyFmt(ctx, "etcd command denied by group policy: %s", "etcd 命令被组策略禁止: %s", command)
		return *out
	}

	merged := mergeEtcdPoliciesForTest(ctx, current, groups)
	result := checkRedisPolicyRules(ctx, EffectiveEtcdPolicy(ctx, merged), command)

	if result.Decision == aictx.Deny {
		return PolicyTestOutput{
			Decision:       aictx.Deny,
			MatchedPattern: result.MatchedPattern,
			MatchedSource:  "", // 当前资产策略
			Message:        result.Message,
		}
	}

	// 与 Redis 路径一致：组通用 allow 只用来把 aictx.NeedConfirm 升为 aictx.Allow，
	// 资产策略已是 aictx.Allow 时不能再被组规则改写 MatchedSource。
	if result.Decision == aictx.NeedConfirm {
		if out := checkGenericAllow(groupAllow, command, MatchRedisRule); out != nil {
			return *out
		}
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}
	return PolicyTestOutput{Decision: aictx.Allow}
}

// --- K8S ---

func testK8sPolicy(ctx context.Context, current *asset_entity.K8sPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	// 与真实 checkK8sPermission 对齐：先按 AST 拆 → 走组通用 CmdPolicy → 再走 K8s 策略。
	subCmds, err := ExtractSubCommands(command)
	if err != nil || len(subCmds) == 0 {
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}

	groupDeny, groupAllow := collectGroupGenericRules(ctx, groups)

	// 组通用 deny：任一子命令命中即拒
	for _, sub := range subCmds {
		if out := checkGenericDeny(groupDeny, sub, MatchCommandRule); out != nil {
			out.Message = PolicyFmt(ctx, "command denied by group [%s] policy: %s", "命令被组 [%s] 策略禁止: %s", out.MatchedSource, sub)
			return *out
		}
	}

	// 组通用 allow：每条子命令都命中才算 allow
	groupAllowDecision := groupGenericAllowAllSubCmds(groupAllow, subCmds, MatchCommandRule)

	merged := mergeK8sPoliciesForTest(ctx, current, groups)
	result := checkK8sPolicyRules(ctx, EffectiveK8sPolicy(ctx, merged), command)
	if result.Decision == aictx.Deny {
		return PolicyTestOutput{
			Decision:       aictx.Deny,
			MatchedPattern: result.MatchedPattern,
			MatchedSource:  "",
			Message:        result.Message,
		}
	}

	// K8s 策略 aictx.NeedConfirm 时由组通用 allow 提升为 aictx.Allow
	if result.Decision == aictx.NeedConfirm && groupAllowDecision != nil {
		return *groupAllowDecision
	}

	if result.Decision == aictx.NeedConfirm {
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}
	return PolicyTestOutput{Decision: aictx.Allow}
}

// groupGenericAllowAllSubCmds 返回所有子命令都被组 allow 命中时的结果，否则 nil。
func groupGenericAllowAllSubCmds(rules []taggedRule, subCmds []string, matchFn MatchFunc) *PolicyTestOutput {
	if len(rules) == 0 {
		return nil
	}
	var firstSource, firstPattern string
	for _, sub := range subCmds {
		matched := false
		for _, tr := range rules {
			if matchFn(tr.Rule, sub) {
				matched = true
				if firstSource == "" {
					firstSource = tr.Source
					firstPattern = tr.Rule
				}
				break
			}
		}
		if !matched {
			return nil
		}
	}
	return &PolicyTestOutput{
		Decision:       aictx.Allow,
		MatchedSource:  firstSource,
		MatchedPattern: firstPattern,
	}
}

func mergeQueryPoliciesForTest(ctx context.Context, current *asset_entity.QueryPolicy, groups []*group_entity.Group) *asset_entity.QueryPolicy {
	var policies []*asset_entity.QueryPolicy
	if current != nil {
		policies = append(policies, current)
	}
	for _, g := range groups {
		p, err := g.GetQueryPolicy()
		if err == nil && p != nil {
			policies = append(policies, p)
		}
	}

	merged := &asset_entity.QueryPolicy{}
	for _, p := range policies {
		expanded := expandQueryPolicy(ctx, p)
		if len(merged.AllowTypes) == 0 && len(expanded.AllowTypes) > 0 {
			merged.AllowTypes = AppendUnique(merged.AllowTypes, expanded.AllowTypes...)
		}
		merged.DenyTypes = AppendUnique(merged.DenyTypes, expanded.DenyTypes...)
		merged.DenyFlags = AppendUnique(merged.DenyFlags, expanded.DenyFlags...)
	}
	return merged
}

func mergeRedisPoliciesForTest(ctx context.Context, current *asset_entity.RedisPolicy, groups []*group_entity.Group) *asset_entity.RedisPolicy {
	var policies []*asset_entity.RedisPolicy
	if current != nil {
		policies = append(policies, current)
	}
	for _, g := range groups {
		p, err := g.GetRedisPolicy()
		if err == nil && p != nil {
			policies = append(policies, p)
		}
	}

	merged := &asset_entity.RedisPolicy{}
	for _, p := range policies {
		expanded := expandRedisPolicy(ctx, p)
		if len(merged.AllowList) == 0 && len(expanded.AllowList) > 0 {
			merged.AllowList = AppendUnique(merged.AllowList, expanded.AllowList...)
		}
		merged.DenyList = AppendUnique(merged.DenyList, expanded.DenyList...)
	}
	return merged
}

func mergeEtcdPoliciesForTest(ctx context.Context, current *asset_entity.EtcdPolicy, groups []*group_entity.Group) *asset_entity.EtcdPolicy {
	var policies []*asset_entity.EtcdPolicy
	if current != nil {
		policies = append(policies, current)
	}
	for _, g := range groups {
		p, err := g.GetEtcdPolicy()
		if err == nil && p != nil {
			policies = append(policies, p)
		}
	}

	merged := &asset_entity.EtcdPolicy{}
	for _, p := range policies {
		expanded := expandEtcdPolicy(ctx, p)
		if len(merged.AllowList) == 0 && len(expanded.AllowList) > 0 {
			merged.AllowList = AppendUnique(merged.AllowList, expanded.AllowList...)
		}
		merged.DenyList = AppendUnique(merged.DenyList, expanded.DenyList...)
	}
	return merged
}

func mergeK8sPoliciesForTest(ctx context.Context, current *asset_entity.K8sPolicy, groups []*group_entity.Group) *asset_entity.K8sPolicy {
	var policies []*asset_entity.K8sPolicy
	if current != nil {
		policies = append(policies, current)
	}
	for _, g := range groups {
		p, err := g.GetK8sPolicy()
		if err == nil && p != nil {
			policies = append(policies, p)
		}
	}

	merged := &asset_entity.K8sPolicy{}
	for _, p := range policies {
		expanded := expandK8sPolicy(ctx, p)
		if len(merged.AllowList) == 0 && len(expanded.AllowList) > 0 {
			merged.AllowList = AppendUnique(merged.AllowList, expanded.AllowList...)
		}
		merged.DenyList = AppendUnique(merged.DenyList, expanded.DenyList...)
	}
	return merged
}

// --- 通用组链解析 ---

// resolveGroupChainForTest 根据 assetID 或 groupID 解析组链。
func resolveGroupChainForTest(ctx context.Context, assetID, groupID int64) []*group_entity.Group {
	var startGroupID int64

	if assetID > 0 {
		asset, err := asset_svc.Asset().Get(ctx, assetID)
		if err == nil && asset != nil {
			startGroupID = asset.GroupID
		}
	} else if groupID > 0 {
		// GroupDetail 编辑：当前组的策略已由调用方提供，从父组开始
		g, err := group_svc.Group().Get(ctx, groupID)
		if err == nil && g != nil {
			startGroupID = g.ParentID
		}
	}

	if startGroupID == 0 {
		return nil
	}

	var chain []*group_entity.Group
	currentID := startGroupID
	for i := 0; i < 5 && currentID > 0; i++ {
		g, err := group_svc.Group().Get(ctx, currentID)
		if err != nil || g == nil {
			break
		}
		chain = append(chain, g)
		currentID = g.ParentID
	}
	return chain
}

// CheckGroupGenericPolicy 在真实执行路径中检查组的通用策略（CmdPolicy）。
// subCmds 是已经按资产语义拆好的执行单元：对 K8s/SSH 这类 shell 类资产应传入
// ExtractSubCommands 的结果；对 Database/Redis/Mongo/Kafka 等单语句类资产传入
// 单元素切片即可。aictx.Deny 优先：任一子命令命中即拒绝；aictx.Allow 必须所有子命令都命中。
func CheckGroupGenericPolicy(ctx context.Context, assetID int64, subCmds []string, matchFn MatchFunc) aictx.CheckResult {
	if len(subCmds) == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil || asset == nil || asset.GroupID == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}

	groups := ResolveGroupChain(ctx, asset.GroupID)

	type taggedGroupRule struct {
		Rule, GroupName string
	}
	var allDeny, allAllow []taggedGroupRule
	for _, g := range groups {
		p, err := g.GetCommandPolicy()
		if err != nil || p == nil {
			continue
		}
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := ResolveCommandGroups(ctx, p.Groups)
			for _, r := range grpDeny {
				allDeny = append(allDeny, taggedGroupRule{r, g.Name})
			}
			for _, r := range grpAllow {
				allAllow = append(allAllow, taggedGroupRule{r, g.Name})
			}
		}
		for _, r := range p.DenyList {
			allDeny = append(allDeny, taggedGroupRule{r, g.Name})
		}
		for _, r := range p.AllowList {
			allAllow = append(allAllow, taggedGroupRule{r, g.Name})
		}
	}

	// deny：任一子命令被命中即拒绝
	for _, sub := range subCmds {
		for _, tr := range allDeny {
			if matchFn(tr.Rule, sub) {
				return aictx.CheckResult{
					Decision:       aictx.Deny,
					Message:        PolicyFmt(ctx, "command denied by group [%s] policy: %s", "命令被组 [%s] 策略禁止: %s", tr.GroupName, sub),
					DecisionSource: aictx.SourcePolicyDeny,
					MatchedPattern: tr.Rule,
				}
			}
		}
	}

	// allow：每个子命令都必须命中某条 allow 规则
	if len(allAllow) == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	var firstMatched string
	for _, sub := range subCmds {
		matched := false
		for _, tr := range allAllow {
			if matchFn(tr.Rule, sub) {
				matched = true
				if firstMatched == "" {
					firstMatched = tr.Rule
				}
				break
			}
		}
		if !matched {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		}
	}
	return aictx.CheckResult{
		Decision:       aictx.Allow,
		DecisionSource: aictx.SourcePolicyAllow,
		MatchedPattern: firstMatched,
	}
}
