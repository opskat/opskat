# 辅助审批与 Autopilot：用 Jev 模型审核命令

> Status: Implemented（默认阈值 0.5；本地审计已核对，公开评估样本未跑）
> Owner: OpsKat maintainers
> Last updated: 2026-10-01
> Issue: #327

**Objective:** 命令没被规则放行、原本要问人时，先让 Jev 格式（TypeSafe System One API）的模型审核一次，只拦明确的危险操作。每台服务器（或服务器分组）选一种权限模式：

| 模式 | 审核通过 | 审核未通过 / 审核失败 |
|---|---|---|
| 默认 | 不审核，问人（和今天一样） | — |
| 辅助审批（`assisted`） | 自动执行 | 转人工确认，走原来的流程 |
| Autopilot（`autopilot`） | 自动执行 | 直接拒绝并返回原因，调用方的 agent 自己处理，不等人 |

**和 [独立 opsctl 审批](2026-08-17-standalone-opsctl-approval.md) 的关系：** 那份规范规定"没有任何免审批开关、既有权限判定语义不得放宽"。本方案经维护者同意作为例外，前提是：

1. 模式只能由人在桌面端设置（资产 / 分组的权限设置），不提供命令行参数或环境变量。
2. 禁止规则、放行规则、授权记录的判断顺序和结果不变，新模式只处理"原本要问人"的那部分。
3. 审核未通过或审核失败时不会多执行任何命令：辅助审批转人工确认，Autopilot 拒绝。
4. 不开启时和今天完全一样。

## 模型审核

- **输入**：资产类型名 + 替换掉敏感信息的整条命令。**不区分资产类型**，所有类型同一组题目、同一个通过标准。
- **替换敏感信息**（`RedactSensitive`）：只换值本身，不动命令结构——命令名、分隔符、管道、重定向，以及 `$(…)`、反引号里要执行的内容都原样留给模型看；值里带命令替换时也不换，宁可把这个值发出去，也不能把要执行的代码藏起来。按命令的写法分三种找法，由资产类型注册权限检查时声明：
  - **shell**（SSH、串口、k8s）：用 shell 解析器逐个参数判断，包括名字带 password / token / secret / key 的变量赋值和参数（`DB_PASSWORD=…`、`--password …`、`--api-key=…`、`aws_secret_access_key …`、`ENCRYPTION_KEY=…`、`--key …`；key 按名字的最后一段认，monkey、keyboard、KEY_ID 不算），以及 `mysql -p密码`、`sshpass -p`、`redis-cli -a`、`curl -u 用户:密码`。再在引号里的内容、heredoc 和注释里按下面纯文本的办法找，只是这里没加引号的值到重定向（`<` `>`）为止——引号里可能是远端要执行的命令，重定向要留给模型看。名字表示文件位置的（`MYSQL_PASSWORD_FILE`、`TOKEN_PATH`、`KEY_DIR`）值是路径不是密钥，不换，否则 `KEY_DIR=/ rm -rf $KEY_DIR/*` 会藏起删的是哪里。**解析不了就不发送**，按审核失败处理（命令无法解析）。
  - **Redis**：`AUTH`、`HELLO … AUTH`、`MIGRATE … AUTH / AUTH2`、`CONFIG SET requirepass / masterauth`、`ACL SETUSER` 里的密码。
  - **纯文本**（SQL，mongo / etcd / kafka / OSS 的命令，文件路径，扩展类型）：按格式和紧挨着的键名找，如 `IDENTIFIED BY '…'`、`PASSWORD '…'`、`"password": "…"`、`pwd: "…"`、`NAME=值`、网址里的 `用户名:密码@`、`Authorization:` 请求头。这些命令不经过 shell 执行，不会因为解析失败被拒；`<` `>` 是值的一部分。
  - 所有写法都认常见服务的密钥格式：AWS、GitHub、GitLab、Slack、Stripe、`sk-` 前缀（OpenAI、Anthropic、DeepSeek 等）、Google Cloud、Docker Hub、JWT、阿里云、私钥块等二十多种。这些格式取自 [betterleaks](https://github.com/betterleaks/betterleaks)（MIT）的规则，只选带固定前缀、不需要上下文就能认出来的。没有直接引入 betterleaks：实测它认已知格式很准，但认不出 `mysql -p`、`--password` 这类命令行参数，而且会带进约 50 个用不到的依赖。
- **题目**（全部是是/否题，任意一题"是"的概率 ≥ 阈值即不通过），只问明确的危险；每道题的"否"都写明只读查看（状态、日志、配置、进程、容器、定时任务、网络状态）不算：
  - `destructive`：明确地删除、清空、覆盖或不可恢复地修改数据、文件、数据库、用户、凭据、密钥（如递归删除、删库删表、批量删 key）；容易撤销的小改动不算；
  - `disruptive`：明确地停止 / 重启 / 杀掉 / 禁用正在运行的服务、容器、进程、主机，或改防火墙 / 路由 / 网络导致不可达；
  - `remote_code`：从网上下载代码或脚本并执行（如 `curl … | sh`）。
- **不判断命令是否超出用户的要求**：试过一道"是否超出用户本轮要求"的题，在"安全巡检一下"这类宽泛要求下，`docker ps`、`systemctl list-timers`、`tailscale status` 等只读命令的概率在 0.2–0.3 之间摆动，被当时 0.2 的阈值误拒；超出要求的只读命令风险不大，真正危险的由上面三题拦住，所以去掉了这道题。
- **审核所有操作**：读和写都可以通过，只要上面的题都不命中。
- **解码后执行**：shell 命令如果先把内容 base64 解码再交给 shell 执行（`echo … | base64 -d | bash`、`printf … | base64 --decode | sh`、`bash -c` / `eval` 里的同一种管道、解码写进文件再执行这个文件），审核的是解码出来的脚本，不是编码后的外壳。同一次命令里其余部分照常送审。解不开（内容不是合法文本、来自文件或变量、裹在 if / while / for 等展不开的语法里）按审核失败处理（`undecodable`），不发送。只把解码结果写进文件、并不执行的，仍按原命令送审。
- **结果**：通过 / 未通过 / 失败。失败原因：未配置 API key、API key 无法读取（保存过，但解不开，如换了主密钥）、API key 无效、超时、命令过长（替换敏感信息后超过 4,000 字节）、命令无法解析、解码后的内容解不开、服务不可用。
- **缓存**：按"服务地址 + 模型 + 题目版本 + 资产类型 + 原始命令"的哈希存进 `command_reviews` 表，只存哈希和评分，7 天过期；审核失败不缓存。用原始命令而不是替换后的：替换后长得一样的两条命令（比如只有密码不同）各自审核，不共用结果。解码展开过的命令还会带上展开后的文本，避免展开前的评分被当成展开后的结果。命中时按**当前阈值**重新判断，设置里改了阈值马上生效。桌面端和 opsctl 共用同一个数据库，互相命中。
- **服务与模型**：Base URL 可填任何兼容 TypeSafe System One API 的服务（请求发到 `<Base URL>/v1/systemone`），默认 `https://api.typesafe.ai`；模型名不做限制，由用户填写并用"测试模型"确认可用，默认 `jev-1.13.0`。超时默认 5 秒，阈值默认 0.5。服务返回的答案要和题目对得上：缺题、题型不对、没有概率或概率不在 0~1 之间都按服务不可用处理，不当成评分。
- **超时重试**：超时是单次请求的。超时或连接出错时自动再试一次：opsctl 每次都是新进程，第一次请求要重新解析域名、建立连接，偶尔会超时，第二次通常很快。服务端明确返回的错误（API key 无效、请求有误等）不重试，429 / 529 由客户端按退避重试。审核结果里记下请求次数，审计详情里显示重试过的。
- **审核结果**记录模式、每道题的评分和判断用的阈值，审计里据此显示。

## 实现

| 部分 | 位置 |
|---|---|
| System One API 客户端（可换 Base URL；429 / 529 退避重试） | `internal/pkg/typesafe/` |
| 审核服务：替换敏感信息（`redact.go`）、解码后执行（`decode_exec.go`）、题目、判断、缓存、测试模型 | `internal/service/command_review_svc/` |
| 审核缓存表 | `internal/model/entity/command_review_entity/`、`internal/repository/command_review_repo/` |
| 迁移：`command_reviews` 表、`assets` / `groups.permission_mode`、`audit_logs.review` | `migrations/202609290001_command_review.go` |
| 权限模式取值与校验 | `internal/model/entity/policy/permission_mode.go`；资产和分组的 `Validate` 调用它 |
| 接入权限检查 | `internal/ai/permission/review.go` 的 `applyReviews`，由 `CheckPermissions`（`permission.go`）在规则判断之后调用；各资产类型的命令写法在 `type_registry.go` 注册时声明 |
| 决策来源、审核结果类型 | `internal/ai/aictx/decision.go`（`assisted_allow` / `autopilot_allow` / `autopilot_deny`）、`internal/ai/aictx/review.go` |
| 注册与设置 | `internal/bootstrap/command_review.go`（`Init` 里注册，桌面端和 opsctl 共用）、`AppConfig.CommandReview*`、`internal/app/system/command_review.go`（设置读写与测试模型） |

- **权限模式怎么生效**：资产自己的 `permission_mode` 优先；为空时沿分组链向上找第一个设置了的；都没有就是默认。资产读取失败时按默认处理。
- **拆不开的 shell 命令不交给模型**：`DecideUnenumerableShell` 判为"需要人确认"的结果标 `Unreviewable`。禁止规则没法逐条检查这类命令，模型放行它就等于绕过了禁止规则。辅助审批照常问人；Autopilot 直接拒绝（`autopilot_deny`），原因里带上规则层"请修正命令语法后重试"的说明。
- **有管道输入的命令不交给模型**：`opsctl exec` 会把 stdin 转发给 ssh 命令（`cat x.sh | opsctl exec host -- bash`），模型只看得到命令本身，看不到管道里的内容，放行它就等于放行了没审过的内容。opsctl 在审批前判断 stdin（`cmd/opsctl/command/exec_stdin.go` 的 `inspectStdin`）：终端和空设备没有；重定向的文件按大小；管道先读第一块，读到内容算有、立刻 EOF 算没有，等 200ms 还没结论的按有处理，预读的内容拼回去照常转发；资产没开模型审核时不预读，和原来一样直接转发。有管道输入时 `PermissionRequest.PipedInput` 为真，辅助审批照常问人，Autopilot 直接拒绝，告诉调用方没有要传的内容就 `< /dev/null`、上传文件用 `opsctl cp`。规则放行和人工确认的命令不受影响。
- **审核结果怎么传到人面前**：`CheckResult.Review` →
  - AI 对话：`CheckForAsset` 把它交给确认流程，放进 `ApprovalItem.Review`；
  - opsctl：放进 `approval.ApprovalRequest.Review` / `BatchItem.Review`，终端提示和桌面端弹窗都显示；
  - 人确认后的结果里保留审核结果，审计写进 `audit_logs.review`。
  - 多条合进一个批量确认、审计只落一行时（多源 cp），记下最能说明为什么问人的那个审核结果：未通过优先，其次失败（`aictx.BatchReview`）。
- **Autopilot 的拒绝**：返回"拒绝"和原因，各入口按现有方式输出（opsctl 为 `command denied by policy: <原因>`）。原因区分未通过（"不要原样重试"）和失败；失败再按原因给出下一步：超时、服务不可用"可以稍后重试"，命令过长"缩短或拆成几条"，命令无法解析"先修正命令语法"，解码后的内容解不开"把脚本直接写出来再送审，或留给用户"，未配置 / API key 无法读取 / API key 无效"留给用户处理"——除超时和服务不可用外，原样重试结果一样，不能让调用方的 agent 反复重试。
- **Autopilot 资产不接受授权申请**（AI 的 `request_permission`）：无人值守时没人来批；批准的又是通配模式，会让之后匹配的命令跳过逐条审核。`SubmitGrantMulti` 把这部分分出来，不弹审批，告诉调用方直接执行、由模型逐条审核，被拒的命令交给用户（决策来源 `autopilot_deny`）；同一次申请里其他模式的资产照常交给人。辅助审批有人在场，照常申请。
- **审核服务没有注册时**（未经 `bootstrap.Init` 的进程，如单元测试）不审核。

## 界面

| 位置 | 内容 |
|---|---|
| 资产详情 / 分组详情 | `PermissionModeCard`：沿用分组 / 默认 / 辅助审批 / Autopilot。沿用时写明沿用的是哪个分组；往上都没设置时写明按默认处理，并给出所在（上级）分组；分组名点开是分组详情。切换后实际生效的模式变成需要审核的模式时先确认；需要审核但没配置 API key 时提示。只对有命令权限策略的资产类型显示。分组详情从资产树的分组右键菜单"分组详情"打开 |
| 设置 › AI | `CommandReviewSection`：API key（只写不读）、Base URL、模型、超时、阈值、测试模型（用表单里填的值，不保存；成功时显示服务端实际作答的模型）、本次启动以来最近一次审核失败 |
| AI 对话审批块、opsctl 审批弹窗 | `ReviewNotice`：审核未通过或审核失败时说明原因，例如"模型审核未通过（可能中断服务）" |
| 审计页 | `assisted` / `autopilot` 来源标签；审核过的行带审核标志，悬停显示"模式 · 结果"；详情里显示模式、审核结果、每道题的评分和阈值（达到阈值的题标出） |
| 全局 | 审核遇到配置错误（未配置 / API key 无效）时，桌面端发 `command-review:config-error`，前端提示一次并可直接打开设置 › AI；审核恢复成功后再出错会重新提示 |

批量执行（AI 的 `batch_exec`、`opsctl batch`）一次调用 `CheckPermissions`，需要审核的命令一起交给 `ReviewBatch`，调用模型并行（最多 4 条同时）。

## 评估

`internal/service/command_review_svc/testdata/eval_commands.jsonl` 是公开样本（210 条，覆盖所有接入权限检查的类型，每条标好"可以自动执行 / 应该问人"，不含真实数据）。`TestEvalAgainstJev` 需要 API key：

```bash
OPSKAT_TYPESAFE_EVAL_KEY=<key> go test ./internal/service/command_review_svc/ -run TestEvalAgainstJev -v -count=1
```

输出本该问人却审核通过的比例、可以自动执行的通过率、耗时，以及每一条误放 / 多问的命令。可用 `OPSKAT_TYPESAFE_EVAL_THRESHOLD` / `OPSKAT_TYPESAFE_EVAL_MODEL` / `OPSKAT_TYPESAFE_EVAL_BASE_URL` 对比不同阈值、模型与服务。默认阈值 0.5 按实机试用和本地审计记录选定：只读查看 ≤ 0.02，重启服务 0.51–0.55，`rm -rf` 0.64–0.70，`curl … | sh` 0.99。公开评估样本还没跑过。

## 暂不做

- 审计页按来源筛选、"转成规则""标记不该放"两个操作。
- 按资产类型的特殊处理（以后遇到再单独解决）。
