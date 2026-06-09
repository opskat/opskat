# 终端链接高亮开关 (#153) — 设计文档

> 状态：已定稿，待实现。日期：2026-06-07。分支：`pr-153`（位于 #153 实现及其 revert 之上）。

## 1. 背景与动机

终端输出里的 URL 目前**已经可点击**：`terminalRegistry.ts` 加载了 `WebLinksAddon`，悬停下划线 + 点击调 `BrowserOpenURL`。缺的是「常驻高亮」——让 URL 平时就以主题链接色显示，一眼能认出来，而不是只有鼠标悬停才有下划线。

#153 曾实现过这个开关，但**被整体 revert**。复盘 revert 的三个 commit（`d45faa6e` 加开关、`f41c5b23` 修刷新、`b04d0d48` 确保新输出生效）后，根因是：

- **两套机制并存且互相打架**：既用 xterm decoration（marker + `registerDecoration`，在 `onWriteParsed`/`onScroll`/`onResize` 上刷新），又用 **ANSI 注入**（`colorizeOutput` 往输出流里塞 `\x1b[38;2;…m`）。两条路径语义重叠，难以推理。
- **decoration 刷新策略错误**：每个事件都 `clearDecorations()` 全清再全量重扫视口重建 —— 这就是「修刷新 / 确保新输出生效」两个 bugfix 一直在追的抖动/竞态来源。
- **列几何算错**：decoration 的 `x` 直接用了 JS 字符串下标 `match.start` 当列号。一旦行内有 CJK/宽字符，字符串下标 ≠ 终端列号，高亮就会错位。

本设计**只保留 decoration 一套机制，并做对**，删掉 ANSI 注入，删掉全量重扫。

## 2. 目标 / 非目标

**目标**
- 设置里新增「高亮链接」开关，默认**关闭**，状态持久化。
- 开启后：normal buffer 中的 `http(s)://` URL 以主题链接色常驻着色；关闭后回退到「仅悬停下划线」（即当前行为）。
- 高亮范围与可点击范围对齐（同一套 URL 边界规则）。
- 开关/主题色变更**即时**生效于当前可见内容与后续输出与回滚（scrollback）。

**非目标**
- 不改动点击行为：`WebLinksAddon` 始终挂载，点击/悬停逻辑不变。高亮开关只管「着色」。
- 不做颜色自定义（用主题链接色，YAGNI）。
- 不在 alternate buffer（全屏 TUI）里高亮（见 §7 限制）。

## 3. 方案选型

最终方案：**decoration 叠加层（overlay）**。核心不变量：**绝不改写 buffer**，URL 着色是一个由 buffer 状态推导出的纯叠加层 `decorations = f(可见buffer, enabled, color)`，输出数据处理路径逐字节不变。

被否决的备选：
- **CSS 给链接层着色**：项目默认 WebGL 渲染器，文本画在 canvas 上，没有可 CSS 的 DOM 文本层 —— 不可行。
- **`registerLinkProvider` 的 link decoration**：xterm 只在**悬停**时绘制 link 装饰，无法常驻。
- **OSC 8 超链接注入 / ANSI 颜色注入**：都要改写程序输出流（伪造程序没发过的字节、且 `\x1b[39m` 复位会污染已着色文本的后续字符），与「内容/表现分离」相悖。注入唯一优势是能在 alt buffer 生效，但已确认接受 alt buffer 限制（§7）。

> 选 decoration 的代价是「列几何、换行、滚动跟随、reflow、活动输入行」这些 xterm 在注入方案里免费帮我们处理的事，现在要自己做对。本设计逐项给出做法。

## 4. 架构与组件

| 文件 | 改动 | 职责 |
|---|---|---|
| `frontend/src/components/terminal/terminalUrlScan.ts` | **新增（纯函数）** | URL 检测 + **正确的列几何**。无 xterm 副作用，可独立单测。 |
| `frontend/src/components/terminal/terminalUrlHighlighter.ts` | **新增** | overlay 控制器：维护可见窗口内的 decoration，diff 式 reconcile，订阅事件。 |
| `frontend/src/components/terminal/terminalRegistry.ts` | 编辑 | 挂载/释放 highlighter；新增 `terminalUrlHighlightColor(theme)`。**数据处理回调不变**。 |
| `frontend/src/components/terminal/Terminal.tsx` | 编辑 | 在已有主题 effect 里把 `highlightLinks` + 链接色推给 highlighter。 |
| `frontend/src/stores/terminalThemeStore.ts` | 编辑 | `highlightLinks` 状态 + `setHighlightLinks`，随现有 `partialize` 持久化。 |
| `frontend/src/components/settings/AppearanceSection.tsx` | 编辑 | GPU 加速开关下方新增一行 `Switch`。 |
| `frontend/src/i18n/locales/{en,zh-CN}/common.json` | 编辑 | `terminal.highlightLinks` / `terminal.highlightLinksHint`。 |

### 4.1 `terminalUrlScan.ts`（关键正确性所在）

```
URL_RE = /https?:\/\/[^\s<>"'`]+/gi
TRAILING_PUNCT = /[),.;!?\]}]+$/
```

为避免循环依赖（`registry → highlighter → scan → registry`），把 URL 规则下沉到 `terminalUrlScan.ts`：**`normalizeHttpUrl` 从 `terminalRegistry.ts` 迁入本模块并导出**，registry 的 `WebLinksAddon` 回调改为 `import { normalizeHttpUrl } from "./terminalUrlScan"`。依赖单向（registry → scan），且高亮与点击共用同一套 URL 规则，使**高亮边界 === 可点击边界**由构造保证。

**列几何（修复 revert 的根因 bug）**：不能用字符串下标当列号。按 cell 遍历：

```
findUrlSpansInText(line: IBufferLine, cols): { text, colStarts }
  text = ""; colStarts = []        // colStarts[i] = text[i] 起始的终端列；末尾多压一个“最后一格之后的列”
  for x in 0..cols-1:
    cell = line.getCell(x, reuse); if !cell: break
    w = cell.getWidth()
    if w === 0: continue            // 宽字符的占位格，跳过
    chars = cell.getChars() || " "
    for ch of chars: text += ch; colStarts.push(x)
  colStarts.push(<最后一个非零宽 cell 的列 + 其宽度>)   // 哨兵，供算末列
```

URL span：对 `text` 跑 `URL_RE` → 修剪尾标点 → `normalizeHttpUrl` 校验 → `startCol = colStarts[i]`，`endColExclusive = colStarts[i + url.length]`，`width = endColExclusive - startCol`。这样 IDN/CJK 宽字符场景列号也正确。

**换行 URL（决策：完整逐行段着色）**：xterm 把折行存为连续 `IBufferLine`，续行 `isWrapped === true`。
- 逻辑行 = 一个 `isWrapped===false` 的起始行 + 其后所有 `isWrapped` 续行。
- 把逻辑行各物理行的**整行文本**拼成逻辑文本，并为每个逻辑文本字符记录 `(物理行号, 列)`。
- 在逻辑文本上跑 URL 检测；命中的 URL 可能跨多物理行 → 按物理行分组，每个物理行发**一个** decoration 段（该行内的 startCol/width）。

导出（建议）：`findUrlRowSpans(buffer, startLine, endLine, cols): { line, startCol, width, url }[]`，内部处理换行拼接与回映射，返回**按物理行**的段，供控制器直接建 decoration。

### 4.2 `terminalUrlHighlighter.ts`（overlay 控制器）

```
attachTerminalUrlHighlighter(term, { enabled, color }) -> { setEnabled, setColor, dispose }
```

- 内部状态：`enabled`、`color`（已校验 `#RRGGBB`）、`Map<key, IDecoration>`（仅可见窗口）。
- `sync()`（用 `requestAnimationFrame` 去抖，合并突发事件为每帧一次）：
  1. 若 `!enabled` 或 `buffer.active.type === 'alternate'` 或 color 非法 → 释放全部 decoration，return。
  2. 计算可见窗口 `[top, bottom]`：`top = viewportY`，若 `top` 行 `isWrapped` 则向上走到逻辑行首（保证滚入视口的换行 URL 完整着色）；`bottom = min(length-1, viewportY + rows - 1)`。
  3. `findUrlRowSpans` 得到期望段集合，key = `${absLine}:${startCol}:${width}:${url}`。
  4. **diff 式 reconcile**：当前 Map 里不在期望集合的 → dispose；期望集合里没有的 → `registerMarker(absLine-(baseY+cursorY))` + `registerDecoration({marker,x:startCol,width,foregroundColor:color,layer:'top'})` 建立。marker/decoration 取不到（alt buffer/已释放）就跳过。**绝不**全清重建。
- 订阅 `onWriteParsed`、`onScroll`、`onResize`、`buffer.onBufferChange` → 调度 `sync()`。`setEnabled`/`setColor` 改值后调度 `sync()`。
- `dispose()`：取消未决 rAF，dispose 订阅与全部 decoration。

为什么 bounded 且不抖：只为**可见窗口**维护 decoration（屏外 decoration 不渲染无意义）；diff 让未变的 decoration 保持不动 —— 这同时修掉 revert 的「全量重建抖动」和「列几何错位」两个根因。滚动时 onScroll 触发 sync，scrollback 进入视口才**惰性**着色。

### 4.3 `terminalRegistry.ts`

- 新增 `terminalUrlHighlightColor(theme?: ITheme): string | undefined` = `theme?.brightBlue ?? theme?.blue`，校验为 `#RRGGBB`（`registerDecoration.foregroundColor` 仅支持该格式）否则返回 `undefined`。
- `WebLinksAddon` 回调里的 `normalizeHttpUrl` 改为从 `terminalUrlScan.ts` 导入（该函数从本文件迁出，见 §4.1）。
- `term.open(container)` 后挂 `const urlHighlighter = attachTerminalUrlHighlighter(term, { enabled: init.highlightLinks === true, color: terminalUrlHighlightColor(init.theme) })`，存入 `TerminalInstance`，在 `instance.dispose` 中 `urlHighlighter.dispose()`。
- `getOrCreateTerminal` 的 `init` 增加可选 `highlightLinks?: boolean`。
- **数据事件回调保持原样**（`term.write(bytes)`，不解码、不着色、不改字节）。

### 4.4 `Terminal.tsx`

在已有主题 effect（依赖 `[xtermTheme, fontSize, fontFamily, scrollback]`）中加入 `highlightLinks` 依赖，并：

```
const inst = getTerminalInstance(sessionId)
inst?.urlHighlighter.setEnabled(highlightLinks)
inst?.urlHighlighter.setColor(terminalUrlHighlightColor(xtermTheme))
```

`highlightLinks` 取自 `useTerminalThemeStore`。挂载时把它传进 `getOrCreateTerminal(init)`。无需重建终端。

### 4.5 store / 设置 UI / i18n

- store：`highlightLinks: boolean`（默认 `false`）+ `setHighlightLinks(enabled)`；现有 `partialize`（排除 `fontFamily` 外全持久化）自动覆盖，无需改 `partialize`。
- 设置：GPU 加速开关下、`Separator` 上，加一行 `Label` + 说明 + `Switch`（沿用现有排版）。
- i18n（en / zh-CN）：
  - `terminal.highlightLinks`: "Highlight Links" / "高亮链接"
  - `terminal.highlightLinksHint`: "Keep terminal URLs tinted with the theme link color. When off, links are only underlined on hover." / "打开后，终端中的 URL 会以主题蓝色常驻显示；关闭时仅在鼠标悬停时显示下划线"

## 5. 数据流

- 开关（设置）→ `store.highlightLinks` → `Terminal.tsx` effect → `highlighter.setEnabled` → `sync()` → decoration 增删。
- 输出到达 → registry 数据回调原样 `term.write(bytes)` → xterm `onWriteParsed` → `sync()` → reconcile 可见窗口。
- 主题变更 → `Terminal.tsx` effect → `highlighter.setColor` → `sync()`。
- 滚动 → `onScroll` → `sync()`（scrollback 惰性着色）。
- buffer 切换（normal↔alt）→ `onBufferChange` → `sync()`（进 alt 清空，回 normal 重扫）。

## 6. 测试策略（TDD，先红后绿）

- `terminalUrlScan.test.ts`（纯，重点）：单 URL；多 URL 同行；尾标点修剪；**CJK 宽字符前置导致列偏移**（revert bug 的回归测试）；非法/非 http URL 不命中；换行逻辑行 → 逐物理行段（列与 width 正确）。
- `terminalUrlHighlighter.test.ts`（mock term：buffer/getCell/registerMarker/registerDecoration/onWriteParsed/onScroll/onResize/onBufferChange）：enabled 在正确列建 decoration；disabled 释放全部；alt buffer 为 no-op；setColor 重新着色；滚动时 reconcile 走 **diff**（未变的不重建，断言 dispose/create 次数）；dispose 清理订阅与 decoration。
- `terminalThemeStore.test.ts`：`highlightLinks` 默认 `false` + setter 切换（复用 revert 版用例）。
- `terminalRegistry.test.ts`：highlighter 被挂载、`dispose` 时被释放；**数据回调仍写原始字节**（行为不变）。

## 7. 已知限制（需在代码注释 + 此文档记录）

- **Alternate buffer 不高亮**：`registerDecoration` 在 alt buffer 返回 `undefined`、`registerMarker` 只作用于 normal buffer，故 `less`/`vim`/`git log` 分页器/`htop` 等全屏 TUI 中的 URL 不会着色（悬停可点击不受影响）。这是 decoration 方案相对注入方案的取舍，已确认接受。
- **极长换行 URL 起点远在视口上方**：可见窗口向上回溯逻辑行首来覆盖滚入情形；正常一两行折行可完整着色。

## 8. 范围与提交

- 一个特性提交，commit message 末尾带 `#153`（关联 issue 约定）。
- 不顺手做无关重构 / 格式化 / 死代码清理。
- 验证：`vitest` 跑前端单测；前端 lint（按仓库约定，不依赖 `pnpm build` 门禁）。
