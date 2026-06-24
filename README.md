<p align="right">
<a href="./README.md">English</a> | <a href="./docs/README_zh.md">中文</a>
</p>

<h1 align="center">
<img src="build/appicon.png" width="128" height="128"/><br/>
OpsKat
</h1>

<p align="center">
<b>Your one-stop server operations workbench</b><br/>
SSH, databases, Redis, Kafka, Kubernetes… everything ops has to touch, unified in a single cross-platform desktop app. And you can let AI execute it for you in natural language — every step guarded by policy and audit.
</p>

<p align="center">
<a href="https://opskat.dev/">Website</a> ·
<a href="https://opskat.dev/docs/getting-started/installation">Docs</a> ·
<a href="https://github.com/opskat/opskat/releases">Download</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go">
  &nbsp;
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react&logoColor=white" alt="React">
  &nbsp;
  <img src="https://img.shields.io/badge/Wails-v2-EB4034?style=for-the-badge&logo=wails&logoColor=white" alt="Wails">
  &nbsp;
  <img src="https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey?style=for-the-badge&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgZmlsbD0ibm9uZSIgc3Ryb2tlPSJ3aGl0ZSIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiPjxwYXRoIGQ9Ik0yMCAxNlY3YTIgMiAwIDAgMC0yLTJINmEyIDIgMCAwIDAtMiAydjltMTYgMEg0bTE2IDAgMS4yOCAyLjU1YTEgMSAwIDAgMS0uOSAxLjQ1SDMuNjJhMSAxIDAgMCAxLS45LTEuNDVMNCAxNiIvPjwvc3ZnPg%3D%3D" alt="Platform">
</p>

<p align="center">
  <a href="https://t.me/opskat"><img src="https://img.shields.io/badge/Telegram-OpsKat-26A5E4?style=for-the-badge&logo=telegram&logoColor=white" alt="Telegram"></a>
  &nbsp;
  <a href="https://qm.qq.com/q/sERnNKEzeg"><img src="https://img.shields.io/badge/QQ_Group-Join-12B7F5?style=for-the-badge&logo=qq&logoColor=white" alt="QQ Group"></a>
</p>

<p align="center">
  <img src="docs/images/screenshot-main.png" alt="OpsKat Screenshot">
</p>

## 🧭 About

Managing servers usually means juggling a pile of tools — SSH clients, database GUIs, Redis managers, Kafka consoles — and constantly switching between them. OpsKat brings all of those everyday asset operations into a single interface, so one app is enough. On its own, that's already a full ops workbench.

On top of it sits a layer of AI: just say what you need in natural language, and the AI agent connects and runs it for you — pulling logs, running SQL, checking cluster status, and more. Every step is backed by policy enforcement and full audit logging, so handing work to the AI stays safe.

**If you find it useful, please give us a Star ⭐ — it means a lot!**

## ⬇️ Install

### Download

Grab the latest build for your platform — **macOS, Windows, or Linux** — from the [Releases page](https://github.com/opskat/opskat/releases). No Go/Node toolchain required: download and run. Step-by-step notes are in the [installation docs](https://opskat.dev/docs/getting-started/installation).

### First run

1. **Add an asset** — an SSH host, database, Redis, and so on — or import from your SSH config / Tabby.
2. **Connect** — open a terminal, run a query, or browse keys and collections.
3. *(Optional)* **Configure an AI provider**, then just tell the agent what you need.

## 📦 Supported Assets

| Category | Assets |
| :-- | :-- |
| **Servers** | <img src="https://img.shields.io/badge/SSH-4D4D4D?style=flat-square&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgZmlsbD0ibm9uZSIgc3Ryb2tlPSJ3aGl0ZSIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiPjxyZWN0IHdpZHRoPSIyMCIgaGVpZ2h0PSIxNCIgeD0iMiIgeT0iMyIgcng9IjIiLz48bGluZSB4MT0iOCIgeDI9IjE2IiB5MT0iMjEiIHkyPSIyMSIvPjxsaW5lIHgxPSIxMiIgeDI9IjEyIiB5MT0iMTciIHkyPSIyMSIvPjwvc3ZnPg%3D%3D" alt="SSH"> <img src="https://img.shields.io/badge/Serial-5A6B7B?style=flat-square&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgZmlsbD0ibm9uZSIgc3Ryb2tlPSJ3aGl0ZSIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiPjxjaXJjbGUgY3g9IjEwIiBjeT0iNyIgcj0iMSIvPjxjaXJjbGUgY3g9IjQiIGN5PSIyMCIgcj0iMSIvPjxwYXRoIGQ9Ik00LjcgMTkuMyAxOSA1Ii8%2BPHBhdGggZD0ibTIxIDMtMyAxIDIgMloiLz48cGF0aCBkPSJNOS4yNiA3LjY4IDUgMTJsMiA1Ii8%2BPHBhdGggZD0ibTEwIDE0IDUgMiAzLjUtMy41Ii8%2BPHBhdGggZD0ibTE4IDEyIDEtMSAxIDEtMSAxWiIvPjwvc3ZnPg%3D%3D" alt="Serial"> |
| **Databases** | <img src="https://img.shields.io/badge/MySQL-4479A1?style=flat-square&logo=mysql&logoColor=white" alt="MySQL"> <img src="https://img.shields.io/badge/PostgreSQL-4169E1?style=flat-square&logo=postgresql&logoColor=white" alt="PostgreSQL"> <img src="https://img.shields.io/badge/SQL_Server-CC2927?style=flat-square" alt="SQL Server"> <img src="https://img.shields.io/badge/SQLite-003B57?style=flat-square&logo=sqlite&logoColor=white" alt="SQLite"> <img src="https://img.shields.io/badge/Redis-FF4438?style=flat-square&logo=redis&logoColor=white" alt="Redis"> <img src="https://img.shields.io/badge/MongoDB-47A248?style=flat-square&logo=mongodb&logoColor=white" alt="MongoDB"> <img src="https://img.shields.io/badge/etcd-419EDA?style=flat-square&logo=etcd&logoColor=white" alt="etcd"> |
| **Middleware** | <img src="https://img.shields.io/badge/Apache_Kafka-231F20?style=flat-square&logo=apachekafka&logoColor=white" alt="Kafka"> <img src="https://img.shields.io/badge/Kubernetes-326CE5?style=flat-square&logo=kubernetes&logoColor=white" alt="Kubernetes"> |

_More asset types are on the way via the plugin system._

## 🖥️ A Complete Ops Workbench

Even before you turn on the AI, OpsKat is a full-featured terminal and asset manager:

- Tree-structured grouping for every supported asset type
- Split-pane terminal with customizable themes
- SFTP file browser
- Jump host chain connections
- SQL query editor (MySQL/PostgreSQL via SSH tunnel)
- Redis command execution with key browser
- MongoDB collection browsing and query execution
- Kafka cluster, topic, message, consumer group, ACL, Schema Registry, and Kafka Connect management
- Port forwarding and SOCKS proxy
- Encrypted credential storage
- Import from SSH config / Tabby

## 🤖 Let AI Operate for You

Configure an AI provider and you can describe what you need in plain language — the agent connects and does it for you:

- **"Show me the recent nginx error logs on web-01"** → AI automatically SSHs in, runs the command, and returns the results
- **"Count users by status in the db-prod users table"** → AI connects to the database via SSH tunnel and executes the SQL query
- **"List lagging Kafka consumer groups in kafka-prod"** → AI checks Kafka metadata and group lag under policy control
- **"Check the health of the k3s cluster"** → AI runs kubectl commands and summarizes node and pod status

### How the AI works

- **Bring your own key** — configure any **OpenAI- or Anthropic-compatible** provider; your API key is encrypted and stored locally.
- **Use almost any model** — OpenAI, Anthropic (Claude), DeepSeek, Gemini, Qwen, GLM, Kimi, MiniMax… or a self-hosted/local endpoint (e.g. an OpenAI-compatible Ollama).
- **Direct connection** — OpsKat talks to the model you configure directly; nothing is relayed through our servers, and you are not locked into any vendor.
- **You stay in control** — the AI only proposes actions; every command runs from your machine against your servers, under the policy and audit controls below.

## 🛡️ Security & Audit

Giving AI permission to operate on your servers — how do you keep it safe?

- **Operation policies** — SSH/serial commands, SQL statements, Redis, MongoDB, Kafka, Kubernetes, and etcd operations all support allow/deny lists. SQL is analyzed by a parser that automatically blocks dangerous operations like DELETE/UPDATE without WHERE clauses
- **Policy groups** — Built-in templates (Linux read-only, dangerous command deny, etc.) plus custom user-defined groups
- **Pre-approved permissions** — AI or opsctl can request a batch of command patterns upfront. Once approved, matching commands execute automatically without per-command confirmation
- **Audit logs** — Every operation is automatically recorded: who, when, which server, what command, and the full decision trail

## 🎥 Demo

https://github.com/user-attachments/assets/2af6e52e-637c-4398-9c8b-8b39b4238b12

https://github.com/user-attachments/assets/035fc0df-230c-456b-87bd-8a4a125feaec

## ⌨️ opsctl — CLI & AI Coding Tool Integration

> For CLI users and AI coding assistants. If you only use the desktop app, you can skip this.

OpsKat ships a standalone CLI tool (`opsctl`), primarily designed for AI coding assistants like **Claude Code**, **Codex**, and **Gemini CLI**. One-click skill installation from the desktop app teaches these AI assistants to use `opsctl` — so they can directly manage servers, check logs, query databases, and troubleshoot production issues.

When the desktop app is running, opsctl reuses its connection pool and approval workflow, with all operations subject to the same policy enforcement and audit logging.

You can also use it manually:

```bash
opsctl exec web-01 -- tail -n 100 /var/log/nginx/error.log
opsctl sql db-prod "SELECT status, COUNT(*) FROM users GROUP BY status"
opsctl ssh web-01
```

## 🛠️ Tech Stack

| | |
|---------|------------|
| Desktop | [Wails v2](https://wails.io/) (Go + Web) |
| Frontend | React 19 + TypeScript + Tailwind CSS |
| Backend | Go 1.26, SQLite |

## 🔧 Build from Source

> For contributors. If you just want to use OpsKat, see **Install** above.

**Prerequisites:** [Go 1.26+](https://go.dev/), [Node.js 22+](https://nodejs.org/) with [pnpm](https://pnpm.io/), [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation)

```bash
make install        # Install frontend dependencies
make dev            # Development mode (hot reload)
make build          # Production build
make build-embed    # Production build with embedded opsctl
make build-cli      # Build opsctl CLI only
```

## ❓ FAQ

**Is it free?** Yes — OpsKat is open source under [GPLv3](./LICENSE).

**Which AI models does it support, and do I need an API key?** You bring your own key for any OpenAI- or Anthropic-compatible provider (OpenAI, Claude, DeepSeek, Gemini, Qwen, GLM, Kimi, and more). See [How the AI works](#how-the-ai-works).

**Does my data pass through your servers?** No. OpsKat connects directly to the model endpoint you configure and to your own servers — nothing is relayed through us, and credentials are encrypted locally.

**Can I use it without the AI?** Absolutely. It is a complete terminal and asset manager on its own.

**Does it work on an intranet or offline?** Asset connections are direct, so they work on private networks. For AI features, point it at an internal or self-hosted model endpoint.

---

## 🤝 Contributing

We welcome all forms of contribution! Read the [Contributing Guide](./CONTRIBUTING.md) for development setup, commit conventions, and the PR process, then check out the issues or submit a pull request.

---

## 📄 License

This project is open-sourced under the [GPLv3](./LICENSE) license.

## 🔗 Links

- [LINUX DO](https://linux.do/)
