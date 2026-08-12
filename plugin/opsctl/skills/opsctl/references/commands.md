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

## get

### `get asset <asset>`

Get asset details including description and SSH config (host, port, username, auth method).

```bash
opsctl get asset web-server
opsctl get asset 1
opsctl get asset production/web-01
```

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
1. Command policy check (allow-list/deny-list per asset)
2. Session check (grant item consumption or session auto-approve)
3. Desktop app approval (blocks until response)

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

**Approval flow**: Policy pre-check per command → single batch approval dialog for all need-confirm commands → parallel execution.

## create

Both `create` and `update` (below) dispatch to the same `put_asset` /
`put_group` tools the AI uses (`internal/ai/tool/tool_handlers_crud.go`) — the
CLI surface is unchanged from before Plan C, only the tool underneath it.

### `create asset [flags]`

Create a new asset (ssh, database, redis, mongodb, or k8s). Requires approval.

**Required flags**:
- `--name <string>` — Display name
- `--host <string>` — Hostname or IP (not required for k8s — use `--kubeconfig`/`--kubeconfig-file` instead)
- `--username <string>` — Login username (not required for k8s)

**Optional flags**:
- `--type <string>` — Asset type: "ssh" (default), "database", "redis", "mongodb", or "k8s"
- `--port <int>` — Port number (default: auto by type — 22/3306/5432/6379/27017)
- `--auth-type <string>` — SSH auth method: "password" or "key" (SSH type only)
- `--driver <string>` — Database driver: "mysql" or "postgresql" (required for database type)
- `--database <string>` — Default database name (database type)
- `--read-only` — Enable read-only mode (database type)
- `--kubeconfig <string>` — Kubeconfig YAML content (k8s type)
- `--kubeconfig-file <path>` — Path to a kubeconfig YAML file (k8s type)
- `--namespace <string>` — Default Kubernetes namespace (k8s type)
- `--context <string>` — Kubeconfig context name (k8s type)
- `--ssh-asset <asset>` — SSH asset name/ID for tunnel connection (database/redis/k8s types)
- `--group-id <int>` — Group ID (0 = ungrouped)
- `--description <string>` — Description
- `--icon <string>` — Icon name (default: auto by type). Available icons:
  - Infrastructure: server, database, cloud, monitor, laptop, router, hard-drive, globe, shield, container, cpu, network
  - Cloud: aws, azure, gcp, alicloud, tencentcloud, huaweicloud, cloudflare
  - DB/Middleware: mysql, postgresql, redis, mongodb, elasticsearch, kafka, mariadb, sqlite, rabbitmq, etcd, clickhouse
  - System/OS: docker, kubernetes, linux, windows, ubuntu, centos, debian, redhat, macos
  - DevOps: nginx, grafana, prometheus

```bash
opsctl create asset --name "Web Server" --host 10.0.0.1 --username root
opsctl create asset --type database --driver mysql --name "Prod DB" --host db.internal --username app
opsctl create asset --type database --driver postgresql --name "Analytics" --host pg.internal --port 5432 --username readonly --read-only
opsctl create asset --type mongodb --name "MongoDB" --host mongo.internal --port 27017 --username admin
opsctl create asset --type redis --name "Cache" --host redis.internal --username default
opsctl create asset --type database --driver mysql --name "DB via SSH" --host 127.0.0.1 --username app --ssh-asset web-server
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

Update an existing asset. Only provided fields change. Requires approval.

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
(`internal/ai/tool/tool_handlers_crud.go`). **Always requires desktop app
confirmation — this cannot be pre-approved or granted, even with an active
session.** Unlike `exec`/`create`/`update`, there is no "allow all" for delete.

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

## grant

### `grant submit <asset> <pattern>...` (simple mode)

Submit exec command patterns for a single asset. No stdin needed.

```bash
opsctl grant submit web-01 "systemctl *" "df -h" "uptime"
opsctl grant submit --group production "uptime" "df -h"
```

### `grant submit [options] [asset...] < input` (JSON mode)

Complex grants from stdin with per-item asset/group overrides.

**Options**:
- `--group <name|id>` — Default group for items without asset/group (repeatable: `--group g1 --group g2`)

**Input JSON**:
```json
{
  "description": "Grant description",
  "items": [
    {"type": "exec", "asset": "web-01", "command": "uptime"},
    {"type": "exec", "group": "production", "command": "systemctl status *"},
    {"type": "cp", "asset": "web-server", "detail": "upload config.yml"},
    {"type": "exec", "command": "df -h"}
  ]
}
```

**Item fields**:
- `type` — "exec", "cp", "create", "update"
- `asset` — Asset name or ID (targets a single asset)
- `group` — Group name or ID (targets all assets in the group)
- `command` — Shell command pattern (supports `*` wildcard)
- `detail` — Human-readable description

Items without asset/group inherit from positional args and `--group` flags (expanded to one item per target).

**Output**: Session ID (UUID) on approval, error on denial.

```bash
# Single asset
opsctl grant submit web-01 < grant.json
# Multiple assets (each item expanded to all targets)
echo '{"items":[{"type":"exec","command":"uptime"}]}' | opsctl grant submit web-01 web-02 web-03
# Per-item overrides (no expansion)
opsctl grant submit < complex-grant.json
# Commands matching grant patterns auto-pass
opsctl exec web-01 --type ssh -- uptime
```

## session

Sessions are auto-created on the first write operation if none exists. Explicit `session start` is only needed if you want to manage the lifecycle manually.

**Storage**: `.opskat/sessions/<scope>` in CWD (walks up directory tree). Scope is derived from terminal env vars (`TERM_SESSION_ID`, `ITERM_SESSION_ID`, `WT_SESSION`, `WINDOWID`) so different terminal windows get separate sessions. **Sessions expire after 24 hours.**

**Session ID resolution priority**:
1. `--session <id>` global flag (explicit)
2. `OPSKAT_SESSION_ID` environment variable (desktop app injects this)
3. `.opskat/sessions/<scope>` file (auto-created, walks up directory tree)

### `session start`

Create a session and print its ID. Writes to `.opskat/sessions/<scope>` in CWD.

### `session end`

End the current active session (removes the session file).

### `session status`

Show the current active session ID.

```bash
# Auto session (default, no manual steps needed)
opsctl exec web-01 --type ssh -- uptime       # auto-creates session on first call
opsctl exec web-02 --type ssh -- df -h        # reuses same session

# Explicit management (cross-terminal/scripting only)
SESSION=$(opsctl session start)
opsctl --session $SESSION exec web-01 --type ssh -- uptime
opsctl session end
```

## version

Print CLI version.

```bash
opsctl version
```
