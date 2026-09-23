---
name: redis
description: "Run Redis commands against a Redis asset (standalone, cluster or sentinel) via exec. Covers command syntax, what scope means in each mode (db number or cluster node), cluster command routing, and why SELECT must not be used."
---

# Redis assets

## Command syntax

Pass the Redis command verbatim as `command`:

- `GET mykey`
- `HGETALL user:1`
- `SET key value EX 3600`
- `SCAN 0 MATCH prefix:* COUNT 100`

## Scope

A Redis asset runs in one of three modes (`mode` in its config); `scope` means
something different in each.

**Standalone and sentinel** — `scope` is the database number (0-15), e.g.
`scope: "3"`. Omit it to use the asset's configured database. In sentinel mode
every command goes to the master the sentinels currently report.

**Cluster** — a cluster has only db0; `scope` is a node address `host:port`.
Each command is routed as follows:

1. Commands that name keys (`GET`, `HGETALL`, `SET`, …) are routed to the master
   that owns the key's slot; `scope` is ignored. Multi-key commands whose keys
   hash to different slots fail with Redis's `CROSSSLOT` error — use hash tags
   (`{user:1}:a`, `{user:1}:b`) or run them one key at a time.
2. `PING`, `ECHO`, `TIME`, `COMMAND …` and `CLUSTER …` run on any reachable node
   when `scope` is omitted (or on the `scope` node when given).
3. Every other keyless command (`SCAN`, `KEYS`, `DBSIZE`, `INFO`, `CONFIG GET`,
   `FLUSHDB`, …) only sees one node's data, so it **requires** `scope` set to a
   node, e.g. `scope: "10.0.0.2:7002"`. Without it (or with a db number / unknown
   address) the call fails and the error lists the current master addresses —
   pick one and retry, once per master if you need the whole cluster. A replica
   address is accepted too (useful for read-only diagnostics).

Cluster results carry `node` (the node that executed the command) and `route`
(`key`, `scope` or `any`) so you can tell how much of the cluster a result covers.

**Do NOT send `SELECT`.** It is rejected outright before execution — connections
are pooled, so a per-connection database switch would corrupt another caller's
selection; a cluster has only db0 anyway. `scope` is the only correct way to
switch databases.

## Notes

- Results are returned as JSON.
- Credentials are resolved automatically from the app's encrypted store; never
  ask the user for a password.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `mode` | string | no | `standalone` (default), `cluster` or `sentinel` |
| `host` | string | standalone | Standalone only |
| `port` | number | no | Standalone only. Defaults to `6379` |
| `nodes` | string[] | cluster, sentinel | `host:port` list: cluster seed nodes, or the sentinel nodes |
| `master_name` | string | sentinel | Name of the master group monitored by the sentinels |
| `username` | string | yes | Data-node user. Use `"default"` for Redis's built-in default user |
| `password` | string | no | **Write-only.** Data-node password, encrypted in the asset; does not create a credential |
| `credential_id` | number | no | Existing managed password credential ID |
| `sentinel_username` | string | no | Sentinel only, when the sentinels themselves require auth |
| `sentinel_password` | string | no | Sentinel only. **Write-only.** Encrypted in the asset; managed credentials are not supported |
| `redis_db` | number | no | Default DB index (0-15). Standalone and sentinel; must be 0 in cluster mode |
| `node_address_map` | object | no | Cluster and sentinel. Maps announced `host:port` → actual `host:port` to dial, for nodes that announce addresses unreachable from here (e.g. Docker bridge IPs) |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches. Applies to every node connection |

Only the fields of the chosen `mode` are stored. `password` and `credential_id` are
mutually exclusive. Plaintext is never returned, is encrypted in the asset, and never
creates a managed credential.
