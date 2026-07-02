<p align="right">
<a href="./CONTRIBUTING.md">English</a> | <a href="./docs/CONTRIBUTING_ZH.md">中文</a>
</p>

# Contributing to OpsKat

Thank you for your interest in contributing to OpsKat! All forms of contribution are welcome — bug reports, feature ideas, documentation, and code.

This guide covers the contribution workflow. The detailed development handbook lives in [docs/DEVELOP.md](./docs/DEVELOP.md), and the architecture & subsystem map in [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md).

## Ways to Contribute

- **Report a bug** — open an [issue](https://github.com/opskat/opskat/issues/new/choose) using the bug report template. Reproduction steps and relevant logs help a lot.
- **Suggest a feature** — open a feature request, or start a thread in [Discussions](https://github.com/opskat/opskat/discussions) if it's still an early idea.
- **Security vulnerabilities** — please do **not** open a public issue; report privately via [GitHub Security Advisories](https://github.com/opskat/opskat/security/advisories/new).
- **Improve docs** — typo fixes and clarifications are always welcome.
- **Write code** — fix a bug or implement a feature. For anything non-trivial, open an issue first to discuss the approach before investing time.
- **Extensions** — extension source lives in a separate repository: [opskat/extensions](https://github.com/opskat/extensions).

## Development Setup

Prerequisites: [Go 1.26+](https://go.dev/), [Node.js 22+](https://nodejs.org/) with [pnpm](https://pnpm.io/), and the [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation).

```bash
make install        # Install frontend dependencies
make dev            # Development mode (hot reload)
```

The full command list (build, opsctl CLI, extension devserver, coverage, …) is in [docs/DEVELOP.md → Common Commands](./docs/DEVELOP.md#common-commands).

> ⚠️ Some files are **generated** (e.g. `frontend/wailsjs/`, `mock_*/`, lockfiles) — never hand-edit them. See [docs/DEVELOP.md → Generated / auto-managed files](./docs/DEVELOP.md#️-generated--auto-managed-files).

## Testing & Linting

Run these before opening a PR — CI runs the same checks:

```bash
make test                                 # Go tests
make lint                                 # Go lint
cd frontend && pnpm test && pnpm lint     # Frontend tests + lint
```

CI additionally runs the GUI e2e suite (`make test-e2e`, Playwright driving the real Wails app).

A few conventions to know:

- **Bug fixes start with a failing test.** Reproduce the bug as a `go test` / `vitest` case before touching the implementation, then fix the root cause — see the Fix policy in [AGENTS.md](./AGENTS.md).
- **Reuse first.** Before adding a new component / hook / helper, grep for an existing one — parallel copies drift apart quickly.
- Frontend style is enforced by Prettier (120 columns, 2-space indent).
- To verify a feature by observing logs / database / headless `opsctl`, see [docs/testing-debugging-guide.md](./docs/testing-debugging-guide.md) (written for AI coding agents); for GUI end-to-end details, see [docs/e2e-harness-guide.md](./docs/e2e-harness-guide.md).

## Commit Messages — gitmoji

The first character of the subject line is the **emoji glyph itself** (not the `:sparkles:` text code, and not a `feat:` / `fix:` prefix):

```
✨ Add Kafka consumer group lag view
🐛 Fix SFTP upload progress not updating
```

Common emoji: ✨ feature · 🐛 bugfix · ♻️ refactor · 🎨 UI · ⚡️ perf · 🔒 security · 🔧 config · ✅ tests · 📄 docs. The full table and the issue-number rules are in [docs/DEVELOP.md → Commit message](./docs/DEVELOP.md#commit-message--gitmoji).

Commit messages may be written in Chinese or English — the emoji-first rule is language-agnostic.

## Pull Requests

1. **Fork** the repository on GitHub (the "Fork" button on [opskat/opskat](https://github.com/opskat/opskat)), then clone your fork:

   ```bash
   git clone https://github.com/<your-username>/opskat.git
   cd opskat
   ```

2. **Create a branch from `main`**:

   ```bash
   git checkout -b my-feature main
   ```

3. Make your changes, commit with gitmoji messages, and make sure tests and lint pass locally.

4. **Push** the branch to your fork, then **open a Pull Request** on GitHub against `opskat/opskat`'s `main` branch:

   ```bash
   git push -u origin my-feature
   ```

5. Fill in the PR template — gitmoji in the title, screenshots for any UI change. Keep each PR focused on a single change; discuss large refactors in an issue first.

6. Wait for CI to pass (Go lint/tests, frontend lint/tests, GUI e2e) and respond to review feedback.

## License

OpsKat is licensed under [GPLv3](./LICENSE). By contributing, you agree that your contributions are licensed under the same license.
