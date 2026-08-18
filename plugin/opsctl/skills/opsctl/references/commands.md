# opsctl Command Reference

## list

### `list assets [flags]`

List managed server assets. Does not include description — use `get asset` to view descriptions.

**Flags**:
- `--type <string>` — Filter by asset type (e.g., "ssh")
- `--group-id <int>` — Filter by group ID (0 = all)

```bash
opsctl list assets
opsctl list assets --type ssh --group-id 2
```

### `list groups`

List all asset groups. Descriptions are not included.

```bash
opsctl list groups
```

### `list audit [flags]`

Read-only listing of stored audit log rows: time, source, asset, tool, command summary, and decision source, newest first. **AI-usable read-only entry point** — no TTY required. Rows are presented exactly as stored, no re-redaction.

**Flags**:
- `--asset <asset>` — Filter by asset (name, group/name, or numeric ID)
- `--limit <int>` — Maximum rows to show (default 20)

```bash
opsctl list audit
opsctl list audit --asset web-01 --limit 50
```

## get

### `get asset <asset>`

Get safe asset detail including registered type-specific connection metadata. When the asset
references managed authentication, the response adds `authentication` with a typed ref and
availability (`stored`/`missing` for credentials; runtime status for SSH Agent). It never
returns passwords, private keys/passphrases, kubeconfig, Agent endpoint values, or Agent
public-key blobs. Existing inline-encrypted authentication has no fabricated managed ref.

```bash
opsctl get asset web-server
opsctl get asset 1
opsctl get asset production/web-01
opsctl get asset '[web-01](opsctl://asset/1)'
opsctl get asset opsctl://asset/1
```

### `list credentials [--type password|ssh_key|ssh_agent]`

List the unified safe key-management inventory. Omit `--type` to include password credentials,
SSH-key credentials, and SSH Agent sources. Each item has a typed `ref`: `credential:<id>` or
`agent-source:<id>`. Agent availability is metadata (`ok`, `empty`, `unavailable`, or
`unsupported`), not an error or endpoint disclosure.

```bash
opsctl list credentials
opsctl list credentials --type ssh_agent
```

### `get credential <typed-ref>`

Get safe metadata and usage. A bare numeric ID is rejected as ambiguous. Credential details
include referencing assets; SSH-key detail may include its public key. Agent detail includes
sanitized identity fingerprints/comments and usage, but not endpoint values or full Agent
public keys. No command reveals password/ciphertext, private key/passphrase, signing material,
or challenges.

```bash
opsctl get credential credential:3
opsctl get credential agent-source:2
```

`--credential-id` on `create asset` remains a numeric credential-row ID, not one of these
typed detail refs.

## ssh

### `ssh <asset>`

Open interactive SSH terminal. No approval needed (human use).

- Full terminal emulation (xterm-256color)
- Terminal resize via SIGWINCH
- Exit code propagation

```bash
opsctl ssh web-server
```

## help

### `help <asset-or-type>`

Print the config contract, command syntax, and usage notes for an asset's type —
the same documentation the AI `help` tool uses before `put_asset` or `exec`.
Pass an asset name/ID, or pass a canonical type name when no asset of that type
exists yet. Read-only: never asks for approval.

```bash
opsctl help web-server
opsctl help prod-db
opsctl help 1
opsctl help kafka
```

## exec

### `exec <asset> [--type <type>] [--] <command>`

Execute a command against **any** asset type (ssh, serial, database, redis,
mongodb, etcd, kafka, k8s, oss, ...) — dispatch always comes from the asset's
real type, never from a flag or verb name.

- **ssh** assets keep the original streaming channel: stdin piping, direct
  stdout/stderr passthrough, and the remote exit code propagated as opsctl's
  exit code.
- Every other type runs through the same unified `exec` handler the AI uses
  and returns captured output (JSON for database/redis/mongodb/etcd/kafka
  reads; an affected-row / exit-code summary for writes). Command syntax is
  per-type — run `opsctl help <asset-or-type>` first if you don't already know it.

**Flags**:
- `--type <type>` — Optional assertion: fails fast (before any approval
  prompt) if the asset is not of this type. Does not select dispatch — that
  always comes from the asset's real type. Accepts three kinds of value:
  - canonical asset types: `ssh`, `serial`, `database`, `redis`, `mongodb`,
    `etcd`, `kafka`, `k8s`, `oss`;
  - protocol aliases: `exec` (ssh), `sql` / `db` (database), `mongo`
    (mongodb), `kubernetes` / `kube` (k8s);
  - database driver names: `mysql`, `postgresql` / `postgres`, `mssql` /
    `sqlserver`, `sqlite` / `sqlite3`. These assert the driver **as well as**
    the type — `--type mysql` fails on a PostgreSQL asset, which is the whole
    reason driver names are accepted rather than folded into `database`.

When the type is known, always pass this assertion. Prefer the canonical
asset type; reach for a driver name only when the dialect actually matters to
the command you are about to run. Omit it only when the type is genuinely
unknown.

**Approval flow**:
1. Command policy check (permanent allow/deny rules from the asset's own column, its group chain, and attached policy groups)
2. Still-valid grant match (24-hour grants saved by the desktop dialog's "Remember")
3. Approver selection: an interactive terminal (stdin and stderr both TTYs) prompts right there — `exec` with piped stdin does NOT count; otherwise the running desktop app shows its dialog; with neither, exit code 3 with `NEEDS AUTHORIZATION` on the first stderr line plus a paste-ready `opsctl policy allow` line

```bash
opsctl exec web-server --type ssh -- uptime
opsctl exec 1 --type ssh -- ls -la /var/log
echo "data" | opsctl exec web-server --type ssh -- cat
opsctl exec web-01 --type ssh -- systemctl restart nginx
opsctl exec prod-db --type database -- "SELECT * FROM users LIMIT 10"
opsctl exec cache --type redis -- "GET session:abc123"
opsctl exec mongo-db --type mongodb -- find users --query='{"filter":{"status":"active"}}'
opsctl exec etcd-cluster --type etcd -- get /app/config --prefix
opsctl exec events --type kafka -- topic list
opsctl exec prod-k8s --type k8s -- get pods -A
```

## batch

### `batch [args...]`

Execute multiple commands in parallel with a single approval request.
Dispatches every item by its asset's real type (database, redis, mongodb,
etcd, kafka, k8s, ...), the same coverage as `exec` — not just ssh.

**Input modes**:

1. **Stdin JSON** (AI-friendly — primary mode):
```bash
echo '{"commands":[
  {"asset":"web-01","type":"ssh","command":"uptime"},
  {"asset":"db-01","type":"database","command":"SELECT 1"},
  {"asset":"cache","type":"redis","command":"PING"},
  {"asset":"mongo-db","type":"mongodb","command":"find users --query={\"filter\":{\"status\":\"active\"}}"},
  {"asset":"events","type":"kafka","command":"topic list"}
]}' | opsctl batch
```

2. **Positional args**:
```bash
# No assertion when the type is genuinely unknown
opsctl batch 'unknown-target:hostname'
# With type prefix (type:asset:command)
opsctl batch 'ssh:web-01:uptime' 'database:db-01:SELECT 1' 'redis:cache:PING' 'mongodb:mongo-db:find users'
```

**Args format**: `asset:command` (no assertion) or `type:asset:command`.
When the type is known, always use the prefixed form. Prefer canonical asset
types (`ssh`, `database`, `redis`, `mongodb`, `etcd`, `kafka`, `k8s`,
`serial`); compatibility aliases (`exec`, `sql`, `mongo`) remain accepted.
The prefix is a **type assertion**, not a dispatch selector: a mismatch fails
that item before approval, while dispatch always uses the asset's real type.
This mirrors `exec`'s `--type` flag.

**Output**: JSON with per-command results:
```json
{
  "results": [
    {"asset_id":1,"asset_name":"web-01","type":"ssh","command":"uptime","exit_code":0,"stdout":"...","stderr":""},
    {"asset_id":2,"asset_name":"db-01","type":"database","command":"SELECT 1","exit_code":0,"stdout":"...","error":""}
  ]
}
```

**Exit code**: 0 if any command succeeded, 1 if all failed.

**Approval flow**: Policy pre-check per command → one approval step for all need-confirm commands (a single terminal prompt when interactive, a single queued desktop dialog otherwise; with neither, exit code 3 with `NEEDS AUTHORIZATION` and one paste-ready `opsctl policy allow` line per subject) → parallel execution.

## create

### `create asset [flags]`

Create any registered built-in asset type. `--name` is required; `--type` defaults to `ssh`.
The selected registered handler owns accepted fields, required combinations, and defaults.
`opsctl create asset --help` prints the current registered types; `opsctl help <type>` prints
the exact config contract. Unknown config keys fail before approval.

**Generic config**:
- `--config '<JSON object>'` — Type-owned config object
- `--config-file <path>` — File containing that JSON object; mutually exclusive with `--config`

**Authentication**:
- `--credential-id <id>` — Reuse an existing managed credential after type/auth validation
- `--password-stdin` — Preferred plaintext path; reads stdin without prompt/echo, removes one terminal LF/CRLF
- `--password <value>` — Unsafe argv path; warns about shell history, process listings, and CI logs
- `--agent-source-id <id>` and `--agent-key-fingerprint <SHA256...>` — SSH Agent identity pair; both required

Plaintext inline `--config` has the same argv risk. A plaintext `--config-file` must use
restrictive permissions, must not be committed, and should be removed afterward. Plaintext,
credential reference and Agent identity are conflicting auth sources;
the command fails instead of applying override precedence.

**Compatibility flags** remain accepted: `--host`, `--port`, `--username`, `--auth-type`,
`--driver`, `--database`, `--read-only`, `--ssh-asset`, `--kubeconfig`,
`--kubeconfig-file`, `--namespace`, `--context`, `--group-id`, `--description`, and `--icon`.
Only explicitly supplied convenience flags override matching non-secret generic config keys.
`--kubeconfig-file` remains a K8s raw-file convenience input.

The flow is parse/merge → resolve references/files and validate → approval → one
asset transaction. Approval is always required — no rule can pre-authorize a
create: an interactive terminal prompts there, otherwise the running desktop app
is asked, and with neither available opsctl exits with code 3 and a NEEDS TTY
marker telling you to run the command yourself. Plaintext is encrypted in the asset and never creates a managed credential;
create credentials in the desktop key manager and pass `--credential-id` to reuse them. Denial
or failure commits no new asset row. Successful JSON contains the asset ID and a safe
authentication reference when applicable, never supplied plaintext/ciphertext.

```bash
printf '%s\n' "$SSH_PASSWORD" | opsctl create asset --name "Web Server" --host 10.0.0.1 --username root --password-stdin
opsctl create asset --type database --name "Prod DB" --config '{"driver":"mysql","host":"db.internal","username":"app"}' --credential-id 4
opsctl create asset --type database --name "Local SQLite" --config '{"driver":"sqlite","path":"/var/lib/app.db"}'
opsctl create asset --type ssh --name "Agent Host" --host host.internal --username root --agent-source-id 2 --agent-key-fingerprint SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
opsctl create asset --type k8s --name "Prod Cluster" --kubeconfig-file ~/.kube/config --context prod
```

### `create group [flags]`

Create a new asset group. Requires approval.

**Required flags**:
- `--name <string>` — Display name

**Optional flags**:
- `--parent-id <int>` — Parent group ID for nesting (0 = top-level)
- `--icon <string>` — Icon name
- `--description <string>` — Description
- `--sort-order <int>` — Sort order within the parent; lower comes first

```bash
opsctl create group --name "Production"
opsctl create group --name "Web Tier" --parent-id 3
```

## update

### `update asset <asset> [flags]`

Update an existing asset. Only provided fields change. Requires approval — an
interactive terminal prompts there, otherwise the running desktop app is asked;
with neither available opsctl exits with code 3 and a NEEDS TTY marker telling
you to run the command yourself (like `create`/`delete`, no rule can
pre-authorize it).

**Optional flags**:
- `--name <string>` — New display name
- `--host <string>` — New hostname/IP
- `--port <int>` — New SSH port (0 = unchanged)
- `--username <string>` — New SSH username
- `--description <string>` — New description
- `--group-id <int>` — New group ID (-1 = unchanged, 0 = ungrouped)
- `--icon <string>` — New icon name (see `opsctl create asset --help` for full list)

```bash
opsctl update asset web-server --name "New Name"
opsctl update asset 1 --host 192.168.1.100 --port 2222
opsctl update asset 1 --icon kubernetes
```

### `update group <group> [flags]`

Update an existing asset group. Only provided fields change. Requires approval.

**Optional flags**:
- `--name <string>` — New display name
- `--parent-id <int>` — New parent group ID (-1 = unchanged, 0 = top-level)
- `--icon <string>` — New icon name
- `--description <string>` — New description
- `--sort-order <int>` — New sort order (-1 = unchanged)

```bash
opsctl update group 3 --name "Production"
opsctl update group staging --parent-id 1
```

## delete

Dispatches to the same `delete_asset` / `delete_group` tools the AI uses
(`internal/ai/tool/tool_handlers_crud.go`). **Always requires human
confirmation — this cannot be pre-approved or granted by any rule.** An
interactive terminal prompts there; otherwise the running desktop app is
asked; with neither available opsctl exits with code 3 and a NEEDS TTY marker
telling you to run the command yourself. Unlike `exec`, there is no
"allow always" for delete — no rule form exists for it at all.

### `delete asset <asset>`

Delete an asset (soft-delete; connection config is cleared).

```bash
opsctl delete asset old-server
opsctl delete asset 1
```

### `delete group <group> [--delete-assets]`

Delete a group. Assets in the group move to ungrouped by default; pass
`--delete-assets` to delete them as well (irreversible from the app).

**Flags**:
- `--delete-assets` — Also delete every asset in this group (default: assets move to ungrouped and survive)

```bash
opsctl delete group 3
opsctl delete group staging --delete-assets
```

## cp

### `cp [-r] <source>... <destination>`

File transfer between any two endpoints. Each endpoint is a local path, an SSH server (over SFTP), or object storage — any combination works, including server → object storage. Requires approval, and **each asset endpoint is approved separately under that asset's own policy**, before any byte moves.

**Path format**:
- Local: `/path/to/file` or `./relative`
- SSH: `<asset>:/<remote-path>`
- Object storage: `<asset>:/<bucket>/<key>`

The path after `:` must start with `/`. When the part before the colon does name an asset, writing `web-01:etc/hosts` is rejected outright rather than quietly treated as a local file called `web-01:etc/hosts` — so a typo shows up as a path error, not as "no such file". A Windows path like `C:\logs` is unaffected, since `C` is not an asset.

**Transfer modes**:
- Local → SSH: `opsctl cp ./config.yml web-server:/etc/app/config.yml`
- SSH → local: `opsctl cp 1:/var/log/app.log ./app.log`
- SSH → SSH: `opsctl cp 1:/etc/hosts 2:/tmp/hosts` (direct streaming, no local disk)
- Local → object storage: `opsctl cp ./dump.sql.gz s3-prod:/backups/2026/dump.sql.gz`
- Object storage → local: `opsctl cp s3-prod:/artifacts/app.tar ./app.tar`
- SSH → object storage: `opsctl cp web-01:/var/log/app.log s3-prod:/logs/app.log`

**Recursion and wildcards**:
- `-r` transfers a directory tree or an object prefix: `opsctl cp -r ./dist s3-prod:/bucket/releases/v2/`
- Wildcards work on the remote side too, but **must be quoted** so the local shell doesn't expand them first: `opsctl cp 'web-01:/var/log/*.log' s3-prod:/bucket/logs/`
- Several sources with one destination is fine: `opsctl cp ./a.txt ./b.txt web-01:/opt/app/`
- With `-r`, a wildcard, or several sources, the **destination must end with `/`** — the landing path is the destination plus each entry's path relative to the expansion base. There is no `cp`-style "does the destination exist" guessing.
- Recursive and glob transfers approve the source and destination directory/object-prefix scopes before listing. Files inside those approved scopes are then enumerated and transferred without a per-file approval dialog.
- The first failed entry aborts the whole transfer and reports how many of how many were transferred.
- A symlink you name directly as a source or destination is followed, matching POSIX `cp`. One encountered while expanding a wildcard or recursive walk is skipped and reported instead — that keeps `cp -r ./dir ...` from escaping into whatever the link points at.

**Copying inside one object-storage asset**: `cp` streams the object down and back up through this process. Use `opsctl exec <asset> -- "object copy <bucket>/<src> --to=<bucket>/<dst>"` instead — that copies server-side.

## policy

Permanent permission-rule management. This family replaces the removed `grant submit` (its multi-target / multi-pattern shapes carry over) and is the only place permanent allow/deny rules are written from the CLI.

**TTY gating — read vs write**:
- **AI-usable read-only entry points (no TTY needed)**: `policy show`, `policy group list`, `policy group show` (and `list audit`).
- **Write subcommands run in an interactive terminal only**: `allow` / `deny` / `rm`, the whole `group` write side (`create` / `copy` / `allow` / `deny` / `rm`), and `attach` / `detach`. Called without a TTY they exit with code 3 and a `NEEDS TTY` marker and write nothing — an AI cannot widen its own permissions. Hand the exact command line to the user to run instead.

```
opsctl policy show  <asset> | --group <group>
opsctl policy allow <asset>... | --group <group>...  [--type <asset-type>] -- <pattern>...
opsctl policy deny  <asset>... | --group <group>...  [--type <asset-type>] -- <pattern>...
opsctl policy rm    <asset>  | --group <group>  <id>
opsctl policy group list   [--type <policy-type>]
opsctl policy group show   <group-id>
opsctl policy group create --name <name> --type <policy-type>
opsctl policy group copy   <group-id> --name <name>
opsctl policy group allow  <group-id> -- <pattern>...
opsctl policy group deny   <group-id> -- <pattern>...
opsctl policy group rm     <group-id> [<entry-id>]
opsctl policy attach <asset> | --group <group>  <group-id>...
opsctl policy detach <asset> | --group <group>  <group-id>...
```

### `policy show <asset> | --group <group>`

Read-only (no TTY). For an asset it shows the **effective** merged rules — the asset's own column, its group chain, and attached policy groups, each entry marked with its layer, allow rules shadowed by a deny flagged as ineffective — plus still-valid grants with their remaining time. For `--group` it shows that group's own policy columns, for verifying a just-written group-level rule.

### `policy allow|deny <targets> [--type <t>] -- <pattern>...`

Write permanent rules. Targets are one or more assets, or `--group <group>` (repeatable) for asset groups; multiple patterns per call. Echoes the rules and asks for confirmation before writing; flags when a resulting rule is broader than the requested subject. An `allow` that would be shadowed by an in-effect deny is refused, naming the deny and its layer. Any failure in a multi-target/multi-pattern call fails the whole call — no half-written rule set.

**`--type` semantics differ by target**:
- Asset target: a type assertion that must match the asset's type; the rule shape comes from the asset itself, so it can be omitted.
- Group target: **required** — a group has no type of its own, this is the only way to select which policy shape the rules land in.
- Patterns support `*` wildcard and are normalized like the permission check does; a normalized-empty pattern is refused rather than landing an unusable rule.

### `policy rm <asset>|--group <group> <id>`

Remove one entry listed by `show`: the target's own permanent allow/deny rule, or a grant item (`g<id>` — grants are still written by the desktop dialog and remain revocable here). Rules inherited from an attached policy group are NOT removed by `rm` — `detach` the group, or edit/copy the policy group itself.

### `policy group ...` — policy groups

The third rule holder. Group IDs are `builtin:<name>`, `ext:<name>`, or a numeric user-group ID. Builtin and extension groups are read-only: `list` / `show` / `copy` / `attach` / `detach` work on them; `create` / edit / `rm` are refused with the copy-first route (`copy` into a user group, edit the copy, then `attach` it). `attach` fails before any write when the group's policy type does not match the target asset's type. `group rm <group-id> <entry-id>` removes one entry (IDs come from `show`); the one-argument form deletes the whole user group.

```bash
opsctl policy show web-01
opsctl policy allow web-01 -- 'systemctl restart nginx' 'df -h'
opsctl policy allow --group production --type ssh -- 'uptime'
opsctl policy deny web-01 -- 'rm -rf *'
opsctl policy rm web-01 2
opsctl policy group list --type query
opsctl policy group copy builtin:linux-readonly --name my-readonly
opsctl policy attach web-01 builtin:linux-readonly
opsctl policy detach --group production builtin:sql-readonly
```

## version

Print CLI version.

```bash
opsctl version
```
