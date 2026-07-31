package permission

import (
	"context"
	"errors"
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

// GrantToolCp 是文件传输授权在 grant_items.tool_name 里的取值，同时也是它的审批类型。
// 它的 Command 是路径而非命令，匹配走 policy.MatchPathRule，必须与命令面的 grant
// 彻底隔离——见 grantItemAppliesTo。
const GrantToolCp = "cp"

// CheckPermission 统一权限检查（策略 + DB Grant 匹配）。
// 不包含用户确认逻辑 — aictx.NeedConfirm 时由调用方处理。
// assetType: "ssh" | "serial" | "database" | "redis" | "mongodb" | "kafka" | "k8s" |
// "exec"（exec 等同于 ssh）| "sql"（sql 等同于 database）| "mongo"（mongo 等同于 mongodb）
func CheckPermission(ctx context.Context, assetType string, assetID int64, command string) aictx.CheckResult {
	handler, ok := permissionTypeFor(assetType)
	if !ok {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	return handler.check(ctx, assetID, command)
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
	if grantResult := matchGrantForAssetSubCmds(ctx, assetID, stmtTexts, "sql"); grantResult != nil {
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
	if grantResult := matchGrantForAsset(ctx, assetID, command, "redis"); grantResult != nil {
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

	if grantResult := matchGrantForAsset(ctx, assetID, command, "etcd"); grantResult != nil {
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
	if grantResult := matchGrantForAssetSubCmds(ctx, assetID, subCmds, "k8s"); grantResult != nil {
		return *grantResult
	}

	merged := policy.EffectiveK8sPolicy(ctx, mergedPolicy)
	if len(merged.AllowList) > 0 {
		result.HintRules = merged.AllowList
	}
	return result
}

// --- MongoDB ---

func checkMongoDBPermission(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	// 组通用策略（Mongo 命令是单条，单元素切片）。匹配函数必须与类型专用策略同为
	// MatchMongoRule：统一 exec 送进来的是富命令串，而
	// MatchCommandRule("deleteMany", "deleteMany users --db=prod --query=…") = false
	// ——写成裸 op 的组通用 deny 规则会静默失效。与 redis/etcd 传 MatchRedisRule 同理。
	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, []string{command}, policy.MatchMongoRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	// MongoDB 策略
	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectMongoDBPolicies(ctx, asset)
	result := policy.CheckMongoDBPolicy(ctx, mergedPolicy, command)

	// 组通用 allow 优先于类型专用的 aictx.NeedConfirm
	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	// DB Grant 匹配：同样走 MatchMongoRule（与 Kafka 传 MatchKafkaRule 同理）。
	// 用 MatchCommandRule 会让 grant 绑死审批时那一条命令的 --db/--query，
	// "全部允许"批下来的 pattern 连自己都匹配不上，几乎不可复用。
	if grantResult := matchGrantForAssetWith(ctx, assetID, command, "mongo", policy.MatchMongoRule); grantResult != nil {
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
	if grantResult := matchGrantForAssetWith(ctx, assetID, command, "kafka", policy.MatchKafkaRule); grantResult != nil {
		return *grantResult
	}

	// aictx.NeedConfirm：收集允许的 Kafka action/resource 规则作为提示
	merged := policy.EffectiveKafkaPolicy(ctx, mergedPolicy)
	if len(merged.AllowList) > 0 {
		result.HintRules = merged.AllowList
	}
	return result
}

// --- OSS ---

// ossPolicyStrings 把一条送进 OSS 权限检查的命令展开成它请求的策略串（`<action> <resource>`）。
// derived 报告这些串是不是由 DSL 派生出来的——ossGrantPatterns 靠它区分"系统替用户推导的"
// 与"用户自己写的"（设计 §4.3）。
//
// 两个入口喂进来的形状不同：
//   - exec 送规范 DSL（`object copy S --to=D`），一条命令派生 1~3 条串（§4.1）；
//   - cp 的 OSS 端点送的**已经是策略串**（`object.read b/k`），由传输适配器的
//     ApprovalSubject 给出（§6.2）；用户在审批弹窗里手写的 pattern 也是这个形状。
//
// 判别只看第一个词里有没有 '.'：DSL 的 family 只有 bucket / object 两种，而策略串的
// action 恒带点（bucket.list / object.read / object.presign.write）。两种形状因此不会
// 互相误判，走错分支也不会 fail open——一条 DSL 命令被当策略串读时 action 是 "object"，
// 匹配不上任何 `object.*` 规则，退回审批。
func ossPolicyStrings(command string) (policyStrings []string, derived bool, err error) {
	if isOSSPolicyString(command) {
		return []string{command}, false, nil
	}
	derive, ok := policyStringsFor(asset_entity.AssetTypeOSS)
	if !ok {
		return nil, false, errors.New("oss policy string deriver not registered")
	}
	policyStrings, err = derive(command)
	return policyStrings, true, err
}

// isOSSPolicyString 判断一条输入是否已经是策略串形状：第一个词带 '.'。
func isOSSPolicyString(command string) bool {
	action, _, ok := strings.Cut(strings.TrimSpace(command), " ")
	return ok && strings.Contains(action, ".")
}

// checkOSSPermission 镜像 checkKafkaPermission 的顺序，差别只在"命令 → 策略串"是一对多：
// copy 读源写目的、move 还要删源，任一条被 deny 即整条命令被拒，allow 名单存在时必须
// 每条都命中才放行（设计 §4.1 / D7）。
func checkOSSPermission(ctx context.Context, assetID int64, command string) aictx.CheckResult {
	// 派生失败退回 aictx.NeedConfirm，不整串匹配——与 shell 分支同一 fail-closed 姿态：
	// 拿一条没派生出策略串的原文去撞规则，`*` 会把它当成一条合法策略串放行。
	policyStrings, _, err := ossPolicyStrings(command)
	if err != nil || len(policyStrings) == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}

	// 组通用策略：匹配函数必须是 MatchOSSRule，传 MatchCommandRule 会让写成
	// `object.delete *` 的组通用 deny 规则静默失配（与 mongo/redis/etcd 传各自匹配器同理）。
	groupResult := policy.CheckGroupGenericPolicy(ctx, assetID, policyStrings, policy.MatchOSSRule)
	if groupResult.Decision == aictx.Deny {
		return groupResult
	}

	asset := resolveAssetForPolicy(ctx, assetID)
	mergedPolicy := collectOSSPolicies(ctx, asset)
	result := policy.CheckOSSPolicy(ctx, mergedPolicy, policyStrings)

	// 组通用 allow 优先于类型专用的 aictx.NeedConfirm
	if result.Decision == aictx.NeedConfirm && groupResult.Decision == aictx.Allow {
		return groupResult
	}

	if result.Decision != aictx.NeedConfirm {
		return result
	}

	// DB Grant 匹配：每条策略串都必须命中 grant，不能用单条 grant 覆盖 copy/move 的多个资源。
	if grantResult := matchGrantForAssetSubCmdsWith(ctx, assetID, policyStrings, asset_entity.AssetTypeOSS, policy.MatchOSSRule); grantResult != nil {
		return *grantResult
	}

	// aictx.NeedConfirm：把有效 allow 名单作为提示回给模型
	merged := policy.EffectiveOSSPolicy(ctx, mergedPolicy)
	if len(merged.AllowList) > 0 {
		result.HintRules = merged.AllowList
	}
	return result
}

// ossGrantPatterns 是 OSS 注册的 grant 归一化：一条审批输入 → 常驻授权的 pattern 列表。
//
// 派生失败退回原串，与 shell 分支的"解析失败退回原行"同一姿态。
//
// **resource 以 "/" 结尾的派生串被丢弃**（设计 §4.3 / 决策 D20）：尾随斜杠的 key 是合法的
// 单个对象——S3 用零字节的 `<prefix>/` 当目录标记，本产品自己的"新建文件夹"就在建它们，
// 所以 `object delete mybucket/logs/` 说的是一个对象。但同一个串当规则读时，尾随 "/"
// 意味着递归前缀（policy.MatchOSSRule 的 key 段语义，决策 D5）。原样落库，用户批准一次
// "删掉这个目录标记"并选"始终允许"，换来的就是一条"递归删除 logs/ 下全部对象"的常驻授权。
// 丢弃而不是拒绝命令：目录标记该删得掉，只是这一条不该变成可复用的授权。
//
// 用户手写的策略形式 pattern 不受此限——那是他明确要求的授权范围，与系统替他推导出来的
// 不是一回事；cp 端点给出的策略串同样原样落库，它形状上到不了这里（OSS 端点路径在
// 解析期就拒绝尾随 "/"，§6.2）。
func ossGrantPatterns(command string) []string {
	policyStrings, derived, err := ossPolicyStrings(command)
	if err != nil || len(policyStrings) == 0 {
		return []string{command}
	}
	if !derived {
		return policyStrings
	}
	patterns := make([]string, 0, len(policyStrings))
	for _, ps := range policyStrings {
		if strings.HasSuffix(ps, "/") {
			continue
		}
		patterns = append(patterns, ps)
	}
	return patterns
}

// --- Grant 匹配辅助 ---

// --- 文件传输（cp） ---

// checkFileTransferPermission 校验一次文件传输的远端路径。
//
// 与命令类检查有意不同：只查 grant，不查 CommandPolicy 的 allow/deny 规则——那些
// 规则是命令形状的（`systemctl *`），拿路径去撞它们只会产生误判。匹配用
// policy.MatchPathRule（POSIX glob），与 local_write 的路径白名单同一套语义。
func checkFileTransferPermission(ctx context.Context, assetID int64, remotePath string) aictx.CheckResult {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	if grantResult := matchGrantForAssetWith(ctx, assetID, remotePath, GrantToolCp, policy.MatchPathRule); grantResult != nil {
		return *grantResult
	}
	return aictx.CheckResult{Decision: aictx.NeedConfirm}
}

// matchGrantForAsset 为 database/redis 类型做 DB Grant 匹配。
// toolName 是调用方的审批类型，决定这次匹配看哪个工具面的 grant（见 grantItemAppliesTo）。
func matchGrantForAsset(ctx context.Context, assetID int64, command, toolName string) *aictx.CheckResult {
	return matchGrantForAssetWith(ctx, assetID, command, toolName, policy.MatchCommandRule)
}

func matchGrantForAssetWith(ctx context.Context, assetID int64, command, toolName string, matchFn policy.MatchFunc) *aictx.CheckResult {
	return matchGrantForAssetSubCmdsWith(ctx, assetID, []string{command}, toolName, matchFn)
}

// matchGrantForAssetSubCmds 用 policy.MatchCommandRule 按子命令逐条匹配，专给 shell 类资产（如 K8s）使用。
func matchGrantForAssetSubCmds(ctx context.Context, assetID int64, subCmds []string, toolName string) *aictx.CheckResult {
	return matchGrantForAssetSubCmdsWith(ctx, assetID, subCmds, toolName, policy.MatchCommandRule)
}

func matchGrantForAssetSubCmdsWith(ctx context.Context, assetID int64, subCmds []string, toolName string, matchFn policy.MatchFunc) *aictx.CheckResult {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil
	}
	var groups []*group_entity.Group
	if asset != nil && asset.GroupID > 0 {
		groups = policy.ResolveGroupChain(ctx, asset.GroupID)
	}
	if pattern := matchGrantPatternsWith(ctx, assetID, groups, subCmds, toolName, matchFn); pattern != "" {
		return &aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceGrantAllow, MatchedPattern: pattern}
	}
	return nil
}

// --- SaveGrantPattern ---

// shellGrantPatterns 是 SSH / K8s 注册的 grant 归一化：按行 + policy.ExtractSubCommands 拆。
// 复合命令必须按子命令存，否则 `ls /tmp && cat /etc/hosts` 会被存成单条 pattern，
// 后续 grant 子命令匹配永远命中失败。
// AST 解析失败时退回原行，让上层依旧能存下 grant；下次匹配同样会解析失败走 aictx.NeedConfirm。
func shellGrantPatterns(command string) []string {
	var patterns []string
	for line := range strings.SplitSeq(command, "\n") {
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

// NormalizeGrantPatterns 把一条用户审批输入拆成可独立匹配的 grant pattern 列表。
//
// 拆法由类型注册表上的 grantPatterns 给出（type_registry.go 的 init）：
//   - 未注册归一化函数的类型（sql/redis/mongo/kafka/serial/cp）保留原命令，匹配规则各自处理；
//   - SSH/K8s 走 shellGrantPatterns；
//   - OSS 走 ossGrantPatterns（DSL → 策略串）。
//
// 所有 SaveGrantPattern 调用前都应当先经过这个归一化函数。
func NormalizeGrantPatterns(approvalType, command string) []string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}
	handler, ok := permissionTypeFor(approvalType)
	if !ok || handler.grantPatterns == nil {
		return []string{cmd}
	}
	return handler.grantPatterns(cmd)
}

// SaveGrantPatternsForApproval 用 NormalizeGrantPatterns 拆出 patterns 后依次落库。
// 适合 app 层在多种审批回调（opsctl 单审批、AI grant 流）里调用，避免每个路径重复拆分逻辑。
func SaveGrantPatternsForApproval(ctx context.Context, sessionID string, assetID int64, assetName, approvalType, command string) {
	for _, p := range NormalizeGrantPatterns(approvalType, command) {
		SaveGrantPattern(ctx, sessionID, assetID, assetName, approvalType, p)
	}
}

// SaveGrantPattern 将模式保存为已批准的 GrantItem。
// 如果 sessionID 对应的 GrantSession 不存在，自动创建（状态: approved）。
//
// toolName 取审批类型（"exec" / "redis" / "cp" …），决定这条授权属于哪个工具面：
// 匹配时按它隔离，命令授权与文件传输授权互不可见（见 grantItemAppliesTo）。
func SaveGrantPattern(ctx context.Context, sessionID string, assetID int64, assetName, toolName, command string) {
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
		ToolName:       toolName,
		AssetID:        assetID,
		AssetName:      assetName,
		Command:        command,
		Createtime:     time.Now().Unix(),
	}
	if err := repo.CreateItems(ctx, []*grant_entity.GrantItem{item}); err != nil {
		logger.Default().Error("save grant pattern", zap.Error(err))
	}
}
