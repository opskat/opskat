package permission

import (
	"context"
	"errors"
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
// kind: ApprovalKind* 常量
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

// ConfirmFunc 返回确认回调，供 batch_exec 聚合多条审批项一次性调用。
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
	return matchGrantPatternsWith(ctx, assetID, groups, subCmds, "exec", policy.MatchCommandRule)
}

// grantItemAppliesTo 判断一条 grant item 是否属于当前检查的工具面。
//
// 只在 cp 与非 cp 之间划线，不是按 toolName 严格相等：存量行的 tool_name 一律被旧版
// SaveGrantPattern 写死成 "exec"（含 redis/sql 等），严格相等会让它们集体失效。cp 行
// 只可能由新代码产生，因此这条线是准确的——而它正是必须划的那条：cp 的 pattern 是
// 路径、匹配走 policy.MatchPathRule，被命令面匹配到就意味着一条 `/opt/*` 授权能放行
// 任意命令，反过来一条 `*` 命令授权也不该放行任意文件写入。
func grantItemAppliesTo(item *grant_entity.GrantItem, toolName string) bool {
	return (item.ToolName == GrantToolCp) == (toolName == GrantToolCp)
}

func matchGrantPatternsWith(ctx context.Context, assetID int64, groups []*group_entity.Group, subCmds []string, toolName string, matchFn policy.MatchFunc) string {
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
			if !grantItemAppliesTo(item, toolName) {
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

// Check 检查命令是否允许执行
func (c *CommandPolicyChecker) Check(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	result := CheckPermission(ctx, asset_entity.AssetTypeSSH, assetID, command)
	if result.Decision != aictx.NeedConfirm {
		return result
	}
	return c.HandleConfirm(ctx, assetID, asset_entity.AssetTypeSSH, command)
}

// CheckForAsset 按资产类型分发权限检查。
// detail 可选，透传给审批项供前端展示（cp 用它展示"本地 → 远端"的完整传输方向）。
func (c *CommandPolicyChecker) CheckForAsset(ctx context.Context, assetID int64, assetType, command string, detail ...string) aictx.CheckResult {
	result := CheckPermission(ctx, assetType, assetID, command)
	if result.Decision != aictx.NeedConfirm {
		return result
	}
	return c.HandleConfirm(ctx, assetID, assetType, command, detail...)
}

// HandleConfirm 处理需要用户确认的情况。
// detail 是可选的展示补充（沿用本包 RegisterExecutor 的可选参数写法），
// 只影响审批项在前端的呈现，不参与任何匹配。
func (c *CommandPolicyChecker) HandleConfirm(ctx context.Context, assetID int64, assetType, command string, detail ...string) aictx.CheckResult {
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

	// 与 ApprovalTypeFor 同一张表、同一条不变式：未注册类型回落到原样的 assetType，
	// 不是静默变成 "exec"（type_registry.go 的 ApprovalTypeFor doc comment 有论证）。
	approvalType := ApprovalTypeFor(assetType)

	item := ApprovalItem{
		Type:      approvalType,
		AssetID:   assetID,
		AssetName: assetName,
		Command:   command,
	}
	if len(detail) > 0 {
		item.Detail = detail[0]
	}
	resp := c.confirmFunc(ctx, ApprovalKindSingle, []ApprovalItem{item})
	parsed, parseErr := ParseApprovalResponse(ApprovalKindSingle, resp, []ApprovalItem{item})
	if parseErr != nil || parsed.Decision == ApprovalDeny {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyFmt(ctx, "USER DENIED: The user has denied execution of command: %s. Stop the current task immediately.", "用户拒绝：用户已拒绝执行命令: %s。请立即停止当前任务。", command), DecisionSource: aictx.SourceUserDeny}
	}
	switch parsed.Decision {
	case ApprovalAllow:
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
	case ApprovalAllowAll:
		sessionID := aictx.GetSessionID(ctx)
		if sessionID == "" {
			return aictx.CheckResult{
				Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny,
				Message: policy.PolicyMsg(ctx, "allowAll requires a grant session; execution denied", "全部允许需要授权会话，拒绝执行"),
			}
		}
		// 三条 grant 落库路径（HandleConfirm / opsctl 单审批 / AI grant 流）共用 NormalizeGrantPatterns：
		// SSH/K8s shell 类按 AST 子命令拆，其他类型直通。这里既保持本路径行为一致，
		// 又保证编辑模式（多行/通配）后的每一行都按子命令分别落库。
		//
		// 来源在这条缝上是分得清的，必须原样透传（见 GrantOrigin）：EditedItems 是用户在
		// 弹窗里手写的 pattern，command 是系统交上来的主体（exec 的规范 DSL、cp 的
		// ApprovalSubject）。两者都是策略串形状，混成一种就等于把适配器给出的主体
		// 当用户 pattern 放行——设计 §4.3 记下的正是这个洞。
		//
		// 分支条件是"用户有没有编辑"，**不是**"归一化后是不是空"。两者看起来等价，
		// 因为 shell 类的 shellGrantPatterns 对任何非空白输入至少给一条 pattern（而
		// ParseApprovalResponse 已经保证编辑项的 Command 非空白），所以对 SSH/K8s 这两种
		// 写法逐字节同解。OSS 不然：空列表对它是个正常答案（下面那段注释说的 D20），
		// 于是"编辑了但用不了"会掉进兜底，把用户的编辑**静默换成系统主体**——用户改这
		// 一栏通常是想收窄，却反手拿到一条他没要的更宽授权。用不了就什么都不授权。
		var patterns []string
		if len(parsed.EditedItems) > 0 {
			for _, item := range parsed.EditedItems {
				patterns = append(patterns, NormalizeGrantPatterns(assetType, item.Command, GrantOriginUser)...)
			}
		} else {
			patterns = NormalizeGrantPatterns(assetType, command, GrantOriginSystem)
		}
		// 归一化交出空列表是一个**答案**，不是失败：OSS 会把"批准了但不该变成常驻授权"
		// 的串（决策 D20 的目录标记）全部丢掉。此处不能退回 []string{command} —— 那条串
		// 匹配不上任何策略串，落库只是在授权列表里显示一条用户其实没拿到的授权，
		// 外加一条同样不真实的 grant_submit 审计行。shell 类到不了这里：
		// shellGrantPatterns 对任何非空输入至少给出一条 pattern。
		if len(patterns) > 0 {
			for _, cmd := range patterns {
				SaveGrantPattern(ctx, sessionID, assetID, assetName, approvalType, cmd)
			}
			audit.WriteGrantSubmitAudit(ctx, assetID, assetName, patterns)
		} else {
			// "什么都不落"不能等于"什么都不说"：命令本身仍然照常放行执行（下面的
			// return），用户点的却是"始终允许"——不留痕迹的话，用户会以为自己拿到了一条
			// 常驻授权，下次同样的命令还会再弹一次框，audit_logs 里也查不出原因。
			// 结构化日志只记 assetID/assetType correlation 供现场定位；独立 ToolName 的
			// 审计行把"为什么"（含 command）留在 audit_logs 里——不能复用
			// grant_submit：AuditLogPage 把那个 ToolName 的
			// Command 当已生效的 pattern 聚合展示给用户看，混进去就是本条要防的同一种
			// "显示一条没拿到的授权"，只是换了个地方发生。
			logger.Ctx(ctx).Warn("always-allow approved but normalized to zero grant patterns; nothing persisted",
				zap.Int64("assetID", assetID), zap.String("assetType", assetType))
			audit.WriteGrantDiscardedAudit(ctx, assetID, assetName, command)
		}
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
	default:
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyMsg(ctx, "invalid approval response, execution denied", "审批响应无效，拒绝执行"), DecisionSource: aictx.SourceUserDeny}
	}
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

// collectOSSPolicies 收集资产 + 组链的 OSS 权限策略并合并
func collectOSSPolicies(ctx context.Context, asset *asset_entity.Asset) *asset_entity.OSSPolicy {
	holders := policyHoldersForAsset(ctx, asset)
	policies := collectPoliciesFromChain(holders, func(h policyent.Holder) (*asset_entity.OSSPolicy, error) {
		return h.GetOSSPolicy()
	})
	if len(policies) == 0 {
		return nil
	}
	// 解析引用的权限组
	for _, p := range policies {
		if len(p.Groups) > 0 {
			grpAllow, grpDeny := policy.ResolveOSSGroups(ctx, p.Groups)
			p.AllowList = append(p.AllowList, grpAllow...)
			p.DenyList = append(p.DenyList, grpDeny...)
		}
	}
	// 合并：allow_list 取第一个非空（资产优先），deny_list 全部合并
	merged := &asset_entity.OSSPolicy{}
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

// getPolicyChecker 从 context 中取 PolicyChecker，可能为 nil。
//
// **故意不导出**：包外只能经 RequireChecker / RequireCheckerOrPreapproved 拿它，
// 这两个都在缺失时返回错误。`if checker := GetPolicyChecker(ctx); checker != nil { 检查 }`
// 这种"注入缺失 == 放行"的写法因此不再写得出来——这是 #249 的根因，靠评审和注释是
// 挡不住的，得让它在类型层面不可达。
func getPolicyChecker(ctx context.Context) *CommandPolicyChecker {
	c, _ := ctx.Value(policyCheckerKeyType{}).(*CommandPolicyChecker)
	return c
}

// RequireChecker 取出 PolicyChecker，缺失即报错。没有任何豁免。
//
// 给那些没有 opsctl 对应物的路径用：batch_exec（只对 AI 会话开放）、扩展策略确认
// （opsctl 的 requireApproval 不认识扩展 manifest 里的 action）、request_permission。
func RequireChecker(ctx context.Context) (*CommandPolicyChecker, error) {
	if c := getPolicyChecker(ctx); c != nil {
		return c, nil
	}
	return nil, errors.New("permission checker not available")
}

type preapprovedKeyType struct{}

// WithPreapproved 声明：本次调用的权限检查已经由调用方在工具之外完成，工具内不必再查。
//
// 唯一的合法使用者是 opsctl：它在 requireApproval 里跑完策略 / DB Grant / 桌面审批
// （cmd/opsctl/command/approval.go），拿到 ApprovalResult 之后才派发 handler，因此
// 派发时 context 里没有 PolicyChecker——那是桌面 AI 会话专属的。
//
// 存在的意义是把"没有 checker"这个状态一分为二：**声明过**的是 opsctl 已检查，
// **没声明过**的是接线漏了。后者必须失败（见 RequireChecker），否则漏接线等于放行。
func WithPreapproved(ctx context.Context) context.Context {
	return context.WithValue(ctx, preapprovedKeyType{}, true)
}

// RequireCheckerOrPreapproved 取出 PolicyChecker，fail-closed，但认 WithPreapproved 豁免。
//
// 返回 (nil, nil) **只**发生在 ctx 被 WithPreapproved 标记过时——调用方据此跳过检查。
// 既没有 checker 又没有标记，说明这条调用路径压根没接上权限系统，返回错误而不是放行：
// 从前这里写成 `if checker != nil { 检查 }`，语义是"注入缺失 == 放行"，安全不变式全靠
// internal/app/ai/chat.go 里两个字段的赋值顺序兜着，既没有类型表达也没有测试锁定。
func RequireCheckerOrPreapproved(ctx context.Context) (*CommandPolicyChecker, error) {
	if c := getPolicyChecker(ctx); c != nil {
		return c, nil
	}
	if preapproved, _ := ctx.Value(preapprovedKeyType{}).(bool); preapproved {
		return nil, nil
	}
	return nil, errors.New("permission checker not available")
}
