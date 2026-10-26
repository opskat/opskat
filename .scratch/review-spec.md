You are the SPEC reviewer. Read-only. Do not edit files, commit, or run mutating commands. Work in D:\src_test_env\opskat on branch feature/assisted-approval-autopilot.

Fixed point: upstream/main = 2b9abfb7 (three-dot). Diff: git diff 2b9abfb7...HEAD. The branch includes step 1 AND step 2 of the extension reviewer, so judge the code against the specs as amended by their "实现说明" sections, not the superseded "step 1 only" sentences.

Spec sources, read fully:
1. docs/specs/2026-09-29-assisted-approval-autopilot.md
2. docs/specs/2026-10-14-extension-command-reviewer.md (including 实现说明 at the bottom — that section overrides earlier decisions it names: built-in reviewer removed, auto-select single reviewer, pure reviewer needs no asset type, settings_get password placeholder, host timeout+one retry, error results not cached, test-review entry).

Report:
(a) requirements the specs asked for that are missing or partial;
(b) behaviour in the diff that wasn't asked for (scope creep) — ignore the merged Windows cp/test commits (ed94dac4 and ancestors about Windows tests / drive-letter cp); those are an intentional merge;
(c) requirements that look implemented but where the implementation looks wrong.
Quote the spec line for each finding. Under 400 words.

Hard invariants to verify in code, not just names:
- Reviewer can only turn "needs confirmation" into allow; never touches rule-deny or Unreviewable.
- Missing reviewer / error / timeout / bad JSON = review failure: assisted asks a human, autopilot denies, never allows.
- Redaction replaces values only; unparseable shell is not sent (review failure). Decode-then-exec reviews the decoded script; undecodable = failure and is not sent.
- Cache key is service+model+question version+asset type+raw command hash; failures not cached; threshold re-applied on hit.
- Mode only from desktop asset/group settings, no CLI flag or env.
- Piped stdin on opsctl exec is not model-allowed.
- Only store-signed extensions can be the reviewer on a release build; nightly/dev may use local and must be marked unsigned. Load rechecks package sha256.
- Secret settings are not returned in plaintext; {{setting.field}} substituted only toward the declared endpoint.
- One active reviewer; none selected + exactly one enabled commandReview extension auto-selects; zero or many without a selection = no_reviewer failure.

Return the report as the worker_done summary body. No file edits.
