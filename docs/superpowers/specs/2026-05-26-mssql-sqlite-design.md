# 新增 SQL Server 与 SQLite 数据库资产支持 — 设计

- 状态：草案
- 日期：2026-05-26
- 范围：在现有 `AssetTypeDatabase` 下新增 `mssql` / `sqlite` 两个 driver；SQLite 仅本地文件，不支持远程 / SSH 隧道。
- 不在范围内：Oracle 支持、MSSQL Windows AD / Azure AD 认证、MSSQL 连接级只读、SQLite 加密、SQLite 远程文件、AI 方言专属 policy、DialectAdapter 接口重构。

## 1. 背景与目标

OpsKat 当前数据库资产只支持 MySQL 与 PostgreSQL（见 `internal/model/entity/asset_entity/asset.go` 的 `DriverMySQL` / `DriverPostgreSQL`）。本期新增两类常见远程数据库 / 本地嵌入式数据库支持，使运维场景能覆盖 SQL Server 生产库与本地 SQLite 文件查询。

设计原则：

- 复用现有 `DatabaseConfig` 与 `DialDatabase` 入口，不抽 DialectAdapter 接口（YAGNI，等加 Oracle 时再说，详见 §10.2）。
- SQLite 复用 `AssetTypeDatabase`，新增 `Path` 字段，driver=sqlite 时跳过 host/port/user/pass。
- MSSQL 只支持 SQL 认证；连接级只读 no-op，仅靠现有查询面板 / AI policy 拦截写操作。
- 遵循 CLAUDE.md 的"Fix policy — TDD"、"关键流程要打日志"、"Reuse first — grep before writing"。

## 2. 关键决策汇总

| 维度 | 决策 |
|---|---|
| SQLite 数据模型 | 复用 `AssetTypeDatabase`，`DatabaseConfig` 加 `Path string` 字段 |
| MSSQL 认证 | 只 SQL Auth（用户名/密码），走现有 credential_svc |
| ReadOnly 处理 | SQLite `PRAGMA query_only=1`；MSSQL no-op |
| 高级功能（OpenTable/导入/FK） | 两个驱动都完整适配 |
| MSSQL 驱动 | `github.com/microsoft/go-mssqldb`（纯 Go，无 CGO） |
| SQLite 驱动 | `modernc.org/sqlite`（纯 Go，无 CGO） |

## 3. 架构与触点

新增 driver 常量（`internal/model/entity/asset_entity/asset.go`）：

```go
DriverMySQL      DatabaseDriver = "mysql"
DriverPostgreSQL DatabaseDriver = "postgresql"
DriverMSSQL      DatabaseDriver = "mssql"     // 新增
DriverSQLite     DatabaseDriver = "sqlite"    // 新增
```

`DefaultPort()`：MSSQL=1433；SQLite=0。

改动文件清单：

| 层 | 文件 | 改动性质 |
|---|---|---|
| Model | `internal/model/entity/asset_entity/asset.go` | 加 driver 常量、`DatabaseConfig.Path` 字段、`Validate()` 按 driver 分支、`DefaultPort()` |
| AssetType | `internal/assettype/database.go` | `ValidateCreateArgs` / `ApplyCreateArgs` / `ApplyUpdateArgs` 按 driver 分支处理 path |
| ConnPool | `internal/connpool/database.go` | `buildDSN` 加 mssql/sqlite case；`openWithTunnel` 加 mssql case；`setReadOnly` 加 sqlite case；`DialDatabase` 在 SQLite 时跳过 tunnel 分支 |
| QuerySvc | `internal/service/query_svc/{quote,open_table,table_import}.go` | 加 MSSQL `[bracket]` 引号、SQLite `"double"` 引号；OpenTable 的 PK/Columns 加 MSSQL/SQLite 分支；TableImport 的 FK 关闭分驱动实现 |
| Frontend | `frontend/src/components/asset/{DatabaseConfigSection,AssetForm}.tsx`、`frontend/src/stores/queryStore.ts` | driver 下拉加 MSSQL/SQLite；SQLite 切到文件路径表单；端口/图标默认值 |
| App / IPC | `internal/app/<file_dialog 相关>.go` | SQLite 文件选择需要的本地文件对话框（如已有则复用） |
| 依赖 | `go.mod` | 新增 `github.com/microsoft/go-mssqldb`、`modernc.org/sqlite` |
| i18n | `frontend/src/i18n/locales/{zh-CN,en}/common.json` | driver 选项 label、SQLite path 字段提示、MSSQL ReadOnly hint |

作用边界：每个新驱动 ≤ 2 个新文件（驱动 import 不算）、≤ 5 处现有方言 switch 加 case。无新增接口 / 抽象。

## 4. 数据模型

```go
const (
    DriverMySQL      DatabaseDriver = "mysql"
    DriverPostgreSQL DatabaseDriver = "postgresql"
    DriverMSSQL      DatabaseDriver = "mssql"
    DriverSQLite     DatabaseDriver = "sqlite"
)

type DatabaseConfig struct {
    // ... 现有字段不变 ...
    Path string `json:"path,omitempty"` // 新增：仅 SQLite 使用，本地文件绝对路径
}

func (cfg *DatabaseConfig) Validate() error {
    if cfg.Driver == "" {
        return fmt.Errorf("driver 不能为空")
    }
    switch cfg.Driver {
    case DriverMySQL, DriverPostgreSQL, DriverMSSQL:
        if cfg.Host == "" {
            return fmt.Errorf("host 不能为空")
        }
        if cfg.Port <= 0 {
            return fmt.Errorf("port 必须 > 0")
        }
        if cfg.Username == "" {
            return fmt.Errorf("username 不能为空")
        }
    case DriverSQLite:
        if cfg.Path == "" {
            return fmt.Errorf("SQLite 必须指定 path")
        }
        if !filepath.IsAbs(cfg.Path) {
            return fmt.Errorf("SQLite path 必须为绝对路径")
        }
        // SQLite 不允许 SSH 隧道
    default:
        return fmt.Errorf("不支持的数据库驱动: %s", cfg.Driver)
    }
    return nil
}
```

约束：

- `Path` 字段对 MySQL/PG/MSSQL 永远为空（`omitempty`）。
- 路径要求绝对路径，避免相对路径在不同 CWD 下解析到不同文件造成安全/数据混淆。
- SQLite 不允许 `SSHAssetID > 0`——`Validate` 里拒掉。
- 不引入 SQLCipher / 加密。`Password` 字段对 SQLite 永远为空。

SafeView（`internal/assettype/database.go`）：SQLite 时返回 `{driver, path}` 不返回 host/port/username。

迁移：不需要数据迁移。`Path` 是新字段，老 JSON 反序列化为空字符串；现有 MySQL/PG 资产读出来 `Path=""` 不参与任何逻辑。

## 5. 连接池（connpool）

新依赖 import：

```go
_ "github.com/microsoft/go-mssqldb"  // mssql 驱动，注册 "sqlserver"
_ "modernc.org/sqlite"               // sqlite 驱动，注册 "sqlite"
```

`buildDSN` 加两个 case：

```go
case asset_entity.DriverMSSQL:
    q := url.Values{}
    q.Set("database", cfg.Database)
    if cfg.TLS {
        q.Set("encrypt", "true")
        q.Set("trustservercertificate", "true") // 与 MySQL TLS skip-verify 行为对齐
    } else {
        q.Set("encrypt", "disable")
    }
    if cfg.Params != "" {
        for k, vs := range parseParams(cfg.Params) {
            q.Set(k, vs)
        }
    }
    u := &url.URL{
        Scheme:   "sqlserver",
        User:     url.UserPassword(cfg.Username, password),
        Host:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
        RawQuery: q.Encode(),
    }
    return "sqlserver", u.String()

case asset_entity.DriverSQLite:
    dsn := "file:" + cfg.Path
    if cfg.Params != "" {
        dsn += "?" + cfg.Params
    }
    return "sqlite", dsn
```

`openWithTunnel` 加 MSSQL case，SQLite 不走此分支：

```go
func openWithTunnel(...) (*sql.DB, error) {
    switch cfg.Driver {
    case asset_entity.DriverMySQL:      return openMySQLWithTunnel(...)
    case asset_entity.DriverPostgreSQL: return openPgWithTunnel(...)
    case asset_entity.DriverMSSQL:      return openMSSQLWithTunnel(...)
    case asset_entity.DriverSQLite:
        return nil, fmt.Errorf("SQLite 不支持 SSH 隧道") // 防御性，UI/Validate 已拦
    }
}
```

MSSQL 隧道实现：用 `go-mssqldb` 的 `mssql.NewConnectorConfig(msdsn.Config)` API，注入 `Config.Dialer = func(ctx, network, addr) { return tunnel.Dial(ctx) }`，再 `sql.OpenDB(connector)`。

`DialDatabase` 入口加 SQLite 短路：在隧道判断之前，若 driver=sqlite 直接 `openDirect(cfg, "")`，不进 tunnel 分支、不要求 password。

`setReadOnly` 加 SQLite case，MSSQL 显式 no-op：

```go
case asset_entity.DriverSQLite:
    _, err := db.ExecContext(ctx, "PRAGMA query_only = 1")
    return err
case asset_entity.DriverMSSQL:
    logger.Ctx(ctx).Info("MSSQL connection-level read-only not supported, relying on policy",
        zap.Int64("assetID", assetID))
    return nil
```

## 6. query_svc 方言适配

### 6.1 标识符引号（`quote.go`）

```go
func QuoteIdent(name string, driver asset_entity.DatabaseDriver) string {
    switch driver {
    case asset_entity.DriverPostgreSQL, asset_entity.DriverSQLite:
        return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
    case asset_entity.DriverMSSQL:
        return `[` + strings.ReplaceAll(name, `]`, `]]`) + `]`
    default: // MySQL
        return "`" + strings.ReplaceAll(name, "`", "``") + "`"
    }
}
```

`QuoteTableRef` 同步：

- PostgreSQL / SQLite：忽略 database，按 schema.table 处理。
- MSSQL：`[db].[schema].[table]`，模型中 schema 缺失时退化为 `[db].[table]`。
- MySQL：`` `db`.`table` ``。

### 6.2 主键查询（`open_table.go::queryPrimaryKeys`）

```go
case asset_entity.DriverMSSQL:
    sqlText = `SELECT kcu.COLUMN_NAME
        FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
        JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu
          ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME
        WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY'
          AND tc.TABLE_CATALOG = @p1 AND tc.TABLE_NAME = @p2
        ORDER BY kcu.ORDINAL_POSITION`
    args = []any{sql.Named("p1", database), sql.Named("p2", table)}

case asset_entity.DriverSQLite:
    // pragma_table_info 是表值函数，参数必须内联字符串字面量（不能用 placeholder）
    // sqliteStringLiteral 是本期新增 helper：转义 ' 为 ''，加 ' 包裹
    sqlText = fmt.Sprintf("SELECT name FROM pragma_table_info(%s) WHERE pk > 0 ORDER BY pk",
        sqliteStringLiteral(table))
    args = nil
```

### 6.3 列元信息（`open_table.go::queryColumns`）

```go
case asset_entity.DriverMSSQL:
    sqlText = `SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT
        FROM INFORMATION_SCHEMA.COLUMNS
        WHERE TABLE_CATALOG = @p1 AND TABLE_NAME = @p2
        ORDER BY ORDINAL_POSITION`

case asset_entity.DriverSQLite:
    sqlText = fmt.Sprintf(
        "SELECT name, type, CASE notnull WHEN 0 THEN 'YES' ELSE 'NO' END, dflt_value FROM pragma_table_info(%s)",
        sqliteStringLiteral(table))
```

### 6.4 外键临时关闭（`table_import.go`）

```go
switch driver {
case asset_entity.DriverMySQL:
    // 现有：SET FOREIGN_KEY_CHECKS=0
case asset_entity.DriverPostgreSQL:
    // 现有：session_replication_role = replica（不变）
case asset_entity.DriverSQLite:
    // PRAGMA foreign_keys = OFF; 导入后 ON
case asset_entity.DriverMSSQL:
    // 每个表：ALTER TABLE <t> NOCHECK CONSTRAINT ALL; 导入后 CHECK CONSTRAINT ALL
}
```

MSSQL 的 NOCHECK 是表级而非 session 级，table_import 框架的 `disableFK` 签名若没传"涉及到的目标表列表"，需要加一层参数——这是本设计唯一可能要动 caller 的方言点。

### 6.5 参数占位符

| Driver | 占位符 |
|---|---|
| MySQL | `?` |
| SQLite | `?` |
| PostgreSQL | `$1, $2, ...` |
| MSSQL | `@p1, @p2, ...`（`sql.Named`） |

`open_table.go` 现有 PG 分支已有占位符差异处理；MSSQL 比照增加 helper `placeholders(driver, n int)`。

## 7. 前端

### 7.1 `DatabaseConfigSection.tsx`

driver 下拉新增 MSSQL / SQLite；按 driver 切换字段：

- SQLite：渲染 `SqliteFileField`（文本输入 + "选择文件…" 按钮，调用新增 App binding 包装 `wailsRuntime.OpenFileDialog`，filter `.db,.sqlite,.sqlite3` 返回绝对路径——`wailsRuntime.OpenFileDialog` 已在 `internal/app/system/settings.go` 与 extension 模块用过，本期为资产表单加一个专用 binding 如 `App.SelectSQLiteFile()`），不渲染 host/port/user/pass。
- MSSQL：渲染常规 host/port/user/pass + Database + TLS 开关（语义对应 `encrypt=true&trustservercertificate=true`）。
- MySQL / PostgreSQL：保持现状。

### 7.2 `AssetForm.tsx`

- `DEFAULT_PORTS` 加 `mssql: 1433`；sqlite 不进端口逻辑。
- `DEFAULT_ICONS` 加 `mssql / sqlite`（lucide 兜底或 IconPicker 现有库，不新增 svg）。
- `resetSharedFields` / `onTypeChange` / `onDriverChange` 切换 sqlite 时清空 host/port/user/pass，启用 path。
- 序列化（约 936 行）：

```ts
const cfg: DatabaseConfig = { driver };
if (driver === "sqlite") {
  cfg.path = path;
} else {
  cfg.host = host; cfg.port = port; cfg.username = username;
  if (driver === "postgresql" && sslMode !== "disable") cfg.ssl_mode = sslMode;
  if ((driver === "mysql" || driver === "mssql") && tls) cfg.tls = tls;
}
```

- 编辑回填（约 546 行）：读 `cfg.path` 写入本地 state。
- 校验：sqlite 时 path 非空才允许提交。

### 7.3 `queryStore.ts`

注释更新：`driver?: string; // "mysql" | "postgresql" | "mssql" | "sqlite"`。第 412 / 461 行的 PG 专属分支保留，新驱动走 else 即可——除非测试发现 MSSQL 需要特殊处理（如三段式 db.schema.table），届时再补。

### 7.4 类型同步

`frontend/wailsjs/go/app/models.ts`（generated）会在 `wails build` / `wails dev` 时自动重新生成，新增 `Path` 字段。不要手改。

### 7.5 i18n

`frontend/src/i18n/locales/{zh-CN,en}/common.json` 新增 key：

- `asset.database.driver.mssql`：`"SQL Server"` / `"SQL Server"`
- `asset.database.driver.sqlite`：`"SQLite"` / `"SQLite"`
- `asset.database.sqlite.path.label`：`"数据库文件路径"` / `"Database file path"`
- `asset.database.sqlite.path.placeholder`：`"/Users/.../my.db"` / `"/Users/.../my.db"`
- `asset.database.sqlite.path.browse`：`"选择文件…"` / `"Choose file…"`
- `asset.database.mssql.readonly.hint`：`"SQL Server 仅通过策略拦截写操作，连接级不强制只读"` / `"SQL Server enforces read-only via policy only, not at the connection level"`

## 8. 错误处理与日志

按 CLAUDE.md "关键流程要打日志"：

- `DialDatabase` 开始 / 成功 / 失败三态日志，带 `driver, host(或 path), assetID, sshTunnelID`。SQLite 时 host 为空 → 改打 `path`（path 算半敏感的运维信息，不打 password）。
- `setReadOnly` MSSQL no-op 时每连接打一行 Info，便于排查为何写还能过。
- SQLite path 校验失败 / Validate 拒绝 SQLite + SSHTunnelID → IPC 边界返回明确错误（按 CLAUDE.md "Validate at boundaries only"）。
- 密码字段：MSSQL 走现有 `credential_svc.Encrypt`；SQLite 不需要密码字段。

按 CLAUDE.md "Don't swallow errors"：`logger.Ctx(ctx).Error(..., zap.Error(err))` 后照常 `return err`。

## 9. 测试策略

按 CLAUDE.md "Fix policy — TDD"，每个改动**失败测试先**：先写测试看到正确的失败信息，再写实现。

| 层 | 工具 | 是否进 CI |
|---|---|---|
| `quote_test.go` | 单元 + goconvey | ✅ |
| `open_table_test.go` | sqlmock | ✅ |
| `database_test.go`（assettype 验证） | 单元 | ✅ |
| connpool MSSQL 真连接 | docker `mcr.microsoft.com/mssql/server:2022-latest` | ❌ 本地手动 |
| connpool SQLite 真连接 | 临时文件，无依赖 | ✅ |
| 前端 `DatabaseConfigSection.test.tsx` | vitest + RTL | ✅ |
| 前端 `AssetForm.test.tsx` | vitest + RTL | ✅ |
| 手动 e2e | desktop app 连真 MSSQL/SQLite，跑 ad-hoc query + OpenTable + 一次 INSERT/DELETE 通过 policy 拦截 | 发版前 |

## 10. 风险与权衡

### 10.1 已识别风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| `go-mssqldb` 包体积 ~5MB | 二进制变大 | 接受，desktop 包本就 ~100MB |
| `modernc.org/sqlite` 性能比 CGO 版低 ~30% | 本地小库查询无感 | 接受；用户场景是 ops 查询不是 OLTP |
| MSSQL `INFORMATION_SCHEMA.TABLE_CATALOG` 行为依赖 cross-db 权限 | 普通用户可能查不到 PK | 文档化；OpenTable 查不到 PK 时退回到 ROWID 浏览（与 PG 兜底逻辑一致） |
| SQLite WAL 模式被并发写者锁 | 浏览/导入时阻塞 | 打开时 `PRAGMA busy_timeout=5000`；busy 错误透传 |
| `table_import.go` MSSQL NOCHECK 是表级 | 现有 disableFK 签名可能没传表清单 | 实现时若签名不够，加表清单参数；唯一可能动 caller 的方言点 |
| modernc.org/sqlite build tag | Windows arm64 / 老 Go 可能编译失败 | 项目 Go 1.25，平台 macOS / Win amd64+arm64 已知支持；CI 加 build matrix 验证 |

### 10.2 范围之外（明确不做）

- Oracle 支持
- MSSQL Windows AD / Azure AD 认证
- MSSQL 连接级 ReadOnly
- SQLite 加密（SQLCipher）
- SQLite 远程文件 / SSH 隧道 SQLite
- AI command_policy 方言专属规则（沿用现有 TiDB parser + fallback）
- DialectAdapter 接口重构（YAGNI，等加 Oracle 时再说）

## 11. 提交节奏（粒度建议）

按 gitmoji 拆 commit，正式 plan 阶段细化：

1. ✨ 加 driver 常量 + `DatabaseConfig.Path` + `Validate()` 分支（model 层，单测先）
2. 🔧 引入 `go-mssqldb`、`modernc.org/sqlite` 依赖
3. ✨ connpool MSSQL DSN + 直连 + 隧道
4. ✨ connpool SQLite DSN + `PRAGMA query_only`
5. ✨ query_svc Quote / OpenTable MSSQL 适配
6. ✨ query_svc Quote / OpenTable SQLite 适配
7. ✨ table_import MSSQL / SQLite FK 处理
8. 🎨 前端 driver 下拉 + DatabaseConfigSection 分支
9. 🎨 前端 SqliteFileField + 文件选择 IPC
10. 📄 i18n 文案、文档更新
