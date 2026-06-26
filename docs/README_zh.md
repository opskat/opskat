<p align="right">
<a href="../README.md">English</a> | <a href="./README_zh.md">中文</a>
</p>

<h1 align="center">
<img src="../build/appicon.png" width="128" height="128"/><br/>
OpsKat
</h1>

<p align="center">
<b>一站式服务器运维工作台</b><br/>
SSH、数据库、Redis、Kafka、Kubernetes…… 运维要碰的一切，统一在一个跨平台桌面应用里。还能让 AI 用自然语言替你执行，每一步都有策略与审计护航。
</p>

<p align="center">
<a href="https://opskat.dev/">官网</a> ·
<a href="https://opskat.dev/docs/getting-started/installation">文档</a> ·
<a href="https://github.com/opskat/opskat/releases">下载</a>
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
  <a href="https://qm.qq.com/q/sERnNKEzeg"><img src="https://img.shields.io/badge/QQ_群-加入-12B7F5?style=for-the-badge&logo=qq&logoColor=white" alt="QQ 群"></a>
</p>

<p align="center">
  <img src="images/screenshot-main.png" alt="OpsKat 截图">
</p>

## 🧭 关于

管服务器平时要在一堆工具之间来回切：SSH 客户端、数据库 GUI、Redis 管理器、Kafka 控制台…… OpsKat 把这些常用的资产操作收进同一个界面，一个应用就够了——光是这样，它就已经是个完整的运维工作台。

在这之上再加一层 AI：直接用自然语言说需求，AI Agent 就替你连上去执行，查日志、跑 SQL、看集群状态都能交给它。每一步都有策略管控和完整审计日志兜底，放权给 AI 也安心。

**如果觉得有用，求个 Star ⭐ 这是对我们最大的支持！**

## ⬇️ 安装

### 下载

到 [Releases 页面](https://github.com/opskat/opskat/releases) 下载对应平台（**macOS / Windows / Linux**）的安装包，下载即用，无需 Go/Node 等开发环境。详细步骤见 [安装文档](https://opskat.dev/docs/getting-started/installation)。

### 首次使用

1. **添加资产** —— SSH 主机、数据库、Redis 等，也可以从 SSH config / Tabby 导入。
2. **连接** —— 打开终端、跑查询，或浏览 Key 与集合。
3. *（可选）* **配置 AI 服务商**，然后直接跟 AI 说你想做什么。

## 📦 支持的资产

| 分类 | 资产 |
| :-- | :-- |
| **服务器** | <img src="https://img.shields.io/badge/SSH-4D4D4D?style=flat-square&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgZmlsbD0ibm9uZSIgc3Ryb2tlPSJ3aGl0ZSIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiPjxyZWN0IHdpZHRoPSIyMCIgaGVpZ2h0PSIxNCIgeD0iMiIgeT0iMyIgcng9IjIiLz48bGluZSB4MT0iOCIgeDI9IjE2IiB5MT0iMjEiIHkyPSIyMSIvPjxsaW5lIHgxPSIxMiIgeDI9IjEyIiB5MT0iMTciIHkyPSIyMSIvPjwvc3ZnPg%3D%3D" alt="SSH"> <img src="https://img.shields.io/badge/Serial-5A6B7B?style=flat-square&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgZmlsbD0ibm9uZSIgc3Ryb2tlPSJ3aGl0ZSIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiPjxjaXJjbGUgY3g9IjEwIiBjeT0iNyIgcj0iMSIvPjxjaXJjbGUgY3g9IjQiIGN5PSIyMCIgcj0iMSIvPjxwYXRoIGQ9Ik00LjcgMTkuMyAxOSA1Ii8%2BPHBhdGggZD0ibTIxIDMtMyAxIDIgMloiLz48cGF0aCBkPSJNOS4yNiA3LjY4IDUgMTJsMiA1Ii8%2BPHBhdGggZD0ibTEwIDE0IDUgMiAzLjUtMy41Ii8%2BPHBhdGggZD0ibTE4IDEyIDEtMSAxIDEtMSAxWiIvPjwvc3ZnPg%3D%3D" alt="Serial"> |
| **数据库** | <img src="https://img.shields.io/badge/MySQL-4479A1?style=flat-square&logo=mysql&logoColor=white" alt="MySQL"> <img src="https://img.shields.io/badge/PostgreSQL-4169E1?style=flat-square&logo=postgresql&logoColor=white" alt="PostgreSQL"> <img src="https://img.shields.io/badge/SQL_Server-CC2927?style=flat-square" alt="SQL Server"> <img src="https://img.shields.io/badge/SQLite-003B57?style=flat-square&logo=sqlite&logoColor=white" alt="SQLite"> <img src="https://img.shields.io/badge/Redis-FF4438?style=flat-square&logo=redis&logoColor=white" alt="Redis"> <img src="https://img.shields.io/badge/MongoDB-47A248?style=flat-square&logo=mongodb&logoColor=white" alt="MongoDB"> <img src="https://img.shields.io/badge/etcd-419EDA?style=flat-square&logo=etcd&logoColor=white" alt="etcd"> |
| **中间件** | <img src="https://img.shields.io/badge/Apache_Kafka-231F20?style=flat-square&logo=apachekafka&logoColor=white" alt="Kafka"> <img src="https://img.shields.io/badge/Kubernetes-326CE5?style=flat-square&logo=kubernetes&logoColor=white" alt="Kubernetes"> |

_更多资产类型将通过插件模式持续扩展。_

## 🖥️ 完整的运维工作台

就算不开 AI，OpsKat 本身也是一个功能完整的终端和资产管理工具：

- 树形分组管理所有支持的资产类型
- 分屏终端，自定义主题
- SFTP 文件浏览器
- 跳板机链式连接
- 数据库查询编辑器（MySQL/PostgreSQL，支持 SSH 隧道）
- Redis 命令执行与 Key 浏览器
- MongoDB 集合浏览与查询执行
- Kafka 集群、Topic、消息、消费组、ACL、Schema Registry 和 Kafka Connect 管理
- 端口转发、SOCKS 代理
- 凭据加密存储
- 从 SSH config / Tabby 导入

## 🤖 让 AI 替你操作

配置好 AI 服务商，你就能用自然语言说需求，AI Agent 替你连上去执行：

- **"帮我看一下 web-01 上 nginx 最近的错误日志"** → AI 自动 SSH 上去执行命令并返回结果
- **"统计一下 db-prod 上 users 表各 status 的数量"** → AI 通过 SSH 隧道连数据库执行 SQL
- **"列出 kafka-prod 里有延迟的消费组"** → AI 在策略管控下读取 Kafka 元数据和消费组延迟
- **"检查一下 k3s 集群的健康状况"** → AI 自动跑 kubectl 相关命令，汇总节点和 Pod 状态

### AI 如何工作

- **自带 Key** —— 配置任意 **OpenAI / Anthropic 兼容** 的服务商，API Key 加密存储在本地。
- **几乎什么模型都能接** —— OpenAI、Anthropic（Claude）、DeepSeek、Gemini、通义千问、智谱 GLM、Kimi、MiniMax…… 也支持自建/本地端点（如 OpenAI 兼容的 Ollama）。
- **直连** —— OpsKat 直连你配置的模型服务，不经我们的服务器中转，也不绑定任何厂商。
- **你始终掌控** —— AI 只负责提出操作，真正的命令都从你本机发往你的服务器，并受下面的策略与审计管控。

## 🛡️ 安全与审计

给 AI 操作服务器的权限，怎么保证安全？

- **操作策略** — SSH/串口命令、SQL 语句、Redis、MongoDB、Kafka、Kubernetes 和 etcd 操作都支持白名单/黑名单，SQL 还会基于 Parser 自动拦截无 WHERE 的 DELETE/UPDATE 等危险操作
- **策略组** — 内置常用模板（Linux 只读、危险命令拒绝等），也可以自定义
- **预申请权限** — AI 或 opsctl 可以提前申请一批命令的执行权限，用户一次审批后，后续匹配的命令自动放行，不用每条都确认
- **审计日志** — 所有操作自动记录，谁在什么时候对哪台服务器执行了什么命令，决策来源全部可追溯

## 🎥 演示

https://github.com/user-attachments/assets/5816f1b1-ba90-4a7c-a5a7-cf4c5d7bbb89

https://github.com/user-attachments/assets/035fc0df-230c-456b-87bd-8a4a125feaec

## ⌨️ opsctl CLI + AI 编程工具集成

> 面向 CLI 用户和 AI 编程助手用户；只用桌面端的话可以跳过。

OpsKat 还提供了独立命令行工具 `opsctl`，主要给 **Claude Code**、**Codex**、**Gemini CLI** 这类 AI 编程助手用。桌面端一键安装 Skill，AI 编程助手就能通过 opsctl 直接管理服务器、查日志、查数据库、排查线上问题。

桌面端运行时，opsctl 会复用桌面端的连接池和审批流程，操作同样受策略管控和审计。

当然也可以自己手动用：

```bash
opsctl exec web-01 -- tail -n 100 /var/log/nginx/error.log
opsctl sql db-prod "SELECT status, COUNT(*) FROM users GROUP BY status"
opsctl ssh web-01
```

## 🛠️ 技术栈

| | |
|---------|------------|
| 桌面端 | [Wails v2](https://wails.io/) (Go + Web) |
| 前端 | React 19 + TypeScript + Tailwind CSS |
| 后端 | Go 1.26、SQLite |

## 🔧 从源码构建

> 面向贡献者；只想使用的话看上面的 **安装** 即可。

**前置依赖：** [Go 1.26+](https://go.dev/)、[Node.js 22+](https://nodejs.org/) + [pnpm](https://pnpm.io/)、[Wails v2 CLI](https://wails.io/docs/gettingstarted/installation)

```bash
make install        # 安装前端依赖
make dev            # 开发模式（热重载）
make build          # 生产构建
make build-embed    # 生产构建（内嵌 opsctl）
make build-cli      # 仅构建 opsctl CLI
```

## ❓ 常见问题

**免费吗？** 免费，基于 [GPLv3](../LICENSE) 开源。

**支持哪些模型？要自己的 API Key 吗？** 自带 Key，支持任意 OpenAI / Anthropic 兼容的服务商（OpenAI、Claude、DeepSeek、Gemini、通义千问、智谱 GLM、Kimi 等）。详见 [AI 如何工作](#ai-如何工作)。

**数据会经过你们的服务器吗？** 不会。OpsKat 直连你配置的模型服务和你自己的服务器，不经我们中转，凭据也是本地加密存储。

**不用 AI 行吗？** 完全可以，它本身就是个完整的终端和资产管理工具。

**内网 / 离线能用吗？** 资产连接是直连的，内网可用；AI 部分把模型指向内网或自建端点即可。

---

## 🤝 参与贡献

我们欢迎所有形式的贡献！请先阅读[贡献指南](./CONTRIBUTING_ZH.md)了解开发环境、提交规范和 PR 流程，然后查看 Issues 或提交 Pull Request。

---

## 📄 开源许可

本项目基于 [GPLv3](../LICENSE) 协议开源。

## 🔗 友情链接

- [LINUX DO](https://linux.do/)
