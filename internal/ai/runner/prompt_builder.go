package runner

import (
	"fmt"
	"sort"
	"strings"
)

// TabInfo 当前打开的 Tab 信息
type TabInfo struct {
	// Type 是该 Tab 所指资产的**真实资产类型**（asset_entity.AssetType* 的取值，
	// 如 ssh / serial / database / redis / k8s / mongodb / oss ...），不是 Tab 的
	// 界面种类：前端传的是 ref.assetType（frontend/src/stores/aiStore.ts 收集
	// openTabs 处），会话绑定资产时传 linkedAssetType。所以这里不是一个封闭的短枚举，
	// 新增资产类型会直接出现在这里，不需要改本文件。
	Type      string `json:"type"`
	AssetID   int64  `json:"assetId"`
	AssetName string `json:"assetName"`
}

// AIContext 前端传入的上下文信息
type AIContext struct {
	OpenTabs []TabInfo `json:"openTabs"`
}

// PromptBuilder 动态构建 System Prompt
type PromptBuilder struct {
	language          string
	context           AIContext
	assetTypeSkills   map[string]string // 内置资产类型 → 一行描述（skills.Description）
	extensionSkillMDs map[string]string // extName → SKILL.md content
}

// SetExtensionSkillMDs sets all extension SKILL.md contents to inject.
// Keys are extension names, values are the raw markdown.
func (b *PromptBuilder) SetExtensionSkillMDs(mds map[string]string) {
	b.extensionSkillMDs = mds
}

// SetAssetTypeSkills sets the compact per-built-in-type skill listing to render in the
// prompt. Keys are asset types (ssh/serial/database/redis/k8s), values are their
// one-line skills.Description() — never the full SKILL.md body. The full body is loaded
// on demand via the help(asset) tool; inlining it here would defeat the whole point of
// having a separate help tool (token savings from collapsing 14 tools into exec/help).
//
// This listing is purely a DISCOVERY aid: it tells the model that help exists and which
// types it covers. It does NOT satisfy the exec doc gate — only an actual help(asset)
// call does. A one-line description is not command syntax, so treating its presence as
// "the model has seen the docs" would let exec run against a type whose syntax never
// reached the model. Callers must therefore pass every built-in type unconditionally
// (it's one line each), not just the types with an open tab.
func (b *PromptBuilder) SetAssetTypeSkills(descriptions map[string]string) {
	b.assetTypeSkills = descriptions
}

// NewPromptBuilder 创建 PromptBuilder
func NewPromptBuilder(language string, context AIContext) *PromptBuilder {
	return &PromptBuilder{
		language: language,
		context:  context,
	}
}

// Build 构建运行时上下文 prompt，注入到 cago 模板的 {{.AppendSystem}} 位。
// 角色身份已经搬到 internal/ai/system_template.go 的模板 intro，此处只输出
// 每次 Send 都可能变化的动态段（语言 / Tab / 知识 / 错误恢复 / extension SKILL.md）。
func (b *PromptBuilder) Build() string {
	var parts []string

	// 1. 用户语言
	parts = append(parts, b.buildLanguageHint())

	// 2. 当前 Tab 上下文
	if tabContext := b.buildTabContext(); tabContext != "" {
		parts = append(parts, tabContext)
	}

	// 3. 内联 mention 引导
	parts = append(parts, b.buildMentionGuidance())

	// 4. 资产知识引导
	parts = append(parts, b.buildKnowledgeGuidance())

	// 5. 多资产 / 批量操作引导
	parts = append(parts, b.buildMultiAssetGuidance())

	// 6. 凭据与敏感信息引导
	parts = append(parts, b.buildSecretsGuidance())

	// 7. 错误恢复引导
	parts = append(parts, b.buildErrorRecoveryGuidance())

	// 8. 用户拒绝操作引导
	parts = append(parts, b.buildUserDenialGuidance())

	// 9. exec/help 用法说明 + 内置资产类型技能清单（只列一行描述，正文由 help 按需加载）。
	// 无条件渲染：exec/help 是资产操作的主路径，不能只在"恰好开着某个 Tab"时才告诉模型。
	parts = append(parts, b.buildAssetTypeSkills())

	// 10. Extension tools guide
	if len(b.extensionSkillMDs) > 0 {
		names := make([]string, 0, len(b.extensionSkillMDs))
		for name := range b.extensionSkillMDs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("## From extension: %s\n%s", name, b.extensionSkillMDs[name]))
		}
	}

	return strings.Join(parts, "\n\n")
}

func (b *PromptBuilder) buildLanguageHint() string {
	switch b.language {
	case "zh-cn":
		return "The user's preferred language is Chinese (Simplified). Always respond in Chinese."
	case "en":
		return "The user's preferred language is English. Always respond in English."
	default:
		return "Respond in the same language the user uses."
	}
}

func (b *PromptBuilder) buildTabContext() string {
	if len(b.context.OpenTabs) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "The user currently has these tabs open (this helps you understand what they're working on):")
	for _, tab := range b.context.OpenTabs {
		typeName := tab.Type
		switch tab.Type {
		case "ssh":
			typeName = "SSH Terminal"
		case "database":
			typeName = "Database Query"
		case "redis":
			typeName = "Redis"
		case "sftp":
			typeName = "SFTP"
		}
		lines = append(lines, fmt.Sprintf("- %s: \"%s\" (ID: %d)", typeName, tab.AssetName, tab.AssetID))
	}
	return strings.Join(lines, "\n")
}

func (b *PromptBuilder) buildMentionGuidance() string {
	return `User messages may contain inline XML mention tags such as <mention asset-id="42" type="database" target="table" database="app" table="users" driver="mysql">@app.users</mention>. Treat these tags as authoritative user-selected context, not as prose to quote back verbatim.

Use asset-id for tool calls. When target="database", scope SQL work to the database attribute. When target="table", scope SQL work to both database and table attributes, and qualify table names as needed for the driver. If you execute SQL for a mentioned database or table, keep the exact SQL visible to the user in your response or tool-call explanation so they can verify what ran.`
}

func (b *PromptBuilder) buildKnowledgeGuidance() string {
	return `Discover before acting: call list_assets / get_asset first, then operate. The asset Description often contains prior findings (OS, services, DB version) — read it to avoid redundant exploration. When you learn new non-secret facts about an asset during work, append them to the asset Description via update_asset.

Running commands on an asset: use exec(asset, command), preceded by help(asset) the first time you touch a given asset type. exec dispatches on the asset's real type, so it is the only path for running commands on SSH, serial, database, Redis, K8s, etcd, MongoDB, and Kafka assets. Some capabilities are not commands and still have their own tool: upload_file / download_file for SFTP transfer, and batch_command for the same operation across several assets.

Local vs remote — VERY IMPORTANT: every tool whose name starts with ` + "`local_`" + ` (local_bash / local_write / local_edit / local_read / local_grep / local_find / local_ls) operates ONLY on the USER'S OWN MACHINE — they do NOT touch any remote asset. When the scenario targets a specific server / database / Redis / Kafka / K8s asset (an SSH / Database / Redis / SFTP tab is open for it, the user names the asset, or the request is clearly about that asset), you MUST reach it through a remote tool: exec(asset, command) for SSH / serial / database / Redis / K8s / etcd / MongoDB / Kafka assets (use ` + "`cat`/`ls`/`grep`" + ` inside an SSH exec for file inspection), and upload_file / download_file for SFTP transfer. Never fall back to a local_* tool even when the command looks identical — running ` + "`local_ls /etc/nginx`" + ` lists YOUR machine's filesystem, not the server the user asked about. local_* tools are only correct when the user explicitly asks about their local machine, or when there is no remote asset in scope.

Within the local_* family: prefer local_grep / local_find / local_ls / local_read over local_bash for file exploration on the user's machine (they are faster, .gitignore-aware, and don't require shell escaping). Use local_bash only when you need shell features (pipes, env vars, scripts).`
}

func (b *PromptBuilder) buildMultiAssetGuidance() string {
	return `When the same operation targets 2 or more assets, prefer batch_command over a loop of exec calls — it parallelizes execution and batches approval prompts. When you expect to issue several command patterns that will trigger approval, call request_permission upfront so the user grants them in a single review instead of one popup per call.`
}

func (b *PromptBuilder) buildSecretsGuidance() string {
	return `Never echo passwords, private keys, kubeconfig contents, or other credentials back to the user. The app stores them encrypted; treat anything that came from a password / private_key / kubeconfig field as write-only. If a tool result includes a secret, mask it before referencing it.`
}

func (b *PromptBuilder) buildErrorRecoveryGuidance() string {
	return `When a tool execution fails, analyze the error and try a different approach. If repeated attempts fail, explain the issue to the user and suggest alternatives. Do not give up after a single failure.`
}

// buildAssetTypeSkills 渲染 exec/help 的用法说明，以及内置资产类型的一行技能清单
// （只列描述，不内联 SKILL.md 正文）。
//
// 说明段无条件渲染，清单段只在调用方给了描述时追加：exec/help 是资产操作的**唯一**
// 路径（按类型区分的旧工具已全部删除），若只在"有匹配的 Tab 打开"时才出现，模型在
// 没开 Tab 的会话里就看不到这条路径，只能瞎猜工具名。
func (b *PromptBuilder) buildAssetTypeSkills() string {
	lines := []string{
		"Operating on an asset — exec / help: exec(asset, command) runs a command against an asset and dispatches on the asset's REAL type, read from the asset record. You do not need to know the type in advance and you cannot mis-route: passing a Redis asset to exec runs it as Redis. Pass the asset's id or name as `asset`; use `scope` for the connection-level target that is not part of the command itself (database name for database assets, db index for Redis).",
		"",
		"Command syntax differs per asset type and is documented per type. The FIRST time you use exec against a given asset type in a conversation, call help(asset) to load that type's syntax — exec will tell you to if you skip it, and that round trip is wasted. Once you have called help for a type, it stays loaded for the rest of the conversation.",
	}
	if len(b.assetTypeSkills) > 0 {
		types := make([]string, 0, len(b.assetTypeSkills))
		for t := range b.assetTypeSkills {
			types = append(types, t)
		}
		sort.Strings(types)

		lines = append(lines, "", "Asset types exec currently covers:", "")
		for _, t := range types {
			lines = append(lines, fmt.Sprintf("- %s: %s", t, b.assetTypeSkills[t]))
		}
	}
	return strings.Join(lines, "\n")
}

func (b *PromptBuilder) buildUserDenialGuidance() string {
	return `IMPORTANT: When the user denies a command execution or permission request, you MUST immediately stop the current task. Do not attempt alternative commands, workarounds, or different approaches to achieve the same goal. Simply acknowledge the user's decision and ask if they need anything else. The user's denial is final and must be respected.`
}
