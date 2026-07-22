# AI 工具面收敛：统一 exec + help 技能注入

> Issue [#123](https://github.com/opskat/opskat/issues/123)
> 日期：2026-07-20
> 状态：设计已确认，待实现计划

## 1. 背景与动机

`internal/ai/tool` 目前向模型暴露 **27** 个工具，其中 **14** 个是"每种资产类型一个操作工具"：
`run_command` / `run_serial_command` / `exec_sql` / `exec_redis` / `exec_mongo` / `exec_etcd` /
`exec_k8s` 以及 7 个 `kafka_*`。

> 计数按 `tools_*.go` 中的 `NameStr:` 实测（8 + 6 + 5 + 7 + 1）。
> 注意 `tools.go:11` 的 `make([]tool.Tool, 0, 24)` 是**过期的容量提示，不是数量**——
> 极易被误读为工具总数。实现时顺手把它改正为 27（属于光标下的就地漂移修正）。

这不只是数量问题，而是**注册化原则被绕过**：

- `internal/ai/runner/prompt_builder.go` 的 `buildKnowledgeGuidance()` 用英文散文写死了一张
  "哪个类型用哪个工具"的路由表（`exec_sql` for databases、`exec_redis` for Redis、
  `exec_mongo` for MongoDB、`exec_k8s` for kubectl、`kafka_*` for Kafka）。
  这是一个用自然语言书写的 `switch assetType`，AGENTS.md 在 Go 代码里禁止的耦合，
  在 prompt 里原样复现了一份。
- `internal/ai/runner/system_template.go` 的能力清单同样逐条枚举资产类型。
- `cmd/opsctl/command/root.go` 的 verb 列表是第三份。

新增一个资产类型需要同时改这三处，且没有任何机制强制它们同步——这正是 issue 想消除的漂移。

### 1.1 仓内已有先例

`exec_tool`（`internal/ai/tool/tools_ext.go:16`）已经是 issue 所描述形态的实现：
单一 dispatcher 工具 + schema 里 `args` 为自由对象 + 真正的用法契约以散文形式注入
system prompt（`## From extension: <name>`，见 `prompt_builder.go:82`，内容取自扩展的 `SKILL.md`）。

所以本设计是**把已经在跑的机制推广到内置类型**，不是发明新机制。

### 1.2 关键技术依据

每个结构化工具**已经**在内部把结构化参数拍平成一条命令字符串，用于策略匹配 / grant / 审计：

- etcd：`cmd := FormatEtcdCommand(req)`（`internal/ai/helper/etcd_helper.go:49`），
  注释明确要求"与 audit extractor 的 formatEtcdCommand 保持等价"
- kafka：`checkKafkaToolPermission(ctx, assetID, command string)`（`kafka_helper.go:478`）
- mongo：只传裸 `operation`（`mongodb_helper.go:62`）

即：issue 要求的"命令字符串"**不需要发明，它已经作为策略/审计的内部表示存在**。

当前流程是有损往返：模型发结构化参数 → handler 拍平成字符串做审批 → 再按结构化执行。
mongo 暴露了这个代价：用户批准的是 `find`，实际执行的是"对任意集合、任意过滤条件的 find"。
**被批准的东西不等于被执行的东西。**

## 2. 目标与非目标

### 目标

1. 14 个按类型分裂的操作工具收敛为单一 `exec`。
2. 用法文档改为按资产类型的技能文件，按需注入；删除 prompt 里写死的路由表。
3. `add_x`/`update_x` 合并为 `put_x`；新增 `delete_x`。
4. `exec_tool` 更名 `ext_exec` 并改用 `(asset, command)` 形态。
5. 统一用法文档格式，消除仓内三套并存的 skill/doc 写法。
6. opsctl 同步收敛为 `opsctl exec`（接受破坏性 CLI 变更）。

### 非目标（本次不做，另开 issue 跟踪）

- **修复既有审批漏洞**：`upload_file`/`download_file` 无审批检查
  （`tool_handlers_exec.go:93,131`）、10 处 `if checker != nil` fail-open、
  `exec_tool` 在 `ext.Manifest.Policies.Type == ""` 时整段跳过检查
  （`tool_handler_ext.go:53`）。
- **补齐无 tool 的资产类型**：`local`（有 `PolicyKind`、有默认策略，却无 permission 注册也无工具，
  等于可配置不可用）、`oss`（仅扩展可达，无 PolicyKind）、`vnc`/`rdp`（无可脚本化命令面）。
  本设计用一个**只可缩短的豁免清单**把该缺口固化进测试（见 §8），不在本次填平。
- `group_svc.Delete` 的非事务性（`group_svc/group.go:75-95`，与同文件 `Reorder` 的
  `dbutil.WithTransaction` 不一致）。
- 删除资产不断开在用连接（仅 Kafka 有 `CloseAsset`，且删除路径未调用）。

## 3. 架构决策

### 3.1 每类型接缝的位置

`internal/assettype` 被 `internal/service/asset_credential_svc` 依赖，位于 service 层**之下**。
把协议执行放进去会让 service 层包传递依赖 SSH 连接池、Kafka admin client、kubectl——层级反转。

**决策：扩展 `internal/ai/permission/type_registry.go` 中已有的按类型注册表。**

该表（`:47-56`）已承载 policy kind、`shellLike` 标志、checker 函数、协议别名。
为其条目增加 `Parse` / `Format` / `Help` / `Execute`。注册表数量 2 → 1，而非 2 → 3。
协议依赖留在 `internal/ai/helper`。且 `shellLike`（驱动子命令拆分）与产出命令的 parser
必须语义一致，放在一起才能保证这一点。

**注册方向必须自下而上推送（重要）**：`internal/ai/helper` **导入** `internal/ai/permission`
（每个 helper 都调用 `permission.GetPolicyChecker`），因此 `permission` **不能**反向导入 `helper`，
执行函数无法以直接引用的方式写进表里。

解法保持设计不变：`permission` 只声明函数类型与注册入口

```go
type ExecFunc func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)

func RegisterExecutor(canonical string, exec ExecFunc, parse ParseFunc, help string)
```

由持有协议代码的包在 `init()` 中调用 `permission.RegisterExecutor(...)` 把实现推上来。
无循环依赖，表仍只有一张。这也是仓内既有惯例——扩展执行器正是通过
`tool.SetExecToolExecutor` 在 `main.go:323` 注入的。

被否决的方案：

- 扩展 `assettype.AssetTypeHandler`——层级反转，如上。
- dispatcher 内 `switch assetType`——直接违反 AGENTS.md 的 OCP 条款。

### 3.2 入参格式：纯命令字符串

`exec(asset, command)`，`command` 为该类型的规范命令字符串。

对 mongo/etcd/kafka，parser 必须是既有 `Format*` 函数的**精确逆函数**，
由此获得属性测试 `Parse(Format(req)) == req`——这比手写用例强得多，
也是 kafka 约 40 个操作仍然可行的原因。

### 3.3 用法文档：采用 cago skill 格式，不发明新格式

仓内已有规范格式：`plugin/opsctl/skills/opsctl/SKILL.md`——带 `name`/`description` frontmatter，
配 `references/commands.md` 做渐进披露，经 `plugin/content.go` 的 `//go:embed` 编入
（`SkillMD` / `CommandsMD`），再由 `internal/app/system/settings.go:939-958` 安装到
四个外部 CLI（Claude Code、Codex、OpenCode、Gemini CLI）。

而 `pkg/extension` 加载的是同一想法的退化版：裸字符串、无 frontmatter、
**硬上限 4 KiB**（`manager.go:292-296`），超限直接 `return nil, err` 导致整个扩展加载失败。
且 `Bridge` 按**资产类型**而非扩展名索引（`bridge.go:42`），同类型冲突时先写入者胜。

**决策：**

- 内置类型用 `internal/ai/skills/<type>/SKILL.md`（cago 格式，`//go:embed`）。
- `pkg/extension` 改为解析同一 frontmatter，随之取消 4 KiB 上限（正文移入 `references/`）。
- `buildKnowledgeGuidance()` 的按类型表迁入这些文件，prompt 里只留跨切面规则
  （密钥、拒绝、错误恢复）。

**与调研建议的一处刻意分歧**：调研建议用 `WithSkillDirs` 复用 cago 原生 skills 加载器，
以免费获得 `<available-skills>` 清单式渐进披露。本设计只采用**格式**，不采用**加载器**：
cago 把 skills block 的渲染 gate 在 `HasReadTool` 上，而 OpsKat 把 `read` 改名为
`local_read`（`local_tool_wrap.go`），该 block 可能静默不渲染；且其加载路径是
"模型读任意文件路径"，与 help **门禁**语义相冲突。

改为：自行渲染紧凑清单（每类型 name + description，始终列出，成本很低），
**`help(asset)` 即加载器**。渐进披露收益相同，不依赖 cago 内部实现，
且门禁自洽——`help` 既是模型的学习入口，也是门禁的满足条件。

### 3.4 snippets 不并入

snippets 是用户编写的可执行命令库（按资产类型、扩展可播种），目前除 `prompt` 分类外
对模型不可见。把用户维护的命令库并入策展的静态文档会混淆两个不同的轴。
若日后希望 AI 知晓"本厂标准命令"，那应是基于 snippet 库的独立工具。

## 4. 工具面

**27 → 15。**

| 保留不变 | 收敛进 `exec` | 新增 / 改造 |
|---|---|---|
| `list_assets`、`get_asset`、`list_groups`、`get_group` | `run_command`、`run_serial_command`、`exec_sql`、`exec_redis`、`exec_mongo`、`exec_etcd`、`exec_k8s`、7×`kafka_*` | `exec`、`help` |
| `upload_file`、`download_file`、`request_permission` | `batch_command` → `batch_exec` | `put_asset`、`put_group`、`delete_asset`、`delete_group`、`ext_exec` |

收敛后的 15 个工具：`list_assets`、`get_asset`、`put_asset`、`delete_asset`、
`list_groups`、`get_group`、`put_group`、`delete_group`、`exec`、`batch_exec`、
`help`、`ext_exec`、`upload_file`、`download_file`、`request_permission`。

其中 14 个按类型分裂的操作工具 → 1 个 `exec`，是本次收敛的主体；
`add_*`/`update_*` 合并省下 2 个，`delete_*` 新增 2 个，`help` 新增 1 个，净减 12。

`upload_file`/`download_file` 刻意保留独立：它们接收本地文件系统路径并流式传输，不是命令形态；
硬塞进命令字符串等于发明一套策略层无法有意义匹配的 scp DSL。

### 4.1 `exec`

```
exec(asset: string, command: string, scope?: string, type?: string)
```

- `asset`——id 或名称。共用解析器，**同名歧义必须报错**而非任选其一（当前允许重名）。
- `command`——该类型的规范命令字符串。
- `scope`——确实不属于命令本身的连接级目标。仅两个类型使用：
  `database`（库名）与 `redis`（db 序号，因为连接池下 `SELECT` 无效）。
- `type`——**可选断言，不参与派发**。给出时与资产真实类型比对，不符则在权限检查/审批
  **之前**返回点名双方类型的错误。Plan C 新增，理由与三处落点见 §4.6 的决策更新。

| 类型 | 命令形态 |
|---|---|
| ssh / serial | shell / 控制台命令，原样 |
| database | SQL，原样 |
| redis | `GET mykey`，原样 |
| k8s | `get pods -A`（kubectl），原样 |
| mongo | `find app.users {"age":{"$gt":18}}` |
| etcd | `get /key --prefix --limit 10`、`put /k v --lease 123`、`lease grant 60`、`member list` |
| kafka | `topic create foo --partitions 3`、`consumer-group reset-offset g1 --to-earliest`、`message browse t --limit 10` |

### 4.2 `help`

`help(asset)` 返回该资产类型的用法文档。

门禁：当模型已对该类型调用过 `help` 时，`(conversationID, assetType)` 标记为"已知用法"。
对未标记类型调用 `exec` 返回**引导性工具结果，而非 Go error**——模型据此自我纠正，
不会中断整轮。状态按会话保存在内存中，生命周期与 `LocalToolGate` 的 allow-list 一致。

> **实施期修正（2026-07-20，分支 `feature/ai-tool-exec-foundation`）**：本节原先规定门禁有
> **两个**满足条件，第二个是"该类型的文档已在本次 Send 注入 system prompt"。该条件在实施
> 收尾评审中被移除，`help` 成为**唯一**满足条件，原因有二：
>
> 1. prompt 里注入的只是 `skills.Description()` 的**一行描述**，不是命令语法正文。把它当成
>    "模型已看过文档"，等于让 `exec` 在模型从未见过语法的前提下执行——门禁想防的正是这个。
> 2. 标记在整个会话内**永久**有效，而 prompt 注入是**每次 Send 重建**的。第 1 次 Send 恰好
>    开着某个 Tab，就会让该类型在此后整个会话里永远不过门禁，哪怕 Tab 早已关闭。
>
> prompt 中的按类型清单**保留**，但仅作**发现**入口（让模型知道 `help` 存在、`exec` 覆盖了
> 哪些类型），且改为无条件全量列出、不再随 Tab 变化。见 `internal/ai/tool/exec_gate.go`
> 与 `internal/app/ai/chat.go` 的 `allBuiltinAssetTypeSkills`。

### 4.3 `put_asset` / `put_group`

```
put_asset(id?, name, type, group_id?, config)   // 有 id → 更新，无 id → 创建
```

`add_asset` 当前带一张覆盖全部 10 种类型的**巨型 schema**（`tools_asset.go:51-102`）——
这与我们正在移除的类型分支是同一种耦合，只是写成了 JSON Schema 的属性并集。
合并为 `put_asset` 时删除它：`config` 为自由对象，其按类型的形状由**同一份 help 文档**说明，
校验回到本就该负责的 `assettype.ValidateCreateArgs`。

于是一份类型文档同时服务三个工具（`exec`、`put_asset`、`help`），这也提高了格式选型的门槛
（见 §3.3）。

### 4.4 `delete_asset` / `delete_group`

删除比表面更危险：

- `asset_repo.Delete`（`asset_repo/asset.go:86-93`）是软删除，但会把 `config` 与
  `command_policy` **清空为 `""`**。行还在，连接信息与凭据引用已丢失，且仓内**没有任何恢复路径**。
  实质上不可逆。
- **无任何级联**：凭据静默孤儿化，grant item 仍指向已删资产，**在用会话不会关闭**
  （仅 Kafka 有 `CloseAsset`，删除路径未调用）。AI 可以删掉用户正连着的资产，
  而终端标签页照常工作。工具描述不得暗示会断开连接。

> **实施期修正（2026-07-21，Plan C 计划编写时）**：上面第二条的**断连部分已不成立**。
> §9 第 5 条列的 issue 已在 `fix/exec-convergence-followups` 上实现并合回：
> `internal/assetconn` 注册表把连接分成 Closer（交互式会话，删除时关）与 Invalidator
> （缓存/池化连接，改配置和删除都丢），`asset_svc.Delete` 删除成功后广播、
> `group_svc.Delete(deleteAssets=true)` 在事务提交后逐个补广播并逐条写 `delete_asset` 审计
> （`fe71ee15`、`219b691d`）。
> 因此 `delete_asset` 的工具描述**应当**说明连接会被断开——刻意未接的两处例外
> （k8s 日志流、本地终端）见 `internal/assetconn` 包注释。
> 仍然成立的是：凭据孤儿化、grant item 悬挂、软删除不可逆——这三条仍是"恒需确认、不可 grant"的理由。

因此：**`delete_*` 恒需确认，且不可 grant。** grant 用于重复命令模式，
"删除这个资产"不是可预批的模式。handler 在删除**之前**捕获名称并写入审计记录。

`delete_group` 默认 `delete_assets=false`（移入未分组），即非破坏性分支。

### 4.5 `ext_exec`

`exec_tool` 更名 `ext_exec`，改为 `(asset, command)`，其中 command 为
`<extension> <tool> --flags`——与 `exec` 的"首 token 即操作"一致。
`asset` 留在外层，因为策略检查读的就是外层值。
`--json '{...}'` 形态覆盖 flag DSL 无法表达的 schema，确保没有工具变得不可调用。

flag DSL 可行：两个真实 manifest 中每个属性都有声明类型、无嵌套、最大 arity 为 5
（28 个 `string`、2 个 `integer`、1 个 `array<string>`）。

**但有陷阱**：`manifest.validate()` 从不检查 `tools[].parameters`（`manifest.go:194-230`），
且该字段从未被任何代码读取——我们将把一个**从未校验、从未被行使**的字段提升为承重契约。
**必须在同一次改动中加固 manifest 校验**：加载期拒绝缺失/非对象的 `parameters` 与无类型属性，
让坏 manifest 响亮失败，而不是退化成令人困惑的运行期解析错误。

`ext_exec` 保持与 `exec` 分离而非完全统一——不是拖延，而是策略路径确实不同：
扩展调用要经过 WASM `Plugin.CheckPolicy` 往返加 `CheckExtensionPolicy`，
且并非所有扩展都是资产范围的。

### 4.6 类型识别与错误可供性

收敛后一个自然的疑问是："模型还分得清资产是什么类型吗？"
答案是**它不再需要分**——派发方向反转了。

**今天：模型选协议。** `exec_sql` 接收 `asset_id`，并用写死的 `AssetTypeDatabase`
做权限检查（`database_helper.go:74`），而 `!asset.IsDatabase()` 守卫在 11 行之后
（`:85`）。若模型把 Redis 资产传给 `exec_sql`，SQL 文本会被拿去匹配**数据库策略**，
之后才报错退出。**类型混淆当前就可能发生，且会在报错前污染策略判定。**

**改造后：资产决定协议。** `exec(asset, command)` 先解析资产、读取其真实类型，
再从注册表取执行器。**模型无法误路由**——协议来自资产记录，而非模型的工具选择。
这是安全性提升，不是退步。

模型仍需知道类型才能写出合法命令，而类型已有四条发现路径：
`list_assets` / `get_asset` 返回 `Type`（`tool_handlers_asset.go:79`）、
打开的 Tab 上下文、mention 标签的 `type="database"`。
help 门禁把"可发现"升级为"有保证"：`exec` 解析资产 → 得到类型 →
若该类型本会话未记录用法，则返回引导而不执行。模型调用 `help`，
同时学到类型与 DSL，然后重试。自我纠正，一次往返，按类型按会话缓存。

**决策：只在消息层处理，不新增参数。**

1. 门禁引导语明确点出解析出的类型：
   `asset "prod-db" is type=database — call help("prod-db") for its command syntax`，
   而不是干巴巴的"请先调用 help"。
2. `help` 输出以解析出的类型开头，让"这是什么"与"怎么写"一起到达。
3. 解析失败的报错带类型：
   `asset "cache-1" is type=redis; "SELECT * FROM users" is not a valid Redis command`。
   今天这类错配只会浮现为协议层的服务端报错，读起来像基础设施故障而非建模错误。

~~**不加** 由模型声明的 `type` 断言参数：既然派发已由资产导出，
校验模型给出的类型是在防一个不可能产生错误路由的场景，
正是 AGENTS.md 所禁止的无意义防御式代码。~~

> **决策更新（2026-07-21，Plan C，用户裁定）：改为加一个可选的 `type` 断言参数。**
>
> 上面那段划掉的推理有一处越界：它论证的是「type 无法产生错误**路由**」——这仍然成立，
> `type` **绝不参与派发**，协议永远只从资产记录取。但它由此推出「因此不该校验」，
> 而实际要防的根本不是路由，是**方言错配**：模型完全可能把 Redis 命令写给一个
> database 资产。本节末尾「一处诚实的局限」已经承认，五个原样透传的类型
> （ssh、serial、database、redis、k8s）**无法靠解析发现错配**——`SELECT 1` 在 SQL 与
> Redis 里都合法。`type` 恰好补上的就是这个洞：让模型把它以为的类型说出来，与资产的
> 真实类型对一次。
>
> 这也不违反 AGENTS.md 的「无意义防御式代码」条款——工具入参是**边界**
> （模型产出的 `map[string]any`），而 AGENTS.md 的规则正是「Validate at boundaries only」。
>
> 落点三处，形态一致、共用同一个校验函数：
>
> | 面 | 形态 | 缺省 |
> |---|---|---|
> | AI `exec` 工具 | `exec(asset, command, scope?, type?)` | 不给则跳过校验，按资产真实类型执行 |
> | AI `batch_exec` 条目 | 每条可带 `type` | 同上 |
> | `opsctl` | `opsctl exec <asset> [--type <t>] -- <cmd>`；`batch` 沿用既有 `'sql:2:SELECT 1'` 前缀 | 同上，裸 `'1:uptime'` 继续可用 |
>
> 两条硬性要求：
>
> 1. **校验发生在权限检查与审批之前**。对照 Plan A 收尾评审的 IMPORTANT-1
>    （serial 走统一 exec 时"先弹审批、批准后才失败"）——断言失败不该让用户先批一个注定失败的命令。
> 2. **错误信息点名双方**，与本节三条可供性措施同格式：
>    `asset "cache-1" is type=redis, but you passed type=database — call help("cache-1") for its command syntax`。
>
> 副作用（正面）：`opsctl batch` 的前缀语法**无需破坏性变更**即可完成语义迁移——
> 从"选 handler"变成"类型断言"。

**一处诚实的局限**：五个原样透传的类型（ssh、serial、database、redis、k8s）
无法靠解析发现错配，因为任何字符串在语法上都成立——`SELECT 1` 甚至在 SQL 与 Redis 中都合法。
缓解手段不是解析，而是"资产被显式命名 + 派发由资产导出"，
因此最坏情况是"把错误的命令发给了被正确识别的资产"，会在协议层失败。
其影响范围与今天的手误相同，且严格小于今天的误路由。

## 5. 审批与审计不变式

1. **被批准的 == 被执行的**：`CheckForAsset` 收到模型的字面命令字符串，而非重新推导的字符串。
2. **策略按资产的真实类型检查，且类型先于检查解析**。
   今天 `HandleExecSQL` 用写死的 `AssetTypeDatabase` 做检查（`database_helper.go:74`），
   `!asset.IsDatabase()` 守卫却在 11 行之后（`:85`）——传入非数据库资产时，
   策略会用错误的类型匹配一遍才报错。
   新流程"解析资产 → 取真实类型 → 按该类型检查 → 执行"天然消除该顺序缺陷。
   **这是本次重构顺带修掉的真实缺陷，不只是重排代码。**
3. **审计必须认识 `asset` 参数**：`audit.go:53-55` 目前只读 `asset_id` / `id`。
   新工具用 `asset`。不改则**每个新工具都记录不到资产**——这类缺陷能通过测试却悄悄掏空审计日志。
4. **删除前捕获名称**：`asset_repo.Find` 过滤 `status = StatusActive`（`asset.go:55`），
   而审计在工具执行**之后**才解析名称（`audit.go:59-61`）。
   不处理则删除记录的 `AssetName` 为空——恰恰是最需要名称的那条记录。
5. ssh/k8s 仍走 `shellLike` 子命令拆分。
6. 各工具的审计 extractor 收敛为 `exec` 一个。

### 5.1 迁移后果（需发版说明）

mongo/etcd/kafka 的策略字符串形状改变：mongo 当前匹配裸 `"find"`，之后将看到
`find app.users {...}`。**这三类的既有用户策略与 grant 会失配。**
失败方向是安全的（更多确认弹窗，绝不会放宽权限），但用户会有感知。
这也是 mongo/etcd/kafka 三个 parser 应在同一个版本内一次发布、而非分批滴漏的最强理由。

## 6. opsctl

- `opsctl exec <asset> -- <command>` 覆盖全部类型；`opsctl help <asset>`。
- 旧 verb（`sql`/`redis`/`mongo`/`ssh`）移除——**已确认接受破坏性 CLI 变更**。

> **实施期修正（2026-07-21，Plan C 计划编写时，对照真实代码）**：本节有两处需要更正。
>
> 1. **`ssh` verb 不移除。** 它不是 `exec` 的旧形态，而是**交互式 pty 会话**
>    （`cmd/opsctl/command/ssh.go`：`term.MakeRaw` + 窗口 resize + 连接池代理），
>    `exec` 无从替代。实际移除的只有 `sql` / `redis` / `mongo`。
> 2. **`exec` 按类型分派，ssh 保留流式路径。** 今天的 `opsctl exec` 是 SSH 专用的**流式**通道：
>    转发 stdin 管道、stdout/stderr 直写本地、透传远端 exit code
>    （`exec.go`：proxy 快路径 + `helper.ExecWithStdio`）。统一 exec 的 handler 返回的是
>    **捕获后的字符串**，全量改道会打断 `plugin/opsctl/skills/opsctl/SKILL.md` 里已文档化的
>    管道工作流（`cat config.yml | opsctl exec web-01 -- tee ...`、
>    `opsctl exec staging-db -- "mysqldump | gzip" > dump.gz`）。
>    因此 `cmdExec` 先解析资产、取真实类型：ssh 走现有流式路径，其余类型走统一 `exec`。
>    分派依据仍是资产的真实类型，OCP 不受影响。
> 3. `--type` 可选断言与 `batch` 前缀语法的保留，见 §4.6 的补充。
- 顺带盘活 `exec_etcd`/`exec_k8s`/`kafka_*`：这些 handler 已在 `AllToolDefs()` 注册，
  却没有任何 verb 能抵达（`root.go:110-136`），属于已注册的死代码。
- `serial` 返回明确的"需要桌面端会话"错误——它依赖已连接的串口 session，
  因此被刻意排除在 `AllToolDefs()` 之外（`tool_registry.go:30-31`）。
- `opsctl delete asset` 几乎免费：`create`/`update` 已经直接派发进 AI tool handler
  （`create.go:148,224`）。需照 `create.go:135,217` 的模式补 grant `Detail` 串。

## 7. 前端影响

- `ApprovalBlock.tsx` 的类型徽章按工具名映射，需随工具改名更新。
- `assetStore.ts:80-85` 的 `deleteAsset` 会同时清理 `useRecentAssetStore` 与
  `selectedAssetId`。AI 路径删除资产不会触发该清理，需要经由既有 `data:changed`
  事件让前端重取。

> **补充（2026-07-21，Plan C 计划编写时，逐处核实）**：
>
> - `ApprovalBlock.tsx` 现位于 `frontend/src/components/approval/`（不在 `components/ai/`）。
>   它的 `TypeBadge` 图标表按 **`item.type`**（`exec`/`sql`/`redis`/`mongo`/`kafka`/`cp`/`grant`/
>   `local_*`）映射，**不按工具名**——所以 `put_*`/`batch_exec`/`ext_exec` 改名不影响它。
> - 真正需要前端配合的是 `delete_*`：`kind === "single"` 会渲染 rememberMode（"全部允许"→
>   写 grant）。而 §4.4 要求删除**不可 grant**，因此删除审批必须用一个新的 `kind`
>   （不渲染 rememberMode、不带 allowAll 按钮），并给 `TypeBadge` 补删除图标。
>   前端不改就等于在 UI 上给用户提供了一个"以后自动批准删除"的按钮——把后端的
>   不可 grant 约束当场架空。
> - `ToolBlock.tsx` 的 `toolIcons` 表已含 `exec`/`help`（Plan A 收尾的 MINOR-5 已修），
>   Plan C 补 `put_asset`/`put_group`/`delete_asset`/`delete_group`/`ext_exec` 五个图标。
> - `assetStore` 那条仍然成立，但 AI 侧删除只需复用既有的 `aictx.NotifyDataChanged("asset")`
>   （`handleAddAsset` 等已在调用，经 `internal/app/ai/notifier.go` 广播 `data:changed`，
>   `App.tsx:61` 已在监听）。

## 8. 测试策略

- **属性测试**：`Parse(Format(req)) == req`，逐类型——正确性锚点。
- **门禁测试**：help 前 exec → 引导结果；help 后 → 放行；已自动注入 → 放行。
- **齐备性测试**：每个 `PolicyKind()` 非空的 `assettype` 都必须有 exec 条目，
  配 `{local, oss}` 豁免清单，且该清单**只可缩短不可增长**——沿用
  `internal/archtest` 已确立的惯例。缺口被固化进代码而非遗忘在 issue 里。
- **审批保真**：表驱动断言 `CheckForAsset` 收到的是字面命令，逐类型。
- **策略类型正确性（回归测试）**：断言策略检查收到的是资产的**真实**类型，
  覆盖"命令写成另一类型语法"的场景——锁住 `database_helper.go:74` 的顺序缺陷不再复现。
- **类型可供性**：断言门禁引导语、`help` 输出、解析失败报错三者都包含解析出的资产类型。
- **审计回归**：断言 `asset` 能解析出非空名称，含删除场景。
- **解析器测试**：名称、字符串形态的数字 id、**同名歧义必须报错**、不存在。

## 9. 待跟踪的独立 issue

实现时应开出：

1. ✅ [#248](https://github.com/opskat/opskat/issues/248) `upload_file`/`download_file` 无审批检查（安全）。
2. ✅ [#249](https://github.com/opskat/opskat/issues/249) 审批 fail-open，**已修**。
   开 issue 时数的 10 处，到统一 exec 收尾时只剩 4 处（`internal/ai/helper/` 下那 6 处
   随旧工具一起删了）：unified exec、`exec_tool` 的 NeedConfirm 分支、batch 的 2 处。
   修法不是逐处补 `== nil` 判断，而是**把放行分支写不出来**：`GetPolicyChecker` 收成包内的
   `getPolicyChecker`，包外只剩 `RequireChecker`（缺失即报错）与 `RequireCheckerOrPreapproved`
   （只在 ctx 带 `WithPreapproved` 时返回空 checker）。opsctl 是唯一的豁免方——它在
   `requireApproval` 里已经跑完策略/Grant/桌面审批，两个派发点（`callHandler` 带审批结论时、
   `executeBatchHandler`）显式打标记。
   *开 issue 时核实的结论仍然成立：这条当前**不可达**——`policyChecker` 与 `systemCfg` 都只在
   `activateProvider` 成功路径赋值且前者在先，而 `SendAIMessage` 守卫 `systemCfg == nil`，
   传递性地保证了 checker 非 nil。修它是为了消除"不变式由赋值顺序承载、无类型/测试锁定"
   这个结构性隐患，不是在补一个活的漏洞。*
3. ✅ [#250](https://github.com/opskat/opskat/issues/250) `local` / `oss` 的 AI 工具支持（豁免清单清零）。
4. ✅ `group_svc.Delete` 事务化。
5. ✅ 删除资产时断开在用连接（`internal/assetconn` 注册表 + `asset_svc.Delete` 删除成功后广播；
   `group_svc.Delete(deleteAssets=true)` 走 `DeleteByGroupID` 绕过 asset_svc，在事务提交后
   逐个补广播，并逐条写 `delete_asset` 审计）。k8s 日志流（streamID 与 assetID 无关）与
   本地终端刻意未接，见 `internal/assetconn` 包注释。
   *顺带修掉的邻接缺陷*：资产**改配置**同样要处置连接，而此前只有 etcd / oss 做了失效、
   还挂在 `assettype.ApplyUpdateArgs` 里（只有 AI / opsctl 的 `update_asset` 会走到，
   桌面 UI 改资产不触发）。现在 `assetconn` 分成 Closer（交互式会话，只在删除时关）
   与 Invalidator（缓存/池化连接，改配置和删除都丢），`asset_svc.Update` 写库成功后
   广播 `InvalidateAsset`——改口令 / 换主机之后不必再手动重开面板。
6. ✅ 桌面 UI 路径的资产 CRUD 补审计（`audit.WriteAssetChange`，经 `System.desktopCtx()`
   标 `source=desktop`；请求体是白名单字段，不带 `Config` 里的口令）。
   *开 issue 时那句"`Source: "desktop"` 有定义但无写入方"当时就已经不准：
   `external_edit_svc` 一直在写外部编辑器会话的 desktop 审计。真正缺的只有资产 CRUD。*

另有一条不在原清单里、实现期间发现并一并修掉的：`batch_command` 按名字寻址资产恒失败
（`resolveAssetForBatch` 借道只认数字 id 的 `handleGetAsset`，而工具参数描述写的是
`{"asset": "name-or-id"}`）。已改走 `assetref.Resolve`，与 exec / help 同一个解析器。

1–3 已于 2026-07-20 随 Plan A 收尾开出；4–6 未开 issue，直接在
`fix/exec-convergence-followups` 上实现并合回本分支。
