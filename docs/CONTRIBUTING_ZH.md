<p align="right">
<a href="../CONTRIBUTING.md">English</a> | <a href="./CONTRIBUTING_ZH.md">中文</a>
</p>

# OpsKat 贡献指南

感谢你有兴趣为 OpsKat 做贡献！我们欢迎所有形式的贡献 —— 报告 Bug、提出功能想法、完善文档、提交代码。

本指南只讲贡献流程。详细的开发手册见 [docs/DEVELOP.md](./DEVELOP.md)，架构与子系统地图见 [docs/ARCHITECTURE.md](./ARCHITECTURE.md)。

## 贡献方式

- **报告 Bug** — 使用 Bug 模板提交 [Issue](https://github.com/opskat/opskat/issues/new/choose)，附上复现步骤和相关日志会很有帮助。
- **功能建议** — 提交 Feature Request；如果还只是初步想法，可以先到 [Discussions](https://github.com/opskat/opskat/discussions) 讨论。
- **安全漏洞** — 请**不要**提交公开 Issue，通过 [GitHub Security Advisories](https://github.com/opskat/opskat/security/advisories/new) 私下报告。
- **完善文档** — 错别字修正、表述澄清都欢迎。
- **提交代码** — 修 Bug 或实现功能。改动较大时，请先开 Issue 讨论方案，再动手投入时间。
- **扩展开发** — 扩展源码在独立仓库：[opskat/extensions](https://github.com/opskat/extensions)。

## 开发环境

前置依赖：[Go 1.26+](https://go.dev/)、[Node.js 22+](https://nodejs.org/) + [pnpm](https://pnpm.io/)、[Wails v2 CLI](https://wails.io/docs/gettingstarted/installation)。

```bash
make install        # 安装前端依赖
make dev            # 开发模式（热重载）
```

完整命令列表（构建、opsctl CLI、扩展 devserver、覆盖率等）见 [docs/DEVELOP.md → Common Commands](./DEVELOP.md#common-commands)。

> ⚠️ 部分文件是**生成物**（如 `frontend/wailsjs/`、`mock_*/`、lockfile），不要手工编辑。清单见 [docs/DEVELOP.md → Generated / auto-managed files](./DEVELOP.md#️-generated--auto-managed-files)。

## 测试与 Lint

提 PR 前先在本地跑通 —— CI 会执行同样的检查：

```bash
make test                                 # Go 测试
make lint                                 # Go lint
cd frontend && pnpm test && pnpm lint     # 前端测试 + lint
```

CI 还会额外运行 GUI 端到端套件（`make test-e2e`，Playwright 驱动真实 Wails 应用）。

几条需要了解的约定：

- **修 Bug 先写失败的测试。** 先用 `go test` / `vitest` 复现 Bug，再改实现，并且修根因而不是打补丁 —— 见 [AGENTS.md](../AGENTS.md) 的 Fix policy。
- **复用优先。** 新增组件 / hook / 工具函数前，先 grep 是否已有现成实现 —— 平行副本很快就会各自漂移。
- 前端代码风格由 Prettier 强制（120 列、2 空格缩进）。
- 验证流程（先跑什么、按什么顺序）：[docs/VERIFICATION.md](./VERIFICATION.md)。通过观察日志 / 数据库 / 无头 `opsctl` 验证功能，见 [docs/references/testing-debugging-guide.md](./references/testing-debugging-guide.md)（面向 AI 助手，英文）；GUI 端到端细节见 [docs/references/e2e-harness-guide.md](./references/e2e-harness-guide.md)。

## Commit 信息 — gitmoji

Subject 首字符必须是 **emoji 字符本身**（不是 `:sparkles:` 文本码，也不是 `feat:` / `fix:` 前缀）：

```
✨ 新增 Kafka 消费组延迟视图
🐛 修复 SFTP 上传进度不更新
```

常用 emoji：✨ 新功能 · 🐛 修复 · ♻️ 重构 · 🎨 UI · ⚡️ 性能 · 🔒 安全 · 🔧 配置 · ✅ 测试 · 📄 文档。完整表格和 issue 编号规则见 [docs/DEVELOP.md → Commit message](./DEVELOP.md#commit-message--gitmoji)。

Commit 信息中英文都可以 —— emoji 开头的规则与语言无关。

## Pull Request

1. 在 GitHub 上 **Fork** 本仓库（[opskat/opskat](https://github.com/opskat/opskat) 页面右上角的 "Fork" 按钮），然后 clone 你的 fork：

   ```bash
   git clone https://github.com/<你的用户名>/opskat.git
   cd opskat
   ```

2. **从 `main` 切出新分支**：

   ```bash
   git checkout -b my-feature main
   ```

3. 完成修改，用 gitmoji 格式提交 commit，并确保本地测试和 lint 通过。

4. **Push** 分支到你的 fork，然后在 GitHub 上向 `opskat/opskat` 的 `main` 分支**发起 Pull Request**：

   ```bash
   git push -u origin my-feature
   ```

5. 按 PR 模板填写 —— 标题带 gitmoji，UI 改动必须附截图。一个 PR 只做一件事；大型重构先开 Issue 讨论。

6. 等待 CI 通过（Go lint/测试、前端 lint/测试、GUI e2e），并跟进 review 意见。

## 开源许可

OpsKat 基于 [GPLv3](../LICENSE) 协议开源。提交贡献即表示你同意你的贡献以同样的协议发布。
