package permission

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// CommandConfirmFunc 命令确认回调，发送审批请求并阻塞等待前端响应
// ctx 携带会话 ID 等上下文（通过 aictx.GetConversationID 获取）
// kind: "single", "batch", "grant"
// items: 审批项列表
// 返回 ApprovalResponse
type CommandConfirmFunc func(ctx context.Context, kind string, items []ApprovalItem) ApprovalResponse

// GrantRequestFunc Grant 审批回调，创建 grant 并等待用户审批
// ctx 携带会话 ID 等上下文（通过 aictx.GetConversationID 获取）
// items 为多资产的审批条目列表，用户可能在审批弹窗中编辑
// 返回 (approved, 用户编辑后的 patterns)
type GrantRequestFunc func(ctx context.Context, items []ApprovalItem, reason string) (approved bool, finalPatterns []string)

// CommandPolicyChecker 命令权限检查器，通过 context 注入到两条执行路径
type CommandPolicyChecker struct {
	confirmFunc      CommandConfirmFunc
	grantRequestFunc GrantRequestFunc
}

// NewCommandPolicyChecker 创建权限检查器
func NewCommandPolicyChecker(confirmFunc CommandConfirmFunc) *CommandPolicyChecker {
	return &CommandPolicyChecker{
		confirmFunc: confirmFunc,
	}
}

// ConfirmFunc 返回确认回调，供 batch_command 聚合多条审批项一次性调用。
func (c *CommandPolicyChecker) ConfirmFunc() CommandConfirmFunc {
	return c.confirmFunc
}

// SetGrantRequestFunc 设置 Grant 审批回调
func (c *CommandPolicyChecker) SetGrantRequestFunc(fn GrantRequestFunc) {
	c.grantRequestFunc = fn
}

// SubmitGrant 提交 grant 审批请求（request_permission 工具调用）
func (c *CommandPolicyChecker) SubmitGrant(ctx context.Context, assetID int64, patterns []string, reason string) aictx.CheckResult {
	if c.grantRequestFunc == nil {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyMsg(ctx, "no grant approval mechanism", "无 Grant 审批机制")}
	}

	assetName := ""
	if assetID > 0 {
		asset, err := asset_svc.Asset().Get(ctx, assetID)
		if err != nil {
			logger.Default().Warn("get asset for grant submission", zap.Int64("assetID", assetID), zap.Error(err))
		}
		if asset != nil {
			assetName = asset.Name
		}
	}

	items := make([]ApprovalItem, 0, len(patterns))
	for _, p := range patterns {
		items = append(items, ApprovalItem{
			Type:      "grant",
			AssetID:   assetID,
			AssetName: assetName,
			Command:   p,
			Detail:    reason,
		})
	}

	approved, finalPatterns := c.grantRequestFunc(ctx, items, reason)
	if !approved {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyMsg(ctx, "USER DENIED: The user has denied the grant approval request. Stop the current task immediately.", "用户拒绝：用户已拒绝 Grant 审批请求。请立即停止当前任务。"), DecisionSource: aictx.SourceGrantDeny, MatchedPattern: strings.Join(patterns, "; ")}
	}

	return aictx.CheckResult{Decision: aictx.Allow, Message: policy.PolicyFmt(ctx, "grant approved, %d patterns", "Grant 已批准，共 %d 条模式", len(finalPatterns)), DecisionSource: aictx.SourceGrantAllow, MatchedPattern: strings.Join(finalPatterns, "; ")}
}

// GrantItem represents a single asset's patterns in a multi-asset grant request.
type GrantItem struct {
	AssetID  int64
	Patterns []string
}

// SubmitGrantMulti 提交多资产 grant 审批请求
func (c *CommandPolicyChecker) SubmitGrantMulti(ctx context.Context, items []GrantItem, reason string) aictx.CheckResult {
	if c.grantRequestFunc == nil {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyMsg(ctx, "no grant approval mechanism", "无 Grant 审批机制")}
	}

	approvalItems := make([]ApprovalItem, 0)
	var allPatterns []string
	for _, item := range items {
		assetName := ""
		if item.AssetID > 0 {
			asset, err := asset_svc.Asset().Get(ctx, item.AssetID)
			if err != nil {
				logger.Default().Warn("get asset for grant submission", zap.Int64("assetID", item.AssetID), zap.Error(err))
			}
			if asset != nil {
				assetName = asset.Name
			}
		}
		for _, p := range item.Patterns {
			approvalItems = append(approvalItems, ApprovalItem{
				Type:      "grant",
				AssetID:   item.AssetID,
				AssetName: assetName,
				Command:   p,
				Detail:    reason,
			})
			allPatterns = append(allPatterns, p)
		}
	}

	approved, finalPatterns := c.grantRequestFunc(ctx, approvalItems, reason)
	if !approved {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyMsg(ctx, "USER DENIED: The user has denied the grant approval request. Stop the current task immediately.", "用户拒绝：用户已拒绝 Grant 审批请求。请立即停止当前任务。"), DecisionSource: aictx.SourceGrantDeny, MatchedPattern: strings.Join(allPatterns, "; ")}
	}

	return aictx.CheckResult{Decision: aictx.Allow, Message: policy.PolicyFmt(ctx, "grant approved, %d patterns", "Grant 已批准，共 %d 条模式", len(finalPatterns)), DecisionSource: aictx.SourceGrantAllow, MatchedPattern: strings.Join(finalPatterns, "; ")}
}

// matchGrantPatterns 从 DB 中查找已批准 grant 的 items，用通配匹配命令
// 返回首个匹配的 pattern，空字符串表示未匹配
// groups 为资产所属的组链（组 → 父组 → ... → 根）
func matchGrantPatterns(ctx context.Context, assetID int64, groups []*group_entity.Group, subCmds []string) string {
	return matchGrantPatternsWith(ctx, assetID, groups, subCmds, policy.MatchCommandRule)
}

func matchGrantPatternsWith(ctx context.Context, assetID int64, groups []*group_entity.Group, subCmds []string, matchFn policy.MatchFunc) string {
	sessionID := aictx.GetSessionID(ctx)
	if sessionID == "" {
		return ""
	}
	repo := grant_repo.Grant()
	if repo == nil {
		return ""
	}
	items, err := repo.ListApprovedItems(ctx, sessionID)
	if err != nil || len(items) == 0 {
		return ""
	}

	// 构建资产所属的组 ID 集合，用于匹配 group 级 grant item
	groupIDs := make(map[int64]bool, len(groups))
	for _, g := range groups {
		groupIDs[g.ID] = true
	}

	// 所有子命令都必须匹配某个 grant item
	var firstPattern string
	for _, cmd := range subCmds {
		matched := false
		for _, item := range items {
			if !grantItemMatchesTarget(item, assetID, groupIDs) {
				continue
			}
			if matchFn(item.Command, cmd) {
				matched = true
				if firstPattern == "" {
					firstPattern = item.Command
				}
				break
			}
		}
		if !matched {
			return ""
		}
	}
	return firstPattern
}

// grantItemMatchesTarget 检查 grant item 是否匹配目标资产
// AssetID=0 且 GroupID=0 → 匹配所有资产
// AssetID>0 → 精确匹配资产
// GroupID>0 → 匹配组内资产（检查资产所属组链）
func grantItemMatchesTarget(item *grant_entity.GrantItem, assetID int64, groupIDs map[int64]bool) bool {
	if item.AssetID != 0 {
		return item.AssetID == assetID
	}
	if item.GroupID != 0 {
		return groupIDs[item.GroupID]
	}
	// AssetID=0 且 GroupID=0，匹配所有资产
	return true
}

// Reset 重置会话级白名单（已迁移到 DB Grant，无需内存清理）
func (c *CommandPolicyChecker) Reset() {
}

// Check 检查命令是否允许执行
func (c *CommandPolicyChecker) Check(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	result := CheckPermission(ctx, asset_entity.AssetTypeSSH, assetID, command)
	if result.Decision != aictx.NeedConfirm {
		return result
	}
	return c.HandleConfirm(ctx, assetID, asset_entity.AssetTypeSSH, command)
}

// CheckPolicyOnly 只检查 allow/deny 列表 + DB Grant 匹配，不触发确认回调。
// 向后兼容包装器，内部委托 CheckPermission。
func CheckPolicyOnly(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	return CheckPermission(ctx, asset_entity.AssetTypeSSH, assetID, command)
}

// CheckSQLPolicyForOpsctl 检查 SQL 策略，向后兼容包装器，内部委托 CheckPermission。
func CheckSQLPolicyForOpsctl(ctx context.Context, assetID int64, sqlText string) aictx.CheckResult {
	return CheckPermission(ctx, asset_entity.AssetTypeDatabase, assetID, sqlText)
}

// CheckRedisPolicyForOpsctl 检查 Redis 策略，向后兼容包装器，内部委托 CheckPermission。
func CheckRedisPolicyForOpsctl(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	return CheckPermission(ctx, asset_entity.AssetTypeRedis, assetID, command)
}

// CheckForAsset 按资产类型分发权限检查
func (c *CommandPolicyChecker) CheckForAsset(ctx context.Context, assetID int64, assetType, command string) aictx.CheckResult {
	result := CheckPermission(ctx, assetType, assetID, command)
	if result.Decision != aictx.NeedConfirm {
		return result
	}
	return c.HandleConfirm(ctx, assetID, assetType, command)
}

// HandleConfirm 处理需要用户确认的情况
func (c *CommandPolicyChecker) HandleConfirm(ctx context.Context, assetID int64, assetType, command string) aictx.CheckResult {
	if c.confirmFunc == nil {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyMsg(ctx, "command not authorized and no confirmation mechanism", "命令未授权且无确认机制"), DecisionSource: aictx.SourcePolicyDeny}
	}

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		logger.Default().Warn("get asset for confirm", zap.Int64("assetID", assetID), zap.Error(err))
	}
	assetName := ""
	if asset != nil {
		assetName = asset.Name
	}

	// 映射资产类型到审批项类型
	approvalType := "exec"
	switch assetType {
	case asset_entity.AssetTypeSerial:
		approvalType = "serial"
	case asset_entity.AssetTypeDatabase:
		approvalType = "sql"
	case asset_entity.AssetTypeRedis:
		approvalType = "redis"
	case asset_entity.AssetTypeEtcd:
		approvalType = "etcd"
	case asset_entity.AssetTypeMongoDB:
		approvalType = "mongo"
	case asset_entity.AssetTypeKafka:
		approvalType = "kafka"
	case asset_entity.AssetTypeK8s:
		approvalType = "k8s"
	}

	items := []ApprovalItem{{
		Type:      approvalType,
		AssetID:   assetID,
		AssetName: assetName,
		Command:   command,
	}}
	resp := c.confirmFunc(ctx, "single", items)

	if resp.Decision == "deny" {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyFmt(ctx, "USER DENIED: The user has denied execution of command: %s. Stop the current task immediately.", "用户拒绝：用户已拒绝执行命令: %s。请立即停止当前任务。", command), DecisionSource: aictx.SourceUserDeny}
	}
	if resp.Decision == "allowAll" {
		sessionID := aictx.GetSessionID(ctx)
		// 三条 grant 落库路径（HandleConfirm / opsctl 单审批 / AI grant 流）共用 NormalizeGrantPatterns：
		// SSH/K8s shell 类按 AST 子命令拆，其他类型直通。这里既保持本路径行为一致，
		// 又保证编辑模式（多行/通配）后的每一行都按子命令分别落库。
		var patterns []string
		if len(resp.EditedItems) > 0 {
			for _, item := range resp.EditedItems {
				patterns = append(patterns, NormalizeGrantPatterns(assetType, item.Command)...)
			}
		}
		if len(patterns) == 0 {
			patterns = NormalizeGrantPatterns(assetType, command)
		}
		if len(patterns) == 0 {
			// shell parse 失败或全为空白 — 至少保留原命令一条，避免静默丢 grant
			patterns = []string{command}
		}
		for _, cmd := range patterns {
			SaveGrantPattern(ctx, sessionID, assetID, assetName, cmd)
		}
		audit.WriteGrantSubmitAudit(ctx, assetID, assetName, patterns)
	}
	return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
}

// --- 策略收集 ---

// resolveAssetForPolicy resolves the asset used as the root for policy checks.
func resolveAssetForPolicy(ctx context.Context, assetID int64) *asset_entity.Asset {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		logger.Default().Warn("get asset for policy check", zap.Int64("assetID", assetID), zap.Error(err))
		return nil
	}
	return asset
}

// collectPoliciesFromChain 从策略链中收集指定类型的策略
func collectPoliciesFromChain[T any](holders []policyent.Holder, getter func(policyent.Holder) (*T, error)) []*T {
	var policies []*T
	for _, h := range holders {
		if p, err := getter(h); err == nil && p != nil {
			policies = append(policies, p)
		}
	}
	return policies
}

func policyHoldersForAsset(ctx context.Context, asset *asset_entity.Asset) []policyent.Holder {
	if asset == nil {
		return nil
	}
	holders := []policyent.Holder{asset}
	if asset.GroupID > 0 {
		for _, g := range policy.ResolveGroupChain(ctx, asset.GroupID) {
			holders = append(holders, g)
		}
	}
	return holders
}

func collectPolicies(ctx context.Context, asset *asset_entity.Asset, groups []*group_entity.Group) []*asset_entity.CommandPolicy {
	var holders []policyent.Holder
	if asset != nil {
		holders = append(holders, asset)
	}
	for _, g := range groups {
		holders = append(holders, g)
	}
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.CommandPolicy, error) {
		return h.GetCommandPolicy()
	})
	// 解析每个策略引用的权限组，将组的规则合并进来
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := policy.ResolveCommandGroups(ctx, p.Groups)
			p.AllowList = append(p.AllowList, grpAllow...)
			p.DenyList = append(p.DenyList, grpDeny...)
		}
	}
	return policies
}

// collectQueryPolicies 收集资产 + 组链的 SQL 权限策略并合并
func collectQueryPolicies(ctx context.Context, asset *asset_entity.Asset) *asset_entity.QueryPolicy {
	holders := policyHoldersForAsset(ctx, asset)
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.QueryPolicy, error) {
		return h.GetQueryPolicy()
	})
	if len(policies) == 0 {
		return nil
	}
	// 解析引用的权限组
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllowTypes, grpDenyTypes, grpDenyFlags := policy.ResolveQueryGroups(ctx, p.Groups)
			p.AllowTypes = append(p.AllowTypes, grpAllowTypes...)
			p.DenyTypes = append(p.DenyTypes, grpDenyTypes...)
			p.DenyFlags = append(p.DenyFlags, grpDenyFlags...)
		}
	}
	// 合并：allow_types 取第一个非空（资产优先），deny_types/deny_flags 全部合并
	merged := &asset_entity.QueryPolicy{}
	for _, p := range policies {
		if len(merged.AllowTypes) == 0 && len(p.AllowTypes) > 0 {
			merged.AllowTypes = p.AllowTypes
		}
		merged.DenyTypes = policy.AppendUnique(merged.DenyTypes, p.DenyTypes...)
		merged.DenyFlags = policy.AppendUnique(merged.DenyFlags, p.DenyFlags...)
	}
	return merged
}

// collectRedisPolicies 收集资产 + 组链的 Redis 权限策略并合并
func collectRedisPolicies(ctx context.Context, asset *asset_entity.Asset) *asset_entity.RedisPolicy {
	holders := policyHoldersForAsset(ctx, asset)
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.RedisPolicy, error) {
		return h.GetRedisPolicy()
	})
	if len(policies) == 0 {
		return nil
	}
	// 解析引用的权限组
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := policy.ResolveRedisGroups(ctx, p.Groups)
			p.AllowList = append(p.AllowList, grpAllow...)
			p.DenyList = append(p.DenyList, grpDeny...)
		}
	}
	// 合并：allow_list 取第一个非空（资产优先），deny_list 全部合并
	merged := &asset_entity.RedisPolicy{}
	for _, p := range policies {
		if len(merged.AllowList) == 0 && len(p.AllowList) > 0 {
			merged.AllowList = p.AllowList
		}
		merged.DenyList = policy.AppendUnique(merged.DenyList, p.DenyList...)
	}
	return merged
}

// collectEtcdPolicies 收集资产 + 组链的 etcd 权限策略并合并
// EtcdPolicy 与 RedisPolicy 同构（类型别名），但策略组类型为 etcd，须用 ResolveEtcdGroups 解析。
func collectEtcdPolicies(ctx context.Context, asset *asset_entity.Asset) *asset_entity.EtcdPolicy {
	holders := policyHoldersForAsset(ctx, asset)
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.EtcdPolicy, error) {
		return h.GetEtcdPolicy()
	})
	if len(policies) == 0 {
		return nil
	}
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := policy.ResolveEtcdGroups(ctx, p.Groups)
			p.AllowList = append(p.AllowList, grpAllow...)
			p.DenyList = append(p.DenyList, grpDeny...)
		}
	}
	merged := &asset_entity.EtcdPolicy{}
	for _, p := range policies {
		if len(merged.AllowList) == 0 && len(p.AllowList) > 0 {
			merged.AllowList = p.AllowList
		}
		merged.DenyList = policy.AppendUnique(merged.DenyList, p.DenyList...)
	}
	return merged
}

// collectMongoDBPolicies 收集资产 + 组链的 MongoDB 权限策略并合并
func collectMongoDBPolicies(ctx context.Context, asset *asset_entity.Asset) *asset_entity.MongoPolicy {
	holders := policyHoldersForAsset(ctx, asset)
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.MongoPolicy, error) {
		return h.GetMongoPolicy()
	})
	if len(policies) == 0 {
		return nil
	}
	// 解析引用的权限组
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllowTypes, grpDenyTypes := policy.ResolveMongoGroups(ctx, p.Groups)
			p.AllowTypes = append(p.AllowTypes, grpAllowTypes...)
			p.DenyTypes = append(p.DenyTypes, grpDenyTypes...)
		}
	}
	// 合并：allow_types 取第一个非空（资产优先），deny_types 全部合并
	merged := &asset_entity.MongoPolicy{}
	for _, p := range policies {
		if len(merged.AllowTypes) == 0 && len(p.AllowTypes) > 0 {
			merged.AllowTypes = p.AllowTypes
		}
		merged.DenyTypes = policy.AppendUnique(merged.DenyTypes, p.DenyTypes...)
	}
	return merged
}

// collectKafkaPolicies 收集资产 + 组链的 Kafka 权限策略并合并
func collectKafkaPolicies(ctx context.Context, asset *asset_entity.Asset) *asset_entity.KafkaPolicy {
	holders := policyHoldersForAsset(ctx, asset)
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.KafkaPolicy, error) {
		return h.GetKafkaPolicy()
	})
	if len(policies) == 0 {
		return nil
	}
	// 解析引用的权限组
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := policy.ResolveKafkaGroups(ctx, p.Groups)
			p.AllowList = append(p.AllowList, grpAllow...)
			p.DenyList = append(p.DenyList, grpDeny...)
		}
	}
	// 合并：allow_list 取第一个非空（资产优先），deny_list 全部合并
	merged := &asset_entity.KafkaPolicy{}
	for _, p := range policies {
		if len(merged.AllowList) == 0 && len(p.AllowList) > 0 {
			merged.AllowList = p.AllowList
		}
		merged.DenyList = policy.AppendUnique(merged.DenyList, p.DenyList...)
	}
	return merged
}

// collectK8sPolicies 收集资产 + 组链的 K8s 权限策略并合并
func collectK8sPolicies(ctx context.Context, asset *asset_entity.Asset) *asset_entity.K8sPolicy {
	holders := policyHoldersForAsset(ctx, asset)
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.K8sPolicy, error) {
		return h.GetK8sPolicy()
	})
	if len(policies) == 0 {
		return nil
	}
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := policy.ResolveCommandGroups(ctx, p.Groups)
			p.AllowList = append(p.AllowList, grpAllow...)
			p.DenyList = append(p.DenyList, grpDeny...)
		}
	}
	merged := &asset_entity.K8sPolicy{}
	for _, p := range policies {
		if len(merged.AllowList) == 0 && len(p.AllowList) > 0 {
			merged.AllowList = p.AllowList
		}
		merged.DenyList = policy.AppendUnique(merged.DenyList, p.DenyList...)
	}
	return merged
}

func collectDenyRules(policies []*asset_entity.CommandPolicy) []string {
	rules := make([]string, 0, len(policies))
	for _, p := range policies {
		rules = append(rules, p.DenyList...)
	}
	return rules
}

func collectAllowRules(policies []*asset_entity.CommandPolicy) []string {
	rules := make([]string, 0, len(policies))
	for _, p := range policies {
		rules = append(rules, p.AllowList...)
	}
	return rules
}

// --- context 注入 ---

type policyCheckerKeyType struct{}

// WithPolicyChecker 将 PolicyChecker 注入 context
func WithPolicyChecker(ctx context.Context, c *CommandPolicyChecker) context.Context {
	return context.WithValue(ctx, policyCheckerKeyType{}, c)
}

// GetPolicyChecker 从 context 中获取 PolicyChecker
func GetPolicyChecker(ctx context.Context) *CommandPolicyChecker {
	c, _ := ctx.Value(policyCheckerKeyType{}).(*CommandPolicyChecker)
	return c
}
