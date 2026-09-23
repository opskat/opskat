# opsctl 的 SSH MFA 支持

> Status: Approved
> Owner: OpsKat maintainers
> Last updated: 2026-09-22

**Objective:** opsctl 连接需要 keyboard-interactive 二次验证（OTP / MFA）的 SSH 资产时，能把验证走完再继续执行，而不是直接断开；走不完时明确告诉调用方（人或 AI）该用哪种方式完成验证（issue #322）。

**Hard invariant:** 不跳过、不伪造 MFA —— 没有答案来源时绝不自动填答案（已保存密码回答首轮密码提示的既有行为除外）。MFA 答案只用于当次握手，绝不写入日志、审计、数据库或任何持久状态。opsctl 与桌面端生命周期解耦（#318）不回退。SSH Agent 资产既有的认证顺序约束（精确公钥为唯一第一因子、keyboard-interactive 至多一次、只在公钥部分成功后提供）不放宽。桌面端终端 tab 的 MFA 交互行为不变。

## Problem

1. **`opsctl ssh` 对密码 / 私钥资产完全不接 MFA。** `DialSSHClientInteractive`（`internal/ai/helper/ssh_helper.go:511`）只给 Agent 资产接了 `terminalMFACaller`（`cmd/opsctl/command/ssh.go:140`），构造的 `ssh_svc.ConnectConfig` 不设 `OnAuthChallenge`。于是 `buildAuthMethods`（`internal/service/ssh_svc/ssh.go:854`）里的 keyboard-interactive 回调：私钥资产报 `keyboard-interactive 认证需要用户输入`（`:875`）；密码资产把已保存密码填进 OTP 提示（`:869`-`:873`），服务器必然拒绝。issue #77 / #109 修的「公钥 + MFA 链路」（`:908`）因此只在桌面端终端 tab 生效（`internal/app/ssh/ssh_ops.go:253`）。（已验证：代码阅读）

2. **非交互命令（`exec` / `cp` / `batch`）对所有认证类型都无法完成 MFA，且失败不可识别。** #318 之后 opsctl 的远程操作一律自行拨号（`cmd/opsctl/command/exec.go:156` 调 `helper.ExecWithStdio`，`internal/ai/helper/ssh_helper.go:556`；`cp` 经 `helper.EnsureSFTPClientCache`，`cmd/opsctl/command/cp.go:47`），这些拨号都走 `credential_resolver.DialAssetSSH`，不接任何挑战应答方：Agent 资产得到 `ssh_agent_mfa_required`，密码 / 私钥资产得到 Problem 1 里的错误。失败以普通 `Error:` + 退出码 1 呈现，外部 AI 无法把「需要人给验证码」与「命令写错了」区分开。（已验证：代码阅读，`origin/main` 956c932b）
3. **`opsctl batch` 的 SSH exec 条目各自拨号。** `executeBatchExec` 每条都调 `helper.ExecWithStdio` 新建连接（`cmd/opsctl/command/batch.go:368`），条目在并发协程里执行（同文件 `:275`）；`root.go:97` 的进程内连接池只服务协议隧道，不被 exec 条目使用。对需要 MFA 的资产，同一批里对同一资产的 N 条命令会同时发起 N 次挑战。（已验证：代码阅读）

## Actors and user stories

1. 作为**运维工程师**，我想在终端里 `opsctl ssh` / `opsctl exec` 一台需要 OTP 的堡垒机时，被提示输入验证码并继续连接，以便不用切到桌面端。
2. 作为**调用 opsctl 的外部 AI agent**（Claude Code / Codex 等），我想在遇到 MFA 时拿到一条机器可识别的信号，并能在拿到验证码后（人给的，或我自己有 TOTP 工具算的）通过参数交给 opsctl，以便把任务继续下去而不是误判为失败。
3. 作为**开着桌面端、让 AI 替我跑命令的人**，我想在 AI 触发的连接需要 MFA 时由桌面端弹窗让我输入，以便不必守在 AI 的终端前。

## Design decisions

| # | 决策 | 依据与被否方案 |
|---|---|---|
| 1 | 桌面弹窗 + 参数两条路都做（用户决定） | 桌面弹窗覆盖「人在桌面前、AI 在终端里跑」；参数覆盖无桌面 / 无 TTY 与 AI 自带 TOTP 工具。被否：只做桌面弹窗 —— 无桌面时无解；只做参数 —— 人在场时每条命令都要手动把码喂给 AI |
| 2 | 应答来源固定优先级：参数 → 终端提示（可交互）→ 桌面弹窗（`approval.sock` 可达）→ 明确失败（用户决定） | 与 opsctl 审批人选择同一顺序（`requireApproval`，`cmd/opsctl/command/approval.go:87`：可交互走终端、否则桌面、否则退出码 3），「可交互」的判定也相同（stdin 与 stderr 均为终端）。显式输入优先于交互，交互优先于失败。被否：桌面优先于终端 —— 与审批顺序相反，人在终端前时却要切窗口 |
| 3 | 参数只回答**单提示**的一轮挑战 | 一次性验证码天然对应一个提示；多提示或多轮时无法可靠对位，猜测会造成锁号。被否：按顺序重复 `--mfa-code` 对位多个提示 —— 调用方无法预知服务器的提示结构 |
| 4 | 无应答来源时以退出码 3 + `NEEDS MFA` 标记失败 | 复用 opsctl 现有「需要人介入」约定（退出码 3，`NEEDS AUTHORIZATION` / `NEEDS TTY`，`cmd/opsctl/command/root.go` 使用说明），AI 可据此停下向人要码。被否：新退出码 —— 调用方要多识别一种约定 |
| 5 | 已保存密码仍回答首轮 keyboard-interactive（沿用现状），之后的轮次交给应答来源 | 「keyboard-interactive 代替 password」的服务器依赖该行为（`internal/service/ssh_svc/ssh.go:882`），不能回退。被否：所有轮次都交给应答来源 —— 会让只需密码的服务器也开始要求 MFA 输入 |
| 6 | 桌面弹窗经 `approval.sock` 承接，不恢复连接池代理 | #318（`docs/specs/2026-09-15-opsctl-ssh-proxy-removal.md`）已决定 opsctl 远程操作一律自行拨号、与桌面端生命周期解耦；`approval.sock` 是仍然存在的 opsctl → 桌面通道，本来就承载「需要桌面端的人做决定」的请求。被否：恢复经桌面连接池拨号以复用已通过 MFA 的连接 —— 推翻 #318 的决定（用户决定不做） |
| 7 | 不做跨 opsctl 进程的 MFA 连接复用 | 每个 opsctl 进程各自拨号，#318 已接受其握手开销；同一进程内对同一资产的多条命令改为复用一条已验证的连接（Problem 3），`opsctl batch` 因此成为「一次验证、多条命令」的入口。代价：连续多条 `opsctl exec` 每条都要验证，且很多 TOTP 服务器拒绝在同一时间窗内重复使用同一个码 —— 文档引导改用 `opsctl batch`。被否：跨进程复用（见决策 6）|

## 应答来源与行为

**参数。** 全局参数 `--mfa-code <code>`，或环境变量 `OPSKAT_MFA_CODE`（参数优先）。前置：命令需要新建到 SSH 资产的连接且服务器发起 keyboard-interactive 挑战。结果：当某轮挑战恰好一个提示时，用该值作答并继续；每条新建连接中该值至多使用一次。若该轮有多个提示，或值已用过后又来一轮挑战，连接失败并提示改用桌面端或交互式终端完成验证，不填答案。提供了参数时不再询问终端或桌面端；该值被服务器拒绝即失败，不改走其它应答来源。使用说明建议优先用环境变量，因为参数会出现在 shell 历史与进程列表中。

**终端提示。** 前置：未提供参数，opsctl 可交互（stdin 与 stderr 均为终端，与审批的判定相同）。结果：在 stderr 呈现挑战（名称、说明、逐条提示），回显关闭的提示隐藏输入，读完答案后继续。`opsctl ssh` / `exec` / `cp` / `batch` 行为一致，对密码 / 私钥 / keyboard-interactive / Agent 资产行为一致。Ctrl-C 在拨号阶段中止等待。

**桌面弹窗。** 前置：未提供参数，opsctl 不可交互，且桌面端在运行（`approval.sock` 可达）。结果：opsctl 把挑战（资产名、服务器说明、逐条提示与回显标记）经 `approval.sock` 发给桌面端；桌面端弹出一个全局 MFA 对话框（不依附任何终端 tab），回显关闭的提示以掩码输入，窗口在后台时唤到前台，与现有 opsctl 审批对话框一致。提交后答案回到 opsctl，握手继续、命令照常执行。用户取消或关闭对话框时，opsctl 以 MFA 已取消的错误退出（非 0，非 3），不重试。opsctl 进程中途退出时，桌面端对应的对话框关闭。桌面端在请求过程中退出时，opsctl 按「明确失败」处理。

**明确失败。** 前置：需要 MFA，但没有参数、不可交互、桌面端也未运行。结果：立即失败（不挂起等待），退出码 3，stderr 首行是固定标记 `NEEDS MFA`，正文指引：用 `--mfa-code` / `OPSKAT_MFA_CODE` 重试，或打开桌面端，或在交互式终端里运行。服务器拒绝答案时报 MFA 验证失败（退出码 1），调用方不应自动用同一个码重试。

## 连接复用

不做跨 opsctl 进程的复用：每个 opsctl 进程各自拨号，每次新建连接都会重新触发 MFA。一个 opsctl 进程内，对同一资产至多完成一次 MFA：`batch` 中针对同一 SSH 资产的 exec 条目（包括并发执行的条目）共用同一条已验证的连接，各自开独立会话执行，输出、退出码与审计按条目独立，与现状一致；并发条目等待这一次验证完成，不各自发起挑战。验证失败或取消时，该资产的全部条目以同一错误失败，其它资产的条目不受影响。`cp` 在单次命令内对同一资产复用会话，同样只验证一次。

## 安全与审计

MFA 答案不出现在任何日志、审计记录、错误信息或持久化状态中；桌面对话框提交或取消后即清除答案。审计记录可以包含「本次连接的 MFA 由参数 / 桌面 / 终端完成」这类来源信息，但不含答案本身。桌面弹窗的挑战与答案只经本机已鉴权的 `approval.sock` 传递，答案不进入桌面端的审计或日志。

## 文档

`opsctl` 使用说明与 `plugin/opsctl/skills/opsctl/SKILL.md` 说明 `--mfa-code` / `OPSKAT_MFA_CODE`、`NEEDS MFA` 标记的含义，以及 AI 遇到它时应向人索取验证码（或调用自己的 TOTP 工具）后带参数重试；需要对同一 MFA 资产连续执行多条命令时改用 `opsctl batch`。

## Out of scope

- 跳板机（jump host）与代理链上的 MFA：握手不在本轮应答来源覆盖范围内，行为不变。
- 其它资产类型（数据库 / Redis 等）经 SSH 隧道时的 MFA。
- 桌面内置 AI 的 exec 工具遇到 MFA：本轮只处理 opsctl。
- 多轮 / 多提示挑战的参数作答（见决策 3），这类服务器请用桌面弹窗或交互式终端。
- 桌面端终端 tab 的 MFA 交互（已有，不变）。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 进程内测试 SSH 服务器（第一因子后要求 keyboard-interactive）+ opsctl 拨号入口 | 参数作答成功；多提示 / 二轮时参数拒答；终端应答方对密码 / 私钥资产生效；密码资产首轮仍用已保存密码；无应答来源时得到「需要 MFA」且不挂起；`batch` 同资产并发条目只触发一次挑战、验证失败时同资产条目全部失败 | `internal/sshagent/mfa_test.go`、`internal/service/ssh_svc/agent_factory_test.go` |
| `approval.sock` 客户端 / 服务端 | MFA 请求往返：桌面端回答案 / 取消；桌面端不可达时落到明确失败 | `internal/approval/ipc_test.go` |
| opsctl 命令层 | 应答来源优先级；无应答来源时退出码 3 + `NEEDS MFA` | `cmd/opsctl/command/approval_test.go`、`ssh_test.go`、`exec_test.go` |
| 前端 MFA 对话框（vitest） | 提示渲染、回显掩码、提交 / 取消回调 | `frontend/src/components/terminal/__tests__/ConnectionProgress.test.tsx` |

无法自动化的部分由运行时观察覆盖：`make dev-sandbox` 启动桌面端，配合一个要求 OTP 的测试 sshd，以非交互方式驱动 `opsctl exec`，观察桌面弹窗出现、提交后命令执行、取消时的退出码；以及桌面未运行时 `OPSKAT_MFA_CODE` 的成功路径与无码时的 `NEEDS MFA`。真实堡垒机（JumpServer 等）的提示结构未验证，若有可用环境再补一次真机观察。

## Open questions
