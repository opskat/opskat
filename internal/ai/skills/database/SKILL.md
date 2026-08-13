---
name: database
description: "Run SQL against a database asset (MySQL, PostgreSQL, SQL Server, SQLite) via exec. Covers SQL syntax and the database scope parameter."
---

# Database assets

## Command syntax

Pass the SQL verbatim as `command`:

- `SELECT id, name FROM users LIMIT 10`
- `SHOW TABLES`
- `DESCRIBE orders`
- `UPDATE users SET active = 0 WHERE id = 3`

## Scope

Use `scope` to override the default database for this call, e.g. `scope: "analytics"`.

## Notes

- Reads (`SELECT` / `SHOW` / `DESCRIBE` / `EXPLAIN`) return rows as JSON;
  writes return an affected-row count.
- Multi-statement input is split and each statement is policy-checked
  separately, so a read cannot smuggle a write past approval.
- Credentials are resolved automatically; never ask the user for a password.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `driver` | string | yes | `"mysql"`, `"postgresql"`, `"mssql"`, or `"sqlite"` |
| `host` | string | non-SQLite | Hostname or IP |
| `port` | number | no | Defaults by driver: 3306 / 5432 / 1433 |
| `username` | string | non-SQLite | Copied to newly created credential metadata |
| `password` | string | no | **Write-only.** For non-SQLite, creates a managed password credential |
| `credential_id` | number | no | Existing managed password credential ID; rejected for SQLite |
| `database` | string | no | Default database |
| `read_only` | boolean | no | Connection-level read-only mode |
| `query_timeout_seconds` | number | no | Per-query timeout override, seconds |
| `ssh_asset_id` | number | no | SSH asset for remote connections; required by remote SQLite VFS |
| `sqlite_source` | string | SQLite only | `"local"` (default) or `"remote_ssh_vfs"` |
| `path` | string | SQLite only | Absolute database-file path |

For non-SQLite drivers, `password` and `credential_id` are mutually exclusive. Plaintext is
write-only and becomes a managed credential named by top-level `credential_name` (default:
final asset name). SQLite accepts neither password source: local SQLite must not have an SSH
asset; `remote_ssh_vfs` requires one and uses a POSIX absolute remote path.

Examples:

    put_asset(name="prod-db", type="database", config={"driver":"postgresql","host":"db.internal","username":"app","password":"..."})
    put_asset(name="local-db", type="database", config={"driver":"sqlite","path":"/var/lib/app/data.db"})
    put_asset(name="remote-db", type="database", config={"driver":"sqlite","sqlite_source":"remote_ssh_vfs","path":"/srv/data.db","ssh_asset_id":12})
