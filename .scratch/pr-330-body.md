## 变更说明 / Summary

实现 #327。命令没被规则放行、原本要问人时，先交给 Jev 格式（TypeSafe System One API）的模型审核一次，只拦破坏数据、中断服务、下载并执行代码这类明确的危险操作。每台服务器（或服务器分组）可以选一种权限模式：

| 模式 | 审核通过 | 审核未通过 / 审核失败 |
|---|---|---|
| 默认 | 不审核，问人（和今天一样） | — |
| 辅助审批 | 自动执行 | 转人工确认 |
| Autopilot | 自动执行 | 直接拒绝，把原因返回给调用方的 agent |

设计和取舍见 #327 和 `docs/specs/2026-09-29-assisted-approval-autopilot.md`。

### 主要改动

- **权限检查**（`internal/ai/permission/review.go`）：规则判断之后，把"需要人确认"的结果按权限模式交给模型审核。批量执行时一次送审，并行调用模型。禁止规则、放行规则、授权记录的判断不变，默认模式下和今天完全一样。
- **审核服务**（`internal/service/command_review_svc/`）：三道是非题，任意一题回答"是"的概率达到阈值（默认 0.5）就不通过。评分按原始命令的哈希缓存 7 天，命中时按当前阈值重新判断。
- **发送前替换敏感信息**：只换值，不动命令结构（分隔符、管道、重定向、`$(…)` 都原样留给模型看）。shell 命令用解析器逐个参数判断，解析不了就不发送；名字带 password / token / secret / key 的变量和参数都替换（表示文件位置的 `…_FILE` / `…_PATH` / `…_DIR` 除外）；常见服务的密钥格式取自 betterleaks（MIT）的规则。
- **拆不开的 shell 命令不送审**：禁止规则没法逐条检查这类命令，所以辅助审批照常问人，Autopilot 直接拒绝。
- **Autopilot 的资产不接受授权申请**（AI 的 `request_permission`），无人值守时不会弹窗等人。
- **有管道输入的命令不送审**：`opsctl exec` 会把 stdin 转发给 ssh 命令，模型只看得到命令本身、看不到管道里的内容（`cat x.sh | opsctl exec host -- bash` 里模型只看到 `bash`）。这类命令辅助审批照常问人，Autopilot 直接拒绝；规则放行和人工确认的命令不受影响。
- **审核失败的处理**：超时或连接出错时自动重试一次；Autopilot 审核失败时按原因告诉调用方下一步——超时、服务不可用可以稍后重试，命令过长要缩短或拆开，无法解析要先修正语法，配置问题留给用户。
- **审核由扩展完成**：命令审核器是一个扩展能力（`capabilities.commandReview` + `describe()` 的 `reviewer` 段 + `review_commands` 入口），官方实现是 `extensions/jev-command-review`。宿主只负责和具体服务无关的部分：找出当前生效的审核器、展开解码后执行的命令、替换敏感信息、并发/超时重试、缓存和失败归类。审核器只能把"需要确认"改成放行，碰不到规则拒绝和 `Unreviewable` 命令；缺失、出错、超时、结果不合法一律按审核失败处理。
- **opsctl 也能审核**：桌面端没开时，opsctl 在进程内按需加载并运行当前生效的审核扩展（先核对启用状态与包摘要），其余扩展仍只读缓存的 `describe()`。
- **扩展级设置**：扩展在 `describe()` 里声明 `settingsSchema`，宿主渲染表单并校验；`format: "password"` 的字段经 `credential_svc` 加密存储，扩展只能拿到 `{{setting.<field>}}` 占位，宿主只在发往该扩展声明的 `format: "endpoint"` 地址时替换；带密钥的请求也只在声明地址内重定向。
- **审核器资格**：安装时记录来源（`store` / `local` / `dev`）与包摘要（wasm + `manifest.json`），加载审核器前重算比对。正式版只接受商店安装，nightly / 开发版也接受本地安装并标注"未经签名"。
- **设置 › AI › 命令审核**：显示当前生效的审核扩展、切换审核器、宿主侧超时，以及本次启动以来最近一次还没恢复的审核失败（之后有一次审核成功就不再显示）。API key、服务地址、模型、阈值属于扩展，在扩展详情里设置，并提供"保存并测试"。
- **界面**：
  - 资产详情和分组详情里的权限模式卡片，沿用分组设置时写明沿用的是哪个分组；
  - 审批弹窗和终端提示里写明审核结果；
  - 审计页标出审核过的记录，详情里显示权限模式、每道题的评分、审核扩展与版本。
- **数据库迁移** `202609290001`：新增 `command_reviews` 缓存表，以及 `assets` / `groups.permission_mode`、`audit_logs.review` 两列。`202610140001` 给 `extension_state` 增加 `source` 与 `package_sha256`。
- **顺带修复**：#124 之后分组右键菜单没有了"分组详情"入口，分组的命令权限和权限模式都没法设置，现在加回来了。

## 关联 Issue / Related Issue

Closes #327

## 截图 / Screenshots

截图来自实机：审核由 `jev-command-review` 1.0.1 调 TypeSafe Jev 完成。主机、资产名、会话 ID 和命令里的敏感片段已打码。

**设置 › 扩展：已安装的 Jev 命令审核**

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/01-extensions.png" width="762">

**扩展详情：自动放行提示、API key / 服务地址 / 模型 / 阈值，以及三道审核题**

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/02-extension-settings.png" width="512">

**分组详情：权限模式设为辅助审批；命令权限在同一页设置**

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/03-group-mode.png" width="762">

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/04-group-policy.png" width="762">

**资产详情：权限模式沿用分组，或自己设为默认（问人）**

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/05-asset-mode.png" width="762">

**设置 › AI › 命令审核：当前审核扩展（未经签名的开发安装）、宿主侧超时**

<img src="https://raw.githubusercontent.com/Pililink/opskat/bf1368ce5c4116bb322e9ddc0b5c8e0118766447/09-command-review.png" width="762">

**审计页：审核通过的记为 assisted，未通过后由人确认的记为 user，并带审核标志**

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/06-audit-list.png" width="800">

**详情：审核通过，由 jev-command-review 1.0.1 判断**

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/07-audit-pass.png" width="700">

**详情：审核未通过（破坏数据 0.95），人确认后执行，记为 user_allow**

<img src="https://raw.githubusercontent.com/Pililink/opskat/8c2419c5a6ec479512a1dbc1f02edd58d8f5fec5/08-audit-reject.png" width="700">

## 测试情况

- **单元测试 / CI**
  - CI 全部通过：Go Lint、Go Test、Wails E2E、前端 Lint / Test、Windows 和 macOS 的本地 IPC。
  - 本地 Windows 上 `cmd/opsctl/command`、`internal/ai/{execimpl,helper,tool}` 有 cp / SFTP / 符号链接相关的测试失败，在本分支改动之前的提交上结果相同，和本 PR 无关。
- **实机**：真实服务器 + 真实 Jev 模型。
  - AI 对话（代码审查修复后的版本）：
    - Autopilot：审核通过的自动执行；`truncate` 日志、`uv cache clean`、`docker builder prune` 等被判为破坏数据，直接拒绝，AI 按拒绝原因停下；AI 在 Autopilot 资产上的授权申请被拒，并提示直接执行、逐条审核。
    - 辅助审批：审核通过的自动执行；`rm -rf` 缓存目录这类被判为破坏数据的命令弹出确认框，框里写明审核结果，人确认后执行，审计记为 `user_allow` 并带审核结果。
    - SSH（Linux、macOS）和数据库资产都覆盖到，每次审核 0.4–0.9 秒。
  - 辅助审批（opsctl）：白名单外的只读命令审核通过后自动执行；`rm -rf`、`curl … | sh` 审核未通过，转人工确认，弹窗写明原因；拆不开的命令不送审、直接问人，拒绝后审计记为 `user_deny`。
  - Autopilot（opsctl）：审核通过的自动执行；审核未通过、审核失败（命令过长）、拆不开的命令、有管道输入的命令直接拒绝，拒绝原因写明下一步；规则放行或禁止的命令不送审。全程没有弹窗。
  - 管道输入：`… | opsctl exec … -- bash` 被拒；stdin 是空设备、已关闭的空管道时照常审核；规则放行的命令，管道内容照常转发到远端。
  - 缓存：`opsctl exec` 和 `opsctl batch`、SSH 和数据库资产都能互相命中；换模型后不命中旧缓存。
  - 实测评分：只读命令 ≤ 0.02，`systemctl restart` 0.51–0.55，`rm -rf` 0.64–0.70，`curl … | sh` 0.99。题目的"否"按设计补全只读查看之后重测，评分基本不变（只读 0.01，restart 0.53，rm -rf 0.67，curl | sh 0.99）。
- **评估**：`testdata/eval_commands.jsonl` 里有 210 条公开样本，配上 API key 可以跑 `TestEvalAgainstJev`。默认阈值 0.5 按实机试用的评分选定，还需要用评估样本确认。

## 试用中发现、已在本 PR 修复

- 通过管道送进 `opsctl exec` 的内容没有被审核：有管道输入的命令不再靠审核放行（见上）。
- Autopilot 因配置问题、命令过长审核失败时也提示"可以稍后重试"：改为按原因给出下一步。
- opsctl 每次都是新进程，第一次请求偶尔超时：超时或连接出错时自动重试一次。
- `opsctl exec` 传的是审批类型的别名（`exec` / `sql`），模型看到的资产类型不对，缓存也和 batch、AI 对话分成两份：送审前统一换成规范资产类型。
- 切换权限模式时显示"已保存..."：改为"保存中..."。

## 代码审查后修复

按仓库规范和 #327 / 设计文档各审查了一遍，查出的问题都已修复：

- 替换敏感信息时，引号里的值会连同紧跟的重定向一起被换掉（`bash -c 'API_TOKEN=abc>/root/.ssh/authorized_keys'` 送审时变成 `bash -c 'API_TOKEN=***'`）；`ENCRYPTION_KEY=…`、`--key …` 没有替换。
- 保存过的 API key 解不开时被当成"未配置"：现在单独报"API key 无法读取"，按配置错误提醒。
- 设置页的"最近一次审核失败"在审核成功后仍一直显示：现在之后有一次审核成功就不再显示。
- 多源 cp 转人工确认后，审计行没有审核结果。
- opsctl exec 在默认模式下也会预读 stdin，可能多等 200ms：现在只在开了模型审核时才预读。
- 有两道题的"否"没写全只读查看；题目版本升到 3。
- 设置校验与设置页不一致（阈值、超时为 0 时）。
- 误提交的 pnpm 占位配置、过时的注释、只在重复实现常量的测试，以及几处代码整理（结果类型、失败原因的说明集中成一张表、权限模式卡片自己管保存状态）。

之后又专门过了一遍边界情况，补上三处：

- 兼容服务返回不合法的答案（概率为负、超过 1、缺概率、题型不对）以前会被当成低风险放行，现在按审核失败处理。
- 名字表示文件位置的变量（`KEY_DIR`、`MYSQL_PASSWORD_FILE`、`TOKEN_PATH`）不再替换：值是路径，换掉会藏起命令动的是哪里。
- 裸值到 `<` `>` 为止只用在 shell 命令里；SQL、etcd 等纯文本里它们是值的一部分，以前会漏出密码的后半截。

- 审核器插件化，官方实现是 `extensions/jev-command-review`（TypeSafe Jev，题目与判定在 `internal/pkg/jevreview`），默认模型 `jev-latest`。
- 审核结果按题目返回逐题评分；`error` 表示没得到判断，按审核失败处理且不进缓存。
- 去掉内置审核后遗留的字段与常量（`Model` / `Threshold`、未使用的重试判断等）。

审核服务没注册时不审核（"nil 判断"）是设计文档和 ARCHITECTURE 里写明的约定，保留。

## 已知限制（后续处理）

- 审核 cp 时模型只看到路径，不知道这是一次覆盖写入。
- 读取密钥（`cat /etc/shadow`、`kubectl get secret -o yaml`）不在三道题的范围里，按设计只拦明确的危险。
- 修改权限模式没有写审计；资产和分组的其他设置改动目前也都不写。
- 审核缓存表里过期的记录不会被清理。
- 多文件 cp 逐个串行审核。
- 审核缓存没命中时，GORM 会打一条 `record not found` 的 error 日志（查询用的是 `First`，代码本身把没命中当正常情况处理）；仓库里授权记录的查询也有同样的日志。

## 自检 / Checklist

- [x] Fixes mentioned issues / 修复已提及的问题
- [x] Code reviewed by human / 代码通过人工检查
- [x] Changes tested / 已完成测试


