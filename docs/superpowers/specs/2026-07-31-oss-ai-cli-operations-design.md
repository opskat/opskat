# OSS 资产的 AI / CLI 操作能力

> Issue [#250](https://github.com/opskat/opskat/issues/250)
> 日期：2026-07-31
> 状态：设计待确认

**目标**：让 `oss` 资产像 ssh/database/redis/mongodb/etcd/kafka/k8s 一样，通过统一 `exec` 接缝获得一套可脚本化、可审批、可审计的结构化对象存储操作，AI 与 opsctl 共用同一份执行与权限实现。

**硬不变式**：**每一条实际被读写的路径 / 对象 key，都必须在动手之前作为一个具体主体被授权过**——单次传输如此，递归与通配展开出的每一条也如此；`exec` 与 `cp` 两条入口都成立。且 OSS 的权限判定只走 OSS 自己的策略语义，绝不落到 shell 命令策略或 cp 的路径式授权上。

---

## 1. 现状与问题

1. **`oss` 是唯一有服务端操作能力、却没有任何脚本化入口的内置资产类型。** `internal/service/oss_svc/service.go` 已实现 `ListBuckets` / `ListObjects` / `StatObject` / `PutObject` / `GetObject` / `CopyObject` / `MoveObject` / `RemoveObject` / `PresignGet` / `PresignPut` 全部能力，但唯一调用方是桌面 IPC（`internal/app/oss/oss_ops.go`、`oss_transfer.go`）。AI 与 opsctl 都够不着。

2. **`oss` 的 `PolicyKind()` 返回空串**（`internal/assettype/oss.go:44`），`DefaultPolicy()` 返回 `nil`（同文件 :43）。因此 `policy.AssetKindOf("oss")` 查不到 kind，`ResolvePolicyKind("oss")` 返回 `ok=false`，OSS 资产在策略编辑器与策略测试面板里都不存在。前端 `frontend/src/lib/assetTypes/oss.ts:21` 的注释把这一点写死为"OSS 无 policy"。

3. **`oss` 目前是 doc-only 类型**：`internal/ai/execimpl/register.go` 把它和 `rdp` / `vnc` / `local` 一起放进 `RegisterHelpDoc` 循环，只注册用法文档、不注册执行器。`internal/ai/skills/oss/SKILL.md` 正文明确写着 "There is **no command surface**: `exec` is not supported for this type"。`internal/ai/execimpl/help_coverage_test.go` 的 `TestDocOnlyTypesHaveNoExecutor` 把这一状态锁死为断言。

4. **opsctl 完全够不着 OSS。** `cmd/opsctl/command/root.go` 的 verb 表里没有 OSS 相关项；`exec` 走 `permission.ExecutorFor(asset.Type)`，OSS 查不到执行器，只会拿到 `unsupportedTypeError`（`internal/ai/tool/tool_handlers_unified.go:155`）；`cp` 的两条路径（proxy 与直连）都写死 SFTP。

5. **预签名 URL 目前没有任何脱敏处理。** `internal/pkg/auditredact/redact.go` 的 `textRedactors` 只覆盖 `password` / `token` / `secret_access_key` / `Authorization: Bearer` / URL userinfo / PEM 私钥六类形态，**不认识** SigV4 查询参数（`X-Amz-Signature` / `X-Amz-Credential` / `X-Amz-Security-Token`）。一旦 presign 结果进入审计 `result` 列，签名会原样落库。

**基线**：`go test ./...` 在 `main`（`24110bf7`）为全绿（40 个包 ok，无 FAIL）。本设计不修复任何既有失败。

---

## 2. 目标与非目标

### 目标

1. 为 `oss` 定义 bucket/object 语义的策略种类 `PolicyKindOSS`，与 shell 命令策略彻底隔离。
2. 为 `oss` 定义可解析、可逆、可规范化的命令 DSL，并注册统一 `exec` 执行器。
3. 覆盖 issue 列出的 9 个操作：list buckets、list objects、stat、upload、download、copy、move、delete、presign。
4. opsctl 经 `opsctl exec` 获得全部 9 个操作。
5. 传输面收敛成**一个 `cp`**：AI 的 `upload_file` + `download_file` 合并为 `cp(src, dst)`，与 `opsctl cp` 共用端点解析、适配器与权限检查。两个入口都支持本地 / SSH / OSS 的任意两端组合，每一端各自按它所属资产类型的权限语义单独审批。
6. `cp` 支持递归（`-r`）与通配复制，展开出的每一条都是独立的审批主体，一次性批量确认。
7. 审批弹窗、grant、审计三处展示与匹配的都是同一个规范形式；预签名 URL 的签名参数不入库。

### 非目标

- **不为 `local` / RDP / VNC 增加统一 `exec` 执行器**（issue 明确要求）。
- **不新增 `opsctl oss` 子命令**（决策 D1）。
- 不实现 bucket 的 create / delete、批量删除（`RemoveObjects`）、创建文件夹（`CreateFolder`）、多版本、生命周期、ACL/policy 管理——issue 未列，桌面端已有的 `CreateFolder` / `RemoveObjects` 保持只在 IPC 面。
- 不给 OSS 的 `exec` DSL 加递归/通配。`object copy` / `object delete` 仍是单对象；批量语义只存在于传输面（§6.5），issue 未列，`object list` 已能满足"看看有哪些"。
- 不把传输面并进 `exec`。`exec` 的入参是某资产类型自己的命令 DSL，而 `cp` 的两端可以属于两个不同类型的资产，没有哪一方的 DSL 能承载它——这正是 [2026-07-20 spec §4](2026-07-20-ai-tool-exec-refactor-design.md) 把传输面留在 `exec` 之外的原因，本轮不推翻它，只是把传输面自己收敛成一个工具。
- 不改动桌面对象浏览器（`internal/app/oss/*`、`frontend/src/components/oss/*`）的任何行为。
- 不重构 `PolicyGroupManager` 之外的前端策略编辑体系。

---

## 3. 命令面：`exec` 的 OSS DSL

### 3.1 语法

沿用 kafka 已验证的 `<family> <verb> [target] [--flags]` 形态（`internal/ai/helper/kafka_dsl.go`），复用 `internal/ai/cmdline` 的引号感知切词，不发明第二套解析器。

family 只有两个：`bucket` 与 `object`。

```
bucket list

object list    <bucket>[/<prefix>]   [--max-keys=N] [--after=<key>]
object stat    <bucket>/<key>
object get     <bucket>/<key>   [--file=<absolute-local-path>] [--max-bytes=N]
object put     <bucket>/<key>   --file=<absolute-local-path> [--content-type=<mime>]
object copy    <bucket>/<key>   --to=<bucket>/<key>
object move    <bucket>/<key>   --to=<bucket>/<key>
object delete  <bucket>/<key>
object presign <bucket>/<key>   [--expiry=<seconds>] [--method=get|put]
```

- `bucket list` 不取 target；所有 `object` verb 必须取 target。多给或少给 target 一律报错，不静默丢弃（与 `ParseKafkaCommand` 对 `needsTarget` 同时当上下界的处理一致）。
- 未知 flag 报错而非忽略。`--max-keys` / `--after` / `--expiry` / `--max-bytes` 的取值形状在权限检查**之前**校验（复用 kafka 的 `kafkaInt` 同款判定），确保"批准之后必然失败"的调用在弹窗之前就失败。
- `scope` 参数对 OSS 无意义，执行器忽略，SKILL.md 说明。

### 3.2 target 与资源标识

target 是单个词 `<bucket>[/<key-or-prefix>]`，含空格时可加引号（`cmdline.Words` 剥引号）。规则：

- 不得为空、不得以 `/` 开头、不得以 `--` 开头（否则 `Render` 之后重新解析会被误读成 flag——与 `KafkaCommand.Render` 拒绝同形 Target 的理由完全一致）。
- 不得有前导/尾随空白（解析期报错）。**允许**内部空白：`object stat "mybucket/My Report.pdf"` 合法。这是与 kafka 的有意分歧，理由见决策 D4。
- `object list <bucket>` 规范化为 `object list <bucket>/`：列举永远是按前缀进行的，bucket 根就是前缀 `<bucket>/`。这让"整桶授权"的规则形态与列举命令自洽。

### 3.3 策略串

每条命令派生 1~3 条**恰好两段**的策略串 `<action> <resource>`，resource 为 `<bucket>/<key>`：

| 命令 | 策略串 |
|---|---|
| `bucket list` | `bucket.list *` |
| `object list B/P` | `object.list B/P` |
| `object stat B/K` | `object.read B/K` |
| `object get B/K` | `object.read B/K` |
| `object put B/K` | `object.write B/K` |
| `object copy S --to=D` | `object.read S` + `object.write D` |
| `object move S --to=D` | `object.read S` + `object.write D` + `object.delete S` |
| `object delete B/K` | `object.delete B/K` |
| `object presign B/K --method=get` | `object.presign.read B/K` |
| `object presign B/K --method=put` | `object.presign.write B/K` |

copy / move 派生多条是本设计的核心安全点：只检查目的地就等于放行"把受限对象复制到可读位置再读"的绕过路径。**任一条被 deny 即整条命令被拒；allow 名单存在时必须每条都命中才放行**（与 `checkCommandPolicyPermission` 对 shell 子命令的处理同构）。

### 3.4 规则匹配（`policy.MatchOSSRule`）

规则与命令都按**第一段空白**切成 `(action, resource)` 两段——不是 `strings.Fields`，见决策 D4。

- 整条规则为 `*` → 匹配任何形状合法的策略串（沿用 `isWildcardAll`）。
- action：大小写不敏感的 `path.Match`。action 内无 `/`，因此 `object.*` 能匹配 `object.read` 与 `object.presign.read`。
- resource：以**第一个 `/`** 切成 `bucket` 与 `key` 两部分分别匹配。
  - bucket 段用 `path.Match`。
  - key 段：规则侧为空（即规则只写了桶名，或以 `/` 结尾）→ 匹配该桶下任意深度的任意 key；否则用 `path.Match`，`*` 不跨 `/`。

由此得到的规则语义（写进 SKILL.md 与前端 hint）：

| 规则 | 含义 |
|---|---|
| `*` | 一切 |
| `object.read mybucket` / `object.read mybucket/` | mybucket 下任意深度的任意对象可读 |
| `object.read mybucket/logs/` | `logs/` 前缀下任意深度可读 |
| `object.read mybucket/logs/*.gz` | 只匹配 `logs/` 这一层的 `*.gz` |
| `object.* mybucket/` | mybucket 上的任意操作 |
| `object.presign.* *` | 任意桶上的任意预签名 |

"规则只写桶名 = 整桶"是刻意的：若按 `path.Match` 字面处理，`mybucket` 只能匹配资源恰好等于 `mybucket` 的串，而 3.2 已把列举规范化为 `mybucket/`，于是一条写着 `mybucket` 的 **deny 规则会一条都匹配不上**——这是静默的 fail-open，正是 kafka `PolicyString` 后置条件那段注释在防的同一类事故。

---

## 4. 权限、审批与授权

### 4.1 检查链

`checkOSSPermission(ctx, assetID, command)` 镜像 `checkKafkaPermission` 的顺序，差别只在"命令 → 策略串"是一对多：

1. 解析规范 DSL → `PolicyStrings()`（1~3 条）。解析失败 → `NeedConfirm`（不整串匹配，与 shell 分支同一 fail-closed 姿态）。
2. 组通用策略 `policy.CheckGroupGenericPolicy(ctx, assetID, policyStrings, policy.MatchOSSRule)`——匹配函数必须是 `MatchOSSRule`，传 `MatchCommandRule` 会让写成 `object.delete *` 的组通用 deny 规则静默失配（与 mongo/redis/etcd 传各自匹配器同理）。deny 命中即返回。
3. 类型策略 `policy.CheckOSSPolicy(ctx, merged, policyStrings)`：先 deny（任一命中即拒），再 allow（allow 非空时必须全部命中才放行，否则 `NeedConfirm`）。
4. 组通用 allow 优先于类型专用的 `NeedConfirm`（与其它类型一致）。
5. DB Grant：`matchGrantForAssetSubCmdsWith(ctx, assetID, policyStrings, "oss", policy.MatchOSSRule)`——每条策略串都必须命中 grant，不能用单条 grant 覆盖 copy/move 的多个资源。
6. 仍为 `NeedConfirm` 时，把有效 allow 名单作为 `HintRules` 回给模型。

注册：`registerPermissionType(asset_entity.AssetTypeOSS, "oss", ossGrantPatterns, checkOSSPermission)`，无别名。

### 4.2 规范化与展示形式

`CanonicalizeOSSCommand` 返回**规范 DSL 串**（`Parse` + `Render` 往返，归一 verb 拼写、flag 顺序与引号），而不是策略串。因此：

- 审批弹窗、`audit_logs.command` 展示 `object move mybucket/a --to=mybucket/b`，是人能读懂的操作；
- 策略与 grant 匹配用的是 `PolicyStrings()` 派生出的两段串。

这与 kafka 有意不同（kafka 把双 token 策略串本身当规范形式，代价是审批弹窗显示 `topic.delete orders`）。OSS 是一对多，无法用单串既做展示又做匹配；选展示态做规范形式，是因为审批弹窗必须让用户看清"这一步会同时读 A 并写 B"。

### 4.3 Grant 归一化：`shellLike bool` → 注册的归一化函数

现状 `permissionTypeHandler.shellLike bool`（`internal/ai/permission/type_registry.go:19`）只有一个消费者 `isShellLikeApprovalType`，被 `NormalizeGrantPatterns` 用来决定"按 shell 子命令拆"还是"整串存一条"。OSS 需要第三种：按 DSL 派生策略串。

把该字段换成 `grantPatterns func(command string) []string`：

- `nil`（database/redis/mongo/kafka/serial/cp）→ 保持 `[]string{cmd}`，行为逐字节不变；
- ssh / k8s → `shellGrantPatterns`，即当前 `NormalizeGrantPatterns` 的 shell 分支原样搬迁；
- oss → `ossGrantPatterns`：解析 DSL → `PolicyStrings()`；**解析失败退回 `[]string{cmd}`**，这样用户在审批弹窗里手写策略形式的 pattern（如 `object.read mybucket/logs/`）也能原样存下并在下次命中。

这是"用注册取代分支"：字段数不变，消费者不变，只是把一个布尔开关换成它本来就在选择的那个函数。

**【实施期更正】`permission` 无法直接调用 `ParseOSSCommand`。** `internal/ai/helper/transfer_ssh.go` 为了取 `GrantToolCp` 已经 import 了 `internal/ai/permission`，反向 import 即 import cycle（任务 5 用探针构建实测确认）。因此上面"`ossGrantPatterns`：解析 DSL → `PolicyStrings()`"在本包内不可编译。

改为沿用本包既有的**推上来**方向（与 `RegisterExecutor` / `RegisterPrecheck` 同一姿态）：`permission` 只声明入口 `RegisterPolicyStrings(canonical string, fn PolicyStringsFunc)`，由 `internal/ai/execimpl` 在 `init()` 里连同执行器一起注册。未注册时按"派生失败"处理并退回 `NeedConfirm`（fail-closed，且漏接线不会被静默放行，只会每条命令都弹审批）。

也就是说，`RegisterPolicyStrings` **确实是一个新增的扩展点**——本节原先那句"不是新增扩展点"不成立，D8 的表述同此更正。它没有引入按类型分支，仍然满足 OCP，但接线责任因此落到任务 9：`execimpl/register.go` 必须调用 `permission.RegisterPolicyStrings(asset_entity.AssetTypeOSS, …)`，否则 §4.1 的三种判定（allow / needconfirm / deny）在生产路径上全部到不了。

#### 前缀形状的资源不落成 grant

`ossGrantPatterns` **丢弃 resource 以 `/` 结尾的策略串**，只把非前缀形状的落库；被丢弃的那条不影响本次审批的放行，只是不产生常驻授权。

理由是一次命令与一条规则对同一个字符串的读法不同。尾随斜杠的 key 是合法的对象——S3 用零字节的 `<prefix>/` 当目录标记，本产品自己就在创建它们（桌面对象浏览器的"新建文件夹"走 `oss_svc.CreateFolder`）。所以 `object delete mybucket/logs/` 说的是**一个**对象。但同一个串按 §3.4 当规则读时，尾随 `/` 意味着**递归前缀**。若原样落库，用户批准一次"删掉这个目录标记"并选"始终允许"，换来的是一条"递归删除 `logs/` 下全部对象"的常驻授权——与 §3.3 那条"批准一件事、拿到另一件"是同一种事故。

丢弃而不是拒绝命令：目录标记该删得掉，只是这一条不该变成可复用的授权。代价是反复操作目录标记每次都要重新审批，这是有意接受的。

用户在审批弹窗里手写的前缀形状 pattern 不受此限——那是用户明确要求的授权范围，与"系统替他推导出来的"不是一回事。

### 4.4 默认策略与内置权限组

- `DefaultOSSPolicy()` → `Groups: [builtin:oss-readonly, builtin:oss-dangerous-deny]`，与其它每一种类型的默认形态一致。
- `builtin:oss-readonly`（allow）：`bucket.list *`、`object.list *`、`object.read *`。
- `builtin:oss-dangerous-deny`（deny）：**只有** `object.presign.write *`。

`EffectiveOSSPolicy` 会无条件追加默认 DenyList（与 redis/etcd/kafka 同构），所以进入这份清单的条目是**用户改不掉的地板**。因此地板里只放一条：

> **预签名 PUT URL 是唯一一个把写权限完全移出本产品的操作。** URL 一旦签发，任何拿到它的人都能在有效期内写入该 key，不再经过任何策略、审批与审计。这与 etcd 把 `auth disable` 放进地板是同一类判断，而 put / copy / move / delete 都属于"每次都要过一次审批"的普通数据操作，对应 etcd 没有把 `put *` / `del *` 放进地板。

> ⚠️ **这一条与 issue 原文有分歧**：issue 写的是"生成可外部访问的预签名 URL 应按风险进入审批"。本设计把 presign **GET** 留在审批（`NeedConfirm`），把 presign **PUT** 提升为默认拒绝。若不同意，改为把 `builtin:oss-dangerous-deny` 从 `DefaultOSSPolicy()` 中移除、只作为可选组提供，presign PUT 即回落为审批。

三种结果因此都自然存在且可测：allow（`object stat`）、needconfirm（`object delete` / `object presign --method=get`）、deny（`object presign --method=put`）。

---

## 5. 执行体

`helper.ExecOSSOnAsset(ctx, asset, command, _ scope)`：解析规范命令 → 调 `oss_svc.New()` 的既有方法 → 序列化为紧凑 JSON。不直接碰 `connpool` / minio client，复用 service 层已有的凭据解析与连接池路径。

返回形状：

| 命令 | 结果 |
|---|---|
| `bucket list` | `{"buckets":[{"name":…,"creationDate":…}]}` |
| `object list` | `oss_svc.ListObjectsResult` 原样（含 `nextContinuationToken` / `isTruncated`） |
| `object stat` | `oss_svc.ObjectItem` 原样 |
| `object get`（无 `--file`） | `{"bucket":…,"key":…,"size":…,"contentType":…,"truncated":bool,"encoding":"utf-8"\|"base64","content":"…"}` |
| `object get --file=P` | `{"bucket":…,"key":…,"size":…,"file":"P"}` |
| `object put --file=P` | `{"bucket":…,"key":…,"size":…}` |
| `object copy` / `object move` | `{"status":"ok","src":…,"dst":…}` |
| `object delete` | `{"status":"ok","bucket":…,"key":…}` |
| `object presign` | `{"url":…,"method":"get"\|"put","expiresIn":N}` |

要点：

- **AI 侧的本地路径必须是绝对路径**，相对路径报错。AI 进程的工作目录不是用户的终端目录，容忍相对路径等于让同一条命令写到无法预期的位置。这沿用即将退役的 `upload_file` / `download_file` 的既有约定（`internal/ai/tool/tools_exec.go:22,44` 的 schema 描述就是 "Absolute path"），新的 `cp` 工具继承它。
- **`object put --file=P` 与 `object get --file=P` 不自己搬字节**，而是调用 §6.2 的传输接缝（本地端点 ↔ OSS 端点）。这样"把本地文件写进对象存储"在全仓只有一份实现，`exec` 与 `opsctl cp` 不可能分叉。`object get` **不带** `--file` 时才直接用 `oss_svc.GetObject` 走内联返回，那是另一种行为，不涉及文件。
- `object get` 不带 `--file` 时按 `--max-bytes`（默认 64 KiB，硬上限 1 MiB）截断读取，非 UTF-8 内容改用 base64 并在 `encoding` 字段标明；`truncated` 为真时提示改用 `--file`。目的是让 AI 能直接读小的配置/日志对象，而不必先落盘。
- `object move` 复用 `oss_svc.MoveObject`（copy 成功才删源），不重写一遍。
- 错误原样返回给调用方，不吞。

注册：`permission.RegisterExecutor(asset_entity.AssetTypeOSS, ExecOSSOnAsset, mustSkillDoc(oss), helper.CanonicalizeOSSCommand)`；从 `execimpl/register.go` 的 doc-only 循环中移除 `oss`。

---

## 6. 两个入口：命令面 `exec` 与传输面 `cp`

统一 `exec` 收敛的是**命令面**。传输面（本地文件系统 ↔ 远端）此前被刻意留在外面——[2026-07-20 spec §4](2026-07-20-ai-tool-exec-refactor-design.md) 的原话是"它们接收本地文件系统路径并流式传输，不是命令形态"。那个判断成立，但结论只做到一半：传输面留下了 **`upload_file` + `download_file` 两个工具 + opsctl cp 的两套 switch**，四份实现干一件事。

本轮把传输面也收敛成**一个 `cp`**，两个入口（AI 工具 / opsctl verb）共用同一套端点适配器。

> 这一步在审计层**已经完成了**：`internal/ai/audit/extractor_default.go:23-33` 把 `upload_file` 与 `download_file` 双双 `RegisterToolAlias(..., "cp")`，并且已经注册了一个 `src` / `dst` 形状的 `cp` 提取器供 `opsctl cp` 使用。审计早就认为这是一个工具面，只是工具层还没跟上。收敛之后那两条别名与两个提取器一起删掉，`cp` 提取器成为唯一入口。

### 6.1 `opsctl exec`（9 个操作的主入口）

注册执行器后自动可用，零新增 verb：

```
opsctl exec s3-prod -- "bucket list"
opsctl exec s3-prod --type oss -- "object list backups/2026/"
opsctl exec s3-prod -- "object get backups/db.sql.gz --file=/tmp/db.sql.gz"
opsctl exec s3-prod -- "object presign backups/db.sql.gz --expiry=600"
```

`cmdExec` 已按 `permission.ApprovalTypeFor(asset.Type)` 取审批类型、按 `prepareExecCommand` 做规范化并把规范串交给审批与策略匹配（`cmd/opsctl/command/exec.go:80-107`），OSS 自动继承。`asset.IsSSH()` 分支不成立，走 `callHandler(..., "exec", …)`，与 database/redis/etcd/kafka/k8s 同一条路。仅需在 `printExecUsage` 的类型列表与示例中补 OSS。

### 6.2 传输接缝：端点适配器注册表

cp 当前是一个 4 分支的 switch，直连路径在 `cmd/opsctl/command/cp.go:93-129`、proxy 路径在 :220-272 各写一份，每个分支都写死 SFTP；AI 侧另有 `handleUploadFile` / `handleDownloadFile`（`internal/ai/tool/tool_handlers_exec.go:91,133`）把同样的 SFTP 流式逻辑再写两遍。加入 OSS 后组合数从 4 涨到 9，继续用分支写就是一张必然漂移的类型矩阵。

改成**端点适配器注册表**，9 种组合塌缩成一条 `openRead(src) → write(dst)`，AI 与 opsctl 共用。

```go
// internal/ai/helper/transfer.go —— 端点解析（<asset>:<path>，经 assetref.Resolve）
// 与适配器注册表同处一处：它们描述的是同一件事，即"什么是一个传输端点"。
type Direction int // DirRead | DirWrite | DirList

// Entry 是一次展开产出的单个可传输条目。只有文件/对象，没有目录——
// 目录由 RelPath 隐含，目的端按需创建。
type Entry struct {
    Path    string // 端点上的完整路径 / 对象 key
    RelPath string // 相对展开基点的路径，决定它在目的端的落点
    Size    int64
}

type TransferAdapter interface {
    // List 展开 pattern。recursive 为真时下钻目录树；pattern 含 glob 元字符时按 glob 展开。
    // 单个具体文件/对象返回单元素切片。
    List(ctx context.Context, asset *asset_entity.Asset, pattern string, recursive bool) ([]Entry, error)
    // OpenRead 打开该端点用于读取；size 未知时返回 -1。
    OpenRead(ctx context.Context, asset *asset_entity.Asset, path string) (io.ReadCloser, int64, error)
    // Write 把 r 的内容写入该端点的 path；size 未知时传 -1。必要时创建中间目录。
    Write(ctx context.Context, asset *asset_entity.Asset, path string, r io.Reader, size int64) error
    // ApprovalSubject 返回这一端点在该方向上必须被授权的审批类型与匹配串。
    ApprovalSubject(path string, dir Direction) (approvalType, command string)
}

func RegisterTransferAdapter(assetType string, a TransferAdapter)
```

三个实现，全部是本次变更的必需项，不是预留：

| 端点 | `List` | `OpenRead` / `Write` | `ApprovalSubject`（read / write / list） |
|---|---|---|---|
| 本地（无资产） | `filepath.Glob` / `filepath.WalkDir`，包级实现，不进注册表 | `os.Open` / `os.Create`（+ `MkdirAll` 父目录） | 无——本地端不需要审批，与现状一致 |
| `ssh` | `sftp.Client.Glob`（已验证存在，语法同 `path.Match`）/ `ReadDir` 递归 | `List` / `Write` 复用既有 `ExecuteWithSFTP`；**`OpenRead` 不能用它**——它是作用域回调，`fn` 一返回就关掉 SFTP 与 SSH 连接，而 `OpenRead` 要交出一个活得比调用更久的 `io.ReadCloser`，因此走 `DialAssetSSH`，把连接寿命绑到返回值的 `Close` 上 | `("cp", remotePath)` ×3，即现状的 `GrantToolCp` + `MatchPathRule` |
| `oss` | `oss_svc.ListObjects` 按前缀分页拉全（glob 在客户端按 `path.Match` 过滤） | `oss_svc.GetObject`（天然返回 `(io.ReadCloser, size)`）/ `oss_svc.PutObject`（天然收 `(io.Reader, size)`，size 传 -1 时 minio 走分片；对象 key 是平的，无需建目录） | `("oss", "object.read B/K")` / `("oss", "object.write B/K")` / `("oss", "object.list B/P/")` |

`ApprovalSubject` 与读写、展开放在同一个接口里，是为了不出现第二张"类型 → 审批语义"的表：一个类型接进传输面，它的四件事一次说清。

#### 端点路径语法

两个入口共用一个解析器（从 `cmd/opsctl/command/helpers.go:66` 的 `parseRemotePathCtx` 上提到共享包）。语法就是既有的 `<asset>:<path>`，`<asset>` 是 ID / 名称 / 组路径，无冒号即本地路径。

**OSS 端点写成 `<asset>:/<bucket>/<key>`**，带前导斜杠。现有解析器靠"冒号后必须以 `/` 开头"把 `C:\windows` 排除在远端之外（`helpers.go:74-77`），OSS 的 `bucket/key` 不带前导斜杠，直接沿用会被判成本地路径。让 OSS 路径也以 `/` 开头，解析规则一个字节不用改，也没有新的歧义；读起来同样自洽——冒号后就是"该资产上的路径"，对象存储上的路径就是 `/bucket/key`。

只保留这一种写法，不同时接受 `s3:bucket/key`（同义写法只会让模型多一种猜法，与 kafka DSL 对同义 operation 的处理同理）。但**写错时必须报错，不能静默当本地路径**：冒号后不以 `/` 开头时，若前缀能解析成一个资产，就报 "remote paths must be written `<asset>:/<path>`"，而不是回落成本地路径去撞一个"文件不存在"。这条守卫只在前缀真的是资产时触发，`C:\windows` 不受影响。

其余形态校验：

- OSS 端点剥掉前导 `/` 后必须是 `<bucket>/<key>`：只有桶名（无 `/`）或以 `/` 结尾（是前缀不是对象）一律报错，不猜。
- 本地相对路径由入口层展开为绝对路径后再交给本地适配器（`opsctl cp ./a.txt` 的命令行手感不变；AI 侧仍要求绝对路径，见 D11）。

#### 逐端点审批

每个远端端点向自己的适配器索取 `ApprovalSubject`，各自独立走一次权限检查：

- SSH 端点 → 类型 `cp`、主体是远端路径，**行为逐字节不变**（`checkFileTransferPermission` + `MatchPathRule`）。
- OSS 端点 → 类型 `oss`、主体是 `object.read <bucket>/<key>` 或 `object.write <bucket>/<key>`，走 §4 的 OSS 策略与 OSS grant。
- 本地端点不产生审批项。
- 任一端被拒即整体失败，且**在任何字节被读写之前**——所有端点审批通过后才开始传输。

因此 `cp ./a s3-prod:/b/k` 落下的 grant 是 `object.write b/k`，与 `exec` 跑 `object put b/k --file=…` 时 `ossGrantPatterns` 派生的 pattern 逐字节相同，两条入口的授权互相复用。

cp 的路径式 grant（`GrantToolCp` + `MatchPathRule`）**只在 SSH 端点上生效**——那套语义是远端文件系统路径，拿去撞 bucket/key 只会误判。两种授权按 `grantItemAppliesTo` 的既有 toolName 隔离机制天然互不可见。

#### 传输执行

审批通过后：`reader, size := srcAdapter.OpenRead(...)` → `dstAdapter.Write(..., reader, size)`。本地↔本地报错（至少一端必须是远端，现状不变）。

- 既有的 `helper.CopyBetweenAssets`（SSH↔SSH 直传）、`handleUploadFile`、`handleDownloadFile` 三处的流式实现全部被适配器组合取代并删除——它们都是"两端各开一次句柄再 `io.Copy`"，与通用路径同构，留着就是第二、三、四份实现。
- **两端是同一个 OSS 资产时**走流式（对象下行再上行，经本地进程），**不是**服务端 copy；此时提示改用 `object copy`，见决策 D12。

### 6.3 AI 工具面：`upload_file` + `download_file` → `cp`

```
cp(src="/tmp/conf.yml",            dst="web-01:/etc/app/conf.yml")   # 原 upload_file
cp(src="web-01:/var/log/app.log",  dst="/tmp/app.log")               # 原 download_file
cp(src="web-01:/var/log/app.log",  dst="s3-prod:/logs/app.log")      # 新：SSH → OSS
cp(src="s3-prod:/artifacts/a.tar", dst="/tmp/a.tar")                 # 新：OSS → 本地
cp(src="web-01:/var/log", dst="s3-prod:/logs/", recursive=true)      # 新：递归（§6.5）
cp(src="s3-prod:/dist/*.js", dst="/tmp/js/")                         # 新：通配（§6.5）
```

- 参数是 `src` / `dst` 两个字符串 + 可选 `recursive` 布尔，与 `opsctl cp` 同一套路径语法、同一个解析器、同一份文档。`src`/`dst` 正是 `internal/ai/audit/extractor_default.go` 里那个 `cp` 提取器已经在等的形状。
- `IsSerial: true`，沿用 `upload_file` / `download_file` 的既有标注。
- 权限：`handleCp` 解析两端 → 展开（必要时先过展开授权）→ 取每条的 `ApprovalSubject` → 批量 `checker` 检查。opsctl 那条已预检的路径走 `permission.RequireCheckerOrPreapproved`，与 `handleExec` 完全同构。**不设 doc gate**：`exec` 的门禁存在的理由是"每种资产类型的命令语法都不同，模型必须先读文档"，而 cp 的语法与类型无关，一句工具描述说得完。
- 返回 `{"transferred":N,"bytes":B,"skipped":[…]}`；单源时 `N` 为 1。原来两个工具各自返回的 `{"message":"file uploaded successfully","remote_path":…}` 一并退役。

**这是一次破坏性的工具面变更**（工具数 15 → 14），需要发版说明：模型 prompt 里 `upload_file` / `download_file` 的名字全部消失。受影响的写死清单：`internal/ai/runner/prompt_builder.go:161,163`、`internal/ai/tool/tool_registry.go:57-58`、`tools_exec.go:17,38`、`docs/ARCHITECTURE.md:107`，以及 `tools_test.go:37,65` 的工具名清单 + 穷尽性数量断言（该断言的注释明说"删工具时必须同步改这里"）与 Serial 清单、`handler_test.go:124`。

### 6.4 `opsctl cp`：任意两端组合

```
opsctl cp ./dump.sql.gz s3-prod:/backups/2026/dump.sql.gz     # 本地 → OSS
opsctl cp s3-prod:/backups/2026/dump.sql.gz ./dump.sql.gz     # OSS → 本地
opsctl cp web-01:/var/log/app.log s3-prod:/logs/app.log       # SSH → OSS
opsctl cp s3-prod:/artifacts/app.tar web-01:/opt/app.tar      # OSS → SSH
opsctl cp s3-src:/a/x.bin s3-dst:/b/y.bin                     # OSS → OSS
opsctl cp web-01:/etc/hosts web-02:/tmp/hosts                 # SSH → SSH（现状不变）
```

`cmdCp` 收敛成与 `cmdExec` 同构的三步：**解析两端 → 逐端点审批 → `callHandler(ctx, handlers, "cp", {src, dst})`**。`requireCpApproval`（`cp.go:144`）本来就是按端点循环，只需把每个 target 的 `Type` / `Command` 从写死的 `("cp", path)` 改为向适配器索取；两处 4 分支 switch 连同 `helper.CopyBetweenAssets` 一起删除。

- **proxy 快路径**（`cpSSHProxyClientFn`）只在**所有远端端点都是 SSH** 时启用，即完全等价于现状的四种组合；任何一端是 OSS 就走直连流式路径。proxy 复用的是桌面端的 SSH 连接池，对 OSS 没有对应能力。
- 两端同一 OSS 资产时在 stderr 打一行提示，指向 `opsctl exec <asset> -- "object copy a/x --to=a/y"`——那条命令走 `oss_svc.CopyObject`，数据不出服务端。

### 6.5 递归与通配

```
opsctl cp -r ./dist s3-prod:/bucket/releases/v2/      # 本地目录树 → OSS 前缀
opsctl cp -r 's3-prod:/bucket/logs/' ./logs/          # OSS 前缀 → 本地目录树
opsctl cp 'web-01:/var/log/*.log' s3-prod:/bucket/logs/   # 远端 glob → OSS
opsctl cp ./a.txt ./b.txt web-01:/opt/app/            # 多源（shell 展开的结果）
```

```
cp(src="web-01:/var/log", dst="s3-prod:/bucket/logs/", recursive=true)
cp(src="s3-prod:/bucket/dist/*.js", dst="/tmp/js/")
```

#### 何时算"多源"

满足任一即为多源：`recursive` 为真、源路径含 glob 元字符（`*` `?` `[`）、或（仅 CLI）给了多个源。**多源时目的地必须以 `/` 结尾**，否则报错。

目的路径 = `<dst 基点> + <entry.RelPath>`，`RelPath` 相对展开基点：glob `'/var/log/*.log'` 的基点是 `/var/log`，递归 `/var/log` 的基点是它自己，OSS 的 `/bucket/logs/` 基点是前缀 `logs/`。

**不复刻 POSIX `cp` 的目的地推断**（`cp -r a b`：b 存在则落 `b/a`，不存在则落 `b`）。那套语义要探测目的地是否存在、是否是目录，结果依赖一次 TOCTOU 式的探测，而这里的目的地必须在**审批之前**就完全确定——批的是哪些具体路径，写的就必须是哪些。强制尾随 `/` 让目的地纯由输入决定，没有探测，没有惊喜。见决策 D16。

#### 展开与审批：两段

**第一段 · 展开授权。** 枚举读的是元数据（名字、大小），不是内容，但 `cp -r web-01:/ ./x` 能把整棵文件树的结构拖出来，所以它自己要过一次授权：向源端适配器要 `ApprovalSubject(base, DirList)`——SSH 是 `("cp", <base>)`，OSS 是 `("oss", "object.list <bucket>/<prefix>/")`。OSS 侧因此在默认只读组下自动放行（`object.list *` 在 `builtin:oss-readonly` 里），常见路径上完全无感；SSH 侧是一次新的确认，批准后落 grant，重复执行不再打断。

**第二段 · 传输授权。** 展开完成后，为每个 entry 生成源读 + 目的写两个主体，去重，**一次性塞进同一个审批对话框**——`CommandConfirmFunc(ctx, kind string, items []ApprovalItem)` 本来就收切片，`batch_exec` 已经在用这条路。

关键点：**审批主体始终是具体路径 / 具体 key，不是递归 pattern。** 因此 `policy.MatchPathRule` 一个字节都不用改——它同时是 `local_write` / `local_edit` 白名单的匹配器（`local_tool_gate.go:173`、`path_policy.go` 的注释明写共用），让它支持递归会顺带放宽本地写授权，是一次与 OSS 毫无关系的安全回归。见决策 D17。

用户点"始终允许"时，`SaveGrantPatternsForApproval` 按条落 grant，重跑同一条 cp 直接命中、不再打断；用户若在弹窗里把条目自己改写成 `/opt/app/*` 这类通配 grant，那是他用既有语义做的显式决定，不是系统替他放宽。

**条目上限 200。** 超出即报错，报出实际条数并要求收窄 pattern，**绝不静默截断**。上限不是性能考虑，是审批质量：五千条的对话框不是决策，是橡皮图章。

#### 失败、符号链接、进度

- **快速失败**：任一 entry 传输出错立即中止，退出码非零，报 `已传输 N/M` 并点名失败的那条。不采用 POSIX `cp -r` 的"继续并最终非零"——每一个已传输的字节都是一次已批准的副作用，出意外后继续会留下一个看起来完整、实际残缺的目的地。重跑代价很低：grant 已经落库，第二次不再打断。
- **不跟随符号链接**：本地与 SFTP 递归遇到 symlink 一律跳过并计数上报。跟随会让 `cp -r ./dir` 因为一条指向 `/` 的链接变成整机 dump，而那些逃逸出去的路径根本不在用户审阅过的清单里。
- **进度**：opsctl 按条打 stderr；AI 侧只返回汇总 `{"transferred":N,"bytes":B,"skipped":[…]}`，不逐条回流（会淹掉上下文）。`ctx` 取消沿用既有的 `closeOnCancel` 机制。

#### 两个入口的形态差异

CLI 是 `opsctl cp [-r] <src>... <dst>`（N 源 1 目的），因为用户的 shell 会先把本地 glob 展开成多个参数，cp 必须收得下。**远端 glob 必须加引号**（`'web-01:/var/log/*.log'`），否则本地 shell 会先动手——与 `scp` 的既有习惯一致，写进 usage 与 SKILL.md。

AI 工具的 `src` 保持单个字符串 + `recursive` 布尔：模型没有 shell，不需要 N 元形态，多个来源就调两次。

---

## 7. 审计与脱敏

- 命令摘要走既有链路：`runner.auditMiddleware` 在类型注册了 `CanonicalizeFunc` 时覆盖 `ToolCallInfo.Command`，OSS 因此落库的是规范 DSL 串。无需新增 extractor。
- `aictx.RecordDecision` 记录 decision / decision_source，三种结果（allow / needconfirm 后的用户选择 / deny）都成行。规范化失败、门禁未过等短路路径由 `recordShortCircuit` 覆盖，OSS 自动继承。
- **预签名 URL 脱敏**：在 `internal/pkg/auditredact` 的 `textRedactors` 增加一条规则，把 SigV4 与遗留 V2 签名查询参数的值替换为 `<redacted>`：`X-Amz-Signature`、`X-Amz-Credential`、`X-Amz-Security-Token`、`Signature`、`AWSAccessKeyId`。加在 `auditredact` 而不是 OSS 执行器里，是因为它是本仓脱敏的唯一入口（`audit.WriteToolCall` 对 command / result / request / error 四处统一调用），在执行器里另做一次等于第二份真相，且只覆盖 result 一处。
- 返回给调用方（模型 / opsctl stdout）的 URL 是完整可用的——脱敏只发生在落库那一层。
- `cp` 的审计形态收敛为一种：工具名 `cp`，参数 `src` / `dst`（opsctl 侧还带 `source_asset_id` / `destination_asset_id`，见 `buildCpAuditArgs`），摘要由既有的 `cp` 提取器产出。`upload_file` / `download_file` 的两条 `RegisterToolAlias` 与两个提取器一并删除——别名存在的唯一理由就是把它们归到 `cp` 这个面上，工具真的叫 `cp` 之后别名就是纯噪音。跨协议传输的两条端点审批各自经 `aictx.RecordDecision` 落 decision，与 SSH↔SSH 两次审批的现状一致。

---

## 8. 前端影响

1. `frontend/src/lib/assetTypes/oss.ts`：`policy: undefined` 改为完整 `PolicyDefinition`（`policyType: "oss"`，allow_list / deny_list 两个字段），并删掉那句已经不成立的注释。
2. i18n（`zh-CN` 与 `en` 各一份，各自用地道表达，不逐字对译）：`asset.ossPolicy`、`asset.ossPolicyHint`、`asset.ossPolicyAllowList`、`asset.ossPolicyDenyList`、`asset.ossPolicyPlaceholder`、`asset.policyTestOSSPlaceholder`。
3. `PolicyTestPanel.tsx`：占位符映射表加 `oss` 一项（纯数据）。
4. `PolicyGroupManager.tsx`：`POLICY_TYPES` 加 `{ key: "oss", label: "OSS" }`。该文件现有四处 `editState.policyType === "kafka" ? A : B` 的三元式（:552/:571/:577/:596）无法再表达三种取值——把这四处换成一张 `policyType → i18n key` 的小映射表。这是"光标下的就地修正"：不扩大分支，而是把已经在漂移的分支换成数据。
5. `ApprovalBlock.tsx` / `OpsctlApprovalDialog.tsx`：类型→图标映射加 `oss`（复用 `resolveAssetIcon` 已有的 S3 图标语义，不新造图标）。
6. `ai/ToolBlock.tsx` 的 `toolIcons`（:19-34）加 `cp`。`upload_file` / `download_file` 本来就不在这张表里（一直落到 `Terminal` 兜底），所以这是补齐而不是替换；两处测试里把已删除的 `upload_file` 换成 `cp`（`aiStore.test.ts:2111`、`ToolBlock.test.tsx:12,28` —— 它们只是拿它当任意工具名用，留着一个不存在的名字会误导后来人）。
7. `ApprovalBlock.tsx` / `OpsctlApprovalDialog.tsx`：审批项超过 10 条时折叠为一行摘要（"从 `web-01:/var/log` 复制 137 个文件到 `s3-prod:/bucket/logs/`"）+ 展开查看全部。递归 cp 会一次送来上百条 `ApprovalItem`，原样铺开没法读。**安全模型不变**：折叠只是呈现，批准的仍然是那 N 条具体主体（D17）。这一项**不做原型**——它是既有列表上的标准渐进披露，用仓内已有的折叠交互模式，没有新的布局或层级决策；摘要行的文案在 i18n 里补两个 key。

不改动：桌面对象浏览器、`OSSConfigSection`、`OSSDetailInfoCard`。

**没有 UI 原型，也不需要**：本节全部是往既有组件的数据表里加一项（策略定义、i18n key、图标映射、占位符），复用的是 etcd/kafka 已经在跑的同一套 `PolicyDefinition` 渲染路径与 `CommandPolicyCard` / `PolicyTagEditor` 组件，没有任何新的布局、密度或层级决策。第 4 点把四处三元式换成映射表是同文件内的等价改写，视觉输出不变。无障碍同理不受影响：没有新增交互控件、焦点顺序或色彩语义。

---

## 9. 数据与迁移

- `internal/model/entity/policy/policy.go`：新增 `OSSPolicy`（`AllowList` / `DenyList` / `Groups`，与 `KafkaPolicy` 同形）、`IsEmpty()`、`DefaultOSSPolicy()`、两个 `Builtin*` 常量；`Holder` 接口加 `GetOSSPolicy()`。
- `internal/model/entity/policy/registry.go`：`PolicyKindOSS = "oss"`，`RegisterDefaultPolicy("oss", …)`。
- `Asset` 侧复用既有的 `command_policy` 列（`a.CmdPolicy`），与 kafka/etcd 完全一致，**无需资产表迁移**。
- `Group` 侧需要新列：`migrations/202607310001_group_oss_policy.go`，`ALTER TABLE groups ADD COLUMN oss_policy TEXT`，Rollback 为 no-op（SQLite 不支持 DROP COLUMN，与 `202605260001_group_etcd_policy.go` 一致）。`group_entity.Group` 加 `OssPolicy` 字段与 Get/Set 方法。
- `policy_group_entity`：`PolicyTypeOSS` 常量 + `registerBuiltinGroups(PolicyTypeOSS, …)` 一段。

其余按 [`docs/references/adding-an-asset-type.md` §"Brand-new policy kind"](../../references/adding-an-asset-type.md) 的清单执行：`ai/policy` 的 kind alias、`registerPolicyKind`、`testOSSPolicy`、`ResolveOSSGroups`。

---

## 10. 文档

- `internal/ai/skills/oss/SKILL.md` 重写：保留 `put_asset` 的配置字段表，替换掉 "no command surface" 段，补命令语法、flag 参考、规则语义、审批粒度说明、以及"key 含空白可用引号、但不能有前后空白"这条限制。frontmatter 的 `description` 同步。
- `plugin/opsctl/skills/opsctl/SKILL.md`：`exec` 的类型覆盖列表补 OSS；`cp` 段改写为"任意两端组合 + `-r` + 通配"，说明每端各自审批、OSS 端点写作 `<asset>:/<bucket>/<key>`、多源时目的地必须以 `/` 结尾、远端 glob 必须加引号、200 条上限、同资产 OSS 复制应改用 `object copy`。`printCpUsage` 同步。
- `internal/ai/runner/prompt_builder.go:161,163`：两处写死的 `upload_file / download_file for SFTP transfer` 改为 `cp(src, dst)`，并说明它跨 SSH 与对象存储。
- `docs/ARCHITECTURE.md:107`：那句 "`upload_file` / `download_file` run the same gate under the `cp` face…" 改为 `cp` 单工具的表述——它现在描述的正是本轮要消除的中间状态。
- `docs/references/adding-an-asset-type.md`：若其中列出的"已有 policy kind"清单需要同步，就地更新。
- 更正 `internal/ai/execimpl/coverage_test.go` 与 `help_coverage_test.go` 中把 OSS 描述为 doc-only / 无 PolicyKind 的注释与断言。

---

## 11. 设计决策

| # | 决策 | 被否方案与理由 |
|---|---|---|
| **D1** | CLI 入口复用 `opsctl exec`，不新增 `opsctl oss` 子命令。 | 否决"新增 `opsctl oss` verb"：仓库已在 #123 中删除 `sql`/`redis`/`mongo` 三个按类型开的 verb 并收敛到 `exec`（`cmd/opsctl/command/exec.go` 的 `cmdExec` 注释记录了这次收敛），再开一个只服务 OSS 的 verb 是直接回退，且要在 opsctl 侧重写一份参数解析（第二份真相）。也否决"两者都要"（DSL + 友好包装）：包装层的唯一价值是手感，代价是一张需要维护和测试的映射表。**用户已确认。** |
| **D2** | 上传/下载同时支持 `exec` 的 `--file=` 与 `opsctl cp`；`opsctl cp` 支持本地/SSH/OSS 的任意两端组合。 | 否决"只做 `--file=`"：`opsctl cp` 是本仓传输操作的既有肌肉记忆，OSS 缺席会让用户去猜。否决"cp 只支持本地↔OSS、跨协议报错"：把"从服务器抓日志丢进对象存储"这类最常见的运维动作留给用户手动落一次盘。**用户已确认要两者都支持、且要跨协议直传。** |
| **D3** | cp 改用**端点适配器注册表**（`OpenRead` / `Write` / `ApprovalSubject`），9 种两端组合塌缩为一条读→写；`helper.CopyBetweenAssets` 被取代并删除。 | 否决"在 cp 里按 `asset.IsOSS()` 分支"：两端各 3 种形态 = 9 种组合，proxy 与直连还各要一份，正是 AGENTS.md 禁止的类型矩阵，且必然漂移。否决"把 cp 改写成 `exec` 的 DSL 串"（本设计上一版的方案）：SSH↔OSS 根本没有一条 DSL 命令能表达"从 A 的 SFTP 读、写进 B 的对象存储"，该方案只在本地↔OSS 时成立。适配器有且只有 3 个实现（local/ssh/oss），全部是本次必需，不是预留扩展点。 |
| **D4** | 策略串按**第一段空白**切成两段，因此 object key 允许含空格。 | 否决"照搬 kafka 的 `strings.Fields` + 拒绝含空白的 target"：kafka 那样做的代价是名字含空格的资源完全不可达，对 topic/consumer group 可以接受，对 object key 不行（`My Report.pdf` 是对象存储里再常见不过的 key）。切第一段空白同样保证恒为两段，deny 规则不会因为分段数不对而静默全失配——kafka 那条约束要防的正是这一点，而不是空格本身。 |
| **D5** | 规则里"只写桶名 / 以 `/` 结尾"表示该前缀下**任意深度**递归匹配。 | 否决"纯 `path.Match`"：`*` 不跨 `/`，用户无法写出"整个桶"或"某前缀下全部"，而这正是对象存储最常见的授权粒度；更糟的是一条写着 `mybucket` 的 deny 规则会一条都匹配不上（fail-open）。否决"引入 `**` 语法"：发明本仓不存在的通配符，且 `path.Match` 不支持，要自己实现匹配器。 |
| **D6** | `Canonicalize` 返回规范 **DSL** 串（展示态），策略/grant 匹配用派生的策略串。 | 否决"照 kafka 让规范形式就是策略串"：OSS 的 copy/move 是一对多，单串无法同时承载展示与匹配；且审批弹窗必须让用户看清一条 move 会同时读源、写目的、删源。 |
| **D7** | copy/move 派生多条策略串，全部参与 deny/allow/grant 匹配。 | 否决"只按目的地检查"：`object copy secrets/key public/key` 随后 `object get public/key` 即可绕过对 `secrets/` 的读限制。否决"只按最危险的单一动作检查"：同一问题。 |
| **D8** | `permissionTypeHandler.shellLike bool` 改为注册的 `grantPatterns func(string) []string`；OSS 的派生函数经**新增的** `permission.RegisterPolicyStrings` 由 `execimpl` 推上来。 | 否决"让 `NormalizeGrantPatterns` 的非 shell 分支统一按 `\n` 拆"：会改变 database 多行 SQL 审批的既有落库形态，是本 issue 之外的行为变更。否决"在 `NormalizeGrantPatterns` 里加 `if approvalType == "oss"`"：正是 AGENTS.md 禁止的按类型分支。**【实施期更正】**原表述称"不是新增扩展点"，不成立：`helper` 已 import `permission`（取 `GrantToolCp`），`permission` 直接调 `ParseOSSCommand` 即 import cycle，故必须新增一个注册入口，接线责任落在任务 9（详见 §4.3）。 |
| **D9** | `builtin:oss-dangerous-deny` 只含 `object.presign.write *`，且进入默认策略（不可覆盖地板）。 | 否决"把 delete/put 也放进地板"：地板不可移除，等于单方面取消 AI 的写能力，重蹈 `BuiltinKafkaMessageWriteDeny` 注释记录的那个坑。否决"地板为空、presign PUT 也只审批"：预签名 PUT 是唯一把写权限移出本产品、后续不再经过任何审批与审计的操作。**与 issue 原文有分歧，见 §4.4 的标注，可否决。** |
| **D10** | 预签名 URL 脱敏加在 `internal/pkg/auditredact`。 | 否决"在 OSS 执行器里返回前脱敏"：那样连调用方拿到的 URL 都是坏的。否决"在 OSS 执行器里另写一份落库脱敏"：`auditredact` 是本仓脱敏的唯一入口，第二份实现会漏掉 command / request / error 三列。 |
| **D11** | AI 侧的本地文件路径必须绝对；opsctl 侧维持相对路径由入口展开。 | 否决"AI 侧也按 cwd 解析相对路径"：AI 进程的 cwd 不是用户的终端目录，写到哪里不可预期。这条是 `upload_file` / `download_file` schema 的既有约定，`cp` 继承。`opsctl cp ./a.txt` 的命令行手感不变——相对路径在入口层展开为绝对路径后才进适配器。 |
| **D12** | 同一 OSS 资产两端的 `opsctl cp` 走流式，不做服务端 copy；改为在 stderr 提示改用 `object copy`。 | 否决"给适配器加可选的 `DirectCopy` 接口"：那是一个只有一种组合能命中的扩展点，且要在 cp 里加一次"两端能否直连"的判定。服务端 copy 的能力本来就在 `exec` 的 `object copy` 里，指过去比在 cp 里再实现一次更诚实。否决"静默流式、不提示"：用户不会知道自己让 10 GB 对象绕了一圈本地进程。 |
| **D13** | cp 的每个远端端点各自按其资产类型的权限语义单独审批；SSH 端点行为不变。 | 否决"整次传输用单一审批类型"：那意味着要么拿 OSS 的 bucket/key 去撞路径式 grant，要么拿 SSH 路径去撞 OSS 策略，两种都是误判。否决"跨协议时降级为一次笼统确认"：跨协议恰恰是最需要说清"读了哪个、写了哪个"的场景。 |
| **D14** | 删除 AI 的 `upload_file` / `download_file`，合并为 `cp(src, dst)`，与 `opsctl cp` 共用适配器。 | 否决"保留两个工具、只让它们内部改调适配器"：那样 AI 仍然只能本地↔SSH，SSH↔OSS 够不着，而工具描述里还要解释为什么 `upload_file` 不能上传到对象存储。否决"再加第三个 `transfer` 工具"：三个工具做一件事。**依据**：审计层早已把两者 `RegisterToolAlias(..., "cp")` 并注册了 `src`/`dst` 形状的 `cp` 提取器（`extractor_default.go:23-33`），`permission.GrantToolCp` 也早就是 `"cp"`——工具面是这条链上唯一还没收敛的一环。代价是一次破坏性变更，见 §6.3。 |
| **D15** | OSS 端点路径写作 `<asset>:/<bucket>/<key>`（带前导斜杠）；冒号后不以 `/` 开头但前缀能解析成资产时**报错**。 | 否决"接受 `s3:bucket/key`"：现有解析器靠"冒号后以 `/` 开头"排除 `C:\windows`（`helpers.go:74-77`），放宽它要么误判 Windows 路径，要么要先解析前缀才能判断是不是远端。否决"两种写法都接受"：同义写法让模型多一种猜法。否决"写错时静默回落成本地路径"（现状行为）：那会把一次拼错的远端路径变成一句"文件不存在"，这正是 AGENTS.md 说的"吞掉错误"。 |
| **D16** | 多源传输的目的地必须以 `/` 结尾；不复刻 POSIX `cp` 的目的地推断。 | 否决"照抄 `cp -r a b`（b 存在→`b/a`，不存在→`b`）"：那要先探测目的地是否存在且是目录，而目的路径必须在**审批之前**完全确定——批的是哪些具体路径，写的就得是哪些。探测式推断在探测与写入之间留了一条 TOCTOU 缝，且"少打一个斜杠就写到别处"是这类命令最经典的事故。 |
| **D17** | 递归/通配的审批主体是**展开后的每一条具体路径**，批量塞进一次对话框；`MatchPathRule` 不改。 | 否决"批一条递归 pattern（如 `/opt/app/**`）"：`MatchPathRule` 同时是 `local_write` / `local_edit` 白名单的匹配器（`local_tool_gate.go:173`，`path_policy.go` 注释明写共用），让它支持递归会顺带放宽本地写授权——一次与 OSS 完全无关的安全回归。否决"逐条弹 N 次对话框"：不可用。批量项走的是 `CommandConfirmFunc` 已有的 `[]ApprovalItem` 切片，与 `batch_exec` 同一条路。 |
| **D18** | 展开（枚举）自己要过一次授权，主体是源端基点（`DirList` 方向）。 | 否决"枚举不需要授权，它只是元数据"：`cp -r web-01:/ ./x` 能把整棵文件树的结构拖出来。OSS 侧映射到已有的 `object.list`，在默认只读组下自动放行，常见路径无感；SSH 侧复用 `cp` 面，批准后落 grant。 |
| **D20** | `ossGrantPatterns` 丢弃 resource 以 `/` 结尾的策略串，不落成常驻 grant（§4.3）。 | 实施期修正，由任务 4 的评审提出、用户裁定。否决"DSL 直接拒绝尾随斜杠的 target"：目录标记是本产品自己创建的合法对象（`oss_svc.CreateFolder`），拒绝等于 exec/cp 删不掉自己建的东西。否决"原样落库、只在文档里警告"：那把一个安全边界交给用户读文档来守，与 §3.3 已经修掉的那类"批准一件事、拿到另一件"同形。 |
| **D19** | 条目上限 200，超出报错；快速失败；不跟随符号链接。 | 否决"不设上限"：五千条的审批对话框是橡皮图章，不是决策。否决"静默截断到前 200 条"：一次看起来成功、实际只复制了一部分的传输，比报错糟得多。否决"POSIX 式继续并最终非零"：每个已传输字节都是已批准的副作用，出意外后继续会留下看似完整、实则残缺的目的地；重跑代价很低（grant 已落库）。否决"跟随符号链接"：一条指向 `/` 的链接能把 `cp -r ./dir` 变成整机 dump，而逃逸出去的路径不在用户审阅过的清单里。 |

---

## 12. 测试决策

按"最少、最高的接缝"选择，优先可观察边界：

| 接缝 | 验证什么 | 既有先例 |
|---|---|---|
| `internal/ai/helper`（OSS DSL） | `ParseOSSCommand` 的 verb/target/flag 校验；`Parse`→`Render` 往返；`PolicyStrings()` 对 9 个操作产出的策略串（含 copy/move 的多条）；每条结果恒为两段 | `kafka_dsl_test.go`、`mongo_command_test.go` |
| `internal/ai/policy`（`MatchOSSRule` / `CheckOSSPolicy`） | §3.4 表格里的每条规则语义；deny 优先于 allow；allow 非空时必须全命中 | `kafka_policy_test.go`、`redis_policy_test.go` |
| `internal/ai/permission`（`CheckPermission(ctx,"oss",…)`） | **三种结果各一组**：allow（`object stat`，默认只读组）、needconfirm（`object delete`）、deny（`object presign --method=put`，默认地板）；grant 按多条策略串逐条匹配；OSS grant 与 `cp`/命令面 grant 相互不可见 | `kafka_policy_test.go`、`grant_isolation_test.go` |
| `internal/ai/tool`（`handleExec`） | **高风险操作在审批前无副作用**：规范化失败 / 门禁未过时，checker 与执行器都未被调用，且记了 `decision=deny` 的审计行 | `tool_handlers_unified_test.go` |
| `internal/ai/helper`（端点解析） | `<asset>:/<path>` 的解析；`C:\windows` 仍判为本地；冒号后缺前导 `/` 且前缀是资产时**报错**而非回落；OSS 端点 `<bucket>/<key>` 形态校验（缺 `/`、尾随 `/` 报错） | `helpers.go` 现有的 `parseRemotePath` 测试上提 |
| `internal/ai/helper`（传输适配器） | 三个适配器的 `ApprovalSubject` 在 read/write/list 三个方向各自产出的类型与主体串；`OpenRead`→`Write` 的字节往返（OSS 侧用 `mock_ossclient`，SSH 侧沿用既有 SFTP 测试设施） | `oss_svc/mock_ossclient`、`ssh_helper_test.go` |
| `internal/ai/helper`（展开） | `List` 的 glob 与递归展开；`RelPath` 相对基点计算（glob 基点 = 通配前的最后一层目录；递归基点 = 源本身；OSS 基点 = 前缀）；symlink 被跳过并计数；OSS 的分页拉全 | `oss_svc/mock_ossclient`、本地端可用 `t.TempDir()` |
| `internal/ai/tool`（`handleCp`，单源） | **9 种两端组合**各自向哪些端点发起了权限检查、类型与主体串是什么（本地端无检查；SSH 端是 `cp`+路径；OSS 端是 `oss`+`object.read/write`）；**任一端被拒时没有任何字节被读写**；本地↔本地报错；原 `upload_file` / `download_file` 的审批不变式在新工具上仍成立 | `file_transfer_approval_test.go` 就地迁移（它现在锁的正是"传输前必须过审批"） |
| `internal/ai/tool`（`handleCp`，多源） | **硬不变式本体**：展开出的每一条都出现在批量审批项里，且拒绝时零字节读写；展开授权先于传输授权发生；多源时目的地缺尾随 `/` 报错；`RelPath` → 目的路径的拼装；条目数超 200 报错且**不截断**；快速失败时上报 `N/M` 与失败条目 | 同上 + `batch_exec` 的批量审批测试形态 |
| `cmd/opsctl/command`（`cmdCp`） | 端点审批与 `callHandler("cp", …)` 的接线；`-r` 与 N 源 1 目的的参数解析；两端同一 OSS 资产时的 stderr 提示；proxy 快路径只在全 SSH 时启用 | `cp_approval_test.go`（已用 `cpApprovalFn` / `cpSSHProxyClientFn` 变量替换审批与 proxy 入口） |
| `internal/ai/audit` | `cp` 提取器成为唯一入口后摘要仍正确；`upload_file` / `download_file` 的别名与提取器已移除 | `audit_integration_test.go`、`internal/ai/audit_test.go` 就地迁移 |
| `internal/ai/execimpl` | OSS 不再是 doc-only：有执行器、进 `RegisteredExecTypes()`；`TestEveryPolicyKindTypeHasExecutor` 现在真的覆盖 OSS | 现有 `coverage_test.go` / `help_coverage_test.go` 就地更新 |
| `internal/pkg/auditredact` | SigV4 与 V2 签名查询参数被替换；URL 其余部分保留 | `redact_test.go` |

**不做自动化、靠人工验证的部分**（按 [`docs/VERIFICATION.md`](../../VERIFICATION.md) 的可观察方式）：

- 对真实 S3/MinIO 的 9 个操作往返——用 `opsctl exec` 打一遍，读 `logs/opskat.log` 与 `opskat.db` 的 `audit_logs` 核对 decision / command / result。
- 对真实 MinIO + 一台 SSH 资产跑 SSH→OSS 与 OSS→SSH 各一次，核对字节一致、两条审批各自出现、审计行的 `source_asset_id` / `destination_asset_id` 正确。
- 递归实测：`cp -r` 一棵含嵌套子目录的真实目录树到 OSS 前缀再拉回来，核对树形与字节一致；跑一次含 symlink 的目录，确认被跳过并计数。
- 桌面审批弹窗对 OSS 条目的渲染（类型徽章、多资源 move 的展示、cp 跨协议时两条不同类型的审批项并列、上百条时的折叠摘要与展开）。
- 前端策略编辑器与策略测试面板出现 OSS 项。

e2e（`e2e/tests/`）本轮不新增 OSS 用例：现有 `ai-exec` 系列依赖脚本化的 OpenAI mock，不含对象存储后端，接一个 MinIO 容器进 e2e 属于独立的基建工作。

---

## 13. 验收标准对照（issue §验收标准）

| issue 要求 | 本设计对应 |
|---|---|
| AI 能发现 OSS 语法说明并通过受控入口执行 | §10 重写 SKILL.md；`help(asset="oss")` 已可按类型名取文档；`exec` 门禁强制先 help 后 exec |
| opsctl 能按 ID / 名称 / 组路径定位 OSS 资产并执行 | §6.1，`resolveAsset` 已支持三种形态，无需改动 |
| AI 与 CLI 共用同一执行与权限 seam | 命令面 §6.1 走同一个 `handleExec`；传输面 §6.3/§6.4 走同一个 `handleCp`，`exec` 的 `--file=` 也走同一套适配器。OSS 端点审批走同一个 `checkOSSPermission`、落同一形态的 grant |
| 拒绝 / 需审批 / 允许三种结果均有测试 | §12 第三行 |
| 高风险操作在审批前无副作用 | `handleExec` 的顺序不变式（类型断言 → 执行器查找 → 门禁 → 规范化 → precheck → 权限检查 → 执行）；`cp` 的全部端点审批排在任何读写之前，递归/通配展开出的每一条也各自是审批主体（§6.2 / §6.5，硬不变式本体）。测试见 §12 的 `handleExec` 与两行 `handleCp` |
| 操作被正确审计，错误返回调用方而不被吞掉 | §7；§5"错误原样返回" |
| 不记录凭证或预签名 URL 的敏感查询参数 | §7 的 `auditredact` 新规则 |
| 不为 local / RDP / VNC 增加执行器 | §2 非目标 |
| 按注册机制扩展，不在 dispatcher 里新增类型分支 | `RegisterExecutor` / `registerPermissionType` / `registerPolicyKind` / `registerBuiltinGroups` / `RegisterTransferAdapter` 全部是注册；`opsctl cp` 原有的 4 分支类型 switch 被适配器注册表**消除**，不是被扩大（D3） |
