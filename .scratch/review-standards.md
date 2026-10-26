You are the STANDARDS reviewer. Read-only. Do not edit files, commit, or run tests that mutate state. Work in D:\src_test_env\opskat on branch feature/assisted-approval-autopilot.

Fixed point: upstream/main = 2b9abfb7d98fce163a4b1efe75b5c8069bbbfc6f (three-dot).
Diff: git diff 2b9abfb7...HEAD
Commits: git log --oneline 2b9abfb7..HEAD
Scope is the whole branch (~163 files, +10800 lines): assisted approval / Autopilot, command-review extension point, then migration of the built-in TypeSafe reviewer into extensions/jev-command-review. Windows cp/test commits mixed in via a merge are out of scope for findings unless they violate a standard inside this feature.

Standards sources (read the relevant sections, do not recite them):
- AGENTS.md at repo root (SOLID, TDD/fix policy, defensive code, reuse-first)
- docs/DEVELOP.md (logging, generated files, commit emoji — commits already landed, do not nit emoji)
- docs/ARCHITECTURE.md only where the diff touches a seam

Smell baseline (judgement calls only; a documented repo standard overrides the baseline; skip anything tooling already enforces):
- Mysterious Name, Duplicated Code, Feature Envy, Data Clumps, Primitive Obsession, Repeated Switches, Shotgun Surgery, Divergent Change, Speculative Generality, Message Chains, Middle Man, Refused Bequest.
Each smell: what it is, then how to fix. Name the smell and quote the hunk. Never a hard violation.

Report, per file/hunk where relevant:
(a) every place the diff violates a documented standard: cite the standard (file + the rule);
(b) any baseline smell: name it and quote the hunk.
Distinguish hard violations from judgement calls. Skip prettier/eslint/gofmt nits. Under 400 words.

Focus on new production code, especially:
internal/service/command_review_svc/{review,redact,decode_exec,repo_cache}.go
internal/ai/permission/{review,permission,checker,type_registry}.go
internal/service/extension_svc/{active_reviewer,settings,provenance}.go
pkg/extension/{review,settings_header,host_capability,manifest,runtime}.go
pkg/extsdk/review.go
internal/pkg/typesafe/client.go
internal/pkg/jevreview/jevreview.go
extensions/jev-command-review/main.go
cmd/opsctl/command/{exec,exec_stdin,ext,approval}.go
frontend settings/approval/permission components only if they break a documented frontend rule (notify.ts, no driver-id branches, reuse).

Return the report as the worker_done summary body. No file edits.
