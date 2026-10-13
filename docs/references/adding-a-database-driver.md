# Adding a Database Driver

> This guide is the concrete how-to for adding a driver to the `database` asset type (MySQL, PostgreSQL, SQL Server and SQLite are the built-in ones): which interfaces to implement, what the descriptor declares, what the session contract requires, how the conformance test and the fake driver prove it, how the frontend follows the descriptor, and which files still branch on a driver name this round.
>
> Engineering principles (SOLID, Reuse first, the Fix policy) live in [`../AGENTS.md`](../../AGENTS.md); the architecture and subsystem map live in [`../ARCHITECTURE.md`](../ARCHITECTURE.md). Adding the asset type itself is a different path: see [`./adding-an-asset-type.md`](./adding-an-asset-type.md). Before editing this file, read [`../DOC-MAINTENANCE.md`](../DOC-MAINTENANCE.md).

## Core Idea: Register a Driver, Do Not Branch on Its Name

Everything that differs between SQL databases lives behind one registry, `internal/dbdriver`. A driver registers one `Driver` implementation (which carries its `Descriptor`); shared code looks the driver up by the id stored in the asset's `DatabaseConfig.Driver` and calls through it. Do not add `if cfg.Driver == ...` or `switch driver` to shared Go or TypeScript code; the guards described in [Guards](#guards-against-regressing-to-name-branches) fail the build when you do.

- Backend registry: `internal/dbdriver` (`Register`, `Lookup`, `ByName`, `List`, `IDs`). An id that is not registered yields `dbdriver.ErrDriverUnavailable`, which names the driver. Nothing falls back to MySQL.
- Frontend: `frontend/src/lib/dbDrivers/`. The `Query.ListDatabaseDrivers` binding (`internal/app/query/drivers.go`) returns every registered `Descriptor`, ordered by `Descriptor.Order`; the frontend reads the driver dropdown, form fields, icon, default port, schema grouping and which entry points to show from it.

Enumerate the built-in drivers from committed code rather than copying a list into prose:

```bash
git ls-files 'internal/dbdriver/*/*.go' | grep -v '_test.go'
git grep -n "Register(" -- internal/dbdriver/mysql internal/dbdriver/postgresql internal/dbdriver/mssql internal/dbdriver/sqlite
```

## Backend Steps

### 1. Create the package

Add `internal/dbdriver/<name>/` (the built-ins are `mysql`, `postgresql`, `mssql`, `sqlite`). The driver package may import `internal/dbdriver`, `internal/dbdriver/sqldb` and the entity layer; it must not import `internal/assettype`, because `assettype` links the drivers in (see step 5) and the import would cycle.

### 2. Declare the descriptor

`dbdriver.Descriptor` is plain, serializable data that is handed to the frontend as is:

| Field | Meaning |
| --- | --- |
| `ID` | The value stored in `DatabaseConfig.Driver`. Renaming it is a data migration. Lowercase word, `^[a-z][a-z0-9_]*$` |
| `Name` | Display name (a proper noun, not translated) |
| `Icon` | Asset icon name |
| `Order` | Display order, positive, small first. The built-ins use 10 / 20 / 30 / 40; the dropdown and the driver lists in error and help text follow it |
| `Aliases` | Extra names accepted by `--type` assertions (`postgres`, `sqlserver`, `sqlite3`); an empty slice, not `nil`, when there are none. The `id` is always accepted |
| `DefaultPort` | Default port; `0` for drivers without a port field. Declared if and only if the form uses `FieldPort` |
| `Form` | Per tab (`TabConnection`, `TabTunnel`, `TabTLS`, `TabAdvanced`) the fields the driver uses, in display order. A tab with no declared field is not shown |
| `Capabilities` | The features the driver supports; each is declared separately, undeclared means unsupported |

Form fields are `FieldHost`, `FieldPort`, `FieldUsername`, `FieldPassword`, `FieldDatabase`, `FieldSQLiteSource`, `FieldPath`, `FieldSSHAsset`, `FieldTunnel`, `FieldSSLMode`, `FieldTLS`, `FieldParams`, `FieldReadOnly`, `FieldQueryTimeout`. Declaring `FieldPassword` is also what tells the automation layer that the driver authenticates with a password (`password` / `credential_id` are rejected otherwise). The shared connection arguments (tunnel, proxy chain, credential, `read_only`, `query_timeout_seconds`) are handled once by the database asset handler (`internal/assettype/database.go`, `connection_args.go`); a driver only owns the fields specific to it.

Capabilities (`dbdriver.Capabilities()` is the full list): `CapSchemas` (group by schema under a database), `CapCreateDatabase`, `CapViewDDL`, the four object kinds `CapViews` / `CapProcedures` / `CapFunctions` / `CapTriggers`, and the write-path entries `CapGridEdit`, `CapCreateTable`, `CapAlterTable`, `CapDropTable`, `CapTruncateTable`, `CapImport`, `CapExportSQL`, `CapCopySQL`, plus `CapReadOnly` (connection-level read-only). See [Write path](#write-path-what-still-needs-shared-edits) before declaring any of the write-path ones.

### 3. Implement the config rules

`dbdriver.ConfigRules`: `ValidateConfig` (before persisting), `CreateDefaults`, `ValidateCreateArgs`, `ApplyCreateArgs`, `ApplyUpdateArgs` and `ConnectionView` (what opsctl / AI show about the asset). Arguments arrive through `dbdriver.Args`, which wraps the shared `Arg*` helpers; do not re-parse the raw map. Host-and-port drivers embed `dbdriver.NetworkRules{DefaultPort: ...}` and get all six (the built-in MySQL, PostgreSQL and SQL Server do); SQLite implements its own.

Rules the entity layer cannot import are reached through the hook `asset_entity.SetDatabaseDriverConfigValidator`, which `internal/dbdriver/registry.go` installs; you do not touch the entity layer.

### 4. Implement the connector and the session

`dbdriver.Connector` is `Open(ctx, OpenRequest) (Session, error)` plus `SessionPerDatabase() bool`.

- `OpenRequest` carries the `Config`, the already-decrypted `Password`, and a `Transport`. The shared code has resolved tunnels, proxies, proxy chains and `.local` handling already: every network connection the driver makes must go through `Transport.Dial` when it is non-nil; a driver whose database is a file on a remote host opens it through `Transport.RemoteFS`. The driver never reads `Proxy` / `ProxyChain` / `SSHAssetID` itself.
- `Open` returns only after the database is reachable. A config that asks for `ReadOnly` must be honored; a driver that does not declare `CapReadOnly` must reject it with `dbdriver.ErrUnsupported` rather than open a writable session.
- `SessionPerDatabase()`: `true` when the database name is part of the connection (one session per asset and database); `false` when one session covers the whole asset and the database name is only a namespace in it (SQLite). Shared code decides the granularity of session reuse from this and does not look at the driver name.

`dbdriver.Session` is what shared code holds. It must be safe for concurrent use. Everything returns structured results; shared code never builds dialect SQL and never touches the driver's connection:

| Group | Methods |
| --- | --- |
| Execution (required) | `Execute`, `ExecutePaged` (one page plus the total in `RowSet.Total`) |
| Tree browsing | `ListDatabases`, `ListTables`, `TableMetadata`, `ListObjects`, `ObjectSource` |
| Table browsing | `OpenTable`, `TableRows`, `CountTableRows`, `StreamTableRows` (structured `FilterItem` / `SortItem`, no SQL text) |
| Text output | `TableDDL`, `StarterQuery` |
| Lifecycle | `Close` |

Anything the driver does not provide returns an error wrapping `dbdriver.ErrUnsupported` and runs no statement. `Execute` and `ExecutePaged` must always work.

Drivers on top of `database/sql` should not re-implement the session. `internal/dbdriver/sqldb` provides `sqldb.NewSession(db, dialect, ...)` and the optional dialect interfaces that switch the groups above on: `sqldb.Dialect` (paging and counting), `sqldb.Catalog` (tree browsing), `sqldb.Tables` (table browsing, quoting, starter query) and `sqldb.DDL` (table DDL); a dialect that does not implement one makes the matching `Session` methods return `ErrUnsupported`. `sqldb.Establish` pings the database and applies read-only, `sqldb.ParseParams` parses the `params` field, and `sqldb.LimitOffset` is the common paging dialect. The built-in packages are the reference: `internal/dbdriver/mysql/` has `mysql.go` (descriptor and registration), `connect.go` (`Open`, DSN), `catalog.go`, `table.go`, `ddl.go`.

A driver whose session does not wrap a `*sql.DB` at all (as an extension driver will not) implements `dbdriver.Session` directly; the fake driver below is the example.

### 5. Register and link

Register in the package's `init()`:

```go
func init() {
	dbdriver.Register(driver{NetworkRules: dbdriver.NetworkRules{DefaultPort: descriptor.DefaultPort}})
}
```

`Register` panics when the id or any alias is already taken by another driver. Then add a blank import of the package to `internal/dbdriver/builtin/builtin.go`; `internal/assettype/database.go` blank-imports `builtin`, so both program entry points (the desktop app and `opsctl`) link the driver. Nothing else in the backend changes: asset validation, `opsctl create asset`, the missing-`driver` error text, `--type` assertions (`internal/ai/permission/type_assert.go` resolves names through the registry), the `exec --help` driver list, test connection, the query panel and AI / opsctl SQL execution all go through `dbdriver.Lookup` / `ByName` / `List`.

## Proving the Driver: Conformance and the Fake

`internal/dbdriver/dbdrivertest` is the test support:

- `dbdrivertest.RunConformance(t, d, newSession)` is the test every registered driver must pass. It checks the descriptor (id and alias format, display name, icon, a declared `Order`, non-nil `Aliases` / `Capabilities`, known tabs / fields / capabilities each used once, default port present exactly when the form has a port field), that the registry resolves the driver by id and alias, that `Execute` and `ExecutePaged` return well-formed results that serialize to JSON, that read-only is refused when `CapReadOnly` is not declared, and that undeclared object types are refused with `ErrUnsupported` without running a statement. `newSession` returns a session without a real database: a stub connection for `database/sql` drivers (`go-sqlmock`), a temporary file for file-based ones. Each built-in package calls it from `connect_test.go`; do the same for yours.
- `RunBrowseConformance` and `RunTableConformance` check the declared browsing and table operations; they need a session that holds data, so they are run against the fake and against drivers that can provide data.
- Driver-specific SQL text (quoting, paging, filter semantics, DDL) is pinned by tests in the driver package (`catalog_test.go`, `table_test.go`, `ddl_test.go`); `internal/dbdriver/sqldb/sqldbtest` has a shared filter-semantics suite that each built-in dialect runs.
- `dbdrivertest.NewFake()` is a driver registered only in tests (`dbdrivertest.Register(t, fake)` unregisters it at cleanup). It deliberately has different rules from every built-in (only `host` is required, its own default port and order) and its session is an in-memory table, not a `database/sql` handle. Backend tests that go from asset validation through connection, listing, opening a table and paging via the fake, and frontend tests that fill the driver dropdown and forms from `FAKE_DRIVER` (`frontend/src/__tests__/dbDriverFixtures.ts`), are what show that a new driver needs no shared-code edit. A shared-code change that a fake driver cannot exercise is a sign a name branch crept in.

## Frontend: Follow the Descriptor

The frontend does not know driver names. `frontend/src/lib/dbDrivers/index.ts` exports:

- `loadDatabaseDrivers()` / `useDatabaseDrivers()` (`loading` / `ready` / `error` states; a failed fetch is shown as an error, there is no built-in substitute list), `findDatabaseDriver(drivers, id)`.
- `driverHasCapability(driver, capability)` and `driverUsesField(driver, field)`: the only way shared components ask whether to show an entry point or a field. When an asset's driver is not registered, the panel shows "driver unavailable" naming it, the asset stays editable and deletable.
- `driverFormGroups` and `registerDriverFormField(field, descs)`: the asset form renders, per tab, the controls registered for the fields the descriptor lists. The shared controls are registered in `genericFields.ts`; SQLite's source switch, file picker and SSH asset selector are registered in `sqliteFields.tsx`. A new driver that reuses existing `FormField` values needs no frontend change; one that needs a control that does not exist yet registers it with `registerDriverFormField`, in `src/lib/dbDrivers/`, not by a branch in the form.

Display names, icons, default ports and schema grouping (`CapSchemas`) come from the descriptor. Structured browsing (`ListDatabases`, `ListTables`, `OpenTable`, `QueryTableRows`, `CountTableRows`, export cursors, `GetTableDDL`, `GetStarterQuery`) goes through the `Query` binder, which calls the driver's session.

## Write Path: What Still Needs Shared Edits

The read path is fully behind the driver. The write path stays in the frontend this round: grid editing, create / alter / drop / truncate table, create database, import, export as SQL and copy as SQL still generate dialect SQL in TypeScript (and the transitional `ExecuteTableImport` binder in Go), controlled only by which capabilities the descriptor declares. A new driver therefore has two honest options:

- Do not declare the write-path capabilities (`CapGridEdit`, `CapCreateTable`, `CapAlterTable`, `CapDropTable`, `CapTruncateTable`, `CapImport`, `CapExportSQL`, `CapCopySQL`, `CapCreateDatabase`). Their entries are hidden for the driver and no shared file needs editing.
- Declare them and add the driver's dialect to the files below. Those edits are exactly what the exemption lists allow, and nothing else may name a driver.

The lists are shrink-only: a file leaves when its logic moves into the driver, and nothing is added.

Go (`driverBranchExempt` in `internal/archtest/driver_branch_test.go`):

- `internal/service/query_svc/table_import.go`: `disableForeignKeyChecks` issues a different statement per driver.

TypeScript (`driverBranchExempt` in `frontend/eslint.config.js`): `src/components/query/AlterTableDialog.tsx`, `CreateDatabaseDialog.tsx`, `DatabaseTree.tsx` (truncate falls back to `DELETE FROM` on SQLite), `ExportTableDataDialog.tsx`, `ImportTableDataDialog.tsx`, `TableDataTab.tsx` (copy-as-SQL table naming, the missing-primary-key hint), and `src/lib/tableEdit.ts`, `tableExport.ts`, `tableImport.ts`, `tableSql.ts`.

Also outside this seam for now (the spec's out-of-scope list): parsing the AI SQL permission policy per dialect (`internal/ai/policy/query_policy.go` parses with the TiDB parser) and per-dialect keyword completion in the SQL editor. A driver's SQL is checked by the same policy as every other.

## Guards Against Regressing to Name Branches

- Go: `TestNoDriverBranchInSharedCode` in `internal/archtest/driver_branch_test.go` fails on a comparison or `switch` against `asset_entity.DriverMySQL` / `DriverPostgreSQL` / `DriverMSSQL` / `DriverSQLite`, or against a registered driver id or alias string where the other side is a driver value, in any production Go file outside `internal/dbdriver/`. `TestCheckDriverBranch` pins the detection on sample sources (violations reported; driver packages, exempted files and test files not). `TestDriverBranchExemptionsStillNeeded` fails when an exempted file no longer branches, so the list can only shrink.
- TypeScript: the `no-restricted-syntax` selectors in `frontend/eslint.config.js` reject comparing against a driver-name literal (`===`, `!==`, `==`, `!=`, `case`) under `src/`, except in `src/lib/dbDrivers/`, test files and the exemption list; `frontend/src/__tests__/eslint-harness.test.ts` covers both directions.
- Hand-written driver lists: `opsctl exec --help` and the missing-`driver` errors are generated from the registry. Two static texts cannot be generated, so `internal/archtest/driver_text_test.go` compares them with the registry (ids, aliases, default ports): `internal/ai/skills/database/SKILL.md` and `plugin/opsctl/skills/opsctl/references/commands.md`. Adding a driver or alias fails those tests until the texts are updated.

## Checklist

1. `internal/dbdriver/<name>/`: descriptor, config rules, `Open` / `SessionPerDatabase`, session (usually `sqldb.NewSession` plus the dialect interfaces), `init()` registration.
2. Blank import in `internal/dbdriver/builtin/builtin.go`.
3. A `connect_test.go` that calls `dbdrivertest.RunConformance`, plus tests for the driver's own SQL text.
4. Declare only the capabilities the driver actually supports; write-path ones only together with their dialect in the exempted files.
5. A new form field with no existing control: `registerDriverFormField` in `src/lib/dbDrivers/`, and its i18n keys in both locales.
6. Update the driver names in `internal/ai/skills/database/SKILL.md` and `plugin/opsctl/skills/opsctl/references/commands.md` (the archtest fails until you do).
7. `go test ./internal/dbdriver/... ./internal/archtest/...`, then from `frontend/`: `pnpm run lint` and the related vitest files.
