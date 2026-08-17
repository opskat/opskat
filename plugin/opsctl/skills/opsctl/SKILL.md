---
name: opsctl
description: "opskat CLI for asset management and remote operations (SSH, databases, Redis, MongoDB, Kafka, Kubernetes, etcd, file transfer). Use when: managing server assets, executing remote commands, writing opsctl scripts/automation, or working with approval workflows and permission rules. Also triggers for: deploying to servers, server diagnostics/troubleshooting, batch operations across fleet, database queries, file transfers between servers, server inventory/discovery."
---

# opsctl CLI Tool

Standalone CLI for asset management and remote operations without the GUI. All managed assets are stored in the desktop app — use `list`/`get` to discover targets and `help <asset-or-type>` for the registered type's current config/command contract. `create asset --help` discovers the registered built-in type set at runtime; do not maintain a separate supported-type list in automation.

## Global Flags

- `--data-dir <path>` — Override app data directory
- `--master-key <key>` — Master encryption key (env: `OPSKAT_MASTER_KEY`)

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
`--credential-id <numeric-id>` reuses an existing managed credential. Plaintext is encrypted
inside the asset and never creates a credential; create managed credentials in the desktop
key manager before referencing them. Secret sources conflict rather than override each other.

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

Create flow is prevalidate/resolve → approval (terminal prompt when interactive, otherwise the desktop dialog) → asset transaction. A denied or failed
create leaves no asset row committed; output/audit contains only safe metadata and an
authentication reference only when an existing credential or Agent source is referenced.

## Approval Mechanism

Most write operations require approval. Check order: permanent policy rules (the asset's own column, its group chain, and attached policy groups) → still-valid grants → human approval.

**Approver selection**: an interactive terminal (stdin and stderr both TTYs) prompts right there; otherwise the running desktop app shows its dialog; with neither available opsctl refuses with exit code 3 and a fixed marker on the first stderr line.

- **Terminal prompt**: single-kind operations offer `[a]` allow once / `[p]` allow always (writes a permanent rule through the same path as `opsctl policy allow`) / `[d]` deny; every other kind offers allow once / deny. Empty input, EOF, and Ctrl-C count as deny.
- **Desktop dialog**: concurrent requests queue into one dialog with "Approve All" / "Deny All"; "Remember" saves a 24-hour grant.
- **Offline refusal**: `exec` / `cp` / `batch` carry a subject a rule could match, so they stop with `NEEDS AUTHORIZATION` plus a paste-ready `opsctl policy allow` line. `create` / `update` / `delete` carry no subject — no rule can ever pre-authorize them — so they stop with `NEEDS TTY`; only a human can perform them (delete additionally has no "allow always").
- **Pre-approve patterns**: ask the user to run `opsctl policy allow <targets> -- <patterns>` in their own terminal — you cannot run it yourself (see [references/commands.md](references/commands.md)).

## Sessions

Sessions are internal: one is auto-created on the first write operation per data dir and reused afterwards; it expires after 24 hours. There is no CLI surface to manage it — `--session`, `OPSKAT_SESSION_ID`, and the `session` subcommands no longer exist. The desktop dialog's "Remember" saves a 24-hour grant scoped to that session; `opsctl policy show` lists still-valid grants.

## Parallel Execution

**Preferred: `opsctl batch`** — Execute multiple commands against any asset type (ssh, database, redis, mongodb, etcd, kafka, k8s, ...) in a single invocation with one approval step for all need-confirm commands and parallel execution. This avoids approval race conditions and process-level failures.

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

**Alternative: Parallel sub-agents** — For operations that `batch` doesn't support (e.g., `cp`, `create`), dispatch parallel sub-agents. When the desktop app is running it queues concurrent approval requests into a single dialog with "Approve All" / "Deny All" buttons; without it, each unapproved request stops with `NEEDS AUTHORIZATION` (exit 3).

**Setup for sub-agents**: Ensure approval is handled before parallelizing:
- **Option A**: Ask the user to run `opsctl policy allow <targets> -- <patterns>` in their own terminal for all targets upfront — all matching commands then auto-approve. (You cannot run `policy allow` yourself: write subcommands need an interactive terminal and stop with exit 3 / `NEEDS TTY` when called non-interactively.)
- **Option B**: Run one command first and let the user approve it with a lasting effect — in an interactive terminal "allow always" writes a permanent rule, and the desktop dialog's "Remember" saves a 24-hour grant — then subsequent matching commands auto-approve.

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

Core commands: `list`, `get`, `help`, `create`, `update`, `delete`, `ssh`, `exec`, `batch`, `cp`, `policy`, `ext`, `version`.

For full command reference with flags and examples, see [references/commands.md](references/commands.md).

## Init — Asset Environment Discovery

`/opsctl:init` — Auto-discover server environment via SSH and update asset descriptions. Supports single asset or batch group processing.

## Error Handling

- **User rejection** (output contains "USER DENIED" or "denied: user denied"): Stop the entire task immediately. Report the denied command and wait for user instructions. Do NOT retry, work around, or continue with remaining steps.
- **NEEDS AUTHORIZATION** (first stderr line, exit code 3): No interactive terminal and the desktop app is unreachable, but a rule could authorize the subject. Stop, relay the `opsctl policy allow ...` line from the output verbatim to the user, and after the user has authorized, retry the original command. Do NOT run that authorization line yourself — it needs an interactive terminal and would itself fail with `NEEDS TTY`.
- **NEEDS TTY** (first stderr line, exit code 3): Only a human in a terminal can perform this (rule-writing commands, or `create`/`update`/`delete`, which carry no subject any rule could match). Stop and relay the original command to the user to run themselves. Do NOT retry — once the user runs it, the operation is already done; retrying performs it a second time (a retried `create` makes a duplicate asset). Confirm the outcome with a read-only command such as `get asset`.
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
# 1. Pre-approve the operations (the user runs this in their own terminal;
#    an AI-invoked policy allow stops with exit 3 / NEEDS TTY)
opsctl policy allow web-01 web-02 -- 'tee /etc/app/config.yml' 'systemctl restart app'

# 2. Deploy (all auto-approved by the permanent rules)
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
