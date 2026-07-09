import { useTranslation } from "react-i18next";
import { ScrollArea, Button } from "@opskat/ui";
import { Upload, Download, Loader2, CheckCircle2, XCircle, X } from "lucide-react";
import { formatBytes, formatSpeed } from "@/lib/formatBytes";
import type { OssTransfer } from "@/stores/ossTransferStore";

export interface OSSTransferDockProps {
  transfers: OssTransfer[];
  onCancel: (transferId: string) => void;
  onClear: (transferId: string) => void;
  onClearCompleted: () => void;
}

export function OSSTransferDock({ transfers, onCancel, onClear, onClearCompleted }: OSSTransferDockProps) {
  const { t } = useTranslation();
  return (
    <div className="border-t bg-muted/10" data-testid="oss-transfer-dock">
      <div className="flex items-center justify-between px-3 py-1 text-xs text-muted-foreground">
        <span>
          {t("oss.transfer.transfers")} ({transfers.length})
        </span>
        <Button size="sm" variant="ghost" onClick={onClearCompleted} data-testid="oss-transfer-clear-completed">
          {t("oss.transfer.clearCompleted")}
        </Button>
      </div>
      <ScrollArea className="max-h-32">
        {transfers.map((tr) => {
          const percent = tr.bytesTotal ? Math.round((tr.bytesDone / tr.bytesTotal) * 100) : 0;
          const DirIcon = tr.direction === "upload" ? Upload : Download;
          const StatusIcon = tr.status === "active" ? Loader2 : tr.status === "done" ? CheckCircle2 : XCircle;
          const active = tr.status === "active";
          return (
            <div
              key={tr.transferId}
              className="flex items-center gap-2 px-3 py-1 text-xs"
              data-testid={`oss-transfer-row-${tr.transferId}`}
            >
              <DirIcon className="size-3 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate" title={tr.error ?? tr.name}>
                {tr.name}
              </span>
              <div className="h-1 w-24 shrink-0 overflow-hidden rounded-full bg-muted">
                <div className="h-full bg-primary" style={{ width: `${percent}%` }} />
              </div>
              <span className="w-16 shrink-0 text-right text-muted-foreground">{formatSpeed(tr.speed)}</span>
              <span className="w-24 shrink-0 text-right text-muted-foreground">
                {formatBytes(tr.bytesDone)}/{formatBytes(tr.bytesTotal)}
              </span>
              <StatusIcon className={`size-3 shrink-0 ${active ? "animate-spin text-muted-foreground" : ""}`} />
              <Button
                size="sm"
                variant="ghost"
                className="size-5 shrink-0 p-0"
                onClick={() => (active ? onCancel(tr.transferId) : onClear(tr.transferId))}
                title={active ? t("oss.transfer.cancel") : t("oss.transfer.clear")}
                data-testid={`oss-transfer-action-${tr.transferId}`}
              >
                <X className="size-3" />
              </Button>
            </div>
          );
        })}
      </ScrollArea>
    </div>
  );
}
