# AI 工具与审批秘密显示统一脱敏

> Status: Approved
> Owner: OpsKat maintainers
> Last updated: 2026-08-13

**Objective:** 在不影响当前工具执行的前提下，为 AI 工具事件、AI/opsctl 审批、会话历史、错误、日志和审计建立一个后端统一的安全投影边界，使任何展示、持久化或运维观察面都不能收到工具参数或结果中的凭据材料。

**Hard invariant:** 密码、私钥、passphrase、kubeconfig、Secret Access Key、API key/token、Authorization 凭据、SSH Agent endpoint、签名、挑战及挑战答案不得出现在 AI 工具参数/结果卡片、错误块、审批详情、会话持久化、后续轮次历史回放、日志、审计明文、命令结果或安全视图中。原始材料只可在当前工具执行以及完成当前模型轮次所必需的内存上下文中短暂存在。

## Problem

1. **AI 工具参数在实时 ToolBlock 中显示明文。** 运行时验证在 `1afd3ba54ca2c726f5dfca09b1a7252cdb50b0ea` 上通过真实 AI runner + scripted model 调用 `put_asset(config.password=...)`，`#ai-tool-block code` 直接显示了合成密码；证据位于 `e2e/scratch/2026-08-13-asset-credential-automation/ai-secret-visible.png`。生产链路为 cago `EventPreToolUse` → `internal/ai/runner/stream_event.go` → Wails `tool_input` → `frontend/src/stores/aiStore.ts` → `ToolBlock.tsx`。
2. **工具结果、工具错误和 provider 错误没有同一安全边界。** `tool_result`、retry cause 与 terminal error 可直接进入 ToolBlock、ErrorBlock、日志和前端状态；会话持久化仅脱敏顶层 `ToolInput`，不覆盖工具结果、错误详情或嵌套 agent block，因此工具意外返回 PEM、凭据 URL、Authorization 或嵌套秘密时可能成为新的显示及持久化侧信道。
3. **实时会话与重载会话的安全行为不一致。** 当前实时 `tool_input` 保持原文，而 `conversation_entity.Message.SetBlocks` 在落库时才脱敏，所以首次显示泄漏、重载后却显示 `<redacted>`；前端又从这些 block 重建下一轮模型历史，导致秘密保留策略依赖会话是否重载。
4. **AI 与 opsctl 部分审批把原始 command/detail 直接发送给前端。** 资产自动化创建已提供安全摘要，但通用 command、local write/edit、批处理及部分 opsctl 审批仍按原文渲染；若命令、文件内容或 diff 包含凭据，审批 UI 会成为明文出口。
5. **日志和审计仍有未纳入 canonical redactor 的列。** provider retry cause 可原样写日志；零条 grant pattern 的 warning 记录原始 command；审计 `matched_pattern` 未脱敏。`internal/pkg/auditredact` 也尚未把通用 signature/challenge/Agent endpoint 字段全部识别为敏感字段。

## Actors and user stories

1. As an OpsKat AI user, I want tool cards and errors to preserve useful structure while hiding credential material, so that I can inspect automation without exposing secrets on screen or in conversation history.
2. As an approval reviewer, I want to see the safe operation shape but never secret values, so that approving an operation does not require displaying or persisting the credential itself.
3. As an operator investigating logs and audit rows, I want every AI/opsctl secret-bearing field to use one canonical redaction policy, so that a newly added tool cannot silently create another plaintext sink.
4. As an AI runtime, I need the original tool input/result only while executing and completing the current model turn, so that redacting UI/history copies does not break the requested operation.

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 原始执行数据与安全展示数据在 Go 后端边界分离：cago 当前轮次保留原始值，所有 Wails/UI、审批、持久化、日志和审计接收安全投影。 | 后端是所有消费者之前的共同边界。Rejected: 仅在 ToolBlock 前端遮挡——不能保护 Wails payload、DB、日志、审计、审批或模型历史，且会复制敏感字段规则。 |
| 2 | 复用并扩展 `internal/pkg/auditredact` 作为唯一 canonical redactor，区分结构化 JSON 与不透明文本；非法 JSON fail-closed。 | 已有实现覆盖递归 JSON/数组、常见凭据键、PEM、URL userinfo、Bearer 和 presigned 参数。Rejected: 每个工具注册自己的敏感字段——扩展工具、错误和任意结果会继续漏网。 |
| 3 | 工具参数、结果与错误的安全投影在流式事件发往 Wails 前完成；前端只负责展示，不判断什么是秘密。 | 这样实时会话与后续落库收到同一份安全值。Rejected: 等到会话持久化才脱敏——已经发生本次实测的首次渲染泄漏。 |
| 4 | 当前工具执行和当前模型轮次继续使用原始参数/结果；完成该轮后，持久化和下一用户轮次回放只使用安全历史。 | 工具必须获得真实凭据才能执行，当前模型也可能需要真实工具结果完成用户请求；长期保存或再次发送这些值不再必要。Rejected: 在 dispatch 前改写原始参数——会破坏资产创建和其他依赖秘密的工具。 |
| 5 | 审批后端保留原始待审批主体用于执行与响应校验，前端仅接收脱敏副本。若 command/detail 的安全投影与原文不同，该请求只允许拒绝或仅本次允许，不提供“记住/始终允许”及可编辑 grant pattern。 | `<redacted>` 不能成为授权 pattern，原始秘密也不能经编辑响应回传或持久化。Rejected: 继续提供 remember——可能保存无效的脱敏串或秘密原文。 |
| 6 | 工具卡片继续显示参数/结果结构和非敏感值，敏感叶子替换为 `<redacted>`；整个 PEM 或无法安全解析的秘密文本可以整体替换。 | 用户仍需诊断工具调用形状。Rejected: 隐藏全部工具参数/结果——安全但不必要地损失可观察性。 |
| 7 | 会话持久化对 tool input、tool result、ErrorDetail 及嵌套 agent child blocks 递归执行安全投影，作为流式边界之外的纵深防御。 | 防止其他生产者或旧事件绕过实时边界。Rejected: 只依赖 StreamEvent——未来非流式或手工构造 block 可能再次产生明文。 |
| 8 | 审计所有文本列（包括 `matched_pattern`）和相关安全日志统一调用 canonical redactor。 | 审计 UI 会直接展示 matched pattern，日志是长期运维表面。Rejected: 假设 policy pattern 不含秘密——用户编辑及通用命令可以包含任意文本。 |

## Safe projection boundary

模型或 provider 生成工具调用时，原始参数进入当前 runner 的执行上下文。工具 handler、credential materialization、连接客户端及当前模型轮次可按现有语义使用这些原始值；该执行内存不是新的 reveal API，也不得写入日志。

在 `tool_start` 事件离开 runner 前，参数按 JSON 递归脱敏。合法 JSON 保留对象、数组、键名及非敏感值；敏感键的值替换为 `<redacted>`，字符串中的 PEM、Authorization、凭据 URL、签名参数等按文本规则替换。无法解析为合法 JSON 的工具参数不得原样发送，返回 fail-closed 的 `<redacted>` 安全输入摘要。

在 `tool_result` 离开 runner 前，JSON 对象/数组递归脱敏；普通文本执行 canonical text redaction。工具错误、provider retry cause、terminal error 及面向前端的错误详情执行同一文本脱敏。安全投影不得改变 `tool_name`、`tool_call_id`、成功/失败状态或普通非敏感输出。

ToolBlock、AgentBlock 中的 child ToolBlock 和 ErrorBlock 只消费安全事件，不再实现敏感字段列表。折叠摘要与展开内容必须使用同一安全字符串，避免折叠行安全而展开区泄漏。

## Conversation persistence and replay

会话消息只持久化安全显示副本。持久化边界递归处理：

- tool block 的 `toolInput`；
- tool block 的 `content`，无论是 JSON 还是普通文本；
- error block 的 `content` 和 `errorDetail`；
- agent block 的嵌套 child blocks；
- 未来未知 block 中由既有安全 DTO 明确标记为展示文本的字段。

实时流式显示、关闭 tab 时的快照、自然终态落库和重载后的显示必须一致。数据库 `conversation_messages.blocks` 不得包含合成秘密标记。

当前模型轮次由 runner 内部 conversation 继续接收原始工具结果，以便完成正在进行的请求。该轮完成后，前端重建下一轮 API 历史时使用持久化语义相同的安全 tool input/result；因此下一轮模型不得再次收到上一轮的密码、私钥、passphrase、kubeconfig、Agent endpoint、签名或 challenge material。

## Approval safety

AI single/batch/grant/local-tool 和 opsctl single/batch/grant 审批在发往 Wails 前创建安全 ApprovalItem 副本。资产名、类型、安全 endpoint 摘要及不敏感命令结构保持可见；command/detail 中的凭据材料替换为 `<redacted>`。

后端 pending approval 保存原始 items，并继续使用原始 items 校验响应及执行。安全 items 只用于显示。普通未发生脱敏的审批保持现有 remember/edit 行为。

只要任一可编辑 command 或 detail 的安全投影不同于原文：

- UI 不显示“记住”“始终允许”或 pattern 编辑器；
- 响应只能是 deny 或 allow-once；
- 后端拒绝伪造的 allow-all/edited-items 响应，而不是信任前端隐藏按钮；
- 不保存原始或 `<redacted>` grant pattern。

本地写入/编辑审批保留非敏感上下文和 diff 结构；秘密片段被替换。若内容整体是 PEM 等秘密，预览可只显示 `<redacted>`，但仍允许用户拒绝或仅本次批准。

## Canonical sensitive material

Canonical redactor 在已有规则之上必须覆盖：

- password、passphrase、token、API/client/private key、Secret Access Key、kubeconfig；
- Authorization / Proxy-Authorization、cookie 与 URL userinfo；
- PEM private key；
- signature、signed value、challenge、challenge response/answer；
- SSH Agent endpoint 值及 endpoint/path/socket/pipe/named-pipe 等来源字段；
- presigned URL credential/signature/security-token 参数；
- 上述字段的大小写、snake_case、kebab-case、camelCase 变体及嵌套数组/对象。

普通安全字段如 `endpoint_type`、credential typed ref、source ID、fingerprint、key type/size、public SSH key detail仍按既有安全查询契约显示。不能因为字段名包含宽泛的 `key` 或 `endpoint` 就遮掉所有公有标识；规则应针对秘密值语义并以测试固定边界。

## Logs and audit

AI provider retry、工具失败、审批/grant 退化及其他本轮触及的日志不得把原始 error/command/detail 写入字段。保留错误分类、attempt、delay、asset/tool correlation ID 等安全信息；需要错误正文时写 canonical redacted text。

AI/opsctl audit 的 request、result、error、command 和 `matched_pattern` 均使用 canonical redactor。审计 UI 不承担第二次脱敏责任，但测试应证明读取现有 audit DTO 时看不到秘密。

发生序列化或脱敏失败时 fail closed：展示/持久化 `<redacted>` 或安全错误摘要，不能回退原文。脱敏失败不得阻止原始工具执行上下文返回自己的业务错误，但外发错误仍必须安全。

## Compatibility and UI behavior

工具卡片、审批卡片、错误块的布局、折叠、状态图标、tool call 配对及成功/失败语义保持不变。唯一可见变化是秘密值显示为 `<redacted>`，以及包含被脱敏审批主体时不再提供持久授权控件。

现有不含秘密的命令、SQL、Redis/Kafka/K8s 操作、文件路径和普通工具结果继续完整显示。现有对话中已经安全落库的 `<redacted>` 值保持原样，不进行历史数据解密或恢复。若历史数据库中已有明文 block，加载到前端前也必须经过安全投影；本轮不原地批量重写旧行。

## Out of scope

- 密码、私钥、Agent endpoint 或其他秘密的 reveal/export 能力。
- 对 provider 或 cago 内部当前轮次内存进行加密；其持有原始值是执行请求所必需。
- 自动扫描或清理用户现有数据库、系统日志、截图和备份中的历史明文；加载旧会话时必须安全，但持久数据修复另行处理。
- 改变工具业务权限、命令策略或资产凭据 materialization 语义。
- 在前端维护第二套敏感字段注册表。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `auditredact` canonical redactor | 递归对象/数组、malformed JSON fail-closed、PEM、Authorization、URL、signature/challenge/Agent endpoint，以及安全 public metadata 不被误遮 | `internal/pkg/auditredact` 现有单元测试与 AI audit redaction 测试 |
| Runner StreamEvent translation | `tool_start`、tool result、retry/error 在 Wails 前已安全，同时原始 cago tool execution 不受影响 | `internal/ai/runner/stream_event_test.go`、runner provider tests |
| Conversation block persistence/load/replay | tool input/result/error/嵌套 child blocks 均脱敏；实时与重载一致；下一轮模型只见安全历史 | `conversation_entity` tests、`frontend/src/__tests__/aiStore.test.ts` |
| AI/opsctl approval projection | UI 只收到安全 command/detail；原始 pending item 仍用于一次性执行；发生脱敏时 allow-all/edit 被前后端共同禁止 | AI approval tests、opsctl approval handler/component tests |
| Audit/log sinks | matched pattern、retry/error、零 pattern warning 不包含合成标记 | AI audit integration tests、permission logger capture tests |
| ToolBlock/ErrorBlock UI | 折叠摘要、展开参数/结果、agent child block 与错误详情都只渲染安全字符串，普通非敏感内容仍完整 | `ToolBlock.test.tsx`、AI store/component tests |
| Real runtime | scripted model 分别发送嵌套 password、PEM/passphrase、kubeconfig、Authorization/URL、signature/challenge 和 secret-bearing result/error；检查实时 UI、审批、DB history、下一轮 model requests、audit、logs 全部无标记 | `make dev-sandbox ARGS=--mocks` + `drive.mjs` / `oracle.mjs` |

运行时使用合成秘密和隔离数据目录，不接触真实凭据或 `.env` 目标。自动化无法证明操作系统截图/录屏等外部捕获已经被清理；验证只证明本分支不再把合成秘密发送到其拥有的 UI、Wails、DB、日志、审计和后续模型历史表面。

## Relevant links

- [原资产凭据自动化规格](2026-08-13-asset-credential-automation.md)
- [运行时验证报告](../../e2e/scratch/2026-08-13-asset-credential-automation/report.md)（本地 gitignored 证据，不随 Git 交付）
- [Verification workflow](../VERIFICATION.md)
- [Development logging rules](../DEVELOP.md#logging-for-key-flows)
