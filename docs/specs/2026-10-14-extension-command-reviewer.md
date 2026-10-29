# 扩展能力：命令审核器扩展点（第 1 步）

> Status: Implemented（第 1 步）
> Owner: OpsKat 维护者
> Last updated: 2026-10-14

**Objective:** 让"命令模型审核"可以由扩展提供、按需安装。本文是第 1 步：补齐宿主与扩展 SDK 的能力（审核器扩展点、opsctl 内运行审核器、扩展级设置与密钥、按设置放行网络、安装来源记录），并把 [辅助审批与 Autopilot](./2026-09-29-assisted-approval-autopilot.md) 里与具体审核服务无关的部分留在宿主。第 2 步再把现有的 TypeSafe/Jev 审核迁成官方扩展 `extensions/jev-command-review`，另立文档。

**Hard invariant:** 审核器只能把规则判为"需要确认"的命令改为放行，永远碰不到规则拒绝和"只能由人判断"（`Unreviewable`）的命令；审核器缺失、出错、超时、返回格式不对，一律按"审核失败"处理——辅助审批问人，Autopilot 拒绝，绝不因此放行。

## Problem

标注：**[已核实]** = 对照本分支（合并 main `3c1d05f3` 后的 `94fccc3c`）源码确认；**[用户决定]** = 本轮对话中用户选定。

1. **审核写死在宿主。** [已核实] `permission.applyReviews`（`internal/ai/permission/review.go:26`，由 `permission.go:57` 调用）直接取 `command_review_svc.Default()`；审核服务由 `internal/bootstrap/command_review.go` 用 TypeSafe 客户端注册，设置存在 `config.json` 的 `CommandReview*` 字段（`internal/bootstrap/config.go:52-56`）。不装也得带着，换审核服务只能改宿主。
2. **扩展没有审核入口。** [已核实] 宿主调扩展的入口只有 `describe` / `execute_tool` / `execute_action` / `check_policy` / `validate_config` / `test_connection`（`pkg/extsdk/handler.go:11-21`）；SDK 能注册的只有 Tool、AssetType、Action、PolicyGroup、ConfigValidator（`pkg/extsdk/registry.go`）。
3. **opsctl 不运行扩展代码。** [已核实] opsctl 只按缓存的 `describe()` 注册扩展的非执行面，"全程不编译也不实例化 WASM"（`internal/extreg/describe_only.go:102`）；扩展工具的执行经 `approval.sock` 交给桌面端（`cmd/opsctl/command/ext.go:220` `delegateExtExec`）。而现在的审核在 opsctl 进程内完成，桌面端没开也能审核（`docs/ARCHITECTURE.md` opsctl 一节）。
4. **扩展没有自己的设置与密钥。** [已核实] manifest 只有每个资产类型的 `configSchema`（`pkg/extension/manifest.go:206`）；扩展可存数据的只有按扩展隔开的 KV（`internal/service/extension_svc/host.go:32-49`），明文存库，不经 `credential_svc` 加密。
5. **网络只认写死的白名单或资产地址。** [已核实] HTTP 只放行 manifest 的静态 allowlist，或声明了 `network.assetEndpoint` 时该调用所属资产的地址（`pkg/extension/host_capability.go` `gateHTTP`）。审核服务地址是用户可配置的，两者都覆盖不了。
6. **装好的扩展不记得自己从哪来。** [已核实] 商店安装时知道来源是 `InstallSourceStore`（`internal/service/extstore_svc/install.go:150`），但只用于安装确认；`extension_state` 表只有 `name` / `enabled`（`internal/model/entity/extension_state_entity/extension_state.go`）。加载时无法区分"商店装的官方包"和"本地 ZIP 装的同名包"。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **[用户决定]** 分两步：先补扩展能力并保留宿主侧审核流程，再把 TypeSafe 审核迁成官方扩展 | 能力与迁移各自可评审、可回滚。Rejected: 一次做完——改动跨 SDK、宿主、opsctl、前端，难以评审 |
| 2 | **[用户决定]** opsctl 在自己进程内加载并运行审核器扩展，不经桌面端 | 保持"桌面端没开也能审核"。Rejected: 委托桌面端——桌面端一关，辅助审批退回问人、Autopilot 全部拒绝 |
| 3 | **[用户决定]** 只有经官方商店签名校验安装的扩展能注册审核器；非正式版应用（nightly / 开发版）上，本地安装的扩展也可以，并在设置里标"未经签名" | 审核器"通过"即可让 Autopilot 执行命令，信任根只能是官方签名。开发版例外是为了能开发审核扩展。Rejected: 第三方 + 强提示——一次误点就把自动放行权交给未审查的代码 |
| 4 | 审核器是新的 guest 入口 `review_commands`，一次接收一批命令；host ABI 升到 2.3 | 宿主已经按批调用（`ReviewBatch`），一次调用省掉实例往返。旧宿主读不懂审核器声明，所以要升 ABI 让旧应用拒绝加载。Rejected: 用 Tool 冒充审核器——会进 AI 工具与策略链路，语义错 |
| 5 | 同一时间只允许一个生效的审核器；装了多个时，用户在设置里选一个，没选就是"无" | 多个审核器的结果如何合并没有好答案。Rejected: 全部通过才放行——多一个审核器就多一份延迟与失败面 |
| 6 | 敏感信息替换、缓存、重试、超时、按权限模式落结果都留在宿主；扩展只回答"这批命令每条是否危险" | 替换依赖宿主的命令写法注册表（`permission/type_registry.go`），缓存要被桌面端与 opsctl 共用；放在宿主才能保证 Hard invariant。Rejected: 交给扩展——扩展漏替换就泄露密码 |
| 7 | 扩展级设置由扩展在 `describe()` 里声明 JSON Schema，宿主渲染表单、保存、校验；`format: "password"` 的字段由 `credential_svc` 加密存储 | 复用资产 `configSchema` 那套表单与校验。Rejected: 扩展自己做设置页、存 KV——密钥明文落库 |
| 8 | 密钥字段不交给扩展原文：扩展发 HTTP 时在请求头里写占位 `{{setting.<field>}}`，宿主在发出前替换；只替换发往"设置里声明为该密钥服务地址"的请求 | 与资产类型的 `Auth` 注入同一思路，扩展拿不到明文也就泄露不了。Rejected: 读明文的 host 函数——任何能力都可能把它发到别处 |
| 9 | 设置里 `format: "endpoint"` 的字段，其值视为该扩展可访问的地址（与静态 allowlist 并列）；默认不放行私网地址，除非 manifest 声明 `tunnel` | 用户配置的地址由用户负责，与 `network.assetEndpoint` 的语义一致。Rejected: 只认静态 allowlist——自定义审核服务地址用不了 |
| 10 | 安装来源与包摘要持久化：`extension_state` 增加 `source`（`store` / `local` / `dev`）与 `package_sha256`；加载审核器前重算已装 WASM 的摘要并比对 | 第 3 条的判定需要可信的来源记录；比对摘要防止商店安装后被同名本地包覆盖或篡改。Rejected: 只在安装时判——之后覆盖安装就绕过了 |
| 11 | 审核问题由扩展在 `describe()` 里声明（id + 各语言名称），审核结果只返回问题 id；宿主按声明显示，未知 id 原样显示 | 宿主不再写死 `destructive` / `disruptive` / `remote_code` 三条。Rejected: 扩展直接返回显示文字——审计里存的是语言相关的字符串，无法统计 |

## 审核器扩展点

- **声明。** manifest 的 `capabilities` 新增 `commandReview: true`，安装确认里醒目提示"此扩展可以自动放行命令"。`describe()` 新增 `reviewer` 段：问题列表（id、各语言名称）、单批最大条数、单条命令最大长度。只有声明了能力又在 `describe()` 里给出 `reviewer` 的扩展才算审核器；二者缺一则加载失败并说明原因。
- **SDK。** `extsdk.CommandReviewer(questions, func(ctx *ReviewContext, items []ReviewItem) ([]ReviewResult, error))`。`ReviewItem` 含资产类型、已替换敏感信息的命令、命令写法（shell / sql / redis / text 等，取自宿主注册表）；不含资产 ID、名称、配置。`ReviewResult` 为 `pass` / `reject`（带未通过的问题 id 与可选分数）/ `error`（带原因 id）。
- **宿主调用。** `extension.Plugin.ReviewCommands` 走现有实例池（每扩展最多 4 个实例，`pkg/extension/runtime.go:42`）。超时由宿主按设置控制，到时取消调用。
- **结果校验。** 条数与输入不一致、出现未声明的问题 id、`reject` 不带问题 id、JSON 解析失败：整批按"审核失败（扩展返回无效结果）"处理。扩展 panic 由运行时恢复并记录，同样按失败处理。
- **宿主侧流程。** `command_review_svc` 不再自带 Evaluator，改为调用"当前生效的审核器"（第 5 条）；没有审核器时返回失败原因"未安装或未启用审核扩展"。`applyReviews` 与按模式落结果不变。

## opsctl 内运行审核器

- 启用了辅助审批或 Autopilot 且有需要审核的命令时，opsctl 才加载生效的审核器扩展：仅编译、实例化这一个扩展，其余扩展仍走 describe-only。
- 加载前做与桌面端相同的来源与摘要检查（第 10 条）；检查不过按"审核失败"处理，并在 stderr 给出原因。
- 设置、密钥、缓存都在共享数据库里，桌面端与 opsctl 读同一份。opsctl 发 HTTP 走与桌面端相同的网络能力检查与 `netdial`。

## 扩展级设置

- `describe()` 新增 `settingsSchema`（JSON Schema，支持 `title` / `description` / `default` / `enum` / `format: password | endpoint`，以及现有资产表单支持的字段级校验）。
- 设置 → 扩展 → 某扩展详情里显示设置表单；保存时宿主按 schema 校验，密钥字段加密后入库，读回界面时只显示"已设置"。
- 扩展通过新的 host 函数 `settings_get` 读非密钥字段；密钥字段只能以占位形式用在 HTTP 请求头里（第 8 条）。
- 设置变更时清空该扩展的审核缓存（缓存 key 含扩展名、版本、设置的哈希）。

## 审计与界面

- 审计的审核信息增加扩展名与版本；其余字段（模式、结果、未通过的问题、分数、耗时、是否命中缓存）沿用现有结构。
- 审批弹窗的未通过原因按扩展声明的问题名称显示。
- 设置 → AI 审核：选择生效的审核器（列出已安装且有资格的审核扩展，第 3 条不满足的置灰并说明原因），以及宿主侧的超时、重试设置。

## 不在本步范围

- 迁移 TypeSafe 审核、删除 `internal/pkg/typesafe` 与 `config.json` 的 `CommandReview*` 字段（第 2 步）。
- 第三方审核器（第 3 条已否决）。
- opsctl 内运行审核器以外的扩展代码。

## 验证

- 单测：结果校验各分支、来源与摘要检查、`{{setting.*}}` 只发往声明地址、设置加密与回读、ABI 2.3 拒绝旧宿主。
- 用 `pkg/extension/testdata` 下的测试扩展做一个假审核器（按命令关键词返回结果），覆盖：辅助审批通过 / 不通过 / 失败，Autopilot 通过 / 拒绝 / 失败，`Unreviewable` 不送审。
- `make dev-sandbox` 下：桌面端关闭时 `opsctl exec` 仍完成审核；本地安装的审核扩展在正式版构建上被拒绝、在开发版上可用并标"未经签名"；`node e2e/oracle.mjs audit` 能看到扩展名与版本。

## 实现说明（与上文的差异）

- **内置审核已经去掉（第 2 步完成）。** 命令审核只由审核扩展来做，仓库里的官方实现是 `extensions/jev-command-review`（TypeSafe Jev，题目和判定在 `internal/pkg/jevreview`）。宿主的 `config.json` 只剩审核超时和选用的扩展名；API key、服务地址、模型、阈值都是扩展设置。
- **没选审核扩展时自动选用。** 与第 5 条不同：设置里没选时，启用中的审核扩展只有一个就用它（只读 `manifest.json` 的 `capabilities.commandReview`，opsctl 不加载扩展也能判断）；一个都没有、或有多个却没选，按 `no_reviewer` 审核失败并提醒用户。
- **纯审核扩展不需要资产类型。** 描述符校验原本要求至少一个资产类型和策略面；声明了 reviewer 的扩展可以都不声明，但也就不能声明工具。
- **`settings_get` 对已设置的密钥给出占位。** 扩展据此判断有没有配置 API key，仍然拿不到明文。
- **审核超时由宿主统一设置**，每次调用单独计时、超时重试一次；批次并发调用，上限与扩展实例池一致。
- **审核扩展的 `error` 回答不进缓存。** 它表示"没能得到判断"（如限流），缓存会让同一条命令在缓存有效期内一直审核失败。
- **测试审核服务。** 设置页用在用的审核扩展审一条无害命令（不读写缓存），直接显示审核结果。
