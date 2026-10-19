# opsctl 独立运行扩展

> Status: Approved
> Owner: OpsKat 维护者
> Last updated: 2026-10-19

**Objective:** opsctl 对扩展资产的 `exec` 与 `batch` 在 opsctl 自己的进程里加载扩展并执行，桌面端是否运行都一样可用；策略、审批、授权与审计走 opsctl 对内置类型的同一条流程，并且对扩展的动作与资源正确工作。

**Hard invariant:** 内置资产类型在 opsctl 中的执行、审批、授权与审计不回退；桌面端内（AI、扩展页面）对扩展的调用行为不变；扩展的安装、启停、商店流程不变；opsctl 的标准输出只含执行结果。

## 所处位置

issue #329（Oracle 数据库支持）路线第二步「扩展平台能力」里，**[用户决定]** opsctl 的扩展能力都不依赖桌面端，并先单独做这一轮。之后的「扩展提供数据库驱动」（规格 [2026-10-19-ext-database-driver](2026-10-19-ext-database-driver.md)）让 opsctl 执行扩展驱动的 SQL 时，建在本轮的 opsctl 扩展宿主上。

## Problem

标注：**[已核实]** = 对照 opskat main（`2b9abfb7`）源码确认；**[用户决定]** = 本轮对话中用户选定。

1. **桌面端没开，扩展资产就执行不了。** [已核实] opsctl 判定资产属于扩展后，把整条命令经审批套接字交给桌面端执行（`cmd/opsctl/command/exec.go:347-367`，`ext.go:220-243`）。桌面端不在时直接失败，报「the desktop app must be running」（`exec.go:357-361`）。
2. **opsctl 只注册扩展的描述，不注册执行面。** [已核实] opsctl 按桌面端缓存的 `describe()` 答案注册扩展资产类型（`internal/extreg/describe_only.go:102-168`），不注册策略检查、执行器、命令规范化与配置校验。本机从没被桌面端加载过的扩展连描述都没有，报错让用户先去桌面端加载一次（`describe_only.go:104`）。
3. **batch 遇到扩展资产直接报错。** [已核实] batch 的每一项先找执行器（`cmd/opsctl/command/batch.go:183`），扩展资产在 opsctl 里没有执行器。
4. **opsctl 的审批流程不认扩展的动作与资源。** [已核实] 扩展的分类、记住的模式与授权只在桌面端的确认路径里（`internal/ai/permission/checker.go:268-372`、`:390`）。opsctl 对内置类型的审批流程（`cmd/opsctl/command/approval.go:87-180`）直接用到扩展上时：
   - 规范化授权模式时，扩展类型原样返回整条命令（`internal/ai/permission/permission.go:806-816`）；
   - 终端里选「永久允许」，写出的规则不是动作，被扩展规则落库拒绝（`internal/ai/permission/rule_ext.go:170-193`），命令按拒绝处理；
   - 无人审批时 NEEDS AUTHORIZATION 给出的 `opsctl policy allow … -- '<整条命令>'` 无法生效；
   - 桌面端单条审批选「永久允许」，授权落库失败，只记一条警告（`permission.go:824-840`）。
5. **两个进程同时写扩展 KV 会超出配额。** [已核实] KV 写入先汇总已用大小、再查、再改或新建，不在事务里（`internal/repository/extension_data_repo/extension_data.go:50-80`）。桌面端与 opsctl 同时运行同一扩展后，两边并发写可能都通过配额检查而超出配额；（扩展, 键）上有唯一索引（`migrations/202603300002_extension_data.go:21`），两边同时新建同一个键时，后写的一方报唯一约束错误。

## Actors and user stories

1. 作为**运维用户**，我想在没开桌面端的服务器或脚本里，用 opsctl 对扩展资产执行命令，像 SSH、数据库一样。
2. 作为**运维用户**，我想在终端确认、桌面端审批或预先授权时，看到并授权的是扩展的动作与资源（如 `write:runbook/*`），「永久允许」真的生效。
3. 作为**运维用户**，我想在 batch 里把扩展资产与其他资产放在同一批执行、一次审批。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **[用户决定]** 扩展资产的 exec 一律在 opsctl 进程里执行；桌面端只作为审批人；桌面端「代 opsctl 执行扩展工具」的通道删除 | 与 SSH / 数据库一致：执行位置不随桌面端是否运行而变。opsctl 由桌面端写到 PATH，两者版本一致，不需要兼容旧 opsctl。Rejected: 桌面端在就委托、不在才本地执行——两条执行路径并存，审批形式与行为随环境变化 |
| 2 | **[用户决定]** 本轮单独做、先于扩展驱动 | opsctl 扩展宿主只建一次，扩展驱动在它上面做。Rejected: 并入扩展驱动一轮——那一轮过大；放到扩展驱动之后——opsctl 宿主要先为驱动建一遍再推广 |
| 3 | opsctl 只加载目标资产所属的扩展，用完即释放 | opsctl 是短命进程，按需加载避免每条命令编译全部扩展。扩展的编译缓存与桌面端共用，命中缓存时加载约 0.2~0.5 秒。Rejected: 启动时加载全部已启用扩展——与本次命令无关的扩展也要付编译成本 |
| 4 | 审批走 opsctl 对内置类型的同一条审批链（终端确认 → 桌面端审批 → NEEDS AUTHORIZATION），在链上补齐扩展的动作、资源与授权 | 用户只需要理解一套审批方式；修掉 Problem 4。Rejected: 为扩展另做一套 opsctl 审批——两套会漂移 |
| 5 | **[用户决定]** batch 支持扩展资产 | 有了 opsctl 内的执行器，batch 的每一项与其他类型一样参与整批审批与执行。Rejected: 本轮只做单资产 exec——batch 里继续报错 |
| 6 | 文件对话框在 opsctl 中不可用：需要它的工具报错，并指向 opsctl 的 `--<flag>-file` | opsctl 没有图形界面；`--<flag>-file` 已是 opsctl 读文件参数的方式（`internal/extreg/file_flags.go`）。Rejected: 在终端里模拟一个文件选择——与 `--<flag>-file` 重复 |
| 7 | 扩展 KV 的配额检查与写入在一个事务里完成 | 两个进程会同时运行同一个扩展（Problem 5）。Rejected: 只靠进程内锁——管不到另一个进程 |
| 8 | **[用户决定]** batch 不加「永久允许」 | [已核实] batch 对所有类型都只有本次允许与拒绝（`cmd/opsctl/command/tty_approval.go:51-55`，`internal/ai/permission/approval.go:89-93`，`frontend/src/components/approval/OpsctlApprovalDialog.tsx:436-479`）。Rejected: 给 batch 加永久允许——对所有类型都是新功能，超出本轮 |

## 执行

- **单资产 exec。** 前提：资产属于一个已安装、已启用的扩展。动作：`opsctl exec <asset> -- <command>`。结果：
  - opsctl 在自己的进程里加载这个扩展，按与桌面端 AI 相同的规则规范化命令、检查策略、执行工具；
  - 结果原样写到标准输出，错误写到标准错误；退出码与内置类型相同：成功 0，执行失败 1，待授权 3；
  - 桌面端在不在运行，执行位置都在 opsctl；桌面端在运行时只可能作为审批人出现。
- **扩展状态。** 扩展在桌面端被停用：报错点名扩展、说明已停用，退出码 1，不加载它。扩展目录不存在或加载失败：报错给出原因，退出码 1。
- **没有描述缓存。** 前提：本机没有这个扩展的 `describe()` 缓存（桌面端从没加载过它）。结果：opsctl 自己加载扩展取得描述，写入缓存，照常执行；不再提示「先在桌面端加载一次」。管理类命令（create、update、list、policy）照旧用缓存，缓存缺失时同样由 opsctl 加载扩展补齐。
- **宿主能力。** 扩展在 opsctl 里拿到的宿主能力与桌面端相同：
  - 读资产配置；按扩展声明的凭据能力拿明文或句柄；KV；
  - 经资产连接路径的 HTTP 与 TCP（SSH 隧道、代理、代理链、TLS）；隧道上的 SSH 跳板要 MFA 时，与 `opsctl exec` 对 SSH 资产一样在终端提示。
- **文件对话框。** 前提：工具在执行中请求文件对话框。结果：工具失败，报「opsctl 中没有文件对话框」，并指出可用 `--<flag>-file` 传文件参数。
- **超时与取消。** 工具声明的超时照常生效，超时后工具失败、退出码 1。用户按 Ctrl+C 时 opsctl 结束，与对内置类型执行时一致。
- **日志。** 扩展的日志与它的标准输出 / 标准错误不写进 opsctl 的标准输出，管道与脚本拿到的只有结果。

## 策略与审批

- **策略检查。** 与桌面端 AI 相同：
  - 扩展用 `RejectArgs` 拒绝的参数立即拒绝，不进入审批；
  - 按资产的扩展策略（策略组，或扩展默认策略）先查拒绝再查允许；
  - 已有的扩展授权（含 `--session` 会话授权）命中即放行；
  - 其余进入审批。
- **终端确认。** 前提：标准输入与标准错误都是终端。结果：
  - 提示里显示扩展的动作与资源，与桌面端审批弹窗展示的一致；
  - 选「永久允许」写入扩展规则 `ext:<策略类型>:<动作>[:<资源>]`，之后同样的调用直接放行；
  - 选「本次允许」只执行这一次，选「拒绝」不执行并以拒绝退出。
- **桌面端审批。** 前提：没有终端、桌面端在运行。结果：
  - 桌面端弹出与现在相同的扩展审批项（动作、资源、详情）；
  - 选「永久允许」保存扩展授权，之后同样的调用直接放行；
  - 审批通过后在 opsctl 里执行，结果回到 opsctl 的标准输出。
- **无人审批。** 前提：没有终端，桌面端也不在。结果：退出码 3，NEEDS AUTHORIZATION 给出能生效的授权命令，形如 `opsctl policy allow <asset> -- '<动作>:<资源>'`；按提示授权后重跑即放行。
- **会话。** 与内置类型一样，第一次需要审批时自动建立会话；`--session` 指定的会话授权对扩展调用同样生效。

## batch

- 前提：batch 里有扩展资产的项，可以与其他类型混在一批。动作：`opsctl batch`。结果：
  - 扩展项与其他项一起做整批的策略检查与一次审批；审批里扩展项显示动作与资源；
  - 与其他类型一样，batch 的审批只有「本次允许」与「拒绝」，没有「永久允许」；
  - 执行时每个扩展项在 opsctl 里执行，结果、错误与审计按 batch 现有的逐项格式给出；
  - 同一批里同一扩展的多项只加载一次扩展。
- 某个扩展项所属扩展被停用或加载失败：只有这一项报错，其余项照常。

## 审计

- 每次扩展 exec 写一条审计，与现在经桌面端执行时写的一致：来源 opsctl、工具 `exec`、参数为资产 id 与命令、规范化后的命令、结果或错误、策略决定。batch 按现有的逐项审计。
- 审计中不出现口令或凭据明文。

## 桌面端与 opsctl 同时运行

- 两个进程各自运行扩展实例，互不共享运行时状态；共用扩展目录、编译缓存与描述缓存，写入不会互相破坏。
- 扩展 KV 的写入在事务里完成配额检查与写入：两个进程并发写同一扩展时，总量不超过配额；并发写同一个键时两边都成功，最后只有一行，值是后写的一方。
- 桌面端里「代 opsctl 执行扩展工具」的请求类型被移除；桌面端仍处理 opsctl 发来的审批请求。

## 文档

opsctl 帮助文本、`docs/ARCHITECTURE.md`、`docs/references/testing-debugging-guide.md`、`extensions/README.md` 与 opsctl 技能文档中「opsctl 执行扩展要桌面端运行」的说法全部改为本轮的行为；`cmd/opsctl/command/policy_group.go` 里「扩展策略组只在桌面端运行时可见」的说法一并核对。

## Out of scope

- `opsctl ext dev`：仍把扩展目录装进运行中的桌面端。
- 扩展页面与页面动作（action）：只在桌面端。
- 扩展提供数据库驱动、opsctl 执行扩展驱动的 SQL：规格 [2026-10-19-ext-database-driver](2026-10-19-ext-database-driver.md)。
- opsctl 的日志落盘：opsctl 现在不写日志文件，本轮不改。

## Testing decisions

**[用户决定]** 单测 + e2e + 真实扩展的运行时验证。

| Seam | What it verifies | Prior art |
|---|---|---|
| opsctl 进程内执行扩展 exec | 用 WASM 夹具扩展：不连桌面端即可执行；停用的扩展报错；没有描述缓存时自己描述并写入缓存；文件对话框报错并指向 `--<flag>-file`；标准输出只有结果 | `internal/app/opsctl/ext_tool_reject_test.go`（真实 extreg + 夹具 + 审计记录），`pkg/extension/extensiontest/fixture.go` |
| opsctl 审批链对扩展 | 拒绝参数立即拒绝；终端「永久允许」写出扩展规则并在下次放行；NEEDS AUTHORIZATION 给出的命令照做后放行；桌面端「永久允许」保存扩展授权；会话授权生效 | `cmd/opsctl/command/approval_test.go`、`internal/app/opsctl/approval_grant_ext_test.go` |
| batch | 扩展项与内置项同批审批与执行；审批里扩展项显示动作与资源；一项所属扩展停用只影响这一项 | `cmd/opsctl/command/batch_test.go` |
| KV 跨进程 | 两个数据库连接并发写入时配额成立、同键写入都成功且只留一行 | `internal/repository/extension_data_repo` 的测试 |
| e2e | 桌面端关闭时 opsctl 对夹具扩展资产 exec 成功；桌面端运行、无终端时弹出扩展审批项，批准后 opsctl 输出结果 | `e2e/tests/opsctl-approval.spec.ts`、`e2e/tests/extension-asset-type.spec.ts` |

不能自动化、由收尾时的运行时验证覆盖：用一个真实扩展（如 Elasticsearch）对真实服务、经 SSH 隧道，在桌面端关闭时跑 `opsctl exec` 与 `opsctl batch`，与改动前经桌面端执行的结果和审计逐项对比；桌面端运行时的审批弹窗与「永久允许」。

## Open questions

