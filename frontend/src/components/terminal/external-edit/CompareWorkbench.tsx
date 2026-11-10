import { useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "@opskat/ui";
import { CodeDiffViewer } from "@/components/CodeDiffViewer";
import type { ExternalEditCompareResult } from "@/lib/externalEditApi";
import { ExternalEditIdeaFrame } from "./IdeaFrame";

interface CompareWorkbenchProps {
  compareResult: ExternalEditCompareResult;
  onDismiss: () => void;
}

interface CompareWorkbenchState {
  compareResult: ExternalEditCompareResult;
  activeBlockIndex: number;
  navigationToken: number;
  diffTotal: number;
}

function createCompareWorkbenchState(
  compareResult: ExternalEditCompareResult,
  navigationToken = 0
): CompareWorkbenchState {
  return {
    compareResult,
    activeBlockIndex: 0,
    navigationToken,
    diffTotal: 0,
  };
}

function getCurrentCompareState(
  state: CompareWorkbenchState,
  compareResult: ExternalEditCompareResult
): CompareWorkbenchState {
  if (state.compareResult === compareResult) return state;
  return createCompareWorkbenchState(compareResult, state.navigationToken);
}

export function ExternalEditCompareWorkbench({ compareResult, onDismiss }: CompareWorkbenchProps) {
  const { t } = useTranslation();
  const [workbenchState, setWorkbenchState] = useState(() => createCompareWorkbenchState(compareResult));
  const currentState = getCurrentCompareState(workbenchState, compareResult);
  const diffTotal = currentState.diffTotal;
  const activeBlockIndex = Math.min(currentState.activeBlockIndex, Math.max(diffTotal - 1, 0));
  const navigationToken = currentState.navigationToken;

  const navigate = (direction: -1 | 1) => {
    if (diffTotal === 0) return;
    setWorkbenchState((current) => {
      const base = getCurrentCompareState(current, compareResult);
      if (base.diffTotal === 0) return base;
      const next = Math.min(Math.max(base.activeBlockIndex + direction, 0), base.diffTotal - 1);
      if (next === base.activeBlockIndex) return base;
      return {
        ...base,
        activeBlockIndex: next,
        navigationToken: base.navigationToken + 1,
      };
    });
  };

  return (
    <ExternalEditIdeaFrame
      fileName={compareResult.fileName}
      helper={t("externalEdit.compare.localLeftHelper")}
      layoutLabel={t("externalEdit.compare.localLeftRemoteRight")}
      mode="compare"
      remotePath={compareResult.remotePath}
      sidebarLabel={t("externalEdit.compare.projectView")}
      status={t("externalEdit.compare.status")}
      testId="external-edit-compare-workbench"
      title={t("externalEdit.compare.title")}
      actions={
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="xs"
            className="border-border bg-transparent text-foreground hover:bg-accent hover:text-accent-foreground"
            disabled={diffTotal === 0 || activeBlockIndex === 0}
            onClick={() => navigate(-1)}
          >
            {t("externalEdit.compare.previous")}
          </Button>
          <div
            className="min-w-14 rounded border border-border bg-muted px-2 py-1 text-center text-xs text-foreground"
            data-testid="external-edit-compare-diff-count"
          >
            {diffTotal === 0 ? "0 / 0" : `${activeBlockIndex + 1} / ${diffTotal}`}
          </div>
          <Button
            variant="outline"
            size="xs"
            className="border-border bg-transparent text-foreground hover:bg-accent hover:text-accent-foreground"
            disabled={diffTotal === 0 || activeBlockIndex >= diffTotal - 1}
            onClick={() => navigate(1)}
          >
            {t("externalEdit.compare.next")}
          </Button>
          <Button
            size="icon"
            variant="ghost"
            className="text-muted-foreground hover:bg-accent hover:text-accent-foreground"
            onClick={onDismiss}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      }
    >
      <div
        className="flex min-h-0 flex-1 flex-col gap-2 bg-card p-2"
        data-idea-layout="read-only-diff"
        data-testid="external-edit-compare-idea-layout"
      >
        {/* Monaco 的 original 在左、modified 在右：本地作 original，远端作 modified */}
        <div
          className="grid shrink-0 grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 text-xs"
          data-testid="external-edit-compare-side-labels"
        >
          <div className="min-w-0 truncate rounded border border-success/30 bg-success/5 px-2 py-1 font-semibold text-success">
            {t("externalEdit.side.local")}
          </div>
          <div className="rounded-full border border-border bg-background px-2 py-1 text-[11px] font-medium text-muted-foreground">
            {t("externalEdit.compare.readOnly")}
          </div>
          <div className="min-w-0 truncate rounded border border-info/30 bg-info/5 px-2 py-1 text-right font-semibold text-info">
            {t("externalEdit.side.remote")}
          </div>
        </div>
        <div className="min-h-0 flex-1">
          <CodeDiffViewer
            activeBlockIndex={activeBlockIndex}
            className="border-border bg-card text-card-foreground"
            height="100%"
            language="plaintext"
            modified={compareResult.remoteContent || ""}
            navigationToken={navigationToken}
            original={compareResult.localContent || ""}
            testId="external-edit-compare-diff-editor"
            onDiffStatsChange={({ total }) => {
              setWorkbenchState((current) => {
                const base = getCurrentCompareState(current, compareResult);
                const nextActiveBlockIndex = Math.min(base.activeBlockIndex, Math.max(total - 1, 0));
                if (base.diffTotal === total && base.activeBlockIndex === nextActiveBlockIndex) return base;
                return {
                  ...base,
                  activeBlockIndex: nextActiveBlockIndex,
                  diffTotal: total,
                };
              });
            }}
          />
        </div>
      </div>
    </ExternalEditIdeaFrame>
  );
}
