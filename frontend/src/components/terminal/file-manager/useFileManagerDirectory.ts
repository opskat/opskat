import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SFTPListDir } from "../../../../wailsjs/go/ssh/SSH";
import { sftp_svc } from "../../../../wailsjs/go/models";
import {
  addExpandedPath,
  flattenFolderTree,
  flattenSftpTree,
  folderRevealPaths,
  staleRevealLayers,
  type SftpTreeNode,
} from "@/lib/sftpDirTree";
import { useSFTPStore } from "@/stores/sftpStore";
import { normalizeRemotePath } from "./utils";
import { errorMessage } from "@/lib/errorMessage";

const EMPTY_ENTRIES: sftp_svc.FileEntry[] = [];

interface RefreshTreeOptions {
  nextPath?: string;
  removedPaths?: string[];
}

function loadedNode(entries: sftp_svc.FileEntry[]): SftpTreeNode {
  return { entries, loaded: true, loading: false, error: null };
}

export function useFileManagerDirectory(tabId: string, sessionId: string) {
  const storedPath = useSFTPStore((s) => s.fileManagerPaths[tabId]);
  const currentPath = storedPath || "/";
  const setCurrentPath = useSFTPStore((s) => s.setFileManagerPath);
  const folderTreeVisible = useSFTPStore((s) => s.folderTreeVisible);
  const [pathInput, setPathInput] = useState(currentPath);
  // 每个目录一份条目缓存:根层由 loadDir 写入,子层由 fetchLayer 懒填。
  const [tree, setTree] = useState<Record<string, SftpTreeNode>>({});
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  // 目录树的展开集合：与列表的 expanded 独立，只共用上面的条目缓存。一开始就展开到当前目录。
  const [folderExpanded, setFolderExpanded] = useState<Set<string>>(() => new Set(folderRevealPaths(currentPath)));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelectedState] = useState<string[]>([]);
  const loadRequestRef = useRef(0);
  // 按目录记请求号:后台刷新与手动展开可能并发,只有最后一次请求的结果能落库。
  const layerRequestRef = useRef(new Map<string, number>());
  const currentPathRef = useRef(currentPath);
  const expandedRef = useRef(expanded);
  const folderExpandedRef = useRef(folderExpanded);
  // 屏幕上正显示着条目的目录层(见 refreshTree),随渲染行更新。
  const visibleLayersRef = useRef<Set<string>>(new Set());
  // 定位当前目录时已为它补拉过的过期祖先层(见 staleRevealLayers),换当前目录时清空。
  const revealRefetchRef = useRef({ path: "", layers: new Set<string>() });

  const loadDir = useCallback(
    async (dirPath: string) => {
      const requestId = ++loadRequestRef.current;
      const normalizedPath = normalizeRemotePath(currentPathRef.current, dirPath);
      setLoading(true);
      setError(null);
      setSelectedState([]);
      try {
        const result = await SFTPListDir(sessionId, normalizedPath);
        if (requestId !== loadRequestRef.current) return false;
        setTree((prev) => ({ ...prev, [normalizedPath]: loadedNode(result || []) }));
        setCurrentPath(tabId, normalizedPath);
        setPathInput(normalizedPath);
        return true;
      } catch (e) {
        if (requestId !== loadRequestRef.current) return false;
        setError(errorMessage(e));
        return false;
      } finally {
        if (requestId === loadRequestRef.current) {
          setLoading(false);
        }
      }
    },
    [sessionId, setCurrentPath, tabId]
  );

  /** 拉取某个子层:已有缓存时保留条目并只标记后台刷新,失败时把原因留在该层上。 */
  const fetchLayer = useCallback(
    async (dirPath: string) => {
      const requestId = (layerRequestRef.current.get(dirPath) ?? 0) + 1;
      layerRequestRef.current.set(dirPath, requestId);
      setTree((prev) => {
        const node = prev[dirPath];
        return {
          ...prev,
          [dirPath]: {
            entries: node?.entries ?? EMPTY_ENTRIES,
            loaded: node?.loaded ?? false,
            loading: true,
            error: null,
          },
        };
      });
      try {
        const result = await SFTPListDir(sessionId, dirPath);
        if (layerRequestRef.current.get(dirPath) !== requestId) return;
        setTree((prev) => ({ ...prev, [dirPath]: loadedNode(result || []) }));
      } catch (e) {
        if (layerRequestRef.current.get(dirPath) !== requestId) return;
        setTree((prev) => {
          const node = prev[dirPath];
          return {
            ...prev,
            [dirPath]: {
              entries: node?.entries ?? EMPTY_ENTRIES,
              loaded: node?.loaded ?? false,
              loading: false,
              error: errorMessage(e),
            },
          };
        });
      }
    },
    [sessionId]
  );

  const setExpandedPaths = useCallback((next: Set<string>) => {
    expandedRef.current = next;
    setExpanded(next);
  }, []);

  /** 展开:先渲染既有缓存,同时后台刷新该层;收起只丢展开态,缓存留到下次展开。 */
  const toggleExpand = useCallback(
    (dirPath: string) => {
      if (expandedRef.current.has(dirPath)) {
        const next = new Set(expandedRef.current);
        next.delete(dirPath);
        setExpandedPaths(next);
        return;
      }
      setExpandedPaths(addExpandedPath(expandedRef.current, dirPath));
      void fetchLayer(dirPath);
    },
    [fetchLayer, setExpandedPaths]
  );

  /** 目录树的展开/收起：语义同 toggleExpand，但只动目录树自己的展开集合。 */
  const toggleFolderExpand = useCallback(
    (dirPath: string) => {
      if (folderExpandedRef.current.has(dirPath)) {
        const next = new Set(folderExpandedRef.current);
        next.delete(dirPath);
        folderExpandedRef.current = next;
        setFolderExpanded(next);
        return;
      }
      const next = addExpandedPath(folderExpandedRef.current, dirPath);
      folderExpandedRef.current = next;
      setFolderExpanded(next);
      void fetchLayer(dirPath);
    },
    [fetchLayer]
  );

  /**
   * 刷新:使所有目录的条目缓存失效。屏幕上正显示着的层(列表与目录树里展开着的目录)保留旧条目
   * 并在后台重拉——否则每次移动/删除/重命名之后,目录树都会先塌成只剩 / 再逐层长回来;其余层
   * 直接丢弃,下次展开时重新请求。传入 nextPath 时刷新后落到那个目录:当前目录自己被删/被改名
   * 后,原路径已不存在。removedPaths 是刚被删除/改名/移走的路径:它们及其下层已不存在,同样丢弃、
   * 不再重拉——否则每个展开着的被删目录都会白发一次注定失败的请求。
   */
  const refreshTree = useCallback(
    async ({ nextPath, removedPaths = [] }: RefreshTreeOptions = {}) => {
      const target = nextPath ?? currentPathRef.current;
      const isRemoved = (path: string) =>
        removedPaths.some((removed) => path === removed || path.startsWith(`${removed}/`));
      const visible = [...visibleLayersRef.current].filter((path) => !isRemoved(path));
      setTree((prev) => {
        const kept: Record<string, SftpTreeNode> = {};
        for (const path of visible) if (prev[path]) kept[path] = prev[path];
        return kept;
      });
      for (const path of visible) if (path !== target) void fetchLayer(path);
      return loadDir(target);
    },
    [fetchLayer, loadDir]
  );

  const setSelected = useCallback((next: string[] | ((prev: string[]) => string[])) => {
    setSelectedState(next);
  }, []);

  // currentPath 变化时重置路径输入框:渲染期对比上次值,替代 effect 里的同步 setState
  // 同一处顺带把当前目录的祖先并进目录树的展开集合(只增不减,不收起用户展开的其它分支)。
  const [prevPath, setPrevPath] = useState(currentPath);
  if (currentPath !== prevPath) {
    setPrevPath(currentPath);
    setPathInput(currentPath);
    let next = folderExpanded;
    for (const path of folderRevealPaths(currentPath)) {
      if (!next.has(path)) next = addExpandedPath(next, path);
    }
    if (next !== folderExpanded) setFolderExpanded(next);
  }

  useEffect(() => {
    currentPathRef.current = currentPath;
  }, [currentPath]);

  useEffect(() => {
    folderExpandedRef.current = folderExpanded;
  }, [folderExpanded]);

  const rows = useMemo(() => flattenSftpTree(tree, expanded, currentPath), [currentPath, expanded, tree]);
  // 目录树显示着、且面板真正载入过当前目录(打开着、会话可用)之后,目录树才开始自己拉层;
  // 否则树被收起、面板收起或会话未就绪时也会对着远端发请求。
  const folderTreeActive = folderTreeVisible && !!tree[currentPath];
  const folderRows = useMemo(() => flattenFolderTree(tree, folderExpanded), [folderExpanded, tree]);

  useEffect(() => {
    const layers = new Set<string>([currentPath]);
    for (const row of rows) if (row.state === "entry" && row.expanded) layers.add(row.path);
    if (folderTreeVisible) for (const row of folderRows) if (row.state === "dir" && row.expanded) layers.add(row.path);
    visibleLayersRef.current = layers;
  }, [currentPath, folderRows, folderTreeVisible, rows]);

  // 展开态记忆比缓存活得久(刷新清空缓存、换根后才重新可见):补拉这些「记得展开、
  // 但这一轮还没有条目」的层,否则它们会停在加载中占位上等一个永远不会发出的请求。
  // 目录树另外把定位当前目录要经过的每一级祖先一起并行拉取,不等父层到了再逐级出现
  // 占位行——深层目录(或刷新清空缓存后)不必按深度串行往返。几个来源合并去重:同一轮
  // 提交里 fetchLayer 写入的加载态还看不到,分开遍历会对同一目录重复请求。
  useEffect(() => {
    const pending = new Set<string>();
    for (const row of rows) if (row.state === "loading") pending.add(row.path);
    if (folderTreeActive) {
      for (const row of folderRows) if (row.state === "loading") pending.add(row.path);
      for (const path of folderRevealPaths(currentPath)) pending.add(path);
    }
    for (const path of pending) if (!tree[path]) void fetchLayer(path);
    // 缓存早于远端新建的祖先层:每进入一次当前目录只补拉一次——重拉后仍不含它(比如经由未列出的
    // 路径进入)时不再反复请求。树收起期间换目录也要清空,否则回到原目录时沿用上一次的记录。
    const checked = revealRefetchRef.current;
    if (checked.path !== currentPath) {
      checked.path = currentPath;
      checked.layers.clear();
    }
    if (!folderTreeActive) return;
    for (const path of staleRevealLayers(tree, currentPath)) {
      if (checked.layers.has(path)) continue;
      checked.layers.add(path);
      void fetchLayer(path);
    }
  }, [currentPath, fetchLayer, folderRows, folderTreeActive, rows, tree]);

  return {
    currentPath,
    currentPathRef,
    error,
    folderRows,
    loading,
    loadDir,
    pathInput,
    refreshTree,
    retryExpand: fetchLayer,
    rows,
    selected,
    setError,
    setPathInput,
    setSelected,
    storedPath,
    toggleExpand,
    toggleFolderExpand,
  };
}
