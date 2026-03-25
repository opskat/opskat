---
name: opsctl
description: "opskat CLI tool (opsctl) for server asset management and remote operations. Use when: (1) user asks about opsctl commands or usage, (2) user wants to manage assets, execute remote commands, transfer files, or SSH into servers via CLI, (3) user asks to write scripts or automation using opsctl, (4) user invokes /opsctl. Covers: list/get/create/update assets, exec, ssh, cp, sql, redis, session and grant approval workflow."
---

# opsctl CLI Tool

opskat's standalone CLI for asset management and remote operations without the GUI.

## Data Directory

/Users/codfrm/Library/Application Support/opskat

## Global Flags

- `--data-dir <path>` — Override app data directory (default: platform-specific)
- `--master-key <key>` — Master encryption key (env: `OPSKAT_MASTER_KEY`)
- `--session <id>` — Session ID for batch approval (env: `OPSKAT_SESSION_ID`)

## Asset Resolution

Assets can be referenced by:
- **Numeric ID**: `opsctl get asset 1`
- **Name**: `opsctl get asset web-server`
- **Group/Name path**: `opsctl get asset production/web-01` (disambiguates duplicates)

## Command Quick Reference

| Command | Description |
|---------|-------------|
| `list assets [--type ssh\|database\|redis] [--group-id N]` | List assets with optional filters (no description) |
| `list groups` | List all asset groups (no description) |
| `get asset <asset>` | Get asset details including description and SSH config |
| `get group <group>` | Get group details including description |
| `create asset --name X --host X --username X [--type ssh\|database\|redis] [--port N] [--auth-type key\|password] [--driver mysql\|postgresql] [--database X] [--read-only] [--ssh-asset X] [--group-id N] [--description X]` | Create asset (needs approval). Port auto-detects by type (22/3306/5432/6379) |
| `update asset <asset> [--name X] [--host X] [--port N] [--username X] [--description X] [--group-id N]` | Update asset (needs approval) |
| `ssh <asset>` | Interactive SSH terminal (no approval needed) |
| `exec <asset> -- <command>` | Execute remote command (approval/policy checked) |
| `sql <asset> "<SQL>"` | Execute SQL on database asset (approval/policy checked) |
| `sql <asset> -f <file.sql>` | Execute SQL from file |
| `redis <asset> "<command>"` | Execute Redis command (approval/policy checked) |
| `cp <src> <dst>` | File transfer: local↔remote or remote↔remote (needs approval) |
| `grant submit <asset> <pattern>...` | Simple mode: submit exec patterns for an asset (no stdin needed) |
| `grant submit [--group X] [asset...] < input` | JSON mode: complex grants from stdin with per-item asset/group overrides |
| `session start` | Create session in `.opskat/sessions/<scope>` (auto-created on first write if omitted) |
| `session end` | End the current active session |
| `session status` | Show the current active session ID |
| `init <asset\|--group N>` | Discover server environment and update asset description ([details](references/ops-init.md)) |

For detailed command documentation, see [references/commands.md](references/commands.md).

## Approval Mechanism

Most write operations require desktop app approval via Unix socket (`<data-dir>/approval.sock`).

**Approval flow** (exec/sql/redis):
1. Check asset's policy (command/query/redis allow-list → execute, deny-list → reject)
2. Check grant items with pattern matching (approved grants → auto-allow matching commands)
3. Check session remembered patterns → auto-allow
4. Fall back to desktop app approval (blocks until response)

**Offline mode** (desktop app not running):
- SSH/SQL/Redis: Policy/grant match still auto-approves; otherwise shows allowed commands and rejects
- CP/Create/Update: Always requires desktop app (errors if offline)

**Permission pre-request flow** (`request_permission` tool):
1. AI submits command patterns (one per line, supports `*` wildcard) for a target asset
2. Desktop app shows permission approval dialog, user can edit patterns before approving
3. Approved patterns are stored as grant items in database
4. Subsequent commands matching any approved pattern auto-pass without further prompts

## User Rejected Approval — MUST STOP

**When the user explicitly rejects an approval or permission request, you MUST immediately stop the current task. Do NOT attempt to retry, work around, or continue with subsequent steps.**

Scenarios that require an immediate stop:

1. **User rejected execution approval** — The user denied the approval dialog. Output contains "用户拒绝执行".
2. **User rejected permission request** — A `request_permission` grant was rejected by the user. Output contains "用户拒绝 Grant 审批".

**Correct behavior**:
- Stop the entire task immediately — do not execute any remaining steps.
- Report to the user which command was denied.
- Wait for user instructions before taking any further action.

**Do NOT**:
- Retry the same command or a similar variant hoping it will pass.
- Skip the denied step and continue with subsequent steps (the rest of the grant likely depends on it).
- Treat the rejection as a non-fatal warning.

## Session Workflow

Sessions allow batch approval of operations. They auto-create on first write if none exists, so explicit `session start` is optional.

**Session storage**: `.opskat/sessions/<scope>` in CWD (walks up directory tree). Scope is derived from terminal env vars (`TERM_SESSION_ID`, `ITERM_SESSION_ID`, `WT_SESSION`, `WINDOWID`) so different terminal windows get separate sessions. **Sessions expire after 24 hours.**

**Session ID resolution priority**:
1. `--session <id>` global flag (explicit)
2. `OPSKAT_SESSION_ID` environment variable (desktop app injects this)
3. `.opskat/sessions/<scope>` file (auto-created, walks up directory tree)

```bash
# Auto session (no manual steps needed)
opsctl exec web-01 -- uptime       # auto-creates session on first call
opsctl exec web-02 -- df -h        # reuses same session, auto-approved after first allow

# Or explicit session management
SESSION=$(opsctl session start)
opsctl --session $SESSION exec web-01 -- uptime
opsctl --session $SESSION cp ./config.yml web-01:/etc/app/
opsctl session end
```

On the first operation, the user will be prompted to approve. If they choose **"Allow Session"**, subsequent operations in the same session are auto-approved.

**Grant workflow** — pre-approve command patterns:
```bash
# Simple mode: asset + patterns (no stdin needed)
SESSION=$(opsctl grant submit web-01 "systemctl *" "df -h" "uptime")
# Group mode
SESSION=$(opsctl grant submit --group production "uptime" "df -h")
# JSON mode for complex grants
SESSION=$(opsctl grant submit web-01 < grant.json)
# Commands matching grant patterns auto-pass
opsctl --session $SESSION exec web-01 -- systemctl restart app
```

## Init — Asset Environment Discovery

Auto-discover server environment via SSH and persist a structured description to the asset's `description` field. See [references/ops-init.md](references/ops-init.md) for full instructions.

```bash
/opsctl init web-server       # Single asset
/opsctl init --group 2        # All assets in group
/opsctl init                  # Interactive selection
```

## Common Patterns

**Query a database**:
```bash
opsctl sql prod-db "SELECT * FROM users LIMIT 10"
opsctl sql prod-db -f migration.sql
opsctl sql prod-db -d other_db "SHOW TABLES"
```

**Query Redis**:
```bash
opsctl redis cache "GET session:abc123"
opsctl redis cache "HGETALL user:1"
opsctl redis cache "SET key value EX 3600"
```

**Pipe data through remote command**:
```bash
cat config.yml | opsctl exec web -- tee /etc/app/config.yml
```

**Direct server-to-server file transfer** (no local disk):
```bash
opsctl cp staging:/var/backups/db.sql prod:/var/tmp/db.sql
```

**Deploy workflow with session**:
```bash
SESSION=$(opsctl session start)
opsctl --session $SESSION exec web-01 -- systemctl stop app
opsctl --session $SESSION cp ./app web-01:/usr/local/bin/app
opsctl --session $SESSION exec web-01 -- systemctl start app
opsctl session end
```
