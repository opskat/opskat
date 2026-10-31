# 命令审核器与扩展设置迁到 hostABI 1.0：贡献点、宿主专用工具与依赖倒置

> Status: Approved
> Owner: OpsKat 维护者
> Last updated: 2026-10-31

**Objective:** 把 PR #330（辅助审批 + Autopilot）里由扩展提供的命令审核与扩展级设置，重做为 hostABI 1.0 的贡献点与基础权限：扩展运行时不出现任何「审核」「设置」专用代码，审核器经一种新的工具声明「只给宿主用」被调用，设置复用资产类型配置的同一套 schema、密钥加密与认证注入；宿主侧权限检查、审核服务、审核器注册表之间按接口注入。配套扩展 `jev-command-review` 迁到 1.0。PR #330 对用户可见的行为保持不变。

**Hard invariant:**
- 权限模式为默认（或未设置）时，命令的放行、拒绝、确认与今天的 main 完全相同。
- 审核器只能把规则判为「需要确认」的结果改为放行；规则拒绝、规则放行、拆不开的 shell 命令（`Unreviewable`）、有管道输入的命令都不送审。审核器缺失、不合资格、出错、超时、回答不合法，一律按审核失败处理：辅助审批问人、Autopilot 拒绝，绝不因此放行。
- 扩展设置里的密钥明文不进入扩展；密钥只注入发往该扩展设置里声明的服务地址的请求，重定向也只能跟到这些地址。
- 已迁到 1.0 的扩展（Oracle、Elasticsearch 0.2.0、仓库内 notebook）的加载与行为不变。

## 所处位置

[已核实] PR #330（`Pililink:feature/assisted-approval-autopilot`，允许维护者推送）基于 main `c60d0bf8`，即 hostABI 1.0 重置（#357，见 [2026-10-27 规格](2026-10-27-ext-abi-v3.md)）之前；它的扩展部分建在旧 ABI 2.6 上，与 main 冲突。配套扩展 opskat/extensions#7（`jev-command-review`，同样允许维护者推送）声明 `hostABI: "2.6"`。**[用户决定]** 直接在这两个 PR 分支上处理：先并入 main，再按本规格重构，做好解耦、接口与注入点。

PR #330 自己的两份规格：[辅助审批与 Autopilot](2026-09-29-assisted-approval-autopilot.md) 仍是功能行为的依据；[扩展命令审核器扩展点](2026-10-14-extension-command-reviewer.md) 中扩展点、扩展级设置、网络放行的设计由本规格取代。

## Problem

标注：**[已核实]** = 对照 PR 分支 `4511d50f`（`refs/remotes/pr330/head`）或 main（#357 合并后）源码确认；**[用户决定]** = 本轮对话中用户选定。

1. **运行时又长出了业务能力。** [已核实] PR 在 `pkg/extension` 里新增审核专用的 guest 入口 `review_commands`（`runtime.go:408-417`）、manifest 能力 `capabilities.commandReview`、describe 的 `reviewer` 段与结果校验（`review.go`），hostABI 升到 2.6（`manifest.go:62`）。这正是 1.0 重置刚消除的「能力按业务长」（2026-10-27 规格问题 2）与「调用单元有多套」（问题 3）。
2. **运行时认识「扩展设置」。** [已核实] 设置占位 `{{setting.x}}` 的请求头替换（`settings_header.go:33`）、按设置里的地址放行网络（`host_capability.go:108-128`）、宿主调用 `settings_get` 都在运行时里。设置里填的地址可被访问，但它不是 manifest 权限，安装确认里无从审视。
3. **设置借用扩展自己的 KV 存储。** [已核实] 设置存在扩展 KV 的保留键 `ext.settings`（`extension_svc/settings.go:14`），为此又要拦住扩展经 KV 读写这条记录；与资产配置已有的 schema、密钥加密、认证注入是两套。
4. **依赖方向反了。** [已核实] `internal/ai/permission` 为拿到命令写法枚举而 import `command_review_svc`（`type_registry.go:14,37`）；`command_review_svc` 直接使用 `pkg/extension` 的审核类型。权限检查依赖了审核服务的实现。
5. **opsctl 为审核器开了专门的加载模式。** [已核实] `LoadReviewersOnDemand`（`active_reviewer.go:80-83`，`cmd/opsctl/command/ext.go:60`）；而 main 上 opsctl 执行扩展工具已有按需激活扩展的通用路径（`cmd/opsctl/command/ext.go:51-89` → `extension_svc` `Activate`）。
6. **迁移混了多个归属方。** [已核实] 迁移 `202609290001` 同时建审核缓存表、给 `assets` / `groups` 加 `permission_mode`、给 `audit_logs` 加 `review`；任何一处要改都牵动其余。

## Actors and user stories

1. 作为**运维用户**，我想给服务器或分组选辅助审批 / Autopilot，让规则没覆盖到的命令先经模型审核，与 PR #330 描述的行为一致。
2. 作为**运维用户**，我想在安装审核扩展时就看到「它可以自动放行命令」和「它会访问我在设置里填的服务地址」，再决定装不装。
3. 作为**扩展作者**，我想用与资产类型配置相同的写法声明扩展设置（字段、密钥、服务地址、认证注入），用一个普通工具实现审核，不学第二种调用方式。
4. 作为**宿主维护者**，我想改审核服务、权限检查或扩展运行时中的任一个时，不必同步改另外两个。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 审核器是贡献点 `commandReviewer`，由命令审核子系统注册、校验、接入与撤销；扩展运行时不出现审核专用代码 | 与 1.0 决定 2 一致：业务规则回到规则的主人。Rejected: 保留 guest 入口 `review_commands` 只改成贡献点——又多一种可调用单元；只把 2.6 字段改名为 1.0 写法——把 1.0 刚拆掉的耦合带回来 |
| 2 | 工具新增第三种声明「只给宿主用」（host-only）：`exec` 与页面都不能调用，`help` 不列出，只由引用它的贡献所属子系统调用；每个工具恰好是策略动作、只给页面用、只给宿主用三者之一 | 一种可调用单元（1.0 决定 6），审核调用复用工具的超时、取消、参数校验与实例池。PR 规格拒绝「用工具冒充审核器」的理由是会进 AI 工具与策略链路，host-only 正好排除这一点。Rejected: 用只给页面用的工具——扩展页面也能调到审核逻辑，语义不对 |
| 3 | 审核按**一条命令一次调用**：工具参数为资产类型、命令、命令写法三个字符串，回答为一条结果；多条命令由宿主并发调用（上限与扩展实例池一致） | 工具参数只支持标量与字符串数组，批量对象数组需要扩展参数 schema；Jev 本来就一次只审一条（`maxItems = 1`），模型调用 0.4–0.9 秒远大于一次实例往返。Rejected: 扩展工具参数支持对象数组——为一个用例扩大所有工具的参数契约 |
| 4 | manifest 预先声明 `contributes.commandReviewer: true`，并须与 describe 一致；安装、升级与 `opsctl ext dev` 确认把它标为高风险 | 「能自动放行命令」必须在运行扩展代码前告诉用户，与驱动 id 预先声明（1.0 决定 7）同一理由；opsctl 也能不运行代码就知道谁是审核器。Rejected: 只看 describe——安装确认读不到 |
| 5 | 扩展设置是贡献点 `settings`，由扩展管理子系统拥有，写法与资产类型的配置相同：`configSchema`（含 `format: password` / `format: endpoint`）+ `auth`（与资产类型同一种认证注入声明） | 复用已有的 schema 校验、表单、密钥加密与认证注入，扩展拿不到明文的保证与资产相同。Rejected: PR 的 `{{setting.x}}` 请求头占位——运行时要认识设置，且与资产认证注入是两套 |
| 6 | **[用户决定]** 新增权限 `network.settingsEndpoint`：声明后，扩展设置里 `format: endpoint` 字段填的地址可达，`auth` 只注入到这些地址；内网地址与资产端点同理放行（地址由用户自己填写）；安装确认列出该项 | 用户可配置的服务地址是一项新的网络可达范围，按 1.0 安全契约应在 manifest 里声明、安装时可见。Rejected: 复用 `network.assetEndpoint`——一个权限覆盖两类地址，用户分不清；只用静态 `network.http` 白名单——丢掉自定义服务地址 |
| 7 | 设置的作用域只用于不带资产的调用（host-only 工具、页面不带资产的调用）；带资产的调用仍是该资产的作用域 | 运行时只需允许「无资产作用域也可带端点与认证注入」，不必合并两类端点各自的认证。Rejected: 每次调用都并入设置端点——作用域要按端点分别注入认证，现在没有需求 |
| 8 | 依赖倒置：权限检查拥有审核端口与命令写法（shell / redis / text），审核服务实现端口、由启动代码注入；审核服务经「审核器来源」接口取当前审核器，审核器注册表由 `commandReviewer` 贡献点填充；审核服务不 import 扩展运行时 | 三者可以独立修改与测试（用户故事 4）。Rejected: 保持 PR 现状——权限检查依赖审核服务实现，审核服务依赖运行时类型 |
| 9 | 解码执行展开（base64 解码后执行的 shell 写法）与敏感信息替换是纯算法，各自独立成包，审核服务只做编排（替换、展开、长度、缓存、并发、重试、状态） | 两者共约 1300 行、与审核器无关，独立后各自可测、可复用。Rejected: 留在审核服务包内——服务包同时承担算法与编排 |
| 10 | opsctl 不再有审核器专用加载模式：需要审核时按已有的扩展激活路径激活当前审核器所在扩展，激活前先做资格检查 | 复用 main 上 opsctl 执行扩展工具已有的路径。Rejected: 保留 `LoadReviewersOnDemand`——一条只为审核存在的加载路径 |
| 11 | 迁移按归属方拆开，换用新的迁移 ID；`permission_mode` 仍作 `assets` / `groups` 的列；设置改存独立的 `extension_settings` 表 | 一处改动只牵动它的归属方；新 ID 让已在本地跑过旧 ID 的开发库也能补齐（每个迁移可重复执行）。`permission_mode` 用列与分组上已有的各类策略列一致，独立表需要另行接入备份 / 恢复与删除资产、分组时的清理。Rejected: 保留合在一起的迁移；把 `permission_mode` 放进权限子系统自有的表——本轮收益小于接入成本 |
| 12 | 本轮新增的字段与权限并入 hostABI `1.0`，不升版本 | 1.0 尚未随正式版发布（v1.15.0 未发）。若 v1.15.0 在本 PR 合并前发布，则改为 `1.1` 并按 1.0 规格「版本与旧扩展」的方式处理 |

## 安全契约（manifest）

在 1.0 的 manifest 上增加两项，其余不变：

- `permissions.network.settingsEndpoint`（布尔）：可访问本扩展设置里 `format: endpoint` 字段的地址（决定 6）。
- `contributes.commandReviewer`（布尔）：本扩展提供命令审核器（决定 4）。

二者都按 1.0 的严格解析处理：取值不合法时 manifest 解析失败并点名字段。安装、升级与 `opsctl ext dev` 的确认逐项列出它们；`contributes.commandReviewer` 标为高风险，说明它在辅助审批与 Autopilot 下可以让命令不经人工确认就执行；升级时新增的项标为新增。

## 工具：只给宿主用

- 工具声明 `hostOnly`。每个工具必须恰好是策略动作、只给页面用、只给宿主用三者之一；两者以上或都没有，加载失败并点名工具。
- 经 `exec`（AI 与 opsctl）调用 host-only 工具时返回「该工具只供宿主调用」，`help` 不列出它；页面调用时同样拒绝并点名。
- host-only 工具的超时规则与策略工具相同（默认 30 秒，最长可声明 10 分钟，不能声明不设超时）；调用方可在此上限内给更短的超时。
- host-only 工具不要求扩展提供资产类型。

## 扩展设置

**声明。** describe 的 `contributes.settings` 含 `configSchema` 与可选的 `auth`，规则与资产类型的同名部分相同：schema 只用宿主支持的子集，`format: password` 字段加密保存，`auth` 的绑定只引用 schema 里的字段。另外：

- 声明了 `format: endpoint` 字段或 `auth` 时，权限里必须有 `network.settingsEndpoint`，否则加载失败并点名；声明了 `network.settingsEndpoint` 却没有 `format: endpoint` 字段，同样加载失败。
- schema 在加载时校验（PR 只在保存设置时校验字段）。

**编辑与保存。** 设置 › 扩展 › 某扩展详情显示设置表单，与扩展资产类型的配置表单使用同一个表单组件与同一套校验。密钥字段读回界面时只显示「已设置」，留空表示保留原值。保存时按 schema 校验，失败时表单显示错误、不保存。卸载扩展并选择清除数据时，设置一并删除。

**扩展读取。** 扩展可读到非密钥字段的值，以及每个密钥字段「是否已设置」；任何调用都读不到密钥明文。扩展经 KV 存储读写不到设置。

**网络与认证。** 不带资产的调用（host-only 工具、页面不带资产的调用）的作用域带上设置里的端点与认证注入：

- 声明了 `network.settingsEndpoint` 时，发往这些端点的请求可达，并按 `auth` 注入认证；重定向只跟到这些端点，越界的重定向失败。
- 发往其他地址的请求不注入任何设置里的值；按 `network.http` 白名单照常判断。
- 带资产的调用不受设置影响（决定 7）。
- 被拒绝的请求记录一条点名扩展与目标的拒绝日志，与现有网络门禁一致。

## 命令审核器

**声明。** describe 的 `contributes.commandReviewer` 给出：

- `tool`：一个本扩展的 host-only 工具名，其参数恰好是必填的字符串 `assetType`、`command`、`syntax`；
- `questions`：审核题目，每题有 id（小写字母开头，字母数字下划线，最长 64）与各语言名称，至少一题，id 不重复；
- `maxCommandLen`：单条命令最大长度。

manifest 与 describe 必须同时有或同时没有审核器；工具不存在、不是 host-only、参数不符，加载失败并点名。

**调用。** 审核服务对每条要审的命令调用一次该工具，参数是规范资产类型、替换敏感信息后（必要时先展开解码执行）的命令、命令写法。工具回答一条结果：`pass`；`reject`（未通过的题目 id 与可选的 0–1 分数）；`error`（原因 id）。回答不是这三种之一、出现未声明的题目 id、`reject` 不带题目、分数越界、调用出错或超时，都按审核失败（无效结果或超时）处理。`error` 不进缓存。

**注册与选择。** 贡献接入时，扩展成为审核器候选；撤销（停用、卸载、升级替换）时移除。当前审核器的选择规则沿用 PR：设置里选了且候选可用就用它，没选时启用中的候选只有一个就用它，否则按「没有审核器」审核失败并提示用户。

**资格。** 沿用 PR：安装时记录来源（商店 / 本地 / 开发）与包摘要（wasm 与 manifest），使用审核器前重算摘要比对；正式版只接受商店安装的审核器，非正式版也接受本地与开发安装并在设置里标「未经签名」。资格不符按审核失败处理并说明原因，不运行扩展代码。

**opsctl。** 桌面端没开时，opsctl 在需要审核时才激活当前审核器所在的扩展（与 `opsctl exec` 执行扩展工具同一条激活路径），激活前完成资格检查；不需要审核的命令不激活任何扩展。

## 宿主侧结构

- **权限检查**拥有审核端口与命令写法：内置类型注册时声明写法，扩展资产类型按纯文本。端口由启动代码注入；没注入时（例如测试）辅助审批与 Autopilot 按审核失败处理。
- **审核服务**实现该端口：敏感信息替换、解码执行展开、长度检查、缓存（按扩展名、版本、设置摘要、资产类型与原始命令）、并发、超时重试一次、最近一次未恢复的失败状态，行为与 PR 相同。它经「审核器来源」接口取当前审核器，不依赖扩展运行时的类型。
- **审核器注册表**由 `commandReviewer` 贡献点填充，按上文规则给出当前审核器；对审核服务而言它就是审核器来源。
- 桌面端与 opsctl 注册同一组贡献点（在 1.0 现有六个之外加 `settings` 与 `commandReviewer`）。

## 数据

按归属方拆成独立迁移，均可重复执行，使用新的迁移 ID（PR 原有的 `202609290001`、`202610140001` 删除）：

- 审核缓存表（命令审核）；
- `assets.permission_mode`、`groups.permission_mode`（权限）；
- `audit_logs.review`（审计）；
- `extension_state` 的安装来源与包摘要（扩展管理）；
- `extension_settings` 表：扩展名唯一，值为 JSON，密钥字段为密文（扩展管理）。

## 不变的 PR 行为

以下按 PR #330 现有实现保留，只随上文结构调整位置，不改变可观察行为：三种权限模式与沿用分组的解析；审批弹窗、终端提示、审计页对审核结果的展示；Autopilot 资产拒绝授权申请；批量执行与多文件 cp 一次送审；有管道输入的命令判断；审核失败按原因给出下一步；设置 › AI › 命令审核（当前审核器、宿主侧超时、最近一次未恢复的失败、保存并测试）；分组右键菜单的「分组详情」入口；cp 远端路径在 Windows 上的修正。PR 对 `TableDataTab` 的改动丢弃，以 main 的修复为准。

## 扩展仓迁移（jev-command-review）

- manifest：`hostABI: "1.0"`，`minAppVersion` 为包含本次改动的第一个正式版，`permissions.network.settingsEndpoint: true`，`contributes.commandReviewer: true`；版本 `0.1.0`（尚未发布过）。
- describe：`settings`（API key 为密钥字段、服务地址为端点字段、模型、阈值；`auth` 把 API key 注入为该服务要求的请求头）、`commandReviewer`（三道题、单条长度上限）、一个 host-only 审核工具。
- 扩展代码不再拼接密钥占位；不再自带重试；题目与判定仍在扩展自己的包里。

## Out of scope

- 把 `groups` 上按类型分列的各种策略（command / query / redis / mongo / kafka / k8s / etcd / oss / ext）收拢成统一结构：涉及所有资产类型，另开一轮。
- 第三方（非商店签名）审核器在正式版上可用。
- 带资产的调用使用扩展设置里的端点（决定 7）。
- PR 列出的已知限制：cp 覆盖写入的语义、读取密钥类命令、权限模式修改不写审计、缓存未命中时 GORM 的 `record not found` 日志。
- 宿主发版与商店发布本身。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `settings`、`commandReviewer` 贡献点的 Check / Attach / revoke | 声明规则与错误点名、manifest 与 describe 一致、工具必须 host-only 且参数相符、撤销后候选消失 | `internal/extreg/*_test.go`、`snippet_svc` 的贡献点测试 |
| 工具三选一与 host-only 拒绝 | 声明组合的加载错误；`exec` 与页面调用 host-only 工具被拒 | `internal/extreg/tools_test.go`、`command_test.go` |
| 运行时网络门禁 | 无资产作用域的端点可达与认证注入、越界重定向失败、其他地址不注入、未声明 `network.settingsEndpoint` 时拒绝 | `pkg/extension` 现有门禁与 `auth.go` 测试 |
| manifest 与安装确认 | 两项新字段的解析、高风险与新增标注（Go 与前端） | `pkg/extmanifest`、`install_confirm` 与 `InstallConfirmDialog` 测试 |
| 权限检查 × 审核端口 | 三种模式 × 通过 / 未通过 / 失败的结果矩阵、不送审的几类命令、端口未注入时按失败处理 | PR 的 `permission/review_test.go`（保留并改用端口替身） |
| 审核服务 | 替换、展开、缓存、并发、重试、无效结果、`error` 不进缓存、经审核器来源取审核器 | PR 的 `command_review_svc/review_test.go` |
| 替换与展开两个算法包 | PR 现有的替换与解码用例 | PR 的 `redact_test.go` 与解码用例 |
| 扩展设置服务 | 加密保存、留空保留、读回只显示已设置、扩展读不到明文、清除数据时删除 | PR 的 `extension_svc/settings_test.go` |
| 资格与选择 | 来源与摘要、正式版 / 非正式版、自动选择唯一候选 | PR 的 `active_reviewer_test.go`、`provenance_test.go` |
| 迁移 | 每个迁移可重复执行，开发库已跑过旧 ID 时仍补齐 | PR 的 `migration2026*_test.go` |
| 前端 | 设置表单复用、安装确认新项、PR 现有的界面测试 | PR 的前端测试 |

不能自动化、由运行时验收覆盖（`make dev-sandbox`，仓库内的假审核扩展覆盖宿主全部流程；**[用户决定]** 另用迁移后的 `jev-command-review` 连真实 TypeSafe 服务，需要 `.env` 提供 API key）：

- 桌面端：安装确认的新项与高风险；扩展设置表单保存、读回与「保存并测试」；辅助审批与 Autopilot 下 AI 执行的通过 / 未通过 / 失败；审批弹窗与审计里的审核结果。
- opsctl：桌面端关闭时完成审核；不需要审核的命令不激活扩展；有管道输入、拆不开的命令不送审。
- 密钥只发往设置里的服务地址（本机 mock 服务观察请求头）。
- 真实 Jev：只读命令通过、危险命令未通过、错误 API key 按配置错误提示。

## Open questions

无。
