import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import type * as MonacoNS from "monaco-editor";
import { Button, cn, ConfirmDialog } from "@opskat/ui";
import { CodeEditor } from "@/components/CodeEditor";
import { buildTextDiffBlocks } from "@/lib/textDiffBlocks";
import type { ExternalEditMergePrepareResult } from "@/lib/externalEditApi";
import { useExternalEditStore } from "@/stores/externalEditStore";
import { ExternalEditIdeaFrame, ExternalEditIdeaEditorPane } from "./IdeaFrame";
import {
  blockLineRange,
  buildMergePaneDecorations,
  type MergeDecorationRefs,
  type MergeEditorRefs,
  type MergePaneRole,
} from "./merge-decorations";
import { takeMergeBlock } from "./merge-take";

interface MergeWorkbenchProps {
  mergeResult: ExternalEditMergePrepareResult;
  savingSessionId: string | null;
  onClose: () => void;
  onError: (error: unknown) => void;
}

interface MergeWorkbenchState {
  mergeResult: ExternalEditMergePrepareResult;
  finalContent: string;
  dirty: boolean;
  activeBlockIndex: number;
  navigationToken: number;
}

function createMergeWorkbenchState(
  mergeResult: ExternalEditMergePrepareResult,
  navigationToken = 0
): MergeWorkbenchState {
  return {
    mergeResult,
    finalContent: mergeResult.finalContent,
    dirty: false,
    activeBlockIndex: 0,
    navigationToken,
  };
}

function getCurrentMergeState(
  state: MergeWorkbenchState,
  mergeResult: ExternalEditMergePrepareResult
): MergeWorkbenchState {
  if (state.mergeResult === mergeResult) return state;
  return createMergeWorkbenchState(mergeResult, state.navigationToken);
}

export function ExternalEditMergeWorkbench({ mergeResult, savingSessionId, onClose, onError }: MergeWorkbenchProps) {
  const { t } = useTranslation();
  const applyMerge = useExternalEditStore((s) => s.applyMerge);

  const [workbenchState, setWorkbenchState] = useState(() => createMergeWorkbenchState(mergeResult));
  const [confirmClose, setConfirmClose] = useState(false);
  const [editorMountVersion, setEditorMountVersion] = useState(0);

  const editorRefs = useRef<MergeEditorRefs>({
    local: { editor: null, monaco: null },
    final: { editor: null, monaco: null },
    remote: { editor: null, monaco: null },
  });
  const decorationRefs = useRef<MergeDecorationRefs>({ local: null, final: null, remote: null });
  const revealFrameRef = useRef<number | null>(null);

  const conflictBlocks = useMemo(
    // 以 remote=original、local=modified 做 diff，insert=本地新增，delete=远方独有，modify=真正冲突
    () => buildTextDiffBlocks(mergeResult.remoteContent || "", mergeResult.localContent || ""),
    [mergeResult.localContent, mergeResult.remoteContent]
  );
  const conflictTotal = conflictBlocks.length;
  const currentState = getCurrentMergeState(workbenchState, mergeResult);
  const finalContent = currentState.finalContent;
  const dirty = currentState.dirty;
  const activeBlockIndex = Math.min(currentState.activeBlockIndex, Math.max(conflictTotal - 1, 0));
  const navigationToken = currentState.navigationToken;

  useEffect(() => {
    (["local", "final", "remote"] as MergePaneRole[]).forEach((pane) => {
      const { editor, monaco } = editorRefs.current[pane];
      if (!editor || !monaco) return;
      const decorations = buildMergePaneDecorations(monaco, conflictBlocks, activeBlockIndex, pane);
      decorationRefs.current[pane]?.clear();
      decorationRefs.current[pane] = editor.createDecorationsCollection(decorations);
    });
  }, [activeBlockIndex, conflictBlocks, editorMountVersion]);

  useEffect(() => {
    if (conflictBlocks.length === 0) return;
    const block = conflictBlocks[Math.min(Math.max(activeBlockIndex, 0), conflictBlocks.length - 1)];
    if (revealFrameRef.current != null) cancelAnimationFrame(revealFrameRef.current);
    revealFrameRef.current = requestAnimationFrame(() => {
      (["local", "final", "remote"] as MergePaneRole[]).forEach((pane) => {
        const editor = editorRefs.current[pane].editor;
        if (!editor) return;
        const { startLine } = blockLineRange(block, pane);
        const lineNumber = Math.max(1, startLine);
        editor.revealLineInCenter(lineNumber);
        editor.setPosition({ lineNumber, column: 1 });
      });
      revealFrameRef.current = null;
    });
    return () => {
      if (revealFrameRef.current != null) cancelAnimationFrame(revealFrameRef.current);
    };
  }, [activeBlockIndex, conflictBlocks, navigationToken, editorMountVersion]);

  const navigate = (direction: -1 | 1) => {
    if (conflictTotal === 0) return;
    setWorkbenchState((current) => {
      const base = getCurrentMergeState(current, mergeResult);
      const baseActiveBlockIndex = Math.min(base.activeBlockIndex, Math.max(conflictTotal - 1, 0));
      const next = Math.min(Math.max(baseActiveBlockIndex + direction, 0), conflictTotal - 1);
      if (next === baseActiveBlockIndex) return base;
      return {
        ...base,
        activeBlockIndex: next,
        navigationToken: base.navigationToken + 1,
      };
    });
  };

  const handleEditorMount = useCallback(
    (pane: MergePaneRole) => (editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: typeof MonacoNS) => {
      editorRefs.current[pane] = { editor, monaco };
      setEditorMountVersion((version) => version + 1);
    },
    []
  );

  const selectBlock = (index: number) => {
    setWorkbenchState((current) => {
      const base = getCurrentMergeState(current, mergeResult);
      return { ...base, activeBlockIndex: index, navigationToken: base.navigationToken + 1 };
    });
  };

  const takeBlock = (index: number, side: "local" | "remote") => {
    const next = takeMergeBlock({
      final: finalContent,
      local: mergeResult.localContent || "",
      remote: mergeResult.remoteContent || "",
      block: conflictBlocks[index],
      side,
    });
    if (next === null) {
      toast.error(t("externalEdit.merge.takeBlockUnlocated"));
      return;
    }
    if (next === finalContent) return;
    setWorkbenchState((current) => ({
      ...getCurrentMergeState(current, mergeResult),
      finalContent: next,
      dirty: true,
    }));
  };

  const takeButtonClass = (side: "local" | "remote") =>
    cn(
      "bg-transparent",
      side === "local"
        ? "border-success/40 text-success hover:bg-success/10 hover:text-success"
        : "border-info/40 text-info hover:bg-info/10 hover:text-info"
    );
  const takeHint = (side: "local" | "remote", index: number) =>
    t(side === "local" ? "externalEdit.merge.takeLocalHint" : "externalEdit.merge.takeRemoteHint", {
      index: index + 1,
    });

  // 窗格头部的按钮作用于当前块；按钮在没有差异块时禁用，activeBlockIndex 总落在 conflictBlocks 内
  const takeBlockButton = (side: "local" | "remote") => (
    <Button
      variant="outline"
      size="xs"
      className={takeButtonClass(side)}
      disabled={conflictTotal === 0}
      title={takeHint(side, activeBlockIndex)}
      onClick={() => takeBlock(activeBlockIndex, side)}
    >
      {t(side === "local" ? "externalEdit.merge.takeLocal" : "externalEdit.merge.takeRemote")}
    </Button>
  );

  // 侧栏逐块列出差异：每一块都能直接采用本地或远端，不必先用上一处/下一处走到它
  const blockList = conflictTotal > 0 && (
    <ol className="mt-4 space-y-1.5" data-testid="external-edit-merge-block-list">
      {conflictBlocks.map((block, index) => (
        <li
          key={block.id}
          className={cn(
            "rounded-md border px-2 py-1.5",
            index === activeBlockIndex ? "border-warning/50 bg-warning/10" : "border-border"
          )}
          data-testid={`external-edit-merge-block-${index}`}
        >
          <button
            type="button"
            className="w-full text-left text-xs font-medium text-foreground hover:underline"
            onClick={() => selectBlock(index)}
          >
            {t("externalEdit.merge.blockRow", { index: index + 1 })}
          </button>
          <div className="mt-1 flex gap-1.5">
            {(["local", "remote"] as const).map((side) => (
              <Button
                key={side}
                variant="outline"
                size="xs"
                className={takeButtonClass(side)}
                aria-label={takeHint(side, index)}
                title={takeHint(side, index)}
                onClick={() => takeBlock(index, side)}
              >
                {t(side === "local" ? "externalEdit.merge.takeLocal" : "externalEdit.merge.takeRemote")}
              </Button>
            ))}
          </div>
        </li>
      ))}
    </ol>
  );

  const handleApply = async () => {
    try {
      await applyMerge(mergeResult.primaryDraftSessionId, finalContent, mergeResult.remoteHash);
      setWorkbenchState((current) => {
        const base = getCurrentMergeState(current, mergeResult);
        if (!base.dirty) return base;
        return { ...base, dirty: false };
      });
      onClose();
    } catch (error) {
      onError(error);
    }
  };

  const handleOpenChange = (open: boolean) => {
    if (open) return;
    if (dirty) setConfirmClose(true);
    else onClose();
  };

  return (
    <>
      <ExternalEditIdeaFrame
        fileName={mergeResult.fileName}
        helper={t("externalEdit.merge.helper")}
        layoutLabel={t("externalEdit.merge.localCenterRemote")}
        mode="merge"
        remotePath={mergeResult.remotePath}
        sidebarLabel={t("externalEdit.merge.changelist")}
        sidebarContent={blockList}
        status={t("externalEdit.merge.status")}
        testId="external-edit-merge-workbench"
        title={t("externalEdit.merge.title")}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="xs"
              className="border-border bg-transparent text-foreground hover:bg-accent hover:text-accent-foreground"
              disabled={conflictTotal === 0 || activeBlockIndex === 0}
              onClick={() => navigate(-1)}
            >
              {t("externalEdit.merge.previous")}
            </Button>
            <div
              className="min-w-14 rounded border border-border bg-muted px-2 py-1 text-center text-xs text-foreground"
              data-testid="external-edit-merge-conflict-count"
            >
              {conflictTotal === 0 ? "0 / 0" : `${activeBlockIndex + 1} / ${conflictTotal}`}
            </div>
            <Button
              variant="outline"
              size="xs"
              className="border-border bg-transparent text-foreground hover:bg-accent hover:text-accent-foreground"
              disabled={conflictTotal === 0 || activeBlockIndex >= conflictTotal - 1}
              onClick={() => navigate(1)}
            >
              {t("externalEdit.merge.next")}
            </Button>
            <Button
              variant="outline"
              className="border-border bg-transparent text-foreground hover:bg-accent hover:text-accent-foreground"
              onClick={() => handleOpenChange(false)}
            >
              {t("action.cancel")}
            </Button>
            <Button disabled={savingSessionId === mergeResult.primaryDraftSessionId} onClick={() => void handleApply()}>
              {savingSessionId === mergeResult.primaryDraftSessionId
                ? t("action.saving")
                : t("externalEdit.actions.saveMerge")}
            </Button>
          </div>
        }
      >
        <div
          className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)_minmax(0,1fr)] gap-px bg-border"
          data-idea-layout="three-way-merge"
          data-testid="external-edit-merge-idea-layout"
        >
          <ExternalEditIdeaEditorPane
            actions={takeBlockButton("local")}
            badge={t("externalEdit.merge.readOnlySide")}
            title={t("externalEdit.side.local")}
            tone="local"
          >
            <CodeEditor
              className="min-h-0 flex-1 overflow-hidden"
              fontSize={12}
              height="100%"
              language="plaintext"
              options={{
                lineNumbers: "on",
                contextmenu: true,
                glyphMargin: true,
                minimap: { enabled: false },
                overviewRulerLanes: 3,
                readOnly: true,
              }}
              readOnly
              testId="external-edit-merge-local"
              value={mergeResult.localContent || ""}
              onMount={handleEditorMount("local")}
            />
          </ExternalEditIdeaEditorPane>
          <ExternalEditIdeaEditorPane
            badge={t("externalEdit.merge.editableCenter")}
            title={t("externalEdit.merge.finalDraft")}
            tone="final"
          >
            <CodeEditor
              className="min-h-0 flex-1 overflow-hidden"
              fontSize={12}
              height="100%"
              language="plaintext"
              options={{
                lineNumbers: "on",
                contextmenu: true,
                glyphMargin: true,
                minimap: { enabled: false },
                overviewRulerLanes: 3,
              }}
              testId="external-edit-merge-final"
              value={finalContent}
              onChange={(value) => {
                setWorkbenchState((current) => {
                  const base = getCurrentMergeState(current, mergeResult);
                  return {
                    ...base,
                    finalContent: value,
                    dirty: true,
                  };
                });
              }}
              onMount={handleEditorMount("final")}
            />
          </ExternalEditIdeaEditorPane>
          <ExternalEditIdeaEditorPane
            actions={takeBlockButton("remote")}
            badge={t("externalEdit.merge.readOnlySide")}
            title={t("externalEdit.side.remote")}
            tone="remote"
          >
            <CodeEditor
              className="min-h-0 flex-1 overflow-hidden"
              fontSize={12}
              height="100%"
              language="plaintext"
              options={{
                lineNumbers: "on",
                contextmenu: true,
                glyphMargin: true,
                minimap: { enabled: false },
                overviewRulerLanes: 3,
                readOnly: true,
              }}
              readOnly
              testId="external-edit-merge-remote"
              value={mergeResult.remoteContent || ""}
              onMount={handleEditorMount("remote")}
            />
          </ExternalEditIdeaEditorPane>
        </div>
      </ExternalEditIdeaFrame>
      <ConfirmDialog
        open={confirmClose}
        onOpenChange={(open) => {
          if (!open) setConfirmClose(false);
        }}
        title={t("externalEdit.merge.closeDirtyTitle")}
        description={t("externalEdit.merge.closeDirtyDesc")}
        cancelText={t("action.cancel")}
        confirmText={t("externalEdit.merge.closeDirtyConfirm")}
        onConfirm={() => {
          setConfirmClose(false);
          setWorkbenchState((current) => {
            const base = getCurrentMergeState(current, mergeResult);
            if (!base.dirty) return base;
            return { ...base, dirty: false };
          });
          onClose();
        }}
      />
    </>
  );
}
