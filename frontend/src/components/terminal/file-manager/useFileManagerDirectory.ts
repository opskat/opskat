import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SFTPListDir } from "../../../../wailsjs/go/ssh/SSH";
import { sftp_svc } from "../../../../wailsjs/go/models";
import { addExpandedPath, flattenSftpTree, type SftpTreeNode } from "@/lib/sftpDirTree";
import { useSFTPStore } from "@/stores/sftpStore";
import { normalizeRemotePath } from "./utils";

const EMPTY_ENTRIES: sftp_svc.FileEntry[] = [];

function loadedNode(entries: sftp_svc.FileEntry[]): SftpTreeNode {
  return { entries, loaded: true, loading: false, error: null };
}

export function useFileManagerDirectory(tabId: string, sessionId: string) {
  const storedPath = useSFTPStore((s) => s.fileManagerPaths[tabId]);
  const currentPath = storedPath || "/";
  const setCurrentPath = useSFTPStore((s) => s.setFileManagerPath);
  const [pathInput, setPathInput] = useState(currentPath);
  // 每个目录一份条目缓存:根层由 loadDir 写入,子层由 fetchLayer 懒填。
  const [tree, setTree] = useState<Record<string, SftpTreeNode>>({});
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelectedState] = useState<string[]>([]);
  const loadRequestRef = useRef(0);
  // 按目录记请求号:后台刷新与手动展开可能并发,只有最后一次请求的结果能落库。
  const layerRequestRef = useRef(new Map<string, number>());
  const currentPathRef = useRef(currentPath);
  const expandedRef = useRef(expanded);

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
        setError(String(e));
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
              error: String(e),
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

  /** 刷新:丢掉每个目录的条目缓存并重拉根层,已展开层随后由下面的补拉 effect 重新请求。 */
  const refreshTree = useCallback(async () => {
    setTree({});
    await loadDir(currentPathRef.current);
  }, [loadDir]);

  const setSelected = useCallback((next: string[] | ((prev: string[]) => string[])) => {
    setSelectedState(next);
  }, []);

  // currentPath 变化时重置路径输入框:渲染期对比上次值,替代 effect 里的同步 setState
  const [prevPath, setPrevPath] = useState(currentPath);
  if (currentPath !== prevPath) {
    setPrevPath(currentPath);
    setPathInput(currentPath);
  }

  useEffect(() => {
    currentPathRef.current = currentPath;
  }, [currentPath]);

  const rows = useMemo(() => flattenSftpTree(tree, expanded, currentPath), [currentPath, expanded, tree]);

  // 展开态记忆比缓存活得久(刷新清空缓存、换根后才重新可见):补拉这些「记得展开、
  // 但这一轮还没有条目」的层,否则它们会停在加载中占位上等一个永远不会发出的请求。
  useEffect(() => {
    for (const row of rows) {
      if (row.state === "loading" && !tree[row.path]) void fetchLayer(row.path);
    }
  }, [fetchLayer, rows, tree]);

  return {
    currentPath,
    currentPathRef,
    error,
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
  };
}
