# Doc Maintenance & Fact-Check

> **Read this before adding / editing / reorganizing / reviewing any contributor doc** (`AGENTS.md`, `CLAUDE.md`, `docs/*`). It has two jobs: keep the doc set **organized** (links reachable, index current, no duplication), and keep every statement **factually true for the current branch**.
>
> A distinction to keep: this is about **contributor docs** (in-repo, for agents / developers). `README.md` / `docs/README_zh.md` are the user-facing project intro and are out of scope here.

## Why this exists

Contributor docs describe a living codebase, so two classes of problem recur:

- **Stale facts** — a package / file is renamed, a directory moves, a count changes, and the doc still shows the old value. Real example (**fixed alongside this guide**): `docs/DEVELOP.md` once placed the AI policy checkers as `command_policy.go` under `internal/ai/`; they actually live in **`internal/ai/policy/`**, and the SQL one is `query_policy.go` (there is no `command_policy.go` file; shell-command rules are in `command_rule.go` / `command_shell.go`).
- **Branch / repo leakage** — something that only exists on a feature branch or in a sibling repo gets written as if already shipped on `main`. opskat sits next to two **independent** sibling repos, `../extensions` and `../agentre` (see memory [[reference_extensions_repo]] / [[reference_agentre_repo]]); uncommitted code in your checkout, or a sibling repo's design, is easy to mistake for something this repo already has. agentre is a downstream renamed fork of opskat — **don't port its code / design into opskat docs** (see [[feedback_no_agentre_port_into_opskat]]).

**Rule of thumb: if you can't `git grep` it in committed code on this branch, don't write it.** Verify with git-aware commands (`git grep` / `git ls-files` / `git ls-tree`) — **not** bare `rg` / `ls`, which also match **untracked** files in the working tree, so feature-branch code you have locally but haven't committed to `main` masquerades as "shipped".

> ⚠️ When adding a new doc, confirm it has been `git add`ed before linking it into the set — otherwise a bare `ls` sees it but `git ls-files` doesn't, and CI / anyone cloning won't get it.

## Doc set & responsibilities (don't duplicate — cross-link)

| Doc | Owns |
| --- | --- |
| [`../AGENTS.md`](../AGENTS.md) | Single source of truth for engineering **principles** (SOLID, Fix policy — TDD, Reuse first, defensive code). `CLAUDE.md` only `@import`s it. |
| [`../CLAUDE.md`](../CLAUDE.md) | Just a one-line `@AGENTS.md` pointer — **don't write content here**; change principles in `AGENTS.md`. |
| [`./DEVELOP.md`](./DEVELOP.md) | The concrete "how to": common commands, commit / CI / testing conventions, logging rules for key flows, generated-files list. |
| [`./ARCHITECTURE.md`](./ARCHITECTURE.md) | The **structure**: process topology, backend layering, request lifecycle, per-subsystem map, data model, and the AI / extension / opsctl flows. Owns the architecture & subsystem map; `DEVELOP.md` and `AGENTS.md` link here. |
| [`./DESIGN.md`](./DESIGN.md) | The **design system**: light/dark oklch color tokens, theming, the `@opskat/ui` component palette, the desktop pane shell, domain surfaces, motion / state patterns, accessibility, and a new-surface recipe. Read before building any UI; `DEVELOP.md` / `AGENTS.md` link here. (Visual/presentation layer — complements `ARCHITECTURE.md` §9, which owns frontend *structure*.) |
| [`./adding-an-asset-type.md`](./adding-an-asset-type.md) | Step-by-step how-to for adding a new built-in asset type: the backend `AssetTypeHandler` + frontend `registerAssetType` seams, what's register-based vs still requires editing shared code (query/terminal/AI-mention couplings). |
| [`./testing-debugging-guide.md`](./testing-debugging-guide.md) | Feature verification / debugging: reading logs (`logs/opskat.log`), querying the DB (`audit_logs` in `opskat.db`), headless functional testing with `opsctl` (for agents, in English). |
| [`./e2e-harness-guide.md`](./e2e-harness-guide.md) | GUI end-to-end harness (Playwright × the real Wails app): the committed core-flow suite (`make test-e2e`) + ad-hoc functional verification (gitignored `e2e/scratch/`, `make test-e2e-scratch`), isolation guarantees, and harness-engineering gotchas. Owns everything GUI-e2e; `testing-debugging-guide.md` only points here. |
| [`../CONTRIBUTING.md`](../CONTRIBUTING.md) / [`./CONTRIBUTING_ZH.md`](./CONTRIBUTING_ZH.md) | Contributor guide (EN / ZH mirror — keep them in sync): contribution channels, setup, the fork → branch → PR flow, commit / CI expectations. Summarizes and links into `DEVELOP.md` / `AGENTS.md` — owned facts stay there, not here. |
| [`./DOC-MAINTENANCE.md`](./DOC-MAINTENANCE.md) | This guide: doc-set organization rules + fact-check / anti-drift discipline. |
| `./superpowers/{plans,specs}/` | Date-named design / plan **archives**. A snapshot of one piece of work at the time, **not** current truth — don't backfill "current state" from here. |

opskat has **no separate docs index page**; `AGENTS.md` is the entry point (`CLAUDE.md → @AGENTS.md → docs/DEVELOP.md → the rest`).

When you move a fact, move it to the doc that **owns** it and cross-link — never copy the same fact into two places, or they'll drift apart.

## Checklist 1 — Organization (every doc change)

- [ ] Add / rename / delete a doc → update the *Doc set* table above, **and** every reference to it in `AGENTS.md` / `DEVELOP.md` (the entry point is `AGENTS.md` — don't orphan a new doc).
- [ ] All relative links are reachable (run the link check in *One-shot* below).
- [ ] Nothing that "only exists on a feature branch / sibling repo" is written as current `main` — delete it, or explicitly mark it "planned (branch `X`)".
- [ ] No fact duplicated across docs; the owning doc holds it, others link to it.

## Checklist 2 — Fact-check (when docs state specifics)

Verify each one against the code. Common claim types in opskat and how to check them:

| Claim in the docs | How to check |
| --- | --- |
| Backend layer / subsystem directory exists | `git ls-tree --name-only -d HEAD internal/` (then `git ls-files internal/<name>/` to confirm a subsystem, e.g. `sshpool` / `connpool` / `approval`) |
| **Asset-type list** (N adapters) | `git grep -hn "Register(&" -- internal/assettype/*.go \| grep -v _test` — enumerate them one by one (ssh / database / redis / mongodb / kafka / k8s / etcd / serial), **don't hardcode a number**. Registration-based extension, no `switch assetType` |
| A file / package path exists **by exact name** | `git ls-files 'internal/ai/policy/*_policy.go'` — renamed / moved files are the **#1 drift source** (the `command_policy.go` trap above) |
| AI dispatches extensions via a **single `exec_tool`** | `git grep -n "tool_handler_ext" -- internal/ai` (one `exec_tool` dispatcher, not one AI tool per extension; see [[feedback_ext_exec_single_tool]]) |
| Migration directory / count | `git ls-files 'migrations/*.go' \| grep -v _test \| wc -l` (enumerate; new migrations are **appended**, old files unchanged) |
| Frontend stores (one per domain) | `git ls-files 'frontend/src/stores/*.ts' \| grep -v '\.test\.'` |
| Locales (which / namespace) | `git ls-files 'frontend/src/i18n/locales/*/common.json'` — two, `zh-CN` / `en`; the i18next namespace is `common` |
| A Make target exists | `git grep -nE '^<target>:' -- Makefile` (every `make x` referenced in the docs must be findable in `Makefile`) |
| Soft delete via `Status`, not GORM | `git grep -n "StatusActive *=\|StatusDeleted *=" -- internal/model/entity` (`StatusActive=1` / `StatusDeleted=2`, defined in `asset_entity/asset.go`) |
| Credential encryption | `git grep -niE "argon2\|gcm\|keychain" -- internal/service/credential_svc` (Argon2id + AES-256-GCM, master key in the OS keychain) — the encryption is in `credential_svc`; `internal/bootstrap` only resolves / injects the master key (`ResolveMasterKey`), so don't grep `bootstrap` alone and assume you found it |
| Commit emoji convention aligns with changelog categories | check against `.claude/skills/release/SKILL.md`; only a single commit intentionally linked to an issue ends with that issue as `#<number>` (plain commits, PR work, and PR / review-comment follow-ups do not need a `#xxx` suffix; generally use issue numbers, not PR numbers; see [[feedback_commit_issue_ref]]) |
| Constructor / function signatures | open the file and compare parameter by parameter — no grep shortcut |
| **Generated files** (wailsjs / mock / opsctl_bin) | see *Same name & generated* item 4 below — **don't** use `git ls-files` to check artifacts |

Three traps that keep biting (opskat has hit all of them):

1. **Working tree ≠ committed.** Bare `rg` / `ls` also match **untracked** files, so feature-branch / sibling-repo code reads as "shipped" — exactly the leakage failure above. Always use `git grep` / `git ls-files` / `git ls-tree` so only **committed** code counts.
2. **Same name, different thing.** In opskat "command policy" is at least three things: `internal/ai/policy/command_rule.go` (shell-command rule matching), the `command_policy` column on the assets table (JSON config), and `asset_entity.DefaultCommandPolicy()` (default-policy constructor — itself a re-export of `policy.DefaultCommandPolicy` in `internal/model/entity/policy`) — don't conflate them. Same on the frontend: `@opskat/ui` (`packages/ui`, the main app) ≠ `packages/devserver-ui` (embedded by `cmd/devserver`).
3. **Counts drift silently.** Enumerate the counts of asset types / migrations / locales / stores from the canonical source — don't trust prose in the docs or memory. E.g. `etcd` was added to the asset types later, so any list that hardcodes a number or omits `etcd` is stale.
4. **Generated ≠ source of truth.** `frontend/wailsjs/**`, `internal/embedded/opsctl_bin`, and `frontend/packages/devserver-ui/dist/` are all **generated and gitignored** — `git ls-files` can't find them at all. Verify the **producer** behind them: for Wails bindings look at the Go side in `internal/app/*.go`, don't treat the generated `.ts` as truth; for mocks look at the `go generate ./...` source. Generated-files list: see [DEVELOP.md → Generated / auto-managed files](./DEVELOP.md#️-generated--auto-managed-files).

## One-shot verification

Deliberately all git-aware: each line reads only **committed** code (`git ls-files` / `git ls-tree` / `git grep`), so untracked feature-branch / sibling-repo files in the working tree can't masquerade as present. Run it from the repo root and compare the output line by line against the docs:

```bash
echo "== backend layer dirs =="; git ls-tree --name-only -d HEAD internal/
echo "== asset types (registered handlers — enumerate, don't hardcode a number) =="
git grep -hn "Register(&" -- internal/assettype/*.go | grep -v _test
echo "== AI per-protocol policy checkers (in internal/ai/policy/, NOT internal/ai/) =="
git ls-files 'internal/ai/policy/*_policy.go'
echo "== migrations (count) =="; git ls-files 'migrations/*.go' | grep -v _test | wc -l
echo "== zustand stores (one per domain) =="
git ls-files 'frontend/src/stores/*.ts' | grep -v '\.test\.'
echo "== locales (dirs + namespace) =="; git ls-files 'frontend/src/i18n/locales/*/common.json'
echo "== soft-delete constants =="; git grep -n "StatusActive *=\|StatusDeleted *=" -- internal/model/entity
echo "== make targets (eyeball against every 'make x' in the docs) =="
git grep -nE '^[a-z][a-z0-9-]*:' -- Makefile
echo "== generated artifacts: NOT tracked → verify the PRODUCER, not the file =="
for p in frontend/wailsjs internal/embedded/opsctl_bin frontend/packages/devserver-ui/dist; do
  git ls-files --error-unmatch "$p" >/dev/null 2>&1 \
    && echo "TRACKED?!  $p (DEVELOP.md says it's generated — double-check)" \
    || echo "generated  $p — check the producer (internal/app/*.go / go generate / vite build)"
done
```

Link integrity — confirm every relative markdown link in the core docs is reachable (`CLAUDE.md`'s `@AGENTS.md` is an import directive, not a relative markdown link, so it's not checked here; separately ensure it remains that single import line):

```bash
for doc in AGENTS.md docs/ARCHITECTURE.md docs/DEVELOP.md docs/DESIGN.md docs/testing-debugging-guide.md docs/e2e-harness-guide.md docs/DOC-MAINTENANCE.md; do
  grep -oE '\]\(([^)]+)\)' "$doc" | sed -E 's/^\]\(|\)$//g' | grep -vE '^https?:|^#' | while read -r link; do
    target="$(dirname "$doc")/${link%%#*}"
    [ -e "$target" ] && echo "ok     $doc → $link" || echo "BROKEN $doc → $link"
  done
done
```

## When you find a discrepancy

Change the **doc** to match the code — code on this branch is the source of truth. Exception: if the code itself is wrong (a real bug), fix the code per [AGENTS.md → Fix policy — TDD](../AGENTS.md#fix-policy--tdd-root-cause-in-scope) and explain it in the PR. Either way, **never silently skip a check that didn't pass** — call it out in the PR description so the reviewer can confirm.

**Delete or correct stale / dead content outright — don't keep it around.** Old package names / file names / numbers / nonexistent functions (e.g. a "helper" that was written into docs but never existed in the code) are just noise — don't preserve an old value as history with wording like "used to be" / "previously" / "compat", and don't demote it to a comment. Readers can't tell which line still holds, and the stale fact keeps misleading. **The only thing you may "keep" is an explicitly-flagged stale string that still lives in the *code*** (e.g. the leftover `ops-cat` / `.opscat` in `opsctl`'s help text, see [testing-debugging-guide.md](./testing-debugging-guide.md)) — these must be marked "that is stale" so the reader knows it's a code-side cleanup item, not a current fact. Any hardcodable number (asset types / migrations / `Default()` call counts…) should be reworded to "enumerate from the canonical source", leaving one fewer hardcoded value to drift silently.
