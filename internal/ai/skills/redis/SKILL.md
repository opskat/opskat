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

**Do NOT send `SELECT`.** Connections are pooled, so `SELECT` either has no
effect or corrupts another caller's database selection. `scope` is the only
correct way to switch databases.

## Notes

- Results are returned as JSON.
- Credentials are resolved automatically from the app's encrypted store; never
  ask the user for a password.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | |
| `port` | number | yes | No server-side default — pass `6379` explicitly |
| `username` | string | yes | Required by validation even for legacy single-password Redis (no ACL); pass `"default"`, Redis's built-in default user |
| `password` | string | no | Stored encrypted; never echoed back |
| `redis_db` | number | no | Default DB index (0-15) |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches |
