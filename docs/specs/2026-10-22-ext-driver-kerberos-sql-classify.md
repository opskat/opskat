# 扩展驱动的 Kerberos 认证与语句切分分类（扩展平台能力第三轮）

> Status: Approved
> Owner: OpsKat 维护者
> Last updated: 2026-10-22

**Objective:** 扩展提供的数据库驱动可以用资产里保存的 Kerberos 凭据登录数据库（宿主经资产的到达途径去 KDC 取票，驱动只拿到票据），并可以自己切分与分类 SQL 语句，AI / opsctl 的权限判断与查询面板的「当前语句」和危险确认都按驱动的结果工作；Oracle 的 Kerberos 登录与 Oracle 语句的 AI 执行因此能由第三步的扩展支持。

**Hard invariant:** 四个内置驱动的连接、认证与语句判断（AI / opsctl 用 TiDB 解析器、查询面板按分号切分）、已保存的数据库资产配置、不声明新能力的扩展驱动、查询面板对 UPDATE / DELETE 等语句的危险确认框都不回退；口令、keytab 与 Kerberos 会话密钥不交给扩展。

## 所处位置

本规格是 issue #329（Oracle 数据库支持）三步路线第二步「扩展平台能力」的第三轮，也是最后一轮：**[用户决定]** 原计划分开的「宿主代取 Kerberos 票据」与「驱动提供 SQL 语句分类」合成一轮，共用一次宿主 ABI 升级。前两轮：「扩展提供数据库驱动」（[2026-10-19-ext-database-driver](2026-10-19-ext-database-driver.md)，PR #347）与「扩展驱动的重定向拨号与证书 TLS」（[2026-10-21-ext-driver-redirect-tls](2026-10-21-ext-driver-redirect-tls.md)，PR #348），均已合并。之后是第三步：在 extensions 仓库做 Oracle 扩展。

## Problem

标注：**[已核实]** = 对照 opskat main（`94fe30b8`）或 go-ora v2.9.0 / gokrb5 v8.4.4 源码确认；**[实测]** = 2026-10-10 用 go-ora 连 docker.internal 上 Oracle 11.2~23ai 容器时观察到；**[用户决定]** = 本轮对话中用户选定；**[未知]** = 只能在真机上确认。

1. **扩展驱动没有办法做 Kerberos 登录。** [已核实] go-ora 不在打开连接时要票据，而是在握手中途收到服务器给的服务名与主机名后，调用 `Authenticate(主机名, 服务名)` 要票据（go-ora `advanced_nego/advanced_nego.go:297-311`），所以票据只能在握手过程中按需取得。数据库资产没有任何 Kerberos 字段（`internal/model/entity/asset_entity/asset.go:176-207`），扩展也没有取票的宿主调用。
2. **扩展里取不到票，宿主现成的客户端也到不了隧道后面的 KDC。** [已核实] gokrb5 的客户端只用 `net.DialTimeout` 直拨 KDC（gokrb5 `client/network.go:94,158`），不能换拨号器；扩展里的标准库网络拨号不经宿主，在 WASM 中不可用。KDC 通常与数据库在同一内网，用户往往只能经 SSH 跳板到达。
3. **AI / opsctl 用 MySQL 语法判断所有驱动的 SQL。** [已核实] AI 与 opsctl 执行 SQL 前的权限检查、策略测试、「总是允许」落成规则，都用 TiDB（MySQL）解析器切分并分类语句（`internal/ai/policy/query_policy.go:24-40`，调用处 `internal/ai/permission/permission.go:118`、`internal/ai/permission/rule_persist.go:585`、`internal/ai/policy/policy_tester.go:222`），解析失败即拒绝执行。[实测] 20 条典型 Oracle 语句中 10 条解析失败。
4. **查询面板按分号切语句，切碎 PL/SQL 块。** [已核实] 查询面板的当前语句高亮、「执行当前语句」与危险确认用前端与方言无关的切分，只认引号、注释、`$tag$` 块与分号（`frontend/src/lib/sqlStatements.ts`）；Oracle 的 PL/SQL 块内部含分号。[实测] Oracle 的 PL/SQL 块必须以 `;` 结尾，11g~21c 拒绝普通 SQL 末尾的分号，一次调用只能执行一条语句。

## Actors and user stories

1. 作为**运维用户**，我想在 Oracle 资产里填 Kerberos 主体与口令（或 keytab）登录数据库，KDC 只能经 SSH 跳板到达时也能用，并且口令与 keytab 不交给扩展。
2. 作为**运维用户**，我想让 AI 与 opsctl 能执行 Oracle 的查询、MERGE、PL/SQL 块，并照我配置的语句类型策略放行、确认或拒绝，而不是一律报「SQL 解析失败」。
3. 作为**运维用户**，我想在查询面板里把光标放在一个 PL/SQL 块中按「执行当前语句」就执行整个块，危险语句照旧先弹确认。
4. 作为**运维用户**，我想在安装扩展时看清它会用资产的 Kerberos 凭据取票。
5. 作为**扩展作者**，我想只实现取票回调与一个语句切分分类函数，不必在扩展里实现 Kerberos 协议或 KDC 网络访问。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **[用户决定]** Kerberos 凭据填在资产里：主体、口令或 keytab 文件路径、KDC 地址，可选服务主体；宿主自己去 KDC 取票 | 各平台与 opsctl 行为一致。Rejected: 读本机 `kinit` 的票据缓存与 `/etc/krb5.conf`——gokrb5 只能读文件形式的缓存，macOS 默认的 `API:` 缓存与 Windows 的系统缓存都读不了，实际只有 Linux 能用；两种都支持——工作量与验证量约多一半 |
| 2 | **[用户决定]** 宿主经资产的到达途径（直连、SSH 隧道、代理、代理链）连 KDC，只用 TCP | KDC 通常与数据库同在内网、只能经跳板到达；SSH 隧道不传 UDP。Rejected: 只从本机直连 KDC——KDC 在跳板后面时取不到票 |
| 3 | **[用户决定]** 驱动要哪个服务主体（SPN）就给哪个，只用本资产的凭据、只在本资产的驱动会话里；资产可选填一个服务主体，填了就只给它发票 | Oracle 报的是服务器主机名，资产常填 IP，按地址限定会对不上而连不上。Rejected: SPN 主机限定为连过的地址——资产填 IP 时失败；必须填 SPN——用户得先知道数据库的 SPN |
| 4 | 只交票据：口令、keytab 与 Kerberos 会话密钥留在宿主 | 驱动登录只需要票据；拿不到凭据的扩展不能在宿主之外冒用用户身份长期取票。Rejected: 把口令或 keytab 交给驱动、由扩展自己实现 Kerberos——扩展里拨不了 KDC，且扩大了泄露面 |
| 5 | 使用 Kerberos 要在安全契约里声明，安装 / 升级确认单列说明 | 按决定 3，声明了跟随重定向又声明 Kerberos 的扩展理论上可以用户身份登录内网里其他用 Kerberos 的服务，用户应在安装时看到。Rejected: 只靠驱动描述的字段组声明（证书 TLS 的做法）——安装确认读不到驱动描述 |
| 6 | **[用户决定]** 驱动声明后提供不连数据库的「切分 + 分类」；AI / opsctl 的权限判断与查询面板都用它；内置驱动与未声明的驱动照旧 | 驱动最了解自己方言的语句边界与种类。Rejected: 宿主写一个通用词法分类器替换 TiDB——切不对 PL/SQL 块，还会改变内置驱动的行为；只改 AI / opsctl、面板照旧按分号切——PL/SQL 块在面板里被切碎 |
| 7 | 驱动输出的语句类型沿用现有类型名的含义，方言特有的语句可用驱动自己的类型名；危险原因只能取现有几种 | 组策略按类型名跨驱动匹配，同名必须同义；新类型名不在允许列表里时按现有规则需要确认，默认更保守。Rejected: 驱动任意命名——组里写的 `UPDATE` 对某个驱动可能失效 |
| 8 | Kerberos 与语句切分分类都要求本轮新增的宿主 ABI | 更早的宿主不认识这两项，必须在安装时拒绝，而不是装上后行为不同 |

## Kerberos 认证

- **声明。** 扩展在安全契约（`manifest.json`）的能力里声明使用数据库 Kerberos（`capabilities.databaseKerberos`），只对声明了 `databaseDrivers` 的扩展有意义；驱动描述在连接 Tab 声明使用 Kerberos 字段组。声明了字段组而契约未声明 Kerberos、契约声明了 Kerberos 而未声明驱动、或宿主 ABI 低于本轮新增的版本时，扩展加载失败并点名原因。
- **安装 / 升级确认。** 前提：要安装或升级的扩展声明了 Kerberos。动作：打开安装确认（桌面端安装 / 升级，以及 `opsctl ext dev` 的确认）。结果：能力列表里多一项，说明「驱动可以用资产里的 Kerberos 凭据为登录数据库取票」；升级时新增这项标为「新增」。
- **表单。** 前提：资产选了声明 Kerberos 字段组的驱动。结果：连接 Tab 多一个「认证方式」：口令（默认）或 Kerberos。选 Kerberos 时显示主体（`user@REALM`，realm 取自主体）、凭据（口令，或 keytab 文件路径）、KDC 地址（一个或多个 `host[:port]`，默认端口 88）、服务主体（可选）；资产口令字段隐藏且不必填，用户名照常显示、可留空。不声明的驱动表单不变。主体缺 `@REALM`、没填 KDC 地址、口令与 keytab 都没填时，保存与测试连接被拒并点名字段。
- **存储与展示。** Kerberos 设置存在资产配置里；Kerberos 口令与资产口令一样加密存储；keytab 只存路径。opsctl 与 AI 的创建 / 更新使用同名的配置键（认证方式与 `krb_*` 键），按驱动是否声明 Kerberos 字段组校验，未声明时被拒并点名键；Kerberos 口令以明文出现在命令行或配置文件里时计入明文告警，与资产口令一致；查看资产时 Kerberos 口令只显示为已设置。驱动不可用时编辑保存资产，Kerberos 字段原样保留。
- **打开会话。** 前提：资产用声明了 Kerberos 的扩展驱动，认证方式为 Kerberos。动作：打开会话（每个新实例登录时同样）。结果：驱动得知该资产使用 Kerberos，拿不到 Kerberos 口令、keytab 与资产口令；用户名照常交出。
- **取票。** 前提：上述会话的驱动在登录握手中按服务器给的服务名与主机名向宿主要票据。结果：
  - 宿主用该资产的主体与口令（或 keytab）向 KDC 取 TGT，再取该服务主体的服务票据，把登录用的票据（Kerberos AP-REQ）交给驱动；
  - 连 KDC 经该资产的到达途径，只用 TCP；经隧道、代理或代理链时 KDC 主机名在远端解析；多个 KDC 地址按顺序尝试，第一个连上的为准；
  - 桌面端按资产缓存 TGT 与服务票据直到过期，同一资产同时登录的多个实例只取一次；资产的 Kerberos 设置改动后缓存作废；opsctl 每次运行重新取；
  - 每条会话每个服务主体第一次发票时记一条日志：扩展、驱动、资产、主体、服务主体；日志与错误里不出现口令、keytab 内容、票据或密钥。
- **拒绝与失败。** 都不回退到口令登录：
  - 资产认证方式不是 Kerberos、调用不在该资产的驱动会话里：宿主拒绝并说明原因；
  - 资产填了服务主体而驱动要的是别的：宿主拒绝，错误点名两个服务主体；
  - KDC 都连不上：错误点名尝试过的 KDC 地址；
  - KDC 拒绝（口令或 keytab 不对、主体不存在、服务主体不存在、时钟偏差过大等）：错误给出可读的原因与 KDC 的错误名；
  - keytab 文件读不到或其中没有该主体的密钥：错误点名字段（不含内容）；
  - 驱动拿到票据后数据库拒绝登录：错误给出驱动的报错。
- **范围。** 内置驱动、其他资产类型的认证不变。

## 语句切分与分类

- **声明。** 驱动描述可以声明提供语句切分与分类（宿主 ABI 低于本轮新增版本时扩展加载失败并点名）。声明了的驱动提供一个不连数据库的函数：输入一段 SQL，输出其中每条语句的起止位置、原文、类型、是否危险及危险原因；SQL 无法切分时返回错误与原因。
- **输出的约束。** 类型名大写；现有类型名（如 `SELECT`、`INSERT`、`UPDATE`、`DELETE`、`DROP TABLE`、`TRUNCATE`、`CREATE TABLE`、`ALTER TABLE`、`GRANT`、`CALL`、`EXPLAIN`）只用于同种语句；没有对应现有类型名的语句（如 Oracle 的 `MERGE`、PL/SQL 块）可以用驱动自己的类型名。危险原因只能是现有的四种：不带 WHERE 的 UPDATE、不带 WHERE 的 DELETE、预编译执行、调用存储过程。起止位置必须落在输入之内、按顺序且不重叠。不合约束的输出按「无法切分」处理，错误点名驱动与违反的约束。
- **AI / opsctl。** 前提：数据库资产的驱动声明了切分分类。动作：AI 或 opsctl 对该资产执行 SQL、策略测试（指定了该资产时）、或把一次审批的「总是允许」落成规则。结果：按驱动输出的语句逐条套用组策略、资产策略与授权，规则与现在相同；驱动自己的类型名不在允许列表里时（允许列表含 `*` 除外）需要确认；驱动返回错误时拒绝执行，消息带上驱动给的原因；驱动不可用（扩展停用或卸载）时拒绝执行并说明驱动不可用。
- **查询面板。** 前提：资产的驱动声明了切分分类。结果：
  - 编辑时按当前文本向驱动取切分结果（输入停顿后取，不在每次按键时取），当前语句高亮与「可执行」状态按它显示；
  - 「执行当前语句」执行光标所在的那条驱动切出的语句（PL/SQL 块整体执行）；选中文本与「执行全部」行为不变；执行时按执行那一刻的文本重新取切分；
  - 危险确认的判定与现在相同——任一语句是 DELETE、DROP、TRUNCATE、ALTER 类，或是不带 WHERE 的 UPDATE——只是语句与类型取自驱动；确认框照旧弹出；
  - 驱动返回错误或不可用时，面板提示原因、不执行，不退回按分号切分。
- **未声明的驱动与内置驱动。** AI / opsctl 照旧用 TiDB 解析器，查询面板照旧按分号切分；只有组、没有资产的策略测试同样照旧。

## 宿主 ABI

本轮新增一个宿主 ABI 小版本（2.4 → 2.5）；Kerberos 与语句切分分类两项声明都要求它。2.0~2.4 的扩展照旧受支持。

## Out of scope

- 内置 SQL Server、PostgreSQL、MySQL 的 Kerberos 认证：另开一轮。
- 读本机 `kinit` 票据缓存、`krb5.conf` 的高级选项（加密类型、跨 realm 信任、DNS 查找 KDC）、经 UDP 连 KDC（设计决定 1、2 的被否方案）。
- 粘贴 keytab 内容存进资产：本轮只填路径。
- Kerberos 口令引用统一凭证（资产口令的「已保存凭证」）：本轮只能直接填。
- 查询面板在切分之外的方言能力（补全、格式化、语法高亮）。
- 改变内置驱动的语句判断（设计决定 6 的被否方案）。
- Oracle 扩展本身、go-ora 在 wasip1 下的补丁：第三步。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 取票的宿主调用 | 只在 Kerberos 资产的驱动会话里可用；固定服务主体时拒绝别的；每会话每服务主体记一次日志；口令 / keytab / 票据不进日志；失败不回退 | `pkg/extension` 驱动会话测试、夹具扩展 `testdata/fixture-dbdriver` |
| KDC 收发 | 经给定的拨号途径连 KDC、多地址顺序尝试、TGT 与服务票据缓存与作废、KDC 错误转成可读原因 | 无；用进程内的假 KDC 做报文往返，可行性在计划阶段确认，做不到则只由运行时验证覆盖 |
| 声明与安装确认 | 契约 / 字段组 / ABI 的加载校验；安装 / 升级确认与 `ext dev` 列出 Kerberos 及新增标记 | `pkg/extension` manifest 与 descriptor 测试、`internal/app/opsctl/ext_dev_test.go`、安装确认的 vitest |
| Kerberos 字段 | 表单在声明的驱动上显示认证方式与 Kerberos 字段、校验点名字段；opsctl / AI 的键校验、加密、脱敏与明文告警 | 数据库资产表单 vitest、`cmd/opsctl/command` 的建资产测试 |
| 切分分类契约 | 声明了的夹具驱动的输出被 AI / opsctl 权限判断、策略测试、落规则采用；不合约束的输出与驱动错误导致拒绝；内置驱动与未声明的驱动结果不变 | `internal/ai/policy`、`internal/ai/permission` 现有测试 |
| 查询面板 | 声明了的驱动按其切分高亮、执行当前语句、危险确认；错误时不执行；未声明时照旧 | `SqlEditorTab` 与 `sqlStatements` 的 vitest |

不能自动化、由收尾时的运行时验证覆盖：

- **[用户决定]** 真实的 Kerberos 登录：在 docker.internal 起一个 MIT KDC 容器，把 Oracle 19c 容器配成 Kerberos 认证（用户同意验证时进行，验证后复原并停止）；用验证目录里的 go-ora 最小扩展（不提交、不发布，补上 wasip1 下读本机地址的补丁）分别在 KDC 直连可达与只经 SSH 隧道可达两种情况下登录；口令与 keytab 两种凭据；错误口令、错误服务主体、KDC 不可达的报错。
- 真实的 Oracle 语句：同一扩展带一个最小的 Oracle 切分分类，经 opsctl / AI 执行典型 Oracle 语句看放行、确认与拒绝；查询面板里执行 PL/SQL 块与危险确认。
- **[未知]** Oracle 对票据的具体要求（AP-REQ 的校验和与标志）只能在真机上确认；若与宿主默认的取法不同，按真机结果调整宿主的取法，不改变本规格的承诺。

## Open questions

