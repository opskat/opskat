# Agent Testing & Debugging Guide

Audience: AI coding agents (Claude Code, Codex, …) and developers verifying changes
in OpsKat. This guide explains **how to confirm a feature actually works and how to
debug it** by reading the system's logs and database — not by clicking the UI.

If you only remember one thing: **OpsKat is a Wails desktop GUI with an IPC-only
backend. You usually cannot drive the UI, so you verify behavior through observable
side-effects (structured logs + SQLite rows) and through headless paths (`opsctl`,
automated tests).**

---

## 1. Mental model & verification surfaces

OpsKat = Go backend + React frontend, wired over Wails IPC (no HTTP API). A running
desktop window is hard for an agent to drive, so reach for the *observable* and
*headless* surfaces instead. **Which surface to exercise, in what order** (cheap
signals → `opsctl` → the real app) is owned by [VERIFICATION.md](../VERIFICATION.md);
this guide owns the observation mechanics.

**Two ways to confirm what happened** after exercising a path:

- **Logs** — `logs/opskat.log` (level-gated) and `logs/error.log` (errors only). Every
  cross-boundary / cross-process / long-lived operation is logged with correlatable
  IDs (see [§3](#3-reading-logs)).
- **The database** — `opskat.db` (SQLite). The `audit_logs` table is the single best
  record of *what operations ran, with what arguments, and whether they succeeded*
  (see [§4](#4-inspecting-the-database)).

> A clean verification proves the side-effect, not just "no error": assert a specific
> log line **and/or** a specific DB row, then reset state.

---

## 2. Where everything lives

The default app data directory comes from `bootstrap.AppDataDir()`. A portable build uses
the `data/` directory next to its executable; otherwise the platform defaults are:

| OS | Data directory |
|----|----------------|
| macOS | `~/Library/Application Support/opskat` |
| Windows | `%LOCALAPPDATA%\opskat` (fallback `%USERPROFILE%\AppData\Local\opskat`) |
| Linux | `~/.config/opskat` |

Inside the data directory:

| Path | What it is |
|------|------------|
| `opskat.db` | SQLite database — assets, credentials, audit log, conversations, … (a transient `opskat.db-journal` may appear during writes) |
| `config.json` | App config (incl. `debug_mode`, AI provider, update channel) |
| `logs/opskat.log` | Main log, level depends on `debug_mode` (rotated) |
| `logs/error.log` | Errors only (`error`+), always written |
| `extensions/` | Installed WASM extensions |

Print the directory for the current OS so later commands can reference it (or set `DIR` to
the portable / `OPSKAT_DATA_DIR` override you launched with):

```bash
# macOS
DIR="$HOME/Library/Application Support/opskat"
# Linux:   DIR="$HOME/.config/opskat"
# Windows: $DIR = "$env:LOCALAPPDATA\opskat"
ls -la "$DIR" "$DIR/logs"
```

---

## 3. Reading logs

### Format

Logs are **structured JSON** (zap production encoder, ISO8601 timestamps). One object
per line, for example:

```json
{"level":"info","ts":"2026-06-03T15:04:05.123+0800","caller":"etcd_svc/service.go:43","msg":"etcd exec start","assetID":12,"op":"get","source":"opsctl"}
```

Standard keys: `ts`, `level` (lowercase: `debug`/`info`/`warn`/`error`), `caller`,
`msg`, plus operation-specific fields. Errors carry an `error` field; goroutine panic
boundaries carry `stack`.

### Files & rotation

- `opskat.log` — everything at the active level and above.
- `error.log` — `error` and above only, regardless of `debug_mode`.
- Rotation (lumberjack): **2 MB per file, up to 10 backups, 30 days**, uncompressed.
  Rotated files sit next to the active file with a timestamp suffix.

### Enable debug logging

The active level of `opskat.log` is `info` by default and drops to `debug` when
`debug_mode` is on. Two ways to turn it on:

1. **In the app:** Settings → toggle Debug logging (calls `SetDebugMode`, which rebuilds
   the logger live — no restart needed).
2. **By hand:** set `"debug_mode": true` in `config.json`, then **restart the app**.

```bash
# Inspect / set debug_mode without the UI (app must be restarted after editing)
jq '.debug_mode' "$DIR/config.json"
```

> `debug` level includes high-frequency detail (terminal keystrokes, SFTP frames,
> heartbeats). Turn it on to reproduce, turn it back off when done.

### Level convention (what each level means here)

- **Error** — a failure the user/caller needs to know about.
- **Warn** — self-healed or degraded.
- **Info** — key state changes (connection established, task scheduled, extension loaded).
- **Debug** — high-frequency details, off by default.

### Correlatable IDs & the three-state pattern

Key flows log **start / end / fail** (e.g. `etcd exec start` → `etcd exec end` /
`etcd exec failed`, all carrying the same `assetID`). In the logs the real correlation
fields are `assetID`, `sessionID`, `extension`, and `conv_id` (AI conversations) — use one
to stitch an operation together across files. Note `tool_name` is **not** a log field; it
lives in the `audit_logs` table (see [§4](#4-inspecting-the-database)) — filter per-tool
there, not in the log files.

### Recipes

```bash
LOG="$DIR/logs/opskat.log"

# Live tail
tail -f "$LOG"

# Only errors (from the main log) — pretty, one object per line
jq -c 'select(.level=="error")' "$LOG"

# Everything for one session, in order
jq -c 'select(.sessionID=="5f3c…")' "$LOG"

# Everything for one AI conversation
jq -c 'select(.conv_id==42)' "$LOG"

# One asset across the run
jq -c 'select(.assetID==12)' "$LOG"

# Quick, jq-free filter
grep -F '"assetID":12' "$LOG" | tail -20

# What blew up most recently
tail -50 "$DIR/logs/error.log" | jq -c '{ts,msg,error,caller}'
```

> Never paste secrets from logs. The codebase masks passwords / tokens / private keys /
> SQL parameter values before logging — if you ever see a plaintext secret in a log,
> that is a bug to report, not data to reuse.

---

## 4. Inspecting the database

`opskat.db` is a live SQLite file the running app holds open. It runs in rollback-journal
(`delete`) mode — **not** WAL — so there are no persistent `-wal`/`-shm` files; at most a
transient `opskat.db-journal` appears mid-write. Read it **without ever writing to it**.

> ### ⚠️ Safety
> - **Read-only, always.** Query with the read-only flag so you cannot corrupt the file:
>   ```bash
>   DB="$DIR/opskat.db"
>   sqlite3 -readonly "$DB" "SELECT 1;"
>   ```
>   A read-only query takes only a SHARED lock. If the app happens to be mid-write you
>   may see `database is locked` — just retry, or query a copy / a stopped app.
> - **Never** open it read-write, run a second writer, or hand-edit rows. Schema is
>   owned by the migrations in `/migrations/`; manual edits drift it.
> - **Credentials are encrypted** (Argon2id + AES-256-GCM; the master key is resolved
>   from explicit configuration, the OS keychain, or the protected data-dir key file).
>   The `credentials` table holds ciphertext — do not expect plaintext, and
>   do not try to decrypt it for debugging.
> - To inspect a *quiet, stopped* app you can query `opskat.db` directly, or copy it
>   first (`cp "$DB" /tmp/`) and query the copy — in `delete` mode the single file is the
>   whole database, so a copy is complete.

### Key tables

| Table | What it records | Notable columns |
|-------|-----------------|-----------------|
| `audit_logs` | Operations explicitly wired into the audit pipeline: AI/opsctl tool calls and specific audited desktop flows such as external edit. It does **not** represent every UI interaction (for example, interactive RDP and object-browser activity is not automatically a row per action). | `source`, `tool_name`, `asset_id`/`asset_name`, `command`, `request`, `result`, `error`, `success` (1/0), `decision`, `decision_source`, `session_id`, `grant_session_id`, `conversation_id`, `createtime` |
| `assets` | Built-in and extension assets. Enumerate built-ins from `internal/assettype/*.go`; the current registry spans terminal, VNC/RDP, database, middleware, and object-storage types. | `name`, `type`, `group_id`, `config` (JSON), `command_policy`, `status` (1=active, 2=deleted) |
| `credentials` | Encrypted secrets (ciphertext only) | — |
| `groups` | Asset groups (tree) | `name`, `parent_id` |
| `policy_groups` | Command/operation policies | — |
| `grant_sessions` / `grant_items` | Approval sessions and granted items | session id, expiry, granted patterns |
| `conversations` / `conversation_messages` | AI chat history | conversation id, role, content |
| `ai_providers` | Configured AI providers | — |
| `extension_state` / `extension_data` / `extension_describe` | Installed extension state, per-extension KV, and the cached `describe()` answer (keyed by wasm hash) | — |
| `host_keys` | Known SSH host keys | — |
| `snippets` | Saved command snippets | — |
| `forward_configs` / `forward_rules` | Port forwarding | — |

> Soft delete is via `status` (`1`=active, `2`=deleted), **not** GORM soft delete —
> filter `WHERE status = 1` for live rows.

### Example read-only queries

```bash
DB="$DIR/opskat.db"

# Last 20 operations, newest first
sqlite3 -readonly "$DB" \
  "SELECT datetime(createtime,'unixepoch','localtime') AS t, source, tool_name,
          asset_name, success, substr(COALESCE(error,''),1,60) AS err
   FROM audit_logs ORDER BY id DESC LIMIT 20;"

# Failures only
sqlite3 -readonly "$DB" \
  "SELECT datetime(createtime,'unixepoch','localtime'), tool_name, asset_name, error
   FROM audit_logs WHERE success=0 ORDER BY id DESC LIMIT 20;"

# One AI tool's operations (tool_name lives here, not in the logs)
sqlite3 -readonly "$DB" \
  "SELECT datetime(createtime,'unixepoch','localtime'), source, asset_name, success
   FROM audit_logs WHERE tool_name='exec' ORDER BY id DESC LIMIT 20;"

# Trace one approval session end-to-end
sqlite3 -readonly "$DB" \
  "SELECT id, tool_name, command, decision, success
   FROM audit_logs WHERE session_id='5f3c…' ORDER BY id;"

# Active assets
sqlite3 -readonly "$DB" \
  "SELECT id, name, type, group_id FROM assets WHERE status=1 ORDER BY id;"

# List tables / inspect a schema
sqlite3 -readonly "$DB" ".tables"
sqlite3 -readonly "$DB" ".schema audit_logs"
```

---

## 5. Running the app

```bash
make dev          # Wails hot-reload (frontend + backend) — best for iterating
make run          # Run the embedded production build
make build        # Production build → build/bin/
make build-embed  # Production build with bundled opsctl
```

Environment toggles for the desktop app:

- `OPSKAT_DATA_DIR=/path/to/data` — redirect DB, config, logs, and sockets to an
  isolated directory (the GUI equivalent of opsctl's `--data-dir`).
- `OPSKAT_MASTER_KEY=<key>` — provide the credential-KDF passphrase explicitly; useful
  with an isolated data directory and required by the hermetic e2e harness.
- `OPSKAT_EXTENSIONS=0` — start with the extension system disabled (isolate
  extension-related behavior).
- `OPSKAT_ENV=production` — production mode (e.g. `opsctl ext dev` refuses to run).

The GUI has no `--data-dir` command-line flag; use `OPSKAT_DATA_DIR` instead. Prefer a
throwaway directory for destructive verification. If you must use the default data
directory, back it up (`cp -a "$DIR" "$DIR.bak"`) and restore it afterwards. `opsctl`
accepts either `--data-dir` or the same environment variable (see below).

---

## 6. Headless functional testing with `opsctl`

`opsctl` is the standalone CLI for headless asset operations. It shares the app's asset,
credential, policy, audit, connection-pool, and approval infrastructure where the command
supports it. It is the realistic way for an agent to exercise SSH / databases / Redis /
MongoDB / Kafka / Kubernetes / etcd / file / extension features without a GUI — all of them
through the one `exec` verb, which dispatches on the asset's real type — then verify via
logs and `audit_logs`. Interactive RDP sessions and the built-in OSS browser do not
currently have dedicated opsctl operation commands.

```bash
make install-cli         # install opsctl to GOPATH/bin
# or: make build-cli && ./build/bin/opsctl ...
```

Common verbs (run `opsctl <command> --help` for details):

```bash
opsctl list assets                         # inventory
opsctl get asset web-server                # details by name or numeric ID
opsctl help prod-db                        # command syntax for that asset's type
opsctl exec web-server -- uptime           # run a command over SSH
opsctl exec prod-db -- "SELECT 1"          # query a database asset — same verb, any type
opsctl exec cache -- "PING"                # Redis command
opsctl cp ./file web-server:/tmp/          # scp-style transfer
opsctl delete asset old-server             # always asks the desktop app for confirmation
opsctl ext list                            # installed extensions (with enabled state)
opsctl exec my-bucket -- list_objects --bucket=logs   # extension tool: same exec verb,
                                           # executed by the running desktop app
```

Global flags worth knowing for testing:

- `--data-dir <path>` — point at an **isolated/throwaway** data directory so a test run
  never touches your real assets.
- `--master-key <key>` (or `OPSKAT_MASTER_KEY`) — supply the master key for credential
  decryption when running outside the app.
- `--session <id>` (or `OPSKAT_SESSION_ID`) — approval session id.

> **Approval gate:** write operations (`exec`, `cp`, `create`, `update`, `delete`) require
> approval from the **running desktop app** over a Unix socket. On first write a session is
> auto-created; "Remember" in the app saves that command pattern for the session (24h), so
> later commands matching it auto-approve — except `delete`,
> which always asks and can never be pre-approved or granted. For fully headless write
> tests you need the app running to approve, or a policy/session that permits the
> operation. **Read** verbs (`list`, `get`, `help`, `exec` for reads like `SELECT`) do not
> need approval.

After running an `opsctl` op, confirm it the same way you would any other path:

```bash
# It should appear in the audit log…
sqlite3 -readonly "$DB" \
  "SELECT tool_name, asset_name, success, error FROM audit_logs
   WHERE source='opsctl' ORDER BY id DESC LIMIT 5;"
# …and in the logs, keyed by session
jq -c 'select(.sessionID=="…")' "$DIR/logs/opskat.log"
```

---

## 7. Automated tests

Always prefer a failing test that reproduces the issue before changing impl (see the
"Fix policy — TDD" section in `AGENTS.md`).

```bash
# Go
make test                                  # Go tests: internal/, cmd/opsctl, pkg/ (CI runs `go test ./...`)
go test ./internal/ai/...                  # package scope
go test ./internal/ai/ -run TestName       # single test
make test-cover                            # coverage HTML

# Frontend
cd frontend && pnpm test                   # vitest (happy-dom + RTL; Wails mocked)
cd frontend && pnpm test:watch

# GUI end-to-end (Playwright × the real Wails app — see below)
make test-e2e
```

Notes:
- Go mocks live in `mock_*/` (`go.uber.org/mock`; regen with `go generate ./...`).
- Service tests mock transaction boundaries — when code uses `dbutil.WithTransaction`,
  prefer `dbutil.WithTransactionRunner` over an in-memory SQLite.
- Frontend tests mock the Wails runtime in `src/__tests__/setup.ts`.

### GUI E2E (Playwright × the real Wails app)

There is also a Playwright harness that drives the **real running app** through the Wails dev
browser bridge: a committed core-flow suite (`make test-e2e`, also run in CI on Linux + `xvfb`
as the `Wails E2E` job). For **ad-hoc verification of a feature you just finished**, don't run
that suite — start the sandbox (`make dev-sandbox`) and drive it with `e2e/drive.mjs` /
`e2e/oracle.mjs`, which needs no script at all. Full workflow, isolation guarantees, and
conventions: **[e2e-harness-guide.md](./e2e-harness-guide.md)**.

---

## 8. End-to-end verification recipe (template)

A repeatable loop for "I changed X; does it work?". Run it against the **sandbox**, never
your real data directory — `make dev-sandbox` starts the app on an isolated dir and
`oracle.mjs` reads it, so no step here needs a backup-and-restore dance:

```bash
make dev-sandbox                       # once, in another terminal (ARGS=--reset for a clean DB)

# 0. (optional) enable debug logging: set config.json debug_mode=true in the sandbox dir, restart
# 1. Mark a baseline so you only read NEW audit rows
BASE=$(node e2e/oracle.mjs mark)

# 2. Exercise the path:
#    - unit/integration:  go test ./... -run TestThing
#    - headless:          opsctl --data-dir <tmp> exec web-server -- uptime
#    - GUI:               node e2e/drive.mjs snapshot   → click / fill → shot

# 3. Confirm the side-effect in the audit log
node e2e/oracle.mjs audit --since="$BASE"

# 4. Confirm in the logs
node e2e/oracle.mjs logs --grep=<assetName> --tail=50

# 5. Reset state so the next run is clean: restart with `make dev-sandbox ARGS=--reset`
```

Workflow and when to use which form: [../VERIFICATION.md](../VERIFICATION.md). The same loop
against a real data directory is what §9 forbids — the sandbox exists so you never have to.

---

## 9. Safety checklist

- **Never write `opskat.db` directly.** Read-only (`sqlite3 -readonly`) or query a
  stopped app. Schema belongs to `/migrations/`.
- **Never reuse secrets** seen anywhere; report plaintext secrets in logs as a bug.
- **Isolate destructive runs** — `opsctl --data-dir <tmp>` for headless, `make dev-sandbox`
  for the GUI. Backing up and restoring your real data dir is not an acceptable substitute:
  it fails open, and one forgotten restore costs the whole inventory. The app enforces this
  for GUI verification (`OPSKAT_E2E=1` refuses to boot on the real dir, see
  [e2e-harness-guide.md §3](./e2e-harness-guide.md#3-isolation--safety-guarantees)).
- **Leave `debug_mode` off** when you're done; it's noisy.
- **Reset state** between runs so results are reproducible.
