# AI 工具与秘密数据边界修正

> Status: Approved
> Owner: OpsKat maintainers
> Last updated: 2026-08-14

**Objective:** 删除 AI、审批、直接交互和通用 Audit 中的值改写，让用户、模型、审批者和审计查看者看到对应边界实际接收的数据；只有明确拥有 write-only secret 契约的 producer 生成字段白名单 Audit request。安全查询通过窄 DTO 省略秘密字段，视觉型秘密输入仅控制屏幕显示；应用结构化日志不复制业务 payload。

**Hard invariant:** 执行输入、直接执行输出、AI 工具上下文、审批主体、交互历史和默认 Audit payload 不得被 `<redacted>` 等占位值静默改写。一个表面若允许读取数据，就返回原值；若具体 producer 的契约不允许某些 write-only 字段进入 Audit 或安全查询，就由该 producer 的显式 DTO/字段白名单省略字段；日志若不应保存业务内容，就不记录对应字段。视觉掩码只改变输入控件的显示方式，不改变值。

## Problem

当前分支为了阻止秘密进入 UI、历史、审批、日志和审计，引入了统一值脱敏，导致多个表面收到与实际执行主体不同的 `<redacted>` 副本：

1. AI ToolBlock 展示的参数、结果和错误不是当前工具实际使用或返回的数据。
2. 会话持久化与下一轮历史保存改写后的工具上下文，当前模型轮次与后续轮次语义不一致。
3. 审批 UI 展示改写后的 command/detail，并因是否发生脱敏改变 allow-once、allow-all 和编辑能力；审批者不能直接看到实际主体。
4. opsctl extension execution 的结果和错误被改写，破坏直接 CLI 输出语义。
5. Redis 桌面查询历史改写写命令的值，历史不再等于用户实际执行的命令。
6. AI Provider 同时向前端返回完整 `apiKey` 和 `maskedApiKey`；后者没有安全价值，秘密输入控件之间的显示/隐藏交互也不一致。
7. 应用结构化日志仍可能通过 command、cause、detail 或完整 error 正文复制业务 payload；用 `<redacted>` 改写后记录仍然扩大了日志职责。
8. 通用 Audit writer 对所有 tool 的 command/request/result/error/matched pattern 做启发式值替换，使审计行既不是原始内容，也不是工具拥有的明确安全 DTO；新增字段会依赖全局黑名单猜测。

## Actors and user stories

1. As an AI user, I want ToolBlock、错误和会话历史保留原始工具上下文，使实时显示、重载和下一轮模型回放语义一致。
2. As an approval reviewer, I want看到实际将执行的 command/detail，而不是后端改写的近似值。
3. As an opsctl or Redis user, I want直接执行结果和交互历史保持原样，支持诊断、复制、管道和脚本。
4. As a credential query caller, I want安全查询只返回其契约允许的元数据，秘密字段完全不存在，SSH 公钥等公开材料完整返回。
5. As a settings user, I want API key、password、token 和 passphrase 默认视觉隐藏，并可用眼睛按钮明确查看原值。
6. As an operator, I want应用日志保留操作和 correlation 元数据，但不复制 command、args、stdout/stderr、远端响应或其他业务 payload。
7. As an audit reviewer, I want默认工具的 command/request/result/error/pattern 保留 Audit 边界收到的原值，同时让 `put_asset` 等明确接收 write-only secret 的 producer 直接省略那些字段，而不是生成 `<redacted>`。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | AI ToolBlock 的 tool input、tool result、tool error、provider retry/error 面向前端原样传递。 | 展示面应与实际工具上下文一致。Rejected: 用 `<redacted>` 局部改写——造成显示值与执行值不一致。 |
| 2 | AI 会话 block 原样持久化和加载；下一用户轮次使用原始历史。 | 会话是用户明确保留的业务上下文，与原始用户消息采用同一数据边界。Rejected: 只让当前模型轮次见原值、后续轮次见安全副本——语义依赖轮次和重载时机。 |
| 3 | AI/opsctl 审批 UI 接收原始 command/detail，后端 pending state 和前端不再维护 redacted 状态或基于脱敏结果限制授权控件。 | 审批者必须看到实际主体；秘密应通过 credential ID、asset ID、stdin 或工具专用参数传递，而不是写进可持久化命令 pattern 后再猜测脱敏。 |
| 4 | opsctl `exec`、batch、extension execution 的 result/error 原样返回。 | 它们是用户明确请求的直接执行通道。Rejected: 内容改写——破坏 stdout/stderr 和错误诊断语义。 |
| 5 | Redis 桌面查询历史记录完整原始命令，不替换写入值。 | 这是当前进程内的直接交互历史，不是安全审计副本。 |
| 6 | `get_asset`、`list_credentials`、`get_credential` 等安全查询继续通过窄 DTO 省略 password、private key、passphrase、token、kubeconfig、Agent endpoint 等秘密字段；完整 SSH public key 可以返回。 | 查询契约明确可见字段比返回占位值更清晰。Rejected: `"password":"<redacted>"`——暗示字段存在且产生近似值困惑。 |
| 7 | AI Provider DTO 只保留完整 `apiKey`，删除 `maskedApiKey`；设置表单通过 password 控件默认隐藏，眼睛按钮切换显示原值。 | 当前后端已经把完整 key 发给前端，额外 masked 表示冗余。视觉控制不改变业务值。 |
| 8 | 统一秘密输入控件：API key、password、token、passphrase 等默认隐藏并提供 Eye/EyeOff；已有实现迁移到共享组件，缺少按钮的表单补齐。 | 相同交互应复用，不在各页面重复状态和按钮布局。协议驱动的 terminal echo 控件保留其协议语义。 |
| 9 | 应用结构化日志不记录业务 payload；保留 tool/operation、asset ID、provider/extension、attempt、duration、status、session/conversation ID 和稳定失败分类。 | 日志不需要 command、args、result 或远端错误正文。Rejected: 先脱敏再写日志——仍复制了不属于日志的数据，并依赖不可靠的内容识别。 |
| 10 | Audit 默认保存 writer 收到的 command/request/result/error/matched pattern 原值并保留现有截断；不解析、重编码或执行 canonical 值脱敏。只有明确拥有 write-only secret 契约的 producer 使用字段白名单 Audit DTO：AI/opsctl `put_asset`、desktop asset change 和 external edit。 | 审计不应保存被启发式改写的近似值。Rejected: 所有工具统一扫描敏感字段——任意文本无法可靠分类，且把每个工具的契约耦合到全局黑名单。 |

## Raw AI and approval flow

runner 发送 Wails 事件时直接序列化工具原始输入和输出，不调用 `auditredact.JSON`、`Result` 或 `Text`。工具失败、provider retry 和同步 chat error 返回原始错误正文；工具名称、call ID 和状态语义不变。

`conversation_entity.Message.SetBlocks`、加载和序列化不改写 tool input、tool content、error detail 或嵌套 child blocks。已经在旧版本中写成 `<redacted>` 的历史值无法恢复，本轮不猜测或逆向重建；新写入和仍为明文的旧行按数据库原值加载。

AI 与 opsctl 审批发送原始 ApprovalItem。pending state 只保存一份真实主体；删除安全副本、`containsRedaction`、`CanPersistGrant` 等由投影产生的门禁。allow-once、allow-all、remember 和 edited-items 继续遵守原有审批类型与策略校验，不因内容是否匹配敏感字段名而改变。

秘密传递应依赖既有安全接口：credential typed ref、asset ID、managed credential、stdin 或协议客户端参数。审批和 ToolBlock 不承担修正不安全命令设计的职责；若未来需要禁止 `--password value` 一类形式，应在对应命令解析/校验契约中明确拒绝，而不是更改显示值。

## Direct execution and history

opsctl extension execution 与普通 exec/batch 一致，将 extension executor 返回的 bytes 和 error 原样交给调用者。Audit 默认记录进入 writer 的原始 request/result/error；Audit 截断或特殊 producer request projection 不得回写直接执行结果。

Redis `CommandHistory` 保存 `formatCommandForHistory` 对原始 args 的正常 quoting 结果，不按命令类型替换 SET/HSET/MSET/list/set/zset/stream 等写入值。历史长度、筛选、顺序和错误语义不变。

## Safe query DTOs

安全查询不使用 canonical redactor。DTO 只声明允许返回的字段：

- 允许：ref、ID、type、name、description、username、fingerprint、key type/size、comment、availability、endpoint type、usage metadata、SSH public key、SSH Agent identity public metadata。
- 省略：password、private key、private-key passphrase、API/client secret、token、kubeconfig、Authorization/Cookie、SSH Agent endpoint/path/socket/pipe、签名和 challenge secret。

秘密字段即使有值也不能被序列化；不增加 `<redacted>` 占位字段。Public key 是公开身份材料，`get_credential` 的 SSH key detail 可完整返回。

## Shared visual secret input

新增或抽取共享视觉秘密输入组件，行为为：

- 持有原始 `value`，不生成 masked 副本；
- 默认渲染 `type="password"`；
- Eye 切换为 `type="text"`，EyeOff 切回；
- 支持现有 Input props、disabled、placeholder、validation、autocomplete 和右侧附加 action；
- 有可访问的显示/隐藏 label；
- 不自动解密、不记录、不复制值。

AI Provider 表单删除 `maskedApiKey`，以返回的原始 `apiKey` 初始化共享控件。获取模型列表和保存继续使用同一原始值。

将普通设置/资产/凭据/扩展表单中缺少显示按钮的 password、API key、token、passphrase 输入迁移到共享控件。`PasswordSourceField` 中“首次显示时按资产 ID 向后端解密”的领域行为保留，可复用共享控件的视觉部分；terminal challenge/echo 等协议驱动输入不强行改为普通表单状态。

## Application structured logging

本轮触及的 AI、permission、extension 和 opsctl 日志删除以下字段：

- command、detail、args JSON；
- tool result、stdout/stderr；
- provider/远端响应正文；
- 可能包装用户输入或远端输出的完整 error message。

保留：

- operation/tool name；
- asset ID/type；
- provider/extension name；
- attempt、delay、duration；
- success/status 和稳定 failure kind；
- session、conversation、confirm correlation ID。

面向用户的直接错误返回仍原样；“不写日志”不能反向改写 Wails、AI 或 opsctl 返回值。若当前错误体系没有稳定 code，本轮日志可只记录 `failed=true` 和所在操作，不得通过解析 `err.Error()` 生成分类。

## Audit raw-by-default and producer projections

`DefaultAuditWriter` 不再调用 canonical redactor：

- `Command` 保存调用方提供的 effective/canonical command；调用方未提供时仍使用既有 extractor，命令规范化语义不变；
- `Request` 保存 writer 收到的 JSON 字符串并沿用 4096 字节截断，不解析、不重编码；
- `Result` 保存 writer 收到的结果字符串并沿用 32768 字节截断；
- `Error` 保存原始错误正文；
- `MatchedPattern`、`grant_submit` pattern 和 `grant_discarded` command 原样保存；
- asset/source/session/conversation/decision/success/correlation 字段保持现状。

这不是取证级 byte-for-byte stdout：SSH Audit 仍使用既有 limited buffer，部分 command 仍是策略层 canonical form，stdout/stderr 仍按现有结构组合。当前截断和规范化明确保留，本轮只删除值脱敏与 JSON 重新编码。

默认规则适用于 exec、batch、cp、extension、grant、group CRUD、list/get/help、OSS presign 和 local tools；即使用户主动让这些工具的命令、输出或错误包含秘密，Audit 也不进行内容识别或局部改写。

只有具体 producer 明确拥有 write-only 字段时，Audit request 使用 producer-owned projection：

- AI `put_asset`：复用 `asset_put_svc.Prepared.SafeAuditArgs` / `SafeAuditArgsForResult` 和各 `assettype.AutomationContract.ApprovalFields`，省略 `password`、`private_key`、`passphrase`、`secret_access_key`、`kubeconfig`，保留类型允许的普通 config、资产身份和 typed authentication ref；prepare 失败也不得回退原始 config，至少提供不含 config 的顶层字段 projection；
- opsctl create asset：继续使用同一 `SafeAuditArgsForResult` producer projection；删除暗示通用脱敏的命名；
- desktop asset change：继续使用 `assetAuditView` 白名单；
- external edit：`OpenRequest`、`SaveResult`、`Session` 继续使用显式 producer DTO，保持独立且不变；自由 map metadata 改为真正的 fail-closed allowlist，仅允许 `auto`、`windowSaves`、`rebuild`、`resolution`、`status`、`remoteBytes`、`remoteSha256`、`bytes`、`documentKey`、`readOnly`、`reuse` 这 11 个键。允许键下只有标量 string/bool/number/nil 值按原样逐字保存；map/slice/array/struct/pointer 等复合值（无论 typed 还是 untyped）整体省略，不递归放行，防止 `bakeupPath`/password 藏在复合值里绕过字段白名单。未知字段以及 `bakeupPath`、local/workspace/editor path、local hash/sample 默认省略。既有 4096/8192/2048 截断保持。

通用 writer 不得按 tool name 分支，也不维护敏感字段注册表。AI `put_asset` 通过通用 audit-request override seam 把 producer projection 交给 middleware；没有 override 的工具自动使用原始 args。该 override 只影响 Audit，绝不成为执行、审批、ToolBlock 或会话输入。

完成迁移后删除 `internal/pkg/auditredact`、全部调用点、派生测试和误导性“安全副本/脱敏”注释；不得保留无调用的兼容 shim。已有数据库中历史 `<redacted>` 字面值不可恢复，不做迁移或猜测替换。Audit schema 和页面字段保持不变，页面直接显示数据库值。

## Compatibility and UI behavior

- 工具执行、审批结果、allow-once 和 allow-all 使用原始主体。
- AI ToolBlock、实时会话、重载会话和下一轮模型历史保持同一原始值。
- opsctl stdout/stderr 和 extension result/error 不增加 `--raw` 或其他模式开关。
- Redis 历史将重新显示完整写入值；旧内存历史不会跨进程迁移。
- 已经持久化为 `<redacted>` 的会话历史保持该字面值，因为原值不可恢复。
- 密码/秘密控件默认仍不可见，只有用户点击眼睛后在屏幕显示。
- AI Provider Wails DTO 移除 `maskedApiKey`，前端生成 bindings 随 Go DTO 更新。
- Audit schema 和页面结构不变；新写入的默认 tool payload 显示原值，特殊 producer request 中 write-only 字段完全不存在；旧行保持原值或既有 `<redacted>` 字面值。

## Out of scope

- Audit schema、历史行重写/清理、retention 和 UI 重构。
- 将 Audit 升级为取证级 byte-for-byte stdout/stderr；现有 canonical command、limited buffer、组合格式和截断保持。
- 新增 reveal/export API；本轮眼睛按钮只显示已经由当前表单持有的值，`PasswordSourceField` 已有的按需解密行为保持原契约。
- 自动改写用户命令为 `--password-stdin`，或为所有外部命令新增 stdin secret transport。
- 恢复已经被 `<redacted>` 覆盖的历史值。
- 改变数据库文件、会话导出和备份的访问控制。
- 对直接 opsctl/AI/Redis 内容进行内容审查或默认过滤。
- 调用 `.env` 中的真实 hosted AI 模型；除非用户另行明确授权外部请求。

## Testing decisions

| Seam | What it verifies |
|---|---|
| Runner StreamEvent | tool input/result/retry/error 与原始事件一致，执行链不受显示转换影响 |
| Conversation entity and replay | tool input/result/error/嵌套 child blocks 原样写入、加载并进入下一轮历史；字面 `<redacted>` 作为普通值保留 |
| AI/opsctl approval | Wails item、pending item 和实际执行主体一致；普通 allow-once/allow-all/edit 契约恢复 |
| opsctl extension | result bytes 和 error text 与 executor 返回一致 |
| Redis history | SET/HSET/MSET/list/set/zset/stream 等命令历史包含实际输入值和既有 quoting |
| Safe credential query | secret fields 不存在，public key 完整返回，Agent endpoint 值不存在 |
| AI Provider DTO/form | 仅有 `apiKey`，默认视觉隐藏，眼睛展示/隐藏原值，fetch/save 使用原值 |
| Shared secret input | value 不变、Eye/EyeOff、可访问性、disabled/placeholder/right action；迁移页面不丢失原行为 |
| Structured logs | 失败路径保留 correlation 元数据，但不存在 command/detail/result/cause/raw error payload 字段 |
| Default Audit raw payload | command/request/result/error/matched pattern 与 writer 输入一致并只受既有截断；JSON formatting 和字面 `<redacted>` 不被改写 |
| `put_asset` Audit projection | AI 成功/失败和 opsctl create 的 request 均保留普通 config/identity/ref，但五类 write-only 字段完全不存在；实际执行仍收到原值 |
| Desktop/external-edit Audit projection | desktop 白名单保持；external-edit 显式 `OpenRequest`/`SaveResult`/`Session` DTO 保持独立且不变；map fail-closed allowlist 只输出批准字段，允许键下仅标量 string/bool/number/nil 值逐字保留，map/slice/array/struct/pointer 复合值（typed 或 untyped）整体省略；未知字段/`bakeupPath`/本地环境字段不存在，截断不变 |
| Legacy cleanup | repo 中无 `auditredact` 调用、package、`RedactedValue` Audit 断言或误导性兼容逻辑 |

验证使用合成值和隔离数据目录。运行时检查实时 ToolBlock、审批、会话重载、下一轮模型请求、opsctl extension、Redis 历史和 AI Provider 眼睛控件均展示预期原值；同时检查应用日志没有复制这些合成 payload。Audit runtime 应证明普通 exec/extension result 和 error 原样落库，AI/opsctl `put_asset` 普通 config 可见而五类 write-only 字段不存在，external-edit projection 只含批准字段、未知字段与本地 `bakeupPath` 不存在；允许键下仅标量 string/bool/number/nil 值逐字保留，map/slice/array/struct/pointer 复合值（typed 或 untyped）整体省略，且不生成 `<redacted>`；显式 `OpenRequest`/`SaveResult`/`Session` DTO 独立于 map allowlist 保持原有投影。

## Relevant links

- [原资产凭据自动化规格](2026-08-13-asset-credential-automation.md)
- [Verification workflow](../VERIFICATION.md)
- [Development logging rules](../DEVELOP.md#logging-for-key-flows)
