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
