import { createElement, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { AlertTriangle, CircleHelp, ExternalLink, FileDiff, Loader2, Save } from "lucide-react";
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
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@opskat/ui";
import { CodeEditor, type CodeEditorLanguage } from "@/components/CodeEditor";
import { typeIcon, typeIconColor } from "@/lib/objectContentType";
import { buildTextDiffBlocks, type TextDiffBlock } from "@/lib/textDiffBlocks";
import {
  getExternalEditSettings,
  openExternalEdit,
  readExternalEditSessionText,
  type ExternalEditCompareResult,
  type ExternalEditSaveResult,
  type ExternalEditSession,
} from "@/lib/externalEditApi";
import { resolveExternalEditorTarget, useExternalEditStore } from "@/stores/externalEditStore";
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

function fileNameOf(remotePath: string): string {
  return remotePath.split("/").filter(Boolean).at(-1) ?? remotePath;
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
  // 在途的那次重读连同它问的是哪个会话一起存，而不是「进 effect 就把标记置否」：effect 被重跑时
  // （StrictMode 的二次挂载就是）第一次的 await 会被 cancelled 丢弃，若那时标记已经消费掉，
  // 第二次就直接跳过重读，结果是请求发了、冲突却永远显示不出来。存 promise 让重跑复用同一次
  // 请求：只问远端一次，且哪一次 effect 活到最后都能拿到结论。带上会话 id 是因为 reread 会把
  // tab 换绑到新会话，旧会话的在途结论不能拿来给新会话下结论。
  const recheckRef = useRef<{ sessionId: string; promise: Promise<ExternalEditSession> } | null>(null);
  // 重读是否在途要自己记：store 的 savingSessionId 同样会被保存 / 比对 / 合并指到本会话，
  // 借用它会让未确认横幅在保存期间谎报「正在重新读取远端」，并且锁死手动重读按钮。
  const [rechecking, setRechecking] = useState(false);
  const editorRef = useRef<{ editor: MonacoNS.editor.IStandaloneCodeEditor; monaco: typeof MonacoNS } | null>(null);
  const decorationsRef = useRef<MonacoNS.editor.IEditorDecorationsCollection | null>(null);
  const [mountVersion, setMountVersion] = useState(0);
  const [confirmOverwrite, setConfirmOverwrite] = useState(false);
  const [cursor, setCursor] = useState({ line: 1, column: 1 });
  const [confirmHandoff, setConfirmHandoff] = useState(false);
  // 拉起外部编辑器要下载本地副本、起进程，是可以等上几秒的一趟 IPC。这期间还能敲字的话，
  // 敲进去的内容会在交接完成关掉 tab 的那一刻被无声丢掉：交接开始就锁住这份文档。
  const [handingOff, setHandingOff] = useState(false);
  const [draftCompare, setDraftCompare] = useState<ExternalEditCompareResult | null>(null);
  // 会话记录带着这次编辑的编码与 SSH 传输：状态栏显示前者，交给外部编辑器时用后者。
  const sessionRecord = useExternalEditStore((s) => s.sessions[meta.sessionId]);

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
      let inflight = recheckRef.current;
      if (!inflight || inflight.sessionId !== meta.sessionId) {
        inflight = { sessionId: meta.sessionId, promise: refreshSession(meta.sessionId) };
        recheckRef.current = inflight;
      }
      setRechecking(true);
      try {
        const session = await inflight.promise;
        if (isCancelled()) return;
        setRemoteUnconfirmed(false);
        setOutcome({ key: loadKey, state: remoteCheckOutcome(session.state) });
      } catch (error) {
        if (isCancelled()) return;
        // 失败不是结论：远端状态仍然未确认（等下一个可用会话或用户手动重读），
        // 失败原因按后端给出的分类原样呈现，本地改动一行不动。
        setOutcome({ key: loadKey, state: { kind: "failed", message: errorMessage(error) } });
      } finally {
        // 这一次请求已经有结论，不论消费它的那次 effect 是否已被丢弃都要清掉：
        // 留着它会让重连后的自动重读复用一个已 settle 的 promise，也就是永远不再真的问远端。
        if (recheckRef.current === inflight) recheckRef.current = null;
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
    // 手动重读要真的再问一次远端，而不是搭上一次还在途的请求。
    recheckRef.current = null;
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
    const position = editor.getPosition();
    if (position) setCursor({ line: position.lineNumber, column: position.column });
    editor.onDidChangeCursorPosition((event) => {
      setCursor({ line: event.position.lineNumber, column: event.position.column });
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

  // 「用外部编辑器打开」等同于文件面板右键的同名操作：同一条 open 路径、同一条编辑器挑选规则，
  // SSH 传输取会话记录里当前绑定的那一个（保存 / 重读的重绑会把它更新到可用会话上）。
  const handleOpenExternal = useCallback(async () => {
    const transportSessionId = sessionRecord?.sessionId;
    if (!transportSessionId) {
      toast.error(t("externalEdit.builtIn.openExternalNoSession"));
      return;
    }
    setHandingOff(true);
    try {
      const target = resolveExternalEditorTarget(await getExternalEditSettings());
      if (!target) {
        toast.error(t("externalEdit.builtIn.noExternalEditor"));
        return;
      }
      await openExternalEdit({
        assetId: meta.assetId,
        sessionId: transportSessionId,
        remotePath: meta.remotePath,
        editorId: target.editorId,
      });
      // 交出去就是交出去：后端按 documentKey 复用的是同一个会话，只是把它切到外部编辑器的
      // 落盘即写回。这个 tab 再留着，同一份文档上就又有了两个各自改、各自写回的编辑器 ——
      // 外部编辑器一落盘会直接推进远端与基线，之后这里按 ⌘S 就会拿一份看不见对方改动的草稿盖过去。
      // 未保存的改动在交接开始前就已经被拦下来处理掉，交接期间编辑器是锁住的。
      if (tabId) forceCloseTab(tabId);
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setHandingOff(false);
    }
  }, [forceCloseTab, meta.assetId, meta.remotePath, sessionRecord?.sessionId, t, tabId]);

  // 交出去之前必须先处理未保存的改动：草稿留在这里，外部编辑器打开的是本地副本，
  // 就这样交出去等于让两个编辑器各持一份不同的内容。
  const handleOpenExternalRequest = useCallback(() => {
    if (dirty) {
      setConfirmHandoff(true);
      return;
    }
    void handleOpenExternal();
  }, [dirty, handleOpenExternal]);

  const handleHandoffSave = useCallback(async () => {
    setConfirmHandoff(false);
    // 写回远端同样是一趟能等上几秒的 IPC，和交接本身连着算一段：中途敲进来的字
    // 既不会跟着保存，也会在交接完成关掉 tab 时消失。
    setHandingOff(true);
    if (await handleSave()) await handleOpenExternal();
    setHandingOff(false);
  }, [handleOpenExternal, handleSave]);

  const handleHandoffDiscard = useCallback(async () => {
    setConfirmHandoff(false);
    setLoaded((state) => (state && state.key === loadKey ? { ...state, text: state.baseline } : state));
    await handleOpenExternal();
  }, [handleOpenExternal, loadKey]);

  const fileName = fileNameOf(meta.remotePath);

  // 常规的「我改了什么」：把草稿与读入时的远端基线送进既有的 Compare 工作台。
  // 后端的 Compare 只为冲突态构造比对结果，冲突横幅上的那颗按钮仍然走它。
  const handleCompareDraft = useCallback(() => {
    if (!current || current.failed) return;
    setDraftCompare({
      // 工作台只读内容两栏；documentKey 的规范形态只有会话记录里那一个（软链后与远程路径不同）。
      documentKey: sessionRecord?.documentKey ?? "",
      primaryDraftSessionId: meta.sessionId,
      fileName,
      remotePath: meta.remotePath,
      localContent: current.text,
      remoteContent: current.baseline,
      readOnly: true,
    });
  }, [current, fileName, meta.remotePath, meta.sessionId, sessionRecord?.documentKey]);

  const saving = saveState.kind === "saving";
  const ownsCompare = compareResult?.primaryDraftSessionId === meta.sessionId;
  const ownsMerge = mergeResult?.primaryDraftSessionId === meta.sessionId;

  const language = languageOf(meta.remotePath);
  // 换行符按读入时的内容判定：编辑过程中的击键不该让状态栏跳来跳去。
  const lineEnding = current?.baseline.includes("\r\n") ? "CRLF" : "LF";

  // 缩进是 model 的选项，且 Monaco 建 model 时已经按文件内容判定过一次（detectIndentation 默认开着）：
  // 它的判定专门处理了「一个空格开头的块注释续行」「候选宽度只取 2..8」这类陷阱，再写一份猜测反过来
  // 盖上去只会更差 —— 盖 model 会丢掉更准的结果，盖 editor options 更糟：standalone 的 editor options
  // 是全局配置，会把这一个文件的缩进带给应用里其它 Monaco 实例。这里只把 model 上已经生效的读出来显示。
  // 全局 editor 配置一变 Monaco 会对每个 model 重跑一次判定，所以要订阅而不是只在挂载时读一次。
  const [indent, setIndent] = useState<{ insertSpaces: boolean; size: number } | null>(null);
  useEffect(() => {
    const model = editorRef.current?.editor.getModel();
    if (!model) return;
    const readIndent = () => {
      const options = model.getOptions();
      setIndent({ insertSpaces: options.insertSpaces, size: options.indentSize });
    };
    readIndent();
    const subscription = model.onDidChangeOptions(readIndent);
    return () => subscription.dispose();
  }, [mountVersion]);

  return (
    <div className="flex h-full flex-col bg-background" data-testid="remote-file-editor">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-xs">
        {createElement(typeIcon("", meta.remotePath), {
          className: `h-4 w-4 shrink-0 ${typeIconColor("", meta.remotePath)}`,
        })}
        <span className="shrink-0 font-medium text-foreground">{fileName}</span>
        {dirty && (
          <span
            aria-label={t("externalEdit.builtIn.unsaved")}
            className="size-1.5 shrink-0 rounded-full bg-warning"
            data-testid="remote-file-editor-unsaved"
            title={t("externalEdit.builtIn.unsaved")}
          />
        )}
        <span className="shrink-0 text-muted-foreground">{meta.assetName}</span>
        <span className="shrink-0 text-muted-foreground/60">·</span>
        <span className="truncate text-muted-foreground" title={meta.remotePath}>
          {meta.remotePath}
        </span>
        <div className="ml-auto flex shrink-0 items-center gap-2">
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
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                aria-label={t("externalEdit.builtIn.viewDiff")}
                data-testid="remote-file-editor-compare"
                disabled={!current || current.failed}
                onClick={handleCompareDraft}
                size="icon-xs"
                variant="outline"
              >
                <FileDiff className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{t("externalEdit.builtIn.viewDiff")}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                aria-label={t("externalEdit.builtIn.openExternal")}
                data-testid="remote-file-editor-open-external"
                disabled={handingOff}
                onClick={handleOpenExternalRequest}
                size="icon-xs"
                variant="outline"
              >
                <ExternalLink className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{t("externalEdit.builtIn.openExternal")}</TooltipContent>
          </Tooltip>
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
          {/* 后端消息是中文的 Go 字符串：标题跟随界面语言，英文用户至少不会只看到一句中文。 */}
          <span className="font-medium text-foreground">{t("externalEdit.builtIn.failedTitle")}</span>
          <span className="text-muted-foreground">{saveState.message}</span>
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
            language={language}
            onChange={handleChange}
            onMount={handleMount}
            readOnly={handingOff}
            testId="remote-file-editor-content"
            value={current.text}
          />
        )}
      </div>

      <div
        className="flex items-center gap-3 border-t px-3 py-1 text-[11px] text-muted-foreground"
        data-testid="remote-file-editor-status-bar"
      >
        <span>{t("externalEdit.builtIn.status.position", { line: cursor.line, column: cursor.column })}</span>
        {current && !current.failed && (
          <>
            {indent && (
              <span>
                {indent.insertSpaces
                  ? t("externalEdit.builtIn.status.indentSpaces", { size: indent.size })
                  : t("externalEdit.builtIn.status.indentTab")}
              </span>
            )}
            {sessionRecord && <span>{sessionRecord.originalEncoding.toUpperCase()}</span>}
            <span>{lineEnding}</span>
          </>
        )}
        <span>{language === "plaintext" ? t("externalEdit.builtIn.status.plainText") : language}</span>
        <div className="ml-auto flex items-center gap-2">
          {dirty && (
            <span className="flex items-center gap-1 text-warning">
              <span className="size-1.5 rounded-full bg-warning" />
              {t("externalEdit.builtIn.unsaved")}
            </span>
          )}
          {saving && <span data-testid="remote-file-editor-saving">{t("externalEdit.builtIn.savingRemote")}</span>}
          {saveState.kind === "saved" && (
            <span data-testid="remote-file-editor-saved-at">
              {t("externalEdit.builtIn.savedAt", { time: new Date(saveState.at).toLocaleTimeString() })}
            </span>
          )}
        </div>
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

      <AlertDialog
        open={confirmHandoff}
        onOpenChange={(open) => {
          if (!open) setConfirmHandoff(false);
        }}
      >
        <AlertDialogContent
          data-testid="remote-file-editor-handoff-confirm"
          onOverlayClick={() => setConfirmHandoff(false)}
        >
          <AlertDialogHeader>
            <AlertDialogTitle>{t("externalEdit.builtIn.handoffTitle")}</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div>{t("externalEdit.builtIn.handoffDesc")}</div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setConfirmHandoff(false)}>{t("action.cancel")}</AlertDialogCancel>
            <Button onClick={() => void handleHandoffDiscard()} variant="outline">
              {t("externalEdit.builtIn.handoffDiscard")}
            </Button>
            <AlertDialogAction onClick={() => void handleHandoffSave()} variant="default">
              {t("externalEdit.builtIn.handoffSave")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {draftCompare && (
        <ExternalEditCompareWorkbench compareResult={draftCompare} onDismiss={() => setDraftCompare(null)} />
      )}
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
