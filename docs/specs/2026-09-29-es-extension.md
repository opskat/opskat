# Elasticsearch 扩展：官方扩展仓迁移、多资源策略、可读文件参数与页面直通

> Status: Approved
> Owner: OpsKat 维护者
> Last updated: 2026-09-29

**Objective:** 用户安装 Elasticsearch 扩展后，能像内置数据库一样新建 ES 资产（含隧道 / 代理 / TLS / 认证），在资产页里浏览集群、索引、文档并用控制台发请求；AI 与 opsctl 经 `exec` 操作 ES，并受按索引粒度的规则、审批与审计约束。

**Hard invariant:** 平台能力（#306 / #326）已建立的宿主裁决不回退——AI 与 opsctl 对扩展资产的调用照旧经策略、审批、grant、审计；只使用单资源分类、不使用新参数标记的已有扩展（notebook）行为不变；guest 不因本变更获得超出 manifest 声明的能力；ES 扩展不声明 `credentials:read`，凭据明文不进入 WASM。

## Problem

标注：**[已核实]** = 对照当前 main（12439e63）或 `../extensions` 仓源码确认；**[用户决定]** = 本轮对话中用户选定。

1. **官方扩展仓仍是已删除的旧 ABI。** [已核实] `../extensions` 的 `extensions/oss/manifest.json` 声明 `hostABI: "1.0"`，`sdk/go` 为按次启动、`os.Args[0]` 分发的旧模型；宿主只接受 `2.0` / `2.1`（`pkg/extension/manifest.go:35`）。仓内三个扩展（sdk 示例 echo、oss）都无法在当前应用加载；oss 已成为内置资产类型。仓库没有 CI。
2. **没有 Elasticsearch 支持。** [已核实] 内置资产类型不含 ES；平台能力 spec 以 ES 为首个客户，并把"请求体从文件 / stdin 读取、DSL 补全等"推给本 spec（`docs/specs/2026-09-24-ext-platform-capabilities.md` Out of scope）。
3. **一次调用只能报一个资源，多目标请求可绕过拒绝规则。** [已核实] `PolicyFunc` 的分类函数返回单个 `(action, resource string)`（`pkg/extsdk/registry.go:202`），宿主按单个 resource 做 `path.Match`（`internal/extreg/extreg.go:47`、`:359`）。ES 一个请求常同时作用于多个索引（`DELETE /a,prod-1`、跨索引 `_bulk`、`logs-*` 通配）；压成一个字符串后，`*` 会跨过逗号（`write:logs-*` 命中 `logs-a,secret`），而 `deny delete:prod-*` 不命中 `x,prod-1`，配合一条宽 allow 即被绕过。
4. **opsctl 无法传大请求体。** [已核实] 扩展工具参数只能在命令行内联给出；bulk 等大请求体受命令行长度限制（macOS 约 1MB），也无法从管道读入。
5. **扩展页面上的操作要走审批。** [已核实] 页面对已保存资产的工具调用经 `pageGate.RunPageToolCall` 走策略 / 审批 / grant / 审计（`internal/app/extension/extension_ops.go:130-190`）。[用户决定] 页面操作是用户本人在界面里的操作，应与内置 Redis / 数据库面板一致直接执行；审批只针对 AI 与 opsctl。

## Actors and user stories

1. 作为**运维用户**，我想新建 ES 资产时填地址、选认证方式、配 SSH 隧道 / 代理 / TLS，并点"测试连接"确认可用。
2. 作为**运维用户**，我想打开 ES 资产就看到集群健康、节点、索引列表，点开索引浏览文档、看 mapping / settings，并在控制台里直接发任意请求。
3. 作为**运维用户**，我想给 ES 资产写"放行只读、拒绝删除 prod-* 索引"这类规则，并且 AI / opsctl 一次操作多个索引时，只要其中一个命中拒绝就被拒。
4. 作为**opsctl 用户**，我想用 `--body-file bulk.ndjson` 或管道把大请求体交给 ES，而不受命令行长度限制。
5. 作为**扩展作者**，我想在官方扩展仓里基于 `pkg/extsdk` 开发、构建、测试扩展，并有 CI 把关。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **[用户决定]** `../extensions` 迁为基于 `pkg/extsdk` 的官方扩展仓：删除旧 `sdk/go`、`examples/echo`、`extensions/oss`，ES 放 `extensions/elasticsearch` | 旧内容已无法加载，oss 已内置、notebook 已取代 echo；商店 spec 将在此仓发布扩展。Rejected: 放 opskat 仓 `extensions/`——扩展发版节奏绑死主仓；旧内容原样保留——仓内并存两套互相矛盾的 SDK 与说明 |
| 2 | **[用户决定]** 页面范围：概览 + 索引列表 + 文档浏览 + Mapping / Settings + 控制台 | 覆盖日常查看与排障。Rejected: 只做控制台——浏览需手写请求；完整管理台（建删索引、别名、ILM、快照 UI）——工作量 2–3 倍，留后续 |
| 3 | **[用户决定]** 兼容 ES 7.10+ / 8.x / 9.x，只用这些版本共有的 REST API；运行验证覆盖 7.17 与 8.19 | 7.x 存量大。Rejected: 加 OpenSearch——多一套验收与认证差异；只 8.x / 9.x——漏掉存量集群 |
| 4 | **[用户决定]** 分类可返回多个资源；任一资源命中 deny 即拒，全部被 allow / grant 覆盖才放行；含通配的资源按"可能重叠即 deny、完整覆盖才 allow"保守匹配 | 堵住 Problem 3 的绕过，且对单资源扩展无变化。Rejected: ES 侧把多目标一律收敛成需询问——宽 allow + 窄 deny 的组合下逗号列表仍能绕过 deny |
| 5 | **[用户决定]** SDK 可把字符串参数标为"可读文件"，opsctl 为其额外提供 `--<flag>-file <path\|->` | 大请求体不受命令行长度限制，且策略 / 审批 / 审计看到的是读入后的内容。Rejected: 只在文档里建议 `--body "$(cat f)"`——仍受 ARG_MAX 限制、无法读 stdin |
| 6 | 读文件只发生在 opsctl 进程内；AI 的 `exec` 与扩展页面不接受 `-file` 形式 | 否则 AI 可借此读取用户本机任意文件。Rejected: 由宿主（桌面端）读文件——桌面端进程权限与 opsctl 调用方不同，且 AI 路径也会获得读文件能力 |
| 7 | **[用户决定]** 扩展页面的工具调用不经策略、审批、grant、审计，与内置面板一致；仍按资产作用域执行（endpoint 放行、连接配置、凭据注入），可取消 | 页面操作是用户本人发起的，审批属于 AI 与 opsctl。页面代码与宿主同一 JS 上下文、本可直接调用宿主绑定（#326 已知遗留），闸门对恶意页面并无实际隔离作用。Rejected: 保留闸门（#326 决策 5）——与内置面板行为不一致，浏览文档也可能弹审批；保留审计——每次翻页刷新都写审计行 |
| 8 | **[用户决定]** 控制台里任何请求（含删除、管理类）都直接执行，不另弹确认 | 与 Kibana Dev Tools 一致。Rejected: 删除 / 管理类弹页面内确认；所有非读请求都确认 |
| 9 | **[用户决定]** 多资源审批的"记住并允许"预填能覆盖全部资源的最窄 `<action>:<公共前缀>*`，没有可用前缀时预填裸动作 | 接近最小权限，用户仍可修改。Rejected: 一律预填裸动作——默认覆盖所有索引 |
| 10 | `request` 工具把 ES 的 HTTP 状态与响应体作为结果返回（4xx / 5xx 也是结果），只有连不上、TLS / 超时等传输失败才算工具失败；`health` / `indices` / `mapping` / `search` 遇 ES 错误则失败 | 页面控制台与 AI 需要看到 ES 的错误体；通用请求工具与 curl 一致，便捷工具语义明确。Rejected: `request` 遇 ≥400 即失败——页面控制台拿不到结构化的错误体 |
| 11 | **[用户决定]** 控制台与文档页 DSL 框用宿主的 Monaco 编辑器，由页面自行注册专用语言，提供语法高亮与关键字级自动提示（方法、常用 API 路径、集群中的索引 / 别名、常用 DSL 键、目标索引 mapping 中的字段名） | 覆盖日常手写请求的主要摩擦且无需宿主改动（编辑器挂载回调已交出 Monaco 实例）。Rejected: Kibana 级按 API 请求体 schema 提示——需维护一份 ES API 规范数据，工作量大；只补请求行——请求体仍全靠手写 |

## 扩展页面调用（平台变更）

- 前置：扩展页面对其所属的已保存资产调用工具。动作：调用发出。结果：直接执行该工具，不经策略判定、不弹审批、不查 / 不写 grant、不落审计；调用照旧以该资产为作用域——`ctx.AssetConfig()`、endpoint 放行、连接配置（隧道 / 代理链 / TLS）与凭据注入全部生效。
- 页面仍可取消进行中的调用；取消与超时中断阻塞中的宿主 IO。工具声明的超时照旧生效。
- 工具失败（含 guest 返回的错误）以被拒绝的 promise 交给页面，错误信息原样。
- AI 的 `exec` 与 opsctl 对同一资产的调用不受影响，照旧经策略、审批、grant、审计。
- 扩展开发文档中"页面调用与 opsctl exec 走同一闸门"的描述随之改为本节语义。

## 多资源策略（平台变更）

- **分类。** 工具的分类函数可返回一个 action 与零到多个资源。只返回一个资源的扩展，行为与现状完全一致；零个资源等同于现在的空 resource。
- **通配资源。** 资源中的 `*`、`?` 表示通配（该资源代表一组可能的名字）。
- **判定。** 顺序仍为 deny → allow → grant → confirm：
  - 任一资源命中 deny 规则即拒绝。对通配资源，只要 deny 的 glob 与它**可能匹配同一个名字**即算命中；无法判定时按命中处理。
  - 每个资源都被某条 allow 规则或某个 grant 覆盖时放行（不同资源可由不同规则 / grant 覆盖）。对通配资源，allow 的 glob 须**覆盖它可能代表的全部名字**才算覆盖；无法判定时按未覆盖处理。
  - 其余情况询问用户。无资源的规则照旧覆盖该 action 的任意资源。
- **审批展示。** AI / opsctl 的审批弹窗列出 action 与全部资源。"记住并允许"的预填：单资源为 `<action>:<resource>`；多资源为能覆盖全部资源的最窄 `<action>:<公共前缀>*`（前缀截止到第一个通配符；得到的规则若不能覆盖全部资源，或前缀为空，则预填裸 `<action>`）。用户可编辑，校验规则与现状一致。
- **审计。** 审计沿用现有字段：规范化命令（含完整参数，资源可从中看出）与命中规则。多资源调用放行时，命中规则列出参与判定的全部规则 / grant；拒绝时列出命中的 deny 规则与被拒的资源。不新增审计列。
- **授权请求。** AI 的 `request_permission` 与 opsctl 审批通道送达的 grant 请求格式不变（`<action>[:<resource-glob>]`），落库的 grant 按上述"每个资源分别覆盖"参与判定。

## 可读文件的参数（平台变更）

- **声明。** 扩展作者可把工具的某个字符串参数标记为"可读文件"。`describe()` 如实报告该标记；标记在非字符串参数上注册即失败（与现有 schema 校验同一时机）。
- **opsctl。** 前置：参数 `--<flag>` 标记为可读文件。动作：`opsctl exec <asset> -- <tool> --<flag>-file <path>`，或 `--<flag>-file -` 从 stdin 读取。结果：与 `--<flag> <文件内容>` 完全等价——策略分类、审批展示、grant 判定、审计都基于读入的内容。
- **失败。** 同时给出 `--<flag>` 与 `--<flag>-file` → 报错且不发送；文件不存在 / 不可读 → 报错且不发送，退出码非零；内容超过 16 MiB 或不是合法 UTF-8 文本 → 报错且不发送。
- **审批展示。** 超长参数值在审批弹窗中截断显示并标出总大小，可展开查看全文；批准覆盖的是完整内容。审计照旧截断保存（4096 字节）。
- **其他入口。** AI 的 `exec` 与扩展页面不接受 `--<flag>-file`（按未知参数拒绝）。`opsctl help <asset>` 的参数表为此类参数标出 `-file` 形式及"仅 opsctl"。

## 官方扩展仓（`../extensions`）

- 删除 `sdk/go`、`examples/echo`、`extensions/oss` 及其说明；远端旧分支不动。
- `README.md`、`README_zh.md` 改写为：仓库定位（官方扩展源码）、目录约定、构建 / 测试 / 本地安装（`opsctl ext dev`）步骤；SDK 用法指向 opskat 仓 `extensions/README.md`，不在此仓复述。
- `make build EXT=<name>` 产出 `extensions/<name>/dist/`，该目录可直接经"从目录安装"或 `opsctl ext dev` 安装。
- 扩展依赖 `github.com/opskat/opskat/pkg/extsdk`，固定到包含本 spec 平台变更的 opskat 版本。ES 扩展声明的 hostABI 与最低应用版本取该版本；不支持的旧应用按现有规则拒绝加载并列出其支持的 hostABI。
- CI：PR 与主分支推送时对每个扩展运行 Go 测试、WASM 构建、前端类型检查与构建；任一失败即 CI 失败。发布产物与索引留给商店 spec。

## Elasticsearch 资产类型

- **配置字段。**
  - 地址（必填，endpoint，如 `https://es.example:9200`，可带路径前缀）。
  - 认证方式：无 / Basic / API Key / Bearer Token。
  - Basic：用户名 + 密码；API Key：ES 生成的 encoded 值；Bearer Token：令牌。三种密钥均为密码字段，宿主加密保存。
- **连接。** 声明 SSH 隧道、代理链、TLS（启用 / 跳过校验 / ServerName / CA / 客户端证书与私钥），由宿主在拨号时应用。
- **凭据注入。** 按认证方式由宿主注入：Basic → `Authorization: Basic`；API Key → `Authorization: ApiKey <encoded>`；Bearer → `Authorization: Bearer <token>`；无 → 不注入。扩展不声明 `credentials:read`。
- **校验。** 前置：保存资产。结果：地址不是带主机的 http(s) URL、Basic 缺用户名、API Key / Bearer 缺对应密钥时，表单在对应字段显示错误且不保存（编辑已保存资产时，未改动的已存密钥视为已填）。
- **测试连接。** 请求 `GET /`。2xx → 成功；401 / 403 → "认证失败"并附 ES 返回的原因；其余状态或传输失败 → 失败并附原因。

## Elasticsearch 工具（AI / opsctl）

经 `exec <asset> -- <tool>` 调用；AI 的技能文案（SKILL.md）说明各工具用途、分类规则与规则写法示例（如 `deny delete:prod-*`），并引导优先用便捷工具。

| 工具 | 参数 | 结果 | 分类 |
|---|---|---|---|
| `request` | `--method`、`--path`（以 `/` 开头的路径与查询串，不含协议与主机）、`--body`（可读文件） | `{status, body}`；body 为 JSON 时解析，否则为文本 | 见下节 |
| `health` | 无 | 集群名、版本、状态、节点数、分片统计（活动 / 主 / 未分配） | `read`，资源 `_cluster` |
| `indices` | `--pattern`（默认全部）、`--include-hidden` | 索引名、健康、状态、文档数、存储大小 | `read`，资源为 pattern |
| `mapping` | `--index` | 该索引 mapping | `read`，资源为 index |
| `search` | `--index`、`--query`（DSL JSON）或 `--q`（查询串）、`--sort`、`--size`（默认 10，上限 100） | 命中总数、耗时、每条命中的 `_index` / `_id` / `_source` | `read`，资源为 index |

- `request` 的路径含协议或主机时直接拒绝；请求总是发往资产地址。
- `health` / `indices` / `mapping` / `search` 遇 ES 返回错误时失败，错误信息含状态码与 ES 的 `type` / `reason`；opsctl 以非零退出码结束。

## Elasticsearch 策略面

- **动作。** `read`、`write`、`delete`、`admin`。
- **分类（`request`）。**
  - `read`：GET / HEAD；以及 POST 的检索类 API（`_search`、`_msearch`、`_count`、`_mget`、`_field_caps`、`_validate`、`_explain`、`_termvectors`、`_mtermvectors`、`_sql`、`_eql`、`_async_search`、search template、point-in-time 与 scroll，含清除 scroll / 关闭 point-in-time）。
  - `write`：文档写入（`_doc`、`_create`、`_update`、`_bulk`、`_update_by_query`）。
  - `delete`：删除文档、删除索引、`_delete_by_query`。
  - `admin`：其余全部，包括建索引、改 mapping / settings、别名、open / close、refresh / flush / forcemerge、`_reindex`、rollover / shrink / split / clone、模板、ILM、ingest pipeline、快照、`_cluster/settings`、`_security`、任务取消等；**无法识别的请求按 `admin` 处理**。
- **资源。**
  - 作用于索引的 API：路径中的索引表达式按逗号拆成多个资源；`_bulk`、`_mget`、`_msearch` 还收集请求体里指定的索引；`_reindex` 取源与目标索引。
  - 作用于索引但未指定索引、或为 `_all`：资源为 `*`。通配表达式（如 `logs-*`）原样作为通配资源。
  - 不作用于索引的集群级 API：资源为其首段（`_cluster`、`_nodes`、`_snapshot`、`_security`、`_ilm`、`_ingest` 等）。ES 索引名不能以 `_` 开头，二者不会混淆。
- **权限组。** "只读"（allow `read`），新资产默认授予；"读写"（allow `read`、`write`）。`delete` 与 `admin` 未被规则放行时一律询问。

## Elasticsearch 页面

打开 ES 资产即打开该页面（占一个资产标签）。页面内所有请求直接执行（见"扩展页面调用"），界面跟随宿主主题与语言（en / zh-CN）。布局参照 mockup `.dev-kit/artifacts/2026-09-29-es-extension/mockups/`（本地参考，非约束）。

- **侧栏。**
  - 顶部：集群名、健康徽标（green / yellow / red）、版本、节点数、索引数、刷新按钮。
  - 索引列表：过滤框（按名称子串）、"系统索引"开关（默认隐藏 `.` 开头的索引）、每行健康点 + 名称 + 文档数 + 大小，悬停显示全名。点击索引打开（或切到）该索引的标签。
- **标签。** "概览"常驻不可关；索引标签与控制台标签可关；"+ 控制台"新开控制台。控制台标签与其内容按资产保存在扩展存储中，重新打开资产时恢复；索引标签不恢复。
- **概览。** 集群状态、节点数与主节点、分片（活动 / 总数、主分片、未分配）、文档数与存储（不含系统索引）；节点表：名称、IP、角色、主节点标记、堆内存 / CPU / 磁盘占用；集群非 green 时列出非 green 的索引与原因提示。
- **索引标签。** 顶部显示主分片数、副本数、文档数、大小；子标签"文档 / Mapping / Settings"。
  - 文档：查询方式可切换"查询串 / DSL"（DSL 用 JSON 编辑器，提供与控制台请求体相同的自动提示），可填排序（`字段:asc|desc`）；结果表列为 `_id` 与当前页文档的顶层 `_source` 字段（嵌套值显示为 JSON 文本）；点行打开文档详情（完整命中 JSON 树）——内容区宽时与表格并排，窄时浮在表格之上；底栏显示命中总数与耗时、每页 20 / 50 / 100（默认 50）、翻页；超出索引结果窗口（默认 10000 条）的页不可达并提示原因。查询有误时在结果区显示 ES 的错误。空索引显示空态。
  - Mapping / Settings：只读 JSON 树。
- **控制台。** 左侧编辑器（宿主的 Monaco 编辑器）、右侧响应，中间可拖动。编辑器中每个请求以 `METHOD /path` 行开头，其后到下一个请求行之前为请求体（JSON，或 `_bulk` 等的多行 NDJSON）；`#` 开头为注释。
  - 语法高亮：区分方法、路径、JSON 请求体与注释。
  - 自动提示（随输入弹出，也可手动触发）：行首提示方法（GET / POST / PUT / DELETE / HEAD）；方法后的路径提示常用 API（如 `_search`、`_count`、`_doc`、`_bulk`、`_mapping`、`_settings`、`_cat/indices`、`_cat/nodes`、`_cluster/health`、`_aliases`）与集群中真实的索引名、别名；请求体内提示常用 DSL 键（如 `query`、`bool`、`must`、`filter`、`should`、`must_not`、`match`、`match_phrase`、`term`、`terms`、`range`、`exists`、`wildcard`、`query_string`、`aggs`、常用聚合类型、`sort`、`size`、`from`、`_source`、`track_total_hits`、`highlight`）以及请求行所指索引的 mapping 字段名（含嵌套字段的点路径）。提示不区分具体 API 的请求体结构。
  - 提示所需的索引 / 别名 / mapping 取自页面已加载的数据，按需加载并在刷新时更新；取不到时只缺少这部分提示，不报错、不阻塞输入。
  - ⌘/Ctrl+Enter 或"执行"按钮执行光标所在的请求，顶栏显示该请求。响应区显示状态码（2xx / 非 2xx 区分颜色）、耗时、大小，响应体可在树形与原文间切换；ES 返回 4xx / 5xx 时原样显示状态与错误体；传输失败显示错误信息。
- **状态。** 首次加载显示骨架；连不上或认证失败时整页显示错误原因与"重试"。

## Out of scope

- 按具体 API 细分请求体结构的自动提示（Kibana 级 schema 提示）。
- 建删索引、mapping / settings 编辑、别名、ILM、快照等管理界面（可经控制台直接发请求）。
- OpenSearch。
- 扩展发布产物、签名、商店索引与在线安装 → 商店 spec。
- 扩展页面与宿主的 JS 隔离（#326 已知遗留，单独立项）。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `internal/extreg` + `internal/ai/permission` | 多资源 deny / allow / grant 判定；通配资源的"可能重叠即 deny、完整覆盖才 allow"；单资源行为不变；grant 按资源分别覆盖；记住预填的公共前缀规则与回退（预填由后端计算，未改动时按它落库） | `extreg_rules_test.go`、`grant_request_ext_test.go` |
| `pkg/extsdk` + `pkg/extension` describe 校验 | 多资源分类与可读文件标记经 `describe()` / `check_policy` 往返；非字符串参数标记被拒 | `registry_test.go`、`describe_test.go`、`descriptor_test.go` |
| `cmd/opsctl/command` | `--<flag>-file` 读文件 / stdin 等价于内联；互斥、缺文件、超限、非 UTF-8 报错且不发送；AI 路径不接受 `-file` | `args_test.go` |
| `internal/app/extension` | 页面调用不经策略 / 审批 / 审计但仍带资产作用域；可取消 | `host_test.go`、`tool_cancel_test.go` |
| 前端 vitest（opskat） | 多资源审批列出全部资源；超长参数截断显示与展开 | `ApprovalBlock.test.tsx`、`OpsctlApprovalDialogExtension.test.tsx` |
| ES 扩展 Go 测试（`../extensions`） | 分类表：方法 × 路径 × 请求体 → 动作 + 资源集合；各工具经 `TestHost` + mock HTTP 的结果与错误 | notebook 的 `notebook_test.go` |
| ES 页面 vitest（`../extensions`） | 控制台请求切分（注释、NDJSON、光标定位）；按光标上下文给出的提示集合（行首 / 路径 / 请求体，含索引名与 mapping 字段）；文档表列推导 | 无 |
| e2e（opskat） | 页面调用不再弹审批（改写现有"页面调用触发审批弹窗"用例） | `e2e/tests/extension-asset-type.spec.ts` |

无法自动化、由收尾阶段在沙箱人工驱动验证：在 docker.internal 上临时起 ES 7.17 与 8.19（8.19 开安全与自签证书），经表单建资产（Basic / API Key、TLS CA、SSH 隧道），测试连接；页面浏览概览 / 索引 / 文档 / Mapping / 控制台（含自动提示、ES 错误与连接失败）；AI 与 opsctl 的 `exec` 在多索引请求上的 deny / allow / 审批与记住预填；`opsctl exec … --body-file` 与 stdin 的 bulk。验证完删除临时容器。

## Open questions

