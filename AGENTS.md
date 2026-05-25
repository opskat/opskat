# AGENTS.md

Guidance for Codex when working in this repository.

## Project

OpsKat is an AI-first desktop app for managing remote infrastructure: SSH, SFTP, databases, and Redis.

- Stack: Wails v2, Go 1.25 backend, React 19 frontend.
- Module: `github.com/opskat/opskat`.
- Frontend/backend communicate through Wails IPC. There is no HTTP API for the desktop app.
- Extension source lives in sibling repo `../extensions/`.

## Commands

```bash
# Development
make install                  # install frontend deps with pnpm
make dev                      # Wails dev mode with hot reload
make run                      # run embedded production build
make clean                    # remove build artifacts/caches

# Build
make build                    # production app
make build-embed              # production app with embedded opsctl
make build-cli                # standalone opsctl
make install-cli              # install opsctl to GOPATH/bin

# Tests
make test                     # Go tests for internal, cmd, pkg
make test-cover               # Go coverage report
go test ./internal/ai/...     # package scope example
go test ./internal/ai -run TestName
cd frontend && pnpm test
cd frontend && pnpm test:watch

# Lint/format
make lint
make lint-fix
cd frontend && pnpm lint
cd frontend && pnpm lint:fix

# Extensions / plugin
make devserver EXT=<name>     # isolated extension dev server; blocked when OPSKAT_ENV=production
make build-devserver-ui       # rebuild embedded devserver UI
make install-skill            # register Codex opsctl plugin
```

## Architecture

Backend layering:

```text
main.go
  -> internal/app/         Wails binding layer; keep public App methods thin
     -> internal/service/  business logic
        -> internal/repository/ data access via interfaces + Register()/getters
           -> internal/model/ domain entities
```

Important backend areas:

- `internal/ai/`: provider abstraction, tool registry, policy checks, runner, compression, audit logs.
- `internal/sshpool/`: SSH connection pool and Unix socket proxy for `opsctl`.
- `internal/connpool/`: database/Redis tunnel management.
- `internal/approval/`: desktop <-> `opsctl` approval socket workflow.
- `internal/bootstrap/`: database, credentials, migrations, auth token initialization.
- `internal/embedded/`: embedded `opsctl` binary behind `embed_opsctl`.
- `pkg/extension/`: WASM runtime using wazero; manifest parsing, host bridge, policy evaluation.
- `cmd/opsctl/`: standalone CLI for AI assistant remote operations.
- `cmd/devserver/`: single-extension HTTP dev server for extension development only.

Extension tools are exposed to AI through one `exec_tool` tool. Dispatch happens in `internal/ai/tool_handler_ext.go` using `extension` and `tool` args, then enforces policy against asset policy groups before calling `Plugin.CallTool`.

Frontend:

- App source: `frontend/`; pnpm workspace.
- Shared UI package: `frontend/packages/ui` as `@opskat/ui`.
- Devserver UI: `frontend/packages/devserver-ui`, embedded by `cmd/devserver`.
- Tech: Vite 6, Tailwind CSS 4, shadcn/ui/Radix, Zustand 5, xterm.js 6.
- Navigation: no React Router; use custom tab system in `tabStore`.
- Backend calls: generated Wails bindings in `frontend/wailsjs/`.
- Events: Wails `EventsOn()`.
- i18n: `zh-CN` and `en`, keys under the `common` namespace; use `t("key.subkey")`.
- Tests: Vitest, happy-dom, React Testing Library, Wails mocks in `src/__tests__/setup.ts`.

## Conventions

- CI runs Go lint/tests and frontend lint/tests/build on PRs and pushes to `main`/`develop`.
- Commit messages use gitmoji, e.g. `✨`, `🐛`, `♻️`, `🎨`, `⚡️`, `🔒`, `🔧`, `✅`, `📄`, `🚀`. 关联 issue 时：subject line（第一行）末尾追加 `#<编号>`，body 里另起一行写 `closes #<编号>`（或 `fixes` / `resolves`）触发 GitHub 自动关闭。例如 subject `🐛 修复 xxx #126`，body 末尾 `closes #126`。
- Go mocks live in `mock_*/` and are generated with `go.uber.org/mock`.
- Go tests use goconvey and testify.
- Service tests should mock transaction boundaries. When code uses `dbutil.WithTransaction`, prefer `dbutil.WithTransactionRunner` instead of opening in-memory SQLite.
- Frontend formatting is Prettier with 120-char width and 2-space indent.
- Soft delete uses `Status` (`StatusActive=1`, `StatusDeleted=2`), not GORM soft delete.
- Version info is embedded with ldflags.
- Credentials use Argon2id KDF + AES-256-GCM; the master key is stored in the OS keychain.

## Development Rules

- Search before adding components, hooks, utils, services, or helpers. Reuse existing patterns and shared primitives.
- Keep `internal/app/*.go` as thin Wails bindings: parse args, call services, return. Put business rules in `internal/service/` and persistence in `internal/repository/`.
- UI should depend on hooks/stores and shared components, not direct duplicated data loading/filtering.
- Prefer option-object APIs over large boolean prop lists.
- Do not copy-paste parallel implementations. If a fix applies to two near-identical blocks, extract or reuse the canonical path.
- Do not add silent defaults, empty `catch` blocks, swallowed errors, fake success states, or bypass paths unless they are explicit product behavior.
- Do not reimplement cross-cutting systems: audit, AI tool registration, approval, credential encryption, connection pools, i18n, terminal panes, query grids, tab system, shortcut handling. (Logging rules are covered separately under "Logging Key Flows".)

Reuse these shared frontend primitives when applicable:

- Pickers/tree: `AssetSelect`, `AssetMultiSelect`, `GroupSelect`, `TreeSelect`, `TreeCheckList`.
- Common UI: `ConfirmDialog`, drawer/dialog wrappers, `PasswordSourceField`, `IconPicker`.
- Asset rendering: use canonical helpers such as `getIconComponent`, `getIconColor`, `getAssetType`; respect entity fields like `Icon`, `Type`, `Color`, and policy group.
- Data/state: add filters or derivations to shared hooks/stores such as `useAssetStore`, `useAssetTree`, `useGroupTree`, `useShortcutStore`.

## Logging Key Flows

Troubleshooting depends on logs. Every cross-boundary / cross-process / long-lived operation must be logged — not only on error.

- **Entry point (cago logger):** `github.com/cago-frame/cago/pkg/logger`. **Prefer `logger.Ctx(ctx)`** — cago's own source comment on `Default()` reads "尽量不要使用，会丢失上下文信息" (try not to use, loses context). Only fall back to `logger.Default()` when no ctx is available (`main`, `init`, standalone goroutines). To attach fields for downstream consumers, use `logger.WithContextField(ctx, zap.String("k", v))`; downstream `logger.Ctx(ctx)` inherits them. ⚠️ The repo today has ~377 `Default()` and 0 `Ctx` call sites — historical inertia. New code follows the rule above.
- **Field types:** Pick the zap field that matches the value's natural type: `zap.Error` for errors, `zap.String` for strings, `zap.Int/Int64` for ints, `zap.Bool`, `zap.Duration`, `zap.Stringer`. **Do not use `zap.Any`** (zero intended uses in repo), and **do not wrap values with `fmt.Sprintf(...)` to fit `zap.String`** — pick the right typed field instead of squashing values into strings. Do not use `log.Printf` as a logger in business code. Exceptions: `fmt.Println` in `cmd/opsctl/command/*` is intentional CLI stdout (not logging), and `log.Printf` in `main.go` before logger init is kept.
- **Must-log key flows:** IPC entry (`internal/app/**`), AI tool dispatch (`internal/ai/`), extension WASM calls (`pkg/extension/`, `internal/app/extension/`), approval/grant (`internal/approval/`, `internal/app/opsctl/`), SSH/DB/Redis pool open/close (`internal/sshpool/`, `internal/connpool/`), credential and key operations, migrations, scheduled jobs, external command execution. Log **start / end / failure** of each operation with correlatable IDs (assetID, sessionID, grantID, toolName, extension).
- **Log lines do not replace error returns.** After `logger.Ctx(ctx).Error(..., zap.Error(err))`, still `return err`. `recover()` boundaries: use `zap.Stack("stack")` for the trace, e.g. `logger.Ctx(ctx).Error("xxx panic recovered", zap.String("sessionID", id), zap.Stack("stack"))`; standalone goroutines without a ctx may fall back to `logger.Default()`.
- **Levels:** Error = a failure the caller/user must see; Warn = self-heal or degraded path; Info = important state changes (pool open, task scheduled, extension loaded); Debug = high-frequency detail (terminal keystrokes, SFTP frames, heartbeats) and off by default.
- **Never log secrets:** passwords, tokens, credential plaintext, SSH private keys, or SQL parameter values must be masked before logging.

## Generated Files

Do not edit generated or auto-managed files by hand. Change the source and regenerate.

- `frontend/wailsjs/go/app/App.d.ts`, `App.js`, `models.ts`: generated by Wails from exported `App` methods; regenerate with `make dev` or `wails build`.
- `frontend/wailsjs/runtime/runtime.js`, `runtime.d.ts`: Wails runtime shim.
- `internal/**/mock_*/`: generated by `mockgen`; regenerate with `go generate ./...`.
- `internal/embedded/opsctl_bin`: produced by `make build-cli-embed`; regenerate with `make build-embed`.
- `frontend/packages/devserver-ui/dist/`: Vite build embedded by `cmd/devserver`; regenerate with `make build-devserver-ui`.
- `go.sum`: package-manager managed; update with Go tooling.
- `frontend/pnpm-lock.yaml`: package-manager managed; update with pnpm.

Build artifacts and caches are gitignored and safe to remove with `make clean`: `build/bin/`, `frontend/dist/`, coverage files, `tsconfig.tsbuildinfo`, `package.json.md5`, and top-level `opskat`, `opsctl`, `devserver` binaries.
