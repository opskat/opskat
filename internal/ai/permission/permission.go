package permission

import (
	"context"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// CheckPermission 统一权限检查（策略 + DB Grant 匹配）。
// 不包含用户确认逻辑 — aictx.NeedConfirm 时由调用方处理。
// assetType: "ssh" | "serial" | "database" | "redis" | "mongodb" | "kafka" | "k8s" |
// "exec"（exec 等同于 ssh）| "sql"（sql 等同于 database）| "mongo"（mongo 等同于 mongodb）
func CheckPermission(ctx context.Context, assetType string, assetID int64, command string) aictx.CheckResult {
	// opsctl 使用的类型名映射到内部类型
	switch assetType {
	case "exec":
		assetType = asset_entity.AssetTypeSSH
	case "sql":
		assetType = asset_entity.AssetTypeDatabase
	case "mongo":
		assetType = asset_entity.AssetTypeMongoDB
	}

	switch assetType {
	case asset_entity.AssetTypeSSH, asset_entity.AssetTypeSerial:
		// SSH 与串口共用同一份命令策略（CommandPolicy + grant）。
		return checkCommandPolicyPermission(ctx, assetID, command)
	case asset_entity.AssetTypeDatabase:
		return checkDatabasePermission(ctx, assetID, command)
	case asset_entity.AssetTypeRedis:
		return checkRedisPermission(ctx, assetID, command)
	case asset_entity.AssetTypeEtcd:
		return checkEtcdPermission(ctx, assetID, command)
	case asset_entity.AssetTypeMongoDB:
		return checkMongoDBPermission(ctx, assetID, command)
	case asset_entity.AssetTypeKafka:
		return checkKafkaPermission(ctx, assetID, command)
	case asset_entity.AssetTypeK8s:
		return checkK8sPermission(ctx, assetID, command)
	default:
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
}

// --- SSH / Serial（共用 shell 命令策略） ---

// checkCommandPolicyPermission 走 CommandPolicy + grant 的命令策略校验，
// 适用于所有把"命令文本"作为执行单元的资产类型（目前是 SSH 和串口）。
func checkCommandPolicyPermission(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	// 解析失败或没有可枚举的执行单元（注释/空白等）都退回 aictx.NeedConfirm，
	// 不能整串匹配，否则 `allow *` 会误放行 parser 失败或仅注释的输入。
	subCmds, err := policy.ExtractSubCommands(command)
	if err != nil || len(subCmds) == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		logger.Default().Warn("get asset for permission check", zap.Int64("assetID", assetID), zap.Error(err))
	}
	var groups []*group_entity.Group
	if asset != nil && asset.GroupID > 0 {
		groups = policy.ResolveGroupChain(ctx, asset.GroupID)
	}

	// 策略检查
	allPolicies := collectPolicies(ctx, asset, groups)
	allDenyRules := collectDenyRules(allPolicies)
	allAllowRules := collectAllowRules(allPolicies)

	// deny list
	for _, cmd := range subCmds {
		for _, rule := range allDenyRules {
			if policy.MatchCommandRule(rule, cmd) {
				assetName := ""
				if asset != nil {
					assetName = asset.Name
				}
				hints := policy.FindHintRules(cmd, allAllowRules)
				reason := policy.PolicyMsg(ctx, "command blocked by policy", "命令被策略禁止执行")
				msg := policy.FormatDenyMessage(ctx, assetName, command, reason, hints)
				return aictx.CheckResult{Decision: aictx.Deny, Message: msg, HintRules: hints, DecisionSource: aictx.SourcePolicyDeny, MatchedPattern: rule}
			}
		}
	}

	// allow list
	if len(allAllowRules) > 0 {
		if ok, matched := policy.AllSubCommandsAllowed(subCmds, allAllowRules); ok {
			return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow, MatchedPattern: matched}
		}
	}

	// DB Grant 匹配
	if grantPattern := matchGrantPatterns(ctx, assetID, groups, subCmds); grantPattern != "" {
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceGrantAllow, MatchedPattern: grantPattern}
	}

	// 只返回与命令相似的 allow 规则作为提示
	var filteredHints []string
	seen := make(map[string]bool)
	for _, cmd := range subCmds {
		for _, h := range policy.FindHintRules(cmd, allAllowRules) {
			if !seen[h] {
				filteredHints = append(filteredHints, h)
				seen[h] = true
			}
		}
	}
	return aictx.CheckResult{Decision: aictx.NeedConfirm, HintRules: filteredHints}
}

// --- Database ---

func checkDatabasePermission(ctx context.Context, assetID int64, sqlText string) aictx.CheckResult {
	// 先解析一次 SQL，再把每条语句单独送入组通用/类型策略与 Grant 匹配。
	// 整串传入会被 `SELECT *` 一类的组规则一次性放行，让 `SELECT 1; UPDATE users ...`
	// 把后续高危语句藏进分号后绕过；`UPDATE *` 类 deny 同样命中不到尾部语句。
	stmts, err := policy.ClassifyStatements(sqlText)
	if err != nil {
		return aictx.CheckResult{Decision: aictx.Deny, Message: policy.PolicyFmt(ctx, "SQL parse failed, execution denied: %v", "SQL 解析失败，拒绝执行: %v", err)}
	}

	stmtTexts := policy.StmtRawTexts(stmts)
	if len(stmtTexts) == 0 {
		stmtTexts = []string{sqlText}
	}

	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, stmtTexts, policy.MatchCommandRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectQueryPolicies(ctx, asset)
	result := policy.CheckQueryPolicy(ctx, mergedPolicy, stmts)

	// 组通用 allow 优先于类型专用的 aictx.NeedConfirm
	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	// DB Grant 匹配：每条语句都必须命中 grant，不能用单条 grant 整串覆盖多语句
	if grantResult := matchGrantForAssetSubCmds(ctx, assetID, stmtTexts); grantResult != nil {
		return *grantResult
	}

	// aictx.NeedConfirm：收集允许的 SQL 类型作为提示
	merged := policy.EffectiveQueryPolicy(ctx, mergedPolicy)
	if len(merged.AllowTypes) > 0 {
		result.HintRules = merged.AllowTypes
	}
	return result
}

// --- Redis ---

func checkRedisPermission(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	// 组通用策略（Redis 单语句，单元素切片）
	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, []string{command}, policy.MatchRedisRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	// Redis 策略
	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectRedisPolicies(ctx, asset)
	result := policy.CheckRedisPolicy(ctx, mergedPolicy, command)

	// 组通用 allow 优先于类型专用的 aictx.NeedConfirm
	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	// DB Grant 匹配
	if grantResult := matchGrantForAsset(ctx, assetID, command); grantResult != nil {
		return *grantResult
	}

	// aictx.NeedConfirm：收集允许的 Redis 命令作为提示
	merged := policy.EffectiveRedisPolicy(ctx, mergedPolicy)
	if len(merged.AllowList) > 0 {
		result.HintRules = merged.AllowList
	}
	return result
}

// --- Etcd ---

// checkEtcdPermission 镜像 Redis 策略检查流程：组通用 → etcd 策略 → grant 匹配。
// EtcdPolicy 是 RedisPolicy 的类型别名，匹配规则复用 MatchRedisRule。
func checkEtcdPermission(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, []string{command}, policy.MatchRedisRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectEtcdPolicies(ctx, asset)
	result := policy.CheckEtcdPolicy(ctx, mergedPolicy, command)

	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	if grantResult := matchGrantForAsset(ctx, assetID, command); grantResult != nil {
		return *grantResult
	}

	merged := policy.EffectiveEtcdPolicy(ctx, mergedPolicy)
	if len(merged.AllowList) > 0 {
		result.HintRules = merged.AllowList
	}
	return result
}

// --- K8s ---

func checkK8sPermission(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	// K8s 也是 shell 类，组通用策略要按 AST 子命令逐条比对，避免整串匹配把
	// `kubectl get pods && curl evil` 这类组合命令误放行。
	// 解析失败或子命令为空（注释/空白等）一律 aictx.NeedConfirm，不退回整串。
	subCmds, err := policy.ExtractSubCommands(command)
	if err != nil || len(subCmds) == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}

	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, subCmds, policy.MatchCommandRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectK8sPolicies(ctx, asset)
	result := policy.CheckK8sPolicy(ctx, mergedPolicy, command)

	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	// K8s grant 也要按子命令逐条匹配，否则 `kubectl get *` 整串匹配会让
	// `kubectl get pods && kubectl apply -f x.yaml` 被错误放行。
	if grantResult := matchGrantForAssetSubCmds(ctx, assetID, subCmds); grantResult != nil {
		return *grantResult
	}

	merged := policy.EffectiveK8sPolicy(ctx, mergedPolicy)
	if len(merged.AllowList) > 0 {
		result.HintRules = merged.AllowList
	}
	return result
}

// --- MongoDB ---

func checkMongoDBPermission(ctx context.Context, assetID int64, operation string) aictx.CheckResult {
	// 组通用策略（Mongo 操作是单 token，单元素切片）
	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, []string{operation}, policy.MatchCommandRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	// MongoDB 策略
	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectMongoDBPolicies(ctx, asset)
	result := policy.CheckMongoDBPolicy(ctx, mergedPolicy, operation)

	// 组通用 allow 优先于类型专用的 aictx.NeedConfirm
	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	// DB Grant 匹配
	if grantResult := matchGrantForAsset(ctx, assetID, operation); grantResult != nil {
		return *grantResult
	}

	// aictx.NeedConfirm：收集允许的 MongoDB 操作类型作为提示
	merged := policy.EffectiveMongoPolicy(ctx, mergedPolicy)
	if len(merged.AllowTypes) > 0 {
		result.HintRules = merged.AllowTypes
	}
	return result
}

// --- Kafka ---

func checkKafkaPermission(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	// 组通用策略：使用通用 shell-glob 匹配，与 Database/MongoDB 一致；
	// policy.MatchKafkaRule 仅适用于 "<action> <resource>" 格式，不能用于通用 CommandPolicy。
	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, []string{command}, policy.MatchCommandRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	// Kafka 策略
	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectKafkaPolicies(ctx, asset)
	result := policy.CheckKafkaPolicy(ctx, mergedPolicy, command)

	// 组通用 allow 优先于类型专用的 aictx.NeedConfirm
	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	// DB Grant 匹配
	if grantResult := matchGrantForAssetWith(ctx, assetID, command, policy.MatchKafkaRule); grantResult != nil {
		return *grantResult
	}

	// aictx.NeedConfirm：收集允许的 Kafka action/resource 规则作为提示
	merged := policy.EffectiveKafkaPolicy(ctx, mergedPolicy)
	if len(merged.AllowList) > 0 {
		result.HintRules = merged.AllowList
	}
	return result
}

// --- Grant 匹配辅助 ---

// matchGrantForAsset 为 database/redis 类型做 DB Grant 匹配
func matchGrantForAsset(ctx context.Context, assetID int64, command string) *aictx.CheckResult {
	return matchGrantForAssetWith(ctx, assetID, command, policy.MatchCommandRule)
}

func matchGrantForAssetWith(ctx context.Context, assetID int64, command string, matchFn policy.MatchFunc) *aictx.CheckResult {
	return matchGrantForAssetSubCmdsWith(ctx, assetID, []string{command}, matchFn)
}

// matchGrantForAssetSubCmds 用 policy.MatchCommandRule 按子命令逐条匹配，专给 shell 类资产（如 K8s）使用。
func matchGrantForAssetSubCmds(ctx context.Context, assetID int64, subCmds []string) *aictx.CheckResult {
	return matchGrantForAssetSubCmdsWith(ctx, assetID, subCmds, policy.MatchCommandRule)
}

func matchGrantForAssetSubCmdsWith(ctx context.Context, assetID int64, subCmds []string, matchFn policy.MatchFunc) *aictx.CheckResult {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil
	}
	var groups []*group_entity.Group
	if asset != nil && asset.GroupID > 0 {
		groups = policy.ResolveGroupChain(ctx, asset.GroupID)
	}
	if pattern := matchGrantPatternsWith(ctx, assetID, groups, subCmds, matchFn); pattern != "" {
		return &aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceGrantAllow, MatchedPattern: pattern}
	}
	return nil
}

// --- SaveGrantPattern ---

// isShellLikeApprovalType 判断审批类型是否走 shell（SSH/K8s），grant 保存时需要按 AST 子命令拆。
// 接受审批协议字符串（"exec"）以及 asset_entity 的内部类型常量（AssetTypeSSH/AssetTypeK8s）。
func isShellLikeApprovalType(t string) bool {
	switch t {
	case "exec", asset_entity.AssetTypeSSH, asset_entity.AssetTypeK8s:
		return true
	}
	return false
}

// NormalizeGrantPatterns 把一条用户审批输入拆成可独立匹配的 grant pattern 列表。
//
// 设计要点：
//   - SSH/K8s 等 shell 类资产：按行 + policy.ExtractSubCommands 拆，复合命令必须按子命令存，
//     否则 `ls /tmp && cat /etc/hosts` 会被存成单条 pattern，后续 grant 子命令匹配永远命中失败。
//   - 非 shell 类资产（sql/redis/mongo/kafka）：保留原命令，匹配规则各自处理。
//   - 解析失败时退回原行，让上层依旧能存下 grant；下次匹配同样会解析失败走 aictx.NeedConfirm。
//
// 所有 SaveGrantPattern 调用前都应当先经过这个归一化函数。
func NormalizeGrantPatterns(approvalType, command string) []string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}
	if !isShellLikeApprovalType(approvalType) {
		return []string{cmd}
	}
	var patterns []string
	for line := range strings.SplitSeq(cmd, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		subCmds, _ := policy.ExtractSubCommands(line)
		if len(subCmds) == 0 {
			patterns = append(patterns, line)
		} else {
			patterns = append(patterns, subCmds...)
		}
	}
	return patterns
}

// SaveGrantPatternsForApproval 用 NormalizeGrantPatterns 拆出 patterns 后依次落库。
// 适合 app 层在多种审批回调（opsctl 单审批、AI grant 流）里调用，避免每个路径重复拆分逻辑。
func SaveGrantPatternsForApproval(ctx context.Context, sessionID string, assetID int64, assetName, approvalType, command string) {
	for _, p := range NormalizeGrantPatterns(approvalType, command) {
		SaveGrantPattern(ctx, sessionID, assetID, assetName, p)
	}
}

// SaveGrantPattern 将命令模式保存为已批准的 GrantItem。
// 如果 sessionID 对应的 GrantSession 不存在，自动创建（状态: approved）。
func SaveGrantPattern(ctx context.Context, sessionID string, assetID int64, assetName string, command string) {
	if sessionID == "" || command == "" {
		return
	}
	repo := grant_repo.Grant()
	if repo == nil {
		return
	}

	// 确保 session 存在（create-if-not-exists）
	if _, err := repo.GetSession(ctx, sessionID); err != nil {
		session := &grant_entity.GrantSession{
			ID:         sessionID,
			Status:     grant_entity.GrantStatusApproved,
			Createtime: time.Now().Unix(),
		}
		if createErr := repo.CreateSession(ctx, session); createErr != nil {
			// 可能并发创建，忽略重复错误
			logger.Default().Debug("create grant session (may already exist)", zap.String("sessionID", sessionID), zap.Error(createErr))
		}
	}

	item := &grant_entity.GrantItem{
		GrantSessionID: sessionID,
		ToolName:       "exec",
		AssetID:        assetID,
		AssetName:      assetName,
		Command:        command,
		Createtime:     time.Now().Unix(),
	}
	if err := repo.CreateItems(ctx, []*grant_entity.GrantItem{item}); err != nil {
		logger.Default().Error("save grant pattern", zap.Error(err))
	}
}
