import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, FolderPlus, FolderUp, Upload } from "lucide-react";
import { toast } from "sonner";
import { notifyCopied } from "@/lib/notify";
import {
  Button,
  cn,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@opskat/ui";
import { SFTPCreateFile, SFTPDelete, SFTPGetwd, SFTPMkdir, SFTPPaste, SFTPRename } from "../../../wailsjs/go/ssh/SSH";
import { sftp_svc } from "../../../wailsjs/go/models";
import { openExternalEdit, type ExternalEditMergePrepareResult, type ExternalEditSession } from "@/lib/externalEditApi";
import {
  buildExternalEditAttentionItems,
  isExternalEditClipboardResidueSession,
  useExternalEditStore,
} from "@/stores/externalEditStore";
import { useSFTPStore } from "@/stores/sftpStore";
import { ExternalEditCompareWorkbench } from "./external-edit/CompareWorkbench";
import { ExternalEditMergeWorkbench } from "./external-edit/MergeWorkbench";
import { ExternalEditPendingDialog, type ExternalEditPendingItem } from "./external-edit/PendingDialog";
import { FileList } from "./file-manager/FileList";
import { FloatingMenu } from "./file-manager/FloatingMenu";
import { NameDialog } from "./file-manager/NameDialog";
import { PathToolbar, type DirectorySyncMenuMode } from "./file-manager/PathToolbar";
import { PermissionDialog } from "./file-manager/PermissionDialog";
import { PropertiesDialog } from "./file-manager/PropertiesDialog";
import { TransferSection } from "./file-manager/TransferSection";
import { type ClipboardState, type DeleteTarget, type CtxMenuState, type PermissionTarget } from "./file-manager/types";
import { useFileManagerDirectory } from "./file-manager/useFileManagerDirectory";
import { useNativeFileDrop } from "./file-manager/useNativeFileDrop";
import { useResizeHandle } from "./file-manager/useResizeHandle";
import { useTerminalDirectorySync } from "./file-manager/useTerminalDirectorySync";
import {
  canMovePathToDirectory,
  formatBytes,
  getEntryPath,
  getParentPath,
  getPathBaseName,
  HANDLE_PX,
  joinRemotePath,
} from "./file-manager/utils";

interface FileManagerPanelProps {
  assetId?: number;
  tabId: string;
  sessionId: string;
  isActive?: boolean;
  isOpen: boolean;
  width: number;
  onWidthChange: (width: number) => void;
}

const EXTERNAL_EDIT_SAFE_ERROR_KEY = "externalEdit.error.safeActionFailed";
const EXTERNAL_EDIT_OVERSIZE_ERROR_KEY =
  "当前文件超过最大读取阈值，无法继续完整读取。请前往 设置 > External Edit 调整最大读取大小后再重试";

function isExternalEditOversizeError(error: unknown) {
  const message = error instanceof Error ? error.message : String(error);
  return (
    message.includes("远程文件过大") ||
    message.includes("本地副本过大") ||
    message.includes("读取过程中超过大小上限") ||
    message.includes("无法完整读取")
  );
}

let globalClipboard: ClipboardState | null = null;
const clipboardListeners = new Set<() => void>();
function setGlobalClipboard(next: ClipboardState | null) {
  globalClipboard = next;
  clipboardListeners.forEach((listener) => listener());
}
function useClipboardState() {
  const [clipboard, setClipboard] = useState(globalClipboard);
  useEffect(() => {
    const listener = () => setClipboard(globalClipboard);
    clipboardListeners.add(listener);
    return () => {
      clipboardListeners.delete(listener);
    };
  }, []);
  return clipboard;
}

export function FileManagerPanel({
  assetId,
  tabId,
  sessionId,
  isActive = true,
  isOpen,
  width,
  onWidthChange,
}: FileManagerPanelProps) {
  const { t } = useTranslation();
  const continueEditLabel = t("externalEdit.actions.continueEdit");
  const {
    currentPath,
    currentPathRef,
    entries,
    error,
    loading,
    loadDir,
    pathInput,
    selected,
    setError,
    setPathInput,
    setSelected,
    storedPath,
  } = useFileManagerDirectory(tabId, sessionId);

  const {
    directoryFollowMode,
    navigateToPath,
    paneConnected,
    sessionSync,
    syncPanelFromTerminal,
    syncTerminalToPath,
    toggleFollowMode,
  } = useTerminalDirectorySync({ currentPathRef, loadDir, sessionId, tabId });

  const [ctxMenu, setCtxMenu] = useState<CtxMenuState | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null);
  const [renamePath, setRenamePath] = useState<string | null>(null);
  const [permissionTarget, setPermissionTarget] = useState<PermissionTarget | null>(null);
  const [propertiesTarget, setPropertiesTarget] = useState<PermissionTarget | null>(null);
  const [nameDialog, setNameDialog] = useState<null | "file" | "folder">(null);
  const clipboard = useClipboardState();
  const loadedRef = useRef(false);
  const lastSessionRef = useRef<string | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const [mergePrepareErrors, setMergePrepareErrors] = useState<Record<string, string>>({});
  const [preparedMergeResult, setPreparedMergeResult] = useState<ExternalEditMergePrepareResult | null>(null);
  const [pendingDialogOpen, setPendingDialogOpen] = useState(false);
  const [activeSyncMode, setActiveSyncMode] = useState<DirectorySyncMenuMode>(null);

  const startUpload = useSFTPStore((s) => s.startUpload);
  const startUploadDir = useSFTPStore((s) => s.startUploadDir);
  const startUploadFile = useSFTPStore((s) => s.startUploadFile);
  const startDownload = useSFTPStore((s) => s.startDownload);
  const startDownloadDir = useSFTPStore((s) => s.startDownloadDir);
  const allTransfers = useSFTPStore((s) => s.transfers);
  const allExternalSessions = useExternalEditStore((s) => s.sessions);
  const pendingConflict = useExternalEditStore((s) => s.pendingConflict);
  const dismissCompare = useExternalEditStore((s) => s.dismissCompare);
  const dismissMerge = useExternalEditStore((s) => s.dismissMerge);
  const dismissErrorDetail = useExternalEditStore((s) => s.dismissErrorDetail);
  const compareResult = useExternalEditStore((s) => s.compareResult);
  const mergeResult = useExternalEditStore((s) => s.mergeResult);
  const selectedError = useExternalEditStore((s) => s.selectedError);
  const autoSavePhases = useExternalEditStore((s) => s.autoSavePhases);
  const openErrorDetail = useExternalEditStore((s) => s.openErrorDetail);
  const prepareMerge = useExternalEditStore((s) => s.prepareMerge);
  const resolveConflict = useExternalEditStore((s) => s.resolveConflict);
  const continuePendingSession = useExternalEditStore((s) => s.continuePendingSession);
  const savingSessionId = useExternalEditStore((s) => s.savingSessionId);
  const safePendingConflict = isExternalEditClipboardResidueSession(pendingConflict?.session) ? null : pendingConflict;
  const safeCompareResult = isExternalEditClipboardResidueSession(compareResult?.session) ? null : compareResult;
  const safeStoreMergeResult = isExternalEditClipboardResidueSession(mergeResult?.session) ? null : mergeResult;
  const safePreparedMergeResult = isExternalEditClipboardResidueSession(preparedMergeResult?.session)
    ? null
    : preparedMergeResult;
  const safeMergeResult = safeStoreMergeResult || safePreparedMergeResult;
  const safeSelectedError = isExternalEditClipboardResidueSession(selectedError) ? null : selectedError;

  const transferTarget = useMemo(() => ({ tabId, sessionId }), [tabId, sessionId]);
  const tabTransfers = useMemo(
    () => Object.values(allTransfers).filter((transfer) => transfer.tabId === tabId),
    [allTransfers, tabId]
  );
  const attentionItems = useMemo(
    () => buildExternalEditAttentionItems(allExternalSessions).filter((entry) => entry.session.assetId === assetId),
    [allExternalSessions, assetId]
  );
  const pendingItems = useMemo(() => {
    const items: ExternalEditPendingItem[] = [...attentionItems];
    const pendingSession = safePendingConflict?.session;
    if (pendingSession && pendingSession.assetId === assetId) {
      const runtimeType =
        safePendingConflict?.status === "remote_missing"
          ? "remote_missing"
          : safePendingConflict?.status === "conflict_remote_changed"
            ? "conflict"
            : null;
      if (!runtimeType) {
        return items.filter((item) => !isExternalEditClipboardResidueSession(item.session));
      }
      const decisionType = runtimeType === "remote_missing" ? undefined : runtimeType;
      const exists = items.some((item) => item.session.id === pendingSession.id && item.type === runtimeType);
      if (!exists) {
        items.unshift({
          id: `${runtimeType}:${pendingSession.id}`,
          type: runtimeType,
          session: pendingSession,
          decisionType,
          sourceType: "runtime",
        });
      }
    }
    return items.filter((item) => !isExternalEditClipboardResidueSession(item.session));
  }, [assetId, attentionItems, safePendingConflict]);
  const entryByPath = useMemo(() => {
    const map = new Map<string, sftp_svc.FileEntry>();
    for (const entry of entries) map.set(getEntryPath(currentPath, entry), entry);
    return map;
  }, [currentPath, entries]);
  const selectedEntries = useMemo(
    () => selected.map((path) => entryByPath.get(path)).filter(Boolean) as sftp_svc.FileEntry[],
    [entryByPath, selected]
  );
  const clipboardCutPaths = useMemo(() => {
    if (clipboard?.mode !== "cut") return new Set<string>();
    return new Set(clipboard.items.filter((item) => item.sessionId === sessionId).map((item) => item.path));
  }, [clipboard, sessionId]);

  const isDragOver = useNativeFileDrop({
    currentPathRef,
    isActive,
    isOpen,
    panelRef,
    sessionId,
    startUploadFile,
    tabId,
  });
  const { handleResizeStart, isResizing, outerRef } = useResizeHandle({ onWidthChange, panelRef, width });

  useEffect(() => {
    if (!sessionId) return;
    if (lastSessionRef.current !== sessionId) {
      lastSessionRef.current = sessionId;
      loadedRef.current = false;
    }
    if (!isOpen || loadedRef.current) return;
    loadedRef.current = true;
    if (directoryFollowMode === "always" && sessionSync?.cwdKnown && sessionSync.cwd) {
      void loadDir(sessionSync.cwd);
      return;
    }
    if (storedPath) {
      void loadDir(storedPath);
      return;
    }
    SFTPGetwd(sessionId)
      .then((home) => loadDir(home || "/"))
      .catch(() => loadDir("/"));
  }, [sessionId, isOpen, directoryFollowMode, sessionSync?.cwdKnown, sessionSync?.cwd, storedPath, loadDir]);

  useEffect(() => {
    if (!isOpen || directoryFollowMode !== "always") return;
    if (!sessionSync?.cwdKnown || !sessionSync.cwd) return;
    if (sessionSync.cwd === currentPath) return;
    void loadDir(sessionSync.cwd);
  }, [currentPath, directoryFollowMode, isOpen, loadDir, sessionSync?.cwd, sessionSync?.cwdKnown]);

  useEffect(() => {
    if (safePendingConflict) {
      setPendingDialogOpen(true);
    }
  }, [safePendingConflict]);

  const doneUploadCount = tabTransfers.filter(
    (transfer) => transfer.status === "done" && transfer.direction === "upload"
  ).length;
  const prevDoneCount = useRef(0);
  useEffect(() => {
    if (doneUploadCount > prevDoneCount.current) void loadDir(currentPathRef.current);
    prevDoneCount.current = doneUploadCount;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doneUploadCount]);

  useEffect(() => {
    const handler = (event: Event) => {
      const path = (event as CustomEvent<{ path: string }>).detail?.path;
      if (path) setRenamePath(path);
    };
    window.addEventListener("sftp:rename-request", handler);
    return () => window.removeEventListener("sftp:rename-request", handler);
  }, []);

  const getFullPath = useCallback((entry: sftp_svc.FileEntry) => getEntryPath(currentPath, entry), [currentPath]);
  const selectedItems = useCallback(() => {
    const paths = selected.length ? selected : [];
    return paths
      .map((path) => {
        const entry = entryByPath.get(path);
        if (!entry) return null;
        return { sessionId, path, name: entry.name, isDir: entry.isDir, size: entry.size };
      })
      .filter(Boolean) as ClipboardState["items"];
  }, [entryByPath, selected, sessionId]);

  const copyOrCut = useCallback(
    (mode: "copy" | "cut", paths?: string[]) => {
      const sourcePaths = paths ?? selected;
      const items = sourcePaths
        .map((path) => {
          const entry = entryByPath.get(path);
          if (!entry) return null;
          return { sessionId, path, name: entry.name, isDir: entry.isDir, size: entry.size };
        })
        .filter(Boolean) as ClipboardState["items"];
      if (!items.length) return;
      setGlobalClipboard({ mode, items });
    },
    [entryByPath, selected, sessionId]
  );

  const copyFilePaths = useCallback(
    (paths: string[]) => {
      if (!paths.length) return;
      void navigator.clipboard
        .writeText(paths.join("\n"))
        .then(() => notifyCopied(t("sftp.filePathCopied")))
        .catch((e) => toast.error(String(e)));
    },
    [t]
  );

  const paste = useCallback(async () => {
    if (!clipboard?.items.length) return;
    try {
      await SFTPPaste(
        new sftp_svc.PasteRequest({
          targetSessionId: sessionId,
          targetDir: currentPathRef.current,
          mode: clipboard.mode,
          items: clipboard.items,
        })
      );
      if (clipboard.mode === "cut") setGlobalClipboard(null);
      await loadDir(currentPathRef.current);
    } catch (e) {
      toast.error(String(e));
    }
  }, [clipboard, currentPathRef, loadDir, sessionId]);

  const goUp = useCallback(() => {
    if (currentPath === "/") return;
    void navigateToPath(getParentPath(currentPath));
  }, [currentPath, navigateToPath]);

  const goHome = useCallback(() => {
    SFTPGetwd(sessionId)
      .then((home) => navigateToPath(home || "/"))
      .catch(() => navigateToPath("/"));
  }, [navigateToPath, sessionId]);

  const handleSyncPanelFromTerminal = useCallback(async () => {
    const synced = await syncPanelFromTerminal();
    if (synced) setActiveSyncMode("panel-from-terminal");
  }, [syncPanelFromTerminal]);

  const handleSyncTerminalToCurrentPath = useCallback(async () => {
    const synced = await syncTerminalToPath(currentPathRef.current);
    if (synced) setActiveSyncMode("terminal-from-panel");
  }, [currentPathRef, syncTerminalToPath]);

  const handleFollowToggle = useCallback(async () => {
    await toggleFollowMode();
    setActiveSyncMode((current) => (directoryFollowMode === "always" ? null : current));
  }, [directoryFollowMode, toggleFollowMode]);

  const handleDelete = useCallback(async () => {
    if (!deleteTarget) return;
    try {
      for (const item of deleteTarget.paths) await SFTPDelete(sessionId, item.path, item.isDir);
      await loadDir(currentPathRef.current);
    } catch (e) {
      setError(String(e));
    } finally {
      setDeleteTarget(null);
    }
  }, [currentPathRef, deleteTarget, loadDir, sessionId, setError]);

  const canExternalEdit = useCallback((entry: sftp_svc.FileEntry) => !entry.isDir, []);

  const handleOpenExternalEdit = useCallback(
    async (remotePath: string) => {
      // 兼容旧调用方：只有终端页真正绑定资产后才允许进入外部编辑链路，
      // 这样可以让历史测试和非终端场景继续复用组件，而不需要把 assetId 适配带回测试侧。
      if (!assetId) {
        return;
      }
      try {
        await openExternalEdit({
          assetId,
          sessionId,
          remotePath,
        });
      } catch (error) {
        setError(isExternalEditOversizeError(error) ? EXTERNAL_EDIT_OVERSIZE_ERROR_KEY : String(error));
      }
    },
    [assetId, sessionId, setError]
  );

  const handlePrepareMerge = useCallback(
    async (session: ExternalEditSession) => {
      setMergePrepareErrors((current) => {
        const { [session.id]: _ignored, ...rest } = current;
        return rest;
      });
      try {
        const result = await prepareMerge(session.id);
        const acceptedResult = useExternalEditStore.getState().mergeResult;
        setPreparedMergeResult(
          acceptedResult?.primaryDraftSessionId === result.primaryDraftSessionId ? acceptedResult : null
        );
        return true;
      } catch {
        const safeMessage = t(EXTERNAL_EDIT_SAFE_ERROR_KEY);
        setError(safeMessage);
        setMergePrepareErrors((current) => ({ ...current, [session.id]: safeMessage }));
        return false;
      }
    },
    [prepareMerge, setError, t]
  );

  const handlePendingMerge = useCallback(
    async (session: ExternalEditSession) => {
      const opened = await handlePrepareMerge(session);
      if (opened) {
        setPendingDialogOpen(false);
      }
    },
    [handlePrepareMerge]
  );

  const handlePendingAcceptRemote = useCallback(
    async (session: ExternalEditSession) => {
      try {
        await resolveConflict(session.id, "reread");
      } catch {
        setError(t(EXTERNAL_EDIT_SAFE_ERROR_KEY));
      }
    },
    [resolveConflict, setError, t]
  );

  const handlePendingOverwrite = useCallback(
    async (session: ExternalEditSession) => {
      try {
        await resolveConflict(session.id, session.state === "remote_missing" ? "recreate" : "overwrite");
      } catch {
        setError(t(EXTERNAL_EDIT_SAFE_ERROR_KEY));
      }
    },
    [resolveConflict, setError, t]
  );

  const handlePendingContinueEdit = useCallback(
    async (session: ExternalEditSession, sourceType?: "runtime" | "recovery") => {
      try {
        await continuePendingSession(session.id, sourceType);
        setPendingDialogOpen((open) => {
          if (!open) return open;
          const latestSessions = useExternalEditStore.getState().sessions;
          const latestPendingConflict = useExternalEditStore.getState().pendingConflict;
          const latestAttentionItems = buildExternalEditAttentionItems(latestSessions).filter(
            (entry) => entry.session.assetId === assetId
          );
          const latestPendingSession = latestPendingConflict?.session;
          const latestRuntimeType =
            latestPendingConflict?.status === "remote_missing"
              ? "remote_missing"
              : latestPendingConflict?.status === "conflict_remote_changed"
                ? "conflict"
                : null;
          const hasRuntimeItem =
            !!latestPendingSession &&
            latestPendingSession.assetId === assetId &&
            latestRuntimeType !== null &&
            !latestAttentionItems.some(
              (item) => item.session.id === latestPendingSession.id && item.type === latestRuntimeType
            );
          return latestAttentionItems.length > 0 || hasRuntimeItem;
        });
      } catch {
        setError(t(EXTERNAL_EDIT_SAFE_ERROR_KEY));
      }
    },
    [assetId, continuePendingSession, setError, t]
  );

  const startRename = useCallback(
    (path?: string) => {
      const targetPath = path ?? selected[0];
      if (targetPath) setRenamePath(targetPath);
    },
    [selected]
  );

  const commitRename = useCallback(
    async (oldPath: string, nextName: string) => {
      const entry = entryByPath.get(oldPath);
      if (!entry || nextName === entry.name) {
        setRenamePath(null);
        return;
      }
      try {
        await SFTPRename(sessionId, oldPath, joinRemotePath(getParentPath(oldPath), nextName));
        setRenamePath(null);
        await loadDir(currentPathRef.current);
      } catch (e) {
        toast.error(String(e));
      }
    },
    [currentPathRef, entryByPath, loadDir, sessionId]
  );

  const moveEntriesToDirectory = useCallback(
    async (sourcePaths: string[], targetDirPath: string) => {
      const moves = sourcePaths
        .filter((path) => canMovePathToDirectory(path, targetDirPath))
        .map((path) => {
          const entry = entryByPath.get(path);
          const name = entry?.name || getPathBaseName(path);
          if (!name) return null;
          return {
            from: path,
            to: joinRemotePath(targetDirPath, name),
          };
        })
        .filter((item): item is { from: string; to: string } => !!item && item.from !== item.to);

      if (!moves.length) return;

      try {
        for (const move of moves) {
          await SFTPRename(sessionId, move.from, move.to);
        }
        setSelected([]);
        await loadDir(currentPathRef.current);
      } catch (e) {
        toast.error(String(e));
      }
    },
    [currentPathRef, entryByPath, loadDir, sessionId, setSelected]
  );

  const openPermission = useCallback(
    (path: string) => {
      const entry = entryByPath.get(path);
      if (entry) setPermissionTarget({ entry, path });
    },
    [entryByPath]
  );

  const openProperties = useCallback(
    (path: string) => {
      const entry = entryByPath.get(path);
      if (entry) setPropertiesTarget({ entry, path });
    },
    [entryByPath]
  );

  const handleCtxAction = useCallback(
    (action: string) => {
      if (!ctxMenu) return;
      const entry = ctxMenu.entry;
      const targetPath = entry ? getFullPath(entry) : selected[0];
      const multiPaths = selected.length > 1 ? selected : targetPath ? [targetPath] : [];
      setCtxMenu(null);
      switch (action) {
        case "open":
          if (entry?.isDir) void navigateToPath(getFullPath(entry));
          break;
        case "openTerminal":
          if (entry?.isDir) void syncTerminalToPath(getFullPath(entry));
          break;
        case "download":
          if (entry) startDownload(transferTarget, getFullPath(entry));
          break;
        case "externalEdit":
          if (entry) {
            void handleOpenExternalEdit(getFullPath(entry));
          }
          break;
        case "downloadDir":
          if (entry) startDownloadDir(transferTarget, getFullPath(entry));
          break;
        case "downloadSelected":
          selectedItems().forEach((item) =>
            item.isDir ? startDownloadDir(transferTarget, item.path) : startDownload(transferTarget, item.path)
          );
          break;
        case "cut":
        case "cutSelected":
          copyOrCut("cut", multiPaths);
          break;
        case "copy":
        case "copySelected":
          copyOrCut("copy", multiPaths);
          break;
        case "copyFilePath":
          if (targetPath) copyFilePaths([targetPath]);
          break;
        case "copySelectedFilePaths":
          copyFilePaths(multiPaths);
          break;
        case "paste":
          void paste();
          break;
        case "copyCurrentPath":
          void navigator.clipboard
            .writeText(currentPathRef.current)
            .then(() => notifyCopied(t("sftp.currentPathCopied")))
            .catch((e) => toast.error(String(e)));
          break;
        case "rename":
          if (targetPath) startRename(targetPath);
          break;
        case "permission":
          if (targetPath) openPermission(targetPath);
          break;
        case "properties":
          if (targetPath) openProperties(targetPath);
          break;
        case "upload":
          startUpload(transferTarget, currentPath.endsWith("/") ? currentPath : currentPath + "/");
          break;
        case "uploadDir":
          startUploadDir(transferTarget, currentPath.endsWith("/") ? currentPath : currentPath + "/");
          break;
        case "newFile":
          setNameDialog("file");
          break;
        case "newFolder":
          setNameDialog("folder");
          break;
        case "delete":
        case "deleteSelected":
          setDeleteTarget({
            paths: multiPaths
              .map((path) => {
                const item = entryByPath.get(path);
                return item ? { path, name: item.name, isDir: item.isDir } : null;
              })
              .filter(Boolean) as DeleteTarget["paths"],
          });
          break;
        case "refresh":
          void loadDir(currentPathRef.current);
          break;
      }
    },
    [
      ctxMenu,
      currentPath,
      currentPathRef,
      copyFilePaths,
      copyOrCut,
      entryByPath,
      handleOpenExternalEdit,
      getFullPath,
      loadDir,
      navigateToPath,
      openPermission,
      openProperties,
      paste,
      selected,
      selectedItems,
      startDownload,
      startDownloadDir,
      startRename,
      startUpload,
      startUploadDir,
      syncTerminalToPath,
      t,
      transferTarget,
    ]
  );

  const submitNewName = async (name: string) => {
    try {
      const path = joinRemotePath(currentPathRef.current, name);
      if (nameDialog === "file") await SFTPCreateFile(sessionId, path);
      if (nameDialog === "folder") await SFTPMkdir(sessionId, path);
      setNameDialog(null);
      await loadDir(currentPathRef.current);
    } catch (e) {
      toast.error(String(e));
    }
  };

  useEffect(() => {
    if (!isOpen || !isActive) return;
    const handleKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const editable = target?.tagName === "INPUT" || target?.tagName === "TEXTAREA" || target?.isContentEditable;
      if (editable) return;
      const key = e.key.toLowerCase();
      if ((e.ctrlKey || e.metaKey) && key === "c") {
        e.preventDefault();
        copyOrCut("copy");
      } else if ((e.ctrlKey || e.metaKey) && key === "x") {
        e.preventDefault();
        copyOrCut("cut");
      } else if ((e.ctrlKey || e.metaKey) && key === "v") {
        e.preventDefault();
        void paste();
      } else if (e.key === "F2") {
        e.preventDefault();
        startRename();
      } else if (e.key === "Delete" && selected.length) {
        e.preventDefault();
        setDeleteTarget({
          paths: selected
            .map((path) => {
              const item = entryByPath.get(path);
              return item ? { path, name: item.name, isDir: item.isDir } : null;
            })
            .filter(Boolean) as DeleteTarget["paths"],
        });
      }
    };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [copyOrCut, entryByPath, isActive, isOpen, paste, selected, startRename]);

  const totalWidth = width + HANDLE_PX;
  const selectedTotalSize = selectedEntries.reduce((sum, entry) => sum + (entry.isDir ? 0 : entry.size), 0);
  const uploadTargetDir = currentPath.endsWith("/") ? currentPath : currentPath + "/";

  return (
    <>
      <div
        ref={outerRef}
        className="shrink-0 overflow-hidden transition-[width] duration-200 ease-out"
        style={{ width: isOpen ? totalWidth : 0, pointerEvents: isOpen ? "auto" : "none" }}
      >
        <div className="flex h-full" style={{ minWidth: totalWidth }}>
          <div
            className={cn(
              "w-1 cursor-col-resize hover:bg-primary/20 transition-colors shrink-0",
              isResizing && "bg-primary/30"
            )}
            onMouseDown={handleResizeStart}
          />
          <div
            ref={panelRef}
            className="flex flex-col border-l bg-background relative overflow-hidden"
            style={{ width, "--wails-drop-target": isOpen ? "drop" : undefined } as CSSProperties}
          >
            {isDragOver && (
              <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-primary/5 border-2 border-dashed border-primary/30 rounded animate-in fade-in-0 duration-150">
                <div className="flex flex-col items-center gap-1 text-primary/60">
                  <Upload className="h-5 w-5" />
                  <span className="text-xs">{t("sftp.dropToUpload")}</span>
                </div>
              </div>
            )}
            <PathToolbar
              activeSyncMode={activeSyncMode}
              currentPath={currentPath}
              directoryFollowMode={directoryFollowMode}
              onFollowToggle={() => void handleFollowToggle()}
              onGoHome={goHome}
              onGoUp={goUp}
              onPathInputChange={setPathInput}
              onPathSubmit={(nextPath) => void navigateToPath(nextPath)}
              onRefresh={() => void loadDir(currentPathRef.current)}
              onSyncPanelFromTerminal={() => void handleSyncPanelFromTerminal()}
              onSyncTerminalToPath={() => void handleSyncTerminalToCurrentPath()}
              paneConnected={paneConnected}
              pathInput={pathInput}
            />

            {pendingItems.length > 0 && (
              <div className="border-b bg-amber-500/5 px-3 py-2">
                <Button
                  className="w-full justify-between"
                  data-testid="external-edit-pending-entry"
                  size="sm"
                  variant="outline"
                  onClick={() => setPendingDialogOpen(true)}
                >
                  <span className="flex items-center gap-2">
                    <AlertTriangle className="h-4 w-4 text-amber-500" />
                    {t("externalEdit.pending.entry")}
                  </span>
                  <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-xs text-amber-700 dark:text-amber-300">
                    {pendingItems.length}
                  </span>
                </Button>
              </div>
            )}

            <FileList
              canExternalEdit={canExternalEdit}
              clipboardCutPaths={clipboardCutPaths}
              currentPath={currentPath}
              entries={entries}
              error={error}
              loading={loading}
              onExternalOpen={handleOpenExternalEdit}
              onGoUp={goUp}
              onMoveEntriesToDirectory={moveEntriesToDirectory}
              onNavigate={(path) => void navigateToPath(path)}
              onOpenContextMenu={(x, y, entry) => {
                if (x < 0 || y < 0) return;
                if (!entry) {
                  setSelected([]);
                  setCtxMenu({ x, y, entry: null, selectedEntries: [] });
                  return;
                }
                const menuSelectedEntries = selected
                  .map((path) => entryByPath.get(path))
                  .filter(Boolean) as sftp_svc.FileEntry[];
                setCtxMenu({
                  x,
                  y,
                  entry,
                  canExternalEdit: canExternalEdit(entry),
                  selectedEntries: menuSelectedEntries,
                });
              }}
              onRenameCancel={() => setRenamePath(null)}
              onRenameCommit={commitRename}
              onRetry={() => void loadDir(currentPathRef.current)}
              renamePath={renamePath}
              selected={selected}
              setSelected={setSelected}
            />
            <div
              className="flex items-center justify-between gap-2 border-t px-2 py-1 text-[11px] text-muted-foreground"
              data-testid="sftp-status-bar"
            >
              <span className="min-w-0 truncate">
                {selected.length > 1
                  ? t("sftp.selectedItems", { count: selected.length, size: formatBytes(selectedTotalSize) })
                  : clipboard?.items.length
                    ? clipboard.mode === "cut"
                      ? t("sftp.clipboardCut", { count: clipboard.items.length })
                      : t("sftp.clipboardCopy", { count: clipboard.items.length })
                    : t("sftp.ready")}
              </span>
              <span className="flex shrink-0 items-center gap-0.5">
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="icon-xs" title={t("sftp.uploadTo")} aria-label={t("sftp.uploadTo")}>
                      <Upload className="h-3.5 w-3.5" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem onClick={() => void startUpload(transferTarget, uploadTargetDir)}>
                      <Upload className="h-4 w-4" />
                      {t("sftp.upload")}
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={() => void startUploadDir(transferTarget, uploadTargetDir)}>
                      <FolderUp className="h-4 w-4" />
                      {t("sftp.uploadDir")}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => setNameDialog("folder")}
                  title={t("sftp.newFolder")}
                >
                  <FolderPlus className="h-3.5 w-3.5" />
                </Button>
              </span>
            </div>
            <TransferSection tabId={tabId} transfers={tabTransfers} />
          </div>
        </div>
      </div>

      {ctxMenu && (
        <FloatingMenu
          canPaste={!!clipboard?.items.length}
          ctx={ctxMenu}
          onAction={handleCtxAction}
          onClose={() => setCtxMenu(null)}
        />
      )}

      <ExternalEditPendingDialog
        open={pendingDialogOpen}
        onOpenChange={setPendingDialogOpen}
        pendingItems={pendingItems}
        savingSessionId={savingSessionId}
        autoSavePhases={autoSavePhases}
        mergePrepareErrors={mergePrepareErrors}
        continueEditLabel={continueEditLabel}
        onOpenErrorDetail={openErrorDetail}
        onMerge={handlePendingMerge}
        onAcceptRemote={handlePendingAcceptRemote}
        onOverwrite={handlePendingOverwrite}
        onContinueEdit={handlePendingContinueEdit}
      />

      {safeCompareResult && (
        <ExternalEditCompareWorkbench compareResult={safeCompareResult} onDismiss={dismissCompare} />
      )}

      {safeMergeResult && (
        <ExternalEditMergeWorkbench
          mergeResult={safeMergeResult}
          savingSessionId={savingSessionId}
          onClose={() => {
            setPreparedMergeResult(null);
            dismissMerge();
          }}
          onError={() => setError(t(EXTERNAL_EDIT_SAFE_ERROR_KEY))}
        />
      )}

      <Dialog open={!!safeSelectedError} onOpenChange={(open) => !open && dismissErrorDetail()}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("externalEdit.error.title")}</DialogTitle>
            <DialogDescription>{safeSelectedError ? `${safeSelectedError.remotePath}` : ""}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3 text-sm">
            <div>
              <div className="text-xs text-muted-foreground">{t("externalEdit.error.summaryLabel")}</div>
              <div>{safeSelectedError?.lastError?.summary || ""}</div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">{t("externalEdit.error.stepLabel")}</div>
              <div>{safeSelectedError?.lastError?.step || ""}</div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">{t("externalEdit.error.suggestionLabel")}</div>
              <div>{safeSelectedError?.lastError?.suggestion || ""}</div>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("sftp.deleteConfirmTitle")}
        description={
          deleteTarget?.paths.length === 1
            ? t("sftp.deleteConfirmDesc", { name: deleteTarget.paths[0]?.name ?? "" })
            : t("sftp.deleteMultiDesc", { count: deleteTarget?.paths.length ?? 0 })
        }
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={handleDelete}
      />
      <NameDialog
        open={!!nameDialog}
        title={nameDialog === "folder" ? t("sftp.newFolder") : t("sftp.newFile")}
        placeholder={nameDialog === "folder" ? t("sftp.folderNamePlaceholder") : t("sftp.filenamePlaceholder")}
        onCancel={() => setNameDialog(null)}
        onSubmit={submitNewName}
      />
      <PermissionDialog
        sessionId={sessionId}
        target={permissionTarget}
        onClose={() => setPermissionTarget(null)}
        onSaved={() => void loadDir(currentPathRef.current)}
      />
      <PropertiesDialog sessionId={sessionId} target={propertiesTarget} onClose={() => setPropertiesTarget(null)} />
    </>
  );
}
