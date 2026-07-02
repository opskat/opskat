# 重连时恢复上次工作目录 (Restore last working directory on reconnect)

- **Issue:** #195 —「保持后台连接 + 重新连接到上次的路径」。keepalive/TCP 调优部分已由 #204 完成，本设计只做**重连恢复路径**部分。
- **Date:** 2026-07-02
- **Status:** Approved (inline design), pending implementation.

## 背景 / Problem

SSH 终端常因服务端 `sshd ClientAlive*` / shell `TMOUT` 空闲登出而掉线（客户端 keepalive 无法阻止，见 `ssh.go readOutput` 的 `io.EOF` 注释）。掉线后用户在同一 tab 手动重连（面板提示「按 Enter 重连」），新会话落在 home 目录，用户还得手动 `cd` 回原来的工作目录。

目标：新增一个**每资产**的高级开关，开启后掉线重连时自动回到上次所在目录。

## 关键约束（来自代码勘察）

1. 远端 cwd 只有在**目录同步（directory sync）**激活时才可知——它向 shell 注入隐形 prompt hook（`\x1b]1337;opskat:` OSC 序列 + `/proc/PID/cwd` 探针），仅支持 bash/zsh/ksh/mksh，回显被 `suppressInternalEcho` 抹掉。默认**不启用**，目前只有文件管理器「跟随终端」时按需 `EnableSSHSync`。
2. 会话一旦断开，cwd **不可事后恢复**（`/proc/PID/cwd` 需要活着的 shell 进程）。因此路径必须在会话**存活期间持续追踪**，没有「断开时抓一把」的选项。
3. 前端已在**连接和重连时**注册 `ssh:sync:{sessionId}` 监听（`terminalStore.ts:618`、`:747`），并把状态存进 `sessionSync[sessionId]`（含 `cwd`）。所以只要后端启用同步，前端 cwd 自动可得，无需新增捕获代码。
4. `Session.ChangeDirectoryDirect(path)` 已存在：写 `builtin cd -- '<path>'\r` 且**回显被抑制**（走 `writeInternal`），无需 prompt hook 即可切目录。已有测试 `ssh_test.go:553`。

## 决策（已与维护者确认）

- **捕获机制：** 开关开启时，该资产的 SSH 终端**连接即自动启用目录同步**，复用现有成熟机制。不支持的 shell 静默不恢复。
- **重连方式：** 保持现有**手动重连**（按 Enter），只在这条重连路径上恢复 cwd。**不**加自动重连（超出本 issue 范围）。
- **开关默认：** 关（自动注入 hook 属行为变更，默认关更稳）。
- **恢复可见性：** 复用 `ChangeDirectoryDirect` 的**回显抑制** `cd`——用户直接落在正确目录，不显示 `cd` 命令。

## 设计

### 数据层：每资产高级开关（沿用 `KeepAliveIntervalSeconds` 模式）

- **后端 `SSHConfig`**（`internal/model/entity/asset_entity/asset.go`）：新增
  ```go
  // RestoreCwdOnReconnect 开启后，该资产 SSH 终端断线手动重连时自动 cd 回上次目录，
  // 并在连接时自动启用目录同步以持续追踪 cwd（仅 bash/zsh/ksh/mksh）。
  RestoreCwdOnReconnect bool `json:"restore_cwd_on_reconnect,omitempty"`
  ```
  随 `GetSSHConfig`/`SetSSHConfig` 的 JSON blob 自动往返。**无需**改 `assettype/ssh.go`（keepalive 同样未走 `ApplyCreateArgs`；前端表单直接序列化整个 `SSHConfig` JSON）。

- **前端类型**（`frontend/src/components/asset/SSHConfigSection.config.ts`）：
  - `SSHConfig` JSON 接口加 `restore_cwd_on_reconnect?: boolean`。
  - `SSHFormState` 加 `restoreCwdOnReconnect: boolean`；`SSH_DEFAULTS` 置 `false`。
  - `buildSSHConfig`：`if (state.restoreCwdOnReconnect) cfg.restore_cwd_on_reconnect = true;`（omitempty 语义，false 不写）。
  - `parseSSHConfig`：`restoreCwdOnReconnect: cfg.restore_cwd_on_reconnect || false`。

- **表单 UI**（`frontend/src/components/asset/SSHConfigSection.tsx` 的 `advanced` 组）：在 keepalive 字段后新增一个 `custom` 字段，用 `Field` + `Switch`（复用现有组件），label/hint 走 i18n。

- **i18n**（`zh-CN` / `en` 的 `common.json`）：`asset.sshRestoreCwdOnReconnect`（label）、`asset.sshRestoreCwdOnReconnectHint`（说明：仅支持 bash/zsh/ksh/mksh，会自动启用目录同步）。

### 捕获：开启时自动启用目录同步

- `ConnectConfig`（`internal/service/ssh_svc/ssh.go`）新增 `RestoreCwdOnReconnect bool`、`InitialWorkdir string`。
- `internal/app/ssh/ssh_ops.go` 的 `ConnectSSH` **和** `ConnectSSHAsync` 构建 `connectCfg` 时：
  - `RestoreCwdOnReconnect: sshCfg.RestoreCwdOnReconnect`
  - `InitialWorkdir: req.InitialWorkdir`
- `SSHConnectRequest`（`ssh_ops.go`）新增 `InitialWorkdir string \`json:"initialWorkdir"\``。
- **编排放在 `Manager.Connect`（`createSession` 之后）**，保持 `createSession` 单一职责：
  ```go
  sessionID, err := m.createSession(...)
  if err != nil { ... }
  if cfg.RestoreCwdOnReconnect {
      if sess, ok := m.GetSession(sessionID); ok {
          // 恢复：重连时 cd 回上次目录（首次连接 InitialWorkdir 为空 → no-op）
          if err := sess.RestoreWorkingDirectory(cfg.InitialWorkdir); err != nil {
              logger.Default().Warn("restore cwd on reconnect failed",
                  zap.String("sessionID", sessionID), zap.Error(err))
          }
          // 捕获：延迟后自动启用目录同步，持续追踪 cwd 供下次重连
          go m.autoEnableDirectorySync(sess)
      }
  }
  ```
- 新增 `Session.RestoreWorkingDirectory(dir string) error`：`dir == ""` 直接返回 nil；否则 `return s.ChangeDirectoryDirect(dir)`。（可用 `recordingWriteCloser` 单测。）
- 新增 `Manager.autoEnableDirectorySync(sess)`：`time.Sleep(autoSyncSettleDelay)` 后 `sess.EnableSync()`，best-effort（不支持 shell / 超时 → `logger.Default().Warn` 记录，不影响会话）。`autoSyncSettleDelay`（默认 ~1s）设为包级变量便于测试缩短。settle 延迟避免注入 hook 与 sshd 的 motd/首个 prompt 交错。日志按 DEVELOP.md「三态」记 start/fail。

### 恢复：重连时 cd 回上次目录

- **掉线即快照 cwd 到 pane**（关键修正）：服务端掉线时 `ssh:closed` → `markClosed(sessionId)` → `unregisterSessionSyncListener` 会**立即清空 `sessionSync[sessionId]`**，而用户按 Enter 重连发生在其后——若重连时才读 `sessionSync`，cwd 已丢。故在 `markClosed`（以及对称的 `disconnect`）里，于 `unregisterSessionSyncListener` **之前**把 `sessionSync[sessionId]?.cwd` 快照到 `TerminalPane.lastCwd`（pane 长存，不随监听注销消失）。
- **前端 `reconnect()`**（`frontend/src/stores/terminalStore.ts`）：优先读 pane 快照，仍连接的活会话回退读 sessionSync：
  ```ts
  const lastCwd = pane?.lastCwd ?? get().sessionSync[sessionId]?.cwd ?? "";
  ...
  spec.connectAsync(meta.assetId, { cols: 80, rows: 24, password: "", initialWorkdir: lastCwd })
  ```
- `connectAsync` opts 类型加可选 `initialWorkdir?: string`；SSH transport 把它塞进 `ssh_models.SSHConnectRequest`；serial/local 忽略。
- 普通 `connect()`（`terminalStore.ts:531`）**不**传 `initialWorkdir` → 后端 `InitialWorkdir` 为空 → 只捕获不恢复（对应「仅重连恢复」决策）。
- **后端权威闸门：** 只有 `cfg.RestoreCwdOnReconnect`（读自 `sshCfg`）为真时才 `cd`。即便前端传了 cwd（例如文件管理器独立开了同步但本开关关着），后端也不恢复 → 尊重开关。前端因此无需读取该配置。

### 时序（重连，开关开）

shell 启动 → 立即写 `builtin cd -- '<lastCwd>'`（回显抑制，恢复）→ `autoSyncSettleDelay` 后 `EnableSync` 注入 hook（恢复追踪）→ 新 prompt 显示已在恢复后的目录。

## 边界 / 限制

- 非 bash/zsh/ksh/mksh：同步无法追踪 → cwd 未知 → 落在 home（同现状），静默。
- cwd 未知（同步未及填充 / 用户身处全屏 TUI）：`lastCwd` 空 → 跳过恢复。
- 目录已被删除：`builtin cd` 失败，shell 自报错并留在 home，不特殊处理。
- 分屏 `NewSessionFrom` 不走 `Connect`，本期**不**为分屏 pane 自动启用同步/恢复（超出 issue 范围，记为已知缺口）。
- 仅 SSH 生效（serial/local `hasDirectorySync: false`）。

## 测试（TDD）

- **前端 vitest**
  - `SSHConfigSection.config.test.ts`：`restoreCwdOnReconnect` 的 build（true 写 / false 不写）与 parse 往返。
  - `terminalStore.test.ts`：`reconnect` 把 `initialWorkdir` 传给 `connectAsync`；普通 `connect` 不传；cwd 未知传空串；**掉线（`markClosed` 清空 sessionSync）后重连仍能从 pane 快照恢复**。
- **Go**
  - `asset_entity/asset_test.go` 的 `TestAsset_SSHConfig` 往返块补 `RestoreCwdOnReconnect: true`。
  - `ssh_svc/ssh_test.go`：`TestSession_RestoreWorkingDirectory` —— 空 dir 不写；非空 dir 写 `builtin cd -- '<dir>'\r`（复用 `recordingWriteCloser`）。
- **手动/观察验证**（`Manager.Connect` 无 fake SSH server，无法单测端到端）：按 AGENTS.md「靠观察验证」，跑真机/opsctl，掉线重连后核对落回原目录，并查 `logs/opskat.log` 的 auto-sync start/fail 日志。

## 需重新生成的产物

- `SSHConnectRequest` 改了 → `frontend/wailsjs/go/**`（含 `models.ts`）需 `wails generate module` 重新生成（gitignore 产物，不提交）。
