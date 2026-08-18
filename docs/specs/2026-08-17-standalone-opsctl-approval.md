# 独立 opsctl：终端审批人与授权规则

> Status: Approved
> Owner: OpsKat maintainers
> Last updated: 2026-08-17

**Objective:** 让 `opsctl` 在桌面端不运行时也能完成全部资产管理与操作 —— 由交互式终端承担审批人角色，并把人在终端上做出的授权落成跨进程可见的规则；外部 AI（Claude Code / Codex 等）通过 opsctl 操作资产时，被策略拦下的操作得到一条明确的、只有人能执行的授权指引，而不是一句"桌面端没运行"。

**Hard invariant:** 一条授权规则只能由坐在交互式终端前的人让它生效。非交互调用（AI 的工具调用、CI、shell 管道）既不能创建规则，也不能绕过规则，全程没有任何免审批开关。既有权限判定语义不得放宽：`ApprovalKind` 与 `ParseApprovalResponse` 的决策白名单、grant 的 `cp` 与非 `cp` 面隔离、`NormalizeGrantPatterns` 的来源区分，全部照旧生效。

## Problem

1. **审批人只能是桌面 GUI，这是 opsctl 独立使用的唯一硬阻塞。** `requireApproval` 在策略判定为 `NeedConfirm` 后无条件去连 `approval.sock`（`cmd/opsctl/command/approval.go:89`）。连不上时分两种结局：`exec` / `sql` / `redis` / `mongo` 拒绝并附上可用规则提示（同文件 `:95`），`cp` / `create` / `update` / `delete` 直接抛 `desktop app is not running -- write operations require approval from the running desktop app`（同文件 `:103`）；`grant` 同样硬失败（`cmd/opsctl/command/grant.go:231`）。面向 AI 的技能文档整篇以此为前提：`plugin/opsctl/skills/opsctl/SKILL.md:85` 写 "Most write operations require desktop app approval"，`:90` 写 create/update「always need the desktop app」、delete「always needs desktop app too, and cannot be pre-approved」。

2. **grant 授权按终端窗口分作用域，桌面端批准的授权在另一个终端里不命中。** grant 匹配严格以 sessionID 为作用域：`matchGrantPatternsWith` 先取 `aictx.GetSessionID(ctx)`，空则直接返回未匹配（`internal/ai/permission/checker.go:158`），再 `ListApprovedItems(ctx, sessionID)`（同文件 `:166`）。而 opsctl 的 sessionID 由终端环境变量派生哈希（`OPSKAT_SESSION_ID` / `TERM_SESSION_ID` / `ITERM_SESSION_ID` / `WT_SESSION` / `WINDOWID`，都没有则退化成字面量 `"default"`，`cmd/opsctl/command/session.go:22`-`:39`）。于是同一台机器上，桌面端"记住并允许"批下的 grant 只对当初那个终端 scope 的后续调用生效；AI 子进程若落在不同 scope（不同终端窗口、不同 CWD、或环境里根本没有这些变量）就不会命中。本轮不改桌面端审批路径，这条缺陷因此照旧存在，需要连同 Problem 3 一起在 session 层解决。

3. **项目级 `.opskat/` 目录把机器级概念放进了项目目录，且读写路径本身不一致。** 全仓库对它的唯一用途就是这个 session 文件（`git grep '\.opskat'` 的其余命中都是 `~/.opskat` AI 工作目录、`*.opskat.lock` 远端 SQLite 锁、`.opskat-part-*` SFTP 临时文件等无关形态）。`readActiveSession` 从 CWD 逐级向上查找已存在的 `.opskat/`（`cmd/opsctl/command/session.go:49`），但 `writeActiveSession` 无条件写 CWD 相对路径 `.opskat/sessions/`（同文件 `:101`）：在子目录里自动创建的 session 会静默遮蔽父目录里那一个，同一终端在不同子目录下运行会拿到两个不同 session。`cleanupSessionsDir` 只删 `sessions/` 子目录、从不删 `.opskat/` 本身（同文件 `:169`），空目录永久留在用户仓库里，且不在任何 `.gitignore` 中。

4. **`OPSKAT_SESSION_ID` 的文档描述是假的。** `cmd/opsctl/command/session.go:25` 的注释与 `:209` 的使用说明都写着该变量由 "desktop app injects this"。全仓库没有任何 Go 或 TypeScript 代码写入这个变量，只有 opsctl 读取它。

5. **`opsctl` 只有 0 与 1 两个退出码，调用方无法把"需要人授权"与"命令写错了"区分开。** `cmd/opsctl/main.go:10` 直接 `os.Exit(command.Execute())`，各 `cmd*` 函数只返回 0 或 1。现有的机器可读约定是 stderr 文本标记 —— `SKILL.md:153` 要求 AI 在输出含 `USER DENIED` 时立即停止。

6. **`opsctl update asset` 的审批主体不含任何变更内容，审批人只能闭眼批。** `Detail` 只有 `fmt.Sprintf("opsctl update asset %s", args[1])`（`cmd/opsctl/command/create.go:279`），而紧邻上方刚构造好的 `params`（name / host / port / username / description / group_id / icon，同文件 `:249`-`:277`）没有一项进入审批请求。`Detail` 又恰恰是审批人看到的全部 —— `cmd/opsctl/command/delete.go:93`-`:94` 的注释把这条不变式写死了："Detail is the only field the desktop OpsctlApprovalDialog renders for this request"。同类的 `create` 有实质主体（`asset_put_svc.Prepared.SafeApprovalDetail()` 给出 `{name, type, config}`，`internal/service/asset_put_svc/asset_put.go:182`），`delete` 的资产名即完整描述；`update` 是三者里唯一漏掉实质的。

7. **审计日志没有任何 opsctl 读取入口。** `opsctl list` 只认 `assets` / `groups` / `credentials` 三种资源（`cmd/opsctl/command/list.go:23`、`:39`、`:42`），`get` 只认 `asset` / `credential`（同文件 `:70`、`:79`）。"哪条规则是谁什么时候加的"、"上一条被拒的操作是什么"只能开桌面端或直接 `sqlite3` 查 `audit_logs`。把审批权交给终端却读不到审计，等于审计只写不读。

8. **deny 规则无条件盖过 allow 规则，因此一条新写的 allow 可能自始不生效。** `checkCommandPolicyPermission` 先遍历整条 holder 链收集来的 deny 规则并在命中时直接返回 `Deny`（`internal/ai/permission/permission.go:69`-`:81`），只有全部 deny 都未命中才轮到 allow 列表（同文件 `:84`）。各类型的默认策略都引用了内置危险拒绝组（`internal/model/entity/policy/policy.go:18` 等），所以"人写了一条 allow、却被一条他看不见的内置 deny 遮蔽"是个可达状态 —— 而人得到的反馈只是写入成功。

9. **`opsctl grant submit` 是"人预先批量授权"的现有入口，但它落成的是按 session 作用域的 24 小时 grant，且必须有桌面端在场。** 它支持一资产多 pattern、多资产、以及 `--group <group>` 的组级定向（`cmd/opsctl/command/grant.go:278`-`:300`），能力比逐条授权强；但离线时硬失败（同文件 `:231`），且它产出的授权继承 Problem 2 的作用域缺陷。

## Actors and user stories

1. 作为**运维工程师**，我想在桌面端没运行时也能用 opsctl 完成资产管理与操作，以便不必为了点一个确认弹窗而启动 GUI。
2. 作为**调用 opsctl 的外部 AI 编码 agent**（Claude Code、Codex 等，经 `plugin/opsctl/skills/opsctl/SKILL.md` 学会这套命令），我想在操作被策略拦下时拿到一条明确的、可原样转述给人的授权指引，以便既不误判成任务失败，也不去猜"桌面端没运行"该如何绕过。
3. 作为**要为这批服务器的安全兜底的人**，我想确保只有坐在交互式终端前的人能让一条授权规则生效，以便自动化调用方无法给自己扩权。
4. 作为**长期自动化的维护者**，我想把流水线需要的操作一次性落成永久规则并能随时查看与撤销，以便无人值守的调用不必依赖任何临场审批。

## Design decisions

| # | 决策 | 依据与被否方案 |
|---|---|---|
| 1 | 可交互判据 = `stdin` 是 TTY **且** `stderr` 是 TTY | 两类误判的代价不对称：误判为"可交互"会让 AI 的工具调用永久挂死在一个没人会敲的输入上；误判为"非交互"只让人多收到一条清晰的授权指引。判据必须朝保守方向偏。**被否**：探测 `/dev/tty` 可打开性 —— Claude Code 一类 harness 的 Bash 子进程通常仍继承控制终端，该判据会稳定踩中挂死这个失败模式。 |
| 2 | 有 TTY 时优先在终端审批，不联系桌面端 | Issue 要求"不再经过桌面端"；人在终端里发起的操作应在终端里得到答复，无需切窗口。**被否**：桌面优先、TTY 兵者 —— 行为回归面为零，但把在终端里工作的人赶去点 GUI，与本 issue 的意图相反。 |
| 3 | 无 TTY 时先试 `approval.sock`，不可达才结构化拒绝 | 桌面端在运行时它是更强的审批面（可编辑 pattern、可队列合并），没有理由主动放弃。 |
| 4 | 不提供任何免审批开关（`--yes` 与同类环境变量一律不做） | 与 hard invariant 自洽：唯一通路是人先在 TTY 上建好规则。任何旁路都会被调用方自己加到命令行里，等于取消门禁。**被否**：限定作用域的 `--yes` —— 作用域限制无法阻止调用方自行加上该 flag。 |
| 5 | 授权只有一档：永久规则。不做 24 小时临时授权，因此 `allow` 上也没有 `--permanent` flag | 两档等于每次弹提示都逼人多做一次选择，而临时档换来的唯一好处（自动过期）在单人桌面工具上换不回成本 —— 它真正制造的是"我明明授权过，第二天怎么又拦我"这类失效模式。代价是每次"记住"都长期有效、权限会随时间累积；由 `policy show` 的可见性、`policy rm` 的可撤销性，以及决策 12 的写入前回显来兜。**被否**：默认临时、`--permanent` 升级为永久 —— 即上述两档方案。 |
| 6 | 规则命令收在 `opsctl policy <show\|allow\|deny\|rm>` 一个家族下 | 本轮同时提供 allow 与 deny，单独的 `opsctl allow` / `opsctl deny` 两个平级顶层动词撑不成一个可发现的家族。`docs/ARCHITECTURE.md` §5 警告的 "command policy 是三个不同的东西"是给读代码的人的，而 CLI 操作的恰好是三者中最明确的那一个 —— 存在 `command_policy` 列里的规则本身。**被否**：`opsctl allow` 加 `opsctl deny` 两个顶层动词 —— 二者共享同一份按类型落点机制与同一套 TTY 门禁，拆成平级动词会让"查看现状"无处安放。 |
| 7 | "哪些操作可以被记住"沿用现有 `ApprovalKind` 不变式，不新造规则 | `ParseApprovalResponse` 已把 `allowAll` 限定在 `ApprovalKindSingle` 与 `ApprovalKindLocalTool`（`internal/ai/permission/approval.go:72`），而 `create` / `update` / `delete` / `ext_tool` 都没有注册进 `registerPermissionType`（`internal/ai/permission/type_registry.go:76`-`:90`），经 `singleApprovalKind`（`internal/app/opsctl/approval.go:148`）落到 `once` / `delete` / `extension`。终端审批人只需遵守这张既有的表。 |
| 8 | 终端提示不支持编辑 pattern | 在裸终端里实现行编辑器的成本与出错面都不划算；"看到即所得"比"可编辑"更难误授。想要别的范围就拒绝，改用 `opsctl policy allow` 自己写 —— 那条路径本来就存在。**被否**：移植桌面弹窗的可编辑 pattern 能力。 |
| 9 | session 迁入 data dir 并降为单例，`.opskat/` 整体删除 | data dir 已经是 `opskat.db` / `master.key` / `config.json` / `approval.sock` / `sshpool.sock` 的家；session 的语义正是"共享同一份 DB 与 grant 的那批调用"，与 CWD 无关。顺带消灭 Problem 3 的读写路径不一致与空目录残留。**被否**：只修 `writeActiveSession` 的路径不一致而保留 `.opskat/` —— 修完仍然把机器级概念放在项目目录里，且解决不了 Problem 2 的跨终端问题。 |
| 10 | 不为旧 `.opskat/sessions/<scope>` 文件留兼容读取 | `AGENTS.md`「不为退役数据留运行时 shim」。最坏后果是一次多余的重新审批。 |
| 11 | 永久规则（allow 与 deny 两侧）的按类型落点通过注册表扩展，不在共享代码里 `switch assetType` | `AGENTS.md` 的 OCP 规则明文禁止在共享代码里按类型串分支。落点函数与 `registerPermissionType` 的 `grantPatterns` 并列注册，一个类型注册一次即同时覆盖两侧 —— allow 与 deny 在每种策略形状里是成对的字段（`AllowList`/`DenyList`、`AllowTypes`/`DenyTypes`），拆成两套注册只会让某天新增类型时漏掉一半。 |
| 12 | 永久规则写入前一律回显将要写入的规则原文并二次确认；当结果比请求的主体更宽时明确标注 | `database` 与 `mongodb` 的资产级策略只表达语句/操作**类型**（`QueryPolicy.AllowTypes` / `MongoPolicy.AllowTypes`），永久化一条 `SELECT * FROM users` 只能落成"允许 SELECT 类型"，比人请求的范围宽得多，静默放宽不可接受。**被否**：对这两类直接拒绝永久 allow —— 决策 5 取消临时档后，这等于把 database 与 mongodb 彻底关在规则管理之外，每次操作都必须有人在终端上临场批准。 |
| 13 | 终端提示里的持久化选项直接写永久规则，不走 `allowAll` → grant 那条路；是否提供该选项仍沿用决策 7 的同一张表 | 决策 5 取消临时档后，grant 层在 opsctl 的终端路径上不再产生新行。让终端的 `allowAll` 改指"写永久规则"会使同一个决策值在两个审批人处耐久度不同 —— 与其在共享值上分叉，不如让终端提示只产出 `allow` / `deny`，把"允许并永久放行"作为 opsctl 本地动作、与 `policy allow` 同一条写入路径。**由此接受一处不对称**：桌面弹窗的"记住"仍写 24 小时 grant（桌面路径本轮不动），终端的"永久允许"写永久规则；两者标签不同，不会误导人。 |
| 14 | `policy show` 只读，不需要 TTY；只有 `allow` / `deny` / `rm` 需要 TTY | 读取现状不构成扩权，而且让 AI 能查清自己被哪条规则挡住只会让它写出更准的命令、少一轮往返。把只读能力也关进 TTY 会逼 AI 去猜，与结构化拒绝里附带规则提示的初衷相反。 |
| 15 | 删除 `opsctl grant submit`；它的能力（多 pattern、多资产、组级定向）并入 `policy allow` / `policy deny`，因此**资产组链级策略的写入随之进入本轮** | 决策 5 取消临时档后，`grant submit` 就是那一档唯一残留的入口，留着它等于给被否掉的方案开后门。并入不是纯搬迁：它的组级定向写的是 `grant_items.GroupID`（grant 层的定向），而永久层要表达"这条规则对整个组生效"只能写 `groups.command_policy` —— 若不把组链策略一起做，取消临时档就是一次能力倒退。代价很小：`group_entity.Group` 同样实现 `policyent.Holder`、同样有 `GetCommandPolicy` / `SetCommandPolicy` 一对，决策 11 的注册落点函数按 holder 复用即可。**被否**：删掉 `grant submit` 且不做组链策略 —— 对资产多的人是实打实的倒退；保留 `grant submit` 原样 —— 与决策 5 公然矛盾，且还要额外定义 `ApprovalKindGrant` 在终端上的提示形态。 |
| 16 | 审计只读入口挂在既有 `list` 动词下（`opsctl list audit`），不新开顶层动词 | `list` 已经是"列资源"的入口（assets / groups / credentials），审计行就是又一种资源。新开一个顶层动词只为一条只读查询，会让命令表变长而不增加表达力。 |
| 17 | `create` / `update` / `delete` 在不可交互且无桌面端时归入 `NEEDS TTY`（"请人自己在终端里执行"），不归入 `NEEDS AUTHORIZATION`；本轮**不**为它们引入操作级规则 | 这三个操作不携带命令主体，策略检查被 `if req.AssetID > 0 && req.Command != ""` 挡在门外（`cmd/opsctl/command/approval.go:52`），所以任何规则都不可能预授权它们 —— 给出 `policy allow` 建议就是给一条照抄了也没用的命令。已知限制：`/opsctl:init` 的批量模式靠 `opsctl update asset`（`plugin/opsctl/commands/init.md:90`），因此无人值守下不可用。**被否**：复用 `cp:read:` / `cp:write:` 那种前缀 face 机制加 `asset:update` 之类的操作级规则（不必改表，机制已有先例）—— 但要先定义主体粒度（整个资产还是具体字段），还要推翻 `ApprovalKindDelete` 现有的"永不可预授权、连 session 也不行"不变式。这是一轮独立的策略语义设计，不该塞进审批人这一轮。 |
| 18 | 修 `opsctl update asset` 的审批主体：`Detail` 必须带上本次变更的字段，而不只是命令行 | 今天 `Detail` 只有 `fmt.Sprintf("opsctl update asset %s", ...)`（`cmd/opsctl/command/create.go:279`），紧邻它构造好的 `params`（name / host / port / username / description / group_id / icon）一个都没带进去；而 `Detail` 就是审批人看到的全部（`delete.go:93`-`:94`）。于是无论桌面弹窗还是本轮新增的终端提示，批 `update` 都是闭眼批 —— 而知情同意正是这个提示存在的全部理由，所以修生产者是本轮的必要组成，不是搭车重构。安全上免费：`update asset` 的 flag 集里没有任何密码或密钥项（`create.go:235`-`:243`），因此无需 `create` 那套去密处理；若将来给它加了带密字段，必须同时改走 `SafeApprovalDetail` 那条路。**被否**：只在终端提示里本地拼出变更摘要 —— 桌面弹窗照旧闭眼批，等于同一个缺陷留一半，且在消费者侧补生产者的漏。 |
| 19 | 写入 allow 规则前先判它会不会被生效中的 deny 规则遮蔽；会，就**拒绝写入**并点名那条 deny 及其来源，同时说明需要改什么。`policy show` 对已被遮蔽的 allow 规则同样标注 | deny 无条件先判（Problem 8），所以一条被遮蔽的 allow 是**自始无效**的，写下去只会在规则列表里显示一条人其实没拿到的授权 —— 与决策 12 要防的是同一件事、只是换了种失败模式，也与"归一化结果为空就什么都不落"同一条原则。选择拒绝而不是仅警告：写一条永不触发的规则从来不是人的本意，而静默接受会让人以为已经授权完毕、把问题推到下一次执行失败时才暴露。**必须同时给出出路的命令原文**：遮蔽方在资产自身或组链上时用 `policy rm` 撤掉；在权限组里时走决策 21 的 copy → 改副本 → detach / attach 路径。**被否**：照写并只打一行警告 —— 人在脚本里看不到警告，且规则列表从此含有一条骗人的条目。 |
| 20 | **给人读的终端文本跟随系统语言；机器可读的部分恒定英文 ASCII。** 前者含审批提示的标签与选项、`policy` 家族输出与报错、`list audit` 表头；后者含 `NEEDS AUTHORIZATION` / `NEEDS TTY` 两个标记、退出码、以及供人照抄的命令行 | 两类文本的读者不同，不能一起本地化。标记会被 AI 按字面匹配（`SKILL.md:153` 今天就是这么认 `USER DENIED` 的），一旦随 locale 变动，调用方的判定逻辑就在中文环境下静默失效；供照抄的命令行同理，必须逐字可粘贴。提示正文相反 —— 它的读者是坐在终端前的人，本地化才是对的。**被否**：全部钉死英文 —— 让中文用户读英文审批提示，而这条提示是他做安全决策的唯一依据；全部跟随 locale —— 会连标记一起本地化，等于让 AI 侧的错误处理在中文环境里失效。 |
| 21 | 权限组的增删改与 attach / detach 进本轮，收在 `policy group` 与 `policy attach` / `detach` 之下 | 它是"完全不需要桌面端"最后一块缺的拼图 —— 没有它，决策 19 挡下一条被内置 deny 遮蔽的 allow 之后，人没有任何 CLI 出路。service 层能力已经齐全（`policy_group_svc` 的 `List` / `Get` / `Create` / `Update` / `Delete` / `Copy`），按分层规则 CLI 只是薄命令壳；attach / detach 写的是 holder 策略里的 `Groups` 字段，与 allow / deny 共用决策 11 的落点机制。三种组 ID 形态的可写性不由本轮定义，照搬服务层既有不变式（内置与扩展组只读）。**被否**：留到下一轮 —— 那会让本轮交付一个"授权可能被挡住且无解"的状态，而这个状态恰好由本轮新增的决策 19 制造出来。 |
| 22 | 语言从标准 locale 环境变量解析（`LC_ALL` → `LC_MESSAGES` → `LANG`），取语言前缀映射到 `zh-cn` / `en`，无法识别时落 `en`；不新增 `--lang` flag | 策略消息层本来就是双语二选一（`policy.PolicyMsg(ctx, en, zh)`，`internal/ai/policy/policy_i18n.go:21`）且由 `aictx.WithPolicyLang` 驱动，所以要改的只是**语言的来源**：把 `cmd/opsctl/command/root.go:83` 那句硬编码的 `aictx.WithPolicyLang(ctx, "en")` 换成解析结果。标准 env 本身就是覆盖手段 —— CI 想要确定性输出直接 `LC_ALL=C`（落 `en`），无需再造一个 flag。 |

## Interactivity criterion

任一需要审批的操作即将进入 `NeedConfirm` 分支时，opsctl 判定本次调用是否可交互：仅当 `stdin` 与 `stderr` 双双是终端时判为可交互。

被这条判据归入"非交互"的合法人工用法需要事先授权，而非临场审批 —— 最典型的是 `cat config.yml | opsctl exec web-01 --type ssh -- tee /etc/app/config.yml`（stdin 是管道）。这是决策 1 有意付出的代价，必须写进 CLI 使用说明与技能文档，让人知道该先跑 `opsctl policy allow`。

判据不受任何命令行开关影响：既不提供强制交互的开关（无 TTY 时读 stdin 只会立刻 EOF，等同拒绝），也不提供强制非交互的开关（非交互本来就是默认的降级方向）。

## Approver selection and terminal approval

策略与既有授权判定为 `NeedConfirm` 时（`Allow` 与 `Deny` 两种结局的行为完全不变），按下表选择审批人：

| 情形 | 审批人 |
|---|---|
| 可交互 | 终端提示，不联系桌面端 |
| 不可交互，`approval.sock` 可达 | 桌面弹窗，保持现状（含 stale socket 判定：拨号失败即视为不可达） |
| 不可交互，`approval.sock` 不可达 | 结构化拒绝 |

**语言（决策 20、22）**：给人读的文本 —— 审批提示的字段标签与选项、`policy` 家族的输出与报错、`list audit` 的表头 —— 跟随系统 locale（`LC_ALL` → `LC_MESSAGES` → `LANG`，取语言前缀，无法识别落 `en`，见决策 22）。机器可读的部分恒定英文 ASCII：`NEEDS AUTHORIZATION` / `NEEDS TTY` 两个标记、退出码、以及供人照抄的命令行。

有一处实现上的坑要点明：服务层的错误串是硬编码中文（如 `policy_group_svc` 的"内置权限组不可删除"、"无效的权限组 ID"，`internal/service/policy_group_svc/policy_group.go:124`、`:131`），直接透传会让英文环境下的输出中英混杂。CLI 因此要**自己判定可预见的失败条件并产出本地化消息**（目标 ID 的形态用已导出的 `policy_group_entity.IsBuiltinID` / `IsExtensionID` 就能判），把服务层错误留作兜底而非展示文案。同理 `Copy` 在未指定名字时会拼一个硬编码的中文后缀"（副本）"（同文件 `:160`），CLI 必须显式传名字，不依赖这个默认值。

终端提示的文本写 `stderr`、决策从 `stdin` 读入 —— `stdout` 只承载命令自身的输出，`opsctl exec ... > file` 一类重定向不被污染。提示至少展示：资产名与 ID、资产类型、经 `NormalizeGrantPatterns` 归一化**之后**的主体（人必须看到自己真正授出的范围，而不是自己敲的原串）、以及审批项自带的 detail（`cp` 用它展示两端基点）。

可选项由 `ApprovalKind` 决定（决策 7）：

- `single`（ssh / serial / database / redis / etcd / mongodb / kafka / k8s / oss / cp）：本次允许（allow once）、永久允许（allow always — write a rule）、拒绝（deny）。
- `once` / `batch` / `delete` / `extension`（含 `create` / `update` / `delete` 与批量执行）：本次允许（allow once）、拒绝（deny）。

"永久允许"与 `opsctl policy allow` 共用同一条写入路径（决策 13）：先写规则、写成功才放行本次操作。它不产生 grant 行。

`opsctl batch` 与 `opsctl cp` 的多端点审批在终端上一次性列出全部条目，整批允许或整批拒绝 —— 与 `ApprovalKindBatch` 的现有语义一致，不引入逐条选择。

**`create` / `update` / `delete` 的提示内容。** 这三个没有命令主体（决策 17），落在 `once` / `delete` 两个 kind 上，因此只有"本次允许 / 拒绝"两个选项。它们的可审信息全部来自 `ApprovalRequest.Detail` —— 那**就是**审批人看到的全部（`cmd/opsctl/command/delete.go:93`-`:94` 的注释已经把这条不变式写死在桌面弹窗上）。终端提示必须展示：操作动词、目标资产名与 ID 与类型、以及这次操作的实质：

- `create`：来自 `asset_put_svc.Prepared.SafeApprovalDetail()`（`internal/service/asset_put_svc/asset_put.go:182`）的 `{name, type, config}`，config 已经是去密后的 `approvalConfig`。
- `delete`：目标资产名即完整描述。
- `update`：**必须补上本次变更的字段**（决策 18）。

空输入（直接回车）、EOF、`SIGINT` 一律判为拒绝，命令不执行，并照常写审计。非白名单输入重新提示，不静默当成允许。

选择永久允许时，先回显将要写入的规则原文；当该规则比本次主体更宽时（决策 12），明确标注这一点（"this rule is broader than the operation you approved" 一类），人确认后才写入。

## Structured refusal contract

不可交互且桌面端不可达时，opsctl 拒绝执行并输出机器可读的指引。退出码统一为 `3`（新增，语义是"停下来找人"；`1` 继续表示一般错误，`0` 表示成功），`stderr` 首行是两个固定标记之一 —— **分支依据是"这次操作有没有一个规则能匹配的主体"**（决策 17）：

**`NEEDS AUTHORIZATION` —— 有主体，规则能救。** 适用于 `exec` / `cp` / `batch` 等带命令或路径主体的操作。随后给出：资产名 / ID / 类型、被拒的归一化主体、当前生效的 allow 规则提示（复用既有 `HintRules`，与 `formatOfflineDenyMessage` 今天提供的信息同源），以及**人应当照抄执行的 `opsctl policy allow` 命令原文**，已按 shell 语法转义、可直接粘贴。人授权后由调用方重试。

**`NEEDS TTY` —— 只能由人在终端里做。** 两种情形共用这个标记，因为对调用方来说结论相同：

1. 写规则的命令本身（`policy allow` / `deny` / `rm`）—— 防自动化调用给自己扩权。
2. `create` / `update` / `delete` —— 它们不携带命令主体，`requireApproval` 的策略检查被 `if req.AssetID > 0 && req.Command != ""` 挡在门外（`cmd/opsctl/command/approval.go:52`；`create` 连 `AssetID` 都不传，见 `create.go:151`，`update` 只传 ID，见 `:279`），因此**没有任何规则能预授权它们**。正文必须说明这一点，并给出人应当**自行执行**的那条命令原文 —— 不给 `policy allow` 建议，那条建议对它们永远无效。

`SKILL.md` 的错误处理段增加两条指引，区别在于人处理完之后 AI 该做什么：

- 含 `NEEDS AUTHORIZATION`：立即停止，把 `opsctl policy allow` 那一行原样转述给人，等人授权后**重试原命令**；不要自己去执行那行授权命令 —— 执行也会以 `NEEDS TTY` 失败。
- 含 `NEEDS TTY`：立即停止，把**原命令**转述给人、请他在自己的终端里执行。**不要重试** —— 人执行完操作就已经完成了，重试等于做第二次（`create` 会建出重复资产）。改用 `get asset` 一类只读命令确认结果。

## Rule management: `opsctl policy`

规则命令收在一个家族下（决策 6）：

```
opsctl policy show  <asset> | --group <group>
opsctl policy allow <asset>... | --group <group>...  [--type <asset-type>] -- <pattern>...
opsctl policy deny  <asset>... | --group <group>...  [--type <asset-type>] -- <pattern>...
opsctl policy rm    <asset>  | --group <group>  <id>
```

目标可以是一个或多个资产，也可以用 `--group` 指定一个或多个资产组；一次调用可以给出多条 pattern。这套目标与多值形态承接自被删除的 `grant submit`（决策 15），因此"给一个组预授权一批 pattern"这件事没有能力倒退。资产目标写资产自身的策略列，组目标写该组的策略列（`groups.command_policy`），两者共用决策 11 的同一个按类型落点函数 —— 它按 holder 取 `GetXxxPolicy` / `SetXxxPolicy` 对，与目标是资产还是组无关。

**`show` 只读，任何调用方都能跑**（决策 14）：给资产时展示该资产合并后生效的 allow / deny / 引用的权限组（含内置组与扩展组），以及仍然有效的 grant 授权及其剩余时间；给 `--group` 时展示该组自身那一列的规则，用来核对刚写下的组级规则。资产视图的合并语义与判定路径同源 —— 展示的就是 `policyHoldersForAsset` 沿资产、组链、权限组三层收集后的结果（`internal/ai/permission/checker.go:358`），不是资产自身那一列的原文；每条规则都标出它来自哪一层。

**`allow` / `deny` / `rm` 只在交互式终端中运行**，非交互调用以退出码 `3` 与标记 `NEEDS TTY` 拒绝。这是"AI 不能给自己扩权"的唯一执行点。

- `allow` 与 `deny` 都只写永久规则，没有时效档、没有 `--permanent` flag（决策 5）。
- 永久规则写入目标的策略列：资产目标写 `assets.command_policy`，组目标写 `groups.command_policy`。两处都是单列按类型解释成不同形状（`GetCommandPolicy` / `GetQueryPolicy` / `GetRedisPolicy` 等），allow 与 deny 两侧的字段名随形状而变（`AllowList` / `DenyList` 对 ssh、`AllowTypes` / `DenyTypes` 对 database 与 mongodb，等等）。落点函数与 `registerPermissionType` 并列注册，一个类型注册一次、同时覆盖 allow 与 deny 两侧、且对两种 holder 通用（决策 11、15）；写入走一次事务内的读-改-写，避免并发覆盖。
- **`--type` 在两种目标上含义不同，组目标上它是必填的。** 资产目标下它沿用 opsctl 全局的既有语义 —— 一个前置的类型断言，用来在权限检查与执行之前抓出目标/类型不符；规则形状由资产自身的类型决定，所以可以省略。组目标下没有"该组的类型"这种东西（一个组可以装任意类型的资产），`--type` 因此是**选定这条规则属于哪一种策略形状**的唯一依据，必须显式给出；缺失时在写入前失败并说明原因，绝不猜一个默认形状。
- `rm` 撤销 `show` 中列出的可撤条目：目标自身（资产或组）的永久 allow / deny 规则，以及 grant 授权 —— 后者本轮已不由终端路径产生，但桌面端审批仍在写，存量行也仍在，因此撤销入口必须覆盖它们。**引用来的权限组规则不由 `rm` 撤销** —— 它们属于权限组本身，不属于这个 holder。`show` 要把这类条目标注出来，并指向正确的两条出路：`policy detach` 把整个组从目标上摘掉，或 `policy group deny` / `rm`（用户组）与 `policy group copy` 后再改（内置组与扩展组）—— 见下节。
- pattern 归一化复用 `NormalizeGrantPatterns(assetType, pattern, GrantOriginUser)` —— 人手写的通配就是他要的范围，归一化不收窄它。
- 归一化结果为空时（OSS 的目录标记会出现这种情况）什么都不落并明确报错，不退回原串 —— 落一条匹配不上任何策略串的规则，只会在规则列表里显示一条人实际没拿到的授权。
- **allow 规则被生效中的 deny 遮蔽时拒绝写入**（决策 19）：deny 无条件先判（`internal/ai/permission/permission.go:69`-`:81`），被遮蔽的 allow 自始无效。报错要点名那条 deny 规则原文、它来自哪一层（资产 / 组链上的哪个组 / 哪个权限组，含内置组的名字），并说明出路 —— 若遮蔽方在权限组里，本轮没有摘除或收窄它的 CLI 入口，需要去桌面端处理。`policy show` 对已存在但被遮蔽的 allow 规则同样标注，否则它会显示一条其实不起作用的授权。
- 永久写入前回显将要写入的规则原文并二次确认；结果比请求主体更宽时明确标注（决策 12）。

写入失败时报错，并且不执行触发本次授权的命令 —— 不能让人以为授权已经生效。目标不存在、类型断言与资产实际类型不符、pattern 为空，都在写入前失败并说明原因。多目标或多 pattern 时任一条写入失败即整体失败，不留下写了一半的规则集。

### 权限组与挂载

规则的第三种 holder 也进本轮（决策 21）：

```
opsctl policy group list [--type <policy-type>]
opsctl policy group show   <group-id>
opsctl policy group create --name <name> --type <policy-type>
opsctl policy group copy   <group-id> --name <name>
opsctl policy group allow  <group-id> -- <pattern>...
opsctl policy group deny   <group-id> -- <pattern>...
opsctl policy group rm     <group-id>
opsctl policy attach <asset>|--group <group>  <group-id>...
opsctl policy detach <asset>|--group <group>  <group-id>...
```

`list` 与 `show` 只读、不需要 TTY，与 `policy show` 同一档（决策 14）。其余全部要 TTY —— 挂载一个组等于一次性引入它全部规则，和写单条规则同权。

**三种组 ID 形态各自能做什么**，由服务层既有的不变式决定，CLI 只负责如实呈现：内置组（`builtin:xxx`）与扩展组都**不可改、不可删**（`Delete` 在 `internal/service/policy_group_svc/policy_group.go:123`、`:126` 直接拒绝；`Update` 要求 `ID > 0`，同文件 `:112`，字符串 ID 天然进不去），只能 `list` / `show` / `copy` / attach / detach。用户组（数字 ID）可改可删。

**这条路径正是决策 19 那个缺口的解法**，本轮因此闭合：一条内置 deny 遮蔽了你要的 allow 时 —— `policy group copy <builtin-id> --name <新名>` 分叉出可编辑的用户组（`Copy` 支持内置、扩展、用户三种来源，并剥掉嵌套的 `groups` 字段，同文件 `:136`、`:179`），在副本上删掉那条 deny，然后 `detach` 内置组、`attach` 副本。全程不需要桌面端。`policy allow` 被遮蔽时的报错要顺带给出这条出路的命令原文。

attach / detach 改的是 holder 策略里的 `Groups []string` 字段（`internal/model/entity/policy/policy.go:7` 的 `Groups`），与 allow / deny 写同一列、同一套按类型落点函数（决策 11），因此同样支持资产与资产组两种目标。attach 一个 `PolicyType` 与目标资产类型不匹配的组要在写入前失败并说明原因，不能挂上去之后静默不起作用。

**`opsctl grant submit` 随本轮删除**（决策 15）。它的位置由 `policy allow` 接手：同样支持多 pattern、多资产与组级定向，区别只在落成的是永久规则而不是 24 小时 grant。删除后 opsctl 侧不再有任何产生 `ApprovalKindGrant` 的路径，因此终端提示不需要为该 kind 定义形态（AI 的 `request_permission` 走的是桌面端 `makeGrantRequestFunc`，本轮不动）。

## Session and `.opskat/` retirement

任一 opsctl 调用需要解析 grant 授权的作用域时，从 data dir 读取单一 current session：

- session 文件位于 data dir（与 `opskat.db` / `master.key` / `config.json` / `approval.sock` 同处），全机唯一，按文件修改时间 24 小时过期 —— 过期语义与今天一致。
- 项目级 `.opskat/` 目录不再被创建或读取；随之删除的还有终端环境变量派生 scope、CWD 向上查找、CWD 相对写入、空目录清理这四段逻辑。opsctl 从此不往用户的项目目录写任何东西。
- 旧的 `.opskat/sessions/<scope>` 文件不再被读取，也不做迁移（决策 10）。用户可自行删除遗留目录。

**安全取舍（必须明示）**：grant 授权的作用域从"单个终端窗口"扩大到"该 data dir 下的所有 opsctl 调用，含 AI 发起的调用"。24 小时内，任何能运行 opsctl 的本地进程都能用上桌面端批下的这批 grant。这是 Problem 2 唯一的修法 —— 桌面端在弹窗里批准时并不知道调用方将来落在哪个终端 scope，按 scope 隔离只会让它随机失效。收窄的手段是 `opsctl policy rm` 与更窄的 pattern，而非终端隔离。

决策 5 取消临时档后，终端审批路径不再产生新的 grant 行，因此这个扩大的作用域实际只影响桌面端审批批下的授权。永久规则本来就是全局的，不受此变更影响。

session 的对外入口一并退役：`--session <id>` 全局 flag、`OPSKAT_SESSION_ID` 环境变量、`opsctl session start|end|status` 子命令全部删除。依据：作用域变成单例后这三者再无可调之处；`SKILL.md:95` 本来就明文要求 AI "do NOT manually `session start`"；而 `OPSKAT_SESSION_ID` 的"由桌面端注入"文档从来就是假的（Problem 4）。session 降为纯内部概念 —— 使用者不需要知道它存在，它只继续充当 grant 作用域与审计关联 ID。

## Audit read entry

`opsctl list audit [--asset <asset>] [--limit <n>]` 是审计的只读入口（决策 16），不需要 TTY，任何调用方都能跑。它回答两个问题：一条规则是谁什么时候加的，以及上一批操作各自被谁按什么依据放行或拒绝 —— 每行至少给出时间、来源（`ai` / `opsctl` / `desktop`）、资产、工具、命令摘要与决策来源。默认按时间倒序，`--limit` 有默认值以免一次倒出全表。

它**原样呈现 `audit_logs` 里已存储的内容，不做二次改写、不解密、不补充任何字段**。审计行里哪些 write-only 字段本就不该存在，由各 producer 在写入侧的字段白名单决定（见 `docs/specs/2026-08-13-ai-secret-display-redaction.md`：允许读取的表面就返回原值，不生成占位近似值）。本轮不引入任何新的脱敏层 —— 仓库里原先那个统一值改写包 `internal/pkg/auditredact` 已随该轮一并删除，不要复活它。

## Security, privacy and audit

终端审批做出的每个决策照旧写 `audit_logs`：`source` 仍为 `opsctl`（不新增第四个来源值），决策来源沿用既有 `SourceUserAllow` / `SourceUserDeny`，`MatchedPattern` 记归一化后的主体。

结构化拒绝也必须落审计 —— 它是一次真实的拒绝决策，不是参数错误。每次永久规则写入单独记一行，使"这条规则是谁、什么时候、为什么加的"可回溯；终端提示里选"永久允许"与显式跑 `opsctl policy allow` / `deny` 共用同一条写入路径（决策 13），因此也共用这条审计行，不因入口不同而少记。

`DEVELOP.md` 的日志义务照旧适用于这条新路径：审批的开始、结束、失败三态都要有日志并带上可关联 ID；提示与决策文本中不得出现凭据明文。

## Compatibility and doc sync

两处需要在发布说明中点出的变更：

1. **行为回归面**：桌面端运行时，在终端里跑 opsctl 触发的审批不再弹 GUI 窗口，而是在终端里提问（决策 2）。
2. **输出语言变化**：opsctl 给人读的输出从此跟随系统 locale（决策 20、22），中文环境下审批提示与 `policy` 输出变为中文。两个结构化拒绝标记与供照抄的命令行仍是恒定英文 ASCII，脚本与 AI 侧的匹配逻辑不受影响；想要确定性英文输出用 `LC_ALL=C`。
3. **破坏性 CLI 变更**：`opsctl grant submit`、`opsctl session start|end|status`、`--session <id>`、`OPSKAT_SESSION_ID` 全部删除。前者的替代是 `opsctl policy allow`（决策 15），后三者无替代也不需要替代（session 降为内部概念）。已有依赖它们的脚本会以"未知命令"失败，而不是静默换语义 —— 这是刻意的：静默改语义会让一条脚本里写着"临时授权"的调用悄悄变成永久规则。

本轮的交付**横跨两个 git 仓库**：代码与随代码走的文档在本仓，用户站点文档在独立的 `opskat-docs` 仓库（`/Users/codfrm/Code/opskat/docs`，Docusaurus 3）。两边各出一个提交/PR，但同一份 spec 管辖 —— 站点文档滞后就是对外契约漂移。

### 本仓文档

必须同步，否则就是本仓库明令禁止的契约漂移（行号按 HEAD `9ed75ef5`）：

- `plugin/opsctl/skills/opsctl/SKILL.md` —— 审批机制段（`:83`-`:91`，其中 `:91` 的 "Pre-approve patterns: Use `grant submit`" 已失效）、Sessions 段（`:93`-`:97`）、并行执行段里依赖 `session start` 与桌面队列的两处预授权方案（`:99`-`:123`）、错误处理段（`:151`-`:156`，增加 `NEEDS AUTHORIZATION` 指引）、以及 Deploy 工作流示例里那条 `grant submit`（`:172`）。
- `plugin/opsctl/skills/opsctl/references/commands.md` —— `grant submit` 两种模式的完整章节（`:365`-`:411`）删除或改写为 `policy`；session 存储与解析优先级（`:390`-`:399`）；新增 `policy` 命令族（含 `group` 子族与 `attach` / `detach`）与 `list audit`，并标明 `policy show` / `group list` / `group show` / `list audit` 是 AI 可用的只读入口，写类子命令只在 TTY 上可用。
- `cmd/opsctl/command/root.go` 的使用说明 —— 审批与 session 段（`:189`-`:207`）、命令清单（`:173`-`:187`）、示例。
- `plugin/opsctl/commands/init.md` —— `/opsctl:init` 的批量模式靠 `opsctl update asset <id> --description`（`:90`、`:101`），按决策 17 它在无人值守下会以 `NEEDS TTY` 停下。需要写明这条限制与对应处理（把命令交给人执行，不要重试），否则该 slash command 在无桌面场景下会静默半途而废。
- `docs/ARCHITECTURE.md` §8 —— "当桌面端不运行时 opsctl 回落到 `pkg/client` 与本地策略/授权检查"这段描述需要补上终端审批人这条路径；同节列举 opsctl 操作命令的地方要去掉 `grant`。

### 站点仓库 `opskat-docs`

结构约定：`docs/` 是英文（默认 locale），`i18n/zh-CN/docusaurus-plugin-content-docs/current/` 是同名中文镜像 —— **下面每一项都要改中英两份**。中文不逐字翻译英文，各语言用各自地道的表达；只有语义分叉才算缺陷。

- **`docs/cli/grant.md` 整页作废** —— `grant submit` 的两种模式就是它的全部内容。换成新的 `docs/cli/policy.md`（`show` / `allow` / `deny` / `rm` / `group` 子族 / `attach` / `detach`）。连带：`sidebars.ts:40` 点名 `cli/grant`，必须同步改；`i18n/zh-CN/docusaurus-plugin-content-docs/current.json:14` 只翻译 "CLI Reference" 这个**分类**标签，每页的 `sidebar_label` 在各语言文件自己的 frontmatter 里，新页两份都要写。
- **`docs/cli/overview.md`** —— Sessions 一节整体失效：`:83` 写着 session 存 `.opskat/sessions/`、`:88` 的示例注释 "Uses the session from .opskat/sessions/"、`:99` 的三级解析优先级。另需去掉 `--session` 全局 flag、更新命令清单（加 `policy`、`list audit`，去掉 `session`、`grant`）、补上输出语言跟随 locale 这一条、并把"写操作需要桌面端审批"改成终端审批 / 结构化拒绝两条路径。
- **`docs/cli/exec.md`、`docs/cli/cp.md`、`docs/cli/batch.md`** —— 各自的 `--session` 说明删除；`exec.md` 还写着审批依赖桌面端。
- **`docs/guide/policy.md`** —— 目前只讲策略种类（command / query / redis / mongo / kafka / etcd / oss），**完全没有规则管理入口**。新增 `opsctl policy` 一节：资产级与组级 allow / deny、权限组的增删改与 attach / detach、内置组只能 copy 后再改、`--type` 在组目标上必填、只有 TTY 能写。这是站点上最该补的一页。
- **`docs/guide/audit.md`** —— 提到 session 与 `grant submit`，需按新模型改写；并新增 `opsctl list audit` 作为无 GUI 的审计查阅方式。
- **`docs/development/architecture.md`** —— 本仓 `ARCHITECTURE.md` §8 的站点镜像，同步同一处改动。

两处**不要动**：

- `docs/cli/ssh.md:26` 明写 `opsctl ssh` "Does **not** require desktop app approval (intended for human interactive use)" —— 与代码一致（`cmd/opsctl/command/ssh.go` / `sshproxy.go` 里没有任何 `requireApproval`）。它出现在"提到桌面审批"的搜索结果里，但它是对的，不要在这轮里顺手改错。
- `docs/changelog.md` 及其中文镜像 —— 历史发行记录描述的是过去版本的真实行为，不随本轮改写。新行为在下次发版时追加新条目。

## Out of scope

- **`ext exec` 的离线执行。** WASM 运行时在桌面端进程内，opsctl 今天为此 fail-closed（`cmd/opsctl/command/ext.go:149`）。终端审批解决不了执行器缺失，需要 opsctl 自带 WASM 运行时 —— 独立一轮。
- **opsctl 内置 AI**（在 opsctl 里跑 agent 循环、配置 AI Provider）。本 issue 的方向是 AI 调用 opsctl，不是 opsctl 调用 AI。
- **桌面端 UI 的任何改动**，包括审批弹窗。
- **内置组与扩展组自身的编辑或删除。** 不是本轮的取舍，而是服务层既有的不变式：`Delete` 对两者直接拒绝（`internal/service/policy_group_svc/policy_group.go:123`、`:126`），`Update` 要求 `ID > 0`（同文件 `:112`）。要改内置规则就 `policy group copy` 出一份用户组再改（决策 21），这是本来就设计好的路径。
- **临时（有时效的）授权的任何创建入口。** 决策 5 取消该档、决策 15 删掉它唯一的残留入口 `grant submit`。桌面端审批仍会写 24 小时 grant，opsctl 侧只负责查看（`policy show`）与撤销（`policy rm`）。
- **`create` / `update` / `delete` 的操作级规则**（决策 17）。这三个操作在无 TTY 且无桌面端时只能停下来交给人，规则救不了。已知影响：`/opsctl:init` 的批量模式在无人值守下不可用。要让它们可预授权，需要先定义操作主体的粒度并重新论证 `ApprovalKindDelete` 的"永不可预授权"不变式 —— 独立一轮的策略语义设计。
- **在终端提示里编辑 pattern**（决策 8）。

## Testing decisions

基线：HEAD `5facde9c` 上 `go test ./...` 全绿（exit code 0，0 个 FAIL）。本设计不修复任何既有失败。

| 缝 | 验证的行为 | 现有基础 |
|---|---|---|
| 可交互判据（注入两个 `isTerminal` 结果的纯函数） | 双 TTY 才算可交互；stdin 是管道或 stderr 被重定向都归为非交互 | 无 |
| 终端提示的解析（`kind` × 输入 → 决策） | 每种 `ApprovalKind` 只接受它该有的选项；空输入 / EOF / 非白名单输入的结局；"永久允许"只出现在 `single` 上，不出现在 `once` / `delete` / `extension` 上；提示产出的决策值里不含 `allowAll`（决策 13） | `internal/ai/permission/approval_response_test.go`（`ParseApprovalResponse` 侧） |
| 审批人选择顺序（注入可交互标志 + socket 拨号函数） | 上表三条路径各走一次，且可交互时不发生拨号 | `cmd/opsctl/command/exec.go:27` 与 `delete.go:17` 已有 `execApprovalFn` / `deleteApprovalFn` 包级变量注入缝 |
| 永久规则的按类型落点（table-driven，allow 与 deny 两侧 × 资产与组两种 holder） | 至少覆盖三种策略形状：`CommandPolicy.AllowList` / `DenyList`（ssh）、`QueryPolicy.AllowTypes` / `DenyTypes`（database）、`RedisPolicy.AllowList` / `DenyList`（redis）；同一落点函数施加到资产与组两种 holder 上结果一致；含"结果比请求更宽"的标注 | 无 |
| `opsctl policy` 各子命令的 TTY 门禁 | `allow` / `deny` / `rm` / `group create\|copy\|allow\|deny\|rm` / `attach` / `detach` 无 TTY 时以退出码 `3` + `NEEDS TTY` 拒绝且不落任何改动；`show` / `group list` / `group show` / `list audit` 无 TTY 时正常输出 | 无 |
| attach / detach 的前置判定（决策 21） | 组 `PolicyType` 与目标资产类型不匹配时在写入前失败；内置组与扩展组可 attach / detach / copy 但不可 create / edit / rm，且拒绝理由是 CLI 自己的本地化消息而非服务层的中文错误串 | `internal/service/policy_group_svc/policy_group_test.go`（已有 Create / Update / Delete / Copy 的服务层测试） |
| locale 解析（决策 22） | `LC_ALL` → `LC_MESSAGES` → `LANG` 的优先级；取语言前缀（`zh_CN.UTF-8` → `zh-cn`）；无法识别或 `C` / `POSIX` 落 `en` | 无 |
| 多目标 / 多 pattern 写入的全或无 | 任一条写入失败时整体失败，不留下写了一半的规则集 | 无 |
| `update asset` 的审批主体（决策 18） | `Detail` 含本次实际变更的每个字段；未通过 flag 指定的字段不出现 | `cmd/opsctl/command/create_test.go`（已有 `fakePreparedAssetCreate` 与 create 侧审批主体的测试） |
| allow 被 deny 遮蔽的检测（决策 19） | 被资产自身、组链、权限组三种来源的 deny 遮蔽时都拒绝写入且不落规则，报错点名遮蔽方与其来源层；未被遮蔽时正常写入 | `internal/ai/permission/permission_test.go`（已有 deny 优先于 allow 的判定测试） |
| 结构化拒绝消息与分支选择 | 退出码 `3`；有主体的操作（exec / cp / batch）走 `NEEDS AUTHORIZATION` 且含可照抄的 `opsctl policy allow` 命令行；`create` / `update` / `delete` 走 `NEEDS TTY` 且**不含** `policy allow` 建议（决策 17） | `cmd/opsctl/command/approval_test.go`（现有 `formatOfflineDenyMessage` 断言，扩展它） |
| session 解析与过期 | data dir 级单例；按 mtime 24 小时过期；旧 `.opskat/sessions/<scope>` 文件不再被读取 | `cmd/opsctl/command/datadir_test.go` 附近 |

`opsctl list audit` 的过滤与排序不单独立缝：它是一层薄查询，断言"我的 mock 返回了什么"没有价值。它的正确性由下面的手工验证覆盖 —— 真实跑一遍审批与规则写入，再用它读回来核对。

无法自动化的部分：终端提示的真实交互（需要 pty）与"人批准 → AI 重试通过"这条跨进程流程。由一次 `make dev-sandbox` 下的手工验证覆盖 —— 在 pty 中跑 TTY 审批路径，在管道中跑结构化拒绝路径，然后经 `opsctl list audit` 与 `node e2e/oracle.mjs` 双向回读 `audit_logs` 与 `assets.command_policy` / `groups.command_policy`，确认决策与规则真的落库、且 `list audit` 读出来的与库里一致。不使用 `make test-e2e`：该 harness 用 Playwright 驱动 GUI，而本轮的主题正是不需要 GUI。

## Relevant links

- Issue [#288](https://github.com/opskat/opskat/issues/288) — 独立 opsctl
- `docs/ARCHITECTURE.md` §8 — opsctl 与多进程流程
- `docs/VERIFICATION.md` — 验证顺序与沙箱
- `plugin/opsctl/skills/opsctl/SKILL.md` — 面向 AI 的 opsctl 技能文档
- `docs/specs/2026-08-13-ai-secret-display-redaction.md` — 审计与秘密数据边界（`list audit` 的不改写原则出处）
- `opskat-docs` 仓库（`/Users/codfrm/Code/opskat/docs`，Docusaurus 3）— 用户站点文档，本轮的第二个交付
