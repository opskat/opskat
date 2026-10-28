// Package extreg 是宿主自有子系统的扩展贡献点：资产类型（internal/assettype）、工具的
// 执行面（internal/ai/permission、skills）、策略面（默认策略、权限组、永久规则落点）、
// 数据库驱动（internal/dbdriver）与页面；片段的贡献点在 snippet_svc。Points 把它们交给
// 运行时的 extension.ContributionRegistry——运行时不认识任何一种贡献的规则，只把
// describe() 的每个键交给对应的贡献点去校验（Check）与接入（Attach）。
//
// 它存在的理由是方向：这些注册表都在 internal/ 下，而扩展的运行期对象在 pkg/extension，
// 由 internal/service/extension_svc 的生命周期驱动。把规则与接线集中在这里，消费点
// （exec / help / 审批 / 前端表单）不再需要"内置一条路、扩展一条路"。
//
// 接入是全有或全无：任何一个贡献点接入失败，注册表撤掉这个扩展已接入的全部贡献，
// 调用方据此拒绝加载该扩展。半注册的扩展比不注册更糟——资产类型在表单里出现，exec 却
// 找不到执行器。
package extreg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	aipolicy "github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/snippet_svc"
	"github.com/opskat/opskat/pkg/extension"
)

// helpLang 是渲染扩展用法文档时解析 i18n key 用的语言。文档在加载期渲染一次并注册进
// permission，而模型的对话语言是会话期才知道的；内置类型的 SKILL.md 同样只有英文。
const helpLang = "en"

// pluginCaller 是本包用到的 WASM 插件能力子集（*extension.Plugin 满足它）。
// 收窄到这几个方法而不是直接吃 *Plugin，是为了让策略/执行/配置校验这几条闭包可以在
// 没有 wazero 运行时的情况下被测试驱动——它们承载的是行为契约，不是 WASM 加载。
type pluginCaller interface {
	CallTool(ctx context.Context, toolName string, args json.RawMessage, asset *extension.AssetRef) (json.RawMessage, error)
	CheckPolicy(ctx context.Context, toolName string, args json.RawMessage) (action string, resources []string, err error)
	ValidateConfig(ctx context.Context, config json.RawMessage) ([]extension.ValidationError, error)
	TestConnectionCaller
	driverOpener
}

// TestConnectionCaller is the one plugin capability a conntest tester needs:
// run an asset type's describe()-declared test-connection handler. It is its
// own (exported) interface, not folded silently into pluginCaller, because
// ConnTestRegistrar — implemented outside this package — needs a type it can
// name in its own signature; Go only requires the method sets to match.
type TestConnectionCaller interface {
	TestConnection(ctx context.Context, assetType string, adhoc *extension.AdHocAssetConfig) error
}

// ConnTestRegistrar builds the conntest.TestFunc a describe()-declared
// test-connection handler is registered under. The one implementation
// (internal/service/extension_svc, wired by each process that runs extensions via
// SetConnTestRegistrar) needs the extension service to merge the form's unchanged
// password fields with an edited asset's stored ones — and that service is built
// on the manager the contribution registry (Points) is handed to, so the
// capability is injected once the service exists instead of passed to Points.
type ConnTestRegistrar interface {
	Build(extName string, manifest *extension.Manifest, assetType string, plugin TestConnectionCaller) conntest.TestFunc
}

var connTestRegistrar ConnTestRegistrar

// SetConnTestRegistrar wires the registrar main.go constructs at startup,
// before any extension loads. Called more than once, the last call wins —
// each process (the desktop app, or opsctl when it activates an extension)
// wires this once, same as every other setter-injected seam.
func SetConnTestRegistrar(r ConnTestRegistrar) { connTestRegistrar = r }

// Points 是宿主的全部贡献点。运行扩展的每个进程（桌面端、opsctl）都用它建自己的
// extension.ContributionRegistry，因此两边认的贡献、校验的规则、接入的子系统都是同一组。
// 顺序即接入顺序：驱动撞名最常见，先接；资产类型先于以它为键的执行面与策略面。
func Points() []extension.ContributionPoint {
	return newPoints(pluginOf)
}

// newPoints 按 guest 取扩展的运行中插件建贡献点；测试经它换上假插件。
func newPoints(guest guestFunc) []extension.ContributionPoint {
	return []extension.ContributionPoint{
		dbDriversPoint{guest: guest},
		assetTypesPoint{guest: guest},
		toolsPoint{guest: guest},
		policyPoint{},
		pagesPoint{},
		snippet_svc.ContributionPoint(),
	}
}

// guestFunc 返回扩展运行中的插件；视图读自 describe 缓存（opsctl 启动、扩展列表）时为 nil，
// 贡献点据此只接入不需要运行 WASM 的那一半。
type guestFunc func(v extension.ExtensionView) pluginCaller

func pluginOf(v extension.ExtensionView) pluginCaller {
	if p := v.Plugin(); p != nil {
		return p
	}
	return nil
}

// loaded 是一个扩展在本包内的最小画像。plugin 为 nil 表示没有运行中的插件（仅描述接入）。
type loaded struct {
	name     string
	manifest *extension.Manifest
	plugin   pluginCaller
	// translate 解析扩展多语言里的键（数据库驱动专属字段的标签与说明）。
	translate translateFunc
}

func (g guestFunc) loaded(v extension.ExtensionView) loaded {
	return loaded{name: v.Name(), manifest: v.Manifest(), plugin: g(v), translate: v.Translate}
}

var (
	mu sync.Mutex
	// manifests 是已接入工具的扩展的清单，供 ExpandFileFlags 查工具声明。
	manifests = make(map[string]*extension.Manifest) // extension name → its manifest
	// policyTypeOwner 记录每个策略面（describe 的 policy.type）归哪个扩展。策略面是
	// 权限组 ID（ext:<policyType>:<name>）的命名空间段、组上扩展永久规则的落点键，也是
	// CheckExtensionPolicy 筛权限组的键：两个扩展共用一个策略面，一方的组与规则就会被
	// 当成另一方的来判。
	policyTypeOwner = make(map[string]string) // policy type → extension name
)

// decodeContribution 把扩展在 key 下的原始贡献解进 dst；扩展没有贡献这个键时 dst 不变。
func decodeContribution(v extension.ExtensionView, key string, dst any) error {
	raw := v.Contribution(key)
	if raw == nil {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

// undo 是一串按登记顺序记下的撤销动作，逆序执行。
type undo []func()

func (u undo) run() {
	for i := len(u) - 1; i >= 0; i-- {
		u[i]()
	}
}

// skillDescription 是技能清单里的那一行。优先用 SKILL.md frontmatter 的 description，
// 其次是 manifest 的 i18n 描述——两者都缺时至少说清这个类型归谁，别在清单里留一行空白。
func skillDescription(v extension.ExtensionView) string {
	if d := strings.TrimSpace(v.SkillDescription()); d != "" {
		return d
	}
	if d := strings.TrimSpace(v.Translate(helpLang, v.Manifest().I18n.Description)); d != "" {
		return d
	}
	return fmt.Sprintf("Asset type provided by the %q extension.", v.Name())
}

// helpDocument 是 help(asset) 对扩展类型返回的内容：SKILL.md 正文 + 由 manifest
// describe() 报上来的 tools[].parameters 渲染出的工具/参数表。
//
// 这两半各自补对方的洞：SKILL.md 是散文，说得清"这个扩展是干什么的"，但单靠它
// flag 名与类型只能靠猜；参数表是权威的（同一份声明被 parseCommand 强制
// 执行），但读不出语义。缺 SKILL.md 也照常给出参数表——没有散文不等于没有语法。
func helpDocument(skillMD, extName string, localized *extension.Manifest) string {
	var parts []string
	if body := strings.TrimSpace(skillMD); body != "" {
		parts = append(parts, body)
	}
	if ref := strings.TrimSpace(toolReference(localized)); ref != "" {
		parts = append(parts, ref)
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("Asset type provided by extension %q. It declares no tools.", extName))
	}
	if ref := configReference(localized); ref != "" {
		parts = append(parts, ref)
	}
	return strings.Join(parts, "\n\n")
}

// configReference 渲染每个资产类型在 create asset / update asset / put_asset 里接受的
// config 字段：扩展自己的 configSchema 属性，加上该类型声明的连接项换来的宿主连接字段。
//
// SKILL.md 讲的是怎么**用**一个资产，建资产要填什么它通常不写；而字段契约
// （assettype 的 AutomationContract）只在填错之后才以 "unknown config field" 的形式露面。
// 两份都从契约的同一来源渲染——configSchema 与 assettype.ConnectionFieldDocs——所以不会
// 列出一个实际不接受的字段。
func configReference(localized *extension.Manifest) string {
	var b strings.Builder
	for _, at := range localized.AssetTypes {
		props, _ := at.ConfigSchema["properties"].(map[string]any)
		if len(props) == 0 {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("## asset config\n\n")
			b.WriteString("Fields accepted in `--config` / `--config-file` of `opsctl create asset` and `opsctl update asset`, and in put_asset's `config`.\n")
		}
		fmt.Fprintf(&b, "\n### %s\n\n", at.Type)
		b.WriteString("| field | type | required | notes |\n| --- | --- | --- | --- |\n")
		required := extension.ConfigSchemaRequired(at.ConfigSchema)
		secret := extension.PasswordFieldsFromSchema(at.ConfigSchema)
		for _, name := range extension.ConfigSchemaProperties(at.ConfigSchema) {
			prop, _ := props[name].(map[string]any)
			typ, _ := prop["type"].(string)
			req := "no"
			if slices.Contains(required, name) {
				req = "yes"
			}
			notes, _ := prop["description"].(string)
			if notes == "" {
				notes, _ = prop["title"].(string)
			}
			if slices.Contains(secret, name) {
				notes = strings.TrimSpace("**Write-only.** Encrypted in the asset. " + notes)
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", name, typ, req, tableCell(notes))
		}
		for _, doc := range assettype.ConnectionFieldDocs(connectionSpec(at)) {
			fmt.Fprintf(&b, "| `%s` | %s | no | %s |\n", doc.Name, doc.Type, tableCell(doc.Notes))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// tableCell keeps a note containing "|" or a newline from breaking its markdown row.
func tableCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", "\\|")
}

// canonicalize 是注册给 permission 的 CanonicalizeFunc：它在**权限检查之前**跑，因此
// 一条 flag 写错、工具名不存在的命令会在弹出审批框之前失败，而不是让用户先点头、
// 批准之后才发现命令根本调不动。返回的规范串同时是策略匹配、审批展示与 grant 的主体。
func canonicalize(l loaded) permission.CanonicalizeFunc {
	return func(_ *asset_entity.Asset, command string) (string, error) {
		return canonicalCommand(l.manifest, command)
	}
}

// execTool 是注册给 permission 的执行器。它只在权限检查通过之后被调用（统一 exec 的
// 顺序契约），因此这里不再做任何策略判断——那是 policyCheck 的事。
func execTool(l loaded) permission.ExecFunc {
	return func(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
		toolName, argsJSON, err := parseCommand(l.manifest, command)
		if err != nil {
			return "", err
		}
		log := logger.Ctx(ctx).With(zap.String("extension", l.name),
			zap.String("tool", toolName), zap.Int64("assetID", asset.ID))
		log.Info("extension tool execution start")
		// The exec target travels in the call envelope, not in the arguments: it is
		// the asset the policy check above just ran against, so a tool cannot be
		// pointed at a different one by what the model wrote on the command line.
		result, err := l.plugin.CallTool(ctx, toolName, argsJSON,
			&extension.AssetRef{ID: asset.ID, Name: asset.Name, Type: asset.Type})
		if err != nil {
			// 不记 raw error：它可能包装用户输入或远端输出。Error 级别本身即失败状态。
			log.Error("extension tool execution failed")
			return "", fmt.Errorf("%s.%s failed: %w", l.name, toolName, err)
		}
		log.Info("extension tool execution end")
		return string(result), nil
	}
}

// policyCheck 是扩展类型的策略判定，形状与内置类型的 check* 函数一致：
// 类型策略（deny → allow）→ grant → NeedConfirm。
//
// 与内置类型的差别只在中间那一步的语言：内置类型把命令文本拿去撞规则模式，扩展则先问
// guest 的 check_policy 这条调用按参数分类成哪个 (action, resources)，再拿它去撞 holder
// 自身那一列与它引用的权限组里的 `<action>[:<resource-glob>]` 规则
// （policy.CheckExtensionPolicy）。两套引擎不合并，是因为它们判定的根本不是同一种东西。
//
// 判定逐个资源进行：任一资源命中 deny 即拒；allow 规则没覆盖到的资源再逐个查 grant，
// 每个资源都被某条规则或某个 grant 覆盖才放行（不同资源可由不同规则 / grant 覆盖）。
//
// guest 给出的 action 必须属于被调用工具在 describe() 里声明的动作（ToolDef.Actions，
// 不是整个扩展的 policies.actions 并集）。集合外的 action——包括空串、带 ':' 想冒充 "动作:资源" 的串——
// 是 guest 的缺陷：记一条错误，直接 NeedConfirm，既不撞规则也不查 grant，让用户看见
// 这次调用本身。
//
// 工具拒绝这次调用的参数（SDK 的 RejectArgs，guest 答 {"reject"}）则不是缺陷而是结论：
// 这条调用在任何规则 / grant 下都不会执行，问用户只会让他批准一条随后必然失败的调用。
// 于是它先于 deny → allow → grant 直接 Deny，Message 带上工具给出的原因——调用方与
// 审计行（decision=deny，原因落 error 列）看到的都是它。
//
// 返回 NeedConfirm 之后发生什么，则与内置类型完全一致：CheckForAsset 弹审批框，
// "全部允许"落 grant，下一条同样的调用由这里的 MatchExtensionGrant 直接放行。
func policyCheck(l loaded, assetType string) permission.PolicyCheckFunc {
	return func(ctx context.Context, assetID int64, command string) aictx.CheckResult {
		call, err := classifyCommand(ctx, l, command)
		if rejected, ok := errors.AsType[*extension.ArgsRejectedError](err); ok {
			return argsRejected(ctx, call.tool, rejected)
		}
		if err != nil {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		}
		action, resources := call.action, call.resources
		policyType := l.manifest.Policies.Type
		groups, own := permission.ExtensionPolicyForAsset(ctx, assetID, policyType)
		if len(groups) == 0 {
			groups = l.manifest.Policies.Default
		}
		result := aipolicy.CheckExtensionPolicy(ctx, aipolicy.ExtensionCheck{
			PolicyType: policyType,
			GroupIDs:   groups,
			Own:        own,
			Action:     action,
			Resources:  resources,
		})
		if result.Decision != aictx.NeedConfirm {
			return result.CheckResult
		}
		// Matched by classification (action, resource), not by re-parsing command:
		// two calls that spell the same request differently — different flag order,
		// an equivalent literal — must hit the same grant (spec 参数级策略 › 审批展示).
		if grants, ok := permission.MatchExtensionGrant(ctx, assetID, assetType, policyType, action, result.Uncovered); ok {
			return result.AllowedByGrants(grants)
		}
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
}

// classifiedCall is a command classifyCommand parsed and the guest classified.
type classifiedCall struct {
	action    string
	resources []string
	tool      string
	args      json.RawMessage
}

// errClassifyFailed is classifyCommand failing on a defect — a command the parser
// refuses, a guest that errs or answers an undeclared action — already logged.
var errClassifyFailed = errors.New("extension policy classification failed")

// classifyCommand parses a command and runs the guest's check_policy classification,
// validating the action against the actions the called tool declares
// (ToolDef.Actions) — not the extension-wide union, so one tool cannot answer with
// an action only another tool may request.
// It is the single place policyCheck and classifyForApproval both call, so "undeclared
// action never classifies" can't drift between the check path and the approval/grant
// path — both must see the same failure the same way.
//
// The error is either the tool refusing the arguments — an
// *extension.ArgsRejectedError, with call.tool set — or errClassifyFailed.
func classifyCommand(ctx context.Context, l loaded, command string) (classifiedCall, error) {
	toolName, argsJSON, err := parseCommand(l.manifest, command)
	if err != nil {
		// 到不了这里：canonicalize 已经用同一个解析器跑过一遍。真发生了就是
		// fail-closed 的"分类失败"，而不是放行或落 grant。
		return classifiedCall{}, errClassifyFailed
	}
	call := classifiedCall{tool: toolName, args: argsJSON}
	call.action, call.resources, err = l.plugin.CheckPolicy(ctx, toolName, argsJSON)
	if rejected, ok := errors.AsType[*extension.ArgsRejectedError](err); ok {
		// 不记原因：它常常原样带着用户 / 模型写的参数。
		logger.Ctx(ctx).Info("extension tool rejected its arguments",
			zap.String("extension", l.name), zap.String("tool", toolName))
		return classifiedCall{tool: toolName}, rejected
	}
	if err != nil {
		logger.Ctx(ctx).Warn("extension policy check failed",
			zap.String("extension", l.name), zap.String("tool", toolName), zap.Error(err))
		return classifiedCall{}, errClassifyFailed
	}
	def, _ := toolDef(l.manifest, toolName)
	if !slices.Contains(def.Actions(), call.action) {
		logger.Ctx(ctx).Error("extension policy returned an undeclared action",
			zap.String("extension", l.name), zap.String("tool", toolName), zap.String("action", call.action))
		return classifiedCall{}, errClassifyFailed
	}
	return call, nil
}

// argsRejected is the deny for a call whose tool refused its arguments. No rule
// decided it, so MatchedPattern stays empty; the tool's reason is the message.
func argsRejected(ctx context.Context, tool string, rejected *extension.ArgsRejectedError) aictx.CheckResult {
	return aictx.CheckResult{
		Decision:       aictx.Deny,
		DecisionSource: aictx.SourcePolicyDeny,
		Message: aipolicy.PolicyFmt(ctx, "extension tool %q rejected its arguments: %s", "扩展工具 %q 拒绝了这次调用的参数：%s",
			tool, rejected.Reason),
	}
}

// classifyForApproval adapts classifyCommand to permission.ClassifyFunc: it is the
// grant-pattern producer HandleConfirm calls to show an approval item's Action/
// Resource(s)/Detail and to build the "always allow" grant (spec 参数级策略 ›
// 审批展示 — persisted as ext:<type>:<action>:<resource> for one resource, the
// common-prefix rule for several).
func classifyForApproval(l loaded) permission.ClassifyFunc {
	return func(ctx context.Context, command string) (permission.ExtensionClassification, bool) {
		call, err := classifyCommand(ctx, l, command)
		if err != nil {
			return permission.ExtensionClassification{}, false
		}
		return permission.ExtensionClassification{
			PolicyType: l.manifest.Policies.Type,
			Action:     call.action,
			Resources:  call.resources,
			Tool:       call.tool,
			Args:       call.args,
		}, true
	}
}
