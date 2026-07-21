---
name: etcd
description: "Read and write etcd keys via exec, using an etcdctl-like command syntax."
---

# etcd assets

## Command syntax

`<op> [key] [value] [--flags]` — a subset of etcdctl.

- `get /app/config`
- `get /app/ --prefix`
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

- Quote any value containing spaces or JSON: `put /k '{"a": 1}'`. An unquoted
  multi-word value is rejoined with spaces (`put /k hello world` sets the value
  to `hello world`), so quote it whenever the intent should be unambiguous.
- `--lease` is hexadecimal, matching etcdctl.
- Two-word ops (`lease grant`, `member list`, `endpoint status`) are written with
  a space, exactly as shown.
- Unknown flags are rejected rather than ignored.
- The `scope` parameter is not used by etcd assets.
