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
  （`tool_handlers_exec.go:136,174`）、10 处 `if checker != nil` fail-open、
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
exec(asset: string, command: string, scope?: string)
```

- `asset`——id 或名称。共用解析器，**同名歧义必须报错**而非任选其一（当前允许重名）。
- `command`——该类型的规范命令字符串。
- `scope`——确实不属于命令本身的连接级目标。仅两个类型使用：
  `database`（库名）与 `redis`（db 序号，因为连接池下 `SELECT` 无效）。

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

门禁：当 **(1)** 模型已对该类型调用过 `help`，**或 (2)** 该类型的文档已在本次 Send 注入
system prompt 时，`(conversationID, assetType)` 标记为"已知用法"。
对未标记类型调用 `exec` 返回**引导性工具结果，而非 Go error**——模型据此自我纠正，
不会中断整轮。状态按会话保存在内存中，生命周期与 `LocalToolGate` 的 allow-list 一致。

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

## 5. 审批与审计不变式

1. **被批准的 == 被执行的**：`CheckForAsset` 收到模型的字面命令字符串，而非重新推导的字符串。
2. **审计必须认识 `asset` 参数**：`audit.go:53-55` 目前只读 `asset_id` / `id`。
   新工具用 `asset`。不改则**每个新工具都记录不到资产**——这类缺陷能通过测试却悄悄掏空审计日志。
3. **删除前捕获名称**：`asset_repo.Find` 过滤 `status = StatusActive`（`asset.go:55`），
   而审计在工具执行**之后**才解析名称（`audit.go:59-61`）。
   不处理则删除记录的 `AssetName` 为空——恰恰是最需要名称的那条记录。
4. ssh/k8s 仍走 `shellLike` 子命令拆分。
5. 各工具的审计 extractor 收敛为 `exec` 一个。

### 5.1 迁移后果（需发版说明）

mongo/etcd/kafka 的策略字符串形状改变：mongo 当前匹配裸 `"find"`，之后将看到
`find app.users {...}`。**这三类的既有用户策略与 grant 会失配。**
失败方向是安全的（更多确认弹窗，绝不会放宽权限），但用户会有感知。
这也是 mongo/etcd/kafka 三个 parser 应在同一个版本内一次发布、而非分批滴漏的最强理由。

## 6. opsctl

- `opsctl exec <asset> -- <command>` 覆盖全部类型；`opsctl help <asset>`。
- 旧 verb（`sql`/`redis`/`mongo`/`ssh`）移除——**已确认接受破坏性 CLI 变更**。
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

## 8. 测试策略

- **属性测试**：`Parse(Format(req)) == req`，逐类型——正确性锚点。
- **门禁测试**：help 前 exec → 引导结果；help 后 → 放行；已自动注入 → 放行。
- **齐备性测试**：每个 `PolicyKind()` 非空的 `assettype` 都必须有 exec 条目，
  配 `{local, oss}` 豁免清单，且该清单**只可缩短不可增长**——沿用
  `internal/archtest` 已确立的惯例。缺口被固化进代码而非遗忘在 issue 里。
- **审批保真**：表驱动断言 `CheckForAsset` 收到的是字面命令，逐类型。
- **审计回归**：断言 `asset` 能解析出非空名称，含删除场景。
- **解析器测试**：名称、字符串形态的数字 id、**同名歧义必须报错**、不存在。

## 9. 待跟踪的独立 issue

实现时应开出：

1. `upload_file`/`download_file` 无审批检查（安全）。
2. 审批 fail-open：10 处 `if checker != nil` 应改为 fail-closed，
   参照唯一正确的 `tool_handlers_exec.go:62-65`，以及已中心化且 fail-closed 的
   `LocalToolGate.Middleware()`（`local_tool_gate.go:78-81`）。
3. `local` / `oss` 的 AI 工具支持（豁免清单清零）。
4. `group_svc.Delete` 事务化。
5. 删除资产时断开在用连接。
6. 桌面 UI 路径的资产 CRUD 未写审计（`Source: "desktop"` 有定义但无写入方）。
