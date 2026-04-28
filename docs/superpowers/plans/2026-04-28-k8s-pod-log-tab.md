# K8s Pod 日志独立 Tab 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Pod 日志从详情面板移至独立 tab，每个日志 tab 拥有独立状态，支持同时打开多个 Pod 的日志。

**Architecture:** 日志状态从全局改为按 tab 的 `Record<string, LogTabState>`。新增 `log:` 前缀 tab 类型，每个实例持有独立的 `logStreamID` 和 xterm 终端。`K8sLogsPanel` 改为按 tab 渲染，通过 `updateState` 回调更新对应 tab 的状态。

**Tech Stack:** React 19, TypeScript, Tailwind CSS 4, xterm.js, Wails IPC

---

## 文件结构

| 文件 | 职责 |
|------|------|
| `frontend/src/components/k8s/K8sClusterPage.tsx` | tab 系统扩展、日志状态管理、日志 tab 渲染分支 |
| `frontend/src/components/k8s/K8sLogsPanel.tsx` | 日志面板（容器选择器 + 按钮 + xterm），改为按 tab 隔离 |
| `frontend/src/components/k8s/K8sLogTerminal.tsx` | xterm 终端组件（已存在，无需修改） |
| `frontend/src/i18n/locales/zh-CN/common.json` | 新增 `asset.k8sViewPodLogs` 翻译 |
| `frontend/src/i18n/locales/en/common.json` | 新增 `asset.k8sViewPodLogs` 翻译 |

---

### Task 1: 新增 i18n 翻译

**Files:**
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`
- Modify: `frontend/src/i18n/locales/en/common.json`

- [ ] **Step 1: 在 zh-CN 中添加翻译**

在 `asset` 命名空间下添加（找到 `k8sPodLogs` 附近）：

```json
"k8sViewPodLogs": "查看日志"
```

- [ ] **Step 2: 在 en 中添加翻译**

```json
"k8sViewPodLogs": "View Logs"
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/i18n/locales/zh-CN/common.json frontend/src/i18n/locales/en/common.json
git commit -m "🔧 config: add i18n keys for view pod logs button"
```

---

### Task 2: 改造 K8sLogsPanel 为按 tab 隔离

**Files:**
- Modify: `frontend/src/components/k8s/K8sLogsPanel.tsx`

- [ ] **Step 1: 修改 Props 接口**

```tsx
interface LogTabState {
  logStreamID: string | null;
  logContainer: string;
  logTailLines: number;
  logError: string | null;
}

interface K8sLogsPanelProps {
  tabId: string;
  assetId: number;
  containers: { name: string }[];
  namespace: string;
  podName: string;
  state: LogTabState;
  onStateChange: (patch: Partial<LogTabState>) => void;
}
```

- [ ] **Step 2: 内部持有独立的 logStreamIDRef**

在组件内部添加：

```tsx
const myStreamIDRef = useRef<string | null>(null);

useEffect(() => {
  return () => {
    if (myStreamIDRef.current) {
      StopK8sPodLogs(myStreamIDRef.current);
    }
  };
}, []);
```

- [ ] **Step 3: 重写 start/stop 逻辑**

start 函数：

```tsx
const start = () => {
  stop();
  terminalRef.current?.clear();
  onStateChange({ logError: null });

  StartK8sPodLogs(assetId, namespace, podName, state.logContainer, state.logTailLines)
    .then((streamID: string) => {
      myStreamIDRef.current = streamID;
      onStateChange({ logStreamID: streamID });

      const dataEvent = "k8s:log:" + streamID;
      const errEvent = "k8s:logerr:" + streamID;
      const endEvent = "k8s:logend:" + streamID;

      EventsOn(dataEvent, (data: string) => {
        if (myStreamIDRef.current !== streamID) return;
        terminalRef.current?.write(atob(data));
      });

      EventsOn(errEvent, (err: string) => {
        if (myStreamIDRef.current !== streamID) return;
        if (err === "context canceled" || err.includes("context canceled")) return;
        onStateChange({ logError: err });
      });

      EventsOn(endEvent, () => {
        if (myStreamIDRef.current !== streamID) return;
        myStreamIDRef.current = null;
        onStateChange({ logStreamID: null });
        EventsOff(dataEvent);
        EventsOff(errEvent);
        EventsOff(endEvent);
      });
    })
    .catch((e: unknown) => {
      onStateChange({ logError: String(e) });
    });
};

const stop = () => {
  if (myStreamIDRef.current) {
    StopK8sPodLogs(myStreamIDRef.current);
    myStreamIDRef.current = null;
  }
  onStateChange({ logStreamID: null });
};
```

- [ ] **Step 4: 修改容器选择器 onChange**

```tsx
onChange={(e) => {
  const container = e.target.value;
  onStateChange({ logContainer: container });
  if (state.logStreamID) {
    stop();
    start();
  }
}}
```

- [ ] **Step 5: 修改 tail 输入框 onChange**

```tsx
onChange={(e) => {
  const lines = Number(e.target.value);
  onStateChange({ logTailLines: lines });
}}
```

- [ ] **Step 6: 移除所有全局日志相关 props**

确保不再依赖 `logLines` prop，日志内容只通过 xterm 终端渲染。

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/k8s/K8sLogsPanel.tsx
git commit -m "♻️ refactor: make K8sLogsPanel tab-scoped with independent stream"
```

---

### Task 3: 改造 K8sClusterPage — 日志状态管理

**Files:**
- Modify: `frontend/src/components/k8s/K8sClusterPage.tsx`

- [ ] **Step 1: 定义 LogTabState 类型**

在文件顶部（或附近）添加：

```tsx
interface LogTabState {
  logStreamID: string | null;
  logContainer: string;
  logTailLines: number;
  logError: string | null;
}
```

- [ ] **Step 2: 替换全局日志 state**

把：
```tsx
const [logStreamID, setLogStreamID] = useState<string | null>(null);
const [logContainer, setLogContainer] = useState("");
const [logTailLines, setLogTailLines] = useState(200);
const [logError, setLogError] = useState<string | null>(null);
const logStreamIDRef = useRef<string | null>(null);
const logTerminalRef = useRef<...>(null);
```

替换为：
```tsx
const [logTabStates, setLogTabStates] = useState<Record<string, LogTabState>>({});
```

- [ ] **Step 3: 添加 updateLogTabState 辅助函数**

```tsx
const updateLogTabState = useCallback((tabId: string, patch: Partial<LogTabState>) => {
  setLogTabStates((prev) => ({
    ...prev,
    [tabId]: { ...(prev[tabId] || { logStreamID: null, logContainer: "", logTailLines: 200, logError: null }), ...patch },
  }));
}, []);
```

- [ ] **Step 4: 修改 openTab 支持 log tab**

在 `openTab` 函数中，Pod tab 的初始化后添加：

```tsx
const openLogTab = (ns: string, podName: string, container: string) => {
  const id = `log:${ns}:${podName}`;
  const label = `${t("asset.k8sPodLogs")}: ${podName}`;
  if (!innerTabs.some((t) => t.id === id)) {
    setInnerTabs([...innerTabs, { id, label }]);
    setLogTabStates((prev) => ({
      ...prev,
      [id]: {
        logStreamID: null,
        logContainer: container,
        logTailLines: 200,
        logError: null,
      },
    }));
  }
  setActiveTabId(id);
};
```

- [ ] **Step 5: 修改 closeTab 清理日志 tab**

在 `closeTab` 中：

```tsx
const closeTab = (id: InnerTabId) => {
  if (id.startsWith("log:")) {
    const state = logTabStates[id];
    if (state?.logStreamID) {
      StopK8sPodLogs(state.logStreamID);
    }
    setLogTabStates((prev) => {
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }
  // ...existing close logic...
};
```

- [ ] **Step 6: 移除 stopLogStream / startLogStream useCallback**

这两个全局函数不再需要，因为日志流管理已下放到 `K8sLogsPanel` 内部。

- [ ] **Step 7: 修改 Pod 详情面板 — 移除 K8sLogsPanel，添加查看日志按钮**

在 `activeTabId.startsWith("pod:")` 渲染分支中，移除 `<K8sLogsPanel>` 及其相关代码。在容器表格下方（或容器表格每行）添加：

```tsx
<div className="flex items-center gap-2 mt-2">
  {detail.containers.map((c) => (
    <button
      key={c.name}
      onClick={() => openLogTab(detail.namespace, detail.name, c.name)}
      className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs hover:bg-muted"
    >
      <ScrollText className="h-3 w-3" />
      {t("asset.k8sViewPodLogs")}: {c.name}
    </button>
  ))}
</div>
```

或者简化为一个按钮，默认打开第一个容器的日志：

```tsx
<button
  onClick={() => {
    const container = detail.containers[0]?.name || "";
    openLogTab(detail.namespace, detail.name, container);
  }}
  className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs hover:bg-muted"
>
  <ScrollText className="h-3 w-3" />
  {t("asset.k8sViewPodLogs")}
</button>
```

- [ ] **Step 8: 新增 log tab 渲染分支**

在 `activeTabId.startsWith("pod:")` 分支之后添加：

```tsx
{activeTabId.startsWith("log:") &&
  (() => {
    const parts = activeTabId.split(":");
    const ns = parts[1];
    const podName = parts.slice(2).join(":");
    const state = logTabStates[activeTabId];
    const detail = podDetails[`${ns}/${podName}`];
    if (!state || !detail) return null;
    return (
      <div className="max-w-5xl mx-auto p-4">
        <K8sLogsPanel
          tabId={activeTabId}
          assetId={asset.ID}
          containers={detail.containers}
          namespace={ns}
          podName={podName}
          state={state}
          onStateChange={(patch) => updateLogTabState(activeTabId, patch)}
        />
      </div>
    );
  })()
}
```

- [ ] **Step 9: 修改 tab 栏图标支持 log tab**

在 tab 栏渲染的 icon 逻辑中：

```tsx
{tab.id.startsWith("log:") ? (
  <ScrollText className="h-3 w-3" />
) : tab.id.startsWith("pod:") ? (
  // ...
```

- [ ] **Step 10: Commit**

```bash
git add frontend/src/components/k8s/K8sClusterPage.tsx
git commit -m "♻️ refactor: move Pod logs to independent tabs with per-tab state"
```

---

### Task 4: 清理未使用的 import 和变量

**Files:**
- Modify: `frontend/src/components/k8s/K8sClusterPage.tsx`

- [ ] **Step 1: 删除未使用的 import**

检查并删除：`useRef`（如果 logTerminalRef 已移除）、`StartK8sPodLogs`、`StopK8sPodLogs`（如果已从顶层移除）、`EventsOn`、`EventsOff`（如果已从顶层移除）等。

- [ ] **Step 2: 运行 lint**

```bash
cd frontend && pnpm lint
```

Expected: 无 K8s 相关错误。

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/k8s/K8sClusterPage.tsx
git commit -m "🎨 style: remove unused imports after log tab refactor"
```

---

### Task 5: 最终验证

**Files:**
- N/A（只读验证）

- [ ] **Step 1: 前端 lint 通过**

```bash
cd frontend && pnpm lint
```
Expected: 无 error。

- [ ] **Step 2: 前端测试通过**

```bash
cd frontend && pnpm test
```
Expected: 562/562 pass。

- [ ] **Step 3: 前端 build 通过**

```bash
cd frontend && pnpm build
```
Expected: build 成功。

- [ ] **Step 4: Commit**

```bash
git commit --allow-empty -m "✅ tests: verify K8s log tab refactor passes lint and tests"
```

---

## Self-Review

1. **Spec coverage:** 所有要求都有对应 Task：i18n (Task 1)、K8sLogsPanel 改造 (Task 2)、K8sClusterPage 改造 (Task 3)、清理 (Task 4)、验证 (Task 5)。
2. **Placeholder scan:** 无 TBD/TODO/"implement later"。所有代码均为可直接使用的完整实现。
3. **Type consistency:** `LogTabState` 在 Task 2 和 Task 3 中一致。`updateLogTabState` 的签名在 Task 3 和 Task 2 的 `onStateChange` 中一致。
