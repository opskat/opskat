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
