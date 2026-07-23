# AI 工具面收敛 · Plan C（put_*/delete_*、batch_exec、ext_exec + manifest 加固、opsctl 统一）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 AI 工具面从「Plan B 留下的 15 个（含 `add_*`/`update_*`/`batch_command`/`exec_tool` 旧形态）」收敛为 spec §4 的终态 15 个，并让 opsctl 与之对齐——一份类型文档同时服务 `exec` / `put_asset` / `help`。

**Architecture:** 沿用 Plan A 建立的两条缝，不新造机制：
`internal/ai/permission` 的**按类型注册表**（执行器 / 规范化 / precheck / 用法文档 / 协议别名）与
`internal/ai/skills` 的 **`<type>/SKILL.md` 内嵌文档**。Plan C 只做三件事：
(1) 把 `add`/`update` 合并成 `put`、补 `delete`，并把 `add_asset` 那张覆盖 10 种类型的巨型 JSON Schema
换成「自由 `config` 对象 + 类型文档 + `assettype.ValidateCreateArgs`」；
(2) 给 `exec` / `batch_exec` / `opsctl exec` / `opsctl batch` 加**可选类型断言**，共用同一个校验函数；
(3) `exec_tool` → `ext_exec(asset, command)`，并在同一次改动里加固 manifest 校验——
因为 `tools[].parameters` 至今**从未被校验、也从未被任何代码读过**，这次要把它提升为承重契约。

**Tech Stack:** Go 1.26（`cago-frame/agents` 工具定义、`mvdan.cc/sh/v3` 切词经 `internal/ai/cmdline`）、
React 19 + Zustand（审批 UI）、SQLite（`audit_logs`）、Wails v2 IPC、`goconvey` + 标准 `testing`、vitest。

---

## Global Constraints

以下约束对**每个 task** 生效，任务内不再重复。

- **分支**：`feature/ai-tool-exec-crud`（已创建，起点 `26d38235`，第一个提交 `f3844913` 是 spec 修正）。
  做完合回 `feature/ai-tool-exec-foundation`（PR #247），不单独开 PR。
- **禁止 `git add -A`**。本仓有多个 agent/实例并发工作，历史上因此产出过内容错乱的 commit
  （`915cb069`，事后 `git reset --soft` 重做）。**每个 commit 逐文件 `git add`**，只加本 task 声明的文件。
- **提交信息用 gitmoji**（`✨` 新功能 / `♻️` 重构 / `🐛` 修复 / `✅` 测试 / `📝` 文档 / `🔥` 删除）。
  **subject 不带 issue 编号**，除非刻意关联 issue（本计划全程不需要）。
- **TDD 强制**：每个 task 先写红测试、跑一次确认它**因为正确的理由**失败，再写实现。
  「显而易见的一行改动」不例外。
- **变异验证**：每个 task 的收尾步骤要求把实现改坏一处（步骤里已写明改哪一处），确认测试变红后还原。
  测试不承重就是没有测试——Plan A/B 里有多个测试在变异下存活的先例（`kafka_dsl` 死代码、
  `TestUnregisterExtractorForTest_Restores` 空转）。
- **验证命令**（凡涉及 Go）：`go build ./...` → `go test ./internal/... ./cmd/... ./pkg/...` → `golangci-lint run`。
  涉及前端：`cd frontend && pnpm test` → `pnpm lint`。**不要跑 `pnpm build`**（CI 不做这道门禁）。
- **不要手改 `frontend/wailsjs/`**：它是 `wails generate` 的产物、已 gitignore。
  后端结构体改动后如需前端类型，跑 `wails generate module`。
- **工具面终态（15 个）**，任何 task 结束时 `Tools()` 都必须仍是一个自洽集合：
  `list_assets`、`get_asset`、`put_asset`、`delete_asset`、`list_groups`、`get_group`、`put_group`、
  `delete_group`、`exec`、`batch_exec`、`help`、`ext_exec`、`upload_file`、`download_file`、`request_permission`。
- **`tools_test.go` 的穷尽性数量断言**（`internal/ai/tool/tools_test.go:58-60`，比的是去重后的 `len(names)`）
  是唯一能发现「注册了却没人知道」的检查。每次增删改名工具都必须同步 `expected` 清单，不得放宽该断言。
- **审批不变式**（spec §5，Plan A 收尾评审 IMPORTANT-1 的教训）：任何**无副作用**的校验
  （资产解析、类型断言、执行器查找、门禁、规范化、precheck）都必须排在
  `CheckForAsset` / `ConfirmFunc` **之前**——后者会弹审批对话框并阻塞等待用户。
  让用户批准一个注定失败的命令是缺陷，不是小瑕疵。
- **权限检查 fail-closed**：包外只有 `permission.RequireChecker`（缺失即报错）与
  `RequireCheckerOrPreapproved`（只在 ctx 带 `WithPreapproved` 时返回 nil checker）。
  新代码不得引入 `if checker != nil { ... }` 形态的放行分支。

---

## File Structure

**新建：**

| 文件 | 职责 |
|---|---|
| `internal/ai/permission/type_assert.go` | 类型断言：别名规范化 + `AssertAssetType`，AI/CLI 三处共用 |
| `internal/ai/tool/tool_handlers_crud.go` | `put_asset` / `put_group` / `delete_asset` / `delete_group` 四个 handler |
| `internal/ai/tool/tools_crud.go` | 上述四个工具的定义（从 `tools_asset.go` 拆出 CRUD 部分） |
| `internal/ai/skills/{rdp,vnc,oss,local}/SKILL.md` | 四个只有配置文档、没有命令面的类型 |
| `pkg/skillmd/skillmd.go` | 共享的 SKILL.md frontmatter 解析（`internal/ai/skills` 与 `pkg/extension` 共用） |
| `pkg/extension/toolargs.go` | manifest `tools[].parameters` → flag DSL 的类型化转换 |
| `cmd/opsctl/command/delete.go` | `opsctl delete asset\|group` |
| `cmd/opsctl/command/help.go` | `opsctl help <asset>` |

**删除：**

| 文件 | 理由 |
|---|---|
| `cmd/opsctl/command/db.go`（全 284 行） | `cmdSQL` / `cmdRedisCmd` / `cmdMongo` 与三个 usage 函数，全部由 `opsctl exec` 覆盖 |

**主要修改：**

| 文件 | 改动 |
|---|---|
| `internal/ai/tool/tools_asset.go` | 删 `add_asset` 巨型 schema 与 `add/update_group`，只留四个只读工具 |
| `internal/ai/tool/tool_handlers_asset.go` | `handleAdd*`/`handleUpdate*` 合并进 `tool_handlers_crud.go` |
| `internal/ai/tool/tool_handlers_unified.go` | `handleExec` 插入类型断言 |
| `internal/ai/tool/tool_handler_batch.go` | 条目 `type` 变断言；派发改走 `permission.ExecutorFor` |
| `internal/ai/tool/tool_handler_ext.go` | `exec_tool` → `ext_exec`，`(asset, command)` |
| `internal/ai/tool/tool_registry.go` | `AllToolDefs()` 同步改名 |
| `internal/ai/audit/extractor_default.go` | `exec_tool` 提取器改名并改读 `command` |
| `internal/ai/execimpl/register.go` | 四个 doc-only 类型的 `RegisterHelpDoc` |
| `pkg/extension/manifest.go` | `validate()` 加固 `tools[]` |
| `pkg/extension/manager.go` | 删 4 KiB 上限，改走 `pkg/skillmd` |
| `pkg/extension/bridge.go` | `toolIndex` 保留 `ToolDef`，新增 `FindToolDef` |
| `cmd/opsctl/command/{root,exec,batch,create,handler}.go` | verb 表、类型分派、`--type`、派发名 |
| `plugin/opsctl/skills/opsctl/{SKILL.md,references/commands.md}` | CLI 文档 |
| `frontend/src/components/approval/ApprovalBlock.tsx` | 删除审批的新 kind（不可 grant） |
| `frontend/src/components/ai/ToolBlock.tsx` | 五个新工具名的图标 |

---

## Task 1: 类型断言缝（`permission.AssertAssetType`）+ AI `exec` 的 `type` 参数

spec §4.1 / §4.6「决策更新」。断言**不参与派发**——协议永远从资产记录取；它只把模型/人写错方言
的情况，从「协议层报一个像基础设施故障的错」变成「一条点名双方类型的建模错误」。

复用点：`internal/ai/permission/type_registry.go:49-56` 的注册表**已经**带协议别名
（`ssh`←`exec`、`database`←`sql`、`mongodb`←`mongo`）。断言直接查这张表，
于是 `opsctl batch` 的 `'sql:2:SELECT 1'` 前缀天然被接受——不需要任何兼容 shim。

**Files:**
- Create: `internal/ai/permission/type_assert.go`
- Create: `internal/ai/permission/type_assert_test.go`
- Modify: `internal/ai/tool/tool_handlers_unified.go:45-60`（`handleExec` 开头）
- Modify: `internal/ai/tool/tools_unified.go:31-39`（`exec` 的 schema）
- Test: `internal/ai/tool/tool_handlers_unified_test.go`（追加）

**Interfaces:**
- Produces:
  - `func permission.CanonicalTypeFor(name string) (string, bool)` — 类型名或协议别名 → 规范资产类型
  - `func permission.AssertAssetType(asset *asset_entity.Asset, declared string) error` —
    `declared == ""` 返回 nil；不认识的名字与不匹配都返回点名双方的 error
- Consumes: `permission.permissionTypeFor`（同包私有）、`asset_entity.Asset`

- [ ] **Step 1: 写失败测试（断言语义）**

创建 `internal/ai/permission/type_assert_test.go`：

```go
package permission

import (
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestCanonicalTypeFor(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"ssh", asset_entity.AssetTypeSSH, true},
		{"exec", asset_entity.AssetTypeSSH, true},          // 协议别名
		{"sql", asset_entity.AssetTypeDatabase, true},      // opsctl batch 前缀沿用
		{"database", asset_entity.AssetTypeDatabase, true},
		{"mongo", asset_entity.AssetTypeMongoDB, true},
		{"redis", asset_entity.AssetTypeRedis, true},
		{"nonsense", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := CanonicalTypeFor(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("CanonicalTypeFor(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestAssertAssetType(t *testing.T) {
	redis := &asset_entity.Asset{Name: "cache-1", Type: asset_entity.AssetTypeRedis}
	db := &asset_entity.Asset{Name: "prod-db", Type: asset_entity.AssetTypeDatabase}

	if err := AssertAssetType(redis, ""); err != nil {
		t.Fatalf("empty declaration must skip the assertion, got %v", err)
	}
	if err := AssertAssetType(redis, "redis"); err != nil {
		t.Fatalf("matching type must pass, got %v", err)
	}
	if err := AssertAssetType(db, "sql"); err != nil {
		t.Fatalf("protocol alias must resolve to the canonical type, got %v", err)
	}

	err := AssertAssetType(redis, "database")
	if err == nil {
		t.Fatal("mismatched type must fail")
	}
	// 报错必须点名双方，并指向 help——与 execGuidance 同格式（spec §4.6）。
	for _, want := range []string{`"cache-1"`, "type=redis", "type=database", "help"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}

	err = AssertAssetType(redis, "nonsense")
	if err == nil {
		t.Fatal("unknown type name must fail rather than silently pass")
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("error %q must name the unknown type", err.Error())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/permission/ -run 'TestCanonicalTypeFor|TestAssertAssetType' -v`
Expected: FAIL，`undefined: CanonicalTypeFor` / `undefined: AssertAssetType`（编译错误）

- [ ] **Step 3: 实现**

创建 `internal/ai/permission/type_assert.go`：

```go
package permission

import (
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// CanonicalTypeFor 把一个类型名或协议别名规范成资产类型。
//
// 别名来自本包既有的 permissionTypes 注册表（type_registry.go 的 init）：
// ssh←exec、database←sql、mongodb←mongo。opsctl batch 的 `sql:2:SELECT 1` 前缀
// 与 batch_exec 条目的 type 字段都落在这张表上，因此"沿用旧写法"不需要任何兼容 shim。
//
// 注意 GrantToolCp（"cp"）也注册在同一张表里，它不是资产类型；调用方拿到的
// canonical 会是 "cp"，与任何 asset.Type 都不相等，于是断言正常失败——这正确。
func CanonicalTypeFor(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	handler, ok := permissionTypeFor(name)
	if !ok {
		return "", false
	}
	return handler.canonical, true
}

// AssertAssetType 校验调用方声明的类型与资产的真实类型是否一致。
//
// declared 为空 = 不声明 = 跳过校验（这是缺省形态，spec §4.6 的表格）。
// 声明了就必须对得上：派发永远由资产导出，这里只把"模型/人写错方言"从协议层的
// 服务端报错（读起来像基础设施故障）提前成一条点名双方类型的建模错误。
//
// 调用方必须把它放在权限检查之前——它无副作用，而 CheckForAsset 会弹审批对话框。
func AssertAssetType(asset *asset_entity.Asset, declared string) error {
	if declared == "" {
		return nil
	}
	canonical, ok := CanonicalTypeFor(declared)
	if !ok {
		return fmt.Errorf("unknown type %q; asset %q is type=%s — call help(asset=%q) for its command syntax",
			declared, asset.Name, asset.Type, asset.Name)
	}
	if canonical != asset.Type {
		return fmt.Errorf("asset %q is type=%s, but you passed type=%s — call help(asset=%q) for its command syntax",
			asset.Name, asset.Type, canonical, asset.Name)
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ai/permission/ -run 'TestCanonicalTypeFor|TestAssertAssetType' -v`
Expected: PASS

- [ ] **Step 5: 写 `handleExec` 的失败测试（断言必须早于审批）**

追加到 `internal/ai/tool/tool_handlers_unified_test.go`（沿用该文件已有的 `setupUnified` 与
假 checker/执行器夹具；若夹具名不同，以文件现状为准，不要新建一套）：

```go
// exec 的 type 断言必须在权限检查之前失败：CheckForAsset 会弹审批对话框并阻塞，
// 让用户先批准一条注定失败的命令是缺陷（Plan A 收尾评审 IMPORTANT-1 的同一形状）。
func TestHandleExec_TypeAssertionFailsBeforeApproval(t *testing.T) {
	env := setupUnified(t) // 注册假执行器 + 记录 CheckForAsset 是否被调用

	_, err := handleExec(env.ctx, map[string]any{
		"asset":   "cache-1", // redis 资产
		"command": "SELECT 1",
		"type":    "database",
	})
	if err == nil {
		t.Fatal("declaring the wrong type must fail")
	}
	if !strings.Contains(err.Error(), "type=redis") || !strings.Contains(err.Error(), "type=database") {
		t.Errorf("error %q must name both the real and the declared type", err.Error())
	}
	if env.checkCalls != 0 {
		t.Errorf("permission check ran %d times; the assertion must short-circuit before approval", env.checkCalls)
	}
	if env.execCalls != 0 {
		t.Errorf("executor ran %d times; a failed assertion must not execute", env.execCalls)
	}
}

// 声明正确的类型（含协议别名）照常执行。
func TestHandleExec_TypeAssertionAcceptsAliasAndMatch(t *testing.T) {
	env := setupUnified(t)

	for _, declared := range []string{"", "redis"} {
		if _, err := handleExec(env.ctx, map[string]any{
			"asset": "cache-1", "command": "PING", "type": declared,
		}); err != nil {
			t.Fatalf("type=%q must be accepted, got %v", declared, err)
		}
	}
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/ai/tool/ -run TestHandleExec_TypeAssertion -v`
Expected: FAIL —— `declaring the wrong type must fail`（当前 `type` 参数被忽略，命令一路跑到执行器）

- [ ] **Step 7: 在 `handleExec` 插入断言**

`internal/ai/tool/tool_handlers_unified.go`，在资产解析之后、命令非空校验之前插入
（顺序理由：断言只依赖已解析出的资产，是最便宜的一步；它必须早于任何有副作用的步骤）：

```go
	asset, err := assetref.Resolve(ctx, aictx.ArgString(args, "asset"))
	if err != nil {
		return "", err
	}

	// 可选类型断言：不参与派发（协议只从 asset.Type 取），只把方言写错的情况提前
	// 变成点名双方类型的错误。放在这里 = 所有有副作用的步骤（审批弹窗、执行）之前。
	if err := permission.AssertAssetType(asset, aictx.ArgString(args, "type")); err != nil {
		recordShortCircuit(ctx, aictx.SourceExecTypeMismatch)
		return "", err
	}

	command := aictx.ArgString(args, "command")
```

在 `internal/ai/aictx` 的 decision source 常量表里补一条（与既有
`SourceExecUnsupportedType` / `SourceExecGateBlocked` / `SourceExecCanonicalizeError` /
`SourceExecPrecheckFailed` 并列，保持同一命名与取值风格）：

```go
	// SourceExecTypeMismatch exec/batch_exec 的可选 type 断言与资产真实类型不符，
	// 命令在权限检查之前就被挡下。
	SourceExecTypeMismatch = "exec_type_mismatch"
```

- [ ] **Step 8: 给 `exec` 工具补 `type` 参数**

`internal/ai/tool/tools_unified.go` 的 `SchemaVal.Properties` 追加（`Required` **不变**，仍是
`{"asset", "command"}`）：

```go
					"type": {Type: "string", Description: "Optional assertion of the asset's type (e.g. \"redis\", \"database\"). Not used for dispatch — the protocol always comes from the asset record. Pass it when you believe you know the type: a mismatch is reported before anything executes, instead of surfacing later as a protocol-level error."},
```

同时在 `DescStr` 末尾追加一句（模型只读描述，不读 schema 的长文）：

```go
					"Pass type=<asset type> when you know it: a wrong guess is caught before execution. "
```

- [ ] **Step 9: 跑全部相关测试**

Run: `go test ./internal/ai/... -run 'TestHandleExec|TestTools_RegistryShape' -v`
Expected: PASS（`tools_test.go` 的 schema 合法性断言会检查 `Required` ⊆ `Properties`，新参数不在 `Required` 里，不受影响）

- [ ] **Step 10: 变异验证**

把 Step 7 插入的断言整段临时挪到 `checker.CheckForAsset` 调用**之后**，重跑
`go test ./internal/ai/tool/ -run TestHandleExec_TypeAssertionFailsBeforeApproval`。
Expected: FAIL（`permission check ran 1 times`）——证明「早于审批」这条不变式真被测试锁住，
而不是只测了「会报错」。确认后还原。

- [ ] **Step 11: 提交**

```bash
git add internal/ai/permission/type_assert.go internal/ai/permission/type_assert_test.go \
        internal/ai/tool/tool_handlers_unified.go internal/ai/tool/tools_unified.go \
        internal/ai/tool/tool_handlers_unified_test.go internal/ai/aictx/*.go
git commit -m "✨ exec 支持可选 type 断言，复用 permission 既有的协议别名表"
```

---

## Task 2: `batch_command` → `batch_exec`（type 变断言，派发改走统一执行器）

现状（`tool_handler_batch.go:216-237`）：条目的 `type` 是**handler 选择器**（`exec`/`sql`/`redis`），
经 `batchApprovalAssetType` 映射成策略组，`executeBatchItem` 里三路 switch 分派——
这正是 spec §1 说的「用自然语言/数据写死的 `switch assetType`」的最后一份。
mongodb / etcd / kafka / k8s 资产在 batch 里**根本不可达**（`default: unknown type`）。

改造后：`type` 变成与 `exec` 同语义的**可选断言**，派发由 `permission.ExecutorFor(asset.Type)` 决定，
于是 batch 自动覆盖全部已注册类型；权限检查也改用资产**真实类型** + **规范化后**的命令，
与 `exec` 的「批的 == 执行的」不变式对齐。

> **注意**：`type` 的默认值必须**删掉**。现在 `tool_handler_batch.go:66-70` 把空 type 默认成
> `"exec"`，那是选择器时代的产物；断言语义下保留它等于给每一条没写 type 的条目强行断言 ssh，
> 非 ssh 资产会全数失败。

**Files:**
- Modify: `internal/ai/tool/tools_exec.go:58-77`（工具定义改名 + 描述）
- Modify: `internal/ai/tool/tool_handler_batch.go`（全文：默认值、检查、派发）
- Modify: `internal/ai/tool/tools_test.go:30-40`（`expected` 清单）
- Modify: `internal/ai/tool/permission_failclosed_test.go:95-116`（两处 `batch_command` 字样与注释）
- Test: `internal/ai/tool/tool_handler_batch_test.go`（新建，若已存在则追加）

**Interfaces:**
- Consumes: `permission.AssertAssetType`（Task 1）、`permission.ExecutorFor` / `CanonicalizeFor`（既有）
- Produces: 工具名 `batch_exec`；handler 仍是 `handleBatchCommand`（**函数名不改**，
  它是包内符号，改名只会制造无信息的 diff；工具名与函数名不必同步）

- [ ] **Step 1: 写失败测试**

新建 `internal/ai/tool/tool_handler_batch_test.go`（沿用同包既有夹具风格；假执行器用
`permission.RegisterExecutor` + `t.Cleanup(func(){ permission.UnregisterExecutorForTest(...) })`）：

```go
// batch 条目的 type 从"handler 选择器"变成"可选断言"后，未注册执行器的类型必须
// 不再被硬编码的三路 switch 挡在门外。
func TestHandleBatch_DispatchesByAssetType(t *testing.T) {
	env := setupBatch(t) // 注册 mongodb 假执行器，资产 "docs-1" 是 mongodb 类型

	out, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"docs-1","command":"find app.users {}"}]`,
	})
	if err != nil {
		t.Fatalf("mongodb asset must be dispatchable in batch, got %v", err)
	}
	if env.execCalls["docs-1"] != 1 {
		t.Errorf("executor for the asset's real type must run exactly once, got %d", env.execCalls["docs-1"])
	}
	if strings.Contains(out, "unknown type") {
		t.Errorf("output still mentions the retired type switch: %s", out)
	}
}

// 旧写法（协议别名前缀）继续可用——opsctl batch 的 'sql:2:SELECT 1' 走的就是这条。
func TestHandleBatch_TypeAliasStillAccepted(t *testing.T) {
	env := setupBatch(t)

	if _, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"prod-db","type":"sql","command":"SELECT 1"}]`,
	}); err != nil {
		t.Fatalf("legacy protocol alias must still be accepted, got %v", err)
	}
	if env.execCalls["prod-db"] != 1 {
		t.Errorf("aliased item must execute, got %d calls", env.execCalls["prod-db"])
	}
}

// 断言不符的条目被拒，且**不执行**；同批次里其它条目不受影响。
func TestHandleBatch_TypeMismatchDeniesOnlyThatItem(t *testing.T) {
	env := setupBatch(t)

	out, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"cache-1","type":"database","command":"PING"},
		              {"asset":"prod-db","command":"SELECT 1"}]`,
	})
	if err != nil {
		t.Fatalf("one bad item must not fail the whole batch: %v", err)
	}
	if env.execCalls["cache-1"] != 0 {
		t.Errorf("mismatched item must not execute, got %d calls", env.execCalls["cache-1"])
	}
	if env.execCalls["prod-db"] != 1 {
		t.Errorf("the sibling item must still run, got %d calls", env.execCalls["prod-db"])
	}
	if !strings.Contains(out, "type=redis") {
		t.Errorf("result must name the real type for the denied item: %s", out)
	}
}

// 空 type 不再被默认成 "exec"：默认值是选择器时代的产物，断言语义下它会让
// 每条未声明 type 的条目都强行断言 ssh。
func TestHandleBatch_EmptyTypeIsNoAssertion(t *testing.T) {
	env := setupBatch(t)

	if _, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"cache-1","command":"PING"}]`,
	}); err != nil {
		t.Fatalf("an item without type must dispatch by the asset's real type, got %v", err)
	}
	if env.execCalls["cache-1"] != 1 {
		t.Errorf("redis item without type must execute, got %d calls", env.execCalls["cache-1"])
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/tool/ -run TestHandleBatch_ -v`
Expected: FAIL —— mongodb 那条报 `unknown type: exec`（默认值 + 三路 switch 的双重后果）

- [ ] **Step 3: 改工具定义**

`internal/ai/tool/tools_exec.go:58-77`：

```go
		&tool.RawTool{
			NameStr: "batch_exec",
			DescStr: "Execute commands on multiple assets in parallel. Dispatches each item by that asset's real type — the same coverage as exec, including database/redis/mongodb/etcd/kafka/k8s. Each command is policy-checked; items needing user confirmation are batched into a single approval prompt. Results are returned per-asset (success or error). Prefer this over looping exec calls when targeting >1 asset.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"commands": {Type: "string", Description: `JSON array of commands. Each item: {"asset": "name-or-id", "command": "...", "type": "<optional type assertion>"}. type is never used for dispatch — omit it unless you want a wrong guess caught before execution. Example: [{"asset":"web-1","command":"uptime"},{"asset":"42","type":"database","command":"SELECT VERSION()"}]`},
				},
				Required: []string{"commands"},
			},
			IsSerial: false,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleBatchCommand(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
```

- [ ] **Step 4: 删掉默认值与三路 switch，改走执行器表**

`internal/ai/tool/tool_handler_batch.go`：

1. 删除 `66-70` 行的默认值循环（整段）。
2. 解析循环（`90-119`）里，`resolveAssetForBatch` 之后改为持有 `*asset_entity.Asset`
   （`assetref.Resolve` 本来就返回它，现在的 `resolveAssetForBatch` 只回传 id/name 是信息丢失），
   插入断言与规范化，并用**真实类型**做检查：

```go
	for _, cmd := range commands {
		asset, resolveErr := assetref.Resolve(ctx, cmd.Asset)
		if resolveErr != nil {
			resolved = append(resolved, resolvedCmd{item: cmd, decision: "deny", denyMsg: resolveErr.Error()})
			continue
		}

		// 可选类型断言——与 exec 同一个函数、同一条不变式（早于审批）。
		if err := permission.AssertAssetType(asset, cmd.Type); err != nil {
			resolved = append(resolved, resolvedCmd{
				item: cmd, assetID: asset.ID, assetName: asset.Name,
				decision: "deny", denyMsg: err.Error(),
			})
			continue
		}

		if _, ok := permission.ExecutorFor(asset.Type); !ok {
			resolved = append(resolved, resolvedCmd{
				item: cmd, assetID: asset.ID, assetName: asset.Name,
				decision: "deny",
				denyMsg:  fmt.Sprintf("asset %q (type=%s) has no exec support yet", asset.Name, asset.Type),
			})
			continue
		}

		// 权限检查用规范化后的命令：批的是这个，就该按这个匹配策略——与 handleExec 一致。
		checkCommand := cmd.Command
		if canonicalize, ok := permission.CanonicalizeFor(asset.Type); ok {
			canonical, cerr := canonicalize(asset, cmd.Command)
			if cerr != nil {
				resolved = append(resolved, resolvedCmd{
					item: cmd, assetID: asset.ID, assetName: asset.Name,
					decision: "deny", denyMsg: cerr.Error(),
				})
				continue
			}
			checkCommand = canonical
		}

		decision, denyMsg := "allow", ""
		result := permission.CheckPermission(ctx, asset.Type, asset.ID, checkCommand)
		switch result.Decision {
		case aictx.Deny:
			decision, denyMsg = "deny", result.Message
		case aictx.NeedConfirm:
			decision = "needConfirm"
		case aictx.Allow:
			decision = "allow"
		}

		resolved = append(resolved, resolvedCmd{
			item: cmd, asset: asset, assetID: asset.ID, assetName: asset.Name,
			checkCommand: checkCommand, decision: decision, denyMsg: denyMsg,
		})
	}
```

   `resolvedCmd` 结构体相应加两个字段：`asset *asset_entity.Asset` 与 `checkCommand string`。

3. 审批项（`127-132`）的 `Type` 改成资产真实类型对应的 approval type，并用 `checkCommand`：
   审批弹窗展示的必须与策略匹配的是同一个串（kafka 的双 token 串就是靠这条才对得上）。

```go
					needConfirmItems = append(needConfirmItems, permission.ApprovalItem{
						Type:      permission.ApprovalTypeFor(r.asset.Type),
						AssetID:   r.assetID,
						AssetName: r.assetName,
						Command:   r.checkCommand,
					})
```

   在 `type_registry.go` 补一个导出 getter（`approvalType` 字段本就在表里，只是没有出口）：

```go
// ApprovalTypeFor 返回该资产类型在审批面板上的类型标签（前端 TypeBadge 按它取图标）。
// 未注册类型回落到原样返回——审批项宁可显示一个陌生标签，也不该静默变成 "exec"。
func ApprovalTypeFor(assetType string) string {
	if handler, ok := permissionTypeFor(assetType); ok {
		return handler.approvalType
	}
	return assetType
}
```

4. `executeBatchItem`（`202-247`）删掉三路 switch，改为：

```go
func executeBatchItem(ctx context.Context, item batchCommandItem, asset *asset_entity.Asset) batchResultItem {
	result := batchResultItem{
		AssetID: asset.ID, AssetName: asset.Name,
		Type: asset.Type, Command: item.Command,
	}

	exec, ok := permission.ExecutorFor(asset.Type)
	if !ok {
		// 解析阶段已经拦过一次；这里是并发执行路径上的兜底，只可能在执行器被
		// 反注册（仅测试）时发生。
		result.ExitCode = -1
		result.Error = fmt.Sprintf("asset %q (type=%s) has no exec support yet", asset.Name, asset.Type)
		return result
	}

	// 执行用**原始**命令，不是规范化后的串——规范化结果是给策略/审批/审计看的展示形式，
	// 喂给执行器是有损的（引号与内部空格会被吃掉）。与 handleExec 第 8 步同一理由。
	output, err := exec(ctx, asset, item.Command, "")
	if err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 0
	result.Stdout = output
	return result
}
```

5. 删除 `batchApprovalAssetType`（`265-276`）与 `resolveAssetForBatch`（`249-263`）——
   前者是被替换掉的类型分支表，后者的调用点已改为直接用 `assetref.Resolve`。
   `batchResultItem.Type` 现在填资产真实类型（此前填的是选择器值）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/ai/tool/ -run TestHandleBatch_ -v`
Expected: PASS（4 条全绿）

- [ ] **Step 6: 同步两处清单与一处注释**

- `internal/ai/tool/tools_test.go:30-40`：`"batch_command"` → `"batch_exec"`。
- `internal/ai/tool/permission_failclosed_test.go`：函数名与注释里的 `batch_command` 字样改成
  `batch_exec`（`TestHandleBatchCommand_PreapprovedStillFailsClosed` 的注释写着
  「哪天有人把 batch_command 加进 AllToolDefs」——这条断言的价值不变，只是名字要跟上）。
- `internal/ai/tool/tool_registry.go:39-40` 的 `AllToolDefs` 文档注释同样提到 `batch_command`，一并改。

- [ ] **Step 7: 跑全量 Go 测试**

Run: `go test ./internal/... ./cmd/...`
Expected: 0 FAIL

- [ ] **Step 8: 变异验证**

把 Step 4 里 `CheckPermission` 的第一个实参从 `asset.Type` 改回写死的
`asset_entity.AssetTypeSSH`，重跑 `go test ./internal/ai/tool/ -run TestHandleBatch_ -v`。
Expected: FAIL —— 说明「按真实类型检查」被测试锁住（spec §5 第 2 条正是这条不变式）。
若全绿，说明夹具里的策略检查是个恒 allow 的假货，必须先把夹具改成能区分类型的版本再继续。

- [ ] **Step 9: 提交**

```bash
git add internal/ai/tool/tools_exec.go internal/ai/tool/tool_handler_batch.go \
        internal/ai/tool/tool_handler_batch_test.go internal/ai/tool/tools_test.go \
        internal/ai/tool/permission_failclosed_test.go internal/ai/tool/tool_registry.go \
        internal/ai/permission/type_registry.go
git commit -m "♻️ batch_command 改名 batch_exec，条目 type 从 handler 选择器改为可选断言"
```

---

## Task 3: 类型文档承载 `config` 形状（8 份补配置段 + 4 份 doc-only + 齐备性测试）

Task 4 要删掉 `add_asset` 那张覆盖 10 种类型的巨型 schema（`tools_asset.go:53-93`，40 个属性），
把 `config` 变成自由对象。**删之前必须先把那份知识挪到类型文档里**，否则模型将失去
「某类型建资产要填哪些字段」的全部信息——这是本 Plan 最容易造成静默能力回退的一步。

四个没有命令面的类型（`rdp` / `vnc` / `oss` / `local`）同样需要配置文档，
因此需要一个 **doc-only 注册入口**：有 `help`、无 `exec`。

**Files:**
- Modify: `internal/ai/skills/{ssh,serial,database,redis,k8s,mongodb,etcd,kafka}/SKILL.md`（各追加一节）
- Create: `internal/ai/skills/{rdp,vnc,oss,local}/SKILL.md`
- Modify: `internal/ai/permission/type_registry.go`（`RegisterHelpDoc` + `ExecutorFor` 的 nil 守卫）
- Modify: `internal/ai/execimpl/register.go`（四条 doc-only 注册）
- Create: `internal/ai/execimpl/help_coverage_test.go`
- Modify: `internal/ai/execimpl/coverage_test.go`（注释交叉引用）

**Interfaces:**
- Produces:
  - `func permission.RegisterHelpDoc(canonical, help string)` — 只注册文档，不注册执行器
  - `permission.RegisteredExecTypes()` 语义不变（**只列有执行器的类型**）
  - `func permission.RegisteredHelpTypes() []string` — 有文档的类型（执行器 ∪ doc-only）
- Consumes: `skills.Get`（既有）、`assettype.All()`（既有）

- [ ] **Step 1: 写失败测试（齐备性）**

创建 `internal/ai/execimpl/help_coverage_test.go`：

```go
package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
)

// 每个注册了的资产类型都必须有 help 文档——**没有豁免清单**。
//
// 理由与 exec 的豁免清单（coverage_test.go）不同：exec 覆盖不全只是"这个类型还不能跑命令"，
// 而 help 覆盖不全会让 put_asset 变成不可用——Plan C 删掉了 add_asset 那张按类型枚举的
// 巨型 schema，config 的形状此后**只**由这份文档承载。少一份文档 = 模型无从知道该类型
// 要填什么字段，且不会报错，只会瞎猜。
func TestEveryAssetTypeHasHelpDoc(t *testing.T) {
	for _, h := range assettype.All() {
		if _, ok := permission.HelpFor(h.Type()); !ok {
			t.Errorf("asset type %q has no help doc; add internal/ai/skills/%s/SKILL.md and register it "+
				"(RegisterExecutor if it has a command surface, RegisterHelpDoc if it only has config)",
				h.Type(), h.Type())
		}
	}
}

// doc-only 类型有文档但**没有**执行器：help 能查，exec 必须明确报"尚不支持"，
// 而不是查到一个 nil 执行器后 panic。
func TestDocOnlyTypesHaveNoExecutor(t *testing.T) {
	for _, docOnly := range []string{"rdp", "vnc", "oss", "local"} {
		if _, ok := permission.HelpFor(docOnly); !ok {
			t.Errorf("doc-only type %q must have a help doc", docOnly)
		}
		if _, ok := permission.ExecutorFor(docOnly); ok {
			t.Errorf("doc-only type %q must not report an executor", docOnly)
		}
	}
	// 且不得混进 exec 的类型清单（它会进模型看到的 exec 工具描述）。
	for _, listed := range permission.RegisteredExecTypes() {
		switch listed {
		case "rdp", "vnc", "oss", "local":
			t.Errorf("doc-only type %q must not appear in RegisteredExecTypes()", listed)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/execimpl/ -run 'TestEveryAssetTypeHasHelpDoc|TestDocOnlyTypes' -v`
Expected: FAIL —— 四个类型没有文档；`RegisterHelpDoc` 未定义（编译错误）

- [ ] **Step 3: 加 doc-only 注册入口**

`internal/ai/permission/type_registry.go`：

```go
// RegisterHelpDoc 只注册用法文档，不注册执行器——给没有命令面、但可以被 put_asset
// 创建/更新的类型用（rdp / vnc / oss / local）。它们的 SKILL.md 只写配置字段。
//
// 与 RegisterExecutor 共用同一张 execEntries 表，因此 HelpFor 天然可用；
// 而 ExecutorFor / RegisteredExecTypes 会跳过 exec == nil 的条目——
// exec 对这些类型必须报"尚不支持"，不能查到一个 nil 函数再 panic。
func RegisterHelpDoc(canonical, help string) {
	if canonical == "" || help == "" {
		panic("permission: invalid help-doc registration")
	}
	if _, exists := execEntries[canonical]; exists {
		panic(fmt.Sprintf("permission: duplicate help-doc registration %q", canonical))
	}
	execEntries[canonical] = &execEntry{help: help}
}
```

同文件的 `ExecutorFor` 与 `RegisteredExecTypes` 加 nil 守卫：

```go
func ExecutorFor(assetType string) (ExecFunc, bool) {
	entry, ok := execEntries[assetType]
	if !ok || entry.exec == nil { // doc-only 条目没有执行器
		return nil, false
	}
	return entry.exec, true
}

// RegisteredExecTypes 返回**能执行命令**的资产类型，已排序。doc-only 条目不在其中——
// 这份清单会进模型看到的 exec 工具描述，把只有配置文档的类型列进去等于承诺一个做不到的能力。
func RegisteredExecTypes() []string {
	types := make([]string, 0, len(execEntries))
	for name, entry := range execEntries {
		if entry.exec == nil {
			continue
		}
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}

// RegisteredHelpTypes 返回有用法文档的资产类型（有执行器的 + doc-only 的），已排序。
// put_asset 的错误信息与 prompt 的类型清单用它。
func RegisteredHelpTypes() []string {
	types := make([]string, 0, len(execEntries))
	for name := range execEntries {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
```

- [ ] **Step 4: 写四份 doc-only SKILL.md**

`internal/ai/skills/rdp/SKILL.md`（其余三份同构，字段按各自
`internal/assettype/{vnc,oss,local}.go` 的 `ValidateCreateArgs` / `ApplyCreateArgs` 逐字核对，
**不要凭印象写**）：

```markdown
---
name: rdp
description: "Windows remote desktop assets. Config fields for put_asset; no command surface — exec is not supported for this type."
---

# RDP assets

RDP assets are opened as an interactive desktop session in the app. There is **no command
surface**: `exec` is not supported for this type, and there is nothing to script.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | Hostname or IP |
| `port` | number | no | Defaults to 3389 |
| `username` | string | yes | Local or domain account |
| `password` | string | no | Stored encrypted; never echoed back |
| `domain` | string | no | Windows domain; omit for local accounts |
| `width` | number | no | Initial desktop width, defaults to 1280 |
| `height` | number | no | Initial desktop height, defaults to 720 |
| `clipboard` | string | no | `"true"` / `"false"`, defaults to true |

Example:

    put_asset(name="win-jump", type="rdp", config={"host":"10.0.1.9","username":"admin","domain":"CORP"})
```

- [ ] **Step 5: 给 8 份既有 SKILL.md 追加同名小节**

在每份现有 `internal/ai/skills/<type>/SKILL.md` 末尾追加一节 `## Asset config (for put_asset)`，
字段来源是**被删掉的那张巨型 schema**（`tools_asset.go:55-91`）里属于该类型的属性
＋ 该类型 `ValidateCreateArgs` 的必填判断。例如 `database`：

```markdown
## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | |
| `port` | number | yes | 3306 (mysql) / 5432 (postgresql) |
| `username` | string | yes | |
| `password` | string | no | Stored encrypted; never echoed back |
| `driver` | string | yes | `"mysql"` or `"postgresql"` |
| `database` | string | no | Default database; empty string clears it |
| `read_only` | string | no | `"true"` enables read-only mode |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches |
```

- [ ] **Step 6: 注册四条 doc-only 条目**

`internal/ai/execimpl/register.go` 的 `init()` 末尾：

```go
	// 没有命令面、但可以被 put_asset 创建/更新的类型：只注册文档。
	// exec 对它们仍然报 "no exec support yet"（RegisteredExecTypes 会跳过 exec == nil 的条目）。
	for _, docOnly := range []string{
		asset_entity.AssetTypeRDP,
		asset_entity.AssetTypeVNC,
		asset_entity.AssetTypeOSS,
		asset_entity.AssetTypeLocal,
	} {
		doc, ok := skills.Get(docOnly)
		if !ok {
			// 文档缺失是编译期就能发现的接线错误（SKILL.md 是 //go:embed 进来的），
			// 静默跳过会让 put_asset 对该类型永远无从查起。
			panic("execimpl: missing SKILL.md for doc-only asset type " + docOnly)
		}
		permission.RegisterHelpDoc(docOnly, doc)
	}
```

（四个常量都已存在：`asset_entity/asset.go:26-29` 的 `AssetTypeLocal` / `AssetTypeVNC` /
`AssetTypeRDP` / `AssetTypeOSS`。不要写字面量。）

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/ai/execimpl/ ./internal/ai/skills/ -v`
Expected: PASS（含既有的 `TestEveryPolicyKindTypeHasExecutor` 与 `skill_examples_test.go`）

- [ ] **Step 8: 更新 exec 豁免清单的交叉引用注释**

`internal/ai/execimpl/coverage_test.go:20-22` 现在写着「vnc / rdp / oss：PolicyKind 为空……
列在这里仅供交叉核对」。补一句：这三个类型现在有 doc-only 的 help 文档
（`help_coverage_test.go` 锁住），仍然没有执行器。**不要改 `maxExemptions`**——豁免清单没有增长。

- [ ] **Step 9: 变异验证**

删掉 `internal/ai/skills/vnc/SKILL.md`，重跑 `go test ./internal/ai/execimpl/`。
Expected: panic/FAIL（Step 6 的 panic 或 `TestEveryAssetTypeHasHelpDoc`）——证明齐备性测试
真的锁住了文档存在性，而不是只锁住注册代码。还原。

- [ ] **Step 10: 提交**

```bash
git add internal/ai/skills/ internal/ai/permission/type_registry.go \
        internal/ai/execimpl/register.go internal/ai/execimpl/help_coverage_test.go \
        internal/ai/execimpl/coverage_test.go
git commit -m "✨ 类型文档承载 config 形状，新增 rdp/vnc/oss/local 四份 doc-only skill"
```

---

## Task 4: `put_asset` / `put_group`

spec §4.3。`add_asset` 的巨型 schema 与我们正在移除的类型分支是同一种耦合，
只是写成了 JSON Schema 的属性并集：40 个属性里绝大多数对任一具体类型都是噪音，
而 `oss` 类型压根没进去过——**今天用 AI 建 oss 资产是做不到的**，这次顺带修好。

标识形态：`put_asset` 用 **`asset`（名称或 id，可选）** 而不是 spec 写的 `id?`。
两个理由：(1) 与 `exec`/`help`/`delete_asset`/`batch_exec` 共用同一个资产标识契约，
仓内只有一个「什么是合法资产标识」的答案（Plan B 修 `batch_command` 按名字寻址恒失败时确立）；
(2) `runner.auditMiddleware` 的 `resolveAssetForAudit` 认的就是 `args["asset"]`，
用它能免费拿到正确的审计归属。**分组仍用数字 `id`**：仓内没有 `groupref` 解析器，
为此新造一个不在本 Plan 范围内（`get_group` / `update_group` 一直是数字 id）。

**Files:**
- Create: `internal/ai/tool/tools_crud.go`（`put_asset` / `put_group` / 后续 Task 5 的两个 delete）
- Create: `internal/ai/tool/tool_handlers_crud.go`
- Modify: `internal/ai/tool/tools_asset.go`（删掉四个写工具，只留 `list_assets`/`get_asset`/`list_groups`/`get_group`）
- Modify: `internal/ai/tool/tool_handlers_asset.go`（`handleAddAsset`/`handleUpdateAsset`/`handleAddGroup`/`handleUpdateGroup` 迁走）
- Modify: `internal/ai/tool/tools.go`（追加 `crudTools()`）
- Modify: `internal/ai/tool/tool_registry.go`（`AllToolDefs`）
- Modify: `internal/ai/tool/tools_test.go`
- Test: `internal/ai/tool/tool_handlers_crud_test.go`

**Interfaces:**
- Produces:
  - `put_asset(asset?: string, name?: string, type?: string, group_id?: number, description?: string, icon?: string, config?: object)`
  - `put_group(id?: number, name?: string, parent_id?: number, icon?: string, description?: string, sort_order?: number)`
  - `func handlePutAsset(ctx, args) (string, error)` / `func handlePutGroup(ctx, args) (string, error)`
- Consumes: `assettype.Get(...).ValidateCreateArgs/ApplyCreateArgs/ApplyUpdateArgs`、
  `asset_svc.Asset().Create/Update/Get`、`assetref.Resolve`、`aictx.NotifyDataChanged`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/tool/tool_handlers_crud_test.go`：

```go
// 有 asset → 更新；无 asset → 创建。同一个工具，分支只由标识的有无决定。
func TestHandlePutAsset_CreateThenUpdate(t *testing.T) {
	env := setupCRUD(t)

	out, err := handlePutAsset(env.ctx, map[string]any{
		"name": "web-9", "type": "ssh",
		"config": map[string]any{"host": "10.0.0.9", "port": float64(22), "username": "root"},
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if env.assetCount() != 1 {
		t.Fatalf("expected exactly 1 asset after create, got %d", env.assetCount())
	}

	if _, err := handlePutAsset(env.ctx, map[string]any{
		"asset": "web-9",
		"config": map[string]any{"username": "deploy"},
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if env.assetCount() != 1 {
		t.Errorf("put with an identifier must update in place, not create a second row (got %d)", env.assetCount())
	}
	if got := env.asset("web-9").GetSSHConfig().Username; got != "deploy" {
		t.Errorf("username = %q, want %q", got, "deploy")
	}
	_ = out
}

// config 是自由对象，校验回到 assettype.ValidateCreateArgs——不是回到工具 schema。
func TestHandlePutAsset_ValidationComesFromAssetType(t *testing.T) {
	env := setupCRUD(t)

	_, err := handlePutAsset(env.ctx, map[string]any{
		"name": "broken", "type": "database",
		"config": map[string]any{"host": "10.0.0.1"}, // 缺 port/username/driver
	})
	if err == nil {
		t.Fatal("missing required config fields must fail")
	}
	if !strings.Contains(err.Error(), "driver") && !strings.Contains(err.Error(), "username") {
		t.Errorf("error %q should come from the asset type's own validation", err.Error())
	}
	if env.assetCount() != 0 {
		t.Errorf("a failed validation must not create anything, got %d assets", env.assetCount())
	}
}

// oss 类型此前被巨型 schema 完全遗漏（40 个属性里一个 oss 字段都没有），
// 自由 config 之后它必须可创建。
func TestHandlePutAsset_SupportsTypesTheOldSchemaOmitted(t *testing.T) {
	env := setupCRUD(t)

	if _, err := handlePutAsset(env.ctx, map[string]any{
		"name": "backup-bucket", "type": "oss",
		"config": env.validOSSConfig(), // 按 internal/assettype/oss.go 的必填字段构造
	}); err != nil {
		t.Fatalf("oss asset must be creatable via put_asset, got %v", err)
	}
}

// 未知类型必须报错并列出可用类型，而不是静默创建一个跑不起来的资产。
func TestHandlePutAsset_UnknownTypeIsNamed(t *testing.T) {
	env := setupCRUD(t)

	_, err := handlePutAsset(env.ctx, map[string]any{"name": "x", "type": "sqlite"})
	if err == nil || !strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("unknown type must be named in the error, got %v", err)
	}
}

func TestHandlePutGroup_CreateThenUpdate(t *testing.T) {
	env := setupCRUD(t)

	if _, err := handlePutGroup(env.ctx, map[string]any{"name": "prod"}); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := handlePutGroup(env.ctx, map[string]any{
		"id": float64(env.group("prod").ID), "description": "production fleet",
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if env.groupCount() != 1 {
		t.Errorf("put with an id must update in place, got %d groups", env.groupCount())
	}
	if got := env.group("prod").Description; got != "production fleet" {
		t.Errorf("description = %q, want %q", got, "production fleet")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/tool/ -run 'TestHandlePut' -v`
Expected: FAIL —— `undefined: handlePutAsset`

- [ ] **Step 3: 实现 handler**

创建 `internal/ai/tool/tool_handlers_crud.go`：

```go
package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// putArgs 把工具入参摊平成 assettype 处理器认识的形态。
//
// 工具契约是 `config` 嵌套对象（spec §4.3），而 assettype.ApplyCreateArgs / ApplyUpdateArgs
// 的契约是扁平 map——后者被桌面端与 opsctl 共用，不该为了 AI 工具的形状改动。
// 摊平只在这一处发生，是 AGENTS.md「在边界归一化一次」的直接应用。
func putArgs(args map[string]any) map[string]any {
	flat := map[string]any{}
	if config, ok := args["config"].(map[string]any); ok {
		for k, v := range config {
			flat[k] = v
		}
	}
	return flat
}

// handlePutAsset 创建或更新资产：带 asset 标识 → 更新，不带 → 创建。
//
// 与被它取代的 add_asset 的关键差异：config 是自由对象，其按类型的形状由该类型的
// SKILL.md（同一份 help 文档）说明，校验回到本就该负责的 assettype.ValidateCreateArgs。
// 旧的巨型 schema 把 10 种类型的字段并集写进 JSON Schema，既是我们正在移除的类型分支的
// 另一种写法，又漏掉了 oss——那类资产此前经 AI 完全无法创建。
func handlePutAsset(ctx context.Context, args map[string]any) (string, error) {
	config := putArgs(args)
	ref := aictx.ArgString(args, "asset")

	if ref == "" {
		return createAsset(ctx, args, config)
	}
	return updateAsset(ctx, ref, args, config)
}

func createAsset(ctx context.Context, args, config map[string]any) (string, error) {
	name := aictx.ArgString(args, "name")
	if name == "" {
		return "", fmt.Errorf("missing required parameter: name (creating an asset needs a name; pass asset=<id-or-name> instead to update an existing one)")
	}
	assetType := aictx.ArgString(args, "type")
	if assetType == "" {
		assetType = asset_entity.AssetTypeSSH
	}
	h, ok := assettype.Get(assetType)
	if !ok {
		return "", fmt.Errorf("unsupported asset type %q; supported: %s",
			assetType, strings.Join(permission.RegisteredHelpTypes(), ", "))
	}
	if err := h.ValidateCreateArgs(config); err != nil {
		return "", err
	}

	asset := &asset_entity.Asset{
		Name:        name,
		Type:        assetType,
		Icon:        aictx.ArgString(args, "icon"),
		GroupID:     aictx.ArgInt64(args, "group_id"),
		Description: aictx.ArgString(args, "description"),
	}
	if err := h.ApplyCreateArgs(ctx, asset, config); err != nil {
		return "", err
	}
	if err := asset_svc.Asset().Create(ctx, asset); err != nil {
		return "", fmt.Errorf("failed to create asset: %w", err)
	}
	aictx.NotifyDataChanged("asset")
	return fmt.Sprintf(`{"id":%d,"message":"asset created successfully"}`, asset.ID), nil
}

func updateAsset(ctx context.Context, ref string, args, config map[string]any) (string, error) {
	asset, err := assetref.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	if declared := aictx.ArgString(args, "type"); declared != "" {
		// 更新时给出的 type 与 exec 的 type 同语义：断言，不是改类型。
		// 资产类型是不可变的——改类型等于换协议、换配置形状、换策略组。
		if err := permission.AssertAssetType(asset, declared); err != nil {
			return "", err
		}
	}

	if name := aictx.ArgString(args, "name"); name != "" {
		asset.Name = name
	}
	if _, ok := args["description"]; ok {
		asset.Description = aictx.ArgString(args, "description")
	}
	// 仅接受正整数：避免误传 group_id=0 把资产悄悄移到未分组（潜在破坏性，走 UI）。
	if gid := aictx.ArgInt64(args, "group_id"); gid > 0 {
		asset.GroupID = gid
	}
	if icon := aictx.ArgString(args, "icon"); icon != "" {
		asset.Icon = icon
	}

	if h, ok := assettype.Get(asset.Type); ok {
		if err := h.ApplyUpdateArgs(ctx, asset, config); err != nil {
			return "", fmt.Errorf("apply update args failed: %w", err)
		}
	}
	if err := asset_svc.Asset().Update(ctx, asset); err != nil {
		return "", fmt.Errorf("failed to update asset: %w", err)
	}
	aictx.NotifyDataChanged("asset")
	return fmt.Sprintf(`{"id":%d,"message":"asset updated successfully"}`, asset.ID), nil
}

// handlePutGroup 创建或更新分组：带 id → 更新，不带 → 创建。
// 分组用数字 id 而非名称——仓内没有 groupref 解析器，get_group / list_groups 一直是数字 id。
func handlePutGroup(ctx context.Context, args map[string]any) (string, error) {
	id := aictx.ArgInt64(args, "id")
	if id == 0 {
		name := aictx.ArgString(args, "name")
		if name == "" {
			return "", fmt.Errorf("missing required parameter: name (creating a group needs a name; pass id=<group id> instead to update an existing one)")
		}
		now := time.Now().Unix()
		group := &group_entity.Group{
			Name:        name,
			ParentID:    aictx.ArgInt64(args, "parent_id"),
			Icon:        aictx.ArgString(args, "icon"),
			Description: aictx.ArgString(args, "description"),
			SortOrder:   aictx.ArgInt(args, "sort_order"),
			Createtime:  now,
			Updatetime:  now,
		}
		if err := group_repo.Group().Create(ctx, group); err != nil {
			return "", fmt.Errorf("failed to create group: %w", err)
		}
		aictx.NotifyDataChanged("group")
		return fmt.Sprintf(`{"id":%d,"message":"group created successfully"}`, group.ID), nil
	}

	group, err := group_repo.Group().Find(ctx, id)
	if err != nil {
		return "", fmt.Errorf("group not found: %w", err)
	}
	if name := aictx.ArgString(args, "name"); name != "" {
		group.Name = name
	}
	// 仅接受正整数：避免误传 parent_id=0 把分组悄悄变成顶级。
	if pid := aictx.ArgInt64(args, "parent_id"); pid > 0 {
		group.ParentID = pid
	}
	if _, ok := args["icon"]; ok {
		group.Icon = aictx.ArgString(args, "icon")
	}
	if _, ok := args["description"]; ok {
		group.Description = aictx.ArgString(args, "description")
	}
	if _, ok := args["sort_order"]; ok {
		group.SortOrder = aictx.ArgInt(args, "sort_order")
	}
	group.Updatetime = time.Now().Unix()
	if err := group_repo.Group().Update(ctx, group); err != nil {
		return "", fmt.Errorf("failed to update group: %w", err)
	}
	aictx.NotifyDataChanged("group")
	return fmt.Sprintf(`{"id":%d,"message":"group updated successfully"}`, group.ID), nil
}
```

同时从 `tool_handlers_asset.go` **删除** `handleAddAsset`(193-232)、`handleUpdateAsset`(234-271)、
`handleAddGroup`(323-343)、`handleUpdateGroup`(345-376)。

- [ ] **Step 4: 写工具定义**

创建 `internal/ai/tool/tools_crud.go`（Task 5 会往同一个函数里追加两个 delete 工具）：

```go
package tool

import (
	"context"
	"strings"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"

	"github.com/opskat/opskat/internal/ai/permission"
)

// crudTools 资产/分组的写工具。put_* 合并了旧的 add_*/update_*：分支由标识的有无决定，
// 而不是由两个几乎同构的工具承担。config 是自由对象——它按类型的形状由 help 文档说明
// （同一份文档同时服务 exec / put_asset / help），校验回到 assettype.ValidateCreateArgs。
func crudTools() []tool.Tool {
	return []tool.Tool{
		&tool.RawTool{
			NameStr: "put_asset",
			DescStr: "Create or update an asset. Pass asset=<id-or-name> to update an existing one; omit it to create. " +
				"The per-type shape of `config` is documented by help(asset) — call help against any asset of that type " +
				"(supported types: " + strings.Join(permission.RegisteredHelpTypes(), ", ") + "). " +
				"Credentials inside config (password / private_key) are stored encrypted; never echo them back to the user.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset":       {Type: "string", Description: "Existing asset id or name to update. Omit to create a new asset."},
					"name":        {Type: "string", Description: "Display name. Required when creating."},
					"type":        {Type: "string", Description: `Asset type when creating (defaults to "ssh"). When updating, this is an assertion — the type of an existing asset cannot be changed.`},
					"group_id":    {Type: "number", Description: "Group ID to assign this asset to. Values <= 0 are ignored."},
					"description": {Type: "string", Description: "Description or notes. Empty string clears it."},
					"icon":        {Type: "string", Description: "Icon name."},
					"config":      {Type: "object", Description: "Type-specific connection fields (host, port, username, credentials, …). Call help(asset) for the exact field list of a given type."},
				},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handlePutAsset(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "put_group",
			DescStr: "Create or update an asset group. Pass id to update an existing group; omit it to create. Groups nest via parent_id.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"id":          {Type: "number", Description: "Existing group ID to update. Omit to create."},
					"name":        {Type: "string", Description: "Display name. Required when creating."},
					"parent_id":   {Type: "number", Description: "Parent group ID for nesting. Values <= 0 are ignored."},
					"icon":        {Type: "string", Description: "Icon name. Empty string clears it."},
					"description": {Type: "string", Description: "Description. Empty string clears it."},
					"sort_order":  {Type: "number", Description: "Sort order within the parent; lower comes first."},
				},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handlePutGroup(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
	}
}
```

`internal/ai/tool/tools.go` 追加一行 `tools = append(tools, crudTools()...)`；
`tools_asset.go` 删掉 `add_asset`/`update_asset`/`add_group`/`update_group` 四个定义
（连同那张 40 属性的 schema）。

- [ ] **Step 5: 同步派发表与清单，并在同一个 commit 里修好 opsctl 的调用点**

- `tool_registry.go` 的 `AllToolDefs()`：`{"add_asset", handleAddAsset}` / `{"update_asset", handleUpdateAsset}` /
  `{"add_group", handleAddGroup}` / `{"update_group", handleUpdateGroup}`
  → `{"put_asset", handlePutAsset}` / `{"put_group", handlePutGroup}`。
- `tools_test.go` 的 `expected`：四个旧名 → 两个新名（数量断言会立刻抓到漏改）。
- **`cmd/opsctl/command/create.go:148,224`**：`callHandler(..., "add_asset", ...)` /
  `callHandler(..., "update_asset", ...)` → 均改为 `"put_asset"`，且**更新分支的 args 键
  `"id"` 必须同时改成 `"asset"`**（`handlePutAsset` 认的是 `asset`；只改工具名不改键名，
  更新会静默变成创建）。传的值仍是 `strconv.FormatInt(id, 10)` —— `assetref.Resolve` 认数字串。
- **`cmd/opsctl/command/handler.go:69-80`** 的桌面端刷新白名单
  `toolName == "add_asset" || toolName == "update_asset"` → `put_asset` / `put_group`。
- `cmd/opsctl/command/handler_test.go:112` 的清单加 `"put_asset"`、`"put_group"`。

> **为什么这四处必须留在本 task，而不是等到 Task 10/11 的 opsctl 批次**：
> `buildHandlerMap` 是**按名字的运行期查表**（查不到只打印 `Internal error: unknown tool`），
> 而 `handler_test.go:112` 的既有清单**不含** `add_asset`——也就是说，把它们留到后面，
> 中间每一个 task 期间 `opsctl create asset` 都是坏的，且**没有任何测试会红**。
> 这正是本仓 Plan B 反复吃过的那类静默失效。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/ai/tool/ -v`
Expected: PASS

- [ ] **Step 7: 变异验证**

把 `handlePutAsset` 里的 `if ref == ""` 改成 `if ref != ""`（分支反转），重跑
`go test ./internal/ai/tool/ -run TestHandlePutAsset_CreateThenUpdate`。
Expected: FAIL（第二次调用会创建第二行，`assetCount() != 1`）——证明「同一个工具的两个分支」
真被锁住。还原。

- [ ] **Step 8: 提交**

```bash
git add internal/ai/tool/tools_crud.go internal/ai/tool/tool_handlers_crud.go \
        internal/ai/tool/tool_handlers_crud_test.go internal/ai/tool/tools_asset.go \
        internal/ai/tool/tool_handlers_asset.go internal/ai/tool/tools.go \
        internal/ai/tool/tool_registry.go internal/ai/tool/tools_test.go
git commit -m "✨ add_*/update_* 合并为 put_asset/put_group，删掉 40 属性的巨型 schema"
```

---

## Task 5: `delete_asset` / `delete_group`（恒需确认、不可 grant、删前取名）

spec §4.4。删除比表面更危险：`asset_repo.Delete` 是软删除，但会把 `config` 与 `command_policy`
**清空为 `""`**，仓内没有任何恢复路径——实质不可逆。凭据静默孤儿化、grant item 仍指向已删资产。
（spec 原文说的「在用会话不会关闭」已经在 `fix/exec-convergence-followups` 修掉：
`asset_svc.Delete` 会 `assetconn.CloseAsset`，`group_svc.Delete` 在事务提交后逐个补广播。
工具描述**应当**说明连接会被断开。）

「不可 grant」的实现方式是**把放行分支写不出来**（#249 的修法同款）：不走
`permission.CheckPermission`（那条路径会查策略与 grant），而是直接调 `checker.ConfirmFunc()`。
策略/grant 根本没有机会参与。

**Files:**
- Modify: `internal/ai/tool/tools_crud.go`（追加两个工具）
- Modify: `internal/ai/tool/tool_handlers_crud.go`（追加两个 handler）
- Modify: `internal/ai/tool/tool_registry.go`、`internal/ai/tool/tools_test.go`
- Modify: `internal/ai/audit/extractor_default.go`（两个 delete 的命令摘要）
- Test: `internal/ai/tool/tool_handlers_crud_test.go`（追加）

**Interfaces:**
- Produces:
  - `delete_asset(asset: string)` / `delete_group(id: number, delete_assets?: boolean)`
  - `func handleDeleteAsset(ctx, args) (string, error)` / `func handleDeleteGroup(ctx, args) (string, error)`
  - 审批 kind 常量 `permission.ApprovalKindDelete = "delete"`（Task 6 的前端按它渲染）
- Consumes: `permission.RequireChecker`、`checker.ConfirmFunc()`、`asset_svc.Asset().Delete`、
  `group_svc.Group().Delete(ctx, id, deleteAssets)`

- [ ] **Step 1: 写失败测试**

追加到 `internal/ai/tool/tool_handlers_crud_test.go`：

```go
// 删除恒需确认：即使策略里有 allow * 的 grant，也必须弹确认。
// 实现方式决定了这一点——delete 根本不查策略/grant，直接调 ConfirmFunc。
func TestHandleDeleteAsset_AlwaysConfirmsAndIsNotGrantable(t *testing.T) {
	env := setupCRUD(t)
	env.grantEverything() // 往策略里塞一条 allow * ——对 delete 必须无效

	env.confirmDecision = "allow"
	if _, err := handleDeleteAsset(env.ctx, map[string]any{"asset": "web-9"}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if env.confirmCalls != 1 {
		t.Fatalf("delete must always prompt exactly once, got %d prompts", env.confirmCalls)
	}
	if env.policyChecks != 0 {
		t.Errorf("delete must not consult policy/grant at all, got %d checks", env.policyChecks)
	}
	if env.assetCount() != 0 {
		t.Errorf("asset should be gone, %d left", env.assetCount())
	}
}

// 用户拒绝 → 不删，且不报 Go error（模型据此自纠，而不是整轮中断）。
func TestHandleDeleteAsset_DenyKeepsTheAsset(t *testing.T) {
	env := setupCRUD(t)
	env.confirmDecision = "deny"

	out, err := handleDeleteAsset(env.ctx, map[string]any{"asset": "web-9"})
	if err != nil {
		t.Fatalf("a user denial is not a tool error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "denied") {
		t.Errorf("result must tell the model it was denied: %q", out)
	}
	if env.assetCount() != 1 {
		t.Error("denied delete must keep the asset")
	}
}

// 审批项必须带上资产名与删除语义，审批弹窗上"删哪台"要一眼可见。
func TestHandleDeleteAsset_ApprovalItemNamesTheAsset(t *testing.T) {
	env := setupCRUD(t)
	env.confirmDecision = "allow"

	_, _ = handleDeleteAsset(env.ctx, map[string]any{"asset": "web-9"})

	if env.lastConfirmKind != "delete" {
		t.Errorf("approval kind = %q, want %q (the frontend renders this one without an allow-all button)",
			env.lastConfirmKind, "delete")
	}
	item := env.lastConfirmItems[0]
	if item.AssetName != "web-9" {
		t.Errorf("approval item asset name = %q, want web-9", item.AssetName)
	}
	if !strings.Contains(item.Command, "delete") {
		t.Errorf("approval item must read as a delete, got %q", item.Command)
	}
}

// delete_group 默认非破坏性分支：资产移入未分组，不删。
func TestHandleDeleteGroup_DefaultsToMovingAssetsOut(t *testing.T) {
	env := setupCRUD(t)
	env.confirmDecision = "allow"

	if _, err := handleDeleteGroup(env.ctx, map[string]any{"id": float64(env.group("prod").ID)}); err != nil {
		t.Fatalf("delete_group failed: %v", err)
	}
	if env.assetCount() == 0 {
		t.Error("delete_assets defaults to false — the group's assets must survive")
	}
	if env.groupCount() != 0 {
		t.Error("the group itself should be gone")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/tool/ -run TestHandleDelete -v`
Expected: FAIL —— `undefined: handleDeleteAsset`

- [ ] **Step 3: 实现 handler**

追加到 `internal/ai/tool/tool_handlers_crud.go`：

```go
// handleDeleteAsset 删除资产。
//
// 两条与其它工具都不同的规则（spec §4.4）：
//
//  1. **恒需确认**：不查策略、不查 grant，直接调 ConfirmFunc。这不是"检查后发现需要确认"，
//     而是"根本没有可以放行的分支"——与 #249 的修法同款：把放行写不出来，比补一个
//     `if !grantable` 判断更难被后来的改动绕过。
//  2. **不可 grant**："删除这个资产"不是可预批的重复命令模式。既然从不经过策略层，
//     grant 也就无从匹配。前端必须同步用 delete 这个 kind 渲染（不出现"全部允许"按钮），
//     否则 UI 上仍然可以把删除写进 grant，把这条约束当场架空。
//
// 名称在删除**之前**捕获：asset_repo.Find 过滤 status = Active，删完再查就查不到了，
// 而审计中间件（runner.resolveAssetForAudit）本身就在 c.Next() 之前解析 args["asset"]，
// 所以审计行的归属不依赖这里——这里捕获是给审批弹窗与返回值用的。
func handleDeleteAsset(ctx context.Context, args map[string]any) (string, error) {
	asset, err := assetref.Resolve(ctx, aictx.ArgString(args, "asset"))
	if err != nil {
		return "", err
	}

	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		// 没有确认回调 = 没有人能点头。删除不存在"无人值守也放行"的形态。
		return "", fmt.Errorf("delete_asset requires an interactive approval channel, none is wired")
	}

	resp := confirm(ctx, permission.ApprovalKindDelete, []permission.ApprovalItem{{
		Type:      permission.ApprovalTypeDelete,
		AssetID:   asset.ID,
		AssetName: asset.Name,
		Command:   fmt.Sprintf("delete asset %q (type=%s)", asset.Name, asset.Type),
		Detail:    "The asset row is soft-deleted and its connection config is cleared — this cannot be undone from the app. Open sessions and pooled connections for this asset are closed.",
	}})
	aictx.RecordDecision(ctx, decisionFromApproval(resp))
	if resp.Decision == "deny" {
		return fmt.Sprintf("user denied deleting asset %q", asset.Name), nil
	}

	if err := asset_svc.Asset().Delete(ctx, asset.ID); err != nil {
		return "", fmt.Errorf("failed to delete asset: %w", err)
	}
	aictx.NotifyDataChanged("asset")
	return fmt.Sprintf(`{"id":%d,"name":%q,"message":"asset deleted"}`, asset.ID, asset.Name), nil
}

// handleDeleteGroup 删除分组。delete_assets 默认 false——那是非破坏性分支
// （分组内资产移入未分组）。true 时连带删除，group_svc 在事务提交之后逐个断连
// 并把被删资产回传，这里逐条写 delete_asset 审计（单删一台机器有审计行，
// 删一个含 20 台机器的分组却只有一行是审计盲区）。
func handleDeleteGroup(ctx context.Context, args map[string]any) (string, error) {
	id := aictx.ArgInt64(args, "id")
	if id == 0 {
		return "", fmt.Errorf("missing required parameter: id")
	}
	group, err := group_repo.Group().Find(ctx, id)
	if err != nil {
		return "", fmt.Errorf("group not found: %w", err)
	}
	deleteAssets := aictx.ArgBool(args, "delete_assets")

	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		return "", fmt.Errorf("delete_group requires an interactive approval channel, none is wired")
	}

	command := fmt.Sprintf("delete group %q (assets move to ungrouped)", group.Name)
	detail := "Assets in this group are moved to ungrouped; nothing is deleted besides the group itself."
	if deleteAssets {
		command = fmt.Sprintf("delete group %q AND every asset in it", group.Name)
		detail = "Every asset in this group is soft-deleted and its connection config cleared — this cannot be undone from the app."
	}

	resp := confirm(ctx, permission.ApprovalKindDelete, []permission.ApprovalItem{{
		Type:      permission.ApprovalTypeDelete,
		GroupID:   group.ID,
		GroupName: group.Name,
		Command:   command,
		Detail:    detail,
	}})
	aictx.RecordDecision(ctx, decisionFromApproval(resp))
	if resp.Decision == "deny" {
		return fmt.Sprintf("user denied deleting group %q", group.Name), nil
	}

	deleted, err := group_svc.Group().Delete(ctx, id, deleteAssets)
	if err != nil {
		return "", fmt.Errorf("failed to delete group: %w", err)
	}
	for _, a := range deleted {
		auditDeletedAsset(ctx, a) // 连带删掉的资产逐条补 delete_asset 审计
	}
	aictx.NotifyDataChanged("group")
	return fmt.Sprintf(`{"id":%d,"name":%q,"deleted_assets":%d,"message":"group deleted"}`,
		group.ID, group.Name, len(deleted)), nil
}
```

`decisionFromApproval` 与 `auditDeletedAsset` 两个小辅助：前者把 `ApprovalResponse` 映射成
`aictx.CheckResult`（`allow`/`allowAll` → Allow + `SourceUserAllow`，`deny` → Deny + `SourceUserDeny`），
后者复用 `internal/app/system/asset.go:252` 已确立的「连带删除逐条写审计」写法——
**先去读那处，照它的字段与 source 写**，不要另起一套。

`permission` 侧补两个常量（放在 `approval.go`，`ApprovalItem` 的 `Type` 注释也要更新）：

```go
// ApprovalKindDelete 删除类审批。与 "single" 的区别是前端**不渲染** rememberMode
// （"全部允许"→写 grant）：删除不可 grant，UI 上出现那个按钮就等于把后端的约束架空。
const ApprovalKindDelete = "delete"

// ApprovalTypeDelete 删除审批项的类型标签，前端 TypeBadge 按它取图标。
const ApprovalTypeDelete = "delete"
```

- [ ] **Step 4: 工具定义与派发表**

`tools_crud.go` 的 `crudTools()` 追加：

```go
		&tool.RawTool{
			NameStr: "delete_asset",
			DescStr: "Delete an asset. This always asks the user for confirmation and can never be pre-approved via request_permission. " +
				"The row is soft-deleted and its connection config is cleared — it cannot be restored from the app. " +
				"Open sessions and pooled connections for this asset are closed. Credentials linked to it are left orphaned.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset": {Type: "string", Description: "Asset id or name to delete. Use list_assets to find it."},
				},
				Required: []string{"asset"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleDeleteAsset(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "delete_group",
			DescStr: "Delete an asset group. By default the group's assets are moved to ungrouped and survive. " +
				"Pass delete_assets=true to delete them too — that is irreversible from the app. " +
				"This always asks the user for confirmation and can never be pre-approved.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"id":            {Type: "number", Description: "Group ID to delete. Use list_groups to find it."},
					"delete_assets": {Type: "boolean", Description: "Delete the assets in this group as well. Defaults to false (they move to ungrouped)."},
				},
				Required: []string{"id"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleDeleteGroup(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
```

`AllToolDefs()` 追加 `{"delete_asset", handleDeleteAsset}` 与 `{"delete_group", handleDeleteGroup}`；
`tools_test.go` 的 `expected` 追加两个名字（此时清单应正好是 Global Constraints 里那 15 个）。

审计摘要提取器（`internal/ai/audit/extractor_default.go`）追加：

```go
	RegisterExtractor("delete_asset", func(a map[string]any) string {
		return "delete asset " + aictx.ArgString(a, "asset")
	})
	RegisterExtractor("delete_group", func(a map[string]any) string {
		s := "delete group " + aictx.ArgString(a, "id")
		if aictx.ArgBool(a, "delete_assets") {
			s += " (with assets)"
		}
		return s
	})
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/ai/tool/ ./internal/ai/audit/ -v`
Expected: PASS

- [ ] **Step 6: 变异验证（这是本 task 最重要的一步）**

把 `handleDeleteAsset` 里直调 `confirm(...)` 的那段换成
`permission.CheckPermission(ctx, asset.Type, asset.ID, "delete")` + 常规的 allow/deny 分支，
重跑 `go test ./internal/ai/tool/ -run TestHandleDeleteAsset_AlwaysConfirmsAndIsNotGrantable`。
Expected: FAIL（`env.grantEverything()` 会让它静默放行，`confirmCalls == 0`）——
证明「不可 grant」是被测试锁住的行为，而不只是注释里的承诺。还原。

- [ ] **Step 7: 提交**

```bash
git add internal/ai/tool/tools_crud.go internal/ai/tool/tool_handlers_crud.go \
        internal/ai/tool/tool_handlers_crud_test.go internal/ai/tool/tool_registry.go \
        internal/ai/tool/tools_test.go internal/ai/audit/extractor_default.go \
        internal/ai/permission/approval.go
git commit -m "✨ 新增 delete_asset/delete_group：恒需确认、不可 grant、删前捕获名称"
```

---

## Task 6: 前端——删除审批不可 grant + 五个新工具图标

后端不可 grant 的约束需要前端配合才成立：`ApprovalBlock.tsx` 对 `kind === "single"` 会渲染
rememberMode（「全部允许」→ 写 grant）。删除审批若沿用 `single`，UI 上就摆着一个
「以后自动批准删除」的按钮——后端的约束当场作废。

**Files:**
- Modify: `frontend/src/components/approval/ApprovalBlock.tsx`
- Modify: `frontend/src/components/ai/ToolBlock.tsx:17-27`
- Modify: `frontend/src/i18n/locales/{zh-CN,en}/common.json`
- Test: `frontend/src/__tests__/ApprovalBlock.test.tsx`（追加）

**Interfaces:**
- Consumes: 后端 `approval_request` 事件的 `kind === "delete"`、item 的 `type === "delete"`（Task 5）

- [ ] **Step 1: 写失败测试**

追加到 `frontend/src/__tests__/ApprovalBlock.test.tsx`（沿用该文件已有的 render 夹具）：

```tsx
it("删除审批不提供「全部允许」——删除不可 grant", () => {
  renderApproval({
    approvalKind: "delete",
    approvalItems: [
      { type: "delete", asset_id: 1, asset_name: "web-9", command: 'delete asset "web-9" (type=ssh)' },
    ],
  });

  expect(screen.getByTestId("ai-approval-block")).toHaveAttribute("data-approval-kind", "delete");
  // 「记住」开关是通往 allowAll 的唯一入口，删除审批必须没有它
  expect(screen.queryByTestId("ai-approval-remember")).not.toBeInTheDocument();
  expect(screen.queryByText(/allow all|全部允许/i)).not.toBeInTheDocument();
});
```

（若既有测试文件没有 `ai-approval-remember` 这个 testid，本 task 顺手给 rememberMode 的开关补上
——它是这条断言唯一稳定的抓手，按文案匹配会随 i18n 漂移。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && pnpm test -- ApprovalBlock`
Expected: FAIL —— 当前 `kind === "delete"` 会走 `single` 之外的分支、标题落到默认值，
且 rememberMode 开关仍然渲染

- [ ] **Step 3: 实现**

`ApprovalBlock.tsx`：

1. 标题分支追加 `kind === "delete" ? t("ai.approvalDeleteTitle") : ...`。
2. rememberMode 的渲染与开关按钮**只在 `kind === "single" || kind === "local_tool"` 时出现**
   （现在的 `{kind === "single" && rememberMode && ...}` 已经满足前半条，
   要改的是那个「记住」开关本身的渲染条件，确保 `delete` 拿不到 allowAll 入口）。
3. `TypeBadge` 的 icons 表补 `delete: Trash2`（`lucide-react` 已在依赖里）。

`ToolBlock.tsx:17-27` 的 `toolIcons` 补五条：

```tsx
  put_asset: FilePen,
  put_group: FilePen,
  delete_asset: Trash2,
  delete_group: Trash2,
  ext_exec: Puzzle,
```

i18n 两份 `common.json` 补 `ai.approvalDeleteTitle`（zh-CN：`确认删除`；en：`Confirm deletion`）。
**不要逐字互译**——两侧各用地道表达即可。

- [ ] **Step 4: 跑测试与 lint**

Run: `cd frontend && pnpm test && pnpm lint`
Expected: PASS / 0 warning（lint 全 error 门禁）

- [ ] **Step 5: 变异验证**

把 rememberMode 开关的渲染条件改回无条件，重跑 `pnpm test -- ApprovalBlock`。
Expected: FAIL。还原。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/components/approval/ApprovalBlock.tsx \
        frontend/src/components/ai/ToolBlock.tsx \
        frontend/src/__tests__/ApprovalBlock.test.tsx \
        frontend/src/i18n/locales/zh-CN/common.json frontend/src/i18n/locales/en/common.json
git commit -m "✨ 删除审批用独立 kind 渲染，不提供「全部允许」入口"
```

---

## Task 7: `pkg/skillmd` 共享 frontmatter 解析 + 扩展 SKILL.md 取消 4 KiB 上限

spec §3.3。`pkg/extension` 加载的 SKILL.md 是内置 skill 格式的退化版：裸字符串、无 frontmatter、
**4 KiB 硬上限**（`manager.go:292-299`），超限直接 `return nil, err` 让整个扩展加载失败。

而 `internal/ai/skills` 里已经有一份合规解析器（`skills.go:46-70` 的 `parseFrontmatter`），
但它是**包级私有**且位于 `internal/`——`pkg/extension` 反向导入 `internal/` 是层级倒挂。
因此把解析器提到 `pkg/skillmd`，两侧共用一份，这是「Reuse first」的直接应用，
也顺手修掉现有解析器只认 `description`、不认 `name` 的缺口。

> **跨仓依赖（必须先做）**：`../extensions/extensions/oss/SKILL.md` **没有 frontmatter**
> （首行是 `# OSS Object Storage`）。严格解析上线后它会加载失败（`Scan` 路径只记一条 Warn，
> 静默程度高）。本 task 的 Step 6 要求先在 extensions 仓补上 frontmatter 并提交，
> 再验证本仓的加载路径。**顺序反了就会得到一个"扩展悄悄消失"的现场。**

**Files:**
- Create: `pkg/skillmd/skillmd.go`
- Create: `pkg/skillmd/skillmd_test.go`
- Modify: `internal/ai/skills/skills.go`（删私有解析器，改调 `pkg/skillmd`）
- Modify: `pkg/extension/manager.go:292-299`（删上限，改解析）
- Modify: `pkg/extension/manager_test.go`（新增 SKILL.md 用例——现在**零覆盖**）
- 跨仓：`../extensions/extensions/oss/SKILL.md`

**Interfaces:**
- Produces:
  - `type skillmd.Skill struct { Name, Description, Body string }`
  - `func skillmd.Parse(raw string) (Skill, error)` — 缺 frontmatter / 缺 description 都是 error
- Consumes: 无（纯字符串处理，不引入 YAML 依赖——格式是我们自己写的，键值对形态固定）

- [ ] **Step 1: 写失败测试**

创建 `pkg/skillmd/skillmd_test.go`：

```go
package skillmd

import "testing"

func TestParse(t *testing.T) {
	raw := "---\nname: ssh\ndescription: \"Run shell commands over SSH.\"\n---\n\n# SSH\n\nbody text\n"
	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Name != "ssh" {
		t.Errorf("Name = %q, want ssh", s.Name)
	}
	if s.Description != "Run shell commands over SSH." {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Body != "# SSH\n\nbody text\n" {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParse_CRLF(t *testing.T) {
	raw := "---\r\nname: x\r\ndescription: d\r\n---\r\nbody\r\n"
	if _, err := Parse(raw); err != nil {
		t.Fatalf("CRLF input must parse: %v", err)
	}
}

func TestParse_Rejects(t *testing.T) {
	cases := map[string]string{
		"no frontmatter":     "# Just a heading\n",
		"unterminated":       "---\nname: x\n",
		"no description":     "---\nname: x\n---\nbody\n",
		"empty":              "",
	}
	for label, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("%s: expected an error, got nil", label)
		}
	}
}

// name 缺失是允许的（内置 skill 的目录名/扩展名才是权威标识），
// description 缺失不允许——它是 prompt 里那份清单的唯一内容来源。
func TestParse_NameIsOptional(t *testing.T) {
	if _, err := Parse("---\ndescription: d\n---\nbody\n"); err != nil {
		t.Errorf("missing name must be tolerated: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/skillmd/ -v`
Expected: FAIL（包不存在）

- [ ] **Step 3: 实现 `pkg/skillmd`**

把 `internal/ai/skills/skills.go:46-70` 的解析器**整体搬过来**（逐字保留 CRLF 归一化、
分隔符严格性、`strings.Trim(v, "\"")` 的解引号行为），扩展成同时取 `name`，并导出：

```go
// Package skillmd 解析 cago skill 格式的 SKILL.md：`---` 包裹的 YAML 头 + Markdown 正文。
// 内置资产类型文档（internal/ai/skills）与扩展文档（pkg/extension）共用本包——
// 此前两侧各有一套：内置的是私有函数，扩展侧压根不解析（裸字符串 + 4 KiB 上限）。
//
// 不引入 YAML 依赖：格式是我们自己写的，键值对形态固定，且严格性本身是特性——
// 解析不了要响亮失败，而不是退化成一段进了 prompt 的原始 frontmatter 噪音。
package skillmd

import (
	"fmt"
	"strings"
)

// Skill 是一份解析后的 SKILL.md。Name 可选（内置侧的目录名、扩展侧的扩展名才是权威
// 标识），Description 必填——它是 prompt 里那份类型清单的唯一内容来源，缺了就等于
// 这份文档对模型不可发现。
type Skill struct {
	Name        string
	Description string
	Body        string
}

func Parse(raw string) (Skill, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return Skill{}, fmt.Errorf("missing frontmatter opening delimiter")
	}
	rest := raw[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return Skill{}, fmt.Errorf("missing frontmatter closing delimiter")
	}
	head := rest[:end]

	s := Skill{Body: strings.TrimLeft(rest[end+len("\n---\n"):], "\n")}
	for _, line := range strings.Split(head, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "name":
			s.Name = value
		case "description":
			s.Description = value
		}
	}
	if s.Description == "" {
		return Skill{}, fmt.Errorf("frontmatter has no description")
	}
	return s, nil
}
```

> 与被它取代的 `skills.parseFrontmatter` 逐字一致的部分：CRLF 归一化、
> `---\n` 开头 / `\n---\n` 闭合的严格性、`strings.Trim(v, "\"")` 的解引号行为
> （两端各剥任意数量的双引号，不处理转义、不处理多行 YAML）。
> **唯一的行为扩展**是同时取 `name`——旧实现把除 `description` 外的所有键 `continue` 掉。

- [ ] **Step 4: 内置 skills 改调共享解析**

`internal/ai/skills/skills.go`：删掉私有 `parseFrontmatter`，`init()` 改调 `skillmd.Parse`。
`registry` 的值类型可继续用本地 `skill` 结构，也可直接存 `skillmd.Skill`——取后者更少一层。
`Get` / `Description` / `Types` 的对外签名**不变**。

Run: `go test ./internal/ai/skills/ ./internal/ai/execimpl/`
Expected: PASS（8 + 4 份 SKILL.md 全部仍能解析）

- [ ] **Step 5: 扩展侧删上限、改解析**

`pkg/extension/manager.go:292-299` 替换为：

```go
	// SKILL.md 可选；一旦存在就必须是合规的 cago skill 格式。
	//
	// 此前这里有一个 4 KiB 硬上限，理由是整份正文会进系统提示词。上限是错的解法：
	// 它把"文档写长了"变成"整个扩展加载失败"，而真正该做的是解析 frontmatter、
	// 让 description 进清单、正文只在相关 Tab 打开时注入（bridge → chat.go → prompt_builder）。
	// 上限去掉，严格性移到格式上：解析失败响亮失败，不再把原始 frontmatter 当正文塞进 prompt。
	skillMD := ""
	if data, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err == nil { //nolint:gosec // path constructed from trusted extension directory
		parsed, perr := skillmd.Parse(string(data))
		if perr != nil {
			return nil, fmt.Errorf("SKILL.md: %w", perr)
		}
		skillMD = parsed.Body
	}
```

`Extension` 结构体增加 `SkillDescription string` 字段（存 `parsed.Description`），
`Bridge` 的 `SkillMDWithExtension` 同步带上——它是后续把扩展文档也做成
「清单 + 按需展开」的抓手；本 task 只存不消费，**不改注入行为**
（spec 提到的 `references/` 渐进披露没有加载器也没有消费者，YAGNI，本 Plan 不做）。

`manager.go:352-358` 的 `ScanManifests` 现在连 Warn 都没有——补一条 `m.logger.Warn`，
否则加固后的校验在这条路径上完全静默（recon 的 B.4 结论）。

- [ ] **Step 6: 跨仓：给 oss 扩展补 frontmatter**

在 `/Users/codfrm/Code/opskat/extensions`（**独立 git 仓，独立提交**）把
`extensions/oss/SKILL.md` 首部改为：

```markdown
---
name: oss
description: "Object storage buckets and objects via ext_exec. Covers listing, upload/download, copy/move, delete, and presigned URLs."
---

# OSS Object Storage
...
```

- [ ] **Step 7: 补扩展侧 SKILL.md 测试（此前零覆盖）**

`pkg/extension/manager_test.go` 追加：合规 SKILL.md 被解析且 `Extension.SkillMD` 只含正文；
无 SKILL.md 的扩展照常加载；**无 frontmatter 的 SKILL.md 让 `LoadExtension` 报错**
（这条正是被删掉的 4 KiB 上限所在位置的新契约）；一份 6 KiB 的合规 SKILL.md 能加载
（锁住上限确实没了）。

- [ ] **Step 8: 跑测试**

Run: `go test ./pkg/... ./internal/ai/skills/ -v`
Expected: PASS

- [ ] **Step 9: 变异验证**

把 Step 5 里的 `return nil, fmt.Errorf("SKILL.md: %w", perr)` 改成忽略错误、
`skillMD = string(data)`（即回到旧行为），重跑 `go test ./pkg/extension/`。
Expected: FAIL（无 frontmatter 那条用例）。还原。

- [ ] **Step 10: 提交**

```bash
git add pkg/skillmd/ pkg/extension/manager.go pkg/extension/manager_test.go \
        pkg/extension/bridge.go internal/ai/skills/skills.go
git commit -m "♻️ SKILL.md frontmatter 解析提取为 pkg/skillmd，扩展侧取消 4 KiB 上限"
```

---

## Task 8: manifest `tools[].parameters` 校验加固 + Bridge 透出 `ToolDef`

spec §4.5 的陷阱条款：`manifest.validate()`（`manifest.go:291-328`）**完全没有触碰 `m.Tools`**，
而 `ToolDef.Parameters`（`manifest.go:161`）在整个 Go 侧是**纯死字段**——
grep 全仓 `\.Parameters` 零命中。Task 9 会把它提升为承重契约（flag DSL 靠它做类型转换），
所以必须在**同一批**改动里加固校验：坏 manifest 要在加载期响亮失败，
而不是退化成用户看不懂的运行期解析错误。

同时 `Bridge.toolIndex`（`bridge.go:110-113`）只存 `toolName → *Extension`，**丢弃了 `ToolDef`**，
handler 拿不到 schema。这是 Task 9 的前置接线缺口。

**Files:**
- Modify: `pkg/extension/manifest.go`（`validate()` + 新增 `validateTools()`）
- Modify: `pkg/extension/manifest_test.go`
- Modify: `pkg/extension/bridge.go`（`toolIndex` 存 `ToolDef`，新增 `FindToolDef`）
- Modify: `pkg/extension/bridge_test.go`

**Interfaces:**
- Produces:
  - `func (b *Bridge) FindToolDef(extName, toolName string) (ToolDef, bool)`
- Consumes: 无新增

- [ ] **Step 1: 写失败测试**

`pkg/extension/manifest_test.go` 追加（注意：`138-330` 那批 fixture 用的是**不含 `tools`** 的
manifest，空 `Tools` 切片必须继续通过，否则一次红一大片）：

```go
func TestParseManifest_ToolsValidation(t *testing.T) {
	base := `{"name":"x","version":"1.0.0","hostABI":"1.0"`

	t.Run("没有 tools 仍然合法", func(t *testing.T) {
		if _, err := ParseManifest([]byte(base + `}`)); err != nil {
			t.Fatalf("a manifest without tools must stay valid: %v", err)
		}
	})

	t.Run("缺 parameters 被拒", func(t *testing.T) {
		_, err := ParseManifest([]byte(base + `,"tools":[{"name":"t"}]}`))
		if err == nil || !strings.Contains(err.Error(), "parameters") {
			t.Fatalf("missing parameters must be rejected by name, got %v", err)
		}
	})

	t.Run("parameters 不是 object 被拒", func(t *testing.T) {
		_, err := ParseManifest([]byte(base + `,"tools":[{"name":"t","parameters":{"type":"array"}}]}`))
		if err == nil {
			t.Fatal(`parameters.type must be "object"`)
		}
	})

	t.Run("属性缺 type 被拒", func(t *testing.T) {
		_, err := ParseManifest([]byte(base +
			`,"tools":[{"name":"t","parameters":{"type":"object","properties":{"k":{"description":"no type"}}}}]}`))
		if err == nil || !strings.Contains(err.Error(), "k") {
			t.Fatalf("a property without a type must be rejected and named, got %v", err)
		}
	})

	t.Run("required 引用不存在的属性被拒", func(t *testing.T) {
		_, err := ParseManifest([]byte(base +
			`,"tools":[{"name":"t","parameters":{"type":"object","properties":{},"required":["ghost"]}}]}`))
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("a dangling required entry must be rejected, got %v", err)
		}
	})

	t.Run("tool 重名被拒", func(t *testing.T) {
		one := `{"name":"t","parameters":{"type":"object","properties":{}}}`
		_, err := ParseManifest([]byte(base + `,"tools":[` + one + `,` + one + `]}`))
		if err == nil {
			t.Fatal("duplicate tool names must be rejected: toolIndex would silently keep only one")
		}
	})

	t.Run("真实 manifest 形状全部通过", func(t *testing.T) {
		// 覆盖 oss 用到的三种类型：string / integer / array<string>，含空 properties。
		ok := base + `,"tools":[
			{"name":"list_buckets","parameters":{"type":"object","properties":{}}},
			{"name":"list_objects","parameters":{"type":"object","properties":{"maxKeys":{"type":"integer"}}}},
			{"name":"delete_objects","parameters":{"type":"object","properties":{"keys":{"type":"array","items":{"type":"string"}}},"required":["keys"]}}
		]}`
		if _, err := ParseManifest([]byte(ok)); err != nil {
			t.Fatalf("the shapes used by the real oss manifest must pass: %v", err)
		}
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/extension/ -run TestParseManifest_ToolsValidation -v`
Expected: FAIL（除「没有 tools」与「真实形状」两条外全部失败——`validate()` 从不看 `Tools`）

- [ ] **Step 3: 实现 `validateTools()`**

`pkg/extension/manifest.go`，在 `validate()` 里 `validateSnippets()` 之前调用：

```go
// supportedParamTypes 是 flag DSL 能表达的参数类型。
// 现存两个真实 manifest 只用到 string / integer / array<string>；
// number / boolean 一并支持（它们的转换是同一形状），object 不支持——
// 嵌套结构走 ext_exec 的 --json 逃生口，而不是发明一套嵌套 flag 语法。
var supportedParamTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true, "array": true,
}

// validateTools 校验 tools[].parameters。
//
// 这个字段在本次改动之前**从未被校验、也从未被任何代码读过**——ext_exec 的 flag DSL
// 是它的第一个消费者。把一个从未被行使的字段提升为承重契约，就必须同时给它加载期校验：
// 否则一份漏写 parameters 的 manifest 会安静装上，直到某天模型调用它时，
// 用户收到的是一条读起来像解析器 bug 的运行期错误。
func (m *Manifest) validateTools() error {
	seen := make(map[string]bool, len(m.Tools))
	for i, t := range m.Tools {
		if t.Name == "" {
			return fmt.Errorf("manifest: tools[%d].name is required", i)
		}
		if seen[t.Name] {
			// Bridge.toolIndex 是 map，重名会静默只留一个——那时丢的是哪个取决于顺序。
			return fmt.Errorf("manifest: duplicate tool name %q", t.Name)
		}
		seen[t.Name] = true

		if t.Parameters == nil {
			return fmt.Errorf("manifest: tools[%q].parameters is required (use {\"type\":\"object\",\"properties\":{}} for a no-arg tool)", t.Name)
		}
		if typ, _ := t.Parameters["type"].(string); typ != "object" {
			return fmt.Errorf("manifest: tools[%q].parameters.type must be \"object\", got %q", t.Name, typ)
		}
		props, ok := t.Parameters["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("manifest: tools[%q].parameters.properties must be an object", t.Name)
		}
		for name, raw := range props {
			prop, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("manifest: tools[%q].parameters.properties.%s must be an object", t.Name, name)
			}
			typ, _ := prop["type"].(string)
			if typ == "" {
				return fmt.Errorf("manifest: tools[%q].parameters.properties.%s has no type", t.Name, name)
			}
			if !supportedParamTypes[typ] {
				return fmt.Errorf("manifest: tools[%q].parameters.properties.%s has unsupported type %q (supported: string, integer, number, boolean, array)", t.Name, name, typ)
			}
			if typ == "array" {
				items, ok := prop["items"].(map[string]any)
				if !ok {
					return fmt.Errorf("manifest: tools[%q].parameters.properties.%s is an array without items", t.Name, name)
				}
				if it, _ := items["type"].(string); it != "string" {
					return fmt.Errorf("manifest: tools[%q].parameters.properties.%s: only array<string> is supported, got array<%s>", t.Name, name, it)
				}
			}
		}
		if req, ok := t.Parameters["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, exists := props[name]; !exists {
					return fmt.Errorf("manifest: tools[%q].parameters.required references undeclared property %q", t.Name, name)
				}
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: `Bridge` 保留 `ToolDef`**

`pkg/extension/bridge.go`：

```go
	toolIndex map[string]map[string]toolEntry // extName → toolName → 扩展 + 该工具的声明

// toolEntry 同时持有扩展与工具声明。此前这里只存 *Extension，把 ToolDef 丢掉了，
// 于是 ext_exec 想按 parameters 做类型转换时无从查起。
type toolEntry struct {
	ext  *Extension
	tool ToolDef
}
```

`Register`（110-113）相应改写；`FindExtensionByTool` 行为不变（从 `toolEntry.ext` 取），
新增：

```go
// FindToolDef 返回某扩展某工具的参数声明，供 ext_exec 把 flag 串按声明类型转成 JSON。
func (b *Bridge) FindToolDef(extName, toolName string) (ToolDef, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	tools, ok := b.toolIndex[extName]
	if !ok {
		return ToolDef{}, false
	}
	entry, ok := tools[toolName]
	if !ok {
		return ToolDef{}, false
	}
	return entry.tool, true
}
```

`bridge_test.go` 的 fixture（`21-23`，`Tools: []ToolDef{{Name: "list_buckets", ...}}`，
`Parameters` 为 nil）**不受影响**——新校验在 `ParseManifest` 层，不在 `Register` 层；
但要新增一条 `FindToolDef` 的断言（含未知扩展/未知工具返回 false）。

- [ ] **Step 5: 跑测试**

Run: `go test ./pkg/extension/ -v`
Expected: PASS

- [ ] **Step 6: 用真实 manifest 验一遍（不是只验合成 fixture）**

```bash
go test ./pkg/extension/ -run TestParseManifest -v
go run ./cmd/opsctl ext list   # 若本机装了 oss 扩展，应正常列出而非静默消失
```

- [ ] **Step 7: 变异验证**

从 `validateTools` 里删掉「属性缺 type」那条检查，重跑 `go test ./pkg/extension/`。
Expected: FAIL。还原。

- [ ] **Step 8: 提交**

```bash
git add pkg/extension/manifest.go pkg/extension/manifest_test.go \
        pkg/extension/bridge.go pkg/extension/bridge_test.go
git commit -m "✨ manifest 加载期校验 tools[].parameters，Bridge 透出 ToolDef"
```

---

## Task 9: `exec_tool` → `ext_exec(asset, command)`

spec §4.5。现状（recon 实测，与 spec 原文有出入）：工具是
**`exec_tool(extension, tool, args, asset_id)` 四参**，`args` 是自由对象且
`args["args"].(map[string]any)` 断言失败时被静默丢弃（非对象 args 变成 `null` 传给 WASM）。

新形态：`ext_exec(asset, command)`，`command` 为 `<extension> <tool> --flag value`，
与 `exec` 的「首 token 即操作」一致；`--json '{...}'` 覆盖 flag DSL 表达不了的形状。
切词**复用 `internal/ai/cmdline`**（Plan B Task 1 建的引号感知 tokenizer：
`cmdline.Parse` 直接给出 `Verb` / `Args` / `Flags`），不新写一个。

**`ext_exec` 保持与 `exec` 分离**——策略路径确实不同（WASM `Plugin.CheckPolicy` 往返 +
`CheckExtensionPolicy`，且并非所有扩展都是资产范围的）。

> **实施状态更新（2026-07-23）：** 本段原先记录的三条扩展 fail-open 已在当前分支关闭。
> `ExecuteExtensionTool` 在调用期先按 manifest 校验参数；无 policy/action 时发起不可 grant
> 的逐次审批，`CheckPolicy` 错误直接拒绝；AI 与 opsctl 委托路径都调用这一共享 seam。
> 下方步骤保留为当时的实施记录，不应再按旧行号或旧控制流判断当前安全语义。

**Files:**
- Modify: `internal/ai/tool/tools_ext.go`（全文）
- Modify: `internal/ai/tool/tool_handler_ext.go`（参数解析段 28-50，其余逻辑不动）
- Create: `internal/ai/tool/ext_command.go`（command → `(extName, toolName, argsJSON)`）
- Create: `internal/ai/tool/ext_command_test.go`
- Modify: `internal/ai/tool/exec_tool_handler_test.go`（四条用例全部重写）
- Modify: `internal/ai/tool/tool_registry.go`、`internal/ai/tool/tools_test.go:36,66`
- Modify: `internal/ai/audit/extractor_default.go:26-28`
- Modify: `internal/ai/runner/system_template.go:15`
- Modify: `cmd/opsctl/command/handler_test.go:112`

**Interfaces:**
- Produces:
  - `func parseExtCommand(command string, def extension.ToolDef) (extName, toolName string, argsJSON []byte, err error)`
  - `ExtensionToolExecutor` 接口新增 `FindToolDef(extName, toolName string) (extension.ToolDef, bool)`
- Consumes: `cmdline.Parse`（既有）、`Bridge.FindToolDef`（Task 8）

- [ ] **Step 1: 写失败测试（命令解析）**

创建 `internal/ai/tool/ext_command_test.go`：

```go
func TestParseExtCommand(t *testing.T) {
	def := extension.ToolDef{Name: "list_objects", Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bucket":  map[string]any{"type": "string"},
			"maxKeys": map[string]any{"type": "integer"},
			"keys":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"force":   map[string]any{"type": "boolean"},
		},
	}}

	t.Run("按声明类型转换", func(t *testing.T) {
		ext, tool, argsJSON, err := parseExtCommand(`oss list_objects --bucket my-bucket --maxKeys 100 --force`, def)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if ext != "oss" || tool != "list_objects" {
			t.Fatalf("got (%q, %q), want (oss, list_objects)", ext, tool)
		}
		var got map[string]any
		if err := json.Unmarshal(argsJSON, &got); err != nil {
			t.Fatalf("args must be valid JSON: %v", err)
		}
		// integer 必须是数字而不是字符串——WASM 侧按 schema 解码，"100" 会解失败
		if got["maxKeys"] != float64(100) {
			t.Errorf("maxKeys = %#v, want 100 (number, not string)", got["maxKeys"])
		}
		if got["force"] != true {
			t.Errorf("bare boolean flag = %#v, want true", got["force"])
		}
		if got["bucket"] != "my-bucket" {
			t.Errorf("bucket = %#v", got["bucket"])
		}
	})

	t.Run("array<string> 按逗号切分", func(t *testing.T) {
		_, _, argsJSON, err := parseExtCommand(`oss list_objects --keys a,b,c`, def)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		_ = json.Unmarshal(argsJSON, &got)
		keys, ok := got["keys"].([]any)
		if !ok || len(keys) != 3 {
			t.Fatalf("keys = %#v, want a 3-element array", got["keys"])
		}
	})

	t.Run("--json 逃生口整体接管", func(t *testing.T) {
		_, _, argsJSON, err := parseExtCommand(`oss list_objects --json {"bucket":"b","nested":{"k":1}}`, def)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		_ = json.Unmarshal(argsJSON, &got)
		if got["nested"] == nil {
			t.Error("--json must pass through shapes the flag DSL cannot express")
		}
	})

	t.Run("未声明的 flag 报错并点名", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --nope 1`, def)
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("an undeclared flag must be named in the error, got %v", err)
		}
	})

	t.Run("类型不符报错并点名类型", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --maxKeys abc`, def)
		if err == nil || !strings.Contains(err.Error(), "integer") {
			t.Fatalf("a bad integer must say so, got %v", err)
		}
	})

	t.Run("缺少工具名报错", func(t *testing.T) {
		if _, _, _, err := parseExtCommand(`oss`, def); err == nil {
			t.Fatal("a command without a tool name must fail")
		}
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/tool/ -run TestParseExtCommand -v`
Expected: FAIL —— `undefined: parseExtCommand`

- [ ] **Step 3: 实现命令解析**

创建 `internal/ai/tool/ext_command.go`：

```go
package tool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/pkg/extension"
)

// parseExtCommand 把 `<extension> <tool> --flag value` 解析成扩展名、工具名与
// 按 manifest 声明类型转换过的 JSON 参数。
//
// 切词复用 internal/ai/cmdline（引号感知、拒绝 shell 控制结构），与 exec 侧同一份实现——
// 引号处理写第二遍必然会漂移，Plan B Task 1 的三轮评审（黑名单漏字符导致静默截断、
// Render 不引用 Verb、手抄的保留字表漏 8 个词）已经证明这块很难一次写对。
//
// --json 是逃生口：flag DSL 表达不了嵌套结构，而 manifest 允许声明它们；
// 没有逃生口就会出现"注册了却调不动"的工具。它与其它 flag 互斥——两者混用时
// 哪个赢都是猜，不如直接报错。
func parseExtCommand(command string, def extension.ToolDef) (string, string, []byte, error) {
	c, err := cmdline.Parse(command)
	if err != nil {
		return "", "", nil, fmt.Errorf("ext_exec: %w", err)
	}
	if len(c.Args) == 0 {
		return "", "", nil, fmt.Errorf("ext_exec: command %q names an extension but no tool; use `<extension> <tool> [--flags]`", command)
	}
	extName, toolName := c.Verb, c.Args[0]
	if len(c.Args) > 1 {
		// 位置参数无处可去：manifest 的 parameters 只有具名属性。静默丢弃会让模型
		// 以为传进去了。
		return "", "", nil, fmt.Errorf("ext_exec: unexpected positional argument %q; extension tools take named flags only", c.Args[1])
	}

	if raw, ok := c.Flags["json"]; ok {
		if len(c.Flags) > 1 {
			return "", "", nil, fmt.Errorf("ext_exec: --json cannot be combined with other flags")
		}
		if !json.Valid([]byte(raw)) {
			return "", "", nil, fmt.Errorf("ext_exec: --json value is not valid JSON")
		}
		return extName, toolName, []byte(raw), nil
	}

	props, _ := def.Parameters["properties"].(map[string]any)
	args := make(map[string]any, len(c.Flags))
	for name, raw := range c.Flags {
		prop, ok := props[name].(map[string]any)
		if !ok {
			return "", "", nil, fmt.Errorf("ext_exec: %s.%s has no parameter %q (declared: %s)",
				extName, toolName, name, strings.Join(declaredParamNames(props), ", "))
		}
		typ, _ := prop["type"].(string)
		value, cerr := convertExtFlag(raw, typ)
		if cerr != nil {
			return "", "", nil, fmt.Errorf("ext_exec: --%s: %w", name, cerr)
		}
		args[name] = value
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", "", nil, fmt.Errorf("ext_exec: marshal args: %w", err)
	}
	return extName, toolName, argsJSON, nil
}

// convertExtFlag 按 manifest 声明的类型转换 flag 值。类型不是可选的——
// pkg/extension 的加载期校验（manifest.validateTools）保证每个属性都带 type，
// 所以这里的 default 分支只可能被将来新增的类型触发，报错而非静默透传字符串。
func convertExtFlag(raw, typ string) (any, error) {
	switch typ {
	case "string":
		return raw, nil
	case "integer":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return f, nil
	case "boolean":
		// 裸 `--flag` 经 cmdline.Parse 得到值 "true"，所以无需特判。
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean", raw)
		}
		return b, nil
	case "array":
		// 加载期校验已保证只有 array<string>。逗号切分是 CLI 惯例；
		// 值里真需要逗号时走 --json。
		return strings.Split(raw, ","), nil
	default:
		return nil, fmt.Errorf("unsupported parameter type %q", typ)
	}
}

func declaredParamNames(props map[string]any) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
```

（`sort` 记得进 import。`declaredParamNames` 排序不是美观问题：报错文案进模型上下文，
map 迭代顺序随机会让同一个错误每次读起来都不一样。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ai/tool/ -run TestParseExtCommand -v`
Expected: PASS

- [ ] **Step 5: 改工具定义与 handler**

`tools_ext.go`：

```go
			NameStr: "ext_exec",
			DescStr: "Execute a tool exposed by an installed extension. command is `<extension> <tool> --flag value`, " +
				"the same shape as exec: the first token is the operation. Available extensions and their tools are described " +
				"in any 'From extension: <name>' section of this system prompt — read that section first. " +
				"Use --json '{...}' when a tool takes a nested structure that flags cannot express. " +
				"Pass asset when the extension is asset-scoped, so policy checks can run against that asset's group.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset":   {Type: "string", Description: "Target asset id or name. Required for asset-scoped extensions (those declaring a policy type)."},
					"command": {Type: "string", Description: `Extension command, e.g. "oss list_objects --bucket my-bucket --maxKeys 100".`},
				},
				Required: []string{"command"},
			},
```

`tool_handler_ext.go` 的参数段（28-50）改为：先 `cmdline` 取出扩展名与工具名
（此时还不知道 `ToolDef`，所以分两步：先只切出前两个 token 定位扩展，
再 `FindToolDef` 拿到声明、用 `parseExtCommand` 完整解析），
`asset_id` 改为经 `assetref.Resolve(args["asset"])` 得到——
**`assetref` 是仓内唯一的资产标识契约**，不要在这里另接一个数字 id 通道。
**`52-90` 的策略与执行段一行不改。**

`ExtensionToolExecutor` 接口加一个方法：

```go
type ExtensionToolExecutor interface {
	FindExtensionByTool(extName, toolName string) *extension.Extension
	FindToolDef(extName, toolName string) (extension.ToolDef, bool)
	GetExtensionPolicyGroups(extName, assetType string, assetID int64) []string
}
```

`*extension.Bridge` 因 Task 8 已实现该方法而继续隐式满足，
**`main.go:334` 无需改动**（注入点传的仍是 `b`）。

- [ ] **Step 6: 同步改名的六处**

| 位置 | 改动 |
|---|---|
| `tool_registry.go:59` | `{"exec_tool", handleExecTool}` → `{"ext_exec", handleExecTool}` |
| `tools_test.go:36,66` | `expected` 与 Serial 清单 |
| `audit/extractor_default.go:26-28` | 改读 `command`：`RegisterExtractor("ext_exec", func(a map[string]any) string { return aictx.ArgString(a, "command") })`。**不改会让审计写成一个孤零零的 `"."`** |
| `runner/system_template.go:15` | 提示词正文 `exec_tool` → `ext_exec` |
| `cmd/opsctl/command/handler_test.go:112` | 派发表名字断言 |
| `runner/tool_handler_ext_test.go:20,24` | 喂进去的 SKILL.md 字面量（不改不会红，但会留下误导文案） |

- [ ] **Step 7: 重写 handler 测试**

`exec_tool_handler_test.go` 的四条用例（`32-71`）全部改用 `{asset, command}`：
executor 为 nil、command 为空、command 只有一个 token（缺工具名）、扩展/工具不存在。
mock 补 `FindToolDef` 实现。

**顺带补上策略路径的首个测试**（recon G.1：`Policies.Type == ""` 跳过、`Deny`、`NeedConfirm`
目前**零覆盖**）——只加**不改行为**的表征测试，锁住现状，为将来修 fail-open 留下抓手。

- [ ] **Step 8: 跑全量**

Run: `go test ./internal/... ./cmd/... ./pkg/...` && `golangci-lint run`
Expected: 0 FAIL / 0 issue

- [ ] **Step 9: 变异验证**

把 `integer` 分支的转换改成原样传字符串，重跑 `go test ./internal/ai/tool/ -run TestParseExtCommand`。
Expected: FAIL（`maxKeys = "100"`）。还原。

- [ ] **Step 10: 提交**

```bash
git add internal/ai/tool/tools_ext.go internal/ai/tool/tool_handler_ext.go \
        internal/ai/tool/ext_command.go internal/ai/tool/ext_command_test.go \
        internal/ai/tool/exec_tool_handler_test.go internal/ai/tool/tool_registry.go \
        internal/ai/tool/tools_test.go internal/ai/audit/extractor_default.go \
        internal/ai/runner/system_template.go internal/ai/runner/tool_handler_ext_test.go \
        cmd/opsctl/command/handler_test.go
git commit -m "♻️ exec_tool 改为 ext_exec(asset, command)，flag DSL 按 manifest 声明类型转换"
```

---

## Task 10: `opsctl exec` 覆盖全部类型 + `--type`，删除 sql/redis/mongo 三个 verb

spec §6（含 2026-07-21 的实施期修正）。今天的 `opsctl exec` 是 **SSH 专用的流式通道**：
转发 stdin 管道、stdout/stderr 直写本地、透传远端 exit code（`exec.go:62-115`）。
统一 exec 的 handler 返回捕获后的字符串，**全量改道会打断已文档化的管道工作流**
（`cat config.yml | opsctl exec web-01 -- tee ...`）。

因此按资产真实类型分派：ssh 走现有流式路径，其余类型走 `exec` handler。
`--type` 是可选断言，用 Task 1 的同一个函数，**在 `requireApproval` 之前**校验。

**Files:**
- Modify: `cmd/opsctl/command/exec.go`
- Delete: `cmd/opsctl/command/db.go`（全 284 行）
- Modify: `cmd/opsctl/command/root.go:122-127`（三个 case）、`:144-208`（usage）
- Modify: `cmd/opsctl/command/approval.go:86-101,129-175`（offline 分支的类型枚举）
- Test: `cmd/opsctl/command/exec_test.go`（新建）

**Interfaces:**
- Consumes: `resolveAsset`（返回完整 entity，`asset.Type` 现成可用）、
  `permission.AssertAssetType`（Task 1）、`tool.AllToolDefs` 里的 `"exec"` handler、
  `permission.WithPreapproved`（`callHandler` 已在带审批结论时打标记）

- [ ] **Step 1: 写失败测试**

创建 `cmd/opsctl/command/exec_test.go`（沿用 `handler_test.go` 的 `mockAuditWriter` 与
handler map 注入方式）：

```go
// --type 与资产真实类型不符时必须在审批之前失败：不能让用户先批一条注定失败的命令。
func TestCmdExec_TypeAssertionFailsBeforeApproval(t *testing.T) {
	env := setupOpsctlExec(t) // 资产 cache-1 是 redis；approvalCalls 计数

	code := cmdExec(env.ctx, []string{"cache-1", "--type", "database", "--", "PING"}, "")
	if code == 0 {
		t.Fatal("a mismatched --type must fail")
	}
	if env.approvalCalls != 0 {
		t.Errorf("approval ran %d times; the assertion must short-circuit first", env.approvalCalls)
	}
}

// 非 ssh 资产改走统一 exec handler（此前只有 sql/redis/mongo 三个专用 verb 能碰它们）。
func TestCmdExec_NonSSHGoesThroughUnifiedHandler(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"

	code := cmdExec(env.ctx, []string{"cache-1", "--", "GET k"}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.handlerCalls["exec"] != 1 {
		t.Errorf("unified exec handler ran %d times, want 1", env.handlerCalls["exec"])
	}
	if env.sshStreamCalls != 0 {
		t.Errorf("a redis asset must not go down the SSH streaming path (%d calls)", env.sshStreamCalls)
	}
}

// ssh 资产仍走流式路径：管道与 exit code 透传是已文档化的行为。
func TestCmdExec_SSHKeepsStreamingPath(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"

	code := cmdExec(env.ctx, []string{"web-1", "--", "uptime"}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.sshStreamCalls != 1 {
		t.Errorf("ssh asset must keep the streaming path, got %d calls", env.sshStreamCalls)
	}
	if env.handlerCalls["exec"] != 0 {
		t.Errorf("ssh must not double-dispatch through the handler (%d calls)", env.handlerCalls["exec"])
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/opsctl/command/ -run TestCmdExec_ -v`
Expected: FAIL —— `--type` 未被识别（会被 `extractCommand` 当成命令的一部分）

- [ ] **Step 3: 实现分派与 `--type`**

`cmd/opsctl/command/exec.go`：在 `resolveAsset` 之后、`requireApproval` 之前插入
flag 解析与断言，然后按类型分派：

```go
	asset, err := resolveAsset(ctx, args[0])
	if err != nil { /* 原样 */ }

	// --type 是可选断言：不参与派发（协议永远来自 asset.Type），只把方言写错的情况
	// 提前成一条点名双方类型的错误。必须在 requireApproval 之前——它会去问桌面端，
	// 用户不该为一条注定失败的命令点头。
	declaredType, rest := extractTypeFlag(args[1:])
	if err := permission.AssertAssetType(asset, declaredType); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	command := extractCommand(rest)
```

审批通过之后：

```go
	// ssh 保留流式路径：stdin 管道、stdout/stderr 直写、远端 exit code 透传都是
	// 已文档化的行为（SKILL.md 里有 `cat config.yml | opsctl exec web-01 -- tee ...`
	// 这样的例子）。统一 exec 的 handler 返回的是捕获后的字符串，改道会静默打断它们。
	if asset.Type == asset_entity.AssetTypeSSH {
		return execSSHStreaming(ctx, auditCtx, asset, command, approvalResult)
	}
	// 其余类型走统一 exec handler：opsctl 由此获得 database/redis/mongodb/etcd/kafka/k8s
	// 的全部覆盖——此前只有 sql/redis/mongo 三个专用 verb，etcd/k8s/kafka 的 handler
	// 注册着却没有任何 verb 能抵达（spec §6 说的"已注册的死代码"）。
	return callHandler(ctx, handlers, "exec", map[string]any{
		"asset":   strconv.FormatInt(asset.ID, 10),
		"command": command,
	}, approvalResult.ToCheckResult())
```

注意 `cmdExec` 目前**不接收 `handlers`**（`root.go:114-115`），需要改签名并在 `root.go` 传入。
把现有的流式执行体抽成 `execSSHStreaming`（纯搬迁，逐字保留 proxy 快路径、
`helper.ExecWithStdio` 回落、`*ssh.ExitError` 的 exit code 提取与两处审计写入）。

- [ ] **Step 4: 删除三个 verb**

- `git rm cmd/opsctl/command/db.go`
- `root.go:122-127` 删三个 case。
- `root.go` 的 `printUsage()`：删 `155-157` 三行，改 `154`（exec 不再是 "via SSH"），
  改 `161`（batch 的类型说明），改 `171`（写操作清单），删示例 `198-200`。
- `approval.go:89` 的 offline 分支 case 列表 `"exec", "sql", "redis", "mongo"` → 只留 `"exec"`；
  `formatOfflineDenyMessage`（`129-175`）三处 `switch reqType` 里的 sql/redis/mongo 分支删掉。
  **不要顺手改文案措辞**——只删不再可达的分支。
- `root.go:101-104` 创建 SSH 池那段的注释「供 redis/sql 命令的 SSH 隧道使用」已失实
  （池仍需要，注释要改）——这是光标下的就地漂移修正，改。

- [ ] **Step 5: 跑测试**

Run: `go test ./cmd/... -v`
Expected: PASS。`batch_test.go:212-220` 的 `TestValidBatchTypes` **必须仍然绿**——
`validBatchTypes` 保留 sql/redis/mongo（前缀语法不变，Task 11 才改它的语义）。
若它红了，说明误删了 `validBatchTypes` 条目。

- [ ] **Step 6: 手动验证（可观测，不是断言）**

```bash
go build -o /tmp/opsctl ./cmd/opsctl
/tmp/opsctl help                         # 用法里不再有 sql/redis/mongo
/tmp/opsctl exec <某 redis 资产> -- PING  # 走统一 handler
/tmp/opsctl exec <某 ssh 资产> --type database -- uptime   # 断言失败，且**没有**弹审批
echo hi | /tmp/opsctl exec <某 ssh 资产> -- cat            # 管道仍然工作
```

读 `logs/opskat.log` 与 `opskat.db` 的 `audit_logs` 确认：断言失败那条**没有**审批记录。

- [ ] **Step 7: 变异验证**

把 Step 3 的断言挪到 `requireApproval` 之后，重跑
`go test ./cmd/opsctl/command/ -run TestCmdExec_TypeAssertionFailsBeforeApproval`。
Expected: FAIL。还原。

- [ ] **Step 8: 提交**

```bash
git add cmd/opsctl/command/exec.go cmd/opsctl/command/exec_test.go \
        cmd/opsctl/command/root.go cmd/opsctl/command/approval.go
git rm cmd/opsctl/command/db.go
git commit -m "♻️ opsctl exec 覆盖全部资产类型并支持 --type 断言，移除 sql/redis/mongo"
```

---

## Task 11: `opsctl help` / `delete` / `put` 派发 / batch 前缀断言

**Files:**
- Create: `cmd/opsctl/command/help.go`、`cmd/opsctl/command/delete.go`
- Modify: `cmd/opsctl/command/root.go`（早期 help 拦截、两个新 case、usage）
- Modify: `cmd/opsctl/command/create.go:148,224`（派发名）
- Modify: `cmd/opsctl/command/handler.go:69-80`（UI 刷新白名单）
- Modify: `cmd/opsctl/command/batch.go:87-125`（前缀变断言）
- Modify: `cmd/opsctl/command/{handler_test,batch_test}.go`

**Interfaces:**
- Consumes: `AllToolDefs` 里的 `"help"` / `"delete_asset"` / `"delete_group"` / `"put_asset"` / `"put_group"`
- Produces: `opsctl help <asset>`、`opsctl delete asset <id-or-name>`、`opsctl delete group <id> [--delete-assets]`

- [ ] **Step 1: 写失败测试**

追加到 `cmd/opsctl/command/handler_test.go` 与新建 `delete_test.go`：

```go
// 派发表必须含 opsctl 会按名字查的每一个 handler。这条断言是唯一能在**编译期之后、
// 运行期之前**发现"改了工具名忘了改调用点"的检查——查不到只会在运行时打印
// "Internal error: unknown tool"。
func TestBuildHandlerMap_HasEveryToolOpsctlLooksUp(t *testing.T) {
	m := buildHandlerMap()
	for _, name := range []string{
		"exec", "help", "ext_exec", "upload_file", "download_file",
		"put_asset", "put_group", "delete_asset", "delete_group",
	} {
		if _, ok := m[name]; !ok {
			t.Errorf("buildHandlerMap is missing %q", name)
		}
	}
}

// opsctl help <asset> 不能被"打印 CLI 用法"的早期分支吃掉。
// root.go 在 bootstrap **之前**就拦截了 verb == "help"，那条分支既拿不到数据库
// 也拿不到 handler map——带参数时必须落到后面的 case。
func TestRootHelpVerb_WithArgIsNotTheCLIUsage(t *testing.T) {
	if isCLIUsageHelp([]string{"help"}) != true {
		t.Error(`bare "opsctl help" must still print CLI usage`)
	}
	if isCLIUsageHelp([]string{"help", "web-1"}) != false {
		t.Error(`"opsctl help web-1" must reach the asset help verb, not the CLI usage screen`)
	}
	for _, flag := range []string{"-h", "--help"} {
		if isCLIUsageHelp([]string{flag}) != true {
			t.Errorf("%s must still print CLI usage", flag)
		}
	}
}

// 删除必须拿到桌面端确认。CLI 侧的 checker 走 WithPreapproved 豁免，
// 所以确认由 requireApproval 承担——漏了它，opsctl 就成了绕过
// "delete 恒需确认" 的后门。
func TestCmdDelete_RequiresApproval(t *testing.T) {
	env := setupOpsctlDelete(t) // 资产 web-9；approvalCalls / handlerCalls 计数
	env.approvalDecision = "allow"

	code := cmdDelete(env.ctx, env.handlers, []string{"asset", "web-9"}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.approvalCalls != 1 {
		t.Errorf("approval ran %d times, want exactly 1", env.approvalCalls)
	}
	if env.handlerCalls["delete_asset"] != 1 {
		t.Errorf("delete_asset handler ran %d times, want 1", env.handlerCalls["delete_asset"])
	}
}

// 用户拒绝时不得派发。
func TestCmdDelete_DenyDoesNotDispatch(t *testing.T) {
	env := setupOpsctlDelete(t)
	env.approvalDecision = "deny"

	if code := cmdDelete(env.ctx, env.handlers, []string{"asset", "web-9"}, ""); code == 0 {
		t.Error("a denied delete must exit non-zero")
	}
	if env.handlerCalls["delete_asset"] != 0 {
		t.Errorf("denied delete must not dispatch, got %d calls", env.handlerCalls["delete_asset"])
	}
}
```

（`isCLIUsageHelp(remaining []string) bool` 是 Step 3 要从 `root.go:73-76` 那段
内联条件里提出来的小函数——内联条件测不了，提出来才有抓手。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/opsctl/command/ -v`
Expected: FAIL（缺 put_*/delete_*；`help <asset>` 被 `root.go:73-76` 拦截）

- [ ] **Step 3: 实现**

1. **`root.go:73-76` 的早期 help 拦截加条件**：只有 `len(remaining) == 1` 时才打印 CLI 用法；
   带参数时落到 bootstrap 之后的新 `case "help": return cmdHelp(ctx, handlers, args)`。
   （bootstrap 与 `buildHandlerMap()` 都在 `79-99` 行，即那个早期分支之后——
   不调整顺序就拿不到 handler。）
2. **`help.go`**：`resolveAsset` → `callHandler(ctx, handlers, "help", {"asset": id})`。
   help 不改任何状态，**不需要审批**。
3. **`delete.go`**：`delete asset <id-or-name>` / `delete group <id> [--delete-assets]`，
   派发进 `delete_asset` / `delete_group`。审批由 handler 内部的 `ConfirmFunc` 负责——
   但 CLI 侧的 checker 是 `WithPreapproved` 形态（`callHandler` 带审批结论时打标记），
   **删除必须绕开这条豁免**：CLI 侧走 `requireApproval(approval.ApprovalRequest{Type: "delete", ...})`
   拿到桌面端确认，再以已预批的形态派发。这与 `create`/`update` 的既有形状一致
   （`create.go:139-148`），照它写，`Detail` 串按 `create.go:135-138` 的格式给出
   （`opsctl delete asset <ref>`）。
4. **`create.go` 的派发名已在 Task 4 改完**（连同 `handler.go` 的刷新白名单）。
   本 task 只补一件事：给 `cmdCreate` / `cmdUpdate` 的 `switch resource` 加 `group` 分支
   （recon B 节：`create group` 今天根本不存在，只有 `case "asset"` + 一个
   `unknown resource %q. Supported: asset` 的 default），派发进 `put_group`。
5. **`handler.go:69-80`** 的刷新白名单在 Task 4 已含 `put_asset`/`put_group`，
   本 task 追加 `delete_asset` / `delete_group`。
   **漏改这处不会有任何测试变红，只会让桌面端在 CLI 删数据后不刷新**——
   本 task 必须为它补一条断言。
6. **`batch.go:87-125`**：前缀语义从选择器变断言。`parseBatchArg`（`479-509`）与
   `validBatchTypes`（`65`）**一行不改**——前缀语法保持不变（用户明确要求）。
   改的是解析循环：`resolveAsset` 之后调 `permission.AssertAssetType(asset, cmd.cmdType)`，
   失败则该条目直接落 deny 桶（与解析失败同样处理，不中断整批）；
   `permission.CheckPermission` 的第一个实参从 `cmd.cmdType` 改成 `asset.Type`。
   `executeBatchItem` 的 switch（`246-289`）保持 `exec` 走流式、其余走 handler 的现状——
   但 `default` 分支不再是「不支持的类型」而是「没有前缀 = 按真实类型派发」：
   非 ssh 且无前缀的条目现在必须能跑。

- [ ] **Step 4: 跑测试**

Run: `go test ./cmd/... -v`
Expected: PASS，且 `batch_test.go` 的 `TestParseBatchArg` / `TestParseBatchInput` /
`TestValidBatchTypes` 三条**全部保持绿**（前缀语法未变）。

- [ ] **Step 5: 变异验证**

把 `handler.go` 的 UI 刷新白名单改回 `add_asset`/`update_asset`，重跑测试。
Expected: FAIL（Step 3-5 新增的断言）。还原。

- [ ] **Step 6: 提交**

```bash
git add cmd/opsctl/command/help.go cmd/opsctl/command/delete.go \
        cmd/opsctl/command/root.go cmd/opsctl/command/create.go \
        cmd/opsctl/command/handler.go cmd/opsctl/command/batch.go \
        cmd/opsctl/command/handler_test.go cmd/opsctl/command/batch_test.go \
        cmd/opsctl/command/delete_test.go
git commit -m "✨ opsctl 新增 help/delete verb，create|update 改派 put_*，batch 前缀改为类型断言"
```

---

## Task 12: 文档同步、prompt 清理与全量验证

**Files:**
- Modify: `plugin/opsctl/skills/opsctl/SKILL.md`（`49,52-60,75,119,126-128` 等，见下）
- Modify: `plugin/opsctl/skills/opsctl/references/commands.md`（删 `84-134` 三节，改 `## batch`/`## create`/`## update`/`## exec`，新增 `## help`/`## delete`）
- Modify: `internal/app/system/settings.go:966`（`pluginVersion` 升版，让已安装副本失效）
- Modify: `internal/ai/runner/prompt_builder.go` / `system_template.go`（工具名与能力清单）
- Modify: `docs/ARCHITECTURE.md`、`docs/DEVELOP.md`（若提到旧工具名）
- Modify: `e2e/`（`ai-exec` 系列补 put/delete 场景）
- Modify: `.superpowers/sdd/progress.md`（本 Plan 的执行 ledger）

- [ ] **Step 1: 全仓搜残留**

```bash
git grep -n "add_asset\|update_asset\|add_group\|update_group\|batch_command\|exec_tool" -- \
  ':!docs/superpowers' ':!.superpowers'
```

逐条分诊：代码/文档里的实际引用必须改；**说明「它们已被删除」的历史注释保留**
（Plan B 收尾时确立的判据：注释是否在陈述现状）。

- [ ] **Step 2: 改 opsctl 技能文档**

按 recon I 节的行号清单逐处改：`SKILL.md:75` 的 verb 索引（删 sql/redis/mongo，加 help/delete）、
`:53` 与 `:56-60` 的 batch 例子（前缀语法不变，但补一句「前缀是可选的类型断言」）、
`:119` 的 `opsctl sql` 例子改成 `opsctl exec`、`:126-128` 的 create 例子保持
（CLI 表面未变）。`references/commands.md` 删 `## sql`/`## redis`/`## mongo` 三节，
改 `## exec`（全类型 + `--type`）、`## batch`（`161` 那句"known types"的语义），
新增 `## help` 与 `## delete`。
**不要动 `SKILL.md:10` 的 `## Global Flags` 标题**——`settings.go:1018` 靠它做字符串插入。

`pluginVersion`（`settings.go:966`）从 `1.0.0` 升到 `1.1.0`：已安装到四个外部 CLI
（Claude Code / Codex / OpenCode / Gemini）的旧副本靠它失效，否则用户机器上还留着
教人用 `opsctl sql` 的文档。

- [ ] **Step 3: 清 prompt 里的旧工具名**

`system_template.go` 的能力清单与 `prompt_builder.go` 的工具指引里，
所有旧名换新名。**顺带核对 spec §1 的初衷**：prompt 里不得再出现按资产类型分派的路由表
（Plan B 已清理 `buildKnowledgeGuidance`，这里只做回归检查，`git grep -n "exec_sql\|exec_redis" internal/ai/runner/` 应为 0）。

- [ ] **Step 4: 补 e2e 场景**

`e2e/` 的 `ai-exec` 系列（2026-07-21 新增，脚本化 OpenAI mock）追加：
`put_asset` 创建 + 更新走同一个工具、`delete_asset` **弹审批且审批面板上没有「全部允许」按钮**
（这条是 Task 5/6 那条不变式的端到端验证，也是 GUI 侧唯一能自动化的部分）。

- [ ] **Step 5: 全量验证**

```bash
go build ./...
go test ./internal/... ./cmd/... ./pkg/...
golangci-lint run
cd frontend && pnpm test && pnpm lint
```
Expected: 全绿 / 0 issue

- [ ] **Step 6: 可观测验证（按 docs/VERIFICATION.md）**

桌面端跑一遍，读 `logs/opskat.log` 与 `opskat.db` 的 `audit_logs`：
- `put_asset` 建一台 ssh 资产 → `audit_logs` 有行，`asset_name` 非空；
- `help` 一个 rdp 资产 → 返回配置文档（doc-only 类型）；
- `exec` 该 rdp 资产 → 明确报 "no exec support yet"，且**不弹审批**；
- `delete_asset` → 弹确认，面板无「全部允许」；批准后资产消失、终端标签页断开；
- `batch_exec` 混合 ssh + redis 两条 → 一次审批弹窗、两条结果。

- [x] **Step 7: 扩展 fail-open 实施状态（2026-07-23 更新）**

原计划在 Task 9 结束时保留的三条风险已在当前分支关闭：`ExecuteExtensionTool` 对未声明
policy type 或 policy 未返回 action 的调用发起不可 grant 的逐次审批，`CheckPolicy` 错误
直接返回失败；AI 与 opsctl 委托路径都复用这一共享 seam，并在 policy/审批/plugin 之前执行
manifest 参数的调用期校验。不要再按旧行号创建“尚未修复”的 issue；应从当前实现和回归测试
重新判断。`../extensions/examples/echo/manifest.json` 属于独立 sibling repo，不在本仓当前分支
事实范围内。

- [ ] **Step 8: 提交并合回**

```bash
git add plugin/ internal/app/system/settings.go internal/ai/runner/ docs/ e2e/ .superpowers/sdd/progress.md
git commit -m "📝 Plan C 收尾：opsctl 技能文档、prompt 工具名与 e2e 场景同步"
git checkout feature/ai-tool-exec-foundation
git merge --no-ff feature/ai-tool-exec-crud
```

---

## 自检（写完计划后对照 spec 的一次通读）

| spec 条款 | 落点 |
|---|---|
| §4.1 `exec` 增 `type` | Task 1 |
| §4.2 `help` 门禁 | 无改动（Plan A 已实现）；Task 3 的 doc-only 类型经 `HelpFor` 复用同一门禁 |
| §4.3 `put_asset` / `put_group` | Task 4（标识形态偏离 spec 的 `id?`，理由写在 Task 4 开头） |
| §4.4 `delete_asset` / `delete_group` | Task 5 后端 + Task 6 前端（缺一不可） |
| §4.5 `ext_exec` + manifest 加固 | Task 8 + Task 9 |
| §4.6 类型断言（决策更新） | Task 1（AI exec）+ Task 2（batch_exec）+ Task 10/11（opsctl） |
| §3.3 扩展 SKILL.md 统一格式 | Task 7（`references/` 渐进披露**不做**，理由：无加载器无消费者，YAGNI） |
| §5 审批/审计不变式 | Task 1/2/5/10 各自的「早于审批」步骤 + 变异验证 |
| §6 opsctl | Task 10 + Task 11（`ssh` verb 保留，见 spec §6 实施期修正） |
| §7 前端 | Task 6 |
| §8 测试策略 | 各 task 的 TDD 步骤；新增齐备性测试见 Task 3 |

**已知偏离 spec 之处（均已在 spec 内就地标注）**：
`opsctl ssh` 保留、`opsctl exec` 按类型分派、`put_asset` 用 `asset` 而非 `id`、
`references/` 渐进披露不做。`ext_exec` 的三条 fail-open 已在上方 Step 7 的实施更新中关闭。
