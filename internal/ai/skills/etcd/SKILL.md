---
name: etcd
description: "Read and write etcd keys via exec, using an etcdctl-like command syntax."
---

# etcd assets

## Command syntax

`<op> [key] [value] [--flags]` — a subset of etcdctl.

- `get /app/config`
- `get /app/ --prefix`
- `get '' --prefix` (whole keyspace — `get --prefix` with no key errors)
- `get /app/config --limit=10 --revision=42`
- `put /app/config 'hello world'`
- `put /app/config '{"debug": true}' --lease=694d5c0f`
- `del /app/ --prefix`
- `lease grant --ttl=3600`
- `lease revoke --lease=694d5c0f`
- `lease list`
- `member list`
- `endpoint status`
- `endpoint health`

## Notes

- `get`/`del`/`put` always require a key positional — even to read the whole
  keyspace, pass an explicit empty key: `get '' --prefix`. A bare `get --prefix`
  errors because there is no key argument.
- Quote any value containing spaces or JSON: `put /k '{"a": 1}'`. An unquoted
  multi-word value is rejoined with spaces (`put /k hello world` sets the value
  to `hello world`), so quote it whenever the intent should be unambiguous.
- `--lease` is hexadecimal, matching etcdctl.
- Two-word ops (`lease grant`, `member list`, `endpoint status`) are written with
  a space, exactly as shown.
- Unknown flags are rejected rather than ignored.
- A key/value token starting with `--` is parsed as a flag, not as data: if it
  matches a known flag name it is consumed as that flag instead (`put /k
  --limit=5` fails with "requires key and value" because `--limit=5` becomes
  the `--limit` flag, not the value); if it doesn't, the whole command is
  rejected ("unknown flag"). A single leading `-` is ordinary data and is not
  affected (`get -foo` reads key `-foo`).
- The command line is shell-tokenized but not shell-executed: `$`, `|`, `>`, `&`
  and similar shell metacharacters produce an error rather than expanding or
  redirecting. `put /k "$HOME"` fails; wrap values containing such characters in
  single quotes so they are taken literally.
- This is a narrower subset of etcdctl than it looks: range syntax (`get /a /b`
  meaning "everything from /a to /b") is not supported. Extra positionals are
  silently dropped for `get`/`del` (`get /a /b` reads only `/a`) and joined into
  the value for `put`. Use `--prefix` for range-like reads.
- The `scope` parameter is not used by etcd assets.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `endpoints` | string | yes | Comma/semicolon/newline separated `host:port` list, no scheme prefix — each entry must parse as `host:port`, so `http://`/`https://` is rejected with `etcd endpoint必须为host:port格式`: e.g. `"10.0.1.5:2379,10.0.1.6:2379"` |
| `username` | string | no | Copied to newly created credential metadata |
| `password` | string | no | **Write-only.** Creates a managed password credential |
| `credential_id` | number | no | Existing managed password credential ID |
| `tls` | bool | no | `true` to enable TLS |
| `tls_insecure` | bool | no | `true` to skip TLS certificate verification |
| `tls_server_name` | string | no | TLS SNI / server name override |
| `tls_ca_file` | string | no | Path to a CA certificate file |
| `tls_cert_file` | string | no | Path to a client certificate file (mTLS) |
| `tls_key_file` | string | no | Path to a client key file (mTLS) |
| `dial_timeout_seconds` | number | no | Connection dial timeout override, in seconds |
| `command_timeout_seconds` | number | no | Per-command timeout override, in seconds |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches |

`password` and `credential_id` are mutually exclusive. Plaintext is never returned and is
materialized under top-level `credential_name` (default: final asset name).
