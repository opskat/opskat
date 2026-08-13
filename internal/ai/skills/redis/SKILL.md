---
name: redis
description: "Run Redis commands against a Redis asset via exec. Covers command syntax, the db scope parameter, and why SELECT must not be used."
---

# Redis assets

## Command syntax

Pass the Redis command verbatim as `command`:

- `GET mykey`
- `HGETALL user:1`
- `SET key value EX 3600`
- `SCAN 0 MATCH prefix:* COUNT 100`

## Scope

Use the `scope` parameter to pick the database number (0-15), e.g. `scope: "3"`.

**Do NOT send `SELECT`.** It is rejected outright before execution — connections
are pooled, so a per-connection database switch would corrupt another caller's
selection. `scope` is the only correct way to switch databases.

## Notes

- Results are returned as JSON.
- Credentials are resolved automatically from the app's encrypted store; never
  ask the user for a password.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | |
| `port` | number | no | Defaults to `6379` |
| `username` | string | yes | Use `"default"` for Redis's built-in default user; copied to new credential metadata |
| `password` | string | no | **Write-only.** Creates a managed password credential |
| `credential_id` | number | no | Existing managed password credential ID |
| `redis_db` | number | no | Default DB index (0-15) |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches |

`password` and `credential_id` are mutually exclusive. Plaintext is never returned and is
materialized under top-level `credential_name` (default: final asset name).
