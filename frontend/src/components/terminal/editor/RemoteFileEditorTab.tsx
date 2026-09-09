import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, CircleHelp, FileText, Loader2, Save } from "lucide-react";
import type * as MonacoNS from "monaco-editor";
import type { OnMount } from "@monaco-editor/react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
  ConfirmDialog,
} from "@opskat/ui";
import { CodeEditor, type CodeEditorLanguage } from "@/components/CodeEditor";
import { buildTextDiffBlocks, type TextDiffBlock } from "@/lib/textDiffBlocks";
import {
  readExternalEditSessionText,
  type ExternalEditSaveResult,
  type ExternalEditSession,
} from "@/lib/externalEditApi";
import { useExternalEditStore } from "@/stores/externalEditStore";
import { findEditorTabId, useTabStore, type EditorTabMeta } from "@/stores/tabStore";
import { getTerminalActiveAssetIds, useTerminalStore } from "@/stores/terminalStore";
import { ExternalEditCompareWorkbench } from "../external-edit/CompareWorkbench";
import { ExternalEditMergeWorkbench } from "../external-edit/MergeWorkbench";

const LANGUAGE_BY_EXTENSION: Record<string, CodeEditorLanguage> = {
  js: "javascript",
  json: "json",
  md: "markdown",
  mjs: "javascript",
  sh: "shell",
  sql: "sql",
  ts: "javascript",
  yaml: "yaml",
  yml: "yaml",
};

function languageOf(remotePath: string): CodeEditorLanguage {
  const name = remotePath.split("/").filter(Boolean).at(-1) ?? "";
  const extension = name.includes(".") ? name.split(".").pop() : "";
  return LANGUAGE_BY_EXTENSION[(extension ?? "").toLowerCase()] ?? "plaintext";
}

/**
 * 保存的结果面：进行中 / 写回时刻 / 三类需要用户看清楚的失败。
 * 冲突、远端缺失、写入或重绑被拒绝各自成一种状态 —— 它们的出路不同，不能折叠成一句「保存失败」。
 */
type SaveOutcome =
  | { kind: "idle" }
  | { kind: "saving" }
  | { kind: "saved"; at: number }
  | { kind: "conflict"; message?: string }
  | { kind: "remoteMissing"; message?: string }
  | { kind: "failed"; message: string };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** 重读拿到的会话状态落到与保存时同一套结论上：冲突走同样的四条出路，远端消失走同一条重建出路。 */
function remoteCheckOutcome(state: ExternalEditSession["state"]): SaveOutcome {
  if (state === "conflict") return { kind: "conflict" };
  if (state === "remote_missing") return { kind: "remoteMissing" };
  return { kind: "idle" };
}

// 改动行沿用三栏合并里「本地改动」的槽位配色，不另造一套差异视觉。
function buildChangedLineDecorations(
  monaco: typeof MonacoNS,
  blocks: TextDiffBlock[]
): MonacoNS.editor.IModelDeltaDecoration[] {
  return blocks.flatMap((block) => {
    if (block.modifiedStartLine < 1 || block.modifiedEndLine < block.modifiedStartLine) return [];
    return [
      {
        range: new monaco.Range(block.modifiedStartLine, 1, block.modifiedEndLine, 1),
        options: { isWholeLine: true, linesDecorationsClassName: "external-edit-merge-gutter-local" },
      },
    ];
  });
}

interface RemoteFileEditorTabProps {
  meta: EditorTabMeta;
}

/**
 * 内置编辑器 tab：占满主区、右侧不挂文件面板（要看目录树切回终端 tab）。
 * 文本按会话记录的编码由后端解码后读入；写回只发生在用户显式保存时，且必须走
 * external-edit 的既有保存路径，冲突检测因此始终生效。
 */
export function RemoteFileEditorTab({ meta }: RemoteFileEditorTabProps) {
  const { t } = useTranslation();
  const [reloadToken, setReloadToken] = useState(0);
  // 读取结果连同它属于哪一次读取一起存：换会话 / 重试时不用在 effect 里同步清空旧内容，
  // 只要 key 对不上就还是「读取中」。
  const loadKey = `${meta.sessionId}#${reloadToken}`;
  const [loaded, setLoaded] = useState<{ key: string; text: string; baseline: string; failed: boolean } | null>(null);
  const [outcome, setOutcome] = useState<{ key: string; state: SaveOutcome }>({
    key: loadKey,
    state: { kind: "idle" },
  });

  const tabId = useTabStore((s) => findEditorTabId(s.tabs, meta.assetId, meta.remotePath));
  const pendingCloseTabId = useTabStore((s) => s.pendingCloseTabId);
  const setTabUnsaved = useTabStore((s) => s.setTabUnsaved);

  const saveSessionText = useExternalEditStore((s) => s.saveSessionText);
  const refreshSession = useExternalEditStore((s) => s.refreshSession);
  const resolveConflict = useExternalEditStore((s) => s.resolveConflict);
  const compareSession = useExternalEditStore((s) => s.compareSession);
  const prepareMerge = useExternalEditStore((s) => s.prepareMerge);
  const dismissCompare = useExternalEditStore((s) => s.dismissCompare);
  const dismissMerge = useExternalEditStore((s) => s.dismissMerge);
  const savingSessionId = useExternalEditStore((s) => s.savingSessionId);
  const compareResult = useExternalEditStore((s) => s.compareResult);
  const mergeResult = useExternalEditStore((s) => s.mergeResult);

  // 重启恢复出来的 tab 必须重新比对远端基线：静默拿旧内容继续编辑就是在准备一次覆盖。
  // 在重读给出结论之前它既不是干净态也不是冲突态，而是显式的「远端状态未确认」。
  const [remoteUnconfirmed, setRemoteUnconfirmed] = useState(() =>
    useTabStore.getState().restoredTabIds.includes(tabId ?? "")
  );
  // 重读的 promise 存在 ref 里，而不是「进 effect 就把标记置否」：effect 被重跑时
  // （StrictMode 的二次挂载就是）第一次的 await 会被 cancelled 丢弃，若那时标记已经消费掉，
  // 第二次就直接跳过重读，结果是请求发了、冲突却永远显示不出来。存 promise 让重跑复用同一次
  // 请求：只问远端一次，且哪一次 effect 活到最后都能拿到结论。
  const recheckPromiseRef = useRef<Promise<ExternalEditSession> | null>(null);
  // 重读是否在途要自己记：store 的 savingSessionId 同样会被保存 / 比对 / 合并指到本会话，
  // 借用它会让未确认横幅在保存期间谎报「正在重新读取远端」，并且锁死手动重读按钮。
  const [rechecking, setRechecking] = useState(false);
  const editorRef = useRef<{ editor: MonacoNS.editor.IStandaloneCodeEditor; monaco: typeof MonacoNS } | null>(null);
  const decorationsRef = useRef<MonacoNS.editor.IEditorDecorationsCollection | null>(null);
  const [mountVersion, setMountVersion] = useState(0);
  const [confirmOverwrite, setConfirmOverwrite] = useState(false);

  const current = loaded?.key === loadKey ? loaded : null;
  const saveState: SaveOutcome = outcome.key === loadKey ? outcome.state : { kind: "idle" };
  const dirty = !!current && !current.failed && current.text !== current.baseline;

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const value = await readExternalEditSessionText(meta.sessionId);
        if (!cancelled) setLoaded({ key: loadKey, text: value, baseline: value, failed: false });
      } catch {
        // 具体失败原因带着本地副本路径，不适合直接展示；这里只给出可重试的结论。
        if (!cancelled) setLoaded({ key: loadKey, text: "", baseline: "", failed: true });
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, [loadKey, meta.sessionId]);

  // 「该资产有没有可用会话」用终端已连接资产集合这一个现成信号（决策 12）：
  // 订阅它依赖的两个切片，才能在终端刚连上的那一刻拿到变化，而不是只在挂载时读一次。
  const terminalTabData = useTerminalStore((s) => s.tabData);
  const openTabs = useTabStore((s) => s.tabs);
  const sessionAvailable = useMemo(
    () => getTerminalActiveAssetIds().has(meta.assetId),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [meta.assetId, openTabs, terminalTabData]
  );

  const recheckRemote = useCallback(
    async (isCancelled: () => boolean) => {
      const pending = (recheckPromiseRef.current ??= refreshSession(meta.sessionId));
      setRechecking(true);
      try {
        const session = await pending;
        if (isCancelled()) return;
        recheckPromiseRef.current = null;
        setRemoteUnconfirmed(false);
        setOutcome({ key: loadKey, state: remoteCheckOutcome(session.state) });
      } catch (error) {
        if (isCancelled()) return;
        recheckPromiseRef.current = null;
        // 失败不是结论：远端状态仍然未确认（等下一个可用会话或用户手动重读），
        // 失败原因按后端给出的分类原样呈现，本地改动一行不动。
        setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } });
      } finally {
        setRechecking(false);
      }
    },
    [loadKey, meta.sessionId, refreshSession]
  );

  // 不新建连接：一直等到该资产出现可用会话才自动重读（会话不可达时重读本就必然失败）。
  useEffect(() => {
    if (!remoteUnconfirmed || !sessionAvailable) return;
    let cancelled = false;
    void (async () => {
      await recheckRemote(() => cancelled);
    })();
    return () => {
      cancelled = true;
    };
  }, [recheckRemote, remoteUnconfirmed, sessionAvailable]);

  const handleRecheckRemote = useCallback(() => {
    // 手动重读要真的再问一次远端，而不是复用上一次已经完成的那个 promise。
    recheckPromiseRef.current = null;
    void recheckRemote(() => false);
  }, [recheckRemote]);

  useEffect(() => {
    if (!tabId) return;
    setTabUnsaved(tabId, dirty);
  }, [dirty, setTabUnsaved, tabId]);

  const changedBlocks = useMemo(
    () => (current && !current.failed ? buildTextDiffBlocks(current.baseline, current.text) : []),
    [current]
  );

  useEffect(() => {
    const refs = editorRef.current;
    const decorations = decorationsRef.current;
    if (!refs || !decorations) return;
    decorations.set(buildChangedLineDecorations(refs.monaco, changedBlocks));
  }, [changedBlocks, mountVersion]);

  const applyResult = useCallback(
    (result: ExternalEditSaveResult, savedText: string) => {
      switch (result.status) {
        case "saved":
        case "noop":
          setLoaded((state) => (state && state.key === loadKey ? { ...state, baseline: savedText } : state));
          setOutcome({ key: loadKey, state: { kind: "saved", at: Date.now() } });
          return true;
        case "conflict_remote_changed":
          setOutcome({ key: loadKey, state: { kind: "conflict", message: result.message } });
          return false;
        case "remote_missing":
          setOutcome({ key: loadKey, state: { kind: "remoteMissing", message: result.message } });
          return false;
        default:
          setOutcome({
            key: loadKey,
            state: { kind: "failed", message: result.message || result.status },
          });
          return false;
      }
    },
    [loadKey]
  );

  const handleSave = useCallback(async (): Promise<boolean> => {
    if (!current || current.failed || current.text === current.baseline) return true;
    const text = current.text;
    setOutcome({ key: loadKey, state: { kind: "saving" } });
    try {
      return applyResult(await saveSessionText(meta.sessionId, text), text);
    } catch (error) {
      setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } });
      return false;
    }
  }, [applyResult, current, loadKey, meta.sessionId, saveSessionText]);

  // Monaco 的 ⌘S 命令只在挂载时注册一次，这里用 ref 把它接到最新的保存闭包上。
  const saveRef = useRef(handleSave);
  useEffect(() => {
    saveRef.current = handleSave;
  }, [handleSave]);

  const handleMount = useCallback<OnMount>((editor, monaco) => {
    editorRef.current = { editor, monaco };
    decorationsRef.current = editor.createDecorationsCollection([]);
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      void saveRef.current();
    });
    setMountVersion((version) => version + 1);
  }, []);

  const handleChange = useCallback(
    (value: string) => setLoaded((state) => (state ? { ...state, text: value } : state)),
    []
  );
  const handleRetry = useCallback(() => setReloadToken((token) => token + 1), []);

  const handleMerge = useCallback(async () => {
    try {
      await prepareMerge(meta.sessionId);
    } catch (error) {
      setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } });
    }
  }, [loadKey, meta.sessionId, prepareMerge]);

  const handleCompare = useCallback(async () => {
    try {
      await compareSession(meta.sessionId);
    } catch (error) {
      setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } });
    }
  }, [compareSession, loadKey, meta.sessionId]);

  const updateTab = useTabStore((s) => s.updateTab);
  const handleReread = useCallback(async () => {
    try {
      const result = await resolveConflict(meta.sessionId, "reread");
      // reread 会以远端新基线重建草稿，会话 id 随之变化：tab 必须换绑到新会话再重新读取。
      const nextSessionId = result.session?.id;
      if (nextSessionId && nextSessionId !== meta.sessionId && tabId) {
        updateTab(tabId, { meta: { ...meta, sessionId: nextSessionId } });
        return;
      }
      setReloadToken((token) => token + 1);
    } catch (error) {
      setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } });
    }
  }, [loadKey, meta, resolveConflict, tabId, updateTab]);

  // overwrite / recreate 写的是会话的本地副本，而用户可能在冲突横幅出现之后又改了几行：
  // 先把编辑器当前文本落进本地副本（这一步必然再次撞上同一个冲突），再由显式决策写回远端，
  // 落到远端的字节才等于屏幕上看到的内容。
  const handleResolveWithWrite = useCallback(
    async (resolution: "overwrite" | "recreate") => {
      if (!current || current.failed) return;
      const text = current.text;
      setOutcome({ key: loadKey, state: { kind: "saving" } });
      try {
        const staged = await saveSessionText(meta.sessionId, text);
        if (staged.status === "saved" || staged.status === "noop") {
          applyResult(staged, text);
          return;
        }
        applyResult(await resolveConflict(meta.sessionId, resolution), text);
      } catch (error) {
        setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } });
      }
    },
    [applyResult, current, loadKey, meta.sessionId, resolveConflict, saveSessionText]
  );

  const handleMergeClosed = useCallback(() => {
    dismissMerge();
    const session = useExternalEditStore.getState().sessions[meta.sessionId];
    // 合并稿已落盘时会话不再处于冲突态：以合并后的内容重新载入编辑器。
    if (session && session.state !== "conflict" && session.state !== "remote_missing") {
      setReloadToken((token) => token + 1);
    }
  }, [dismissMerge, meta.sessionId]);

  const forceCloseTab = useTabStore((s) => s.forceCloseTab);
  const cancelPendingClose = useTabStore((s) => s.cancelPendingClose);
  const closeRequested = !!tabId && pendingCloseTabId === tabId;

  const handleSaveAndClose = useCallback(async () => {
    if (!tabId) return;
    cancelPendingClose();
    if (await handleSave()) forceCloseTab(tabId);
  }, [cancelPendingClose, forceCloseTab, handleSave, tabId]);

  const handleDiscardAndClose = useCallback(() => {
    if (tabId) forceCloseTab(tabId);
  }, [forceCloseTab, tabId]);

  const saving = saveState.kind === "saving";
  const ownsCompare = compareResult?.primaryDraftSessionId === meta.sessionId;
  const ownsMerge = mergeResult?.primaryDraftSessionId === meta.sessionId;

  return (
    <div className="flex h-full flex-col bg-background" data-testid="remote-file-editor">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-xs">
        <FileText className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="shrink-0 font-medium text-foreground">{meta.assetName}</span>
        <span className="truncate text-muted-foreground" title={meta.remotePath}>
          {meta.remotePath}
        </span>
        {dirty && (
          <span
            className="shrink-0 rounded bg-warning/15 px-1.5 py-0.5 text-warning"
            data-testid="remote-file-editor-unsaved"
          >
            {t("externalEdit.builtIn.unsaved")}
          </span>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-2">
          {saving && (
            <span className="text-muted-foreground" data-testid="remote-file-editor-saving">
              {t("externalEdit.builtIn.savingRemote")}
            </span>
          )}
          {saveState.kind === "saved" && (
            <span className="text-muted-foreground" data-testid="remote-file-editor-saved-at">
              {t("externalEdit.builtIn.savedAt", { time: new Date(saveState.at).toLocaleTimeString() })}
            </span>
          )}
          <Button
            data-testid="remote-file-editor-save"
            disabled={saving || !dirty}
            onClick={() => void handleSave()}
            size="xs"
            variant="outline"
          >
            {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
            {t("action.save")}
          </Button>
        </div>
      </div>

      {remoteUnconfirmed && (
        <div
          className="flex flex-wrap items-center gap-2 border-b bg-muted/60 px-3 py-2 text-xs"
          data-testid="remote-file-editor-unconfirmed"
        >
          <CircleHelp className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="font-medium text-foreground">{t("externalEdit.builtIn.unconfirmedTitle")}</span>
          {/* 会话可用却仍未确认，说明刚失败过：原因由下面的失败横幅说清楚，这里不再重复一句。 */}
          {rechecking ? (
            <span className="text-muted-foreground">{t("externalEdit.builtIn.unconfirmedChecking")}</span>
          ) : (
            !sessionAvailable && (
              <span className="text-muted-foreground">{t("externalEdit.builtIn.unconfirmedWaiting")}</span>
            )
          )}
          <Button className="ml-auto" disabled={rechecking} onClick={handleRecheckRemote} size="xs" variant="outline">
            {t("externalEdit.actions.refresh")}
          </Button>
        </div>
      )}

      {saveState.kind === "conflict" && (
        <div
          className="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-3 py-2 text-xs"
          data-testid="remote-file-editor-conflict"
        >
          <AlertTriangle className="h-4 w-4 shrink-0 text-warning" />
          <span className="font-medium text-foreground">{t("externalEdit.conflict.remoteChangedTitle")}</span>
          {saveState.message && <span className="text-muted-foreground">{saveState.message}</span>}
          <span className="text-muted-foreground">{t("externalEdit.builtIn.localChangesKept")}</span>
          <div className="ml-auto flex shrink-0 items-center gap-2">
            <Button onClick={() => void handleMerge()} size="xs" variant="outline">
              {t("externalEdit.actions.merge")}
            </Button>
            <Button onClick={() => void handleCompare()} size="xs" variant="outline">
              {t("externalEdit.actions.compare")}
            </Button>
            <Button onClick={() => void handleReread()} size="xs" variant="outline">
              {t("externalEdit.actions.reread")}
            </Button>
            <Button onClick={() => setConfirmOverwrite(true)} size="xs" variant="outline">
              {t("externalEdit.actions.overwrite")}
            </Button>
          </div>
        </div>
      )}

      {saveState.kind === "remoteMissing" && (
        <div
          className="flex flex-wrap items-center gap-2 border-b border-destructive/40 bg-destructive/10 px-3 py-2 text-xs"
          data-testid="remote-file-editor-remote-missing"
        >
          <AlertTriangle className="h-4 w-4 shrink-0 text-destructive" />
          <span className="font-medium text-foreground">{t("externalEdit.conflict.remoteMissingTitle")}</span>
          {saveState.message && <span className="text-muted-foreground">{saveState.message}</span>}
          <span className="text-muted-foreground">{t("externalEdit.builtIn.localChangesKept")}</span>
          <Button
            className="ml-auto"
            onClick={() => void handleResolveWithWrite("recreate")}
            size="xs"
            variant="outline"
          >
            {t("externalEdit.actions.saveAgain")}
          </Button>
        </div>
      )}

      {saveState.kind === "failed" && (
        <div
          className="flex flex-wrap items-center gap-2 border-b border-destructive/40 bg-destructive/10 px-3 py-2 text-xs"
          data-testid="remote-file-editor-error"
        >
          <AlertTriangle className="h-4 w-4 shrink-0 text-destructive" />
          <span className="text-foreground">{saveState.message}</span>
          <span className="text-muted-foreground">{t("externalEdit.builtIn.localChangesKept")}</span>
        </div>
      )}

      <div className="min-h-0 flex-1">
        {current?.failed ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
            <AlertTriangle className="h-5 w-5 text-destructive" />
            <span>{t("externalEdit.builtIn.loadFailed")}</span>
            <Button size="sm" variant="outline" onClick={handleRetry}>
              {t("action.retry")}
            </Button>
          </div>
        ) : !current ? (
          <div className="flex h-full items-center justify-center">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <CodeEditor
            language={languageOf(meta.remotePath)}
            onChange={handleChange}
            onMount={handleMount}
            testId="remote-file-editor-content"
            value={current.text}
          />
        )}
      </div>

      <ConfirmDialog
        cancelText={t("action.cancel")}
        confirmText={t("externalEdit.actions.overwrite")}
        confirmTestId="remote-file-editor-overwrite-confirm"
        description={t("externalEdit.builtIn.overwriteConfirmDesc")}
        onConfirm={() => {
          setConfirmOverwrite(false);
          void handleResolveWithWrite("overwrite");
        }}
        onOpenChange={(open) => {
          if (!open) setConfirmOverwrite(false);
        }}
        open={confirmOverwrite}
        title={t("externalEdit.builtIn.overwriteConfirmTitle")}
      />

      <AlertDialog
        open={closeRequested}
        onOpenChange={(open) => {
          if (!open) cancelPendingClose();
        }}
      >
        <AlertDialogContent data-testid="remote-file-editor-close-confirm" onOverlayClick={cancelPendingClose}>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("externalEdit.builtIn.closeUnsavedTitle")}</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div>{t("externalEdit.builtIn.closeUnsavedDesc")}</div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={cancelPendingClose}>{t("action.cancel")}</AlertDialogCancel>
            <Button onClick={handleDiscardAndClose} variant="outline">
              {t("externalEdit.builtIn.discardAndClose")}
            </Button>
            <AlertDialogAction onClick={() => void handleSaveAndClose()} variant="default">
              {t("externalEdit.builtIn.saveAndClose")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {ownsCompare && compareResult && (
        <ExternalEditCompareWorkbench compareResult={compareResult} onDismiss={dismissCompare} />
      )}
      {ownsMerge && mergeResult && (
        <ExternalEditMergeWorkbench
          mergeResult={mergeResult}
          onClose={handleMergeClosed}
          onError={(error) => setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } })}
          savingSessionId={savingSessionId}
        />
      )}
    </div>
  );
}
