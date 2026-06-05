# AGENTS.md

This file is the single source of guidance for AI coding agents (Claude Code at claude.ai/code, Codex, etc.) working in this repository. `CLAUDE.md` is just a pointer to this file (`@AGENTS.md`) — edit guidance here, not there.

> **Before any development, first read [docs/DEVELOP.md](docs/DEVELOP.md).** This file keeps only the cross-cutting **principles** — SOLID / high cohesion, low coupling, Fix policy — TDD, Reuse first, defensive code / error handling. Development details — common commands, the architecture & subsystem map, commit / CI / testing conventions, logging rules for key flows, the generated-files list — all live in [docs/DEVELOP.md](docs/DEVELOP.md); apply them together with the principles here. **Before editing / reviewing any contributor doc (this file, `CLAUDE.md`, `docs/*`)**, first read [docs/DOC-MAINTENANCE.md](docs/DOC-MAINTENANCE.md): doc-set organization rules + fact-check / anti-drift discipline against the current branch.

## Project Overview

OpsKat — AI-first desktop app for managing remote infra (SSH, MySQL/PostgreSQL, Redis, MongoDB, Kafka, K8s, etcd). **Wails v2** (Go 1.26 + React 19), IPC only — no HTTP API. Module: `github.com/opskat/opskat`.

Extension source lives in the sibling repo `../extensions/`. For the architecture layering, subsystems, data, and frontend structure, see [docs/DEVELOP.md → Architecture](docs/DEVELOP.md#architecture).

## High Cohesion, Low Coupling / SOLID

The architecture map (see [docs/DEVELOP.md → Architecture](docs/DEVELOP.md#architecture)) is a set of seams. These rules keep them seams instead of letting logic bleed across them — they are **SOLID applied to *this* codebase** (the principle letter is tagged on each rule), not generic theory to recite. They build on the layering rule (bindings → service → repository) and [Reuse first](#reuse-first--grep-before-writing) — don't restate those, apply them.

- **One reason to change per unit (SRP).** Protocol logic lives in its own handler + `*_policy.go`; the frontend keeps one Zustand store per domain (`src/stores/`) and components depend on stores/hooks, not on sibling components' internals. If a single feature edit forces changes across three unrelated packages, the responsibility leaked — move it back behind one seam.
- **Extend by registration, not by editing a switch (OCP).** New asset type → implement `assettype.AssetTypeHandler` and `Register()` in the file's `init()` (see `ssh.go`/`redis.go`/`k8s.go`); new repo → `RegisterXxx()` + `Xxx()` getter; new AI tool → register in the tool registry; new policy → its own `*_policy.go`. **Never branch on a type string** (`if assetType == "ssh"`, `switch protocol`) in shared code — that's the coupling the registry exists to remove. Cross-type commonality goes in a shared helper the handlers *call* (e.g. `validateRemoteServerArgs`), not in branches inside the dispatcher. Open for extension (register a handler), closed for modification (don't touch the dispatcher). Adding a whole asset type end-to-end — backend handler + frontend registration + form/detail/serializers, and which couplings still need shared edits — is documented step-by-step in [docs/adding-an-asset-type.md](docs/adding-an-asset-type.md).
- **Depend on the interface, call through the getter (DIP + LSP).** Services consume `asset_repo.Asset()` (the `AssetRepo` interface), never a concrete repo struct or GORM directly — and every implementation must be substitutable behind that interface without callers special-casing a concrete type (LSP); that substitutability is exactly what makes `mock_*/` work in tests. Don't reach past a seam: a service must not import another service's repository or call into `App`; `internal/app/*` must not touch repositories or `db`. If you need another domain's data, go through its service/getter.
- **Keep the boundary contract narrow (ISP).** The `map[string]any` tool args are parsed *once* through the shared `Arg*` helpers (`ArgString`/`ArgInt`/`ArgInt64`), not re-parsed per handler. Prefer option-object args (e.g. `ListOptions`) over long positional/boolean lists. Each handler declares only the fields it needs in `ValidateCreateArgs`; a package exposes the smallest surface callers actually use.

## Fix policy — TDD, root cause, in scope

- **Confirm the bug is real, then reproduce it as a failing test** (`go test` / `vitest`) before touching impl. Don't trust the report at face value — prove it reproduces, failing for the right reason (same error/assertion the user reported). If it doesn't reproduce, say so and stop instead of "fixing" a phantom. If a test isn't reasonable, say so explicitly. No exceptions for "obvious" one-liners.
- **Stay in scope.** A fix touches the producer, its test, and at most an in-scope drift under the cursor (stale docstring, lying AGENTS.md line, obvious one-liner) — fix those *now*, don't TODO. No drive-by refactors / rename sweeps / formatter passes / dead-code cleanup in the same change. Multi-day refactors or hot-subsystem rework → flag and ask.
- **Fix root causes — refactor over patch.** Don't guard at the call site to mask a bad producer; fix the producer. Don't re-normalize a field at multiple consumers; normalize once at the boundary. A "why this workaround" comment usually means the underlying code should change instead. When the clean fix means restructuring the unit you're already touching, prefer that refactor over bolting on a band-aid — restructuring the producer and its seam is *in*-scope, distinct from the drive-by refactors ruled out above. A patch that leaves the root defect in place is not a fix.
- **Verify by observing, not asserting.** A desktop GUI can't be clicked by an agent — reproduce/verify through observable side-effects: run it headlessly (`opsctl`) or run the app, then read the structured logs (`logs/opskat.log`) and DB (`opskat.db`, esp. `audit_logs`). How-to in [docs/testing-debugging-guide.md](docs/testing-debugging-guide.md).

## Defensive Code / Error Handling (No meaningless fallbacks)

Defensive code for cases that can't happen, swallowed errors, or shims for retired data become load-bearing noise — future readers can't tell what's real, and the bug stays hidden behind the guard.

- **Validate at boundaries only.** IPC into `internal/app/*.go` and WASM host calls in `pkg/extension/host.go` are boundaries — check them. Go-to-Go between `service`/`repository`/`internal/ai/` is trusted — no `if x == nil` between them.
- **Don't double-default user-configurable fields.** `Icon` / `Type` / `Color` already have canonical helpers (`getIconComponent` + `getIconColor`, `getAssetType`). `value || "default"` at the call site overrides the user's intentional empty value and hides bugs in the helper.
- **Don't swallow errors.** `if err != nil { return nil }` / `catch { return defaultState }` masks failure and propagates corrupt state. Surface it; only catch when there's a concrete recovery for a specific error type.
- **No runtime shims for retired data.** Migrations in `/migrations/` run once. Don't sprinkle `if legacyField != "" { ... }`, `_renamed` placeholders, or `// removed in v1.x` comments — delete the field from the model.
- **"Fallback" comments are a smell.** `// just in case` / `// guard against nil` / `// legacy-data compat` — if X can happen, fix the producer; if not, delete the line. `recover()` is only for goroutine boundaries that must not crash the app (extension WASM dispatch, AI tool execution) and must record the panic.

## Reuse first — grep before writing

Parallel copies drift within weeks. Before any new component/hook/util/Go helper, grep for the existing one.

- **Shared UI primitives** exist: `AssetSelect` / `AssetMultiSelect` / `GroupSelect`, `TreeSelect` / `TreeCheckList`, `ConfirmDialog`, `PasswordSourceField`, `IconPicker`, terminal panes, query result grid, tab system, shortcut store. Don't re-derive expand/collapse, tri-state checkboxes, search/pinyin, shortcuts, approval flows, or icon resolution.
- **Shared filters/loading** belong in `useAssetStore` / `useAssetTree` / `useGroupTree` / `useShortcutStore`. New filter → hook option, not inline.
- **Cross-cutting concerns** (audit, AI tool registration, approval, credential encryption, connection pools, i18n) have canonical entry points — don't spin up a second one. Logging rules are in [docs/DEVELOP.md → Logging for key flows](docs/DEVELOP.md#logging-for-key-flows).
- **Toast notifications** go through `frontend/src/lib/notify.ts`: for success use `notifyCopied` (copy / clipboard — top-center, a 1s flash) / `notifySuccess` (other successful operations — top-center); **don't call `toast.success` directly**. Errors / warnings / info still use `toast.error` / `toast.warning` / `toast.info` and stay at the default bottom-right position. Terminal / AI / query views all refresh bottom-up, so a success toast at the bottom would occlude the output (#135).

Heuristics: importing a primitive (`lucide-react`, tree, Radix, `ConfirmDialog`, xterm) **and** an entity store from a new file usually means you're re-implementing a picker/pane/dialog. Copying >10 lines → extract. Same fix in two near-identical blocks → the second is the bug; delete it, call the first.
