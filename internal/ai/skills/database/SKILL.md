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
| `host` | string | yes | |
| `port` | number | yes | 3306 (mysql) / 5432 (postgresql) / 1433 (mssql) |
| `username` | string | yes | |
| `password` | string | no | Stored encrypted; never echoed back |
| `driver` | string | yes | `"mysql"`, `"postgresql"`, or `"mssql"` |
| `database` | string | no | Default database; empty string clears it |
| `read_only` | string | no | `"true"` enables read-only mode |
| `query_timeout_seconds` | number | no | Per-query timeout override, in seconds |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches |

`"sqlite"` is not creatable through `put_asset`: the validator requires `host` + `port` +
`username` unconditionally, and SQLite doesn't use any of them, so the request always fails
validation. Create SQLite assets from the desktop UI instead.
