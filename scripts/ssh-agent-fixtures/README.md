# SSH Agent 真实跨平台 fixture

macOS/Linux 真实 Unix socket 与 Windows OpenSSH 兼容 named pipe 的原生 Agent 传输验证
（`internal/sshagent` 的真实传输端到端证明）。这是设计规格「跨平台真实 fixture」测试
决策行与发布流程调用的机器可读验证脚本。

## 布局

| 文件 | 作用 |
|---|---|
| `run.sh` | macOS/Linux：启动**系统 `ssh-agent`**、`ssh-add` 加载 ed25519 密钥、指向 `SSH_AUTH_SOCK`，运行 Go fixture 并输出机器可读结果 |
| `run-windows.sh` | Windows（**CI-only**）：在竞态检测器下运行 named-pipe fixture |
| `out/` | 每次运行的产物：`result.json`（机器可读）、`run.log`/`test.log`（日志）。已 gitignore，绝不含私钥/签名/挑战答案 |
| `work/` | 运行期的私钥与 socket，退出时删除 |
| `internal/sshagent/realfixture/` | Go 测试包：场景编排 + 可控 SSH 服务器 + 平台 fixture 服务 + 结果脱敏 |

## 运行

macOS / Linux（本机可运行）：

```bash
bash scripts/ssh-agent-fixtures/run.sh
```

脚本先 `command -v ssh-agent` 检查系统二进制：存在则用它做**真实原生 fixture**；不存在
时 Go 测试自行在真实 Unix socket 上提供 keyring agent（文档化回退，见
`internal/sshagent/realfixture/realfixture_test.go`）。退出码 = 全部场景通过且无泄密
(0)，否则非 0。

Windows（CI-only，本机勿跑）：`.github/workflows/ssh-agent-fixtures.yml` 的 windows job
调用 `run-windows.sh`，以 `go test -race` 运行 named-pipe fixture。

## 每个 fixture 验证什么

五条场景对应规格「跨平台真实 fixture」行，全部经真实 OS 传输（Unix socket 或
`\\.\pipe\` named pipe）驱动 `internal/sshagent`：

| 场景 | 期望 | 说明 |
|---|---|---|
| `native_success` | `ok` | 系统 agent 精确选一签名器、对可控 SSH 服务器完成握手；服务器只收到所选公钥且签名器真实使用（`Used=true`、`SignCount>0`） |
| `identity_missing` | `ssh_agent_identity_missing` | 已保存指纹不在 agent 身份中，签名前终止且不回退其它密钥 |
| `provider_rejects_signing` | `ssh_agent_sign_failed` | 提供方列出有效身份但拒绝签名（真实 socket/pipe 上的 Go fixture，系统 agent 无法强制此行为） |
| `cancel_while_waiting` | `ssh_agent_cancelled` | 等待 Agent 时取消 context，立即停止等待并释放传输（延迟 fixture） |
| `agent_mfa` | `ok` | 真实 agent 公钥部分成功 + 恰好一次 keyboard-interactive，交互调用方收结构化挑战并返回答案完成连接 |

可控 SSH 服务器复用 ssh_svc 测试的模式（本包自包含实现，`sshserver.go`），主机密钥用
真实 `ssh.FixedHostKey` 验证（无 `InsecureIgnoreHostKey` 回退）。

## 机器可读结果与泄密守卫

`out/result.json` 结构：`platform` / `socket_kind` / `agent_source` / `scenarios[]`
（`name`、`expected`、`got`、`pass`、`used`、`sign_count`、`detail`）/ `all_pass` /
`sanitized`。`detail` 只含指纹与提示名，不含端点、公钥 blob、签名或答案。

泄密守卫双层：
1. Go 脱敏器（`sanitize.go`，单测 `sanitize_test.go`）对报告 JSON + 运行日志扫描私钥
   PEM/内容、公钥 blob、MFA 答案；发现即 `sanitized=false` 并使测试失败。脱敏器自身
   永不回显泄露值。
2. 脚本对 `out/` 再做一次 grep 兜底（私钥头 + 挑战答案）。

## Windows / CI 门禁

- Windows named-pipe fixture 代码全部 `//go:build windows`，本机（macOS/Linux）不编译、
  不运行，只在 `windows-latest` CI job 执行。
- Windows 路径在**竞态检测器**（`go test -race`）下运行，覆盖 named-pipe 取消路径。
- Windows 的“真实 agent”是自包含 keyring 经 named pipe 提供（byte 模式、
  `\\.\pipe\` 命名空间与 OpenSSH 一致），不依赖 Windows OpenSSH agent 服务，因此 CI
  稳定可复现。
- CI 接线为 `workflow_dispatch` 手动手势（草图）；经 CI 验证稳定后可在该 workflow 内
  打开 `pull_request:` 触发。

## 直接跑 Go 测试

不通过脚本、自行提供 agent 时（仅 Unix）：

```bash
OPSKAT_REAL_AGENT_FIXTURE=1 \
SSH_AUTH_SOCK=/path/to/agent.sock \
OPSKAT_FIXTURE_PUBKEY=/path/to/key.pub \
OPSKAT_FIXTURE_PRIVKEY=/path/to/key \
OPSKAT_FIXTURE_OUT=/tmp/result.json \
OPSKAT_FIXTURE_LOG=/tmp/run.log \
go test ./internal/sshagent/realfixture/ -run TestRealUnixSocketFixtures -count=1 -v
```

不设 `OPSKAT_REAL_AGENT_FIXTURE=1` 时测试直接跳过，`go test ./...` 保持快速与 CI 安全。
