# cp 文件传输审批补齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 SFTP 文件传输（AI 的 `upload_file`/`download_file` 与 `opsctl cp`）走审批，补齐 `cp` 这个早已存在却从未被请求过的审批类型。

**Architecture:** `cp` 不是新概念——`internal/approval/approval.go:26`、`internal/ai/permission/approval.go:5`、`grant_entity.Grant.ToolName`、`opsctl grant` 的 CLI 帮助里都写着它，`requireApproval` 的离线分支还专门处理 `"cp"`，只是没有任何调用点发起过 cp 审批。本计划按仓库既有的注册表模式补上：注册一个 `cp` 权限类型（只查 grant、按**路径**匹配，不碰命令策略规则），两个调用方（AI 工具层、opsctl CLI）共用这一条缝。前置条件是 grant 池的 ToolName 隔离——否则 cp 的路径 pattern 会被命令匹配器看见。

**Tech Stack:** Go 1.26（goconvey + testify）、React 19 + i18next。

## Global Constraints

- 修复策略遵循 [AGENTS.md](../../../AGENTS.md)：先写复现失败的测试再动实现；不做顺手重构；错误不吞。
- 扩展靠注册而非改 switch：新类型走 `registerPermissionType`，禁止在共享代码里 `if assetType == "cp"` 分支（`grantItemAppliesTo` 是唯一例外，理由见 Task 1）。
- commit 用 gitmoji，emoji 字形开头；本计划的 commit 都不带 `#248` 后缀，只在最后一个 commit 的 body 写 `closes #248`。
- 基座是 `origin/main`（0e0e71d8）。该缺陷在 main 上就存在，与 `feature/ai-tool-exec-convergence` 无关。
- 后端验证用 `go test ./...` 与 `make lint`（golangci-lint），**不要**用 `go build ./...` 判定成败——`frontend/dist` 是生成物，本地必然缺失。

## 关键设计决策

1. **审批主体（`ApprovalItem.Command`）是「远端路径」**，不是 `"local → remote"` 整串。grant 是按资产存的，本地路径不属于任何资产，塞进 pattern 无法匹配。方向与本地路径放 `Detail`，只用于展示。
2. **cp 的匹配函数是 `path.Match`**，不是 `policy.MatchCommandRule`。`/opt/app/*` 应当匹配 `/opt/app/x.sh` 而不跨 `/`。
3. **cp 只查 grant，不查 CommandPolicy 的 allow/deny 规则**——那些规则是命令形状的（`systemctl *`），拿路径去撞它们只会产生误判。
4. **ToolName 隔离只区分 cp / 非 cp**。历史 grant 行的 `tool_name` 一律被 `SaveGrantPattern` 写死成 `"exec"`（含 redis/sql 等），所以严格按类型相等过滤会让存量会话授权集体失效。本计划让各调用方开始写入真实 approvalType（把列推向正确），但过滤只在 cp 与非 cp 之间划线——cp 行只可能由新代码产生，不存在歧义。**遗留项**：等存量行自然过期后（或补一次 backfill 迁移）再收紧为严格相等，另开 issue，不在本计划内。

## File Structure

| 文件 | 责任 |
|---|---|
| `internal/ai/policy/path_policy.go`（新建） | `MatchPathRule` —— 路径 pattern 匹配的唯一实现 |
| `internal/ai/tool/local_tool_gate.go`（改） | 改调 `policy.MatchPathRule`，删掉本地那份 `path.Match` |
| `internal/ai/permission/checker.go`（改） | grant 匹配加 ToolName 隔离；`HandleConfirm` 支持 detail |
| `internal/ai/permission/permission.go`（改） | `SaveGrantPattern` 写真实 toolName；`checkFileTransferPermission` |
| `internal/ai/permission/type_registry.go`（改） | 注册 `cp` 类型 |
| `internal/ai/tool/tool_handlers_exec.go`（改） | `upload_file`/`download_file` 传输前过审批 |
| `cmd/opsctl/command/cp.go`（改） | 三个分支各自 `requireApproval`；删谎言注释 |
| `frontend/src/components/approval/ApprovalBlock.tsx`（改） | `cp` 图标 + detail 展示不再限定本地工具 |
| `frontend/src/i18n/locales/{en,zh-CN}/common.json`（改） | `ai.approvalTransferDetail` |

---

### Task 1: grant 池的 cp / 非 cp 隔离

**Files:**
- Modify: `internal/ai/permission/checker.go:141-185`（`matchGrantPatterns` / `matchGrantPatternsWith`）
- Modify: `internal/ai/permission/permission.go:410-447`（`SaveGrantPattern`）、`:402-408`（`SaveGrantPatternsForApproval`）、`:81`、`:350-362`
- Modify: `internal/ai/permission/checker.go:271`（`HandleConfirm` 里的落库调用）
- Test: `internal/ai/permission/grant_isolation_test.go`（新建）

**Interfaces:**
- Produces: `permission.GrantToolCp = "cp"` 常量；`SaveGrantPattern(ctx, sessionID, assetID, assetName, toolName, command string)`（**新增第 5 个参数 toolName**）；`matchGrantPatternsWith(ctx, assetID, groups, subCmds, toolName string, matchFn policy.MatchFunc) string`。

- [ ] **Step 1: 写失败测试**

新建 `internal/ai/permission/grant_isolation_test.go`。**本包没有内存 DB，用的是 `stubGrantRepo`**（`permission_test.go:22`）——照 `TestSaveGrantPattern`（`permission_test.go:633`）的 setup 复用，别另起一套：

```go
package permission

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/repository/grant_repo"

	. "github.com/smartystreets/goconvey/convey"
)

// withStubGrant 注册 stubGrantRepo 并返回带 sessionID 的 ctx。
// grant 匹配依赖 aictx.GetSessionID —— 不注入 sessionID 会直接返回 ""，测试会假绿。
func withStubGrant(t *testing.T) context.Context {
	stub := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})
	return aictx.WithSessionID(context.Background(), "sess-cp")
}

func TestGrantIsolation(t *testing.T) {
	Convey("grant 池按工具面隔离", t, func() {
		Convey("cp 的路径授权不能放行命令", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "/opt/app/*")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"/opt/app/deploy.sh"}, "exec", policy.MatchCommandRule)
			So(got, ShouldEqual, "")
		})

		Convey("命令授权不能放行文件传输", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", "exec", "*")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"/etc/cron.d/backup"}, GrantToolCp, policy.MatchPathRule)
			So(got, ShouldEqual, "")
		})

		Convey("存量 tool_name=exec 的行仍然为非 cp 检查生效", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", "exec", "redis-cli get *")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"redis-cli get foo"}, "redis", policy.MatchCommandRule)
			So(got, ShouldEqual, "redis-cli get *")
		})
	})
}
```

`MatchPathRule` 在 Task 2 才建；本任务先用 `policy.MatchCommandRule` 占位跑通前两个测试的编译，Task 2 落地后回来改回 `MatchPathRule`——**不要**为了编译把第二个测试删掉。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/permission/ -run TestGrantIsolation -v`
Expected: 编译失败（`SaveGrantPattern` 参数个数不对、`GrantToolCp` 未定义）——这正是要修的形状。

- [ ] **Step 3: 实现**

`internal/ai/permission/permission.go`：

```go
// GrantToolCp 是文件传输授权在 grant_items.tool_name 里的取值。
// 它的 Command 是路径而非命令，必须与命令面的 grant 彻底隔离，见 grantItemAppliesTo。
const GrantToolCp = "cp"

// SaveGrantPattern 将模式保存为已批准的 GrantItem。toolName 取审批类型
// （"exec"/"redis"/"cp"…），决定这条授权属于哪个工具面——匹配时按它隔离。
func SaveGrantPattern(ctx context.Context, sessionID string, assetID int64, assetName, toolName, command string) {
	// …原逻辑不变，只把 ToolName: "exec" 换成 ToolName: toolName
}
```

`SaveGrantPatternsForApproval` 已经收了 `approvalType`，把它透传下去即可（`permission.go:406`）。`HandleConfirm`（`checker.go:271`）传 `approvalType`（该函数里已算好这个变量）。

`internal/ai/permission/checker.go`：

```go
// grantItemAppliesTo 判断一条 grant item 是否属于当前检查的工具面。
//
// 只在 cp 与非 cp 之间划线，不是按类型严格相等：存量行的 tool_name 一律被旧版
// SaveGrantPattern 写死成 "exec"（含 redis/sql 等），严格相等会让它们集体失效。
// cp 行只可能由新代码产生，因此这条线是准确的——而它正是必须划的那条：cp 的
// pattern 是路径、匹配走 path.Match，被命令面匹配到就意味着一条 `/opt/*` 授权
// 能放行任意命令。
func grantItemAppliesTo(item *grant_entity.GrantItem, toolName string) bool {
	return (item.ToolName == GrantToolCp) == (toolName == GrantToolCp)
}
```

在 `matchGrantPatternsWith` 的 item 循环里，紧挨 `grantItemMatchesTarget` 之后加：

```go
		if !grantItemAppliesTo(item, toolName) {
			continue
		}
```

并给 `matchGrantPatternsWith` / `matchGrantForAssetSubCmdsWith` / `matchGrantForAssetWith` / `matchGrantForAsset` / `matchGrantPatterns` / `matchGrantForAssetSubCmds` 逐层加 `toolName string` 参数。各现有调用方传自己的 approvalType：`permission.go:81`（SSH/serial 路径）传 `"exec"`，`checkDatabasePermission` 传 `"sql"`，redis/etcd/mongo/kafka/k8s 各传自己的。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ai/permission/ -v 2>&1 | tail -20`
Expected: 全部 PASS（含既有用例，不许有回归）。

- [ ] **Step 5: 提交**

```bash
git add internal/ai/permission/
git commit -m "🔒 grant 池按工具面隔离 cp 与命令，落库写入真实 tool_name"
```

---

### Task 2: 路径匹配规则 `MatchPathRule`

**Files:**
- Create: `internal/ai/policy/path_policy.go`
- Modify: `internal/ai/tool/local_tool_gate.go:157-170`（`matchLocalPattern`）
- Test: `internal/ai/policy/path_policy_test.go`（新建）

**Interfaces:**
- Consumes: 无。
- Produces: `policy.MatchPathRule(pattern, subject string) bool`，签名符合既有的 `policy.MatchFunc`。

- [ ] **Step 1: 写失败测试**

```go
func TestMatchPathRule(t *testing.T) {
	convey.Convey("路径 pattern 匹配", t, func() {
		convey.So(MatchPathRule("/opt/app/*", "/opt/app/deploy.sh"), convey.ShouldBeTrue)
		convey.So(MatchPathRule("/opt/app/*", "/opt/app/sub/deploy.sh"), convey.ShouldBeFalse) // * 不跨 /
		convey.So(MatchPathRule("*", "/etc/passwd"), convey.ShouldBeTrue)                      // 全量放行
		convey.So(MatchPathRule("/etc/passwd", "/etc/passwd"), convey.ShouldBeTrue)
		convey.So(MatchPathRule("/etc/*", "/etc/"), convey.ShouldBeTrue)
		convey.So(MatchPathRule("/opt/[", "/opt/x"), convey.ShouldBeFalse) // 非法 pattern 不放行
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/policy/ -run TestMatchPathRule -v`
Expected: FAIL —— `undefined: MatchPathRule`。

- [ ] **Step 3: 实现**

```go
package policy

import "path"

// MatchPathRule 按 POSIX glob 匹配文件路径（`*` 不跨 `/`）。文件传输授权与
// local_write/local_edit 的路径白名单共用这一份实现——命令用 MatchCommandRule，
// 路径用本函数，两者不可互换。pattern 非法时按不匹配处理（fail-closed）。
func MatchPathRule(pattern, subject string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == subject {
		return true
	}
	ok, err := path.Match(pattern, subject)
	return err == nil && ok
}
```

`local_tool_gate.go` 的 `matchLocalPattern` default 分支改成 `return policy.MatchPathRule(pattern, subject)`，并删掉文件顶部的 `"path"` import（若已无其他使用）。函数开头那两行 `pattern == "*" || pattern == subject` 的短路已被 `MatchPathRule` 覆盖，`local_bash` 分支仍需保留它——保持 `matchLocalPattern` 现有结构，只换 default 分支。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ai/policy/ ./internal/ai/tool/ 2>&1 | tail -10`
Expected: PASS（`local_tool_gate_test.go` 的既有用例必须仍然全绿）。

回到 Task 1 的 `TestGrantIsolation_CommandPatternDoesNotAuthorizeTransfer`，把占位的 `policy.MatchCommandRule` 改回 `policy.MatchPathRule`，重跑 `go test ./internal/ai/permission/`。

- [ ] **Step 5: 提交**

```bash
git add internal/ai/policy/ internal/ai/tool/local_tool_gate.go internal/ai/permission/grant_isolation_test.go
git commit -m "♻️ 路径 pattern 匹配抽成 policy.MatchPathRule，本地工具门禁改调它"
```

---

### Task 3: 注册 `cp` 权限类型

**Files:**
- Modify: `internal/ai/permission/type_registry.go:47-56`（`init`）
- Modify: `internal/ai/permission/permission.go`（新增 `checkFileTransferPermission`）
- Modify: `internal/ai/permission/checker.go:222`（`HandleConfirm` 支持 detail）
- Test: `internal/ai/permission/file_transfer_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 的 `GrantToolCp`、带 toolName 的 grant 匹配；Task 2 的 `policy.MatchPathRule`。
- Produces: `CheckPermission(ctx, "cp", assetID, remotePath)`；`(*CommandPolicyChecker).HandleConfirm(ctx, assetID, assetType, command string, detail ...string)`。

- [ ] **Step 1: 写失败测试**

```go
func TestCheckFileTransferPermission(t *testing.T) {
	Convey("cp 类型的权限检查", t, func() {
		Convey("没有 grant 时需要确认", func() {
			ctx := withStubGrant(t) // Task 1 建的 helper
			r := CheckPermission(ctx, GrantToolCp, 1, "/etc/cron.d/backup")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("命中 cp grant 的路径 pattern 时放行", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "/opt/app/*")
			r := CheckPermission(ctx, GrantToolCp, 1, "/opt/app/deploy.sh")
			So(r.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("路径为空时需要确认，不能整串放行", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "*")
			r := CheckPermission(ctx, GrantToolCp, 1, "")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
```

注意 `checkFileTransferPermission` 不解析资产、不收集策略，所以这组用例**不需要** `setupPolicyTest(t)` 的资产 mock；只有 Task 3 之后走到 `HandleConfirm` 的路径才需要（那是 Task 4 的事）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/permission/ -run TestCheckFileTransferPermission -v`
Expected: 第一个子用例意外通过（未注册类型也返回 NeedConfirm），第二个 FAIL —— `Allow != NeedConfirm`，因为 `"cp"` 还没注册，`CheckPermission` 走的是 `permissionTypeFor` 未命中的兜底。这个失败正是缺陷本身。

- [ ] **Step 3: 实现**

`permission.go` 新增：

```go
// --- 文件传输（cp） ---

// checkFileTransferPermission 校验一次文件传输的远端路径。
//
// 与命令类检查有意不同：只查 grant，不查 CommandPolicy 的 allow/deny 规则——
// 那些规则是命令形状的（`systemctl *`），拿路径去撞只会产生误判。匹配用
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
```

`type_registry.go` 的 `init()` 末尾加：

```go
	registerPermissionType(GrantToolCp, "cp", false, checkFileTransferPermission)
```

`HandleConfirm` 加可变参 detail（沿用本包 `RegisterExecutor` 的可选参数写法，避免改动三个既有调用方）：

```go
func (c *CommandPolicyChecker) HandleConfirm(ctx context.Context, assetID int64, assetType, command string, detail ...string) aictx.CheckResult {
	// …
	item := ApprovalItem{Type: approvalType, AssetID: assetID, AssetName: assetName, Command: command}
	if len(detail) > 0 {
		item.Detail = detail[0]
	}
	items := []ApprovalItem{item}
	// …其余不变
}
```

`CheckForAsset` 同样加 `detail ...string` 并透传给 `HandleConfirm`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ai/permission/ -v 2>&1 | tail -20`
Expected: 全绿。另外确认 `TestPermissionTypeRegistry` 之类的既有注册表用例（`type_registry_test.go`）若断言了类型数量，一并更新为含 cp 的新值。

- [ ] **Step 5: 提交**

```bash
git add internal/ai/permission/
git commit -m "🔒 注册 cp 权限类型：按路径匹配 grant，审批项支持 detail"
```

---

### Task 4: AI 的 `upload_file` / `download_file` 过审批

**Files:**
- Modify: `internal/ai/tool/tool_handlers_exec.go:136-…`（`handleUploadFile`）、`:…`（`handleDownloadFile`）
- Test: `internal/ai/tool/file_transfer_approval_test.go`（新建）

**Interfaces:**
- Consumes: Task 3 的 `CheckForAsset(ctx, assetID, "cp", remotePath, detail)`。
- Produces: 无对外新符号。

- [ ] **Step 1: 写失败测试**

关键点：门开在 `helper.ExecuteWithSFTP` **之前**，所以拒绝路径不会碰网络，测试无需 SSH server。

**必须注册两个 mock**，否则不是测不到而是 panic：`HandleConfirm` 会调 `asset_svc.Asset().Get`（底层 `asset_repo.Asset()`，未注册时为 nil），`checkFileTransferPermission` 会调 `grant_repo.Grant()`。本包（`internal/ai/tool`）现有测试都没做过这件事，照 `internal/ai/permission/opsctl_policy_test.go:45` 的 `setupPolicyTest` 写法搬过来。

```go
func TestUploadFileRequiresApproval(t *testing.T) {
	Convey("upload_file 在传输前必须过审批", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(mockAsset)
		t.Cleanup(func() {
			if origAsset != nil {
				asset_repo.RegisterAsset(origAsset)
			}
		})
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
			Return(&asset_entity.Asset{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()

		var seen []permission.ApprovalItem
		checker := permission.NewCommandPolicyChecker(
			func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
				So(kind, ShouldEqual, "single")
				seen = append(seen, items...)
				return permission.ApprovalResponse{Decision: "deny"}
			})
		ctx := permission.WithPolicyChecker(context.Background(), checker)

		_, err := handleUploadFile(ctx, map[string]any{
			"asset_id":    float64(1),
			"local_path":  "/tmp/whatever",
			"remote_path": "/etc/cron.d/backup",
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "USER DENIED")
		So(seen, ShouldHaveLength, 1)
		So(seen[0].Type, ShouldEqual, "cp")
		So(seen[0].Command, ShouldEqual, "/etc/cron.d/backup") // 审批主体是远端路径
		So(seen[0].Detail, ShouldContainSubstring, "/tmp/whatever")
	})
}

func TestDownloadFileRequiresApproval(t *testing.T) {
	// 同款结构（含两个 mock 的注册），差别只有：
	//   args 为 asset_id / remote_path / local_path
	//   seen[0].Command 仍是 remote_path，Detail 含 local_path
}
```

`grant_repo` 未注册时 `matchGrantPatternsWith` 会在 `repo == nil` 处直接返回 ""（`checker.go:151`），所以本测试可以不注册 grant repo——但**只在被拒用例里成立**。如果后续补"命中 grant 直接放行"的用例，必须一并注册 stub grant repo。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/tool/ -run TestUploadFileRequiresApproval -v`
Expected: FAIL —— err 里是 SSH 拨号失败（`failed to get asset` / 连接错误）而不是 `USER DENIED`，`seen` 为空。**这就是 #248 的复现**。

- [ ] **Step 3: 实现**

`handleUploadFile` 在参数校验之后、`helper.ExecuteWithSFTP` 之前插入：

```go
	if checker := permission.GetPolicyChecker(ctx); checker != nil {
		detail := fmt.Sprintf("upload %s → %s", localPath, remotePath)
		if result := checker.CheckForAsset(ctx, assetID, permission.GrantToolCp, remotePath, detail); result.Decision != aictx.Allow {
			aictx.RecordDecision(ctx, result)
			return "", fmt.Errorf("%s", result.Message)
		}
	}
```

`handleDownloadFile` 同理，`detail` 为 `fmt.Sprintf("download %s → %s", remotePath, localPath)`，审批主体仍是 `remotePath`。

> checker 为 nil 时放行，与 `tool_handler_batch.go` / 扩展工具路径一致：那说明调用方不是 AI 会话（opsctl 直连 handler），审批由 Task 5 在 CLI 层负责。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ai/tool/ -v 2>&1 | tail -20`
Expected: 新用例 PASS，既有用例无回归。

- [ ] **Step 5: 提交**

```bash
git add internal/ai/tool/
git commit -m "🔒 upload_file/download_file 传输前过 cp 审批"
```

---

### Task 5: `opsctl cp` 过审批

**Files:**
- Modify: `cmd/opsctl/command/cp.go:44`（删谎言注释）、`:50-58`（proxy 分支）、`:66-88`（直连三分支）
- Test: `cmd/opsctl/command/cp_approval_test.go`（新建）

**Interfaces:**
- Consumes: 既有的 `requireApproval(ctx, approval.ApprovalRequest{Type: "cp", …})`（`cmd/opsctl/command/approval.go:38`）。
- Produces: 包级变量 `cpApprovalFn = requireApproval`（测试接缝）。`cmdCp` 的签名不变：`cmdCp(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int`。

- [ ] **Step 1: 写失败测试**

**不要**让测试真的去连审批 socket：`bootstrap.ResolvedDataDir()` 只在 `bootstrap.Init` 里被赋值，测试无法用环境变量改写它，于是会落到开发机的真实数据目录——桌面端正好开着的话，这个测试会真的弹一个审批框并阻塞。

改用本包已有的接缝写法（`opsctlAuditWriter` 就是这么被测试替换的，见 `handler_test.go:34`）：把审批调用收进包级变量 `cpApprovalFn`，测试直接替换它。断言的是安全属性本身——**被拒时传输 handler 一次都没被调用**。

```go
func TestCmdCpRequiresApproval(t *testing.T) {
	Convey("cp 被拒时不得发起任何传输", t, func() {
		called := false
		handlers := map[string]tool.ToolHandlerFunc{
			"upload_file": func(_ context.Context, _ map[string]any) (string, error) {
				called = true
				return "", nil
			},
		}

		orig := cpApprovalFn
		cpApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
			So(req.Type, ShouldEqual, "cp")
			So(req.Command, ShouldEqual, "/etc/cron.d/backup") // 审批主体是远端路径
			return ApprovalResult{Decision: aictx.Deny}, errors.New("user denied")
		}
		defer func() { cpApprovalFn = orig }()

		// resolveAsset 走 asset_repo，注册 mock 让 "srv" 解析到 ID 1
		// （照 internal/ai/permission/opsctl_policy_test.go:45 的 setupPolicyTest 写法）
		exitCode := cmdCp(ctx, handlers, []string{"/tmp/src", "srv:/etc/cron.d/backup"}, "")

		So(exitCode, ShouldEqual, 1)
		So(called, ShouldBeFalse)
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/opsctl/command/ -run TestCmdCpRequiresApproval -v`
Expected: 编译失败（`cpApprovalFn` 未定义）。加上变量后再跑：FAIL —— `called` 为 true，因为当前 `cmdCp` 压根不请求审批。**这就是 CLI 侧的同一个洞。**

- [ ] **Step 3: 实现**

`cp.go` 顶部删掉 `// cp 不需要审批；…` 这句谎言注释（`:44`），并加上测试接缝：

```go
// cpApprovalFn 是 cp 的审批入口，变量而非直接调用是为了可测——
// 与本包的 opsctlAuditWriter 同一套路（测试替换掉，避免真的去连桌面端 socket）。
var cpApprovalFn = requireApproval
```

替换为按方向发起审批的逻辑。三个分支各自的审批主体：

- 上传（local → remote）：`AssetID = dstAssetID`，`Command = dstPath`，`Detail = "opsctl cp <src> <dst>"`
- 下载（remote → local）：`AssetID = srcAssetID`，`Command = srcPath`，同款 Detail
- 资产间（remote → remote）：**两次审批**，先源端读（`srcAssetID`/`srcPath`）再目的端写（`dstAssetID`/`dstPath`）；任一被拒即返回 1

审批要放在 proxy 分支与直连分支的**共同上游**（即 `if proxy := getSSHProxyClient(); proxy != nil` 之前），否则 proxy 路径会漏。审批结果的 `SessionID` 按 `exec.go:53-55` 的写法注入 `auditCtx`，并把 `approvalResult.ToCheckResult()` 传给 `writeOpsctlAudit`——现在三处调用都传的 `nil`，改成真实决策。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/opsctl/command/ -v 2>&1 | tail -20`
Expected: 全绿。

- [ ] **Step 5: 提交**

```bash
git add cmd/opsctl/command/
git commit -m "🔒 opsctl cp 三条路径接入 cp 审批，审计记录真实决策"
```

---

### Task 6: 审批面板展示 cp

**Files:**
- Modify: `frontend/src/components/approval/ApprovalBlock.tsx:127-138`（detail 展示条件）、`:272-284`（`TypeBadge` 图标表）
- Modify: `frontend/src/i18n/locales/en/common.json:1038` 附近、`frontend/src/i18n/locales/zh-CN/common.json:1038` 附近
- Test: `frontend/src/__tests__/ApprovalBlock.test.tsx`（若不存在则新建）

**Interfaces:**
- Consumes: Task 3/4/5 产生的 `ApprovalItem{ type: "cp", command: <远端路径>, detail: "upload … → …" }`。

- [ ] **Step 1: 写失败测试**

```tsx
it("renders a cp approval with its transfer detail", () => {
  render(<ApprovalBlock block={{ ...baseBlock, approvalItems: [
    { type: "cp", asset_id: 1, asset_name: "srv", command: "/etc/cron.d/backup",
      detail: "upload /tmp/x → /etc/cron.d/backup" },
  ]}} />);
  expect(screen.getByText("CP")).toBeInTheDocument();
  expect(screen.getByText(/upload \/tmp\/x/)).toBeInTheDocument();
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && pnpm test -- ApprovalBlock`
Expected: FAIL —— detail 不渲染，因为现有条件是 `isLocalTool && item.detail`。

- [ ] **Step 3: 实现**

- `TypeBadge` 的 `icons` 表加 `cp: FileUp`（从 `lucide-react` 引入）。
- detail 的渲染条件从 `isLocalTool && item.detail` 改为 `item.detail`，`<summary>` 文案按类型取：`local_write` → `ai.approvalLocalToolContentPreview`，`local_edit` → `ai.approvalLocalToolEditPreview`，其余 → 新键 `ai.approvalTransferDetail`。
- 两个 locale 文件加 `"approvalTransferDetail"`：en `"Show transfer detail"` / zh-CN `"查看传输详情"`（**不要**逐字对译，各语言用地道说法）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd frontend && pnpm test -- ApprovalBlock && pnpm lint`
Expected: PASS + 0 warning（前端 lint 全 error 门槛）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src
git commit -m "🎨 审批面板支持 cp 类型：图标与传输详情展示"
```

---

### Task 7: 文档与收尾验证

**Files:**
- Modify: `docs/ARCHITECTURE.md`（AI/审批一节里说明文件传输走 cp 审批）
- Modify: `docs/superpowers/specs/2026-07-20-ai-tool-exec-refactor-design.md:364`（把 #248 标记为已处理）

- [ ] **Step 1: 改文档**

先读 [docs/DOC-MAINTENANCE.md](../../DOC-MAINTENANCE.md) 再动任何 `docs/*`。只改与本次事实相关的行，不做顺手整理。

- [ ] **Step 2: 全量验证**

Run: `go test ./... 2>&1 | grep -v "^ok\|no test files"`
Expected: 无输出（全绿）。

Run: `make lint`
Expected: 0 issue。

Run: `cd frontend && pnpm test && pnpm lint`
Expected: 全绿。

- [ ] **Step 3: 观测验证（AGENTS.md 要求「看现象而非断言」）**

按 [docs/VERIFICATION.md](../../VERIFICATION.md)：启动应用 → AI 会话里让模型对一台 SSH 资产做一次 `upload_file` → 审批面板应弹出 CP 条目 → 拒绝 → 查 `logs/opskat.log` 与 `opskat.db` 的 `audit_logs`，确认记录到拒绝决策且**没有**发生 SFTP 连接。再跑一次 `opsctl cp /tmp/x srv:/tmp/y`，确认桌面端弹审批。

- [ ] **Step 4: 提交并开 PR**

```bash
git add docs/
git commit -m "📄 文档补齐 cp 审批：架构说明与 spec 遗留项状态

closes #248"
```
