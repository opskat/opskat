---
name: oss
description: "Object storage (S3-compatible) assets. Config fields for put_asset; no command surface — exec is not supported for this type."
---

# OSS assets

OSS assets connect to an S3-compatible object storage endpoint; the app browses buckets and
objects on it interactively. There is **no command surface**: `exec` is not supported for this
type, and there is nothing to script. The asset itself has no per-bucket field — bucket/object
selection happens when browsing, not at creation time.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `endpoint` | string | yes | Host, or `scheme://host[:port]` |
| `access_key_id` | string | yes | |
| `secret_access_key` | string | no | Stored encrypted; never echoed back |
| `credential_id` | number | no | Reference to a managed credential instead of an inline `secret_access_key`; 0 means none |
| `provider` | string | no | UI provider preset label (e.g. `"s3"`, `"minio"`); does not change connection behavior |
| `region` | string | no | |
| `use_path_style` | string | no | `"true"` to force path-style addressing (needed by most self-hosted S3-compatible services) |
| `use_ssl` | string | no | `"true"` to connect over HTTPS |
| `connect_timeout` | number | no | Seconds; 0 uses the default |

Example:

    put_asset(name="backups-bucket", type="oss", config={"endpoint":"s3.us-east-1.amazonaws.com","region":"us-east-1","access_key_id":"AKIA...","secret_access_key":"...","use_ssl":"true"})
