---
name: oss
description: "List, read, upload, copy, move, delete and presign objects in S3-compatible object storage via exec, using a family + verb + target command syntax."
---

# OSS assets

OSS assets connect to an S3-compatible object storage endpoint (AWS S3, MinIO, Aliyun OSS,
…). The asset names the endpoint and credentials only — the bucket is part of every
command's target, not part of the asset.

## Command syntax

`<family> <verb> [target] [--flags]` — `family` is `bucket` or `object`, `verb` is the
operation, and `target` is `<bucket>/<key>` (no leading slash).

Every example below is literally executable as written. Optional flags are not marked with
brackets; each useful combination is spelled out on its own line.

### bucket

- `bucket list`

### object

- `object list backups`
- `object list backups/2026/`
- `object list backups/2026/ --max-keys=100`
- `object list backups/2026/ --max-keys=100 --after=2026/03/db.sql.gz`
- `object stat backups/2026/db.sql.gz`
- `object get backups/app.conf` (returns the content inline, truncated to 64 KiB)
- `object get backups/app.conf --max-bytes=4096`
- `object get backups/2026/db.sql.gz --file=/tmp/db.sql.gz` (streams to a local file instead)
- `object put backups/2026/db.sql.gz --file=/tmp/db.sql.gz`
- `object put backups/app.conf --file=/tmp/app.conf --content-type=text/plain`
- `object copy backups/2026/db.sql.gz --to=archive/db-2026.sql.gz`
- `object move backups/2026/db.sql.gz --to=archive/db-2026.sql.gz`
- `object delete backups/2026/old.log`
- `object presign backups/2026/db.sql.gz`
- `object presign backups/2026/db.sql.gz --expiry=600`
- `object presign uploads/inbox/report.pdf --method=put --expiry=600` (denied by the default policy, see Notes)

## Flag reference

Every flag each verb accepts — anything else is rejected rather than ignored. **Bold**
flags are required: omitting one does not default it, the command is rejected before it
runs. Verbs not listed here take no flags at all.

- `object list`: `--max-keys`, `--after`
- `object get`: `--file`, `--max-bytes`
- `object put`: **`--file`**, `--content-type`
- `object copy`: **`--to`**
- `object move`: **`--to`**
- `object presign`: `--expiry`, `--method`

## Flag values

Checked before any approval dialog, so a wrong value costs nothing:

- `--max-keys`, `--max-bytes` and `--expiry` must be plain decimal integers: `1000`, not
  `1,000`, `1_000`, `1e3` or `3.0`.
- `--method` is `get` or `put`, lower case exactly. Nothing else is accepted, and there is
  no case folding: `GET` is an error rather than a synonym. Omitting it means `get`, so a
  bare `object presign` requests `object.presign.read`.
- `--expiry` defaults to 3600 seconds when omitted.
- `--file` must be an **absolute** local path. The approved command string is all that
  identifies the file, and a relative path resolves against a working directory that is not
  part of it, so it would land somewhere unpredictable.
- `--file` and `--max-bytes` cannot be given together: they select two different behaviors
  of `object get` (stream the whole object to disk, or return the first bytes inline), so
  one of them would have to be ignored.
- `--max-bytes` defaults to 64 KiB and is capped at 1 MiB. A larger value is reduced to the
  cap rather than rejected, and `truncated` then reports that the object was longer.
- `--max-keys` is capped at 1000. A larger value is **rejected**, not reduced: silently
  returning fewer keys than asked would break `--after` pagination against what the caller
  expected to page through.
- `--expiry` is capped at 604800 seconds (7 days) and is also **rejected** rather than
  reduced: a longer-lived presigned URL is what S3-compatible backends refuse to sign in
  the first place, so a value over the cap would otherwise be approved and then fail.
- `--to` is a second `<bucket>/<key>` and follows the same rules as the target, including
  the requirement that it name a single object.

Not checked on this side at all — the storage service is the only thing that will reject a
wrong value, after approval and a round trip:

- `--content-type` is passed through verbatim as the object's MIME type; omitting it lets
  the service decide.
- `--after` is a continuation cursor, not a filter: pass back the `nextContinuationToken`
  from the previous `object list` result to read the next page.

## Results

- `bucket list` returns `{"buckets":[…]}`.
- `object list` returns the raw listing: `prefixes` (one level of "folders"),
  `objects`, plus `isTruncated` and `nextContinuationToken` for paging.
- `object stat` returns the object's metadata.
- `object get` without `--file` returns `content` together with `encoding`
  (`utf-8`, or `base64` when the bytes are not valid UTF-8) and `truncated`. When
  `truncated` is true you read only the first bytes — re-run with `--file` rather than
  guessing at the rest.
- `object get --file` returns the byte count, the object and the local file path;
  `object put` returns the byte count and the object (bucket/key), with no `file`. The
  content never passes through the conversation either way.
- `object copy`, `object move` and `object delete` return `{"status":"ok",…}`.
- `object presign` returns the signed `url`, its `method` and `expiresIn` seconds. The URL
  is fully usable. Under raw-by-default Audit the complete signed URL — signature parameters
  included — enters the Audit result unchanged, subject only to the existing result
  capture/truncation; nothing is redacted, so treat the URL as a capability that grants
  access until it expires.

## Approval and policy rules

Approval is granted per `<action> <resource>`, where resource is `<bucket>/<key>`:

| command | permission(s) requested |
|---|---|
| `bucket list` | `bucket.list *` |
| `object list B/P` | `object.list B/P` |
| `object stat B/K`, `object get B/K` | `object.read B/K` |
| `object get B/K --file=P` | `object.read B/K` **and** a local-write check on `P` (the same gate `cp` uses when writing to this machine) |
| `object put B/K` | `object.write B/K` |
| `object copy S --to=D` | `object.read S` **and** `object.write D` |
| `object move S --to=D` | `object.read S`, `object.write D` **and** `object.delete S` |
| `object delete B/K` | `object.delete B/K` |
| `object presign B/K --method=get` | `object.presign.read B/K` |
| `object presign B/K --method=put` | `object.presign.write B/K` |

`copy` and `move` request every one of their resources: any one of them being denied
rejects the whole command, and where an allow list exists all of them must match.

Rules are written the same way, with `*` wildcards that do not cross `/` inside a key:

| rule | meaning |
|---|---|
| `*` | everything |
| `object.read mybucket` or `object.read mybucket/` | every object in the bucket, at any depth |
| `object.read mybucket/logs/` | everything under the `logs/` prefix, at any depth |
| `object.read mybucket/logs/*.gz` | only `*.gz` directly under `logs/` |
| `object.* mybucket/` | any object operation on that bucket |
| `object.presign.* *` | any presigned URL on any bucket |

## Notes

- Presigned **PUT** URLs are denied by the default policy and cannot be enabled by an
  allow rule: a signed upload URL moves write access outside this app entirely — anyone
  holding it can write that key until it expires, with no further policy, approval or
  audit. Use `object put` instead — no asset or group policy change can enable it, because
  the deny is a floor merged into every effective policy.
- Object keys may contain spaces; quote the whole target when they do, e.g.
  `object stat 'mybucket/My Report.pdf'`. Leading or trailing whitespace is rejected —
  permission rules split `<action> <resource>` at the first whitespace, so a padded name
  could never be authorized.
- Every `object` verb takes exactly one target and it must name a real key:
  `object stat mybucket` is rejected, because `mybucket` as a rule would authorize the
  whole bucket rather than one object. `object list` is the exception — listing is always
  by prefix, so a bare bucket name is normalized to `mybucket/`.
- A trailing `/` names the zero-byte "folder marker" object, so `object delete mybucket/logs/`
  deletes exactly that marker. Because the same string read as a *rule* means the whole
  prefix, such an approval is never turned into a standing grant: repeating it asks again.
  `object list` is the exception — a listing approval on `mybucket/logs/` *does* become a
  standing grant, because there the command and the rule cover the same range.
- Targets must not start with `/` or `--`, and the bucket is part of the target rather
  than of the asset — there is no per-asset default bucket.
- `object copy` and `object move` are single-object and server-side; both endpoints must
  be on this same asset. Use the `cp` tool to move data between different assets or
  between object storage and a server, and to copy whole prefixes.
- Unknown flags are rejected rather than ignored, so a typo such as `--max_keys=100` fails
  instead of silently listing the default page size.
- The command line is shell-tokenized but never shell-executed: `$`, `|`, `>` and `&`
  produce an error rather than expanding or redirecting.
- The `scope` parameter is not used by OSS assets; the target names bucket and key.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `endpoint` | string | yes | Host, or `scheme://host[:port]` |
| `access_key_id` | string | yes | |
| `secret_access_key` | string | no | **Write-only.** Encrypted in the asset; does not create a credential |
| `credential_id` | number | no | Existing managed password credential ID; mutually exclusive with `secret_access_key` |
| `provider` | string | no | UI provider preset label (e.g. `"s3"`, `"minio"`); does not change connection behavior |
| `region` | string | no | |
| `use_path_style` | bool | no | `true` to force path-style addressing (needed by most self-hosted S3-compatible services) |
| `use_ssl` | bool | no | `true` to connect over HTTPS |
| `connect_timeout` | number | no | Seconds; 0 uses the default |

Plaintext is never returned, is encrypted in the asset, and never creates a managed credential.

Example:

    put_asset(name="backups-bucket", type="oss", config={"endpoint":"s3.us-east-1.amazonaws.com","region":"us-east-1","access_key_id":"AKIAEXAMPLE","secret_access_key":"...","use_ssl":"true"})
