# DEVELOP.md — OpsKat Development Guide

OpsKat's development handbook: common commands, the architecture & subsystem map, code conventions, logging rules for key flows, and which files are generated.

> **Before any development, read this document in full.** It's the lookup reference for *how to work in this repo*. The cross-cutting **principles** — SOLID / high cohesion, low coupling, Fix policy — TDD, Reuse first, defensive code / error handling — are not here; they live in [AGENTS.md](../AGENTS.md), and apply alongside this guide.

## Common Commands

```bash
# Install / dev / run
make install                             # Install frontend deps (pnpm)
make dev                                 # Wails hot-reload
make run                                 # Run embedded production build
make clean                               # Remove build artifacts / caches

# Build
make build / make build-embed            # Production (embed = bundled opsctl)
make build-cli                           # Standalone opsctl
make install-cli                         # Install opsctl to GOPATH/bin

# Test
make test                                # All Go tests
go test ./internal/ai/...                # Package scope
go test ./internal/ai/ -run TestName     # Single Go test
make test-cover                          # Coverage HTML
cd frontend && pnpm test                 # Frontend (vitest)
cd frontend && pnpm test:watch
make test-fixtures && make test-e2e      # E2E (needs ../extensions sibling)

# Lint
make lint / make lint-fix                # golangci-lint
cd frontend && pnpm lint / pnpm lint:fix

# Extensions / plugin
make devserver EXT=<name>                # Single-extension dev (refuses if OPSKAT_ENV=production)
make build-devserver-ui                  # Rebuild embedded devserver UI
make install-skill                       # Register opsctl plugin marketplace
```

> **Feature verification & debugging**: how to run and verify a feature, read the logs (`logs/opskat.log`) and database (`opskat.db`, e.g. `audit_logs`) to aid diagnosis, and run headless functional tests with `opsctl` — see [docs/testing-debugging-guide.md](testing-debugging-guide.md) (written for agents like Claude/Codex, in English).

## Architecture

**Backend layers** — bindings stay thin: parse → service → return. Business rules in `service/`, persistence in `repository/`. Logic inside `App` is unreachable from tests and `opsctl`.

```
main.go → internal/app/ (Wails bindings, IPC boundary)
            → internal/service/    (*_svc, business logic)
            → internal/repository/ (interface + impl)
            → internal/model/      (entities)
```

**Key subsystems:**
- `internal/ai/` — provider abstraction (Anthropic/OpenAI), tool registry, conversation runner, audit. AI tools live in `internal/ai/tool/`; the conversation runner in `internal/ai/runner/`. Per-protocol policy checkers live in `internal/ai/policy/`: SQL in `query_policy.go`, plus `k8s_policy.go` / `kafka_policy.go` / `mongo_policy.go` / `redis_policy.go`; shell-command rules in `command_rule.go` / `command_shell.go`.
- `internal/assettype/` — per-asset adapters wired through `registry.go` (enumerate the set with `git grep "Register(&" -- internal/assettype/*.go`). New asset types plug in here, not by hardcoding type strings — full end-to-end how-to in [adding-an-asset-type.md](adding-an-asset-type.md).
- `internal/sshpool/`, `internal/connpool/` — SSH pool (Unix socket proxy for opsctl); DB/Redis tunnels.
- `internal/approval/` — Unix-socket approval flow between desktop app and opsctl.
- `internal/bootstrap/` — DB, credentials, migrations, auth tokens, logger.
- `internal/embedded/` — embedded `opsctl` binary behind the `embed_opsctl` build tag.
- `pkg/extension/` — WASM runtime (wazero); `HostProvider` in `host.go` defines capabilities.
- `cmd/opsctl/`, `cmd/devserver/` — standalone CLI / single-extension dev server.
- `plugin/` — Claude Code plugin marketplace; installed via `make install-skill`.

**Data:** GORM + SQLite, gormigrate migrations in `/migrations/`. Soft deletes via `Status` (`StatusActive=1`, `StatusDeleted=2`), **not** GORM soft delete. Credentials: Argon2id + AES-256-GCM, master key in OS keychain.

**Extensions:** WASM modules with `manifest.json`-declared tools. AI invokes them via a **single `exec_tool`** (not one tool per extension). Dispatcher in `internal/ai/tool/tool_handler_ext.go` enforces extension policy type against asset policy groups before `Plugin.CallTool`.

**Frontend** (`frontend/`, pnpm workspace): root app uses `@opskat/ui` (`packages/ui`); `packages/devserver-ui` is embedded by `cmd/devserver`. Vite 6, Tailwind 4, shadcn/ui (Radix), Zustand 5.
- **No React Router** — custom tabs in `tabStore` (`terminal | ai | query | page | info`). One Zustand store per domain in `src/stores/`.
- Backend via Wails bindings (`frontend/wailsjs/go/app/App`); events via `EventsOn()`.
- i18n: i18next, locales in `src/i18n/locales/{zh-CN,en}/common.json`, all keys under `"common"` → `t("key.subkey")`.
- Tests: Vitest + happy-dom + RTL; Wails runtime mocked in `src/__tests__/setup.ts`.

## Conventions

### Commit message — gitmoji

**The first character of the subject must be the emoji glyph itself** (e.g. `✨`), not the gitmoji text code (`:sparkles:`), and not a plain-text prefix like `feat:` / `fix:`. Default format: `<emoji> <short description>`, e.g. `📄 调整 sessionid 碰撞风险说明` (commit messages themselves are commonly written in Chinese in this repo — the emoji-first rule is language-agnostic).

Common emoji (aligned with the changelog categories in [`/release`](../.claude/skills/release/SKILL.md)):

| Emoji | Use |
|---|---|
| 💥 | Major new feature / brand-new module / cross-subsystem refactor (a user-facing headline; usually 1–3 per release, called out in the release summary) |
| ✨ | General new feature |
| 🐛 | Bug fix |
| 🚑 | Urgent production hotfix |
| ⚡️ | Performance |
| ♻️ | Refactor / compatibility change |
| 🎨 | UI improvement |
| 💄 | Styling / visual detail |
| 🔒 | Security |
| 🔧 | Config |
| ✅ | Tests |
| 📄 | Docs |
| 🚀 | Release |

**Only add an issue number when intentionally linking an issue**: append `#<number>` at the end of the subject line (first line), and on a separate line in the body write `closes #<number>` (or `fixes` / `resolves`) only when the commit should trigger GitHub's auto-close. E.g. subject `🐛 Fix xxx #126`, body ends with `closes #126`. Do not add a PR number, review-comment number, or arbitrary `#xxx` suffix by default.

### Others

- **CI:** runs Go lint/tests and frontend lint/tests/build on PRs and pushes to `main`/`develop`.
- **Go:** mocks in `mock_*/` (`go.uber.org/mock`, regen `go generate ./...`); tests use goconvey + testify. Service tests mock transaction boundaries — when code uses `dbutil.WithTransaction`, prefer `dbutil.WithTransactionRunner` over opening in-memory SQLite.
- **Frontend:** Prettier 120 col, 2-space.
- **Versioning:** version info is embedded with ldflags.
- **Windows child processes:** commands launched from the GUI must not flash a console window. For `exec.Command` paths, call `internal/pkg/executil.HideWindow` for fully hidden children, or `HideConsoleWindow` when only console-subsystem helpers should be suppressed while GUI programs remain visible. The Windows local terminal path is different: `internal/service/localterm_svc/pty_windows.go` starts shells through `internal/pkg/winconpty`, whose process creation flags must include `CREATE_NO_WINDOW`; this prevents the black console flash when opening a `local` asset. If the flash regresses, fix the ConPTY process creation path first instead of adding frontend or call-site fallbacks.

## Logging for key flows

Diagnosing production issues relies on logs. Log every cross-boundary / cross-process / long-lived operation — don't write only on error.

- **One entry point (cago logger):** `github.com/cago-frame/cago/pkg/logger`. **Prefer `logger.Ctx(ctx)`** — the cago source comments explicitly say `Default()` "should be avoided where possible, as it loses context information". Use `logger.Default()` only when there genuinely is no ctx (`main` / `init` / a standalone goroutine). To attach shared fields for everything downstream, use `logger.WithContextField(ctx, zap.String("k", v))`; downstream `logger.Ctx(ctx)` inherits them automatically. ⚠️ Most legacy code is still `Default()` with few `Ctx` calls (historical inertia; the exact count keeps changing — don't hardcode it); write new code per the rule above.
- **Field types:** pick a strongly-typed field matching the value's natural type: `error` → `zap.Error`, string → `zap.String`, integer → `zap.Int`/`Int64`, bool → `zap.Bool`, duration → `zap.Duration`, anything with `String()` → `zap.Stringer`. **No `zap.Any`** (the only reasonable exception is the value from `recover()` — its type is genuinely unknown), and **don't `fmt.Sprintf(...)` into a `zap.String`** — choose the right-typed field instead of squeezing a typed value into a string. Don't use `log.Printf` as a logger in business code; exceptions: the `fmt.Println` in `cmd/opsctl/command/*` is CLI stdout for the user (not a log), and the `log.Printf` in `main.go` before the logger is initialized is also kept.
- **Key flows that must be logged:** IPC entry (`internal/app/**`), AI tool dispatch (`internal/ai/`), extension WASM calls (`pkg/extension/`, `internal/app/extension/`), approval / authorization (`internal/approval/`, `internal/app/opsctl/`), SSH/DB/Redis connection-pool open/close (`internal/sshpool/`, `internal/connpool/`), credential & key operations, migration runs, scheduled tasks, external command execution. Log all **three states (start / end / fail)** of an operation, and attach a correlatable ID (`assetID` / `sessionID` / `grantID` / `toolName` / `extension`, as applicable).
- **Logs don't replace error returns.** After `logger.Ctx(ctx).Error(..., zap.Error(err))` you must still `return err` — see [AGENTS.md → Defensive Code / Error Handling](../AGENTS.md#defensive-code--error-handling-no-meaningless-fallbacks). At `recover()` boundaries capture the stack with `zap.Stack("stack")`, e.g. `logger.Ctx(ctx).Error("xxx panic recovered", zap.String("sessionID", id), zap.Stack("stack"))`; in a standalone goroutine with no ctx, fall back to `logger.Default()`.
- **Level convention:** Error = a failure the user/caller needs to know about; Warn = self-healed or degraded; Info = key state changes (connection established, task scheduled, extension loaded); Debug = high-frequency detail (terminal keystrokes, SFTP frames, heartbeats), off by default.
- **Never log sensitive fields:** mask passwords / tokens / credential plaintext / SSH private keys / SQL parameter values before writing.

## ⚠️ Generated / auto-managed files

| Path | Producer | Regenerate |
|------|----------|------------|
| `frontend/wailsjs/go/app/App.{d.ts,js}`, `models.ts` | Wails (from `internal/app/*.go` + Go structs) | `make dev` / `wails build` |
| `frontend/wailsjs/runtime/*` | Wails runtime shim | ships with Wails CLI |
| `internal/**/mock_*/` | `mockgen` | `go generate ./...` |
| `internal/embedded/opsctl_bin` | `make build-cli-embed` | `make build-embed` |
| `frontend/packages/devserver-ui/dist/` | Vite (embedded by `cmd/devserver`) | `make build-devserver-ui` |

Lockfiles (`go.sum`, `frontend/pnpm-lock.yaml`) — never hand-edit; use `go mod tidy` / `pnpm add|remove|install`.

Build artifacts and caches are gitignored and safe to remove with `make clean`: `build/bin/`, `frontend/dist/`, coverage files, `tsconfig.tsbuildinfo`, `package.json.md5`, and the top-level `opskat` / `opsctl` / `devserver` binaries.
