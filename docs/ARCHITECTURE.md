# Architecture

The canonical map of how OpsKat is put together: the processes it runs, the backend layering, the request lifecycle, each subsystem, the data model, and the AI / extension / opsctl flows.

> This doc owns **structure** (what the pieces are and how they fit). It does *not* repeat the cross-cutting **principles** (SOLID seams, Fix policy, Reuse first, defensive code) — those live in [AGENTS.md](../AGENTS.md) — nor the **how-to** (commands, conventions, logging rules, generated files) — those live in [DEVELOP.md](DEVELOP.md). The step-by-step path for adding a built-in asset type is in [adding-an-asset-type.md](adding-an-asset-type.md).
>
> **Keep it true for the current branch.** Every claim here must be verifiable in committed code. Counts (asset types, migrations, stores) are deliberately written as "enumerate from `<source>`" rather than hardcoded — they drift silently. Before editing, read [DOC-MAINTENANCE.md](DOC-MAINTENANCE.md).

## 1. Topology — three processes, no HTTP API

OpsKat is a **Wails v2** desktop app (Go 1.26 backend + React 19 frontend). The frontend and backend communicate over **Wails IPC only** — there is no REST/HTTP server for the app's own UI. Two other process kinds talk to the running app over **Unix-domain sockets**, and extensions run as sandboxed **WASM** modules inside the backend.

```
┌──────────────────────────────────────────────────────────────────┐
│  Desktop app (single process)                                      │
│                                                                    │
│   React 19 frontend  ──Wails IPC──►  internal/app (bindings)       │
│   (frontend/)                            │                         │
│        ▲  EventsOn/Emit                  ▼                         │
│        │                            internal/service               │
│        │                                 │                         │
│        │                            internal/repository            │
│        │                                 │                         │
│        │                            GORM ─► SQLite (opskat.db)      │
│        │                                                            │
│        │   WASM (wazero)  ◄── pkg/extension ── internal/ai (tools)  │
│        │                                                            │
│   sshpool / connpool  (live SSH conns + DB/Redis tunnels)          │
└───────┬──────────────────────────────┬────────────────────────────┘
        │ approval.sock                 │ sshpool.sock
        │ (approve / grant)             │ (reuse pooled SSH conns)
        ▼                               ▼
   opsctl CLI  ── headless asset ops (exec / sql / redis / cp / batch) ──►
   (cmd/opsctl, also embeddable in the app via internal/embedded)
```

- **Desktop app** — the Wails process. Owns the DB, credential keys, connection pools, the AI runner, and the WASM extension runtime.
- **`opsctl` CLI** — a standalone binary (`cmd/opsctl`) for headless/scripted asset operations. It reuses the running app's SSH connections and asks the app for approval over Unix sockets; it degrades to a limited offline mode when the app isn't running. Can be embedded in the app (`internal/embedded`, build tag `embed_opsctl`) and installed to the user's PATH.
- **Extensions** — WASM modules loaded by `pkg/extension` (wazero runtime). The host exposes a narrow capability surface; the AI calls extension tools through a single dispatcher.
- **`devserver`** (`cmd/devserver`) is a **dev-only** single-extension harness with its own local HTTP API — not part of the shipped app. See [§7](#7-extensions--wasm-plugins).

## 2. Backend layering

Bindings stay thin: parse → delegate → return. Business rules live in `service/`, persistence in `repository/`. **Logic placed directly in an `App` binding is unreachable from tests and from `opsctl`** — so it doesn't belong there.

```
main.go
  └─ internal/app/        IPC boundary (Wails-bound structs, "binders")  ── validate here
       └─ internal/service/        *_svc — business logic, validation, orchestration
            └─ internal/repository/    interface + impl + mock — data access
                 └─ internal/model/entity/   GORM entities
```

**Dependency rule (DIP).** A layer depends on the **interface getter** of the layer below, never on a concrete struct. Services call `asset_repo.Asset()` (the `AssetRepo` interface), repositories reach GORM through cago's `db.Ctx(ctx)`. Implementations are swapped behind the interface in tests via the generated `mock_*/` packages. A service must not import another service's repository or call back into `App`; `internal/app/*` must not touch repositories or the DB directly.

**Registration, not branching (OCP).** Repositories and services are wired as global singletons at startup: `RegisterXxx(NewXxx())` installs the implementation, `Xxx()` returns it. New protocol behavior plugs in by *registering* (an asset-type handler, an AI tool, a policy) — not by adding a `switch assetType` to shared code. See [AGENTS.md → SOLID](../AGENTS.md#high-cohesion-low-coupling--solid) for the seams this enforces.

**Underlying framework.** The backend is built on the **cago** framework (`github.com/cago-frame/cago`): DB registration/access (`db.Database()` / `db.Ctx(ctx)`), the structured logger (`pkg/logger`), config sources, and the AI agent loop (`coding` / `agent` packages, see [§6](#6-ai-subsystem)).

## 3. Request lifecycle

A representative read, traced front to back:

1. **Frontend** calls a generated Wails binding, e.g. `System.GetAsset(id)` from `frontend/wailsjs/go/system/System` (the `wailsjs/` bindings are **generated & gitignored** — the producer is the Go side in `internal/app/*.go`).
2. **Binding** (`internal/app/system/asset.go`) — a thin method on the bound `System` struct. It wraps the Wails context with the current language (`i18n.Ctx(s.ctx, s.Lang())`) and delegates: `asset_svc.Asset().Get(ctx, id)`. Boundary inputs are validated here / in the service; this is one of the **validate-at-the-boundary** points (alongside WASM host calls — see [AGENTS.md → Defensive Code](../AGENTS.md#defensive-code--error-handling-no-meaningless-fallbacks)).
3. **Service** (`internal/service/asset_svc`) — runs business logic (validation, defaults, e.g. seeding the default command policy on create), then calls the repository interface getter `asset_repo.Asset()`.
4. **Repository** (`internal/repository/asset_repo`) — issues the GORM query through `db.Ctx(ctx)`, filtering on `status = StatusActive` (soft delete, [§5](#5-data--persistence)).
5. **Return** — entity → service → binding → Wails marshals JSON → frontend store updates → subscribed components re-render.

Backend-initiated updates flow the other way as **events**: the app emits a Wails event (e.g. `data:changed`) and the frontend re-fetches via `EventsOn` (see [§8](#8-frontend)).

## 4. Process wiring (`main.go`)

`main.go` is the composition root. In order, it: resolves the data dir / master key (env overrides `OPSKAT_DATA_DIR`, `OPSKAT_MASTER_KEY`; `OPSKAT_E2E` relaxes the single-instance lock for the e2e harness); runs `bootstrap.Init` (DB open, master-key resolve, repository registration, migrations); initializes the logger and config; builds the shared infrastructure (SSH pool, SFTP service); constructs the per-domain **binders** and hands them to `wails.Run` via `Bind`.

Binders implement a small `Lifecycle` (`Startup(ctx)` / `Cleanup()`) that `main.go` drives explicitly from Wails' `OnStartup` / `OnShutdown` hooks (Wails does not auto-call lifecycle methods on bound structs). The extension subsystem is initialized **asynchronously after startup** so WASM compilation never blocks the UI coming up.

## 5. Data & persistence

- **Store:** GORM over **SQLite** (`opskat.db` in the data dir), opened during `bootstrap.Init` and registered as the cago `db` singleton.
- **Migrations:** `gormigrate` (`go-gormigrate/gormigrate/v2`), one append-only list in `migrations/migrations.go`, run from bootstrap. New migrations are **appended**; old files never change. Enumerate them from `git ls-files 'migrations/*.go'` — don't hardcode a count.
- **Soft delete:** via a `Status` field, **not** GORM's `DeletedAt`. `StatusActive = 1` / `StatusDeleted = 2` are defined in `internal/model/entity/asset_entity/asset.go`; queries filter `status = StatusActive`. (Distinct from `internal/status/`, which is unrelated system-health reporting.)
- **Entities:** one package per domain under `internal/model/entity/` — enumerate with `git ls-tree --name-only -d HEAD internal/model/entity/` (asset, credential, group, policy_group, conversation, audit, grant, host_key, forward, ai_provider, snippet, extension_state, extension_data, and the shared `policy` package).
- **Credential encryption:** done in `internal/service/credential_svc` — **Argon2id** to derive a key from the master key + a per-install KDF salt, then **AES-256-GCM** for each secret. The **master key** is resolved by `credential_svc.ResolveMasterKey` (priority: explicit arg/env → OS keychain via `zalando/go-keyring` → `<dataDir>/master.key` file → freshly generated). `internal/bootstrap` only *resolves and injects* the key + salt (the KDF salt is persisted base64 in `config.json`); the crypto itself lives in `credential_svc`.

> "Command policy" is three different things — don't conflate them: (1) the shell-rule matching engine in `internal/ai/policy/command_rule.go` + `command_shell.go` (there is **no** `command_policy.go` source file); (2) the `command_policy` JSON column on the assets table (`asset_entity.Asset.CmdPolicy`); (3) `asset_entity.DefaultCommandPolicy`, a re-export of `policy.DefaultCommandPolicy` from `internal/model/entity/policy`.

## 6. AI subsystem

`internal/ai/` turns a conversation into guarded tool calls against assets. Its subpackages (enumerate with `git ls-tree --name-only -d HEAD internal/ai/`):

| Package | Responsibility |
| --- | --- |
| `runner` | Builds the cago `coding.System` + `agent.Runner` (provider, tools, middleware) and drives Send/Cancel/Steer. Registers an **audit middleware** around every tool call and an optional gate for local file/shell tools. |
| `tool` | The tool registry. `AllToolDefs()` is the name→handler table opsctl shares; `Tools()` exposes the same business tools to the AI as cago `tool.Tool`s (asset / exec / data / kafka / extension groups). Extensions are reached through the **single** `exec_tool` dispatcher (`tool_handler_ext.go`), not one AI tool per extension. |
| `policy` | Per-protocol rule checkers — `query_policy.go` (SQL, parsed with the TiDB parser), `redis_policy.go`, `mongo_policy.go`, `k8s_policy.go`, `kafka_policy.go`, plus shell-command rules in `command_rule.go` / `command_shell.go` (shell AST via `mvdan.cc/sh`). |
| `permission` | Dispatches an asset type to its policy checker, merges asset + group-chain effective policy, and returns Allow / Deny / NeedConfirm. Owns the grant flow (matching previously-approved DB grants, submitting new ones for approval). |
| `helper` | Protocol clients (SSH, SQL, Redis, Mongo, Kafka, etcd, K8s) that each run the permission check before executing and record the decision. |
| `audit` | Writes a tool-call audit log after execution, with per-tool command-summary extractors. |
| `aictx` | Shared context keys and the decision primitives (Allow / Deny / NeedConfirm, the `CheckResult` slot) used across the packages above. |

**Tool-call flow:** the LLM emits a tool use → cago's loop runs the middleware chain → the handler asks `permission` to check the asset's policy → **Allow** runs it; **Deny** returns a refusal to the model; **NeedConfirm** prompts the user (and, on "allow all", persists a **grant** so future matching calls skip the prompt) → the result and the recorded decision (source + matched pattern) are written to the audit log. Extension tool calls go through `exec_tool`, which resolves the extension, checks its declared policy type against the asset's policy groups, then calls `Plugin.CallTool`.

For the exact logging obligations on this path, see [DEVELOP.md → Logging for key flows](DEVELOP.md#logging-for-key-flows).

## 7. Extensions — WASM plugins

`pkg/extension/` is a **wazero** WASM runtime. Each extension ships a `manifest.json` declaring its tools (JSON-Schema params), asset types, capabilities, and policy type; the WASM module exports an `execute_tool` entry. Key files: `runtime.go` (compile/instantiate, `Plugin.CallTool`), `manifest.go` (parse + capability checks), `host.go` / `hostfn.go` / `host_default.go` (the host capability surface), `bridge.go` (registry of loaded extensions, their tools, asset types, and policy groups), `manager.go` (scan/load/watch the extensions dir).

**Host capability surface** (declared in the manifest, enforced at each host call): scoped filesystem read/write, HTTP (allowlist; private networks rejected unless tunneled), TCP, asset-config access (credential fields decrypted only with the `credentials: read` capability), KV storage (per-value / per-extension quotas), native file dialogs, logging, and action events/cancellation. Each tool call instantiates a **fresh** module — plugins keep no in-process state between calls.

**Lifecycle:** `internal/service/extension_svc` scans the extensions dir at startup, applies enabled/disabled state from `extension_state_repo`, and registers enabled extensions into the `Bridge`; KV data is persisted via `extension_data_repo`. Enable / disable / install / uninstall mutate the bridge and persist state. The AI side is wired by handing the `Bridge` to `tool.SetExecToolExecutor` (the `exec_tool` executor) once the service is ready.

**`cmd/devserver`** runs a *single* extension in isolation for development (`make devserver EXT=<name>`, refuses when `OPSKAT_ENV=production`). It serves a local HTTP API + WebSocket event stream and embeds its own UI (`frontend/packages/devserver-ui`, built to a generated `dist/`). This is a dev tool, separate from the shipped app's IPC-only model.

## 8. opsctl & the multi-process flow

`opsctl` performs asset operations headlessly while the desktop app remains the broker for connections and approvals. It shares the same bootstrap (DB, credentials) and talks to the running app over two Unix sockets, both under the data dir and mode-0600, authenticated with a token file written at startup:

- **`approval.sock`** (`internal/approval`, server started from `internal/app/opsctl`) — line-delimited JSON request/response. When a command needs confirmation, opsctl sends an `ApprovalRequest` (exec / cp / create / update / grant / batch / ext_tool); the app emits a Wails event, the UI shows the dialog, and the decision (plus any user-edited grant patterns) is returned. Approved **grants** are persisted via `grant_repo` so later matching commands are auto-approved.
- **`sshpool.sock`** (`internal/sshpool`) — a framed binary proxy. opsctl asks the app to run exec / upload / download / copy over an **already-open** pooled SSH connection instead of dialing (and re-authenticating) itself.

`internal/sshpool` pools live SSH clients (ref-counted, idle-reaped); `internal/connpool` builds short-lived TCP **tunnels** through those SSH connections for DB/Redis/etc. protocols, or dials through a per-asset **SOCKS5 proxy** (`internal/pkg/socksdial`, shared with SSH) when the config carries a `proxy` block — tunnel wins if both are present. When the app isn't running, opsctl falls back to `pkg/client` (direct SSH with TOFU known-hosts) and a local policy/grant check for the operations that allow offline use.

End to end — `opsctl exec <asset> -- <cmd>`: resolve asset → local policy check → if NeedConfirm, request approval over `approval.sock` (UI prompt) → on approval, execute over `sshpool.sock` against the pooled connection (falling back to a direct dial if the socket is unavailable) → stream stdout/stderr/exit, write the audit log.

## 9. Frontend

`frontend/` is a **pnpm workspace** (Vite 6, Tailwind 4, shadcn/ui over Radix, Zustand 5). The root app lives in `frontend/src/`; `frontend/packages/ui` is the shared `@opskat/ui` component library (consumed by both the app and `devserver-ui`); `frontend/packages/devserver-ui` is the separate UI embedded by `cmd/devserver`.

- **No React Router.** Navigation is a custom tab system in `tabStore`; tab kinds are the `TabType` union `"terminal" | "ai" | "query" | "page" | "info"`, each with its own metadata. Tab state persists to localStorage for session restore.
- **One Zustand store per domain** in `frontend/src/stores/` (enumerate with `git ls-files 'frontend/src/stores/*.ts'`). Components depend on stores/hooks, not on sibling components' internals; backend calls and connection/transport logic live in the stores, not scattered across components.
- **Backend bridge:** the generated, gitignored `frontend/wailsjs/go/<domain>/*` bindings call into the matching `internal/app/<domain>` binder; backend→frontend push uses `EventsOn` / `EventsOff` against events the app emits.
- **i18n:** i18next with locales `frontend/src/i18n/locales/{zh-CN,en}/common.json`, single namespace `common`, keys via `t("key.subkey")`.
- **Tests:** Vitest + happy-dom + React Testing Library; the Wails runtime and bindings are mocked in `frontend/src/__tests__/setup.ts`.

## 10. Where things connect

| Concern | Owner | This doc cross-links to |
| --- | --- | --- |
| Engineering principles, SOLID seams | [AGENTS.md](../AGENTS.md) | [§2](#2-backend-layering) |
| Commands, conventions, logging rules, generated files | [DEVELOP.md](DEVELOP.md) | [§3](#3-request-lifecycle), [§6](#6-ai-subsystem) |
| Adding a built-in asset type (handler + frontend registration, remaining couplings) | [adding-an-asset-type.md](adding-an-asset-type.md) | [§5](#5-data--persistence) |
| Verifying / debugging a feature (logs, DB, headless opsctl) | [testing-debugging-guide.md](testing-debugging-guide.md) | [§8](#8-opsctl--the-multi-process-flow) |
| GUI end-to-end harness (Playwright × Wails) | [e2e-harness-guide.md](e2e-harness-guide.md) | [§9](#9-frontend) |
