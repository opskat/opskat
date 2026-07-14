# VNC 面板 UI/UX 重设计 — 设计文档

- 日期：2026-07-14
- 分支：`codex/vnc-rdp-222`
- Mockup：`docs/superpowers/mockups/vnc-redesign.html`（可交互，含状态切换 + 明暗切换）

## 目标

把 VNC 面板（`frontend/src/components/vnc/VNCPanel.tsx`）的 UI/UX 提升到与 RDP 面板一致的水准。当前 VNC 面板功能可用，但视觉更"重"、信息更少：混排的文字/图标按钮、黑屏上一个小 spinner、指纹校验走弹窗、底部一条常驻的"未配置文件通道"提示条，且缺少状态点 / 主机信息 / 运行时长 / 分辨率。

RDP 面板（`RDPChrome.tsx` + `RDPPanel.tsx`）已经具备想要的 chrome：slim 顶栏（身份 + 状态胶囊 + 分组动作）+ 全面板会话 overlay + 底部状态栏。**本设计让 VNC 采用同一套 chrome，并把两者近乎相同的展示层抽为共享组件（单一事实来源），RDP 一并改用。**

## 决策记录（brainstorm 结论）

1. **范围**：重排布局（不是仅换皮），对齐 RDP。
2. **优先级**：hybrid —— 顶部 slim 栏 + 底部状态栏都常驻（即 RDP 现有模式），不做 hover 隐藏。
3. **控件**：在保留现有能力基础上新增「特殊按键」与「剪贴板同步态」；**不做只读模式**。
4. **复用**：抽取共享展示原子（状态胶囊 / 状态栏 / 会话 overlay 外壳），**重构 RDP 一起消费**；两侧工具栏各自保留（动作集本就不同），但用同一批 `@opskat/ui` 基元与共享原子搭建，视觉一致。
5. **重连位置**：跟随 RDP —— 顶栏放「断开」(power)，「重新连接 / 编辑连接」只在 error/closed 的 overlay 出现。

## 可行性确认（对照 noVNC 1.5.0 源码 + 代码库）

全部可实现：

- 状态胶囊：`status` 状态已存在（idle/connecting/connected/closed/error）。
- `host:port` chip：解析 `asset.Config`（`host` / `port`，见 `VNCDetailInfoCard`）。
- 适应/原始：已用 `rfb.scaleViewport` + `clipViewport`，仅把文字 toggle 换成 `Segmented`。
- 特殊按键：`rfb.sendCtrlAltDel()` 是**公开方法**；`sendKey(keysym, code, down)` 已在粘贴逻辑中使用，可发 Alt+Tab / Super_L / Esc。
- 分辨率读数：连接后读取 noVNC 在容器内创建的 `<canvas>` 的 `.width` / `.height`（公开 DOM；`_fbWidth/_fbHeight` 亦存在但为私有）。VNC 会话 `resizeSession=false`，分辨率连接后基本固定，`connect` 事件后读一次即可。
- 运行时长：记录 `connectedAt`，与 `SessionToolbar` / RDP 同法。
- 指纹校验入 overlay：把现有 `serverFingerprint` / `approveServer` / `cancelVNCServerVerification` 逻辑从 `ConfirmDialog` 搬进 overlay 状态，逻辑不变。
- 重连 / 编辑：`connect()` 已有；`onEdit` 按 RDP 方式在 `MainPanel` 里一行接上（`<VNCPanel ... onEdit={() => onEditAsset(asset)} />`）。
- 全屏 / 断开：已实现（`DisconnectVNC`）。

**唯一 caveat —— 剪贴板开关的实现方式与 RDP 不同**：RDP 调后端 `SetRDPClipboardEnabled`；noVNC 剪贴板始终开启，所以 VNC 的开关是**前端 gate**——关时不再把 `clipboard` 事件写入本地、并拦截 paste-to-remote。UX 一致，管线不同。

## 架构

新增共享展示层，RDP + VNC 共用。共享原子放在新目录 `frontend/src/components/remote/`（仅前端展示层；与后端"VNC 专属边界"的收敛无关——那是 IPC/service 层的边界，本目录不触碰后端）。

```
components/remote/                 ← 新增：共享展示原子（dumb / 无 i18n，label 由调用方传入）
  RemoteStatusPill.tsx             status + label → 颜色映射（现 RDP 的 STATUS_PILL）
  RemoteStatusBar.tsx              分辨率 W×H · 适应徽章 · 运行时长
  RemoteConnectionOverlay.tsx      connecting / error / closed 外壳 + 重连/编辑动作；支持传入自定义节点（VNC 用于指纹校验态）
  remoteChrome.ts                  共享类型（RemoteStatus）+ formatDuration 工具（从 rdpInput 迁出或复用）

components/rdp/
  RDPChrome.tsx                    RDPToolbar 保留；RDPStatusBar/RDPSessionOverlay 改为薄封装 → 调用共享原子
  RDPPanel.tsx                     基本不变（消费共享原子后行为一致，测试须仍绿）

components/vnc/
  VNCPanel.tsx                     重排为新 chrome 布局
  VNCToolbar.tsx                   新增：VNC 专属工具栏（用共享原子 + @opskat/ui 基元）
```

### 共享原子的接口（presentational，i18n 由调用方传入）

- `RemoteStatusPill({ status, label })`：`status: RemoteStatus`（connecting/connected/error/closed），`label` 为已翻译文案。
- `RemoteStatusBar({ width, height, showFit, fitLabel, connected, elapsed })`：`fitLabel` 已翻译；内部 `formatDuration(elapsed)`。
- `RemoteConnectionOverlay({ status, error, host, port, labels, onReconnect, onEdit?, children? })`：渲染 connecting/error/closed 通用态；`children` 在需要时叠加自定义态（VNC 指纹校验用同一容器样式）。`labels` 为一组已翻译字符串（connecting/error/closed/reconnect/edit）。

> 抽取原则：**只共享近乎相同、低风险的展示原子；工具栏动作各自保留**（VNC 有 Files、keysym 特殊键、前端剪贴板、指纹；RDP 有 scancode chord、后端剪贴板、无 Files）。避免把两个本就分叉的工具栏硬塞进一个抽象。

## VNC 面板布局（重排后）

外层 `flex-col`，顶栏与状态栏横跨整个面板：

```
┌ VNCToolbar (h-10, bg-muted/30) ───────────────────────────────────────────┐
│ [icon] 名称  [host:port]  [●状态胶囊]      [适应|原始] [⌨特殊键▾] [📋] [📁] [⛶] [⏻]│
├────────────────────────────────────────────────┬───────────────────────────┤
│ viewport (bg-black, flex-1)                    │ FileManagerPanel (可选)   │
│   · noVNC <canvas>                             │ （复用现有组件，右侧内嵌）│
│   · RemoteConnectionOverlay（非 connected 态） │                           │
│   · 指纹校验 overlay（verify 态）              │                           │
├────────────────────────────────────────────────┴───────────────────────────┤
│ RemoteStatusBar (h-6): [⤢ 1920×1080]  [自动适应]  [📋 已同步]      02:14:37 │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 顶栏动作组（右对齐，从左到右）

1. **Segmented 适应 | 原始**（替换现有 `Scale`/`Original` 文字 toggle）→ `rfb.scaleViewport` true/false。
2. **特殊按键 ▾**（`DropdownMenu`，触发器 `⌨ Ctrl+Alt+Del`）：
   - `Ctrl + Alt + Del` → `rfb.sendCtrlAltDel()`
   - `Alt + Tab` / `Esc` → `rfb.sendKey(keysym, code, down)` 序列
   - 与 RDP 特殊键集完全一致（三项）；未连接时禁用。
3. **剪贴板** toggle（图标）：前端 gate；开=success 色 + 状态栏"已同步"，关=灰。
4. **文件** toggle（`FolderOpen`）：切换右侧 `FileManagerPanel`；`!session.fileSshAssetId` 时禁用 + tooltip（**删除**当前底部常驻的"未配置文件通道"提示条）。
5. **全屏**（`Maximize2`/`Minimize2`）。
6. **断开**（`Power`，destructive 色）。

### viewport overlay 状态

- **connecting**：spinner + 文案 + `host:port`（`RemoteConnectionOverlay`）。
- **verify（VNC 专属）**：`Fingerprint` 图标 + 说明 + RSA SHA-256 指纹（mono）+ `取消` / `校验并连接`；用 overlay 容器样式（替换现 `ConfirmDialog`）。
- **error**：`AlertTriangle` + 错误信息 + `重新连接` / `编辑连接`。
- **closed**：`Power` + 已断开 + `重新连接`。

### 底部状态栏

分辨率 `W × H`（`Scaling` 图标）· 适应徽章（fit 时）· 剪贴板同步指示 · 运行时长（右，`tabular-nums`）。

## i18n

沿用各自命名空间：VNC 用 `vnc.*`，RDP 用 `asset.rdp*`；共享原子不含 i18n，label 由各面板传入（en / zh-CN 各语言用地道表达，不逐字对齐）。VNC 需新增键（en + zh-CN）：`vnc.disconnect`、`vnc.viewFit` / `vnc.viewOriginal`、`vnc.specialKeys`、`vnc.clipboardOn` / `vnc.clipboardOff`、`vnc.clipboardSynced`、`vnc.editConnection`、`vnc.autoFit`、状态标签（connecting/connected/error/closed，可复用现有或新增）。移除 `vnc.fileChannelDisabled`（改为禁用按钮 tooltip）。

## 测试（TDD）

- 共享原子：`RemoteStatusPill` 状态→色、`RemoteStatusBar` 分辨率/时长渲染、`RemoteConnectionOverlay` 各态与 `onReconnect/onEdit` 回调。
- `VNCToolbar`：动作回调、未连接禁用、Files 无通道禁用、剪贴板 toggle。
- `VNCPanel`：更新现有 `VNCPanel.test.tsx` 到新 chrome；指纹校验从弹窗改 overlay 后的 approve/cancel 路径；分辨率读数。
- **RDP 回归**：`RDPPanel.test.tsx` 等重构后须仍全绿（重构不改行为）。

## 影响面 / 改动清单

- 新增：`components/remote/{RemoteStatusPill,RemoteStatusBar,RemoteConnectionOverlay}.tsx` + `remoteChrome.ts`；`components/vnc/VNCToolbar.tsx`；对应测试。
- 改：`components/vnc/VNCPanel.tsx`（重排 + 新能力）；`components/rdp/RDPChrome.tsx` + `RDPPanel.tsx`（改用共享原子）；`components/layout/MainPanel.tsx`（给 VNCPanel 接 `onEdit`）；`i18n/locales/{en,zh-CN}/common.json`。
- 不改后端；不动 VNC 会话 / IPC 边界。

## 非目标（YAGNI）

- 只读模式（明确不做）。
- 顶栏 hover 自动隐藏 / 沉浸态（保持 RDP 常驻 chrome 一致）。
- 剪贴板改走后端（保持 noVNC 前端 gate）。
- 后端 / 会话传输改动。
