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
  separately, so a read cannot smuggle a write past approval. Drivers may
  report their own statement types (e.g. `MERGE`); a type missing from the
  allow list needs confirmation unless the list contains `*`.
- Credentials are resolved automatically; never ask the user for a password.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `driver` | string | yes | `"mysql"`, `"postgresql"`, `"mssql"`, or `"sqlite"` |
| `host` | string | non-SQLite | Hostname or IP |
| `port` | number | no | Defaults by driver: 3306 / 5432 / 1433 |
| `username` | string | non-SQLite | Database account name; may be left empty under Kerberos (`auth_method: "kerberos"`) |
| `password` | string | no | **Write-only.** For non-SQLite, encrypted in the asset; does not create a credential |
| `credential_id` | number | no | Existing managed password credential ID; rejected for SQLite |
| `database` | string | no | Default database |
| `read_only` | boolean | no | Connection-level read-only mode |
| `query_timeout_seconds` | number | no | Per-query timeout override, seconds |
| `ssh_asset_id` | number | no | SSH asset for remote connections; required by remote SQLite VFS |
| `proxy_chain` | array | no | Ordered list of hops, nearest to this machine first; replaces the whole stored chain and the SSH tunnel, `[]` clears it. Layers: `{"type":"ssh","ssh_asset_id":N}`, `{"type":"socks5","host":"...","port":N,"username":"...","password":"..."}`, `{"type":"http_tunnel","url":"https://...","token":"...","timeout_seconds":N}` (first layer only). `password` / `token` are **write-only**, encrypted in the asset. Not together with `ssh_asset_id` |
| `sqlite_source` | string | SQLite only | `"local"` (default) or `"remote_ssh_vfs"` |
| `path` | string | SQLite only | Absolute database-file path |
| `driver_options` | object | no | Driver-specific fields; keys, types, required and defaults come from the selected driver's declaration. Only drivers that declare fields accept it, and undeclared keys are rejected. Password-type values are **write-only**, encrypted in the asset, and shown only as set (`driver_options_secrets_set`) when the asset is viewed |
| `tls` | boolean | no | Certificate TLS switch. The `tls_*` keys below have the same names and meaning as Redis assets; only drivers that declare the certificate field group accept them (and `tls`), any other driver rejects them naming the keys |
| `tls_insecure` | boolean | no | `true` to skip TLS certificate verification |
| `tls_server_name` | string | no | TLS SNI / server name override |
| `tls_ca_file` | string | no | Path to a CA certificate file. The three certificates share one source: give them all as paths (`tls_*_file`) or all as content (`tls_*_pem`); switching source drops what was stored in the other |
| `tls_ca_pem` | string | no | CA certificate as PEM content, stored in the asset |
| `tls_cert_file` | string | no | Path to a client certificate file (mTLS) |
| `tls_cert_pem` | string | no | Client certificate as PEM content (mTLS) |
| `tls_key_file` | string | no | Path to a client key file (mTLS) |
| `tls_key_pem` | string | no | **Write-only.** Client key as PEM content (mTLS), encrypted in the asset; shown only as set (`tls_key_pem_set`) when the asset is viewed |
| `auth_method` | string | no | `""` (password, default) or `"kerberos"`. The Kerberos keys (`auth_method` and `krb_*`) are only accepted by drivers that declare the Kerberos field group; any other driver rejects them naming the keys |
| `krb_principal` | string | Kerberos | Principal as `user@REALM`; the realm is taken from it |
| `krb_password` | string | no | **Write-only.** Principal password, encrypted in the asset; shown only as set (`krb_password_set`) when the asset is viewed. Under Kerberos the asset `password` is not needed |
| `krb_keytab_file` | string | no | Path to a keytab file (only the path is stored); used instead of `krb_password` when set |
| `krb_kdc` | string | Kerberos | One or more KDC addresses, comma-separated `host[:port]` (default port 88) |
| `krb_spn` | string | no | Pin the service principal `service/host[@REALM]`; empty lets the driver choose |

For non-SQLite drivers, `password` and `credential_id` are mutually exclusive. Plaintext is
write-only, encrypted in the asset, and never creates a managed credential. SQLite accepts
neither password source: local SQLite must not have an SSH
asset; `remote_ssh_vfs` requires one and uses a POSIX absolute remote path.

Examples:

    put_asset(name="prod-db", type="database", config={"driver":"postgresql","host":"db.internal","username":"app","password":"..."})
    put_asset(name="local-db", type="database", config={"driver":"sqlite","path":"/var/lib/app/data.db"})
    put_asset(name="remote-db", type="database", config={"driver":"sqlite","sqlite_source":"remote_ssh_vfs","path":"/srv/data.db","ssh_asset_id":12})
