package extreg

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// maxToolTimeout is the longest timeout a tool may declare. A tool call holds one
// of the extension's few instance slots for as long as it runs; work that needs
// longer is a page-only tool declaring noTimeout, which streams progress and is
// canceled by its page.
const maxToolTimeout = 10 * time.Minute

// ToolTimeout is the extension.ToolCall.Timeout a call of m's tool runs under,
// for every caller alike: none (negative) for a page-only tool declaring
// noTimeout, its declared timeout, or 0 — the runtime's default — when it
// declares nothing. Callers resolve the tool name first (parseCommand,
// ValidateToolArgs), so a name m does not declare is not a case here.
func ToolTimeout(m *Manifest, tool string) time.Duration {
	def, _ := toolDef(m, tool)
	if def.NoTimeout {
		return -1
	}
	return def.Timeout()
}

// injectedAssetParam is the one parameter name a tool may not declare: the asset
// a tool runs against is the `exec` target, put into the call envelope by the
// host (AssetRef) and read off the guest's ToolContext.
//
// It is refused rather than overridden because policy, approval and grant are all
// keyed on the exec target: a tool that could be handed a different asset id in
// its arguments would reach an asset the user never granted, and "sometimes the
// flag wins" is not a rule anyone can audit.
const injectedAssetParam = "asset_id"

// supportedParamTypes is what the exec flag DSL can express. object is not
// supported: a nested value goes through the command's --json escape hatch rather
// than inventing a nested flag syntax.
var supportedParamTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true, "array": true,
}

// toolsPoint 是工具的贡献点：参数 schema、策略动作、超时、大内容参数的规则；接入的是本扩展
// 每个资产类型的执行面——统一 exec 的执行器、命令规范化、策略判定、help 文档与技能清单。
type toolsPoint struct{ guest guestFunc }

func (toolsPoint) Key() string { return extwire.KeyTools }

// Check validates the parameter schemas the flag DSL parses against.
//
// The SDK reflects these from the handler's own argument type, so a compliant guest
// cannot produce a schema this rejects — which is the point: what used to be a
// hand-written manifest block that only the parser read is now generated, and this
// check is the boundary that keeps a non-SDK guest honest.
//
// A tool that declares a policy action is reached through exec on an asset, checked
// under the asset's permission groups — so the extension must contribute an asset
// type for it to be reached through (decision 8).
func (toolsPoint) Check(v extension.ExtensionView) error {
	var tools []extwire.Tool
	if err := decodeContribution(v, extwire.KeyTools, &tools); err != nil {
		return err
	}
	var assetTypes []json.RawMessage
	if err := decodeContribution(v, extwire.KeyAssetTypes, &assetTypes); err != nil {
		return err
	}
	seen := make(map[string]bool, len(tools))
	for i, t := range tools {
		if t.Name == "" {
			return fmt.Errorf("tools[%d]: name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("tools[%s]: duplicate tool name", t.Name)
		}
		seen[t.Name] = true
		if err := checkTool(t, len(assetTypes) > 0); err != nil {
			return fmt.Errorf("tools[%s]: %w", t.Name, err)
		}
	}
	return nil
}

func checkTool(t extwire.Tool, hasAssetType bool) error {
	if err := checkToolActions(t); err != nil {
		return err
	}
	if !t.PageOnly && !hasAssetType {
		return fmt.Errorf("declares a policy action, checked on an asset of one of this extension's asset types, but the extension contributes no asset type")
	}
	if t.NoTimeout {
		if !t.PageOnly {
			return fmt.Errorf("noTimeout is only for a pageOnly tool — a tool reached through exec keeps a timeout")
		}
		if t.TimeoutMs != 0 {
			return fmt.Errorf("declares both noTimeout and timeoutMs")
		}
	}
	if t.TimeoutMs < 0 || t.TimeoutMs > maxToolTimeout.Milliseconds() {
		return fmt.Errorf("timeoutMs must be between 0 (host default) and %d (%s), got %d", maxToolTimeout.Milliseconds(), maxToolTimeout, t.TimeoutMs)
	}
	if t.Parameters == nil {
		return fmt.Errorf("parameters is required (an object schema, empty properties for a no-arg tool)")
	}
	if typ, _ := t.Parameters["type"].(string); typ != "object" {
		return fmt.Errorf("parameters.type must be \"object\", got %q", typ)
	}
	props, ok := t.Parameters["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("parameters.properties must be an object")
	}
	for name, raw := range props {
		if name == injectedAssetParam {
			return fmt.Errorf("declares parameter %q — the asset a tool runs against is the exec target, injected by the host; read it from the tool context instead of accepting it as a flag", name)
		}
		prop, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("parameters.properties.%s must be an object", name)
		}
		typ, _ := prop["type"].(string)
		if typ == "" {
			return fmt.Errorf("parameters.properties.%s has no type", name)
		}
		if !supportedParamTypes[typ] {
			return fmt.Errorf("parameters.properties.%s has unsupported type %q (supported: string, integer, number, boolean, array)", name, typ)
		}
		if typ == "array" {
			items, ok := prop["items"].(map[string]any)
			if !ok {
				return fmt.Errorf("parameters.properties.%s is an array without an items object", name)
			}
			if it, _ := items["type"].(string); it != "string" {
				return fmt.Errorf("parameters.properties.%s: only array<string> is supported, got array<%s>", name, it)
			}
		}
	}
	for _, name := range t.Large {
		prop, _ := props[name].(map[string]any)
		if typ, _ := prop["type"].(string); typ != "string" {
			return fmt.Errorf("large names %q, which is not a declared string parameter", name)
		}
		// opsctl reads `--<name>-file` as the file form of name, so a parameter
		// spelled that way could never be passed.
		if _, clash := props[name+"-file"]; clash {
			return fmt.Errorf("large names %q, but %q is itself a parameter", name, name+"-file")
		}
	}
	if rawReq, exists := t.Parameters["required"]; exists {
		req, ok := rawReq.([]any)
		if !ok {
			return fmt.Errorf("parameters.required must be an array, got %T", rawReq)
		}
		for _, r := range req {
			name, _ := r.(string)
			if _, exists := props[name]; !exists {
				return fmt.Errorf("parameters.required references undeclared property %q", name)
			}
		}
	}
	return nil
}

// checkToolActions checks what the tool declares it is: a policy tool, which requests
// a fixed action or the set a per-call classification picks from (never both), or a
// page-only tool, which requests none. Exactly one of the two, never both, never neither.
//
// An action name is the segment of a rule <action>[:<resource-glob>] that ends at
// the first ':' — a name that itself contains ':' (or whitespace, which a rule is
// trimmed of) could not be told apart from an action plus a resource, so it is
// refused here rather than silently never matching.
func checkToolActions(t extwire.Tool) error {
	if t.PageOnly {
		if len(t.Actions()) > 0 {
			return fmt.Errorf("declares both pageOnly and a policy action — a tool is either checked against the asset's permission groups or only for the extension's pages")
		}
		return nil
	}
	if t.PolicyAction != "" && len(t.PolicyActions) > 0 {
		return fmt.Errorf("declares both policyAction and policyActions — a tool's action is either fixed or classified per call")
	}
	actions := t.Actions()
	if len(actions) == 0 {
		return fmt.Errorf("declares neither a policy action nor pageOnly — a tool is checked against the asset's permission groups or is only for the extension's pages")
	}
	seen := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		if a == "" {
			return fmt.Errorf("declares an empty policy action")
		}
		if strings.ContainsFunc(a, func(r rune) bool { return r == ':' || unicode.IsSpace(r) }) {
			return fmt.Errorf("policy action %q must not contain ':' or whitespace — rules are <action>[:<resource-glob>]", a)
		}
		if _, dup := seen[a]; dup {
			return fmt.Errorf("declares policy action %q twice (duplicate)", a)
		}
		seen[a] = struct{}{}
	}
	return nil
}

// Attach 为本扩展的每个资产类型接上执行面。有运行中的插件时是完整的一套：策略判定、
// 执行器（连同命令规范化与 help 文档）、技能清单；仅描述接入（opsctl 启动时）只登记 help
// 文档——HelpFor 照常返回用法，而 ExecutorFor / RegisteredExecTypes 跳过它：这个进程确实
// 执行不了，不能假装能。opsctl 的 exec 要执行时先激活扩展，换成完整接入。
//
// 技能清单与 help 文档分开登记，和内置类型完全一样（execimpl 也是先 skills.Get 再
// RegisterExecutor）：清单进 prompt，让模型知道这个类型存在、help 能问；正文只在模型真的
// 调了 help 之后才下发。
func (p toolsPoint) Attach(v extension.ExtensionView) (func(), error) {
	l, err := p.guest.loaded(v)
	if err != nil {
		return nil, err
	}
	localized := l.manifest.Localized(func(key string) string { return v.Translate(helpLang, key) })
	help := helpDocument(v.SkillMD(), l.name, localized)
	description := skillDescription(v)
	var done undo
	for _, at := range l.manifest.AssetTypes {
		revoke, err := attachExecFace(l, at.Type, help, description)
		if err != nil {
			done.run()
			return nil, fmt.Errorf("tools: asset type %q: %w", at.Type, err)
		}
		done = append(done, revoke)
	}
	mu.Lock()
	manifests[l.name] = l.manifest
	mu.Unlock()
	done = append(done, func() {
		mu.Lock()
		delete(manifests, l.name)
		mu.Unlock()
	})
	return done.run, nil
}

func attachExecFace(l loaded, assetType, help, description string) (func(), error) {
	if l.plugin == nil {
		if err := permission.RegisterDynamicHelpDoc(assetType, help); err != nil {
			return nil, err
		}
		return func() { permission.UnregisterExecutor(assetType) }, nil
	}
	if err := permission.RegisterPolicyCheck(assetType, policyCheck(l, assetType), classifyForApproval(l)); err != nil {
		return nil, err
	}
	done := undo{func() { permission.UnregisterPolicyCheck(assetType) }}
	if err := permission.RegisterDynamicExecutor(assetType, execTool(l), help, canonicalize(l)); err != nil {
		done.run()
		return nil, err
	}
	done = append(done, func() { permission.UnregisterExecutor(assetType) })
	if err := skills.RegisterDynamic(assetType, help, description); err != nil {
		done.run()
		return nil, err
	}
	done = append(done, func() { skills.UnregisterDynamic(assetType) })
	return done.run, nil
}
