# 扩展平台能力：连接、凭据注入、参数级策略与页面调用闸门

> Status: Approved
> Owner: OpsKat 维护者
> Last updated: 2026-09-24

**Objective:** 让一个 WASM 扩展能以与内置资产类型同等的安全性与可用性接入"用户自配地址的网络服务"（首个客户：Elasticsearch），而宿主仍是网络、凭据、策略、审计的唯一裁决者。

**Hard invariant:** PR #306 已建立的"扩展走宿主同一套注册表 / exec / 策略 / 审计"不回退；guest 不因本变更获得任何超出 manifest 声明的能力；未声明新能力的已有扩展（notebook）行为不变。

## Problem

标注：**[已核实]** = 对照 PR #306 分支源码确认；**[用户决定]** = 本轮对话中用户选定。

1. **扩展连不上内网服务。** [已核实] HTTP 放行只按 manifest 里的静态前缀匹配（`pkg/extension/manifest.go` `CheckHTTPURL`），用户自配的地址只能声明成 `https://` 全放；内网 IP 直连被拒，放行私网只能靠语义不符的 `capabilities.tunnel`（`host_capability.go`）。
2. **隧道 / 代理 / TLS 对扩展不可用。** [已核实] 宿主有 `TunnelDialer` 但 provider 按扩展只建一个、从不设置资产隧道；扩展资产表单把 `sshTunnelId` 写死为 0（`ExtensionConfigSection.tsx`）；HTTP handle 直接 clone 默认 Transport，无法配 CA / 客户端证书 / 跳过校验（`io_http.go`）。内置类型的 `connpool` 隧道 / 代理链 / `BuildTLSConfig` 均无法复用。
3. **凭据只能以明文进入 WASM。** [已核实] 不声明 `credentials:read` 时 guest 拿到的不透明句柄没有任何宿主路径能兑换成真实凭据；于是 HTTP 认证只能声明 `read` 取明文。
4. **策略只能按工具区分。** [已核实] 工具的策略动作是注册时固定的（`pkg/extsdk/registry.go` `.Policy(action)`），guest 返回的 resource 被宿主丢弃（`internal/extreg/extreg.go` `action, _, err`）；一个通用 request 工具无法区分只读查询与删除索引。
5. **扩展页面发起的调用绕过策略与审计。** [已核实] `CallExtensionTool` 直接调用 `Plugin.CallTool`（`internal/app/extension/extension_ops.go`），不走策略、不审批、不落审计、不可取消。
6. **"测试连接"按钮无条件出现且没有契约。** [已核实] 表单对所有扩展类型显示该按钮并调用约定俗成的 `test_connection` action（`ExtensionConfigForm.tsx`）。
7. **长请求与大响应不可控。** [已核实] 工具调用硬上限 30s（`runtime.go`）；阻塞在宿主 IO 上的调用不响应超时 / 取消，可占满 4 个实例槽；每次请求新建 Transport，无连接复用。
8. **扩展页面无法复用宿主 UI。** [已核实] 注入给扩展的只有 React / ReactDOM / i18n / `@opskat/ui` 基础组件（`frontend/src/extension/inject.ts`），代码编辑器、JSON 视图、结果表格需扩展自带一份，违反 Reuse first。

## Actors and user stories

1. 作为**运维用户**，我想像配置内置数据库一样为扩展资产填地址、选 SSH 隧道 / 代理、配 TLS 证书，并点"测试连接"确认可用。
2. 作为**运维用户**，我想给扩展资产写"放行只读、拒绝删除 prod-* 索引"这类规则，并且无论操作来自 AI、opsctl 还是扩展页面，都受同一套规则、审批和审计约束。
3. 作为**扩展作者**，我想声明认证方式而无需接触明文密码，声明连接能力而无需实现隧道 / TLS，按参数给出策略分类，并在页面里直接用宿主的编辑器和结果表格。
4. 作为**安全审阅者**，我想在安装前从 manifest 看出扩展能连哪里、能否读明文凭据。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **[用户决定]** 策略采用"guest 分类 + 资源 glob"：guest 按参数返回 action 与 resource，宿主按 `<action>[:<resource-glob>]` 匹配 | 协议语义只有扩展懂，匹配 / 审批 / 审计由宿主统一。Rejected: 宿主对规范化命令串做通配——用户需懂 flag 串格式，参数顺序 / 写法变体可绕过；仅按工具拆分——无法做通用 request 工具，无法按索引名限制 |
| 2 | **[用户决定]** 凭据走通用模板注入，`credentials:read` 保留给非 HTTP 协议 | 模板（header / query / basic）覆盖绝大多数 REST 服务且明文不入 WASM；裸 TCP 协议宿主无法代做握手。Rejected: 只保留明文读取——凭据安全完全依赖安装时的信任；只支持注入——堵死 TCP 协议扩展 |
| 3 | 网络目标来自"当前调用资产配置中标记为 endpoint 的字段"，由新能力 `network.assetEndpoint` 授权；该目标自动允许私网地址 | 地址是用户亲手配置的，放行它不扩大信任面；静态 allowlist 保留给固定公网 API。Rejected: 让扩展声明 `https://` 全放——安全契约失效；继续借用 `tunnel:true` 放行私网——语义错位 |
| 4 | 隧道 / 代理链 / TLS 是宿主拥有的标准"连接"配置区，资产类型在 `describe()` 中声明支持哪些；宿主在拨号时应用 | 与内置类型同一套字段与 `connpool` 实现，证书文件不进 WASM。Rejected: 由扩展在 configSchema 里自定义这些字段并自行处理——重复实现且 guest 需读本地文件 |
| 5 | 扩展页面的工具调用与 `exec` 走同一闸门（策略、应用内审批、grant、审计），并可取消 | 同一动作不因入口不同而绕过规则。Rejected: 页面调用单独弹二次确认——两套规则会漂移 |
| 6 | 宿主 UI 以带版本的注入模块 `@opskat/host-ui` 暴露代码编辑器、JSON 视图、结果表格，版本随 hostABI | Reuse first；控制暴露面。Rejected: 扩展自带打包——体积大、主题不一致 |

## 网络与连接

- **能力声明。** manifest `capabilities` 新增 `network.assetEndpoint`（布尔）。未声明时，行为与现状一致（只受静态 `http` allowlist 约束）。
- **Endpoint 字段。** 扩展在资产类型的 configSchema 中把字段标记为 `format:"endpoint"`（URL 或 host:port）。前置：扩展声明了 `network.assetEndpoint` 且调用带有资产。动作：guest 发起 HTTP 请求或 TCP 连接。结果：目标的 scheme+host+port 与该资产任一 endpoint 字段一致时放行，允许私网地址；否则以明确的"目标不在资产 endpoint 内"错误拒绝。重定向到非 endpoint 目标同样被拒。
- **连接配置区。** 资产类型在 `describe()` 中声明 `connection: {sshTunnel, proxyChain, tls}` 的子集。声明了的项，资产表单显示与内置类型一致的连接配置区（隧道资产选择、代理链、TLS：启用 / 跳过校验 / ServerName / CA / 客户端证书与私钥）；详情卡同样展示。宿主向 endpoint 拨号时按该资产配置经隧道 / 代理链并应用 TLS。未声明的项不显示、不生效。
- **失败。** 隧道资产不可达、证书文件读取失败、TLS 握手失败等，以宿主错误原样返回给 guest 与调用方（AI / opsctl / 页面），并记日志；不静默回落到直连或不校验。
- **连接复用。** 宿主按"扩展 + 资产 + 连接配置指纹"缓存 HTTP 客户端，跨调用复用 keep-alive；资产被修改或删除、扩展被禁用 / 卸载 / 重装时丢弃对应缓存。

## 凭据注入

- **声明。** 资产类型在 `describe()` 中声明 `auth` 绑定列表，每项为：位置（`header` / `query` / `basic`）、名称（header 名或 query 参数名；basic 无）、值模板。模板只能引用该资产 configSchema 中的字段，支持 `{{field}}` 与 `{{base64(<parts>)}}` 拼接（parts 为字段或字面量）。可声明多组并由某个配置字段（如 `authType`）选择生效的一组。
- **生效。** 前置：请求目标是当前资产的 endpoint。动作：guest 发起 HTTP 请求。结果：宿主渲染模板、解密引用到的密码字段并注入；guest 无法读取注入后的请求头。目标不是 endpoint 时不注入。
- **明文读取。** 未声明 `credentials:read` 的扩展，`ctx.AssetConfig()` 中密码字段仍是不透明句柄；声明了的照旧拿明文。安装确认与扩展详情对 `credentials:read` 显示醒目提示。
- **失败。** 模板引用不存在的字段 → `describe()` 校验失败，扩展加载被拒；运行期解密失败 → 请求失败并报错，不发送未认证请求。

## 参数级策略

- **分类。** SDK 为工具提供 `PolicyFunc(args) → (action, resource)`，替代或补充固定 `.Policy(action)`。action 必须属于该类型在 `describe()` 中声明的动作集合；resource 为任意字符串（可空）。返回未声明的 action → 视为 NeedConfirm 并记录错误。
- **规则。** 规则形如 `<action>` 或 `<action>:<resource-glob>`，存于资产的策略列、资产组按策略面分开的 ext_policy 列与权限组，`opsctl policy allow/deny` 与资产详情策略卡均可编辑；glob 语义与现有命令规则一致。判定顺序：deny → allow → grant → confirm。无 resource 的规则匹配该 action 的任意 resource。
- **审批展示。** 审批弹窗显示 action、resource 与格式化后的请求（工具名 + 参数，长 JSON 可折叠）。"始终允许"落库的 grant 为 `ext:<type>:<action>:<resource>`。
- **授权请求。** 对扩展资产的 grant 请求（AI 的 `request_permission`，以及经 opsctl 审批通道送达的 grant 请求；opsctl 已无面向用户的 grant 子命令）以 `<action>[:<resource-glob>]` 表达，落库为 `ext:<type>:<action>[:<resource-glob>]`，与规则走同一套校验（action 须属于声明集合）；不合法的请求明确拒绝，不得在 grant 永不命中时告诉调用方"已批准"。扩展的 AI 技能文案按此格式引导。
- **兼容。** 只用固定 `.Policy(action)` 的已有工具行为不变（resource 为空）。

## 扩展页面调用闸门

- 前置：扩展页面对其所属资产调用工具。动作：调用发出。结果：与 AI 的 `exec` 相同地经过策略判定；NeedConfirm 时在应用内弹审批；Deny 时页面收到拒绝错误；执行结果与决策落审计（来源标记为"扩展页面"）。
- 页面可取消进行中的调用；取消与超时会中断阻塞中的宿主 IO，释放实例槽。
- 工具可在 `describe()` 中声明超时（默认 30s，上限 10 分钟）；超出上限的声明在加载时被拒。响应超过宿主上限（默认 16MB）时调用失败并给出明确错误，而非截断返回。

## 测试连接

- 资产类型在 `describe()` 中声明测试连接处理器时，表单才显示"测试连接"按钮；未声明则不显示。
- 新建资产：用表单当前值（含连接配置区）调用。编辑已保存资产：未改动的密码字段由宿主用已存值补齐后调用。结果以成功 / 失败信息显示在表单内。测试连接同样受 endpoint 放行约束，但不经过策略（它不是对资产的操作）。

## 宿主 UI 模块

- 扩展页面可 `import` `@opskat/host-ui`，获得：代码编辑器（语言可选，含 JSON）、JSON 树视图、结果表格（列 / 行数据、排序、复制）。组件跟随宿主主题与语言。
- 模块版本与 hostABI 绑定；扩展要求的 hostABI 高于宿主时按现有规则拒绝加载。

## Out of scope

- opsctl 在桌面端未运行时独立执行扩展资产（仍需桌面端）。
- ES 专属便利：请求体从文件 / stdin 读取、DSL 补全等 → ES 扩展 spec。
- 扩展商店、在线下载与签名 → 商店 spec。
- 非 HTTP 协议的宿主侧认证代做。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `pkg/extension` 宿主 IO（fixture-ext WASM） | endpoint 放行 / 拒绝 / 重定向拒绝；私网放行；凭据注入只发往 endpoint 且 guest 读不到；连接复用与失效；取消中断阻塞 IO | `runtime_test.go`、`io_http_test.go`、`fixture_test.go` |
| `internal/extreg` + `permission` | `PolicyFunc` 分类；`<action>[:<glob>]` 的 deny→allow→grant→confirm；未声明 action → confirm；grant 落库格式 | `extreg_rules_test.go`、`extreg_test.go` |
| `internal/app/extension` | 页面调用经策略 / 审批 / 审计；测试连接补齐已存密码 | `host_test.go` |
| `describe()` 校验 | 非法 auth 模板、未知 connection 项、超时超上限被拒 | `descriptor_test.go` |
| 前端 vitest | 连接配置区按声明显示；测试连接按钮按声明显示；`@opskat/host-ui` 注入 | `ExtensionConfigForm.test.tsx`、`extensionInit.test.ts` |
| e2e（沙箱 + notebook 扩展扩写或 fixture） | 表单连接区与测试连接的真实渲染；页面调用触发审批弹窗 | `e2e/tests/extension-asset-type.spec.ts` |

无法自动化：真实 SSH 隧道 + 自签证书 ES 的端到端连通，由收尾阶段在验证机（docker.local）起一个带 TLS 的 ES 实例人工驱动沙箱验证。

## Open questions
