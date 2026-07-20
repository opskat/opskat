# AI 工具面收敛 · Plan A：exec 基座 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立统一 `exec(asset, command)` 的基座——资产引用解析、按资产真实类型派发的执行器注册表、`help` 工具与用法门禁——并让 ssh / serial / database / redis / k8s 五个"原样透传"类型跑通。

**Architecture:** 派发方向反转：不再由模型选协议（`exec_sql` 等），而是先解析资产、读其真实 `Type`，再从注册表取执行器。注册表挂在既有的 `internal/ai/permission` 类型表上，但因 `helper → permission` 的导入方向，执行函数由下游包在 `init()` 中通过 `permission.RegisterExecutor` **推送**注册。用法文档采用仓内已有的 cago skill 格式（`plugin/opsctl/skills/opsctl/SKILL.md` 是范本），`//go:embed` 编入。

**Tech Stack:** Go 1.26、cago（`github.com/cago-frame/agents`）、goconvey（`. "github.com/smartystreets/goconvey/convey"`）、gomock（`go.uber.org/mock/gomock`）。

## Global Constraints

- 模块路径：`github.com/opskat/opskat`。
- **本 Plan 不删除任何旧工具**。`exec_sql` / `exec_redis` / `run_command` 等继续注册并可用；`exec` 与它们并存。删除动作在 Plan B（全部类型 parser 齐备后）执行。
- **本 Plan 不修既有审批漏洞**（`upload_file`/`download_file` 无检查、`if checker != nil` fail-open）。见 spec §2 非目标。
- 后端验证用 `golangci-lint run`，不用 `go vet`（见 `docs/DEVELOP.md`）。
- 测试命令统一 `go test ./internal/...`。
- 提交信息用 gitmoji；**仅在刻意关联 issue 时** subject 才带 `#123`。
- 仓内 mock 用法：`asset_repo.RegisterAsset(mock)` + `t.Cleanup` 还原，范本见 `internal/ai/testhelpers_test.go:41-63`。
- **模型面文本一律英文，不走 i18n**：SKILL.md、`exec` / `help` 的引导文本、unsupported 与解析错误，全部只写英文——与仓内现有 tool 文案（`"asset not found: %s"` 等）保持一致。模型会依 `buildLanguageHint` 用用户语言回复，无需在工具层翻译。本约束**不适用**于前端 UI 文案：那些仍需 en 与 zh-CN 双份，且各语言用地道表达、不逐字对译。

---

### Task 1: 资产引用解析器（name-or-id，同名必须报错）

`exec` / `help` / 未来的 `batch_exec` 都需要"数字 id 或名称"解析。当前仓内**不存在**任何 name→id 查询：`resolveAssetForBatch`（`internal/ai/tool/tool_handler_batch.go:221`）把字符串塞给 `handleGetAsset`，后者用 `aictx.ArgInt64` 取值，而 `ArgInt64` 没有 `string` 分支（`internal/ai/aictx/args.go:35-50`），恒返回 0 → `batch_command` 对**任何**引用都失败。本任务建立正确实现。

**Files:**
- Create: `internal/ai/assetref/resolve.go`
- Test: `internal/ai/assetref/resolve_test.go`

**Interfaces:**
- Consumes: `asset_svc.Asset().Get(ctx, id)`、`asset_svc.Asset().List(ctx, assetType, groupID)`
- Produces:
  - `func Resolve(ctx context.Context, ref string) (*asset_entity.Asset, error)`
  - `type ErrAmbiguous struct { Ref string; IDs []int64 }`，实现 `error`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/assetref/resolve_test.go`：

```go
package assetref

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

func setupRepo(t *testing.T) *mock_asset_repo.MockAssetRepo {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})
	return m
}

func TestResolve_NumericID(t *testing.T) {
	m := setupRepo(t)
	want := &asset_entity.Asset{ID: 42, Name: "web-1", Type: asset_entity.AssetTypeSSH}
	m.EXPECT().Find(gomock.Any(), int64(42)).Return(want, nil)

	got, err := Resolve(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("got id %d, want 42", got.ID)
	}
}

func TestResolve_ByName(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
		{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis},
		{ID: 8, Name: "web-1", Type: asset_entity.AssetTypeSSH},
	}, nil)

	got, err := Resolve(context.Background(), "web-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 8 {
		t.Fatalf("got id %d, want 8", got.ID)
	}
}

func TestResolve_AmbiguousNameIsError(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
		{ID: 3, Name: "db", Type: asset_entity.AssetTypeDatabase},
		{ID: 9, Name: "db", Type: asset_entity.AssetTypeDatabase},
	}, nil)

	_, err := Resolve(context.Background(), "db")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	amb, ok := err.(*ErrAmbiguous)
	if !ok {
		t.Fatalf("expected *ErrAmbiguous, got %T", err)
	}
	if len(amb.IDs) != 2 {
		t.Fatalf("got %d ids, want 2", len(amb.IDs))
	}
}

func TestResolve_NotFound(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{}, nil)

	if _, err := Resolve(context.Background(), "nope"); err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

func TestResolve_EmptyRef(t *testing.T) {
	setupRepo(t)
	if _, err := Resolve(context.Background(), "  "); err == nil {
		t.Fatal("expected error for empty ref, got nil")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/assetref/ -run TestResolve -v`
Expected: 编译失败，`undefined: Resolve`

- [ ] **Step 3: 写最小实现**

创建 `internal/ai/assetref/resolve.go`：

```go
// Package assetref 把 LLM 传入的资产标识（数字 id 或名称）解析为资产实体。
// exec / help / batch_exec 共用它，避免各自实现一遍 name→id 查询。
package assetref

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// ErrAmbiguous 在同名资产多于一个时返回。名称不是唯一键，静默取第一个会让模型
// 对着错误的机器执行命令，因此这里必须报错并要求改用数字 id。
type ErrAmbiguous struct {
	Ref string
	IDs []int64
}

func (e *ErrAmbiguous) Error() string {
	parts := make([]string, 0, len(e.IDs))
	for _, id := range e.IDs {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return fmt.Sprintf(
		"asset reference %q is ambiguous, it matches ids [%s]; use the numeric id instead",
		e.Ref, strings.Join(parts, ", "))
}

// Resolve 解析资产标识。纯数字按 id 查，否则按名称精确匹配。
func Resolve(ctx context.Context, ref string) (*asset_entity.Asset, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("missing required parameter: asset")
	}

	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		asset, err := asset_svc.Asset().Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("asset not found: %s", ref)
		}
		return asset, nil
	}

	assets, err := asset_svc.Asset().List(ctx, "", 0)
	if err != nil {
		return nil, err
	}

	var matched []*asset_entity.Asset
	for _, a := range assets {
		if a.Name == ref {
			matched = append(matched, a)
		}
	}

	switch len(matched) {
	case 0:
		return nil, fmt.Errorf("asset not found: %s", ref)
	case 1:
		return matched[0], nil
	default:
		ids := make([]int64, 0, len(matched))
		for _, a := range matched {
			ids = append(ids, a.ID)
		}
		return nil, &ErrAmbiguous{Ref: ref, IDs: ids}
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/assetref/ -v`
Expected: 5 个测试全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/ai/assetref/
git commit -m "✨ 新增资产引用解析器：支持 id/名称，同名报错"
```

---

### Task 2: 执行器注册表（推送式注册，避免导入环）

**Files:**
- Modify: `internal/ai/permission/type_registry.go`
- Test: `internal/ai/permission/exec_registry_test.go`

**Interfaces:**
- Produces:
  - `type ExecFunc func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)`
  - `func RegisterExecutor(canonical string, exec ExecFunc, help string)`
  - `func ExecutorFor(assetType string) (ExecFunc, bool)`
  - `func HelpFor(assetType string) (string, bool)`
  - `func RegisteredExecTypes() []string`（供 Task 9 的齐备性测试使用，返回已排序切片）

> **为什么是推送式**：`internal/ai/helper` 导入 `internal/ai/permission`（每个 helper 都调 `permission.GetPolicyChecker`），所以 `permission` 不能反向导入 `helper`。`permission` 只声明类型与注册入口，实现由下游包在 `init()` 中推上来。仓内既有同款惯例：`tool.SetExecToolExecutor`（`main.go:323`）。

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/permission/exec_registry_test.go`：

```go
package permission

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestRegisterExecutor_RoundTrip(t *testing.T) {
	const typeName = "test-exec-type"
	t.Cleanup(func() { delete(execEntries, typeName) })

	RegisterExecutor(typeName, func(_ context.Context, _ *asset_entity.Asset, cmd, _ string) (string, error) {
		return "ran:" + cmd, nil
	}, "usage doc")

	fn, ok := ExecutorFor(typeName)
	if !ok {
		t.Fatal("ExecutorFor returned false for a registered type")
	}
	out, err := fn(context.Background(), &asset_entity.Asset{}, "uptime", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "ran:uptime" {
		t.Fatalf("got %q, want %q", out, "ran:uptime")
	}

	help, ok := HelpFor(typeName)
	if !ok || help != "usage doc" {
		t.Fatalf("got help %q ok=%v", help, ok)
	}
}

func TestExecutorFor_UnknownType(t *testing.T) {
	if _, ok := ExecutorFor("definitely-not-registered"); ok {
		t.Fatal("ExecutorFor returned true for an unregistered type")
	}
}

func TestRegisterExecutor_DuplicatePanics(t *testing.T) {
	const typeName = "test-dup-type"
	t.Cleanup(func() { delete(execEntries, typeName) })

	noop := func(_ context.Context, _ *asset_entity.Asset, _, _ string) (string, error) { return "", nil }
	RegisterExecutor(typeName, noop, "doc")

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	RegisterExecutor(typeName, noop, "doc")
}

func TestRegisteredExecTypes_Sorted(t *testing.T) {
	got := RegisteredExecTypes()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("RegisteredExecTypes not sorted: %v", got)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/permission/ -run TestRegisterExecutor -v`
Expected: 编译失败，`undefined: RegisterExecutor`

- [ ] **Step 3: 写最小实现**

在 `internal/ai/permission/type_registry.go` 末尾追加（并把 `sort` 加进 import）：

```go
// --- 执行器注册表 ---
//
// 注册方向是自下而上推送：helper 等持有协议代码的包导入本包，
// 因此本包只声明类型与注册入口，实现由它们在 init() 中调 RegisterExecutor 推上来。

// ExecFunc 按资产真实类型执行一条命令。scope 是"不属于命令本身的连接级目标"
// （database 用库名、redis 用 db 序号），其余类型忽略。
type ExecFunc func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)

type execEntry struct {
	exec ExecFunc
	help string
}

var execEntries = make(map[string]*execEntry)

// RegisterExecutor 注册某资产类型的执行器与用法文档。重复注册 panic——
// 与 registerPermissionType 一致，注册冲突是启动期的编程错误，不该静默覆盖。
func RegisterExecutor(canonical string, exec ExecFunc, help string) {
	if canonical == "" || exec == nil {
		panic("permission: invalid executor registration")
	}
	if _, exists := execEntries[canonical]; exists {
		panic(fmt.Sprintf("permission: duplicate executor registration %q", canonical))
	}
	execEntries[canonical] = &execEntry{exec: exec, help: help}
}

// ExecutorFor 返回该资产类型的执行器。
func ExecutorFor(assetType string) (ExecFunc, bool) {
	entry, ok := execEntries[assetType]
	if !ok {
		return nil, false
	}
	return entry.exec, true
}

// HelpFor 返回该资产类型的用法文档。
func HelpFor(assetType string) (string, bool) {
	entry, ok := execEntries[assetType]
	if !ok {
		return "", false
	}
	return entry.help, true
}

// RegisteredExecTypes 返回已注册执行器的资产类型，已排序。
func RegisteredExecTypes() []string {
	types := make([]string, 0, len(execEntries))
	for name := range execEntries {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/permission/ -v`
Expected: 新增 4 个测试 PASS，既有测试不受影响

- [ ] **Step 5: 提交**

```bash
git add internal/ai/permission/
git commit -m "✨ 权限类型表新增执行器注册入口"
```

---

### Task 3: 五个原样透传类型的用法文档（cago skill 格式）

采用仓内已有格式，范本见 `plugin/opsctl/skills/opsctl/SKILL.md`：`---` frontmatter 带 `name` / `description`，正文为 Markdown。**不发明** `internal/ai/exec/docs/<type>.md` 这类新格式。

**Files:**
- Create: `internal/ai/skills/ssh/SKILL.md`、`serial/SKILL.md`、`database/SKILL.md`、`redis/SKILL.md`、`k8s/SKILL.md`
- Create: `internal/ai/skills/skills.go`
- Test: `internal/ai/skills/skills_test.go`

**Interfaces:**
- Produces:
  - `func Get(assetType string) (string, bool)` — 返回该类型 SKILL.md 全文
  - `func Description(assetType string) (string, bool)` — 返回 frontmatter 的 `description`
  - `func Types() []string` — 已排序

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/skills/skills_test.go`：

```go
package skills

import (
	"strings"
	"testing"
)

func TestGet_AllFiveTypesPresent(t *testing.T) {
	for _, at := range []string{"ssh", "serial", "database", "redis", "k8s"} {
		body, ok := Get(at)
		if !ok {
			t.Fatalf("no SKILL.md registered for %q", at)
		}
		if !strings.Contains(body, "## Command syntax") {
			t.Fatalf("%s SKILL.md missing '## Command syntax' section", at)
		}
	}
}

func TestDescription_ParsedFromFrontmatter(t *testing.T) {
	desc, ok := Description("redis")
	if !ok {
		t.Fatal("no description for redis")
	}
	if desc == "" {
		t.Fatal("redis description is empty")
	}
	if strings.Contains(desc, "---") {
		t.Fatalf("description still contains frontmatter delimiters: %q", desc)
	}
}

func TestGet_BodyExcludesFrontmatter(t *testing.T) {
	body, _ := Get("ssh")
	if strings.HasPrefix(strings.TrimSpace(body), "---") {
		t.Fatal("Get should return the body without frontmatter")
	}
}

func TestTypes_Sorted(t *testing.T) {
	got := Types()
	if len(got) != 5 {
		t.Fatalf("got %d types, want 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Types not sorted: %v", got)
		}
	}
}

func TestGet_UnknownType(t *testing.T) {
	if _, ok := Get("mongodb"); ok {
		t.Fatal("mongodb should not be registered in Plan A")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/skills/ -v`
Expected: 编译失败，`undefined: Get`

- [ ] **Step 3: 写 SKILL.md 文件**

创建 `internal/ai/skills/redis/SKILL.md`（其余四个照此结构写，内容按各自协议）：

```markdown
---
name: redis
description: "Run Redis commands against a Redis asset via exec. Covers command syntax, the db scope parameter, and why SELECT must not be used."
---

# Redis assets

## Command syntax

Pass the Redis command verbatim as `command`:

- `GET mykey`
- `HGETALL user:1`
- `SET key value EX 3600`
- `SCAN 0 MATCH prefix:* COUNT 100`

## Scope

Use the `scope` parameter to pick the database number (0-15), e.g. `scope: "3"`.

**Do NOT send `SELECT`.** Connections are pooled, so `SELECT` either has no
effect or corrupts another caller's database selection. `scope` is the only
correct way to switch databases.

## Notes

- Results are returned as JSON.
- Credentials are resolved automatically from the app's encrypted store; never
  ask the user for a password.
```

创建 `internal/ai/skills/ssh/SKILL.md`：

```markdown
---
name: ssh
description: "Run shell commands on a remote server over SSH via exec. Covers command syntax and the remote-vs-local distinction."
---

# SSH assets

## Command syntax

Pass the shell command verbatim as `command`:

- `uptime`
- `systemctl status nginx`
- `cat /etc/nginx/nginx.conf`
- `df -h | grep -v tmpfs`

## Notes

- The command runs on the **remote** server, never on the user's machine. Tools
  named `local_*` operate on the user's own machine and are not interchangeable
  with this one.
- Use `cat` / `ls` / `grep` inside the command to inspect remote files.
- The `scope` parameter is not used by SSH assets.
- Credentials are resolved automatically; never ask the user for a password.
```

创建 `internal/ai/skills/serial/SKILL.md`：

```markdown
---
name: serial
description: "Send commands to a serial console device (switch, firewall) via exec. Requires an already-connected desktop serial session."
---

# Serial assets

## Command syntax

Pass the console command verbatim as `command`:

- `display version`
- `show ip interface brief`
- `display current-configuration`

## Notes

- The serial session **must already be connected by the user** in a terminal
  tab. There is no way to open one from here.
- Output is collected until the line goes quiet (2s) or a 15s cap is reached.
- Serial assets are unavailable from `opsctl`; they require the desktop app.
- The `scope` parameter is not used by serial assets.
```

创建 `internal/ai/skills/database/SKILL.md`：

```markdown
---
name: database
description: "Run SQL against a database asset (MySQL, PostgreSQL, SQL Server, SQLite) via exec. Covers SQL syntax and the database scope parameter."
---

# Database assets

## Command syntax

Pass the SQL verbatim as `command`:

- `SELECT id, name FROM users LIMIT 10`
- `SHOW TABLES`
- `DESCRIBE orders`
- `UPDATE users SET active = 0 WHERE id = 3`

## Scope

Use `scope` to override the default database for this call, e.g. `scope: "analytics"`.

## Notes

- Reads (`SELECT` / `SHOW` / `DESCRIBE` / `EXPLAIN`) return rows as JSON;
  writes return an affected-row count.
- Multi-statement input is split and each statement is policy-checked
  separately, so a read cannot smuggle a write past approval.
- Credentials are resolved automatically; never ask the user for a password.
```

创建 `internal/ai/skills/k8s/SKILL.md`：

```markdown
---
name: k8s
description: "Run kubectl commands against a Kubernetes asset via exec. Covers command syntax and the stored kubeconfig / jump-host behavior."
---

# Kubernetes assets

## Command syntax

Pass the kubectl command as `command`, with or without the leading `kubectl`:

- `get pods -A`
- `describe pod api-0 -n prod`
- `logs deploy/api --tail 100`
- `kubectl get nodes -o wide`

## Notes

- The asset's stored kubeconfig is used automatically. **Do not pass
  `--kubeconfig`.**
- The asset's default context and namespace are applied when the command does
  not specify them.
- If the asset has an SSH jump host configured, kubectl runs on that host;
  otherwise it runs locally.
- The `scope` parameter is not used by Kubernetes assets.
```

- [ ] **Step 4: 写加载器**

创建 `internal/ai/skills/skills.go`：

```go
// Package skills 以 cago skill 格式（frontmatter + Markdown 正文）内嵌各资产类型的
// 用法文档。格式与 plugin/opsctl/skills/opsctl/SKILL.md 一致，不另造一套。
package skills

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

//go:embed */SKILL.md
var files embed.FS

type skill struct {
	description string
	body        string
}

var registry = map[string]skill{}

func init() {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(fmt.Sprintf("skills: read embedded dir: %v", err))
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := files.ReadFile(path.Join(entry.Name(), "SKILL.md"))
		if err != nil {
			panic(fmt.Sprintf("skills: read %s/SKILL.md: %v", entry.Name(), err))
		}
		desc, body, err := parseFrontmatter(string(raw))
		if err != nil {
			panic(fmt.Sprintf("skills: parse %s/SKILL.md: %v", entry.Name(), err))
		}
		registry[entry.Name()] = skill{description: desc, body: body}
	}
}

// parseFrontmatter 解析 `---` 包裹的 YAML 头，只取需要的 description 字段。
// 不引入 YAML 依赖：格式是我们自己写的，键值对形态固定。
func parseFrontmatter(raw string) (description, body string, err error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return "", "", fmt.Errorf("missing frontmatter opening delimiter")
	}
	rest := raw[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", "", fmt.Errorf("missing frontmatter closing delimiter")
	}
	head := rest[:end]
	body = strings.TrimLeft(rest[end+len("\n---\n"):], "\n")

	for _, line := range strings.Split(head, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "description" {
			continue
		}
		description = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if description == "" {
		return "", "", fmt.Errorf("frontmatter has no description")
	}
	return description, body, nil
}

// Get 返回该资产类型 SKILL.md 的正文（不含 frontmatter）。
func Get(assetType string) (string, bool) {
	s, ok := registry[assetType]
	return s.body, ok
}

// Description 返回 frontmatter 中的一行描述，用于 prompt 里的技能清单。
func Description(assetType string) (string, bool) {
	s, ok := registry[assetType]
	return s.description, ok
}

// Types 返回已内嵌文档的资产类型，已排序。
func Types() []string {
	types := make([]string, 0, len(registry))
	for name := range registry {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/ai/skills/ -v`
Expected: 5 个测试全部 PASS

- [ ] **Step 6: 提交**

```bash
git add internal/ai/skills/
git commit -m "✨ 内嵌五类资产的 exec 用法技能文档"
```

---

### Task 4: 用法门禁（按会话 × 资产类型记录"用法已知"）

**Files:**
- Create: `internal/ai/tool/exec_gate.go`
- Test: `internal/ai/tool/exec_gate_test.go`

**Interfaces:**
- Consumes: `aictx.GetConversationID(ctx)`（`internal/ai/aictx/context.go:36`）
- Produces:
  - `type DocGate struct{ ... }`
  - `func NewDocGate() *DocGate`
  - `func (g *DocGate) MarkDocumented(convID int64, assetType string)`
  - `func (g *DocGate) IsDocumented(convID int64, assetType string) bool`
  - `func (g *DocGate) Reset(convID int64)`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/tool/exec_gate_test.go`：

```go
package tool

import "testing"

func TestDocGate_UnmarkedTypeIsNotDocumented(t *testing.T) {
	g := NewDocGate()
	if g.IsDocumented(1, "redis") {
		t.Fatal("unmarked type reported as documented")
	}
}

func TestDocGate_MarkThenDocumented(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	if !g.IsDocumented(1, "redis") {
		t.Fatal("marked type reported as undocumented")
	}
}

func TestDocGate_ScopedPerType(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	if g.IsDocumented(1, "database") {
		t.Fatal("marking redis must not document database")
	}
}

func TestDocGate_ScopedPerConversation(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	if g.IsDocumented(2, "redis") {
		t.Fatal("marking conversation 1 must not document conversation 2")
	}
}

func TestDocGate_Reset(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	g.Reset(1)
	if g.IsDocumented(1, "redis") {
		t.Fatal("Reset did not clear the conversation")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/tool/ -run TestDocGate -v`
Expected: 编译失败，`undefined: NewDocGate`

- [ ] **Step 3: 写最小实现**

创建 `internal/ai/tool/exec_gate.go`：

```go
package tool

import "sync"

// DocGate 记录"某会话内，某资产类型的用法文档已经到过模型面前"。
// 满足条件有两条：模型显式调用过 help，或该类型文档已被注入本次 Send 的 system prompt。
// 生命周期与会话一致，与 LocalToolGate 的 allow-list 相同。
type DocGate struct {
	mu   sync.RWMutex
	seen map[int64]map[string]bool
}

func NewDocGate() *DocGate {
	return &DocGate{seen: make(map[int64]map[string]bool)}
}

// MarkDocumented 标记该会话已知晓该资产类型的用法。
func (g *DocGate) MarkDocumented(convID int64, assetType string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen[convID] == nil {
		g.seen[convID] = make(map[string]bool)
	}
	g.seen[convID][assetType] = true
}

// IsDocumented 查询该会话是否已知晓该资产类型的用法。
func (g *DocGate) IsDocumented(convID int64, assetType string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.seen[convID][assetType]
}

// Reset 清空某会话的记录。
func (g *DocGate) Reset(convID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.seen, convID)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/tool/ -run TestDocGate -v`
Expected: 5 个测试全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/ai/tool/exec_gate.go internal/ai/tool/exec_gate_test.go
git commit -m "✨ 新增 exec 用法门禁状态机"
```

---

### Task 5: 五个类型的执行器注册

把既有 helper 的执行部分包成 `permission.ExecFunc` 并注册。**不改动**既有 `HandleExecSQL` 等函数——它们仍被旧工具使用，Plan B 才删。

**Files:**
- Create: `internal/ai/execimpl/register.go`
- Test: `internal/ai/execimpl/register_test.go`

**Interfaces:**
- Consumes: `permission.RegisterExecutor`（Task 2）、`skills.Get`（Task 3）、既有 helper 执行函数
- Produces: 五个类型在 `permission` 表中的执行器条目；包被 import 即完成注册（`init()`）

> **注意执行函数的选取**：现有 helper 的 `HandleExecXxx(ctx, args)` 内部**自带权限检查**。本任务注册的 `ExecFunc` 必须是**不含权限检查的纯执行**部分，因为检查已由 Task 6 的 `exec` 工具在派发前用**资产真实类型**完成。重复检查会导致审批弹两次。
>
> 各类型的纯执行入口：
> - ssh：`helper` 中 `handleRunCommand` 调用的底层执行（`internal/ai/tool/tool_handlers_exec.go:100` 的 `runCommandWithCache`）
> - database：`helper.ExecuteSQL(ctx, db, sqlText)`（`database_helper.go` 末尾），需先经 `getOrDialDatabase`
> - redis / serial / k8s：同理，取各自 helper 中权限检查**之后**的那段
>
> 实现时先读对应 helper 文件，把"检查后"的部分抽成导出函数（如 `helper.ExecSQLOnAsset(ctx, asset, sql, scope string) (string, error)`），旧 handler 改为"检查 + 调用该函数"，从而两条路径共用同一执行体、不产生第二份实现。

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/execimpl/register_test.go`：

```go
package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestInit_RegistersFiveVerbatimTypes(t *testing.T) {
	want := []string{
		asset_entity.AssetTypeSSH,
		asset_entity.AssetTypeSerial,
		asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis,
		asset_entity.AssetTypeK8s,
	}
	for _, at := range want {
		if _, ok := permission.ExecutorFor(at); !ok {
			t.Fatalf("no executor registered for %q", at)
		}
	}
}

func TestInit_HelpDocAttachedForEachType(t *testing.T) {
	for _, at := range []string{
		asset_entity.AssetTypeSSH,
		asset_entity.AssetTypeSerial,
		asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis,
		asset_entity.AssetTypeK8s,
	} {
		help, ok := permission.HelpFor(at)
		if !ok || help == "" {
			t.Fatalf("no help doc registered for %q", at)
		}
	}
}

func TestInit_StructuredTypesNotYetRegistered(t *testing.T) {
	// Plan B 才补 mongo / etcd / kafka；此处锁定 Plan A 的边界。
	for _, at := range []string{
		asset_entity.AssetTypeMongoDB,
		asset_entity.AssetTypeEtcd,
		asset_entity.AssetTypeKafka,
	} {
		if _, ok := permission.ExecutorFor(at); ok {
			t.Fatalf("%q should not be registered in Plan A", at)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/execimpl/ -v`
Expected: 失败，`no executor registered for "ssh"`

- [ ] **Step 3: 抽出各 helper 的纯执行入口**

好消息：纯执行体**大多已经存在**——`helper.ExecuteRedis(ctx, client, command)`
（`redis_helper.go:122`）、`helper.ExecuteSQL(ctx, db, sqlText)`（`database_helper.go`）
都是权限检查之后的那段。缺的只是"资产 → 连接 → 执行"的组装函数。

以 redis 为例，在 `internal/ai/helper/redis_helper.go` 追加：

```go
// ExecRedisOnAsset 是不含权限检查的纯执行入口，供统一 exec 使用。
// HandleExecRedis 保留"检查 + 调用本函数"的形态，两条路径共用同一执行体。
func ExecRedisOnAsset(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
	cfg, err := asset.GetRedisConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get redis config: %w", err)
	}
	if scope != "" {
		dbIndex, err := strconv.Atoi(scope)
		if err != nil {
			return "", fmt.Errorf("scope must be a redis db number (0-15), got %q", scope)
		}
		cfg.DB = dbIndex
	}
	client, closer, err := getOrDialRedis(ctx, asset, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to connect to redis: %w", err)
	}
	if getRedisCache(ctx) == nil {
		defer closeRedisConn(client, closer)
	}
	return ExecuteRedis(ctx, client, command)
}
```

然后把 `HandleExecRedis`（`:49`）权限检查之后的部分替换为对 `ExecRedisOnAsset` 的调用，
**删掉重复的连接/执行代码**——留下两份是新的漂移源。

其余四个同构：
- database：`ExecSQLOnAsset(ctx, asset, sql, scope)`，`scope` 覆盖 `cfg.Database`，末尾调 `ExecuteSQL`
- ssh：`ExecCommandOnAsset(ctx, asset, command, _)`，复用 `tool_handlers_exec.go:100` 的 `runCommandWithCache`（需从 `tool` 包移入 `helper` 或导出）
- serial：`ExecSerialOnAsset(ctx, asset, command, _)`，复用 `serial_helper.go:29` 检查后的部分
- k8s：`ExecK8sOnAsset(ctx, asset, command, _)`，复用 `tool_handler_k8s.go:65` 检查后的部分

每抽一个就跑 `go test ./internal/ai/...` 确认既有测试仍通过，逐个提交。

- [ ] **Step 4: 写注册文件**

创建 `internal/ai/execimpl/register.go`，为五个类型各写一个 `permission.RegisterExecutor(...)` 调用，`help` 参数取 `skills.Get(<type>)`。示例（redis）：

```go
func init() {
	redisHelp, _ := skills.Get(asset_entity.AssetTypeRedis)
	permission.RegisterExecutor(asset_entity.AssetTypeRedis,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecRedisOnAsset(ctx, asset, command, scope)
		}, redisHelp)
	// ...其余四个同构
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/ai/... -v`
Expected: 新增 3 个测试 PASS，既有测试全绿

- [ ] **Step 6: 提交**

```bash
git add internal/ai/execimpl/ internal/ai/helper/
git commit -m "✨ 注册 ssh/serial/database/redis/k8s 的 exec 执行器"
```

---

### Task 6: `exec` 与 `help` 工具

**Files:**
- Create: `internal/ai/tool/tools_unified.go`
- Create: `internal/ai/tool/tool_handlers_unified.go`
- Test: `internal/ai/tool/tool_handlers_unified_test.go`
- Modify: `internal/ai/tool/tools.go:10-18`（把 `unifiedTools()` 加进 `Tools()`，并把过期的 `make([]tool.Tool, 0, 24)` 容量改为 `29`）

**Interfaces:**
- Consumes: `assetref.Resolve`、`permission.ExecutorFor` / `HelpFor`、`DocGate`
- Produces: `handleExec` / `handleHelp`，以及 `Tools()` 中的 `exec` / `help`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/tool/tool_handlers_unified_test.go`，覆盖四条关键行为：

```go
package tool

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

func setupUnified(t *testing.T) *mock_asset_repo.MockAssetRepo {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})
	return m
}

// 门禁未满足时必须返回引导文本，而不是 Go error——否则整轮会中断，模型无法自纠。
func TestHandleExec_UndocumentedTypeReturnsGuidance(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&asset_entity.Asset{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

	out, err := handleExec(context.Background(), map[string]any{
		"asset": "7", "command": "GET foo",
	})
	if err != nil {
		t.Fatalf("gate must not return a Go error, got %v", err)
	}
	if !strings.Contains(out, "help") {
		t.Fatalf("guidance should tell the model to call help, got %q", out)
	}
	// 引导语必须点出解析出的类型（spec §4.6）。
	if !strings.Contains(out, "redis") {
		t.Fatalf("guidance should name the resolved asset type, got %q", out)
	}
}

// help 返回文档，并把该类型标记为已知用法。
func TestHandleHelp_ReturnsDocAndMarksGate(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&asset_entity.Asset{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

	out, err := handleHelp(context.Background(), map[string]any{"asset": "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Command syntax") {
		t.Fatalf("help should return the SKILL.md body, got %q", out)
	}
	// 输出以类型开头（spec §4.6 第 2 条）。
	if !strings.Contains(out, "redis") {
		t.Fatalf("help should lead with the resolved type, got %q", out)
	}
}

// 未注册执行器的类型（Plan A 尚未支持 mongodb）应给出明确错误。
func TestHandleExec_UnsupportedTypeIsExplicit(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().Find(gomock.Any(), int64(5)).Return(
		&asset_entity.Asset{ID: 5, Name: "m1", Type: asset_entity.AssetTypeMongoDB}, nil)

	out, err := handleExec(context.Background(), map[string]any{
		"asset": "5", "command": "find app.users {}",
	})
	if err == nil && !strings.Contains(out, "mongodb") {
		t.Fatalf("expected an explicit unsupported-type message, got %q / %v", out, err)
	}
}

// 同名歧义必须冒泡成错误，不能静默取第一个。
func TestHandleExec_AmbiguousNameErrors(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
		{ID: 1, Name: "db", Type: asset_entity.AssetTypeDatabase},
		{ID: 2, Name: "db", Type: asset_entity.AssetTypeDatabase},
	}, nil)

	if _, err := handleExec(context.Background(), map[string]any{
		"asset": "db", "command": "SELECT 1",
	}); err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/tool/ -run "TestHandleExec|TestHandleHelp" -v`
Expected: 编译失败，`undefined: handleExec`

- [ ] **Step 3: 写 handler**

创建 `internal/ai/tool/tool_handlers_unified.go`。**顺序是有讲究的，必须严格按下列次序**：

1. `assetref.Resolve(ctx, aictx.ArgString(args, "asset"))` → 拿到资产（错误直接返回）
2. 门禁：`tool.GetDocGate(ctx)` 非 nil 且 `IsDocumented(convID, asset.Type)` 为假 → 返回引导文本（**非 error**），文本含资产名与解析出的类型
3. `permission.ExecutorFor(asset.Type)` → 未注册则返回明确的 unsupported 文本
4. **用资产的真实 `asset.Type` 做权限检查**（`checker.CheckForAsset(ctx, asset.ID, asset.Type, command)`），修掉 `database_helper.go:74` 用写死类型先检查、`:85` 才验类型的顺序缺陷（spec §5 第 2 条）
5. 执行并返回

> **为什么门禁与执行器查找必须排在权限检查之前**：权限检查有**用户可见副作用**——
> `NeedConfirm` 会弹审批对话框并阻塞等待。若把它放在门禁之前，模型对一个用法未知
> （或根本不支持）的类型调 `exec` 时，用户会先被弹一次审批，批准之后命令却因门禁被拦下
> 根本不执行。先做无副作用的判断（解析、门禁、执行器查找），再做有副作用的审批。

`handleHelp` 解析资产 → `permission.HelpFor(asset.Type)` → `docGate.MarkDocumented` → 返回 `"Asset \"<name>\" is type=<type>.\n\n" + doc`。

- [ ] **Step 4: 写工具定义并接入 `Tools()`**

创建 `internal/ai/tool/tools_unified.go`，两个 `*tool.RawTool`：`exec`（`asset` / `command` / `scope`，必填 `asset`+`command`，`IsSerial: true`）与 `help`（`asset`，必填，`IsSerial: false`）。在 `tools.go` 的 `Tools()` 中 `append(tools, unifiedTools()...)`，并把容量 `24` 改成 `29`。

- [ ] **Step 5: 更新注册表契约测试**

修改 `internal/ai/tool/tools_test.go:25-38` 的 `expected`，加入 `"exec"`、`"help"`；在 `serialNames`（`:47-53`）加入 `"exec"`。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/ai/... -v`
Expected: 全绿

- [ ] **Step 7: 提交**

```bash
git add internal/ai/tool/
git commit -m "✨ 新增统一 exec 与 help 工具"
```

---

### Task 7: 门禁装配与自动注入（门禁的第二个满足条件）

Task 4 只建了 `DocGate` 的状态机，**没有人构造它**；Task 6 的 handler 引用的 `docGate` 还悬空。
同时 spec §4.2 规定门禁有**两个**满足条件，Task 6 只实现了第一个（显式调 `help`）。
本任务补齐第二个：某类型文档已注入本次 Send 的 system prompt 时，同样视为已知用法——
否则打开了 redis tab 的用户仍会被要求先调 `help`，白白多一次往返。

**Files:**
- Modify: `internal/ai/runner/prompt_builder.go`（新增内置类型技能清单段）
- Modify: `internal/app/ai/chat.go:346-363`（现有按 tab 注入扩展 SKILL.md 的位置）
- Modify: `internal/ai/tool/tool_handlers_unified.go`（改为从 context 取 gate）
- Test: `internal/ai/runner/prompt_builder_test.go`

**Interfaces:**
- Produces:
  - `func WithDocGate(ctx context.Context, g *DocGate) context.Context`
  - `func GetDocGate(ctx context.Context) *DocGate`
  - `func (b *PromptBuilder) SetAssetTypeSkills(mds map[string]string)`

- [ ] **Step 1: 写失败测试**

在 `internal/ai/runner/prompt_builder_test.go` 追加：

```go
func TestBuild_ListsBuiltinAssetTypeSkills(t *testing.T) {
	b := NewPromptBuilder("en", AIContext{})
	b.SetAssetTypeSkills(map[string]string{
		"redis": "Run Redis commands against a Redis asset via exec.",
	})
	got := b.Build()
	if !strings.Contains(got, "redis") {
		t.Fatalf("prompt should list the redis skill, got:\n%s", got)
	}
	// 只列描述，不内联正文——正文由 help 按需加载（spec §3.3）。
	if strings.Contains(got, "## Command syntax") {
		t.Fatal("prompt must not inline the full SKILL.md body")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/runner/ -run TestBuild_ListsBuiltin -v`
Expected: 编译失败，`undefined: SetAssetTypeSkills`

- [ ] **Step 3: 实现清单段**

在 `PromptBuilder` 增加 `assetTypeSkills map[string]string`（类型 → 一行描述）与
`SetAssetTypeSkills`。在 `Build()` 的扩展 SKILL.md 段之前插入一段紧凑清单：

```
Asset command syntax is documented per asset type. Call help(<asset>) to load
the syntax for an asset before running exec against it.

- redis: Run Redis commands against a Redis asset via exec. ...
- ssh: Run shell commands on a remote server over SSH via exec. ...
```

**只列 `skills.Description()`，不内联正文**——正文由 `help` 按需加载。

- [ ] **Step 4: 装配 DocGate 并打通两个满足条件**

在 `internal/ai/tool/exec_gate.go` 增加 context 存取：

```go
type docGateKeyType struct{}

func WithDocGate(ctx context.Context, g *DocGate) context.Context {
	return context.WithValue(ctx, docGateKeyType{}, g)
}

func GetDocGate(ctx context.Context) *DocGate {
	if g, ok := ctx.Value(docGateKeyType{}).(*DocGate); ok {
		return g
	}
	return nil
}
```

在 `internal/app/ai/chat.go` 每次 Send 时：构造（或复用会话级的）`DocGate`，
用 `tool.WithDocGate` 注入 ctx；对本次注入了描述的每个内置类型调
`gate.MarkDocumented(convID, assetType)`。

把 Task 6 handler 中的 `docGate` 改为 `tool.GetDocGate(ctx)`。
**gate 为 nil 时必须放行**（不阻断执行）——门禁是引导机制而非安全边界，
真正的安全边界是权限检查；让引导机制在装配缺失时阻断操作，是把两件事混为一谈。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/ai/... ./internal/app/ai/... -v`
Expected: 全绿

- [ ] **Step 6: 提交**

```bash
git add internal/ai/runner/ internal/ai/tool/ internal/app/ai/
git commit -m "✨ exec 门禁装配与内置类型技能清单注入"
```

---

### Task 8: 审计识别 `asset` 参数

`audit.go:53-55` 只读 `asset_id` / `id`。新工具用 `asset`，不改则**每个新工具都记录不到资产**。

**Files:**
- Modify: `internal/ai/audit/audit.go:52-62`
- Test: `internal/ai/audit/audit_asset_ref_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/audit/audit_asset_ref_test.go`，断言：args 为 `{"asset":"7"}` 时能解析出 id 7 并查到名称；args 为 `{"asset":"web-1"}` 时按名称解析；两者都要产出非空 `AssetName`。用 `mock_asset_repo` 按 Task 1 的 `setupRepo` 模式搭建。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/audit/ -run TestAudit_AssetRef -v`
Expected: FAIL，`AssetName` 为空

- [ ] **Step 3: 写实现**

把 `audit.go:52-62` 的取值逻辑改为：先试 `asset_id` / `id`（保持旧工具行为），再试 `asset` 字符串并走 `assetref.Resolve`。解析成功时同时得到 `assetID` 与 `assetName`，**不再**依赖执行后的 `Find`——这同时为 Plan C 的 `delete_asset` 铺路（删除后 `Find` 会因 `status != Active` 查不到，名称会丢，见 spec §5 第 4 条）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/audit/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/ai/audit/
git commit -m "🐛 审计支持 asset 参数的名称/ID 解析"
```

---

### Task 9: 齐备性守卫（只可缩短的豁免清单）

**Files:**
- Create: `internal/ai/execimpl/coverage_test.go`

- [ ] **Step 1: 写测试**

```go
package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
)

// exemptFromExec 是尚未接入统一 exec 的资产类型。
//
// 这个清单**只可缩短，不可增长**——与 internal/archtest 的 legacy 豁免清单同一惯例。
// 新增资产类型时不要往这里加，而应实现执行器。
//
//   - local / oss：spec §2 明确列为非目标，另开 issue 跟踪
//   - mongodb / etcd / kafka：Plan B 补齐
//   - vnc / rdp：交互式协议，无可脚本化命令面，PolicyKind 为空因而本就不在检查范围
var exemptFromExec = map[string]string{
	"local":   "spec §2 非目标：有 PolicyKind 却无 permission 注册",
	"oss":     "spec §2 非目标：仅扩展可达",
	"mongodb": "Plan B",
	"etcd":    "Plan B",
	"kafka":   "Plan B",
}

func TestEveryPolicyKindTypeHasExecutor(t *testing.T) {
	for _, h := range assettype.All() {
		if h.PolicyKind() == "" {
			continue // vnc / rdp：无策略种类，不在统一 exec 范围内
		}
		if reason, exempt := exemptFromExec[h.Type()]; exempt {
			t.Logf("skipping %s (%s)", h.Type(), reason)
			continue
		}
		if _, ok := permission.ExecutorFor(h.Type()); !ok {
			t.Errorf("asset type %q has PolicyKind %q but no exec executor registered; "+
				"implement one or justify an entry in exemptFromExec",
				h.Type(), h.PolicyKind())
		}
	}
}

// 豁免清单只可缩短：数量增长即失败，逼迫增改者正视。
func TestExemptionListDoesNotGrow(t *testing.T) {
	const maxExemptions = 5
	if len(exemptFromExec) > maxExemptions {
		t.Fatalf("exemptFromExec grew to %d entries (max %d); "+
			"the list may only shrink — implement the executor instead",
			len(exemptFromExec), maxExemptions)
	}
}
```

- [ ] **Step 2: 运行测试**

Run: `go test ./internal/ai/execimpl/ -run "TestEveryPolicyKind|TestExemptionList" -v`
Expected: PASS。`assettype.All()` 已存在（`internal/assettype/registry.go:60`），无需新增。

- [ ] **Step 3: 提交**

```bash
git add internal/ai/execimpl/coverage_test.go
git commit -m "✅ exec 覆盖度门禁：豁免清单只可缩短"
```

---

### Task 10: 收尾验证

- [ ] **Step 1: 全量测试**

Run: `go test ./internal/...`
Expected: 全绿

- [ ] **Step 2: Lint**

Run: `golangci-lint run`
Expected: 无新增告警

- [ ] **Step 3: 可观测验证（不是断言，是观察）**

按 `docs/VERIFICATION.md`：启动应用，在 AI 会话中对一个 redis 资产直接调 `exec`，确认返回的是"请先调用 help"引导且**点出了 redis 类型**；再调 `help` 后重试，确认命令真正执行。随后读 `logs/opskat.log` 与 `opskat.db` 的 `audit_logs` 表，确认该次调用的 `asset_name` **非空**、`decision` 已记录。

- [ ] **Step 4: 提交**

```bash
git commit --allow-empty -m "✅ Plan A 收尾：exec 基座五类型跑通"
```

---

## Plan A 完成标准

- `exec` / `help` 已注册，ssh / serial / database / redis / k8s 五类可用。
- 旧工具全部保留且行为不变。
- 权限检查使用资产**真实类型**，且在执行前完成。
- 审计能从 `asset` 参数解析出资产并记录非空名称。
- 齐备性测试锁住覆盖缺口，豁免清单只可缩短。

## 后续

- **Plan B**：mongo / etcd / kafka 的 DSL parser（`Parse(Format(req)) == req` 属性测试）→ 删除 14 个旧工具 → 清理 `prompt_builder.go` 的写死路由表。
- **Plan C**：`put_*` / `delete_*`、`ext_exec` 与 manifest 校验加固、`batch_exec`、opsctl 统一。
