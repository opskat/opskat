import { useTranslation } from "react-i18next";
import { ScrollArea, Button } from "@opskat/ui";
import { Upload, Download, Loader2, CheckCircle2, XCircle, Ban, RotateCw, X } from "lucide-react";
import { formatBytes, formatSpeed } from "@/lib/formatBytes";
import { useOssTransferStore, type OssTransfer } from "@/stores/ossTransferStore";

export interface OSSTransferDockProps {
  transfers: OssTransfer[];
  onCancel: (transferId: string) => void;
  onClear: (transferId: string) => void;
  onClearCompleted: () => void;
}

export function OSSTransferDock({ transfers, onCancel, onClear, onClearCompleted }: OSSTransferDockProps) {
  const { t } = useTranslation();
  const retryTransfer = useOssTransferStore((s) => s.retry);
  const hasCompleted = transfers.some((tr) => tr.status !== "active");
  return (
    <div className="border-t bg-muted/10" data-testid="oss-transfer-dock">
      <div className="flex items-center justify-between px-3 py-1 text-xs text-muted-foreground">
        <span>
          {t("oss.transfer.transfers")} ({transfers.length})
        </span>
        <Button
          size="sm"
          variant="ghost"
          onClick={onClearCompleted}
          disabled={!hasCompleted}
          data-testid="oss-transfer-clear-completed"
        >
          {t("oss.transfer.clearCompleted")}
        </Button>
      </div>
      <ScrollArea className="max-h-40">
        {transfers.map((tr) => {
          const percent = tr.bytesTotal ? Math.round((tr.bytesDone / tr.bytesTotal) * 100) : 0;
          const DirIcon = tr.direction === "upload" ? Upload : Download;
          const StatusIcon =
            tr.status === "active"
              ? Loader2
              : tr.status === "done"
                ? CheckCircle2
                : tr.status === "cancelled"
                  ? Ban
                  : XCircle;
          const active = tr.status === "active";
          const statusLabel = t(
            tr.status === "active"
              ? "oss.transfer.statusActive"
              : tr.status === "done"
                ? "oss.transfer.statusDone"
                : "oss.transfer.statusFailed"
          );
          const stateText = t(
            tr.status === "active"
              ? "sftp.transferring"
              : tr.status === "done"
                ? "sftp.completed"
                : tr.status === "cancelled"
                  ? "sftp.cancelled"
                  : "sftp.failed"
          );
          const statusClass = active
            ? "animate-spin text-info"
            : tr.status === "done"
              ? "text-success"
              : tr.status === "cancelled"
                ? "text-muted-foreground"
                : "text-destructive";
          const canRetry = tr.status === "error" || tr.status === "cancelled";
          return (
            <div key={tr.transferId} className="px-3 py-1 text-xs" data-testid={`oss-transfer-row-${tr.transferId}`}>
              <div className="flex items-center gap-2">
                <DirIcon className="size-3 shrink-0 text-muted-foreground" aria-hidden />
                <span className="min-w-0 flex-1 truncate" title={tr.name}>
                  {tr.name}
                </span>
                {tr.files && tr.files.total > 0 && (
                  <span className="shrink-0 text-muted-foreground" data-testid={`oss-transfer-files-${tr.transferId}`}>
                    {tr.files.done}/{tr.files.total}
                  </span>
                )}
                {active && (
                  <div
                    className="h-1 w-24 shrink-0 overflow-hidden rounded-full bg-muted"
                    role="progressbar"
                    aria-label={`${tr.name}: ${percent}%`}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={percent}
                  >
                    <div className="h-full bg-primary" style={{ width: `${percent}%` }} />
                  </div>
                )}
                {active && (
                  <span className="w-16 shrink-0 text-right text-muted-foreground">{formatSpeed(tr.speed)}</span>
                )}
                <span className="w-24 shrink-0 text-right text-muted-foreground">
                  {formatBytes(tr.bytesDone)}/{formatBytes(tr.bytesTotal)}
                </span>
                <StatusIcon
                  className={`size-3 shrink-0 ${statusClass}`}
                  aria-hidden
                  data-testid={`oss-transfer-status-${tr.transferId}`}
                />
                <span
                  className={`w-24 shrink-0 truncate ${tr.status === "error" ? "text-destructive" : "text-muted-foreground"}`}
                  data-testid={`oss-transfer-state-${tr.transferId}`}
                >
                  {stateText}
                </span>
                <span className="sr-only">{statusLabel}</span>
                {canRetry && (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-5 shrink-0 gap-1 px-1.5 text-xs"
                    onClick={() => void retryTransfer(tr.tabId, tr.transferId)}
                    data-testid={`oss-transfer-retry-${tr.transferId}`}
                  >
                    <RotateCw className="size-3" aria-hidden />
                    {t("sftp.retry")}
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  className={active ? "h-5 shrink-0 gap-1 px-1.5 text-xs" : "size-5 shrink-0 p-0"}
                  onClick={() => (active ? onCancel(tr.transferId) : onClear(tr.transferId))}
                  title={active ? t("oss.transfer.cancel") : t("oss.transfer.clear")}
                  aria-label={active ? t("oss.transfer.cancel") : t("oss.transfer.clear")}
                  data-testid={`oss-transfer-action-${tr.transferId}`}
                >
                  {active ? t("oss.transfer.cancel") : <X className="size-3" />}
                </Button>
              </div>
              {tr.status === "error" && tr.error && (
                <p
                  className="mt-0.5 break-all pl-5 text-destructive"
                  data-testid={`oss-transfer-error-${tr.transferId}`}
                >
                  {tr.error}
                </p>
              )}
            </div>
          );
        })}
      </ScrollArea>
    </div>
  );
}
