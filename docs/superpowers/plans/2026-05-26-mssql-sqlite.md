# MSSQL / SQLite 资产支持 — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 `AssetTypeDatabase` 下新增 `mssql` / `sqlite` 两个 driver，SQLite 仅本地文件、不走 SSH 隧道；MSSQL 走标准 SQL 认证 + 可选 TLS + 可选 SSH 隧道。

**Architecture:** 复用 `DatabaseConfig` 与 `DialDatabase` 入口，按 driver 在 5 处 switch 加 case：`Validate` / `buildDSN` / `openWithTunnel` / `setReadOnly` / `QuoteIdent` / `queryPrimaryKeys` / `queryColumns` / `disableFK`。SQLite 加 `Path` 字段，driver=sqlite 时跳过 host/port/user/pass 与隧道逻辑。

**Tech Stack:**
- Go 1.25 + 现有 `database/sql` 抽象
- 新依赖：`github.com/microsoft/go-mssqldb`（纯 Go，注册驱动名 `"sqlserver"`），`modernc.org/sqlite`（纯 Go，注册驱动名 `"sqlite"`）
- 前端：React 19 + Wails v2 IPC + i18next
- 测试：Go goconvey / testify / sqlmock；前端 vitest + happy-dom + RTL

**Spec:** `docs/superpowers/specs/2026-05-26-mssql-sqlite-design.md`

---

## Task 1: 加 Driver 常量 + DatabaseConfig.Path 字段

**Files:**
- Modify: `internal/model/entity/asset_entity/asset.go`
- Test: `internal/model/entity/asset_entity/asset_test.go`

- [ ] **Step 1: 写失败测试 — DriverMSSQL/SQLite 常量与默认端口**

在 `asset_test.go` 中加：

```go
func TestDatabaseDriverDefaultPort(t *testing.T) {
    Convey("DatabaseDriver.DefaultPort", t, func() {
        So(asset_entity.DriverMySQL.DefaultPort(), ShouldEqual, 3306)
        So(asset_entity.DriverPostgreSQL.DefaultPort(), ShouldEqual, 5432)
        So(asset_entity.DriverMSSQL.DefaultPort(), ShouldEqual, 1433)
        So(asset_entity.DriverSQLite.DefaultPort(), ShouldEqual, 0)
    })
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/model/entity/asset_entity/ -run TestDatabaseDriverDefaultPort -v`
Expected: FAIL with `undefined: asset_entity.DriverMSSQL`

- [ ] **Step 3: 加常量 + 扩展 DefaultPort**

修改 `internal/model/entity/asset_entity/asset.go` 第 26-44 行：

```go
const (
    DriverMySQL      DatabaseDriver = "mysql"
    DriverPostgreSQL DatabaseDriver = "postgresql"
    DriverMSSQL      DatabaseDriver = "mssql"
    DriverSQLite     DatabaseDriver = "sqlite"
)

func (d DatabaseDriver) DefaultPort() int {
    switch d {
    case DriverMySQL:
        return 3306
    case DriverPostgreSQL:
        return 5432
    case DriverMSSQL:
        return 1433
    case DriverSQLite:
        return 0
    default:
        return 0
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/model/entity/asset_entity/ -run TestDatabaseDriverDefaultPort -v`
Expected: PASS

- [ ] **Step 5: 加 Path 字段**

在 `DatabaseConfig` struct（约第 111 行）最后加：

```go
Path string `json:"path,omitempty"` // SQLite 文件绝对路径，其他 driver 为空
```

- [ ] **Step 6: 编译验证**

Run: `go build ./...`
Expected: 成功，无错误

- [ ] **Step 7: Commit**

```bash
git add internal/model/entity/asset_entity/
git commit -m "$(cat <<'EOF'
✨ 新增 mssql/sqlite driver 常量与 Path 字段

DatabaseDriver 加 DriverMSSQL("mssql")/DriverSQLite("sqlite")，
DefaultPort 分别返回 1433/0；DatabaseConfig 加 Path 字段，
仅 SQLite driver 使用（其他 driver 保持 omitempty 空值）。
EOF
)"
```

---

## Task 2: 扩展 validateDatabase 支持新 driver

**Files:**
- Modify: `internal/model/entity/asset_entity/asset.go`（`validateDatabase` 函数，约第 680 行）
- Test: `internal/model/entity/asset_entity/asset_test.go`

- [ ] **Step 1: 写失败测试 — MSSQL 校验**

```go
func TestValidateDatabaseMSSQL(t *testing.T) {
    Convey("MSSQL driver validation", t, func() {
        Convey("缺 host 应报错", func() {
            a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
            cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverMSSQL, Port: 1433, Username: "sa"}
            So(a.SetDatabaseConfig(cfg), ShouldBeNil)
            So(a.Validate().Error(), ShouldContainSubstring, "host")
        })
        Convey("完整字段通过", func() {
            a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase, Name: "x", GroupID: 1}
            cfg := &asset_entity.DatabaseConfig{
                Driver: asset_entity.DriverMSSQL, Host: "localhost",
                Port: 1433, Username: "sa",
            }
            So(a.SetDatabaseConfig(cfg), ShouldBeNil)
            So(a.Validate(), ShouldBeNil)
        })
    })
}
```

- [ ] **Step 2: 写失败测试 — SQLite 校验**

```go
func TestValidateDatabaseSQLite(t *testing.T) {
    Convey("SQLite driver validation", t, func() {
        Convey("缺 path 应报错", func() {
            a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase, Name: "x", GroupID: 1}
            cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite}
            So(a.SetDatabaseConfig(cfg), ShouldBeNil)
            So(a.Validate().Error(), ShouldContainSubstring, "path")
        })
        Convey("path 非绝对路径应报错", func() {
            a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase, Name: "x", GroupID: 1}
            cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: "relative.db"}
            So(a.SetDatabaseConfig(cfg), ShouldBeNil)
            So(a.Validate().Error(), ShouldContainSubstring, "绝对路径")
        })
        Convey("SQLite 不允许 SSH 隧道", func() {
            a := &asset_entity.Asset{
                Type: asset_entity.AssetTypeDatabase, Name: "x", GroupID: 1,
                SSHTunnelID: 5,
            }
            cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db"}
            So(a.SetDatabaseConfig(cfg), ShouldBeNil)
            So(a.Validate().Error(), ShouldContainSubstring, "隧道")
        })
        Convey("绝对路径 + 无隧道通过", func() {
            a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase, Name: "x", GroupID: 1}
            cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db"}
            So(a.SetDatabaseConfig(cfg), ShouldBeNil)
            So(a.Validate(), ShouldBeNil)
        })
    })
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/model/entity/asset_entity/ -run "TestValidateDatabase" -v`
Expected: FAIL

- [ ] **Step 4: 改 validateDatabase**

修改 `validateDatabase`（约第 680 行）：

```go
func (a *Asset) validateDatabase() error {
    cfg, err := a.GetDatabaseConfig()
    if err != nil {
        return fmt.Errorf("数据库配置无效: %w", err)
    }
    if cfg.Driver == "" {
        return errors.New("数据库驱动不能为空")
    }
    switch cfg.Driver {
    case DriverMySQL, DriverPostgreSQL, DriverMSSQL:
        if cfg.Host == "" {
            return errors.New("数据库主机地址不能为空")
        }
        if cfg.Port <= 0 {
            return errors.New("数据库端口无效")
        }
        if cfg.Username == "" {
            return errors.New("数据库用户名不能为空")
        }
    case DriverSQLite:
        if cfg.Path == "" {
            return errors.New("SQLite 必须指定 path")
        }
        if !filepath.IsAbs(cfg.Path) {
            return errors.New("SQLite path 必须为绝对路径")
        }
        if a.SSHTunnelID > 0 {
            return errors.New("SQLite 不支持 SSH 隧道")
        }
    default:
        return fmt.Errorf("不支持的数据库驱动: %s", cfg.Driver)
    }
    return nil
}
```

加 import：`"path/filepath"`（文件顶部，若已有则跳过）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/model/entity/asset_entity/ -run "TestValidateDatabase" -v`
Expected: PASS

- [ ] **Step 6: 跑全量包测试避免回归**

Run: `go test ./internal/model/entity/asset_entity/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/model/entity/asset_entity/
git commit -m "✨ Asset.Validate 支持 mssql/sqlite driver"
```

---

## Task 3: 引入 go-mssqldb 与 modernc.org/sqlite 依赖

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: 安装 mssql 驱动**

Run: `go get github.com/microsoft/go-mssqldb@latest`
Expected: 写入 go.mod / go.sum

- [ ] **Step 2: 安装 sqlite 驱动**

Run: `go get modernc.org/sqlite@latest`
Expected: 写入 go.mod / go.sum

- [ ] **Step 3: tidy**

Run: `go mod tidy`
Expected: 清理多余条目；保留 mssql 与 sqlite

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 成功

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "🔧 引入 go-mssqldb 与 modernc.org/sqlite 驱动"
```

---

## Task 4: ConnPool MSSQL 直连 DSN

**Files:**
- Modify: `internal/connpool/database.go`
- Test: `internal/connpool/database_dsn_test.go`（新建）

- [ ] **Step 1: 写失败测试 — buildDSN MSSQL**

新建 `internal/connpool/database_dsn_test.go`：

```go
package connpool

import (
    "net/url"
    "strings"
    "testing"

    "github.com/opskat/opskat/internal/model/entity/asset_entity"
    . "github.com/smartystreets/goconvey/convey"
)

func TestBuildDSNMSSQL(t *testing.T) {
    Convey("MSSQL DSN", t, func() {
        cfg := &asset_entity.DatabaseConfig{
            Driver:   asset_entity.DriverMSSQL,
            Host:     "sql.example.com",
            Port:     1433,
            Username: "sa",
            Database: "appdb",
        }
        driverName, dsn := buildDSN(cfg, "p@ss!w0rd")
        So(driverName, ShouldEqual, "sqlserver")

        u, err := url.Parse(dsn)
        So(err, ShouldBeNil)
        So(u.Scheme, ShouldEqual, "sqlserver")
        So(u.Host, ShouldEqual, "sql.example.com:1433")
        So(u.User.Username(), ShouldEqual, "sa")
        pw, _ := u.User.Password()
        So(pw, ShouldEqual, "p@ss!w0rd")
        So(u.Query().Get("database"), ShouldEqual, "appdb")
        So(u.Query().Get("encrypt"), ShouldEqual, "disable")
    })

    Convey("MSSQL DSN with TLS", t, func() {
        cfg := &asset_entity.DatabaseConfig{
            Driver: asset_entity.DriverMSSQL, Host: "h", Port: 1433,
            Username: "u", Database: "d", TLS: true,
        }
        _, dsn := buildDSN(cfg, "pw")
        So(strings.Contains(dsn, "encrypt=true"), ShouldBeTrue)
        So(strings.Contains(dsn, "trustservercertificate=true"), ShouldBeTrue)
    })
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/connpool/ -run TestBuildDSNMSSQL -v`
Expected: FAIL with "未匹配 case" 或 driverName 为空

- [ ] **Step 3: 在 buildDSN 加 MSSQL case**

修改 `internal/connpool/database.go`，在 `buildDSN` 的 `case asset_entity.DriverPostgreSQL` 之后加：

```go
case asset_entity.DriverMSSQL:
    q := url.Values{}
    if cfg.Database != "" {
        q.Set("database", cfg.Database)
    }
    if cfg.TLS {
        q.Set("encrypt", "true")
        q.Set("trustservercertificate", "true")
    } else {
        q.Set("encrypt", "disable")
    }
    if cfg.Params != "" {
        for k, v := range parseParams(cfg.Params) {
            q.Set(k, v)
        }
    }
    u := &url.URL{
        Scheme:   "sqlserver",
        User:     url.UserPassword(cfg.Username, password),
        Host:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
        RawQuery: q.Encode(),
    }
    return "sqlserver", u.String()
```

在文件顶部 import 加：

```go
_ "github.com/microsoft/go-mssqldb" // 注册 "sqlserver" driver
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/connpool/ -run TestBuildDSNMSSQL -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/connpool/
git commit -m "✨ connpool 支持 MSSQL 直连 DSN"
```

---

## Task 5: ConnPool MSSQL SSH 隧道

**Files:**
- Modify: `internal/connpool/database.go`
- Test: `internal/connpool/database_dsn_test.go`

- [ ] **Step 1: 写失败测试 — openWithTunnel 支持 MSSQL**

在 `database_dsn_test.go` 加测试，验证 `openWithTunnel` 对 MSSQL 返回非 nil 错误前不会进入 default 的 "不支持的数据库驱动" 分支：

```go
func TestOpenWithTunnelMSSQLRouted(t *testing.T) {
    // 不实际建立隧道,只验证 switch 路由到 MSSQL 分支(不再走 default 错误)。
    Convey("openWithTunnel 把 MSSQL 路由到专用分支", t, func() {
        cfg := &asset_entity.DatabaseConfig{
            Driver: asset_entity.DriverMSSQL, Host: "h", Port: 1433,
            Username: "u", Database: "d",
        }
        _, err := openWithTunnel(cfg, "pw", nil) // tunnel=nil 会在分支内部出错,但不应是"不支持"错误
        if err != nil {
            So(err.Error(), ShouldNotContainSubstring, "不支持的数据库驱动")
        }
    })
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/connpool/ -run TestOpenWithTunnelMSSQLRouted -v`
Expected: FAIL（错误信息含 "不支持的数据库驱动: mssql"）

- [ ] **Step 3: 实现 openMSSQLWithTunnel**

在 `database.go` 末尾加：

```go
func openMSSQLWithTunnel(cfg *asset_entity.DatabaseConfig, password string, tunnel *SSHTunnel) (*sql.DB, error) {
    // go-mssqldb 通过 msdsn.Config.DialContext 注入自定义 dialer
    _, dsn := buildDSN(cfg, password)
    msdsnCfg, err := msdsn.Parse(dsn)
    if err != nil {
        return nil, fmt.Errorf("parse mssql dsn: %w", err)
    }
    connector := mssql.NewConnectorConfig(msdsnCfg)
    connector.Dialer = mssqlDialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
        return tunnel.Dial(ctx)
    })
    return sql.OpenDB(connector), nil
}

// mssqlDialerFunc 适配 go-mssqldb 的 Dialer 接口签名。
type mssqlDialerFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func (f mssqlDialerFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
    return f(ctx, network, addr)
}
```

在 `openWithTunnel` 的 `case asset_entity.DriverPostgreSQL` 之后加：

```go
case asset_entity.DriverMSSQL:
    return openMSSQLWithTunnel(cfg, password, tunnel)
```

import 加：

```go
mssql "github.com/microsoft/go-mssqldb"
"github.com/microsoft/go-mssqldb/msdsn"
```

并把之前的下划线导入 `_ "github.com/microsoft/go-mssqldb"` 改为命名导入 `mssql "github.com/microsoft/go-mssqldb"`（用到 `mssql.NewConnectorConfig`）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/connpool/ -run TestOpenWithTunnelMSSQLRouted -v`
Expected: PASS

- [ ] **Step 5: 全包回归**

Run: `go test ./internal/connpool/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/connpool/
git commit -m "✨ connpool 支持 MSSQL SSH 隧道连接"
```

---

## Task 6: ConnPool SQLite DSN + PRAGMA query_only

**Files:**
- Modify: `internal/connpool/database.go`
- Test: `internal/connpool/database_dsn_test.go`、`internal/connpool/database_sqlite_test.go`（新建）

- [ ] **Step 1: 写失败测试 — buildDSN SQLite**

加入 `database_dsn_test.go`：

```go
func TestBuildDSNSQLite(t *testing.T) {
    Convey("SQLite DSN", t, func() {
        cfg := &asset_entity.DatabaseConfig{
            Driver: asset_entity.DriverSQLite,
            Path:   "/tmp/test.db",
        }
        driverName, dsn := buildDSN(cfg, "")
        So(driverName, ShouldEqual, "sqlite")
        So(dsn, ShouldEqual, "file:/tmp/test.db")
    })

    Convey("SQLite DSN with params", t, func() {
        cfg := &asset_entity.DatabaseConfig{
            Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db",
            Params: "_pragma=busy_timeout(5000)",
        }
        _, dsn := buildDSN(cfg, "")
        So(dsn, ShouldEqual, "file:/tmp/x.db?_pragma=busy_timeout(5000)")
    })
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/connpool/ -run TestBuildDSNSQLite -v`
Expected: FAIL

- [ ] **Step 3: 加 SQLite case**

在 `buildDSN` 的 MSSQL case 之后加：

```go
case asset_entity.DriverSQLite:
    dsn := "file:" + cfg.Path
    if cfg.Params != "" {
        dsn += "?" + cfg.Params
    }
    return "sqlite", dsn
```

在文件顶部 import 加 `_ "modernc.org/sqlite"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/connpool/ -run TestBuildDSNSQLite -v`
Expected: PASS

- [ ] **Step 5: 写真连接测试（用临时文件）**

新建 `internal/connpool/database_sqlite_test.go`：

```go
package connpool

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "github.com/opskat/opskat/internal/model/entity/asset_entity"
    . "github.com/smartystreets/goconvey/convey"
)

func TestDialDatabaseSQLite(t *testing.T) {
    Convey("SQLite 本地文件直连", t, func() {
        dir := t.TempDir()
        dbPath := filepath.Join(dir, "test.db")

        asset := &asset_entity.Asset{
            ID: 1, Type: asset_entity.AssetTypeDatabase,
        }
        cfg := &asset_entity.DatabaseConfig{
            Driver: asset_entity.DriverSQLite, Path: dbPath,
        }
        db, closer, err := DialDatabase(context.Background(), asset, cfg, "", nil)
        So(err, ShouldBeNil)
        So(db, ShouldNotBeNil)
        So(closer, ShouldBeNil) // SQLite 无隧道,closer 应为 nil
        defer func() { _ = db.Close() }()

        _, execErr := db.Exec("CREATE TABLE t (id INTEGER)")
        So(execErr, ShouldBeNil)
        _, err = os.Stat(dbPath)
        So(err, ShouldBeNil)
    })

    Convey("SQLite ReadOnly 用 PRAGMA query_only", t, func() {
        dir := t.TempDir()
        dbPath := filepath.Join(dir, "test.db")

        asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
        // 先建表
        initCfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: dbPath}
        db, _, err := DialDatabase(context.Background(), asset, initCfg, "", nil)
        So(err, ShouldBeNil)
        _, err = db.Exec("CREATE TABLE t (id INTEGER)")
        So(err, ShouldBeNil)
        _ = db.Close()

        // 再以 ReadOnly 打开
        roCfg := &asset_entity.DatabaseConfig{
            Driver: asset_entity.DriverSQLite, Path: dbPath, ReadOnly: true,
        }
        roDB, _, err := DialDatabase(context.Background(), asset, roCfg, "", nil)
        So(err, ShouldBeNil)
        defer func() { _ = roDB.Close() }()

        _, execErr := roDB.Exec("INSERT INTO t VALUES (1)")
        So(execErr, ShouldNotBeNil)
        So(execErr.Error(), ShouldContainSubstring, "read")
    })
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/connpool/ -run TestDialDatabaseSQLite -v`
Expected: FAIL（DialDatabase 进了 tunnel 分支或 setReadOnly 不识别 sqlite）

- [ ] **Step 7: DialDatabase 加 SQLite 短路 + setReadOnly 加 case**

修改 `DialDatabase` 在 tunnel 判断前加短路：

```go
if cfg.Driver == asset_entity.DriverSQLite {
    // SQLite 本地文件,不走隧道
    db, err := openDirect(cfg, password)
    if err != nil {
        return nil, nil, err
    }
    if pingErr := db.PingContext(ctx); pingErr != nil {
        if cerr := db.Close(); cerr != nil {
            logger.Default().Warn("close db", zap.Error(cerr))
        }
        return nil, nil, fmt.Errorf("数据库连接失败: %w", pingErr)
    }
    if cfg.ReadOnly {
        if roErr := setReadOnly(ctx, db, cfg.Driver); roErr != nil {
            if cerr := db.Close(); cerr != nil {
                logger.Default().Warn("close db", zap.Error(cerr))
            }
            return nil, nil, fmt.Errorf("设置只读模式失败: %w", roErr)
        }
    }
    return db, nil, nil
}
```

放在 `tunnelID := asset.SSHTunnelID` 之前。

在 `setReadOnly` 加：

```go
case asset_entity.DriverSQLite:
    _, err := db.ExecContext(ctx, "PRAGMA query_only = 1")
    return err
```

- [ ] **Step 8: 跑测试确认通过**

Run: `go test ./internal/connpool/ -run TestDialDatabaseSQLite -v`
Expected: PASS

- [ ] **Step 9: 全包回归**

Run: `go test ./internal/connpool/...`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add internal/connpool/
git commit -m "$(cat <<'EOF'
✨ connpool 支持 SQLite 本地文件直连

DialDatabase 在 driver=sqlite 时短路跳过隧道分支；setReadOnly 用
PRAGMA query_only=1 实现连接级只读；不支持 SSH 隧道（model 层已拦）。
EOF
)"
```

---

## Task 7: ConnPool MSSQL ReadOnly no-op + 日志

**Files:**
- Modify: `internal/connpool/database.go`
- Test: `internal/connpool/database_dsn_test.go`

- [ ] **Step 1: 写测试 — MSSQL ReadOnly no-op**

加入 `database_dsn_test.go`：

```go
func TestSetReadOnlyMSSQLNoop(t *testing.T) {
    Convey("MSSQL setReadOnly 是 no-op 不报错", t, func() {
        // 用 sqlmock 验证不发任何 SQL
        db, mock, err := sqlmock.New()
        So(err, ShouldBeNil)
        defer func() { _ = db.Close() }()

        err = setReadOnly(context.Background(), db, asset_entity.DriverMSSQL)
        So(err, ShouldBeNil)
        So(mock.ExpectationsWereMet(), ShouldBeNil) // 没有 ExpectExec,任何调用都会失败
    })
}
```

import 加：`"github.com/DATA-DOG/go-sqlmock"`（若 go.mod 没有，先 `go get github.com/DATA-DOG/go-sqlmock`，project 中已用见 `internal/service/query_svc/open_table_test.go`）。

- [ ] **Step 2: 跑测试**

Run: `go test ./internal/connpool/ -run TestSetReadOnlyMSSQLNoop -v`
Expected: 可能直接 PASS（default 分支返回 nil）也可能 FAIL；如 PASS 进 Step 4。

- [ ] **Step 3: 显式 case 加日志**

在 `setReadOnly` 加（在 default 之前）：

```go
case asset_entity.DriverMSSQL:
    logger.Ctx(ctx).Info("MSSQL connection-level read-only not supported, relying on policy")
    return nil
```

（用 `logger.Ctx(ctx)` 不是 `Default()`，遵循 CLAUDE.md 的日志规范。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/connpool/ -run TestSetReadOnlyMSSQLNoop -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/connpool/
git commit -m "✨ MSSQL ReadOnly 显式 no-op 并记录日志"
```

---

## Task 8: query_svc Quote 适配 MSSQL 与 SQLite

**Files:**
- Modify: `internal/service/query_svc/quote.go`
- Test: `internal/service/query_svc/quote_test.go`

- [ ] **Step 1: 写失败测试 — MSSQL/SQLite 引号**

在 `quote_test.go` 追加：

```go
func TestQuoteIdentMSSQL(t *testing.T) {
    Convey("MSSQL 用 [bracket]", t, func() {
        So(QuoteIdent("user", asset_entity.DriverMSSQL), ShouldEqual, "[user]")
        So(QuoteIdent("a]b", asset_entity.DriverMSSQL), ShouldEqual, "[a]]b]")
    })
}

func TestQuoteIdentSQLite(t *testing.T) {
    Convey("SQLite 用 \"double\"", t, func() {
        So(QuoteIdent("user", asset_entity.DriverSQLite), ShouldEqual, `"user"`)
        So(QuoteIdent(`a"b`, asset_entity.DriverSQLite), ShouldEqual, `"a""b"`)
    })
}

func TestQuoteTableRefMSSQL(t *testing.T) {
    Convey("MSSQL 加 [db] 前缀", t, func() {
        So(QuoteTableRef("appdb", "users", asset_entity.DriverMSSQL), ShouldEqual, "[appdb].[users]")
    })
    Convey("MSSQL 支持 schema.table（输出 [db].[schema].[table]）", t, func() {
        So(QuoteTableRef("appdb", "dbo.users", asset_entity.DriverMSSQL), ShouldEqual, "[appdb].[dbo].[users]")
    })
}

func TestQuoteTableRefSQLite(t *testing.T) {
    Convey("SQLite 只引用表名（无 database 概念）", t, func() {
        So(QuoteTableRef("ignored", "users", asset_entity.DriverSQLite), ShouldEqual, `"users"`)
    })
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/query_svc/ -run "TestQuote" -v`
Expected: FAIL

- [ ] **Step 3: 改 QuoteIdent / QuoteTableRef**

替换 `quote.go`：

```go
func QuoteIdent(name string, driver asset_entity.DatabaseDriver) string {
    switch driver {
    case asset_entity.DriverPostgreSQL, asset_entity.DriverSQLite:
        return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
    case asset_entity.DriverMSSQL:
        return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
    default: // MySQL
        return "`" + strings.ReplaceAll(name, "`", "``") + "`"
    }
}

func QuoteTableRef(database, table string, driver asset_entity.DatabaseDriver) string {
    switch driver {
    case asset_entity.DriverPostgreSQL, asset_entity.DriverSQLite:
        return quoteQualified(table, driver)
    case asset_entity.DriverMSSQL:
        // 支持 db.[schema.]table 拼接,table 可能已含 "schema.table"
        return QuoteIdent(database, driver) + "." + quoteQualified(table, driver)
    default: // MySQL
        return QuoteIdent(database, driver) + "." + QuoteIdent(table, driver)
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/query_svc/ -run "TestQuote" -v`
Expected: PASS（包括原有 MySQL / PG 用例）

- [ ] **Step 5: Commit**

```bash
git add internal/service/query_svc/
git commit -m "✨ query_svc Quote 支持 MSSQL/SQLite 方言引号"
```

---

## Task 9: query_svc OpenTable MSSQL 主键 / 列元信息

**Files:**
- Modify: `internal/service/query_svc/open_table.go`
- Test: `internal/service/query_svc/open_table_test.go`

- [ ] **Step 1: 写失败测试 — MSSQL queryPrimaryKeys**

在 `open_table_test.go` 加：

```go
func TestOpenTableMSSQL(t *testing.T) {
    Convey("MSSQL OpenTable 用 information_schema 查 PK / 列", t, func() {
        db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
        So(err, ShouldBeNil)
        defer func() { _ = db.Close() }()

        pkSQL := "SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc " +
            "JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu " +
            "ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME " +
            "WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' " +
            "AND tc.TABLE_CATALOG = @p1 AND tc.TABLE_NAME = @p2 " +
            "ORDER BY kcu.ORDINAL_POSITION"
        mock.ExpectQuery(pkSQL).
            WithArgs("appdb", "users").
            WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))

        colSQL := "SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT " +
            "FROM INFORMATION_SCHEMA.COLUMNS " +
            "WHERE TABLE_CATALOG = @p1 AND TABLE_NAME = @p2 " +
            "ORDER BY ORDINAL_POSITION"
        mock.ExpectQuery(colSQL).
            WithArgs("appdb", "users").
            WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT"}).
                AddRow("id", "int", "NO", nil).
                AddRow("name", "varchar", "YES", nil))

        mock.ExpectQuery("SELECT COUNT(*) FROM [appdb].[users]").
            WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))

        mock.ExpectQuery("SELECT * FROM [appdb].[users] LIMIT 10 OFFSET 0").
            WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
                AddRow(1, "alice").AddRow(2, "bob"))

        res, err := OpenTable(context.Background(), db, asset_entity.DriverMSSQL, "appdb", "users", 10)
        So(err, ShouldBeNil)
        So(res.PrimaryKeys, ShouldResemble, []string{"id"})
        So(res.Columns, ShouldContain, "id")
        So(res.TotalCount, ShouldEqual, 2)
    })
}
```

注意：MSSQL 不支持 `LIMIT n OFFSET m` 语法。Step 5 会把 `queryFirstPage` 改成 driver-aware：MSSQL 用 `SELECT TOP n * FROM <table>`（offset=0 的简化形式，免去 ORDER BY 要求），其他 driver 保持 `LIMIT/OFFSET`。所以测试里 MSSQL 的 firstPage SQL 应为 `SELECT TOP 10 * FROM [appdb].[users]`。把上面测试里的 `mock.ExpectQuery("SELECT * FROM [appdb].[users] LIMIT 10 OFFSET 0")` 改为 `mock.ExpectQuery("SELECT TOP 10 * FROM [appdb].[users]")`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/query_svc/ -run TestOpenTableMSSQL -v`
Expected: FAIL（switch 进了 default 分支，SQL 不匹配）

- [ ] **Step 3: 加 MSSQL 分支到 queryPrimaryKeys**

修改 `queryPrimaryKeys`，在 `case asset_entity.DriverPostgreSQL` 之后加：

```go
case asset_entity.DriverMSSQL:
    sqlText = "SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc " +
        "JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu " +
        "ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME " +
        "WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' " +
        "AND tc.TABLE_CATALOG = @p1 AND tc.TABLE_NAME = @p2 " +
        "ORDER BY kcu.ORDINAL_POSITION"
    rows, err := conn.QueryContext(ctx, sqlText, sql.Named("p1", database), sql.Named("p2", table))
    if err != nil {
        return nil, err
    }
    return scanPKRows(rows, "COLUMN_NAME")
```

需要重构 `queryPrimaryKeys`，把现有 default 分支抽出 `scanPKRows` helper 收尾扫描逻辑。重构后函数轮廓：

```go
func queryPrimaryKeys(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database, table string) ([]string, error) {
    switch driver {
    case asset_entity.DriverPostgreSQL:
        // ... 现有 PG 实现, scanPKRows(rows, "column_name")
    case asset_entity.DriverMSSQL:
        // ... 上面新增
    default: // MySQL
        sqlText := "SHOW KEYS FROM " + QuoteTableRef(database, table, driver) + " WHERE Key_name = 'PRIMARY'"
        rows, err := conn.QueryContext(ctx, sqlText)
        if err != nil {
            return nil, err
        }
        return scanPKRows(rows, "Column_name")
    }
}

func scanPKRows(rows *sql.Rows, colName string) ([]string, error) {
    defer func() { _ = rows.Close() }()
    cols, err := rows.Columns()
    if err != nil {
        return nil, err
    }
    out := make([]string, 0, 4)
    for rows.Next() {
        values := make([]any, len(cols))
        ptrs := make([]any, len(cols))
        for i := range values {
            ptrs[i] = &values[i]
        }
        if err := rows.Scan(ptrs...); err != nil {
            return nil, err
        }
        row := zipRow(cols, values)
        name := pickString(row, colName, strings.ToLower(colName), "column_name", "Column_name")
        if name != "" {
            out = append(out, name)
        }
    }
    return out, rows.Err()
}
```

- [ ] **Step 4: 加 MSSQL 分支到 queryColumns**

类似重构。在 `queryColumns` 加：

```go
case asset_entity.DriverMSSQL:
    sqlText = "SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT " +
        "FROM INFORMATION_SCHEMA.COLUMNS " +
        "WHERE TABLE_CATALOG = @p1 AND TABLE_NAME = @p2 " +
        "ORDER BY ORDINAL_POSITION"
    rows, err := conn.QueryContext(ctx, sqlText, sql.Named("p1", database), sql.Named("p2", table))
```

并把现有 default 也走同样路径——可以保留 conditional `QueryContext` 调用。最简做法：把 `args []any` 提到 switch 之前，按 driver 分别填充。

```go
func queryColumns(...) {
    var sqlText string
    var args []any
    switch driver {
    case asset_entity.DriverPostgreSQL:
        // 现有
    case asset_entity.DriverMSSQL:
        sqlText = "..."
        args = []any{sql.Named("p1", database), sql.Named("p2", table)}
    default: // MySQL
        sqlText = "SHOW COLUMNS FROM " + QuoteTableRef(database, table, driver)
    }
    rows, err := conn.QueryContext(ctx, sqlText, args...)
    ...
}
```

- [ ] **Step 5: queryFirstPage 加 driver 分支处理 MSSQL TOP 语法**

修改 `queryFirstPage` 签名加 `driver`：

```go
func queryFirstPage(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, tableRef string, pageSize int) ([]map[string]any, []string, error) {
    var sqlText string
    switch driver {
    case asset_entity.DriverMSSQL:
        sqlText = fmt.Sprintf("SELECT TOP %d * FROM %s", pageSize, tableRef) //nolint:gosec // tableRef 已 quote
    default:
        sqlText = fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET 0", tableRef, pageSize) //nolint:gosec
    }
    // ... 其余照旧
}
```

caller 在 `OpenTable` 内更新：

```go
firstPage, dataCols, err := queryFirstPage(ctx, conn, driver, tableRef, pageSize)
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/service/query_svc/ -run TestOpenTableMSSQL -v`
Expected: PASS

- [ ] **Step 7: 现有 MySQL / PG 测试不破**

Run: `go test ./internal/service/query_svc/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/service/query_svc/
git commit -m "✨ OpenTable 适配 MSSQL information_schema 主键/列与 TOP 分页"
```

---

## Task 10: query_svc OpenTable SQLite PRAGMA 主键 / 列元信息

**Files:**
- Modify: `internal/service/query_svc/open_table.go`
- Test: `internal/service/query_svc/open_table_test.go`

- [ ] **Step 1: 写失败测试 — SQLite OpenTable**

加：

```go
func TestOpenTableSQLite(t *testing.T) {
    Convey("SQLite OpenTable 用 pragma_table_info", t, func() {
        db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
        So(err, ShouldBeNil)
        defer func() { _ = db.Close() }()

        pkSQL := "SELECT name FROM pragma_table_info('users') WHERE pk > 0 ORDER BY pk"
        mock.ExpectQuery(pkSQL).
            WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("id"))

        colSQL := "SELECT name, type, CASE notnull WHEN 0 THEN 'YES' ELSE 'NO' END AS is_nullable, dflt_value FROM pragma_table_info('users')"
        mock.ExpectQuery(colSQL).
            WillReturnRows(sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value"}).
                AddRow("id", "INTEGER", "NO", nil).
                AddRow("name", "TEXT", "YES", nil))

        mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).
            WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))

        mock.ExpectQuery(`SELECT * FROM "users" LIMIT 10 OFFSET 0`).
            WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
                AddRow(1, "alice").AddRow(2, "bob"))

        res, err := OpenTable(context.Background(), db, asset_entity.DriverSQLite, "", "users", 10)
        So(err, ShouldBeNil)
        So(res.PrimaryKeys, ShouldResemble, []string{"id"})
        So(res.Columns, ShouldContain, "id")
    })
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/query_svc/ -run TestOpenTableSQLite -v`
Expected: FAIL

- [ ] **Step 3: 加 SQLite 分支**

在 `queryPrimaryKeys` 加：

```go
case asset_entity.DriverSQLite:
    // pragma_table_info 是表值函数,参数必须内联字符串字面量；SQLQuote 处理单引号转义
    sqlText := "SELECT name FROM pragma_table_info(" + SQLQuote(table) + ") WHERE pk > 0 ORDER BY pk"
    rows, err := conn.QueryContext(ctx, sqlText)
    if err != nil {
        return nil, err
    }
    return scanPKRows(rows, "name")
```

在 `queryColumns` 加：

```go
case asset_entity.DriverSQLite:
    sqlText = "SELECT name, type, CASE notnull WHEN 0 THEN 'YES' ELSE 'NO' END AS is_nullable, dflt_value FROM pragma_table_info(" + SQLQuote(table) + ")"
```

确认 `pickString` 在 `queryColumns` 内能处理 SQLite 列名（`name`、`type`、`is_nullable`、`dflt_value`）：

- `pickString(row, "column_name", "Field", "field")` → 加 `"name"`
- `pickString(row, "data_type", "Type", "type", "udt_name")` → 已含 `type`
- `pickString(row, "is_nullable", "Null", "null")` → 已含 `is_nullable`
- default 字段：现有按 `column_default` / `Default` / `default` → 加 `dflt_value`

修改 `queryColumns` 内 `pickString` 调用：

```go
name := pickString(row, "column_name", "name", "Field", "field")
...
typeStr := pickString(row, "data_type", "type", "udt_name", "Type")
...
defaultRaw, hasDefault := row["column_default"]
if !hasDefault || defaultRaw == nil {
    defaultRaw, hasDefault = row["Default"]
}
if !hasDefault || defaultRaw == nil {
    defaultRaw, hasDefault = row["dflt_value"]
}
if !hasDefault || defaultRaw == nil {
    defaultRaw = row["default"]
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/query_svc/ -run TestOpenTableSQLite -v`
Expected: PASS

- [ ] **Step 5: 全包回归**

Run: `go test ./internal/service/query_svc/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/service/query_svc/
git commit -m "✨ OpenTable 适配 SQLite pragma_table_info 主键/列查询"
```

---

## Task 11: table_import 加 Tables 字段 + SQLite/MSSQL FK 支持

**Files:**
- Modify: `internal/service/query_svc/table_import.go`
- Test: `internal/service/query_svc/table_import_test.go`

- [ ] **Step 1: 写失败测试 — SQLite PRAGMA foreign_keys**

加：

```go
func TestRunTableImportBatchSQLiteForeignKeys(t *testing.T) {
    session := &fakeSQLSession{}
    _, err := RunTableImportBatch(context.Background(), session, asset_entity.DriverSQLite, TableImportBatchRequest{
        Mode:                    "append",
        ContinueOnError:         true,
        DisableForeignKeyChecks: true,
        Statements: []string{
            `INSERT INTO "users" ("id") VALUES (1);`,
        },
    })
    if err != nil {
        t.Fatalf("RunTableImportBatch() error = %v", err)
    }
    want := []string{
        "conn:PRAGMA foreign_keys = OFF",
        `conn:INSERT INTO "users" ("id") VALUES (1);`,
        "conn:PRAGMA foreign_keys = ON",
    }
    if strings.Join(session.operations, "\n") != strings.Join(want, "\n") {
        t.Fatalf("got: %v\nwant: %v", session.operations, want)
    }
}
```

- [ ] **Step 2: 写失败测试 — MSSQL NOCHECK CONSTRAINT**

加：

```go
func TestRunTableImportBatchMSSQLForeignKeys(t *testing.T) {
    session := &fakeSQLSession{}
    _, err := RunTableImportBatch(context.Background(), session, asset_entity.DriverMSSQL, TableImportBatchRequest{
        Mode:                    "append",
        ContinueOnError:         true,
        DisableForeignKeyChecks: true,
        Tables:                  []string{"appdb.dbo.users", "appdb.dbo.orders"},
        Statements: []string{
            `INSERT INTO [appdb].[dbo].[users] ([id]) VALUES (1);`,
        },
    })
    if err != nil {
        t.Fatalf("RunTableImportBatch() error = %v", err)
    }
    want := []string{
        "conn:ALTER TABLE [appdb].[dbo].[users] NOCHECK CONSTRAINT ALL",
        "conn:ALTER TABLE [appdb].[dbo].[orders] NOCHECK CONSTRAINT ALL",
        `conn:INSERT INTO [appdb].[dbo].[users] ([id]) VALUES (1);`,
        "conn:ALTER TABLE [appdb].[dbo].[users] WITH CHECK CHECK CONSTRAINT ALL",
        "conn:ALTER TABLE [appdb].[dbo].[orders] WITH CHECK CHECK CONSTRAINT ALL",
    }
    if strings.Join(session.operations, "\n") != strings.Join(want, "\n") {
        t.Fatalf("got: %v\nwant: %v", session.operations, want)
    }
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/service/query_svc/ -run "TestRunTableImportBatch.*ForeignKeys" -v`
Expected: FAIL（Tables 字段不存在，driver 不支持）

- [ ] **Step 4: 加 Tables 字段 + 实现 disableFK helpers**

修改 `TableImportBatchRequest`：

```go
type TableImportBatchRequest struct {
    Statements              []string `json:"statements"`
    Mode                    string   `json:"mode"`
    ContinueOnError         bool     `json:"continueOnError"`
    DisableForeignKeyChecks bool     `json:"disableForeignKeyChecks"`
    Tables                  []string `json:"tables,omitempty"` // 仅 MSSQL 需要,已 Quote 过的表引用
}
```

把 `RunTableImportBatch` 里的 disableFK 逻辑抽到 helper：

```go
func disableForeignKeyChecks(ctx context.Context, session SQLSession, driver asset_entity.DatabaseDriver, req TableImportBatchRequest) (restore func() error, err error) {
    switch driver {
    case asset_entity.DriverMySQL:
        if _, e := session.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); e != nil {
            return nil, e
        }
        return func() error {
            _, e := session.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
            return e
        }, nil
    case asset_entity.DriverSQLite:
        if _, e := session.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); e != nil {
            return nil, e
        }
        return func() error {
            _, e := session.ExecContext(ctx, "PRAGMA foreign_keys = ON")
            return e
        }, nil
    case asset_entity.DriverMSSQL:
        for _, t := range req.Tables {
            if _, e := session.ExecContext(ctx, "ALTER TABLE "+t+" NOCHECK CONSTRAINT ALL"); e != nil {
                return nil, e
            }
        }
        return func() error {
            for _, t := range req.Tables {
                if _, e := session.ExecContext(ctx, "ALTER TABLE "+t+" WITH CHECK CHECK CONSTRAINT ALL"); e != nil {
                    return e
                }
            }
            return nil
        }, nil
    }
    return nil, nil // PG 与未知 driver: 不支持
}
```

替换 `RunTableImportBatch` 里现有的 `disableFK := ... && driver == asset_entity.DriverMySQL` 块：

```go
var restoreFK func() error
if request.DisableForeignKeyChecks {
    var e error
    restoreFK, e = disableForeignKeyChecks(ctx, session, driver, request)
    if e != nil {
        return nil, fmt.Errorf("disable foreign key checks: %w", e)
    }
    if restoreFK != nil {
        defer func() {
            if restoreErr := restoreFK(); restoreErr != nil {
                if err == nil && len(result.Errors) == 0 {
                    err = fmt.Errorf("restore foreign key checks: %w", restoreErr)
                    return
                }
                result.Error++
                result.Errors = append(result.Errors, TableImportBatchError{
                    Index:   -1,
                    Message: fmt.Sprintf("restore foreign key checks: %v", restoreErr),
                })
            }
        }()
    }
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/service/query_svc/ -run "TestRunTableImportBatch" -v`
Expected: PASS（包括原有 MySQL 用例）

- [ ] **Step 6: 检查 caller — 看 query_svc 之外有没有用旧 TableImportBatchRequest 的代码**

Run: `grep -rn "TableImportBatchRequest" /Users/codfrm/Code/opskat/opskat --include="*.go"`
Expected: 只在 `query_svc/table_import.go`、test 与 `internal/service/` 调用方使用。检查每个 caller 是否需要传 Tables——MySQL/PG caller 不需要，留默认空切片即可。

- [ ] **Step 7: 全包回归**

Run: `go test ./internal/service/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/service/query_svc/
git commit -m "$(cat <<'EOF'
✨ table_import FK 控制支持 SQLite/MSSQL

抽出 disableForeignKeyChecks helper：MySQL 用 SET FOREIGN_KEY_CHECKS、
SQLite 用 PRAGMA foreign_keys、MSSQL 按 Tables 列表 ALTER TABLE NOCHECK；
TableImportBatchRequest 加 Tables 字段（仅 MSSQL 用）。
EOF
)"
```

---

## Task 12: assettype/database.go 处理 Path 字段

**Files:**
- Modify: `internal/assettype/database.go`
- Test: `internal/assettype/database_test.go`

- [ ] **Step 1: 写失败测试 — SQLite ApplyCreateArgs 接收 path**

```go
func TestDatabaseHandlerApplyCreateArgsSQLite(t *testing.T) {
    Convey("SQLite ApplyCreateArgs 写入 Path 字段", t, func() {
        h := &databaseHandler{}
        a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
        err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
            "driver": "sqlite",
            "path":   "/tmp/x.db",
        })
        So(err, ShouldBeNil)
        cfg, err := a.GetDatabaseConfig()
        So(err, ShouldBeNil)
        So(cfg.Driver, ShouldEqual, asset_entity.DriverSQLite)
        So(cfg.Path, ShouldEqual, "/tmp/x.db")
        So(cfg.Host, ShouldEqual, "")
    })
}

func TestDatabaseHandlerSafeViewSQLite(t *testing.T) {
    Convey("SQLite SafeView 返回 path 不返回 host", t, func() {
        h := &databaseHandler{}
        a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
        _ = a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
            Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db",
        })
        view := h.SafeView(a)
        So(view["driver"], ShouldEqual, "sqlite")
        So(view["path"], ShouldEqual, "/tmp/x.db")
        _, hasHost := view["host"]
        So(hasHost, ShouldBeFalse)
    })
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/assettype/ -run "TestDatabaseHandler.*SQLite" -v`
Expected: FAIL

- [ ] **Step 3: 改 ApplyCreateArgs / ApplyUpdateArgs / SafeView**

修改 `ApplyCreateArgs`（约第 49 行）：

```go
func (h *databaseHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
    driver := ArgString(args, "driver")
    if driver == "" {
        return fmt.Errorf("database type requires driver parameter (mysql, postgresql, mssql, sqlite)")
    }
    cfg := &asset_entity.DatabaseConfig{
        Driver:     asset_entity.DatabaseDriver(driver),
        Database:   ArgString(args, "database"),
        ReadOnly:   ArgString(args, "read_only") == "true",
        SSHAssetID: ArgInt64(args, "ssh_asset_id"),
    }
    if cfg.Driver == asset_entity.DriverSQLite {
        cfg.Path = ArgString(args, "path")
    } else {
        cfg.Host = ArgString(args, "host")
        cfg.Port = ArgInt(args, "port")
        cfg.Username = ArgString(args, "username")
        if password := ArgString(args, "password"); password != "" {
            encrypted, err := credential_svc.Default().Encrypt(password)
            if err != nil {
                return fmt.Errorf("encrypt database password: %w", err)
            }
            cfg.Password = encrypted
        }
    }
    return a.SetDatabaseConfig(cfg)
}
```

修改 `ApplyUpdateArgs` 类似：在更新分支前判断 `cfg.Driver == DriverSQLite` 时只接收 `path`、`read_only`、`database`，其余字段不动。

修改 `SafeView`：

```go
func (h *databaseHandler) SafeView(a *asset_entity.Asset) map[string]any {
    cfg, err := a.GetDatabaseConfig()
    if err != nil || cfg == nil {
        return nil
    }
    if cfg.Driver == asset_entity.DriverSQLite {
        return map[string]any{
            "driver":    string(cfg.Driver),
            "path":      cfg.Path,
            "database":  cfg.Database,
            "read_only": cfg.ReadOnly,
        }
    }
    return map[string]any{
        "host":      cfg.Host,
        "port":      cfg.Port,
        "username":  cfg.Username,
        "driver":    string(cfg.Driver),
        "database":  cfg.Database,
        "read_only": cfg.ReadOnly,
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/assettype/ -run "TestDatabaseHandler" -v`
Expected: PASS

- [ ] **Step 5: 全包回归**

Run: `go test ./internal/assettype/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/assettype/
git commit -m "✨ databaseHandler 适配 SQLite path 与 mssql driver"
```

---

## Task 13: App.SelectSQLiteFile IPC binding

**Files:**
- Create or Modify: `internal/app/asset.go`（看哪里放 asset 相关 IPC；若无可建 `internal/app/database.go`）
- Modify: `main.go` 若需要

- [ ] **Step 1: 看现有 asset / database 相关 App method 在哪个文件**

Run: `grep -rn "func (a \*App)" internal/app/*.go | head`
找出 asset / database 相关的 App method 放置位置。

- [ ] **Step 2: 写一个简单的 binding 方法**

在合适的文件（建议放在 asset 相关 file，例如 `internal/app/asset.go`）加：

```go
// SelectSQLiteFile 打开原生文件对话框,返回选中的 SQLite 文件绝对路径。
// 取消选择返回空字符串(不算错误)。
func (a *App) SelectSQLiteFile() (string, error) {
    path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
        Title: "选择 SQLite 数据库文件",
        Filters: []wailsRuntime.FileFilter{
            {DisplayName: "SQLite (*.db, *.sqlite, *.sqlite3)", Pattern: "*.db;*.sqlite;*.sqlite3"},
            {DisplayName: "All Files", Pattern: "*"},
        },
    })
    if err != nil {
        return "", fmt.Errorf("打开文件对话框失败: %w", err)
    }
    return path, nil
}
```

确认 `wailsRuntime` import 与 `a.ctx` 用法和现有 App method 一致。

- [ ] **Step 3: 跑 wails dev 让 frontend bindings 重新生成**

Run: `make dev`（在另一个终端跑；等 wails 重新生成 `frontend/wailsjs/go/app/App.{d.ts,js}`）

或直接：

Run: `wails generate module`
Expected: `frontend/wailsjs/go/app/App.d.ts` 出现 `SelectSQLiteFile(): Promise<string>`

可以先 `Ctrl+C` 退出 dev，看一下生成的 `App.d.ts` 是否包含新方法。

- [ ] **Step 4: Commit**

```bash
git add internal/app/ frontend/wailsjs/
git commit -m "✨ App.SelectSQLiteFile 文件选择 IPC"
```

---

## Task 14: 前端 DatabaseConfigSection 加 driver 下拉 + SQLite 分支

**Files:**
- Modify: `frontend/src/components/asset/DatabaseConfigSection.tsx`
- Test: 若有 DatabaseConfigSection.test.tsx 则 modify；否则新建

- [ ] **Step 1: 加 driver 下拉到 DatabaseConfigSection**

注意：当前 driver 是 props 传入只读的，driver 切换发生在 `AssetForm` 里。**Driver 下拉应该放在 AssetForm 而不是 DatabaseConfigSection** —— 看 AssetForm 现有结构，driver state 与 driver 下拉应该已经在 AssetForm 渲染。

Run: `grep -n "driver" /Users/codfrm/Code/opskat/opskat/frontend/src/components/asset/AssetForm.tsx | head -30`
确认 driver 下拉位置。如已有 dropdown，在 Task 15 扩展它的 options；本 Task 只处理 DatabaseConfigSection 内的字段切换。

- [ ] **Step 2: 加 SQLite 路径字段 + MSSQL TLS 字段**

修改 `DatabaseConfigSection.tsx`。把整个 "Connection & Auth" 块包到 `driver === "sqlite"` 的条件外。SQLite 分支单独渲染一个 `SqliteFileField`。

```tsx
{driver === "sqlite" ? (
  <div className="grid gap-3 border rounded-lg p-3">
    <div className="grid gap-2">
      <Label>{t("asset.database.sqlite.path.label")}</Label>
      <div className="flex gap-2">
        <Input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder={t("asset.database.sqlite.path.placeholder")}
        />
        <Button
          type="button"
          variant="secondary"
          onClick={async () => {
            const selected = await SelectSQLiteFile();
            if (selected) setPath(selected);
          }}
        >
          {t("asset.database.sqlite.path.browse")}
        </Button>
      </div>
    </div>
  </div>
) : (
  <>
    <div className="grid gap-3 border rounded-lg p-3">
      {/* 现有 host/port/username/password 块 */}
      ...
    </div>
    <div className="grid gap-2">
      <Label>{t("asset.database")}</Label>
      <Input ... />
    </div>
    {driver === "postgresql" && (...)}
    {(driver === "mysql" || driver === "mssql") && (
      <div className="flex items-center justify-between">
        <Label>TLS</Label>
        <Switch checked={tls} onCheckedChange={setTls} />
      </div>
    )}
    <div className="grid gap-2">{/* Params */}...</div>
    <div className="flex items-center justify-between">{/* Read Only */}...</div>
    <div className="grid gap-2">{/* SSH Tunnel,仅非 sqlite */}...</div>
  </>
)}
```

Props 加：

```tsx
path: string;
setPath: (v: string) => void;
```

import 加：

```tsx
import { Button } from "@opskat/ui";
import { SelectSQLiteFile } from "../../../wailsjs/go/app/App";
```

完整修改：见 spec §7.1 的伪代码并按现有 TSX 行风格落地。

- [ ] **Step 3: 改端口 placeholder**

第 87 行：

```tsx
placeholder={driver === "postgresql" ? "5432" : driver === "mssql" ? "1433" : "3306"}
```

- [ ] **Step 4: 加单元测试**

新建 `frontend/src/components/asset/__tests__/DatabaseConfigSection.test.tsx`。所有 props 通过 `makeProps()` helper 给默认值，逐个 case 覆盖 sqlite / mssql：

```tsx
import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { DatabaseConfigSection, type DatabaseConfigSectionProps } from "../DatabaseConfigSection";

function makeProps(overrides: Partial<DatabaseConfigSectionProps> = {}): DatabaseConfigSectionProps {
  return {
    host: "", setHost: vi.fn(),
    port: 0, setPort: vi.fn(),
    username: "", setUsername: vi.fn(),
    driver: "mysql",
    database: "", setDatabase: vi.fn(),
    sslMode: "disable", setSslMode: vi.fn(),
    tls: false, setTls: vi.fn(),
    readOnly: false, setReadOnly: vi.fn(),
    sshTunnelId: 0, setSshTunnelId: vi.fn(),
    params: "", setParams: vi.fn(),
    password: "", setPassword: vi.fn(),
    encryptedPassword: "",
    passwordSource: "inline", setPasswordSource: vi.fn(),
    passwordCredentialId: 0, setPasswordCredentialId: vi.fn(),
    managedPasswords: [],
    path: "", setPath: vi.fn(),
    ...overrides,
  };
}

describe("DatabaseConfigSection", () => {
  it("driver=sqlite 渲染 path 字段且不渲染 host", () => {
    render(<DatabaseConfigSection {...makeProps({ driver: "sqlite", path: "/tmp/x.db" })} />);
    expect(screen.getByDisplayValue("/tmp/x.db")).toBeInTheDocument();
    expect(screen.queryByPlaceholderText("example.com")).not.toBeInTheDocument();
  });

  it("driver=mssql 渲染 host + TLS 开关", () => {
    render(<DatabaseConfigSection {...makeProps({ driver: "mssql" })} />);
    expect(screen.getByPlaceholderText("example.com")).toBeInTheDocument();
    expect(screen.getByText("TLS")).toBeInTheDocument();
  });
});
```

`SelectSQLiteFile` 是 Wails binding，在 `src/__tests__/setup.ts` 里全局 mock；若 mock 没覆盖这一个，本测试加：

```tsx
vi.mock("../../../wailsjs/go/app/App", () => ({
  SelectSQLiteFile: vi.fn().mockResolvedValue(""),
}));
```

- [ ] **Step 5: 跑测试**

Run: `cd frontend && pnpm test DatabaseConfigSection -- --run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/asset/DatabaseConfigSection.tsx frontend/src/components/asset/__tests__/
git commit -m "🎨 DatabaseConfigSection 支持 sqlite 路径与 mssql 选项"
```

---

## Task 15: AssetForm driver 下拉 + 默认值 + 序列化

**Files:**
- Modify: `frontend/src/components/asset/AssetForm.tsx`

- [ ] **Step 1: 扩展 DEFAULT_PORTS / DEFAULT_ICONS**

修改 `frontend/src/components/asset/AssetForm.tsx` 第 189-208 行：

```ts
const DEFAULT_PORTS: Record<string, number> = {
  ssh: 22,
  mysql: 3306,
  postgresql: 5432,
  mssql: 1433,
  // sqlite 无端口,不放
  redis: 6379,
  mongodb: 27017,
  kafka: 9092,
  k8s: 6443,
};

const DEFAULT_ICONS: Record<string, string> = {
  ssh: "server",
  mysql: "mysql",
  postgresql: "postgresql",
  mssql: "mssql",
  sqlite: "sqlite",
  redis: "redis",
  mongodb: "mongodb",
  kafka: "kafka",
  k8s: "kubernetes",
  serial: "usb",
};
```

确认 `mssql` 与 `sqlite` 在 `@opskat/ui` 的 IconPicker / lucide 库里有对应图标——若没有就用通用 `database` 图标做兜底（在 IconPicker map 里加映射，或临时用 lucide 的 `Database` 图标）。

- [ ] **Step 2: 加 path state**

在 AssetForm 函数体的 useState 区加：

```ts
const [path, setPath] = useState("");
```

- [ ] **Step 3: driver 下拉 options 扩充**

找 driver Select 渲染处（在 AssetForm 内或如果用了 SelectItem 列表，加 mssql / sqlite）。grep 定位：

Run: `grep -n "SelectItem.*mysql\|SelectItem.*postgresql\|driver.*Select" /Users/codfrm/Code/opskat/opskat/frontend/src/components/asset/AssetForm.tsx`

在该位置加：

```tsx
<SelectItem value="mssql">SQL Server</SelectItem>
<SelectItem value="sqlite">SQLite</SelectItem>
```

- [ ] **Step 4: 处理 onDriverChange — 切换到 sqlite 清空 host/port/user/pass**

修改约第 838 行的 `onDriverChange`：

```ts
const onDriverChange = (newDriver: string) => {
  setDriver(newDriver);
  if (newDriver === "sqlite") {
    setHost(""); setPort(0); setUsername(""); setPassword("");
    setSshTunnelId(0);
    setIcon(DEFAULT_ICONS["sqlite"] || "database");
  } else {
    setPort(DEFAULT_PORTS[newDriver] || 3306);
    setIcon(DEFAULT_ICONS[newDriver] || "mysql");
    setPath("");
    if (newDriver !== "postgresql") setSslMode("disable");
  }
};
```

- [ ] **Step 5: 序列化 — driver=sqlite 写 path 不写 host**

修改约第 936 行 `const cfg: DatabaseConfig = ...`：

```ts
const cfg: DatabaseConfig = { driver };
if (driver === "sqlite") {
  cfg.path = path;
  if (database) cfg.database = database;
  if (readOnly) cfg.read_only = readOnly;
} else {
  cfg.host = host;
  cfg.port = port;
  cfg.username = username;
  if (database) cfg.database = database;
  if (driver === "postgresql" && sslMode !== "disable") cfg.ssl_mode = sslMode;
  if ((driver === "mysql" || driver === "mssql") && tls) cfg.tls = tls;
  if (readOnly) cfg.read_only = readOnly;
}
```

- [ ] **Step 6: 编辑回填 — 读 cfg.path**

修改约第 546 行：

```ts
setDriver(cfg.driver || "mysql");
setPath(cfg.path || "");
```

- [ ] **Step 7: 提交按钮校验 — sqlite 时 path 非空**

找 submit 按钮 disabled 判断处，按 driver 增加条件：

```ts
const isValid = driver === "sqlite" ? !!path : !!host;
```

- [ ] **Step 8: 把 path props 传给 DatabaseConfigSection**

```tsx
<DatabaseConfigSection
  ...
  path={path}
  setPath={setPath}
/>
```

- [ ] **Step 9: 跑测试 + 跑 dev**

Run: `cd frontend && pnpm test AssetForm -- --run`
Expected: PASS（已有用例不破）

Run: `make dev`
打开 desktop app，创建 SQLite 资产 → 浏览选文件 → 保存 → 重新打开 → 字段回填。

- [ ] **Step 10: Commit**

```bash
git add frontend/src/components/asset/AssetForm.tsx
git commit -m "🎨 AssetForm 支持 mssql/sqlite 资产创建与编辑"
```

---

## Task 16: i18n 文案

**Files:**
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`
- Modify: `frontend/src/i18n/locales/en/common.json`

- [ ] **Step 1: 找 asset.database 相关 key 在 zh-CN/common.json 的位置**

Run: `grep -n "asset.database\|asset\":" /Users/codfrm/Code/opskat/opskat/frontend/src/i18n/locales/zh-CN/common.json | head -10`

确认 asset 节点结构。

- [ ] **Step 2: 添加 zh-CN 新 key**

在 `frontend/src/i18n/locales/zh-CN/common.json` 的 `asset.database`（或合适层级）加：

```json
{
  "database": {
    "driver": {
      "mysql": "MySQL",
      "postgresql": "PostgreSQL",
      "mssql": "SQL Server",
      "sqlite": "SQLite"
    },
    "sqlite": {
      "path": {
        "label": "数据库文件路径",
        "placeholder": "/Users/.../my.db",
        "browse": "选择文件…"
      }
    },
    "mssql": {
      "readonly": {
        "hint": "SQL Server 仅通过策略拦截写操作，连接级不强制只读"
      }
    }
  }
}
```

（具体放置层级按现有 i18n 结构调整。）

- [ ] **Step 3: 添加 en 新 key**

同样位置加：

```json
{
  "database": {
    "driver": {
      "mysql": "MySQL",
      "postgresql": "PostgreSQL",
      "mssql": "SQL Server",
      "sqlite": "SQLite"
    },
    "sqlite": {
      "path": {
        "label": "Database file path",
        "placeholder": "/Users/.../my.db",
        "browse": "Choose file…"
      }
    },
    "mssql": {
      "readonly": {
        "hint": "SQL Server enforces read-only via policy only, not at the connection level"
      }
    }
  }
}
```

- [ ] **Step 4: 跑前端 lint + test**

Run: `cd frontend && pnpm lint`
Expected: PASS

Run: `cd frontend && pnpm test -- --run`
Expected: PASS（i18n missing key 警告应该清零）

- [ ] **Step 5: Commit**

```bash
git add frontend/src/i18n/locales/
git commit -m "📄 i18n 新增 mssql/sqlite 资产文案"
```

---

## Task 17: 手动 e2e 验证

**Files:** 无代码改动；建一个简短的验证 checklist

- [ ] **Step 1: 起 docker MSSQL**

```bash
docker run -e "ACCEPT_EULA=Y" -e "MSSQL_SA_PASSWORD=YourStrong!Pass" \
  -p 1433:1433 -d --name opskat-mssql-test \
  mcr.microsoft.com/mssql/server:2022-latest
```

等约 30s 让服务起来。

- [ ] **Step 2: 起 desktop app**

Run: `make dev`

- [ ] **Step 3: 创建 MSSQL 资产并验证**

- 资产管理 → 新建资产 → 类型 Database → driver=SQL Server → host=localhost / port=1433 / user=sa / password=YourStrong!Pass → 保存
- 查询面板打开该资产 → `SELECT @@version` → 返回 SQL Server 版本
- 创建表 → 浏览（OpenTable）→ PK / 列名应正确
- 勾上 ReadOnly 重连 → `INSERT` 应被 policy 拦截（前端弹确认对话框）

- [ ] **Step 4: 创建 SQLite 资产并验证**

- 准备一个本地 `.db` 文件：`sqlite3 /tmp/opskat-test.db "CREATE TABLE t(id INTEGER PRIMARY KEY, name TEXT)"`
- desktop app → 新建资产 → driver=SQLite → 浏览选 `/tmp/opskat-test.db` → 保存
- 查询面板：`SELECT * FROM t` → 返回空结果
- 创建 INSERT → ReadOnly 关闭可写；勾上 ReadOnly 重连后写入应被 `PRAGMA query_only` 拒绝（错误信息含 "read-only"）
- OpenTable 浏览表 → 列与 PK 正常显示

- [ ] **Step 5: 清理 docker**

```bash
docker rm -f opskat-mssql-test
```

- [ ] **Step 6: 文档化结果**

无需 commit；把验证结果回报给用户。如发现 bug，新建子任务修复。

---

## Self-Review

完成所有 Task 后跑一次自审：

- [ ] **Spec coverage：** 比对 spec 每一节，确认 plan 都有对应 Task。
  - Spec §3 触点表 → Task 1-16 全覆盖 ✓
  - Spec §4 数据模型 → Task 1, 2 ✓
  - Spec §5 connpool → Task 4, 5, 6, 7 ✓
  - Spec §6 query_svc → Task 8, 9, 10, 11 ✓
  - Spec §7 前端 → Task 13, 14, 15 ✓
  - Spec §8 日志 → 嵌入 Task 6, 7（DialDatabase / setReadOnly 日志）✓
  - Spec §9 测试 → 每个 Task 内的 TDD 步骤 + Task 17 手动 e2e ✓
  - Spec §10 风险 → 在实施过程中观察；如 `INFORMATION_SCHEMA.TABLE_CATALOG` 权限问题或 modernc/sqlite build tag 问题暴露，加补丁 Task ✓

- [ ] **占位符扫描：** 全文搜 "TODO" / "TBD" / "implement later"。无。
- [ ] **类型一致性：** `DriverMSSQL` / `DriverSQLite` / `Path` 三个新名字在所有 Task 拼写一致。`TableImportBatchRequest.Tables` 在 Task 11 新增，Task 17 手动 e2e 不依赖。
- [ ] **Commit 节奏：** 16 个功能 commit + 1 个手动验证 = 17 步，每步 ≤ 30 分钟。

如自审发现问题，inline 修复。

---

## 完成判定

- [ ] 所有 Task 的 Step 都打勾
- [ ] `go test ./...` 通过
- [ ] `cd frontend && pnpm test -- --run` 通过
- [ ] `make lint` 通过
- [ ] `cd frontend && pnpm lint` 通过
- [ ] Task 17 手动 e2e 全部通过
- [ ] Spec 的"范围之外"项确认没有意外引入
