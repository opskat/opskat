import { forwardRef } from "react";
import { useTranslation } from "react-i18next";
import { ScrollText, Square, Play } from "lucide-react";
import { K8sSectionCard } from "./K8sSectionCard";
import { K8sLogTerminal, type K8sLogTerminalHandle } from "./K8sLogTerminal";

interface K8sLogsPanelProps {
  containers: { name: string }[];
  namespace: string;
  podName: string;
  logContainer: string | null;
  logTailLines: number;
  logStreamID: string | null;
  logError: string | null;
  onContainerChange: (container: string) => void;
  onTailLinesChange: (lines: number) => void;
  onStart: () => void;
  onStop: () => void;
}

export const K8sLogsPanel = forwardRef<K8sLogTerminalHandle, K8sLogsPanelProps>(function K8sLogsPanel(
  {
    containers,
    logContainer,
    logTailLines,
    logStreamID,
    logError,
    onContainerChange,
    onTailLinesChange,
    onStart,
    onStop,
  },
  ref
) {
  const { t } = useTranslation();

  return (
    <K8sSectionCard>
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
          <ScrollText className="h-3.5 w-3.5" />
          {t("asset.k8sPodLogs")}
        </h4>
        <div className="flex items-center gap-2">
          <select
            className="h-7 rounded-md border bg-background px-2 text-xs"
            value={logContainer || containers[0]?.name || ""}
            onChange={(e) => onContainerChange(e.target.value)}
            disabled={!!logStreamID}
          >
            {containers.map((c) => (
              <option key={c.name} value={c.name}>
                {c.name}
              </option>
            ))}
          </select>
          <input
            type="number"
            className="h-7 w-16 rounded-md border bg-background px-2 text-xs"
            value={logTailLines}
            onChange={(e) => onTailLinesChange(Number(e.target.value))}
            disabled={!!logStreamID}
            min={1}
            max={10000}
            title={t("asset.k8sPodLogsTailLines")}
          />
          {logStreamID ? (
            <button
              onClick={onStop}
              className="inline-flex items-center gap-1.5 rounded-md border border-destructive/50 px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10"
            >
              <Square className="h-3 w-3" />
              {t("asset.k8sPodLogsStop")}
            </button>
          ) : (
            <button
              onClick={onStart}
              className="inline-flex items-center gap-1.5 rounded-md border border-primary/50 px-3 py-1.5 text-xs text-primary hover:bg-primary/10"
            >
              <Play className="h-3 w-3" />
              {t("asset.k8sPodLogsStart")}
            </button>
          )}
        </div>
      </div>
      {logError && (
        <div className="rounded-lg border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive mb-3">
          {t("asset.k8sPodLogsError")}: {logError}
        </div>
      )}
      <K8sLogTerminal ref={ref} />
    </K8sSectionCard>
  );
})
