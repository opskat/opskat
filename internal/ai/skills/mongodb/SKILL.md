---
name: mongodb
description: "Query and modify MongoDB collections via exec, using an operation + collection + JSON query syntax."
---

# MongoDB assets

## Command syntax

`<operation> [collection] [--db=<database>] [--query=<json>]`

- `find users --query='{"filter":{"age":{"$gt":21}},"limit":10}'`
- `findOne users --query='{"filter":{"_id":"abc"}}'`
- `aggregate events --query='{"pipeline":[{"$match":{"type":"click"}}]}'`
- `countDocuments users`
- `insertOne users --query='{"document":{"name":"alice"}}'`
- `updateMany users --query='{"filter":{"active":false},"update":{"$set":{"archived":true}}}'`
- `deleteMany logs --query='{"filter":{"level":"debug"}}'`
- `listDatabases`
- `listCollections --db=analytics`

Supported operations: `find`, `findOne`, `insertOne`, `insertMany`, `updateOne`,
`updateMany`, `deleteOne`, `deleteMany`, `aggregate`, `countDocuments`,
`listDatabases`, `listCollections`. Anything else is rejected.

## Query sub-keys

`--query` is a single JSON object. Which sub-keys apply depends on the operation:

- `find`: `filter`, `sort`, `projection`, `limit`, `skip`
- `findOne`: `filter`, `projection` (`sort` / `limit` / `skip` are silently ignored)
- `insertOne`: `document` · `insertMany`: `documents`
- `updateOne` / `updateMany`: `filter`, `update`
- `deleteOne` / `deleteMany`: `filter`
- `aggregate`: `pipeline`
- `countDocuments`: `filter`

## Notes

- Always single-quote `--query` — it is JSON and contains spaces and braces.
- `find` returns at most 100 documents when `limit` is omitted. Pass an explicit `limit`
  when you need to know you have the whole result set.
- Every operation except `listDatabases` needs a database. `--db` names it; if you
  omit `--db`, the asset's configured default database is used. When the asset has
  no default database configured, omitting `--db` fails — pass it explicitly.
- `listDatabases` and `listCollections` take no collection; every other operation
  requires one.
- Omitting `--query` means "no filter": `deleteMany logs` deletes **every**
  document in the collection, and `updateMany` without an `update` sub-key fails.
  Pass an explicit `filter` whenever you mean a subset.
- Quote a collection name containing spaces (`countDocuments 'my coll'`). A
  collection name starting with `--` cannot be expressed — it is read as a flag.
- The command line is shell-tokenized but not shell-executed: `$`, `|`, `>`, `&`
  and similar shell metacharacters produce an error rather than expanding or
  redirecting. Wrap values containing them in single quotes.
- Unknown flags are rejected rather than ignored; only `--db` and `--query` exist.
- The `scope` parameter is not used by MongoDB assets; use `--db` instead.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | |
| `port` | number | no | Defaults to `27017` |
| `username` | string | yes | Copied to newly created credential metadata |
| `password` | string | no | **Write-only.** Creates a managed password credential |
| `credential_id` | number | no | Existing managed password credential ID |
| `database` | string | no | Default database |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches |

`password` and `credential_id` are mutually exclusive. Plaintext is never returned and is
materialized under top-level `credential_name` (default: final asset name). Auth source is
fixed to `admin`; it is not configurable through `put_asset`.
