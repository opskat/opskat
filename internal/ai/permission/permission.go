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
//
// derived 报告的是**形状**：true 表示这些串是从一条 DSL 命令派生出来的，false 表示输入
// 本身就是策略串。它不报告作者——那是 GrantOrigin 的事，两者一起才能决定要不要收窄
// （设计 §4.3：只有"用户手写的策略串"该豁免，而适配器给出的主体与它同形）。
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

	// 桶段为空的资源不可**放行**：它指不到任何真实的桶，所以没有一条规则真的授权了它。
	//
	// 这类串来自 helper.ossSubjectResource 给形态错误的端点路径打的记号（原样路径 + 强制
	// 前导 "/"）：前缀当单对象源、桶段带通配、resource 两端带空白。它们到不了 List，
	// 主体却已经生成了，而 policy.splitOSSResource 把 "/mybucket/logs/" 切成
	// bucket="" key="mybucket/logs/" —— 空桶段**不是**天然匹配不上：path.Match("*", "")
	// 为真，于是内置默认策略 builtin:oss-readonly 的 `object.read *` / `object.list *`
	// 照单全收，`cp 's3-prod:/mybucket/logs/' /tmp/out` 一个框都不弹就记下一行
	// policy_allow，之后才由 ossAdapter.List 报错。审计因此记着一件没发生过的事。
	//
	// 判定只落在放行这一侧，deny 不受影响（上面那条 Deny 已经先返回了）：畸形串仍然是切得出
	// 两段的策略串，deny 规则照旧匹配得上。反过来做——让主体整个不成其为策略串——会让 deny
	// 一条都匹配不上，那才是 fail-open。
	//
	// 挡在这里而不是改 policy.MatchOSSRule：那是策略测试面板与规则语义的共用实现，
	// 而这条判断说的是"这个**名字**指不到东西"，不是"这条规则不成立"。
	if result.Decision != aictx.Deny && !ossPolicyStringsNameBuckets(policyStrings) {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}

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

// ossPolicyStringsNameBuckets 报告这一批策略串的 resource 段是不是都指名了一个桶。
//
// 判据是 resource 的第一个字节不是 "/"：policy.splitOSSResource 按第一个 "/" 切
// <bucket>/<key>，所以桶段为空**当且仅当** resource 以 "/" 打头。取 strings.Fields 的第
// 二段而不是自己复刻 policy.splitOSSRule 的空白切分：这里只需要 resource 的首字节，
// 而 Fields 对任意空白（含制表符）都给出同一个答案，两份切分逻辑因此不会漂移。
// 切不出两段的串同样报 false —— 它连策略串都不是，policy.MatchOSSRule 对它一律失配。
func ossPolicyStringsNameBuckets(policyStrings []string) bool {
	for _, ps := range policyStrings {
		fields := strings.Fields(ps)
		if len(fields) < 2 || strings.HasPrefix(fields[1], "/") {
			return false
		}
	}
	return true
}

// ossGrantPatterns 是 OSS 注册的 grant 归一化：一条审批输入 → 常驻授权的 pattern 列表。
//
// 这里是策略串从**名字**变成**规则**的那一步，收窄只发生在这里（决策 D21 更正，见
// ossGrantRule）。豁免看的是来源而不是形状：用户在审批弹窗里手写的策略串是他明确要求的
// 授权范围，原样落库；系统替他推导出来的——exec 的规范 DSL、传输适配器的 ApprovalSubject
// ——一律收窄。两者都是策略串形状，所以只能靠 origin 区分（设计 §4.3 的【实施期更正】）。
//
// 派生失败不落 grant：一条派生不出策略串的原文当规则读时，action 段没有点，
// 匹配不上任何策略串（policy.MatchOSSRule），落库只会在授权列表里显示一条用户其实
// 没拿到的授权。什么都不落是同样 fail-closed 的答案，且不留死行。
func ossGrantPatterns(command string, origin GrantOrigin) []string {
	policyStrings, derived, err := ossPolicyStrings(command)
	if err != nil || len(policyStrings) == 0 {
		return nil
	}
	// 用户手写的策略串原样落库。DSL 形状的输入即便由用户敲进来也要走收窄：他写的是一条
	// **命令**，而命令与规则对同一个字符串的读法本来就不同（这正是 D20 的那条落差）。
	if !derived && origin == GrantOriginUser {
		return policyStrings
	}
	patterns := make([]string, 0, len(policyStrings))
	for _, ps := range policyStrings {
		if rule, ok := ossGrantRule(ps); ok {
			patterns = append(patterns, rule)
		}
	}
	return patterns
}

// ossGrantRule 把一条策略串从"名字"翻译成落成常驻授权的"规则"（决策 D21 更正）。
//
// 同一个字符串在两个角色上要的东西相反：作为**名字**（被 policy.MatchOSSRule 拿去撞规则）
// 必须保持原始 key，作为**规则**必须只覆盖它自己。两边都转义的结果是
// path.Match(`b/secrets\*`, `b/secrets\*`) = false —— 一条谁也匹配不上的死 grant，
// 含元字符的 key 上"始终允许"于是永远不生效。正确配对是规则转义、名字原样：
// path.Match(`b/secrets\*`, "b/secrets*") = true，而对 "b/secretsFOO" 为 false。
//
// ok 为 false 表示这条串不该落成常驻授权（决策 D20）。
func ossGrantRule(ps string) (rule string, ok bool) {
	action, resource, twoSegments := strings.Cut(ps, " ")
	if !twoSegments {
		// 切不出两段的串当规则读时匹配不上任何东西（policy.splitOSSRule 直接 !ok），
		// 落库就是一条死行。
		return "", false
	}

	// 桶段为空的 resource（helper.ossSubjectResource 给形态错误的端点路径打的记号：原样
	// 路径 + 强制前导 "/"）落成规则同样是死行：path.Match("", "mybucket") 为假，它盖不住
	// 任何真实资源；而它唯一盖得住的那种畸形名字，checkOSSPermission 已经拒绝放行了。
	// 不落库好过在授权列表里显示一条用户其实没拿到的授权。
	if strings.HasPrefix(resource, "/") {
		return "", false
	}

	// 决策 D20：resource 以 "/" 收尾的**单对象**操作不落 grant。尾随斜杠的 key 是合法的
	// 单个对象——S3 用零字节的 `<prefix>/` 当目录标记，本产品自己的"新建文件夹"就在建
	// 它们，所以 `object delete mybucket/logs/` 说的是一个对象；而同一个串当规则读时，
	// 尾随 "/" 意味着递归前缀（决策 D5）。批准一次"删掉这个目录标记"换来一条"递归删除
	// logs/ 下全部对象"的常驻授权，正是 §3.3 那类"批准一件事、拿到另一件"。
	//
	// 列举不在此列：`object list mybucket/logs/` 的命令语义就是"列 logs/ 底下"，
	// 规则语义也是"可列 logs/ 底下"，两者是同一个范围，没有那条落差。丢掉它反而让
	// cp 的递归展开永远拿不到常驻授权、每次重新弹框，也让同一个字符串在 exec 面被丢、
	// 在 cp 面被留（设计 §4.3 对 DirList 主体的规定与 §6.2「两条入口的授权互相复用」
	// 直接冲突）。别照着"以 / 收尾"这个症状把它改回去。
	if strings.HasSuffix(resource, "/") && !ossListAction(action) {
		return "", false
	}

	// 桶段与 key 段都要转义，理由是同一条：`object get 'my*/k'` 派生的
	// "object.read my*/k" 当规则读时桶段的 `*` 是跨桶通配，一条授权覆盖 mybucket/k。
	// 桶段永远不是前缀形态（按第一个 "/" 切），所以它走不带豁免的 escapeGlobMeta。
	//
	// `bucket.list *` 的占位 `*` 不需要豁免：它是这条 action 唯一的 resource 形态，
	// 转义成 `\*` 之后 path.Match(`\*`, "*") 仍然为真，往返照样成立（实测），
	// 而豁免会多出一条谁也测不到的分支。
	bucket, key, hasKey := strings.Cut(resource, "/")
	escaped := escapeGlobMeta(bucket)
	if hasKey {
		escaped += "/" + escapeOSSRuleKey(key)
	}
	return action + " " + escaped, true
}

// ossListAction 报告这条 action 的 resource 段本来就是一个前缀而不是单个对象
// （设计 §3.3 表格里的 bucket.list / object.list）。判定用后缀而不是逐个列举动作名：
// 这张表由 internal/ai/helper 的派生持有，本包抄一份只会漂移。
func ossListAction(action string) bool {
	return strings.HasSuffix(action, ".list")
}

// globMetaChars 是 path.Match 在**模式侧**当成语法的字符：三个通配元字符加上转义符本身。
const globMetaChars = `*?[\`

// escapeGlobMeta 把一个具体名字转成"只匹配它自己"的 path.Match 模式（决策 D21）。
//
// OSS 的规则（policy.MatchOSSRule）与 cp 的路径规则（policy.MatchPathRule）都建立在
// path.Match 上，而两种名字——S3 的 key 与远端文件路径——都允许字面量 `* ? [`：不转义的话，
// 一条从具体名字派生出来的规则比这个名字本身宽。实测 path.Match("secrets*", "secretsFOO")
// = true、path.Match("logs/a[1].log", "logs/a1.log") = true，批准一个对象/一条路径换来的是
// 一批。匹配器与规则语法都不动：path.Match 原生认 `\`，而 policy.MatchPathRule 同时是
// local_write / local_edit 白名单的匹配器（决策 D17），改它的语义会波及本地写授权。
//
// 反向的解法（拒绝含元字符的名字）不成立：递归展开本来就会合法产出这种名字
// （path.Match("dist/*", "dist/a[1].js") = true），拒绝等于 cp 传不了自己刚列出来的东西。
//
// `\` 必须在集合里：一个孤立的 `\` 会让 path.Match 对整条模式返回 ErrBadPattern，而
// policy.MatchOSSRule / MatchPathRule 都把 error 当 false —— 对 deny 规则就是静默的
// fail-open，对 allow 就是一条谁也匹配不上的死 grant。
// 按字节扫描是安全的：四个元字符都是 ASCII，而 UTF-8 的续字节一律 >= 0x80。
// `]` 不在集合里也是安全的：`[` 一旦被转义，字符类就永远开不了，类外的 `]` 是字面量。
func escapeGlobMeta(s string) string {
	if !strings.ContainsAny(s, globMetaChars) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := range len(s) {
		if strings.IndexByte(globMetaChars, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// escapeOSSRuleKey 转义一条规则的 key 段。
//
// **前缀形态的 key（空串，或以 "/" 收尾）原样返回**：规则侧对它走的根本不是 path.Match，
// 而是 strings.HasPrefix 字面比较（policy.matchOSSResource，决策 D5 的"该前缀下任意
// 深度"），那条路径上没有转义语法。给前缀加反斜杠不会收窄任何东西，只会让这条规则一条
// 真实的 key 都盖不住——cp 一次 `s3:/b/a\b/` 点"始终允许"，落库的就是一条什么都不授权
// 的死 grant，永远重复弹框。
//
// 这条豁免是 cd012457 修掉的那个缺陷，它的根因（HasPrefix vs path.Match）与 D21 更正的
// 名字/规则之分不同，因此转义搬到哪个接缝上都必须跟着搬。
func escapeOSSRuleKey(key string) string {
	if strings.HasSuffix(key, "/") {
		return key
	}
	return escapeGlobMeta(key)
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

// cpGrantPatterns 是 cp 注册的 grant 归一化：一条审批主体 → 常驻授权的 pattern。
//
// 这是路径从**名字**变成**规则**的那一步，收窄只发生在这里——与 OSS 的 ossGrantRule
// 同一条理由，也是同一个答案（决策 D21 更正：规则转义、名字原样）。
//
// cp 的主体是路径本身，而路径可以合法地含 glob 元字符：`cp ./x 'web-01:/etc/*'` 指名的是
// 一个名字就叫 `*` 的文件（引号挡住了本地 shell，远端 shell 没参与），递归展开同样会产出
// `a[1].log` 这类真实文件名。原样落库的后果是 policy.MatchPathRule 把它当通配读：一条
// `/etc/*` 的 grant 授权 /etc 下的每个文件，而 cp 的 grant **不分方向**
// （checkFileTransferPermission 只看路径），于是"批准往一个文件写"换来"读遍一个目录"。
// 转义之后 path.Match(`/etc/\*`, "/etc/*") 仍为真、对 "/etc/passwd" 为假，"始终允许"照旧
// 生效而范围只剩它自己。顺带修掉的是反向的死行：名字里带 `\` 的真实文件（Linux 上合法）
// 原样落库时 path.Match 对不上自己。
//
// 用户在弹窗里手写的 pattern 不收窄：他写的通配就是他要的授权范围（设计 §4.3），
// 与 ossGrantPatterns 对用户手写策略串的豁免是同一条。
//
// 匹配器一个字节都不用改：`\` 是 path.Match 原生的转义语法。这一点是承重的——
// policy.MatchPathRule 同时是 local_write / local_edit 白名单的匹配器（决策 D17），
// 改它的语义会波及本地写授权，而那条门禁走的是自己的会话白名单，根本不经过本函数。
func cpGrantPatterns(remotePath string, origin GrantOrigin) []string {
	if origin == GrantOriginUser {
		return []string{remotePath}
	}
	return []string{escapeGlobMeta(remotePath)}
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
func shellGrantPatterns(command string, _ GrantOrigin) []string {
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

// GrantPatternsFunc 把一条审批输入拆成可独立匹配的 grant pattern 列表。
type GrantPatternsFunc func(command string, origin GrantOrigin) []string

// GrantOrigin 声明一条审批输入是谁写的。
//
// 一条策略串有两种角色：被拿去撞规则的**名字**，与落成常驻授权的**规则**（决策 D21 更正）。
// 归一化只在规则这一侧收窄——D20 丢弃前缀形状的资源、D21 转义具体 key 里的通配元字符——
// 而收窄的前提是这条串是系统替用户推导出来的。用户在审批弹窗里手写的 pattern 不能收窄：
// 他写的通配就是他要的授权范围（设计 §4.3）。
//
// 两种来源都是策略串形状，字符串本身分不出来，所以来源必须由调用方声明。参数是必填而不是
// 可选的：本分支已经两次被"漏接线也照样编译"咬到（RegisterPolicyStrings、CanonicalizeFor），
// 一个安全的默认值只会把"忘了想这件事"变成一次静默的行为差异，而必填参数把它变成编译错误。
type GrantOrigin int

const (
	// GrantOriginSystem 是系统替用户推导出来的主体：exec 的规范 DSL、传输适配器的
	// ApprovalSubject（helper.TransferAdapter）。
	GrantOriginSystem GrantOrigin = iota
	// GrantOriginUser 是用户在审批弹窗里手写或改写的 pattern。
	GrantOriginUser
)

// NormalizeGrantPatterns 把一条审批输入拆成可独立匹配的 grant pattern 列表。
//
// 拆法由类型注册表上的 grantPatterns 给出（type_registry.go 的 init）：
//   - 未注册归一化函数的类型（sql/redis/mongo/kafka/serial/cp）保留原命令，匹配规则各自处理；
//   - SSH/K8s 走 shellGrantPatterns（origin 不参与：多行/复合命令无论谁写的都要按子命令拆）；
//   - OSS 走 ossGrantPatterns（DSL → 策略串，并按 origin 决定要不要收窄）。
//
// 所有 SaveGrantPattern 调用前都应当先经过这个归一化函数。
func NormalizeGrantPatterns(approvalType, command string, origin GrantOrigin) []string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}
	handler, ok := permissionTypeFor(approvalType)
	if !ok || handler.grantPatterns == nil {
		return []string{cmd}
	}
	return handler.grantPatterns(cmd, origin)
}

// SaveGrantPatternsForApproval 用 NormalizeGrantPatterns 拆出 patterns 后依次落库。
// 适合 app 层在多种审批回调（opsctl 单审批、AI grant 流）里调用，避免每个路径重复拆分逻辑。
func SaveGrantPatternsForApproval(ctx context.Context, sessionID string, assetID int64, assetName, approvalType, command string, origin GrantOrigin) {
	for _, p := range NormalizeGrantPatterns(approvalType, command, origin) {
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
