# opsctl 远程操作与桌面端解耦

> Status: Approved
> Owner: OpsKat maintainers
> Last updated: 2026-09-15

**Objective:** opsctl 的远程操作不再依赖桌面端进程的生命周期——关闭桌面端不会中断正在运行的 opsctl 命令。

**Hard invariant:** 策略管控与审批不得放松。`approval.sock` 通道、策略检查（`permission.CheckPermission`）、审批人选择与审计写入的可观察行为完全不变；`opsctl ext exec` 仍然要求桌面端在线且 fail closed。

## Problem

1. **桌面端退出会掐断在飞的 opsctl 远程操作。** `Server.Stop()` 主动 `conn.Close()` 所有客户端连接，其文档注释明确声明"不等待请求处理协程，以保证应用退出不会被远端命令、文件传输或异常客户端阻塞"（`internal/sshpool/server.go:89`）。opsctl 侧没有重连、没有回落直连、没有续传——一条跑到一半的长命令随 GUI 一起死。*(verified)*

2. **被掐断在 `exec` / `ssh` 上表现为静默成功。** `readOutputFrames` 读到 `io.EOF` 时返回 `(0, nil)`（`internal/sshpool/client.go:299`）。服务端正常结束时一定会先发 `FrameExitCode`（`internal/sshpool/server.go:354`），所以"未收到退出码就 EOF"必然是异常终止，返回 0 在任何情况下都是错的。后果：opsctl 不打印错误、退出码 0，审计记录写成 `{"status":"completed","exit_code":0}`（`cmd/opsctl/command/exec.go:164`）。脚本无法靠退出码察觉命令被截断。`Upload` / `Download` / `Copy` 不受影响，它们的 EOF 会返回 `read frame: EOF` 错误。*(verified)*

3. **退出确认框无法辨认将被中断的是什么。** `activeTasks` 对每个在飞的代理操作只产出字符串 `"operation"`（`internal/app/opsctl/opsctl.go:105`），既无资产名也无命令，用户无从判断该不该确认退出。*(verified)*

4. **代理路径的能力弱于直连。** 桌面连接池以非交互方式拨号：Agent 资产需要 MFA 时只能回传 `ssh_agent_mfa_required`，opsctl 为此专门维护了一段"交接回直连路径呈现挑战"的逻辑（`cmd/opsctl/command/ssh.go:80`）。代理层在这个场景下是纯粹的绕路。*(verified)*

## Actors and user stories

1. 作为在终端里运行 `opsctl exec <asset> -- ./deploy.sh` 的运维，我希望关闭 OpsKat 窗口不影响这条命令，这样 GUI 只是我的工具之一，而不是命令的隐式宿主。
2. 作为编写自动化脚本的使用者，我希望 opsctl 的任何失败都表现为非零退出码，这样脚本能靠退出码做判断，而不会把被截断的执行当成成功。
3. 作为准备关闭桌面端的使用者，我希望不必先去确认有没有 opsctl 命令在跑。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | opsctl 的全部远程操作（`exec` / `ssh` / `cp` / `batch`）一律自行拨号，不再经由桌面端 | 两个 dialer 逐字相同，都委托 `credential_resolver.Default().DialAssetSSH`（`internal/app/sshadapt/pool_dialer.go:19`、`internal/ai/helper/ssh_helper.go:585`），直连无能力损失，且在交互式 MFA 上严格更强（Problem 4）。连接复用的收益与命令时长成反比：长命令里一次握手可忽略，却要为此赌上整个 GUI 的生命周期。*(user-decided)* Rejected: 退出时排空（保留性能，但命令仍会被掐断，只是从静默变知情；且 `tail -f` 与交互式会话会让"等待完成"永不结束）；加 `--no-proxy` 开关（改动最小，但"默认值该是什么"这个问题原样保留，不知情的用户依然被掐断） |
| 2 | 整体删除 sshpool 的 IPC 代理层，而非留作死代码或预留开关 | 该层只有 opsctl 一个消费者（`Server`/`Client`/帧协议的引用全部来自 `cmd/opsctl` 与 `internal/app/opsctl`）。AGENTS.md「Defensive Code / No meaningless fallbacks」禁止保留无消费者的通路。附带效果：Problem 2 的缺陷函数随代码一并消失，无需单独修复 *(verified)* Rejected: 保留 `Server` 以备将来——未请求的能力，且留着就要继续维护它的鉴权与关停语义 |
| 3 | 不为批量场景做握手开销缓解 | 受影响的只剩「shell 脚本中 for 循环连续调用 `opsctl exec`」这一种写法；单进程多命令的场景已由 `opsctl batch` 覆盖，它在进程内自带连接池（`cmd/opsctl/command/root.go:97`）。*(user-decided)* Rejected: 引入常驻守护进程持有连接池——工程量远超本轮，且与「opskat 不从 agentre 移植设计」的既定约束冲突 |
| 4 | `sshpool.Pool` 及其拨号器保留不动 | 桌面端自身的 SSH 使用（扩展宿主、redis、k8s、rdp 等 binder）直接依赖进程内的 `Pool`，与 opsctl 的 IPC 通路无关 *(verified)* |
| 5 | `approval.sock` 及其上的全部语义保持不变 | 审批是独立通道与独立关切；本轮只解耦执行，不触碰管控 |
| 6 | 审计日志页的「连接池」面板保留，内容与文案均不变 | 该池的其余使用者是协议隧道（redis / database / mongodb / kafka / etcd / oss / rdp / query 经 `internal/connpool`）、k8s 跳板、扩展 tunnel dialer 与端口转发，解耦后面板依然有实质内容；桌面端自身的 SSH 终端会话本就不经此池，故该面板一直是"为隧道/跳板保持的复用连接"视图，移除 opsctl 的代理连接反而使其语义更纯 *(verified)* Rejected: 删除面板——会同时拿掉排查"跳板连接是否还在"的唯一入口 |

## 执行路径

`opsctl` 解析资产、完成策略检查与审批之后，直接使用自身进程内的连接完成远程操作。桌面端是否在线不再改变执行路径，因此也不再改变执行结果、输出或退出码。

具体地：当使用者运行任一远程操作命令（`exec`、`ssh`、`cp`、`batch`），无论桌面端处于运行、未启动还是执行期间被关闭，命令都运行至其自身的自然结束，并以远端进程的真实退出码退出。桌面端的启动与关闭对已在运行的 opsctl 进程不产生任何可观察影响。

审批仍在执行之前发生，选择审批人的判据不变：可交互（stdin 与 stderr 双 TTY）走终端提示；不可交互且 `approval.sock` 可达走桌面弹窗；否则是退出码 3 的结构化拒绝。审批被拒或不可达时，命令不执行。

### 失败行为

远程操作自身的失败——拨号失败、认证失败、远端命令非零退出、传输错误——沿用各命令当前的呈现与退出码，本轮不改变。

需要交互式 MFA 的 Agent 资产，由 `opsctl ssh` 在终端直接呈现服务器的结构化挑战；不再存在"先经代理失败、识别错误码、再交接回直连"的中间态。

## 桌面端退出行为

桌面端退出时不再需要考虑 opsctl 的远程操作：退出确认对话框不再列出 opsctl 操作类活动。审批类活动保持现状——当有一次 opsctl 审批正等待用户在桌面端上决定时，退出仍会被列出并需要确认；确认退出后该次审批按既有契约转为退出码 3 的结构化拒绝，opsctl 不执行。

## 兼容性

桌面端不再监听 `sshpool.sock`，也不再创建该 socket 文件。使用者 PATH 中若留有旧版本的 opsctl 二进制，其代理可用性探测会失败，从而走它自身已有的直连回落路径——表现为正常执行，不产生错误或警告。反向组合（新 opsctl 配旧桌面端）下新 opsctl 根本不做探测，同样正常。两个方向都无需迁移动作。

数据目录中可能残留上一次运行留下的 `sshpool.sock` 文件。它不再被任何一方读写；本轮不引入清理逻辑，也不引入针对它的判断。

## 安全

远程连接的凭据解析路径不变，仍是 `credential_resolver`。变化在于：解密后的 SSH 连接不再由桌面端进程代持、经本地 socket 转发给 opsctl，而是由 opsctl 进程自己持有。本地 socket 上的 token 鉴权随该通路一并消失，其所防护的攻击面（同机其它进程连上 `sshpool.sock` 借用已认证连接）随之消失。`approval.sock` 的 token 鉴权不变。

## 已知后果：桌面端对在飞 opsctl 操作失明

解耦之后，桌面端在一条 opsctl 命令的执行期间无法得知它正在运行。审批时刻仍然可见（弹窗），执行完成后仍然可见（审计行由 opsctl 直写共享数据库，`source=opsctl`，不经任何 socket，本轮完全不受影响）；唯独"正在执行"这一段没有任何桌面端入口——审计行在执行结束后才写入，退出确认清单也不再列出 opsctl 操作。

这是决策 1 的必然结果而非可选项：任何让桌面端知晓在飞操作的机制都要求一条从 opsctl 到桌面端的上报通路，与本轮目标直接冲突。若后续需要恢复这一可见性，应作为独立需求另行设计（形态不限于代理层，例如在共享数据库上写入执行中的审计行）。

## Out of scope

- 恢复桌面端对在飞 opsctl 操作的可见性（见上节）
- 审批通道（`approval.sock`）的任何行为变更
- `opsctl ext exec` 的委托机制——扩展的策略检查与审批由桌面端拥有，离线仍 fail closed
- 批量脚本握手开销的任何缓解措施（决策 3）
- 常驻守护进程形态的连接复用
- 残留 `sshpool.sock` 文件的清理

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `cmd/opsctl/command` 的 `exec` / `cp` / `ssh` / `batch` 入口 | 桌面端在线与否不改变所选执行路径与退出码 | `cmd/opsctl/command/cp_approval_test.go:211` 已有的执行路径 stub 机制 |

`internal/sshpool` 中仅服务于代理层的测试（帧编解码、文件帧流、代理 IPC、代理关停）随被测代码一并移除。该包现有的测试全部属于代理层，移除后包内不再有测试文件——`Pool` 本身此前就没有单元测试，这一既有缺口不在本轮范围内填补。专跑该包的 CI 步骤相应不再需要列出它。

退出确认清单不再包含 opsctl 操作类活动——这一项是纯删除，删除后被断言的字段已不存在，任何新增测试都会退化为同义反复，因此不加测试，由收尾的源码审查覆盖。

**不能自动化的部分，需按 docs/VERIFICATION.md 的流程执行并在收尾中报告：**

1. 「关闭桌面端不中断正在运行的命令」这一核心承诺——在隔离沙箱中对真实远端资产发起一条长时间运行的 `opsctl exec`，中途关闭桌面端，观察命令继续输出并以远端真实退出码结束。
2. 传输吞吐不回退——资产间 `cp` 的字节中转点由桌面端进程移至 opsctl 进程（物理路径不变，仍是 remote → 本机 → remote，但换了进程与代码路径）。提交 `6c5dad17` 只在代理路径上留下了基准（资产间 cp 32MB / RTT≈9ms 下 14.6 MB/s），直连侧无等价数字，必须实测对齐而非推定。两条路径共用 `internal/pkg/sftpio` 的并发流水线，预期相当。

## Open questions

无。
