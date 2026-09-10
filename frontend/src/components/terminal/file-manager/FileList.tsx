import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronRight, File, Folder, Loader2 } from "lucide-react";
import { Button, cn, Input, ScrollArea } from "@opskat/ui";
import { sftp_svc } from "../../../../wailsjs/go/models";
import {
  collapseSingleChildChains,
  isSftpTreeEntryRow,
  type SftpTreeChainSegment,
  type SftpTreeDisplayRow,
  type SftpTreeRow,
} from "@/lib/sftpDirTree";
import {
  canMovePathToDirectory,
  formatBytes,
  formatDate,
  getParentPath,
  getPathBaseName,
  indentForDepth,
  splitNameForRename,
  treeGuideLines,
} from "./utils";

function indentStyle(depth: number, panelWidth: number) {
  return { paddingLeft: indentForDepth(depth, panelWidth) };
}

/** 面板宽度未测出前的合理默认,匹配常见的初始面板宽度配置。 */
const DEFAULT_PANEL_WIDTH_PX = 280;

/**
 * 缩进封顶要按面板"当前"宽度算,而宽度只有父级容器的渲染尺寸知道 —— 面板可拖拽调宽
 * (useResizeHandle),用 ResizeObserver 跟着容器盒子实测,而不是让父组件把 width 状态
 * 透传下来:调用方不用为了这一层缩进细节多接一个 prop,拖宽面板时也能跟着重新封顶。
 */
function usePanelWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T | null>(null);
  const [width, setWidth] = useState(DEFAULT_PANEL_WIDTH_PX);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      const rect = el.getBoundingClientRect();
      if (rect.width > 0) setWidth(rect.width);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [ref, width];
}

/**
 * 日期列的收起阈值:面板被拖到比默认宽度还窄时,名字宽度最稀缺,整个日期列先让位(决策 17)。
 * 阈值不取 TREE_MIN_CONTENT_PX(160):面板宽度本身被 sftpStore 钳在 200 以上,那样的阈值
 * 永远不会成立,收起就成了永不执行的死代码。
 */
const DATE_COLUMN_MIN_PANEL_PX = DEFAULT_PANEL_WIDTH_PX;

/**
 * 每一级祖先在自己的缩进位置上一条竖直参考线,并用一小段横线把本行接到直接父级的那条线上
 * (经典树参考线)。绝对定位到祖先的缩进 x —— 行的 paddingLeft 只决定内容起点,画在行元素上
 * 的 border-l 落在面板边缘,任何深度都表达不了层级归属。祖先链高亮由行上的 data 属性驱动
 * (见 FileList 的 hover 委托与 lineagePaths),纯 CSS 生效,不为每行引入 React state。
 */
function TreeGuides({ depth, panelWidth }: { depth: number; panelWidth: number }) {
  const lines = treeGuideLines(depth, panelWidth);
  if (!lines.length) return null;
  const parentLeft = lines[lines.length - 1].left;
  const guideTone =
    "bg-border/70 group-hover/row:bg-primary/50 group-data-[sftp-hover-lineage=true]/row:bg-primary/50 group-data-[sftp-lineage=true]/row:bg-primary/70";
  return (
    <>
      {lines.map((line) => (
        <span
          key={line.depth}
          aria-hidden="true"
          data-sftp-guide-depth={line.depth}
          style={{ left: line.left }}
          className={cn("pointer-events-none absolute inset-y-0 w-px", guideTone)}
        />
      ))}
      <span
        aria-hidden="true"
        data-sftp-guide-connector="true"
        style={{ left: parentLeft, width: indentForDepth(depth, panelWidth) - parentLeft }}
        className={cn("pointer-events-none absolute top-1/2 h-px", guideTone)}
      />
    </>
  );
}

/**
 * 一行的祖先行:展平序是深度优先(父行必在前),因此沿 DOM 往前找 depth 递减的条目行即可。
 * 折叠链在树里就是一行,按 depth 找因此自动落在链行上,不需要为链特殊处理。
 */
function ancestorRowElements(row: HTMLElement): HTMLElement[] {
  let depth = Number(row.dataset.sftpDepth);
  if (!Number.isInteger(depth)) return [];
  const out: HTMLElement[] = [];
  let node = row.previousElementSibling;
  while (node && depth > 0) {
    if (node instanceof HTMLElement && node.dataset.sftpEntryRow && node.dataset.sftpDepth !== undefined) {
      const nodeDepth = Number(node.dataset.sftpDepth);
      if (nodeDepth < depth) {
        out.push(node);
        depth = nodeDepth;
      }
    }
    node = node.previousElementSibling;
  }
  return out;
}

/** 粗略按等宽估算的每字符像素:文件名优先于缩进获得宽度,过长时中段省略而不是砍掉后缀(常是扩展名)。 */
const AVG_CHAR_PX = 6.5;
/** 行内除文件名外的固定开销:展开箭头、图标、右侧大小/日期列与内边距。 */
const ROW_CHROME_PX = 100;
/** 日期列在开销里固定占的那一份;不画它的行(目录行、窄面板)必须把这份宽度还给名字。 */
const DATE_COLUMN_PX = 40;

function middleEllipsisName(name: string, depth: number, panelWidth: number, chromePx: number): string {
  const available = panelWidth - indentForDepth(depth, panelWidth) - chromePx;
  const maxChars = Math.max(8, Math.floor(available / AVG_CHAR_PX));
  if (name.length <= maxChars) return name;
  const keep = maxChars - 1;
  const head = Math.ceil(keep * 0.6);
  const tail = keep - head;
  return `${name.slice(0, head)}…${name.slice(name.length - tail)}`;
}

/**
 * 拖拽事件的实际落点:从事件目标向上找最近的目录标记 —— 折叠链的每一段都各自携带这份标记
 * (仅当它自己是目录时才带 dir="true"),因此深层目录与链上的某一段都能被精确命中,而不是
 * 永远退回外层行代表的最深一段。命中不到目录就是 null,调用方必须当作"这里不能放"处理,
 * 不能退回行的路径 —— 否则会把文件行也当成合法落点。
 */
function resolveDropTargetPath(target: EventTarget | null): string | null {
  if (!(target instanceof Element)) return null;
  const hit = target.closest<HTMLElement>('[data-sftp-entry-dir="true"][data-sftp-entry-path]');
  return hit?.dataset.sftpEntryPath ?? null;
}

interface RenameInputProps {
  initialName: string;
  onCommit: (name: string) => void;
  onCancel: () => void;
}

function RenameInput({ initialName, onCommit, onCancel }: RenameInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [value, setValue] = useState(initialName);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    const range = splitNameForRename(initialName);
    input.focus();
    input.setSelectionRange(0, range.stemLength);
  }, [initialName]);

  return (
    <Input
      ref={inputRef}
      value={value}
      className="h-5 flex-1 border-0 bg-background px-1 text-xs shadow-none focus-visible:ring-1"
      onChange={(e) => setValue(e.target.value)}
      onBlur={onCancel}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === "Enter") onCommit(value);
        if (e.key === "Escape") onCancel();
      }}
    />
  );
}

interface FileListProps {
  canExternalEdit?: (entry: sftp_svc.FileEntry) => boolean;
  clipboardCutPaths: Set<string>;
  currentPath: string;
  error: string | null;
  loading: boolean;
  onExternalOpen?: (path: string) => void;
  onGoUp: () => void;
  onMoveEntriesToDirectory: (sourcePaths: string[], targetDirPath: string) => void;
  onNavigate: (path: string) => void;
  onOpenContextMenu: (x: number, y: number, entry: sftp_svc.FileEntry | null, entryPath: string | null) => void;
  onRenameCancel: () => void;
  onRenameCommit: (oldPath: string, nextName: string) => void;
  onRetry: () => void;
  onRetryExpand: (dirPath: string) => void;
  onToggleExpand: (dirPath: string) => void;
  renamePath: string | null;
  rows: SftpTreeRow[];
  selected: string[];
  setSelected: (next: string[] | ((prev: string[]) => string[])) => void;
}

export function FileList({
  canExternalEdit,
  clipboardCutPaths,
  currentPath,
  error,
  loading,
  onExternalOpen,
  onGoUp,
  onMoveEntriesToDirectory,
  onNavigate,
  onOpenContextMenu,
  onRenameCancel,
  onRenameCommit,
  onRetry,
  onRetryExpand,
  onToggleExpand,
  renamePath,
  rows,
  selected,
  setSelected,
}: FileListProps) {
  const { t } = useTranslation();
  const [containerRef, panelWidth] = usePanelWidth<HTMLDivElement>();
  // 无文件的单子目录链先在这里折叠成一行(每段各自可展开/换根),再进入选择/渲染管线。
  const displayRows = useMemo(() => collapseSingleChildChains(rows), [rows]);
  // 条目行才参与选择与区间选择;加载中 / 空 / 失败三种占位行只负责呈现子层状态。
  const entryRows = useMemo(() => displayRows.filter(isSftpTreeEntryRow), [displayRows]);
  const entryPaths = useMemo(() => entryRows.map((row) => row.path), [entryRows]);
  // 区间选择的下标按「可见条目行」计数,占位行不占位置。
  const renderRows = useMemo(() => {
    const items: { row: SftpTreeDisplayRow; index: number }[] = [];
    let entryIndex = 0;
    for (const row of displayRows) {
      items.push({ row, index: isSftpTreeEntryRow(row) ? entryIndex : -1 });
      if (isSftpTreeEntryRow(row)) entryIndex += 1;
    }
    return items;
  }, [displayRows]);
  // 父组件 FileManagerPanel 把 onNavigate / onOpenContextMenu / onRenameCancel 等
  // 写成内联箭头函数,而 selected 也住在父组件 —— 选中一变父组件就重渲,这些 prop
  // 全部换标识。若把它们直接传给行,行的 memo 一次都不会命中(实测反而更慢,因为白
  // 付了一遍 props 比较)。这里镜像进 ref,对外只暴露标识恒定的包装。
  const cbRef = useRef({
    canExternalEdit,
    onExternalOpen,
    onNavigate,
    onOpenContextMenu,
    onMoveEntriesToDirectory,
    onRenameCancel,
    onRenameCommit,
    onRetryExpand,
    onToggleExpand,
    renamePath,
  });
  useEffect(() => {
    cbRef.current = {
      canExternalEdit,
      onExternalOpen,
      onNavigate,
      onOpenContextMenu,
      onMoveEntriesToDirectory,
      onRenameCancel,
      onRenameCommit,
      onRetryExpand,
      onToggleExpand,
      renamePath,
    };
  }, [
    canExternalEdit,
    onExternalOpen,
    onNavigate,
    onOpenContextMenu,
    onMoveEntriesToDirectory,
    onRenameCancel,
    onRenameCommit,
    onRetryExpand,
    onToggleExpand,
    renamePath,
  ]);

  const stableCanExternalEdit = useCallback(
    (entry: sftp_svc.FileEntry) => cbRef.current.canExternalEdit?.(entry) ?? false,
    []
  );
  const stableExternalOpen = useCallback((path: string) => cbRef.current.onExternalOpen?.(path), []);
  const stableNavigate = useCallback((path: string) => cbRef.current.onNavigate(path), []);
  const stableOpenContextMenu = useCallback(
    (x: number, y: number, entry: sftp_svc.FileEntry | null, entryPath: string | null) =>
      cbRef.current.onOpenContextMenu(x, y, entry, entryPath),
    []
  );
  const stableToggleExpand = useCallback((dirPath: string) => cbRef.current.onToggleExpand(dirPath), []);
  const stableRetryExpand = useCallback((dirPath: string) => cbRef.current.onRetryExpand(dirPath), []);
  const stableMoveEntries = useCallback(
    (sourcePaths: string[], targetDirPath: string) =>
      cbRef.current.onMoveEntriesToDirectory(sourcePaths, targetDirPath),
    []
  );
  const stableRenameCancel = useCallback(() => cbRef.current.onRenameCancel(), []);

  // 命中判断用 Set:以前每行都跑一次 selected.includes(),n 行 × O(选中数) 在
  // 全选(shift 选完整个目录)时退化成 O(n²)。
  const selectedSet = useMemo(() => new Set(selected), [selected]);
  // 选中行 + 它整条祖先链:选中态本来就会让本组件重渲一次(行 memo 逐行比较),顺带算出
  // 祖先链是 O(可见行) 的一遍走行,不额外引入渲染。栈按 depth 维护 —— 折叠链在 displayRows
  // 里就是一行,它的 path 是链末段,与子行认到的祖先一致。
  const lineagePaths = useMemo(() => {
    const lineage = new Set<string>();
    if (selectedSet.size === 0) return lineage;
    const stack: string[] = [];
    for (const row of displayRows) {
      if (!isSftpTreeEntryRow(row)) continue;
      stack.length = row.depth;
      stack[row.depth] = row.path;
      if (!selectedSet.has(row.path)) continue;
      for (let d = row.depth; d >= 0; d -= 1) {
        // 祖先链一律整条加入,因此撞到已加入的一段就说明更浅的几段也都在里面了。
        if (lineage.has(stack[d])) break;
        lineage.add(stack[d]);
      }
    }
    return lineage;
  }, [displayRows, selectedSet]);
  // 悬停高亮不走 state:面板实测能挂 5000 行且没有虚拟化,每次移入都重渲一遍列表就是一场
  // 渲染风暴。这里只在委托到的容器事件里给祖先行打 data 属性,配色交给 CSS。
  const hoverRowRef = useRef<HTMLElement | null>(null);
  const hoverLineageRef = useRef<HTMLElement[]>([]);
  const markHoverLineage = useCallback((row: HTMLElement | null) => {
    for (const el of hoverLineageRef.current) el.removeAttribute("data-sftp-hover-lineage");
    hoverLineageRef.current = row ? ancestorRowElements(row) : [];
    for (const el of hoverLineageRef.current) el.setAttribute("data-sftp-hover-lineage", "true");
  }, []);
  const handleRowHover = useCallback(
    (event: React.MouseEvent<HTMLElement>) => {
      const target = event.target;
      // 占位行(加载中 / 空目录 / 失败)同样带 depth:悬停它时也该看出它挂在哪个目录下面。
      const row = target instanceof Element ? target.closest<HTMLElement>("[data-sftp-depth]") : null;
      if (row === hoverRowRef.current) return;
      hoverRowRef.current = row;
      markHoverLineage(row);
    },
    [markHoverLineage]
  );
  const clearRowHover = useCallback(() => {
    hoverRowRef.current = null;
    markHoverLineage(null);
  }, [markHoverLineage]);

  // 事件回调需要"当前选中集合",但不能把它放进依赖 —— 否则每次改选中回调就换标识,
  // 把所有行的 memo 全部打掉(和 QueryResultTable 里 handleCellContextMenu 同一个坑)。
  const selectedRef = useRef(selected);
  useEffect(() => {
    selectedRef.current = selected;
  }, [selected]);

  const lastClickedRef = useRef<number | null>(null);
  const draggedPathsRef = useRef<string[]>([]);
  const pointerDragRef = useRef<{
    dragging: boolean;
    pointerId: number;
    sourcePath: string;
    sourcePaths: string[];
    startX: number;
    startY: number;
  } | null>(null);
  const suppressNextClickRef = useRef(false);
  // 落点连同"会搬多少个条目"一起存:高亮之外还要在落点上说清这一放会执行什么。
  const [dropTarget, setDropTargetState] = useState<{ path: string; count: number } | null>(null);
  // dragover 每帧都在触发:落点没变就必须交回同一个对象,否则每一帧都换 prop 标识,
  // 把整目录所有行的 memo 全部打掉(和上面 selected 那一处同一个坑)。
  const setDropTarget = useCallback((next: { path: string; count: number } | null) => {
    setDropTargetState((prev) => {
      if (!prev || !next) return prev === next ? prev : next;
      return prev.path === next.path && prev.count === next.count ? prev : next;
    });
  }, []);
  const slowClickRef = useRef<{ path: string; time: number; timer: number | null }>({ path: "", time: 0, timer: null });

  const entryPathsRef = useRef(entryPaths);
  useEffect(() => {
    entryPathsRef.current = entryPaths;
  }, [entryPaths]);

  const selectEntry = useCallback(
    (path: string, index: number, event: React.MouseEvent) => {
      if (event.shiftKey && lastClickedRef.current !== null) {
        const start = Math.min(lastClickedRef.current, index);
        const end = Math.max(lastClickedRef.current, index);
        setSelected(entryPathsRef.current.slice(start, end + 1));
        return;
      }
      if (event.ctrlKey || event.metaKey) {
        setSelected((prev) => (prev.includes(path) ? prev.filter((item) => item !== path) : [...prev, path]));
        lastClickedRef.current = index;
        return;
      }
      setSelected([path]);
      lastClickedRef.current = index;
    },
    [setSelected]
  );

  const maybeStartSlowRename = useCallback(
    (path: string, index: number, eventTime: number) => {
      const now = eventTime;
      const prev = slowClickRef.current;
      const sel = selectedRef.current;
      if (prev.timer) window.clearTimeout(prev.timer);
      if (
        prev.path === path &&
        now - prev.time > 450 &&
        now - prev.time < 1400 &&
        sel.length === 1 &&
        sel[0] === path
      ) {
        prev.path = "";
        prev.time = 0;
        stableOpenContextMenu(-1, -1, null, null); // closes any pending menu in parent no-op path
        window.dispatchEvent(new CustomEvent("sftp:rename-request", { detail: { path } }));
        return;
      }
      slowClickRef.current = { path, time: now, timer: null };
      slowClickRef.current.timer = window.setTimeout(() => {
        if (slowClickRef.current.path === path) slowClickRef.current.path = "";
      }, 1500);
      lastClickedRef.current = index;
    },
    [stableOpenContextMenu]
  );

  const commitRename = useCallback((nextName: string) => {
    const { renamePath: path, onRenameCancel: cancel, onRenameCommit: commit } = cbRef.current;
    if (!path) return;
    const trimmed = nextName.trim();
    if (!trimmed) {
      cancel();
      return;
    }
    commit(path, trimmed);
  }, []);

  const isEntryTarget = (target: EventTarget | null) => {
    return target instanceof Element && !!target.closest("[data-sftp-entry-row]");
  };

  const getDragPaths = useCallback((event: React.DragEvent) => {
    if (draggedPathsRef.current.length) return draggedPathsRef.current;
    const raw = event.dataTransfer.getData("application/x-opskat-sftp-paths");
    if (!raw) return [];
    try {
      const parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
    } catch {
      return [];
    }
  }, []);

  const getMovableDragPaths = useCallback(
    (event: React.DragEvent, targetDirPath: string) =>
      getDragPaths(event).filter((path) => canMovePathToDirectory(path, targetDirPath)),
    [getDragPaths]
  );

  const getPointerDropTarget = useCallback((clientX: number, clientY: number, sourcePaths: string[]) => {
    const target = document.elementFromPoint(clientX, clientY);
    // 只认 dir + path 这对标记,不要求 entry-row:折叠链里每一段都单独携带它们,
    // 这样悬停在链的某一段上也能精确命中那一段,而不是永远退回外层行代表的最深一段。
    const hit = target?.closest<HTMLElement>('[data-sftp-entry-dir="true"][data-sftp-entry-path]');
    const targetPath = hit?.dataset.sftpEntryPath;
    if (!targetPath) return null;
    const movable = sourcePaths.filter((path) => canMovePathToDirectory(path, targetPath));
    return movable.length ? { path: targetPath, count: movable.length } : null;
  }, []);

  const clearDragState = useCallback(() => {
    draggedPathsRef.current = [];
    pointerDragRef.current = null;
    setDropTarget(null);
  }, [setDropTarget]);

  const beginPointerDrag = useCallback((path: string, event: React.PointerEvent<HTMLElement>) => {
    if (event.button !== 0 || event.shiftKey || event.ctrlKey || event.metaKey) return;
    const sel = selectedRef.current;
    const sourcePaths = sel.includes(path) ? sel : [path];
    pointerDragRef.current = {
      dragging: false,
      pointerId: event.pointerId,
      sourcePath: path,
      sourcePaths,
      startX: event.clientX,
      startY: event.clientY,
    };
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // Pointer capture is unavailable in jsdom and may fail if the pointer is already released.
    }
  }, []);

  const updatePointerDrag = useCallback(
    (event: React.PointerEvent<HTMLElement>) => {
      const drag = pointerDragRef.current;
      if (!drag || drag.pointerId !== event.pointerId) return;
      const distance = Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY);
      if (!drag.dragging && distance < 6) return;
      if (!drag.dragging) {
        drag.dragging = true;
        suppressNextClickRef.current = true;
        setSelected(drag.sourcePaths);
      }
      event.preventDefault();
      setDropTarget(getPointerDropTarget(event.clientX, event.clientY, drag.sourcePaths));
    },
    [getPointerDropTarget, setDropTarget, setSelected]
  );

  const endPointerDrag = useCallback(
    (event: React.PointerEvent<HTMLElement>) => {
      const drag = pointerDragRef.current;
      if (!drag || drag.pointerId !== event.pointerId) return;
      const target = drag.dragging ? getPointerDropTarget(event.clientX, event.clientY, drag.sourcePaths) : null;
      clearDragState();
      try {
        event.currentTarget.releasePointerCapture(event.pointerId);
      } catch {
        // Pointer capture is best-effort across browser/test environments.
      }
      if (!drag.dragging) return;
      event.preventDefault();
      event.stopPropagation();
      if (target) stableMoveEntries(drag.sourcePaths, target.path);
      window.setTimeout(() => {
        suppressNextClickRef.current = false;
      }, 0);
    },
    [clearDragState, getPointerDropTarget, stableMoveEntries]
  );

  // 所有行共用同一个 handlers 对象:每行 26 个 prop 时,5000 行全部改选中(shift 全选)
  // 要多付一遍 26×5000 的浅比较和 props 分配,反而比不 memo 更慢。收成一个恒定标识的
  // 对象后,行的 prop 面只剩 entry + 4 个布尔 + h。
  const rowHandlers = useMemo(
    () => ({
      canExternalEdit: stableCanExternalEdit,
      onExternalOpen: stableExternalOpen,
      onNavigate: stableNavigate,
      onOpenContextMenu: stableOpenContextMenu,
      onMoveEntriesToDirectory: stableMoveEntries,
      onRenameCancel: stableRenameCancel,
      onToggleExpand: stableToggleExpand,
      collapseLabel: t("sftp.tree.collapse"),
      expandLabel: t("sftp.tree.expand"),
      commitRename,
      selectEntry,
      maybeStartSlowRename,
      selectedRef,
      setSelected,
      draggedPathsRef,
      suppressNextClickRef,
      getMovableDragPaths,
      setDropTarget,
      clearDragState,
      beginPointerDrag,
      updatePointerDrag,
      endPointerDrag,
    }),
    [
      beginPointerDrag,
      clearDragState,
      commitRename,
      endPointerDrag,
      getMovableDragPaths,
      maybeStartSlowRename,
      selectEntry,
      setDropTarget,
      setSelected,
      stableCanExternalEdit,
      stableExternalOpen,
      stableMoveEntries,
      stableNavigate,
      stableOpenContextMenu,
      stableRenameCancel,
      stableToggleExpand,
      t,
      updatePointerDrag,
    ]
  );

  return (
    <ScrollArea
      className="flex-1 min-h-0"
      onClick={(e) => {
        if (!isEntryTarget(e.target)) setSelected([]);
      }}
      onContextMenu={(e) => {
        if (isEntryTarget(e.target)) return;
        e.preventDefault();
        onOpenContextMenu(e.clientX, e.clientY, null, null);
      }}
    >
      <div
        ref={containerRef}
        className="text-xs select-none min-h-full"
        onMouseOver={handleRowHover}
        onMouseLeave={clearRowHover}
      >
        {loading && (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          </div>
        )}
        {error && !loading && (
          <div className="flex flex-col items-center justify-center py-8 gap-1 px-2">
            <span className="text-destructive text-center text-xs">{t("sftp.loadError")}</span>
            <span className="text-muted-foreground text-center break-all text-[10px]">{error}</span>
            <Button variant="outline" size="xs" onClick={onRetry} className="mt-1">
              {t("sftp.retry")}
            </Button>
          </div>
        )}
        {!loading && !error && displayRows.length === 0 && (
          <div className="flex items-center justify-center py-8">
            <span className="text-muted-foreground">{t("sftp.empty")}</span>
          </div>
        )}
        {!loading && !error && (
          <>
            {currentPath !== "/" && (
              <div
                data-sftp-entry-row="true"
                data-sftp-entry-dir="true"
                data-sftp-entry-path={getParentPath(currentPath)}
                className={cn(
                  "flex items-center gap-1.5 px-2 py-1 cursor-pointer hover:bg-muted/50",
                  dropTarget?.path === getParentPath(currentPath) && "bg-primary/10 ring-1 ring-primary/30"
                )}
                onDoubleClick={onGoUp}
              >
                <Folder className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                <span className="flex-1 truncate">..</span>
                {dropTarget?.path === getParentPath(currentPath) && (
                  <DropHint
                    count={dropTarget.count}
                    target={getPathBaseName(getParentPath(currentPath)) || getParentPath(currentPath)}
                  />
                )}
              </div>
            )}
            {renderRows.map(({ row, index }) =>
              isSftpTreeEntryRow(row) ? (
                <FileRow
                  key={`entry:${row.path}`}
                  row={row}
                  index={index}
                  isSelected={selectedSet.has(row.path)}
                  isCut={clipboardCutPaths.has(row.path)}
                  isRenaming={renamePath === row.path}
                  isLineage={lineagePaths.has(row.path)}
                  dropTarget={dropTarget}
                  panelWidth={panelWidth}
                  h={rowHandlers}
                />
              ) : (
                <TreeStatusRow
                  key={`${row.state}:${row.path}`}
                  row={row}
                  panelWidth={panelWidth}
                  onRetry={stableRetryExpand}
                />
              )
            )}
          </>
        )}
      </div>
    </ScrollArea>
  );
}

/**
 * 落点上的动作说明:高亮只说"放这里",还要说清这一放会执行什么 —— 搬几个条目、搬去哪个目录。
 * 折叠链上它跟着被命中的那一段走,因此链里也能看出目标是哪一级。
 */
function DropHint({ count, target }: { count: number; target: string }) {
  const { t } = useTranslation();
  return (
    <span className="shrink-0 text-[10px] text-primary" data-testid="sftp-drop-hint">
      {t("sftp.tree.dropHint", { count, target })}
    </span>
  );
}

/** 折叠链上任意一段都是独立的落点;单段行退化为长度 1 的数组,渲染路径与之前一致。 */
function segmentsOf(row: SftpTreeDisplayRow): SftpTreeChainSegment[] {
  return (
    row.chain ?? [
      {
        path: row.path,
        name: row.name,
        entry: row.entry as sftp_svc.FileEntry,
        expanded: row.expanded,
        loading: row.loading,
      },
    ]
  );
}

interface FileRowHandlers {
  canExternalEdit: (entry: sftp_svc.FileEntry) => boolean;
  onExternalOpen: (path: string) => void;
  onNavigate: (path: string) => void;
  onOpenContextMenu: (x: number, y: number, entry: sftp_svc.FileEntry | null, entryPath: string | null) => void;
  onMoveEntriesToDirectory: (sourcePaths: string[], targetDirPath: string) => void;
  onRenameCancel: () => void;
  onToggleExpand: (dirPath: string) => void;
  collapseLabel: string;
  expandLabel: string;
  commitRename: (nextName: string) => void;
  selectEntry: (path: string, index: number, event: React.MouseEvent) => void;
  maybeStartSlowRename: (path: string, index: number, eventTime: number) => void;
  selectedRef: React.RefObject<string[]>;
  setSelected: (next: string[] | ((prev: string[]) => string[])) => void;
  draggedPathsRef: React.RefObject<string[]>;
  suppressNextClickRef: React.RefObject<boolean>;
  getMovableDragPaths: (event: React.DragEvent, targetDirPath: string) => string[];
  setDropTarget: (target: { path: string; count: number } | null) => void;
  clearDragState: () => void;
  beginPointerDrag: (path: string, event: React.PointerEvent<HTMLElement>) => void;
  updatePointerDrag: (event: React.PointerEvent<HTMLElement>) => void;
  endPointerDrag: (event: React.PointerEvent<HTMLElement>) => void;
}

interface FileRowProps {
  row: SftpTreeDisplayRow & { entry: sftp_svc.FileEntry; state: "entry" };
  index: number;
  isSelected: boolean;
  isCut: boolean;
  isRenaming: boolean;
  /** 自己被选中,或它是某个选中行的祖先 —— 参考线与目录行一起高亮,深层也能看出挂在谁下面。 */
  isLineage: boolean;
  dropTarget: { path: string; count: number } | null;
  panelWidth: number;
  h: FileRowHandlers;
}

// 单行 memo 化:选中态是列表级 state,不 memo 的话点一行会把整个目录的行全部重渲。
// 5000 个文件的目录实测单击一次 631ms、shift 全选 1.6s(见 PR 说明)。所有需要
// "当前选中集合"的回调都通过 ref 读,保证 props 在选中变化时保持同一标识。
const FileRow = memo(function FileRow({
  row,
  index,
  isSelected,
  isCut,
  isRenaming,
  isLineage,
  dropTarget,
  panelWidth,
  h,
}: FileRowProps) {
  const entry = row.entry;
  const fullPath = row.path;
  // 折叠链上的每一段各自是一个可展开/换根的落点;普通行退化为长度 1 的数组,渲染路径不变。
  const segments = segmentsOf(row);
  const isDropTarget = !!dropTarget && segments.some((segment) => segment.path === dropTarget.path);
  const showsDate = !entry.isDir && panelWidth >= DATE_COLUMN_MIN_PANEL_PX;
  const chromePx = showsDate ? ROW_CHROME_PX : ROW_CHROME_PX - DATE_COLUMN_PX;
  return (
    <div
      data-sftp-entry-row="true"
      data-sftp-entry-dir={entry.isDir ? "true" : "false"}
      data-sftp-entry-path={fullPath}
      data-sftp-depth={row.depth}
      data-sftp-lineage={isLineage || undefined}
      draggable={false}
      style={{
        ...indentStyle(row.depth, panelWidth),
        contentVisibility: "auto",
        containIntrinsicSize: "auto 28px",
      }}
      className={cn(
        "group/row relative flex items-center gap-1.5 pr-2 py-1 cursor-pointer transition-colors rounded-sm",
        isSelected ? "bg-primary/15 text-primary" : "hover:bg-muted/50",
        // 悬停祖先链只有 CSS 认得(属性由容器的委托事件就地打上,不经过 React 渲染)。
        !isSelected && "data-[sftp-hover-lineage=true]:bg-muted/40",
        !isSelected && isLineage && "bg-muted/40",
        isCut && "opacity-45",
        // 折叠链的高亮落在具体命中的那一段上(见下方 segments.map);普通行仍是整行高亮。
        segments.length === 1 && isDropTarget && "bg-primary/10 ring-1 ring-primary/30"
      )}
      onDragStart={(e) => {
        if (isRenaming) {
          e.preventDefault();
          return;
        }
        const { selectedRef, draggedPathsRef } = h;
        const sel = selectedRef.current;
        const paths = sel.includes(fullPath) ? sel : [fullPath];
        draggedPathsRef.current = paths;
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("application/x-opskat-sftp-paths", JSON.stringify(paths));
        e.dataTransfer.setData("text/plain", fullPath);
        if (!sel.includes(fullPath)) h.setSelected([fullPath]);
      }}
      onDragEnd={h.clearDragState}
      onDragOver={(e) => {
        const targetPath = resolveDropTargetPath(e.target);
        const movable = targetPath ? h.getMovableDragPaths(e, targetPath) : [];
        if (!targetPath || !movable.length) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        h.setDropTarget({ path: targetPath, count: movable.length });
      }}
      onDragLeave={(e) => {
        const nextTarget = e.relatedTarget;
        if (nextTarget instanceof Node && e.currentTarget.contains(nextTarget)) return;
        if (isDropTarget) h.setDropTarget(null);
      }}
      onDrop={(e) => {
        const targetPath = resolveDropTargetPath(e.target);
        const sourcePaths = targetPath ? h.getMovableDragPaths(e, targetPath) : [];
        if (!targetPath || !sourcePaths.length) return;
        e.preventDefault();
        e.stopPropagation();
        h.clearDragState();
        h.onMoveEntriesToDirectory(sourcePaths, targetPath);
      }}
      onPointerDown={(e) => {
        if (!isRenaming) h.beginPointerDrag(fullPath, e);
      }}
      onPointerMove={h.updatePointerDrag}
      onPointerUp={h.endPointerDrag}
      onPointerCancel={h.clearDragState}
      onClick={(e) => {
        const { suppressNextClickRef } = h;
        if (suppressNextClickRef.current) {
          suppressNextClickRef.current = false;
          return;
        }
        if (isRenaming) return;
        h.selectEntry(fullPath, index, e);
        h.maybeStartSlowRename(fullPath, index, e.timeStamp);
      }}
      onDoubleClick={() => {
        // 链上非末段自己在 segments.map 里已经 stopPropagation 并换根;这里只兜底末段
        // (含普通单段行)与文件的双击 —— 与折叠前的行为完全一致。
        if (isRenaming) return;
        if (entry.isDir) {
          h.onNavigate(fullPath);
          return;
        }
        if (h.canExternalEdit?.(entry)) {
          h.onExternalOpen?.(fullPath);
        }
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
        if (!h.selectedRef.current.includes(fullPath)) h.setSelected([fullPath]);
        h.onOpenContextMenu(e.clientX, e.clientY, entry, fullPath);
      }}
    >
      <TreeGuides depth={row.depth} panelWidth={panelWidth} />
      {segments.map((segment, i) => {
        const isLast = i === segments.length - 1;
        return (
          <span
            key={segment.path}
            data-sftp-entry-dir={segment.entry.isDir ? "true" : "false"}
            data-sftp-entry-path={segment.path}
            className={cn(
              "flex items-center gap-1 min-w-0",
              isLast ? "flex-1" : "shrink-0",
              // 单段行的高亮走外层整行(见上面 className);多段链上每一段各自的落点单独高亮,
              // 含最后一段 —— 否则拖到链最深的目录时反而看不出命中了哪。
              segments.length > 1 &&
                dropTarget?.path === segment.path &&
                "bg-primary/10 ring-1 ring-primary/30 rounded-sm"
            )}
          >
            {segment.entry.isDir ? (
              <button
                type="button"
                aria-label={segment.expanded ? h.collapseLabel : h.expandLabel}
                data-testid={`sftp-expand-${segment.path}`}
                className="flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground"
                onPointerDown={(e) => e.stopPropagation()}
                onDoubleClick={(e) => e.stopPropagation()}
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  h.onToggleExpand(segment.path);
                }}
              >
                {segment.loading ? (
                  <Loader2 className="h-3 w-3 animate-spin" />
                ) : segment.expanded ? (
                  <ChevronDown className="h-3 w-3" />
                ) : (
                  <ChevronRight className="h-3 w-3" />
                )}
              </button>
            ) : (
              <span className="w-3.5 shrink-0" />
            )}
            {i === 0 &&
              (entry.isDir ? (
                <Folder className="h-3.5 w-3.5 text-primary/70 shrink-0" />
              ) : (
                <File className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
              ))}
            {isRenaming && isLast ? (
              <RenameInput
                key={fullPath}
                initialName={segment.name}
                onCommit={h.commitRename}
                onCancel={h.onRenameCancel}
              />
            ) : (
              <span
                className={isLast ? "flex-1 truncate" : "shrink-0"}
                title={segment.name}
                onDoubleClick={
                  isLast
                    ? undefined
                    : (e) => {
                        // 非末段独立换根:阻断冒泡,否则外层双击会用末段路径覆盖这里的选择。
                        e.stopPropagation();
                        h.onNavigate(segment.path);
                      }
                }
              >
                {middleEllipsisName(segment.name, row.depth, panelWidth, chromePx)}
              </span>
            )}
            {dropTarget?.path === segment.path && <DropHint count={dropTarget.count} target={segment.name} />}
            {!isLast && <span className="text-muted-foreground/60 shrink-0">/</span>}
          </span>
        );
      })}
      {/* 目录行不带大小也不带日期:名字宽度在深层最稀缺,而目录的修改时间对定位最没帮助。 */}
      {!entry.isDir && (
        <>
          <span className="text-muted-foreground shrink-0 text-[10px]">{formatBytes(entry.size)}</span>
          {showsDate && <span className="text-muted-foreground shrink-0 text-[10px]">{formatDate(entry.modTime)}</span>}
        </>
      )}
    </div>
  );
});

interface TreeStatusRowProps {
  row: SftpTreeRow;
  panelWidth: number;
  onRetry: (dirPath: string) => void;
}

/** 展开目录的子层状态行:把「加载中 / 空目录 / 加载失败」摆在该目录下方,彼此可区分。 */
function TreeStatusRow({ row, panelWidth, onRetry }: TreeStatusRowProps) {
  const { t } = useTranslation();
  return (
    <div
      data-sftp-depth={row.depth}
      style={indentStyle(row.depth, panelWidth)}
      className="group/row relative flex items-center gap-1.5 pr-2 py-1 text-muted-foreground"
    >
      <TreeGuides depth={row.depth} panelWidth={panelWidth} />
      <span className="w-3.5 shrink-0" />
      {row.state === "loading" && (
        <>
          <Loader2 className="h-3 w-3 shrink-0 animate-spin" />
          <span className="truncate">{t("sftp.tree.loading")}</span>
        </>
      )}
      {row.state === "empty" && <span className="truncate">{t("sftp.empty")}</span>}
      {row.state === "error" && (
        <>
          <span className="text-destructive shrink-0">{t("sftp.loadError")}</span>
          <span className="min-w-0 flex-1 truncate text-[10px]" title={row.message ?? undefined}>
            {row.message}
          </span>
          <Button variant="outline" size="xs" className="shrink-0" onClick={() => onRetry(row.path)}>
            {t("sftp.retry")}
          </Button>
        </>
      )}
    </div>
  );
}
