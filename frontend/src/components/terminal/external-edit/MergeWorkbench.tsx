import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type * as MonacoNS from "monaco-editor";
import { Button, ConfirmDialog } from "@opskat/ui";
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

interface MergeWorkbenchProps {
  mergeResult: ExternalEditMergePrepareResult;
  savingSessionId: string | null;
  onClose: () => void;
  onError: (error: unknown) => void;
}

export function ExternalEditMergeWorkbench({ mergeResult, savingSessionId, onClose, onError }: MergeWorkbenchProps) {
  const { t } = useTranslation();
  const applyMerge = useExternalEditStore((s) => s.applyMerge);

  const [finalContent, setFinalContent] = useState(mergeResult.finalContent);
  const [dirty, setDirty] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);
  const [activeBlockIndex, setActiveBlockIndex] = useState(0);
  const [navigationToken, setNavigationToken] = useState(0);
  const [editorMountVersion, setEditorMountVersion] = useState(0);

  const editorRefs = useRef<MergeEditorRefs>({
    local: { editor: null, monaco: null },
    final: { editor: null, monaco: null },
    remote: { editor: null, monaco: null },
  });
  const decorationRefs = useRef<MergeDecorationRefs>({ local: null, final: null, remote: null });
  const revealFrameRef = useRef<number | null>(null);

  const conflictBlocks = useMemo(
    () => buildTextDiffBlocks(mergeResult.remoteContent || "", mergeResult.localContent || ""),
    [mergeResult.localContent, mergeResult.remoteContent]
  );
  const conflictTotal = conflictBlocks.length;

  useEffect(() => {
    setFinalContent(mergeResult.finalContent);
    setDirty(false);
    setActiveBlockIndex(0);
    setNavigationToken((token) => token + 1);
  }, [mergeResult]);

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
    if (revealFrameRef.current != null) {
      cancelAnimationFrame(revealFrameRef.current);
    }
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
      if (revealFrameRef.current != null) {
        cancelAnimationFrame(revealFrameRef.current);
        revealFrameRef.current = null;
      }
    };
  }, [activeBlockIndex, conflictBlocks, navigationToken, editorMountVersion]);

  const navigate = (direction: -1 | 1) => {
    if (conflictTotal === 0) return;
    setActiveBlockIndex((current) => {
      const next = Math.min(Math.max(current + direction, 0), conflictTotal - 1);
      if (next !== current) {
        setNavigationToken((token) => token + 1);
      }
      return next;
    });
  };

  const handleEditorMount = useCallback(
    (pane: MergePaneRole) => (editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: typeof MonacoNS) => {
      editorRefs.current[pane] = { editor, monaco };
      setEditorMountVersion((version) => version + 1);
    },
    []
  );

  const handleAcceptLocal = useCallback(() => {
    if (conflictBlocks.length === 0) return;
    const block = conflictBlocks[Math.min(Math.max(activeBlockIndex, 0), conflictBlocks.length - 1)];
    const localLines = (mergeResult.localContent || "").split("\n");
    const finalLines = finalContent.split("\n");
    const { startLine: localStart, endLine: localEnd } = blockLineRange(block, "local");
    const { startLine: finalStart, endLine: finalEnd } = blockLineRange(block, "final");
    const replacement = block.kind === "delete" ? [] : localLines.slice(localStart - 1, localEnd);
    const before = finalLines.slice(0, finalStart - 1);
    const after = finalEnd >= finalStart ? finalLines.slice(finalEnd) : finalLines.slice(finalStart - 1);
    setFinalContent([...before, ...replacement, ...after].join("\n"));
    setDirty(true);
  }, [activeBlockIndex, conflictBlocks, finalContent, mergeResult.localContent]);

  const handleAcceptRemote = useCallback(() => {
    if (conflictBlocks.length === 0) return;
    const block = conflictBlocks[Math.min(Math.max(activeBlockIndex, 0), conflictBlocks.length - 1)];
    const remoteLines = (mergeResult.remoteContent || "").split("\n");
    const finalLines = finalContent.split("\n");
    const { startLine: remoteStart, endLine: remoteEnd } = blockLineRange(block, "remote");
    const { startLine: finalStart, endLine: finalEnd } = blockLineRange(block, "final");
    const replacement = block.kind === "insert" ? [] : remoteLines.slice(remoteStart - 1, remoteEnd);
    const before = finalLines.slice(0, finalStart - 1);
    const after = finalEnd >= finalStart ? finalLines.slice(finalEnd) : finalLines.slice(finalStart - 1);
    setFinalContent([...before, ...replacement, ...after].join("\n"));
    setDirty(true);
  }, [activeBlockIndex, conflictBlocks, finalContent, mergeResult.remoteContent]);

  const handleApply = async () => {
    try {
      await applyMerge(mergeResult.primaryDraftSessionId, finalContent, mergeResult.remoteHash);
      setDirty(false);
      onClose();
    } catch (error) {
      onError(error);
    }
  };

  const handleOpenChange = (open: boolean) => {
    if (open) return;
    if (dirty) {
      setConfirmClose(true);
      return;
    }
    onClose();
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
        status={t("externalEdit.merge.status")}
        testId="external-edit-merge-workbench"
        title={t("externalEdit.merge.title")}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="xs"
              className="border-slate-600 bg-transparent text-slate-200 hover:bg-slate-700 hover:text-white"
              disabled={conflictTotal === 0 || activeBlockIndex === 0}
              onClick={() => navigate(-1)}
            >
              {t("externalEdit.merge.previous")}
            </Button>
            <div
              className="min-w-14 rounded border border-slate-600 bg-slate-800 px-2 py-1 text-center text-xs text-slate-200"
              data-testid="external-edit-merge-conflict-count"
            >
              {conflictTotal === 0 ? "0 / 0" : `${activeBlockIndex + 1} / ${conflictTotal}`}
            </div>
            <Button
              variant="outline"
              size="xs"
              className="border-slate-600 bg-transparent text-slate-200 hover:bg-slate-700 hover:text-white"
              disabled={conflictTotal === 0 || activeBlockIndex >= conflictTotal - 1}
              onClick={() => navigate(1)}
            >
              {t("externalEdit.merge.next")}
            </Button>
            <Button
              variant="outline"
              className="border-slate-600 bg-transparent text-slate-200 hover:bg-slate-700 hover:text-white"
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
          className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)_minmax(0,1fr)] gap-px bg-slate-700"
          data-idea-layout="three-way-merge"
          data-testid="external-edit-merge-idea-layout"
        >
          <ExternalEditIdeaEditorPane
            badge={t("externalEdit.merge.readOnlySide")}
            title={t("externalEdit.merge.localDraft")}
            tone="local"
            actions={
              conflictTotal > 0 ? (
                <button
                  className="rounded bg-emerald-700/70 px-2 py-0.5 text-[10px] text-emerald-100 hover:bg-emerald-600/80 active:bg-emerald-500/80"
                  onClick={handleAcceptLocal}
                  title={t("externalEdit.merge.acceptLocal")}
                >
                  {t("externalEdit.merge.acceptLocal")}
                </button>
              ) : undefined
            }
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
                setFinalContent(value);
                setDirty(true);
              }}
              onMount={handleEditorMount("final")}
            />
          </ExternalEditIdeaEditorPane>
          <ExternalEditIdeaEditorPane
            badge={t("externalEdit.merge.readOnlySide")}
            title={t("externalEdit.merge.remoteDraft")}
            tone="remote"
            actions={
              conflictTotal > 0 ? (
                <button
                  className="rounded bg-sky-700/70 px-2 py-0.5 text-[10px] text-sky-100 hover:bg-sky-600/80 active:bg-sky-500/80"
                  onClick={handleAcceptRemote}
                  title={t("externalEdit.merge.acceptRemote")}
                >
                  {t("externalEdit.merge.acceptRemote")}
                </button>
              ) : undefined
            }
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
          setDirty(false);
          onClose();
        }}
      />
    </>
  );
}
