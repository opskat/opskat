---
name: opsctl
description: "opskat CLI for asset management and remote operations (SSH, databases, Redis, MongoDB, Kafka, Kubernetes, etcd, file transfer). Use when: managing server assets, executing remote commands, writing opsctl scripts/automation, or working with approval/grant/session workflows. Also triggers for: deploying to servers, server diagnostics/troubleshooting, batch operations across fleet, database queries, file transfers between servers, server inventory/discovery."
---

# opsctl CLI Tool

Standalone CLI for asset management and remote operations without the GUI. All managed assets are stored in the desktop app — use `list`/`get` to discover targets and `help <asset-or-type>` for the registered type's current config/command contract. `create asset --help` discovers the registered built-in type set at runtime; do not maintain a separate supported-type list in automation.

## Global Flags

- `--data-dir <path>` — Override app data directory
- `--master-key <key>` — Master encryption key (env: `OPSKAT_MASTER_KEY`)
- `--session <id>` — Session ID for batch approval (env: `OPSKAT_SESSION_ID`)

## Asset Resolution

Assets can be referenced by:
- **Numeric ID**: `opsctl get asset 1`
- **Name**: `opsctl get asset web-server`
- **Group/Name path**: `opsctl get asset production/web-01`

## Type Assertions

**When the asset type is known, always include a type assertion.** This catches
a target/type mismatch before policy checks, approval, or execution. Assertions
never select the protocol: dispatch still comes from the asset record.

- Single command: `opsctl exec <asset> --type <asset-type> -- <command>`
- Positional batch: `<asset-type>:<asset>:<command>`
- JSON batch: include `"type":"<asset-type>"` on every known-type item

Prefer canonical asset types (`ssh`, `database`, `redis`, `mongodb`, `etcd`,
`kafka`, `k8s`, `serial`). Compatibility aliases (`exec`, `sql`, `mongo`) are
accepted, but canonical names make the AI's intent clear. Omit the assertion
only when the type is genuinely unknown; use `get asset` to discover it first
when practical.

## Context Efficiency

Minimize output to save context window:
- **Filter lists**: `opsctl list assets --type ssh --group-id 2` instead of unfiltered `list assets` when the target type/group is known.
- **Targeted get**: Use `get asset <name>` for a single asset instead of listing all then filtering.
- **Batch over sequential**: One `opsctl batch` call returns structured JSON — more compact than N separate `exec` outputs with shell overhead.
- **Pipe to grep/head**: When only partial output is needed, pipe remote commands: `opsctl exec web --type ssh -- "tail -50 /var/log/app.log"` instead of dumping entire logs.

## Generic Asset Creation and Credentials

`opsctl create asset --name <name> --type <type> --config '<JSON object>'` accepts every
registered built-in type. Use `--config-file <path>` instead for a JSON object file; the two
inputs are mutually exclusive. Existing convenience flags (`--host`, `--port`, `--username`,
`--driver`, K8s flags, etc.) remain compatible, and only explicitly supplied flags override
non-secret config keys. Run `opsctl help <type>` for exact accepted fields and defaults.

For plaintext credentials prefer `--password-stdin`; it removes one terminal LF/CRLF and
preserves other bytes. `--password` and plaintext inline `--config` expose values through
argv (shell history, process listings, CI logs) and print a warning. Plaintext config files
must use restrictive permissions, must not be committed, and should be removed after use.
`--credential-id <numeric-id>` reuses an existing managed credential, while
`--credential-name` names a newly materialized password/SSH key. Secret sources conflict
rather than override each other.

SSH Agent creation uses both `--agent-source-id` and `--agent-key-fingerprint` (or the
corresponding config keys). Agent auth rejects password/private-key/credential inputs and
does not require the Agent source to be online at save time.

Discover safe credential refs with:

```bash
opsctl list credentials [--type password|ssh_key|ssh_agent]
opsctl get credential credential:3
opsctl get credential agent-source:2
```

Detail lookup requires a typed ref; bare IDs are ambiguous. `--credential-id` for creation
remains the numeric credential-row ID. These queries return metadata/status/usage only—no
password, private key, passphrase, Agent endpoint, signing material, or full Agent public key.

Create flow is prevalidate/resolve → desktop approval → atomic credential+asset transaction.
A denied or failed create leaves neither new row committed; output/audit contains only safe
metadata and an authentication reference when applicable.

## Approval Mechanism

Most write operations require desktop app approval.

**Flow**: policy check → grant pattern match → session auto-approve → desktop app approval dialog.

- **Queue mode**: Multiple concurrent approval requests are queued into a single dialog. User can approve/deny individually or batch "Approve All" / "Deny All".
- **Offline**: Policy/grant matches still auto-approve; otherwise rejects. Create/Update always need the desktop app (they carry no command, so no policy can match). CP is auto-approved when every endpoint subject matches a policy/grant, and needs the desktop app otherwise. **Delete always needs desktop app too, and cannot be pre-approved or granted even with an active session** — there is no "allow all" for it.
- **Pre-approve patterns**: Use `grant submit` or `request_permission` tool to submit command patterns (supports `*` wildcard). Approved patterns auto-pass subsequent matching commands.

## Sessions

Sessions auto-create on first write — do NOT manually `session start`. The approval dialog offers Deny / Remember / Allow: "Remember" saves that command pattern (editable before you confirm) for the session, so later commands matching it skip approval — it is not a blanket allow for the session. Sessions expire after 24 hours.

For explicit session management, grant workflow, and details, see [references/commands.md](references/commands.md).

## Parallel Execution

**Preferred: `opsctl batch`** — Execute multiple commands against any asset type (ssh, database, redis, mongodb, etcd, kafka, k8s, ...) in a single invocation with one approval dialog and parallel execution. This avoids approval race conditions and process-level failures.

```bash
# Args mode: mark every item whose type is known.
opsctl batch 'ssh:web-01:uptime' 'database:db-01:SELECT COUNT(*) FROM users' 'redis:cache:PING'

# JSON stdin mode (AI-friendly)
echo '{"commands":[
  {"asset":"web-01","type":"ssh","command":"uptime"},
  {"asset":"db-01","type":"database","command":"SELECT COUNT(*) FROM users"},
  {"asset":"cache","type":"redis","command":"PING"}
]}' | opsctl batch
```

Output is structured JSON with per-command results (`exit_code`, `stdout`, `stderr`, `error`).

**Alternative: Parallel sub-agents** — For operations that `batch` doesn't support (e.g., `cp`, `create`), dispatch parallel sub-agents. The desktop app queues concurrent approval requests into a single dialog with "Approve All" / "Deny All" buttons.

**Setup for sub-agents**: Ensure approval is handled before parallelizing:
- **Option A**: Run one command first → user selects "Remember" → subsequent commands matching that saved pattern auto-approve
- **Option B**: `grant submit` patterns for all targets upfront → all matching commands auto-approve

**Parallelizable scenarios**: batch `init`, same command on N servers, multi-target file transfers, independent database queries.

## File Transfer

`opsctl cp [-r] <source>... <destination>` — an endpoint is a local path, an SSH server over SFTP (`<asset>:/<path>`), or object storage (`<asset>:/<bucket>/<key>`, leading slash required). Any combination of the two sides works, including server → object storage; at least one endpoint must be on an asset.

```bash
opsctl cp ./dump.sql.gz s3-prod:/backups/2026/dump.sql.gz   # local -> object storage
opsctl cp web-01:/var/log/app.log s3-prod:/logs/app.log     # server -> object storage
opsctl cp -r ./dist s3-prod:/releases/v2/                   # directory tree
opsctl cp 'web-01:/var/log/*.log' ./logs/                   # remote glob: quote it
```

- **Recursive, glob, or several sources**: the destination must end with `/`, and each entry lands at `<destination>/<path relative to the source base>`. Quote remote globs — an unquoted one is expanded by the local shell first.
- **Approval**: every asset endpoint is authorized separately under that asset's own policy, before any byte is transferred. Recursive/glob transfers approve the source and destination directory/object-prefix scopes before listing; files inside those scopes do not generate per-file approval items.
- Symlinks encountered during expansion are skipped and reported; the first failure aborts the rest.
- Both endpoints on the same object storage asset streams the object through this process. For a server-side copy use `opsctl exec <asset> -- "object copy <bucket>/<key> --to=<bucket>/<key>"`.

## Commands

Core commands: `list`, `get`, `help`, `create`, `update`, `delete`, `ssh`, `exec`, `batch`, `cp`, `grant`, `session`, `ext`, `version`.

For full command reference with flags and examples, see [references/commands.md](references/commands.md).

## Init — Asset Environment Discovery

`/opsctl:init` — Auto-discover server environment via SSH and update asset descriptions. Supports single asset or batch group processing.

## Error Handling

- **User rejection** (output contains "USER DENIED" or "denied: user denied"): Stop the entire task immediately. Report the denied command and wait for user instructions. Do NOT retry, work around, or continue with remaining steps.
- **SSH connection failure**: Report the error, check asset config with `get asset`. Do not retry blindly — ask user if host/credentials changed.
- **Partial batch failure**: `batch` returns per-command results. Report failed commands with their errors, summarize successes. Ask user how to proceed with failures.
- **Command not found on remote**: Suggest installing the missing tool or an alternative command. Do not assume package managers.

## Common Workflows

### Fleet Diagnostics

```bash
# Check disk/memory across all production servers
opsctl batch 'ssh:web-01:df -h && free -h' 'ssh:web-02:df -h && free -h' 'ssh:db-01:df -h && free -h'
```

### Deploy Config → Restart Service

```bash
# 1. Pre-approve the operations
# Simple mode takes exactly ONE asset — use JSON mode to target several.
echo '{"items":[{"type":"exec","command":"tee /etc/app/config.yml"},{"type":"exec","command":"systemctl restart app"}]}' | opsctl grant submit web-01 web-02

# 2. Deploy (all auto-approved by grant)
cat config.yml | opsctl exec web-01 --type ssh -- tee /etc/app/config.yml
cat config.yml | opsctl exec web-02 --type ssh -- tee /etc/app/config.yml
opsctl batch 'ssh:web-01:systemctl restart app' 'ssh:web-02:systemctl restart app'
```

### Cross-Environment Data Migration

```bash
# Export from staging, import to prod (direct streaming, no local disk)
opsctl exec staging-db --type ssh -- "mysqldump -u app dbname | gzip" > /tmp/dump.sql.gz
opsctl exec prod-db --type ssh -- "gunzip | mysql -u app dbname" < /tmp/dump.sql.gz

# Or query + transfer
opsctl exec staging-db --type database -- "SELECT * FROM config WHERE env='staging'"
opsctl cp staging:/var/backups/db.sql prod:/var/tmp/db.sql
```

### Batch Server Setup

```bash
# Create assets → init discovery (use parallel sub-agents for create)
printf '%s\n' "$WEB03_PASSWORD" | opsctl create asset --name web-03 --host 10.0.1.3 --username root --password-stdin
opsctl create asset --name web-04 --host 10.0.1.4 --username root --credential-id 4
# Then batch init with /opsctl:init --group <group-id>
```
