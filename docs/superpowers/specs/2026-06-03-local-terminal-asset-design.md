# 本地终端资产 (local) 设计

- Issue: [#70 [Feature] 终端增加本地 powershell 的支持](https://github.com/opskat/opskat/issues/70)
- 日期: 2026-06-03
- 范围: **只做"本地终端资产",不接入 AI**;一开始就支持 Windows。

## 1. 背景与目标

issue #70 混了两件诉求:① 原标题「让 AI 执行本机 powershell/命令」;② 评论区想要的「本地终端 Tab(把 Windows Terminal 集成进来,All-in-One)」。

本次**只实现 ②,且不接 AI**:新增一个 `local` 资产类型,让用户在 OpsKat 里像连 SSH 一样打开一个本机 shell(PowerShell / cmd / WSL / bash / zsh)的终端 Tab。每个 `local` 资产是一套可保存的 shell profile,可进资产树/分组、配图标。

**非目标(本次不做):**
- AI 在本机执行命令(原 issue 标题诉求)。`local` 资产不参与 `internal/ai` 的工具分发与命令策略。留好 seam,将来要做时只需补一个 policy + tool 路由。
- 通过 opsctl / sshpool 代理执行。本地 shell 直接在桌面 App 进程内 spawn,不需要跨进程代理。

## 2. 总体思路

完全基于 opskat 自身的既有模式,不依赖任何外部仓库:

- **资产接线**镜像现有 `serial` 资产类型。`serial` 是「无 host / 无端口 / 无密码」的本地设备资产,形态与「本地 shell」一致,是最佳模板(handler → service Manager/Session → app binder → 前端 transport)。
- **跨平台 PTY** 照 `serial_svc` 自己的约定落地:`serial_svc` 把平台相关的硬件流控拆成 `hwflow_linux.go` / `hwflow_bsd.go` / `hwflow_windows.go` 三个 build-tag 文件,放在 service 包内部。本地 PTY 同样在 `localterm_svc` 包内用 build-tag 文件区分平台,**不另起独立包、不抄外部结构**。

> 设计原则对齐 AGENTS.md「高内聚低耦合 / 按注册扩展,不在 shared code 上 branch on type string」。新资产类型通过 `assettype.Register()` 注册;前端 transport 由一张映射表驱动,而非散落的 `if transport === "..."`。

## 3. 架构与文件清单

### 3.1 跨平台 PTY 层(opskat 原生,放在 `localterm_svc` 内)

照 `serial_svc/hwflow_*.go` 的约定,PTY 平台代码作为 build-tag 文件直接放进 service 包,对外只暴露一个**包内**最小接口,Manager/Session 不感知平台:

| 文件 | 内容 |
|------|------|
| `internal/service/localterm_svc/pty.go` | 包内接口 `ptyProcess { Read(p []byte)(int,error); Write(p []byte)(int,error); Resize(cols,rows int) error; Close() error }`;函数声明 `startPTY(spec ptySpec) (ptyProcess, error)`;`ptySpec{Shell string; Args []string; Cwd string; Cols,Rows int}`。进程退出由 reader 读到 EOF 感知(creack/pty 与 conpty 一致);僵尸回收放在平台实现的 `Close()` 内(`cmd.Wait()` 带超时,unix)。公共 helper 负责 `~` 工作目录展开与 Windows argv quoting。环境变量由平台实现内部用 `os.Environ()` + `TERM=xterm-256color` 兜底,**不放进 config/spec**(YAGNI)。 |
| `internal/service/localterm_svc/pty_unix.go` | `//go:build !windows`(覆盖 darwin/linux/bsd)。用 `creack/pty`:`exec.Command(shell, args...)` + `pty.StartWithSize`;`Resize` → `pty.Setsize`;`Close` → 发 SIGHUP 后带超时回收;默认 shell `$SHELL` → `/bin/sh`。 |
| `internal/service/localterm_svc/pty_windows.go` | `//go:build windows`。用 `UserExistsError/conpty`:`conpty.Start(commandLine)`;`Resize` → `cpty.Resize`;默认 shell `pwsh.exe` → `powershell.exe` → `%COMSPEC%` → `cmd.exe`。Windows 命令行虽然是单字符串,但启动前按 `CommandLineToArgvW` 兼容规则 quote shell 路径与每个 arg,保留 `Args []string` 语义。ConPTY 需 Win10 1809+,起不来时返回明确错误。 |
| `internal/service/localterm_svc/shells.go` | 公共:`type ShellInfo struct { Name string; Path string; Args []string }`(`Args` 让 WSL/Git Bash 这类"shell+固定参数"的预设可一键选)。 |
| `internal/service/localterm_svc/shells_unix.go` | `//go:build !windows`。`DetectShells()`:`$SHELL`(默认优先)+ 读 `/etc/shells`(系统权威清单),去重 + `os.Stat` 确认存在。 |
| `internal/service/localterm_svc/shells_windows.go` | `//go:build windows`。`DetectShells()`:`LookPath` pwsh/powershell/cmd;探 Git Bash(`C:\Program Files\Git\bin\bash.exe`,带 `--login -i`);`wsl.exe -l -q` 枚举已装发行版,每个一项 `{Name:"WSL: <distro>", Path:wsl.exe, Args:["-d",distro]}`。wsl 输出是 UTF-16LE,v1 用"去 NUL + 去 CR + 按行切"解析(ASCII 名字够用;非 ASCII 发行版名后续再上正规 UTF-16 解码)。 |

`startPTY` 是包内函数 → service 单测可在 Unix 下真起 `/bin/sh` 跑集成用例,Manager 逻辑则通过注入 fake `ptyProcess` 做纯单测(见 §7)。

依赖新增(`go.mod`,均为标准公共库):`github.com/creack/pty`、`github.com/UserExistsError/conpty`。
> 备选:`github.com/aymanbagabas/go-pty` 单库统一两端,省掉两个 build-tag 文件;但与 `serial_svc` 直接包平台库的既有风格不一致,故默认走上面两库 + 平台文件。

### 3.2 资产接线(镜像 serial)

| 文件 | 镜像对象 | 说明 |
|------|----------|------|
| `internal/model/entity/asset_entity/asset.go`(改) | 现有 serial 部分 | 加常量 `AssetTypeLocal = "local"`;`LocalConfig{Shell string; Args []string; Cwd string}`(含 `GetCredentialID()=0`/`GetPassword()=""`);`Asset.IsLocal()`、`GetLocalConfig()`、`SetLocalConfig()`、`validateLocal()`;并在两个类型 switch 里加 `case AssetTypeLocal`(`Validate` 分发 ~L657 → `validateLocal`、`CanConnect` ~L896 → 本地恒可连,`return a.Status == StatusActive` 内 `case AssetTypeLocal: return true`) |
| `internal/assettype/local.go`(新) | `assettype/serial.go` | `localHandler` 实现 `AssetTypeHandler`,`init()` 里 `Register(&localHandler{})`。`Type()="local"`、`DefaultPort()=0`、`ResolvePassword` 返回空、`DefaultPolicy()` 返回 `DefaultCommandPolicy()`(仅满足接口,本次不参与拦截)、`SafeView` 暴露 shell/cwd/args、`ValidateCreateArgs`/`ApplyCreate/UpdateArgs` 读写 `LocalConfig`。Shell 允许为空(空 = `pty.go` 按 OS 兜底)。 |
| `internal/service/localterm_svc/localterm.go`(新) | `internal/service/serial_svc/serial.go` | `Manager`(`Connect`/`SetCallbacks`/`readOutput`/`GetSession`/`Disconnect`/`CloseAll`,含回调宽限期 + 10ms 合批刷新)与 `Session`(包 `ptyProcess`)。`Connect(cfg)` 入参含初始 `Cols/Rows`。 |
| `internal/app/local/local.go`、`internal/app/local/local_ops.go`(新) | `internal/app/serial/` | binder。 |

### 3.3 改动 —— 后端 wiring

`main.go`(对齐 serial 的三处):
- `localMgr := localterm_svc.NewManager()`(挨着 `serialMgr` L103)
- `localB := local.New(appCtx, sys, localMgr)`(挨着 `serialB` L125)
- 加入 `binders := []Lifecycle{..., serialB, localB, ...}`(L138)
- **不** 做 `aiB.SetLocalManager(...)`(本次不接 AI;serial 有 `aiB.SetSerialManager`,local 没有)

### 3.4 改动 —— 前端

前端有两套独立机制:

1. **资产类型注册表 `frontend/src/lib/assetTypes/`(已经很干净,registry 模式)** —— 每种类型一个文件 `registerAssetType({...})`(如 `serial.ts`),`options.ts` 存类型元数据(label/icon/group),`index.ts` 汇总 import。新增 local 只需 **照抄一份**:`local.ts`(`connectAction:"terminal"`、`canConnect:true`、icon、`DetailInfoCard`)+ `options.ts` 加一项 + `index.ts` 加 import + `LocalDetailInfoCard.tsx` + `LocalConfigSection.tsx` + `AssetForm.tsx` 接线 + i18n。**这部分不是重构,是镜像新增。**

2. **终端 transport 分支(`isSerial`,需要重构)** —— 当前用 **二元** `isSerial = transport === "serial"` 分散在 `terminalStore.ts`(L26/30/35/43/110/249/446–567/594–694/892)与 `Terminal.tsx`(L64/65/82/121/149/252)。直接加第三种 transport 会让分支爆炸,违反 AGENTS.md。**作为本次 in-scope 重构**(扩展这条 seam 本身),把二元分支收敛成一张按 transport 取能力的映射表:

```ts
// 单一事实源,ssh/serial/local 各一行
type TerminalTransport = "ssh" | "serial" | "local";
const TRANSPORTS: Record<TerminalTransport, {
  connectAsync: (assetId: number, cols: number, rows: number) => Promise<string>;
  write: (sessionId: string, dataB64: string) => Promise<void>;
  resize: (sessionId: string, cols: number, rows: number) => Promise<void>;
  disconnect: (sessionId: string) => void;
  eventPrefix: string;   // 事件前缀: ssh / serial / local
  canSplit: boolean;     // ssh=true;serial/local=false
}>;
```

> **分屏(split)v1 不做 local。** SSH 的 split 复用同一个 `ssh.Client`(`SplitSSH` 在已建连接上开新 channel);本地 shell 没有可复用的"连接",每个 pane 是独立进程,需要另一条 spawn 路径。v1 让 local 与 serial 一样 `canSplit=false`,split 留作后续增强。

- `terminalRegistry.ts` L71-73 改为查表(`eventPrefix`/`write`)。
- `terminalStore.ts`、`Terminal.tsx` 里所有 `isSerial ? A : B` 改为查 `TRANSPORTS[transport]`。能力差异(如 serial 不可分屏)由表里的 `canSplit` 表达,而不是 `=== "serial"`。
- 新增 wails 绑定调用:`ConnectLocalAsync`、`WriteLocal`、`ResizeLocalTerminal`、`DisconnectLocal`、`ListLocalShells`(`wailsjs/go/local/Local.*` 由 wails 自动生成)。
- 资产**新建/编辑表单**:加 `local` 类型项 + 图标;配置项 Shell(下拉预设 + 可手填)、Args、Cwd。Shell 预设由 `ListLocalShells()` 探测本机(镜像 `ListSerialPorts()`):下拉项带 `{name, path, args}`,选中时同时回填 `shell=path` 与 `args`(WSL 发行版选中即填 `wsl.exe` + `-d <distro>`)。
- Args 输入框用轻量 tokenizer 转成 `string[]`:空白分隔,支持单/双引号和反斜杠转义;展示/编辑时再格式化回可 round-trip 的文本。因此带空格的 WSL 发行版名与路径参数会作为单个 argv 保存和启动。
- `reconnectBySession` 走映射表 → local 的"重连" = 重新 spawn 一个 shell。

## 4. 与 serial 模板的关键差异(即"做得好"的要点)

1. **Resize 真生效**。serial 的 `Session.Resize` 是 no-op;local 调 `ptyProcess.Resize(cols,rows)`(底层 `pty.Setsize` / `conpty.Resize`)。因此 `Connect` 需接收初始 `Cols/Rows`(默认 80×24,前端首次 fit 后再 `ResizeLocalTerminal`)。
2. **进程退出 = 会话关闭**。serial 靠读错误判断断开;local 同理 —— shell 退出(用户 `exit`、崩溃)时 PTY master 读到 EOF,readOutput 退出 → `Session.Close()` → emit `local:closed:<sid>`。前端现有的"按 Enter 重连"语义对 local 即重新 spawn。
3. **Shell 可选可配 + 系统探测**。`LocalConfig.Shell` 空 → `pty.go` 按 OS 兜底;非空 → 用指定 shell。这样可保存「WSL Ubuntu」「PowerShell」「cmd」多套 profile。`ListLocalShells()`(后端 `localterm_svc.DetectShells()`,平台 build-tag 实现)彻底探测:Unix `/etc/shells`+`$SHELL`;Windows pwsh/powershell/cmd/Git Bash + `wsl -l -q` 枚举发行版,供下拉选择。
4. **日志**(AGENTS.md「关键流程要打日志」)。PTY spawn / exit 是长生命周期跨进程操作,打 开始 / 结束 / 失败 三态,带 `assetID`、`sessionID`、`shell` 强类型字段。有 ctx 用 `logger.Ctx(ctx)`;纯 goroutine(reader)无 ctx 降级 `logger.Default()`。`recover()` 边界用 `zap.Stack("stack")`。不打印进程环境变量(可能含敏感值)。

## 5. 绑定 & 事件命名(对齐 serial 风格)

绑定(`internal/app/local`):
- `ConnectLocalAsync(req{assetId int64, cols int, rows int}) (connectionId string, err error)`
- `WriteLocal(sessionId, dataB64 string) error`
- `ResizeLocalTerminal(sessionId string, cols, rows int) error`
- `DisconnectLocal(sessionId string)`
- `ListLocalShells() ([]localterm_svc.ShellInfo, error)`(直接委托 `localterm_svc.DetectShells()`)

事件(Wails Events,base64 payload 与 serial 一致):
- `local:connect:<connectionId>` —— `{type: "progress"|"connected"|"error", sessionId?, error?}`
- `local:data:<sessionId>` —— base64(stdout/stderr)
- `local:closed:<sessionId>` —— 进程退出 / 会话关闭

## 6. 数据 / 迁移

**无需 DB 迁移**。`asset.Type` 是字符串列,配置存 JSON `Config` 字段;新增类型只是新的 `Type` 取值(加 serial 时同样无迁移)。软删除沿用 `Status`。

## 7. 测试(TDD)

- `internal/service/localterm_svc/*_test.go`:注入 fake `ptyProcess`,覆盖 Connect→SetCallbacks→readOutput 合批、Write、Resize 透传、Wait→closed 回调、CloseAll。goconvey + testify。Unix 下另跑一个走真实 `startPTY` 起 `/bin/sh` 的集成用例(echo 回显)。
- `internal/assettype/local_test.go`:镜像 `serial_test.go`,覆盖 `ValidateCreateArgs`(shell 可空)、`ApplyCreate/UpdateArgs`、`SafeView`。
- `frontend/src/__tests__/terminalRegistry.test.ts`:加 `local` transport 用例(eventPrefix、writeFn 取表正确);新增 `TRANSPORTS` 表的单测确保三种 transport 能力齐全。
- 回归:确认 serial 二元分支重构后,ssh/serial 行为不变(分屏禁用、写入、resize)。

## 8. 风险 / 注意

- **安全**:本地终端 = 对用户本机完全访问。但因不接 AI,风险等同用户自开终端 app,未引入新攻击面。
- **Windows ConPTY**:需 Win10 1809+。`conpty.Start` 在更老系统会失败 → `ConnectLocalAsync` 返回明确错误,前端 toast 提示(走 `toast.error`,右下角)。需在 Windows 实机/虚机实测一轮,CI 覆盖 Windows 构建。
- **进程回收**:Close 时 Unix 发 SIGHUP + 带超时回收;避免僵尸进程。`pty_windows.go` 由 conpty 关闭句柄回收。
- **前端重构面**:第 3.4 的二元→映射表是本次最大的"非新增"改动面,需保证 ssh/serial 行为零回归(故第 7 节列了回归项)。
