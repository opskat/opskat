import { useTranslation } from "react-i18next";
import { Ban, CheckCircle2, Download, FolderOpen, Loader2, RotateCw, Upload, X, XCircle } from "lucide-react";
import { Button, ScrollArea } from "@opskat/ui";
import { type SFTPTransfer, useSFTPStore } from "@/stores/sftpStore";
import { formatBytes, formatSpeed } from "@/lib/formatBytes";
import { OpenDirectory } from "../../../../wailsjs/go/system/System";

interface TransferSectionProps {
  tabId: string;
  transfers: SFTPTransfer[];
}

/** 下载完成后「所在目录」：文件取其父目录，目录下载本身就是目标目录。兼容 / 与 \。 */
function containingFolder(transfer: SFTPTransfer): string | null {
  if (transfer.status !== "done" || transfer.direction !== "download" || !transfer.localPath) return null;
  if (transfer.retry?.kind === "downloadDir") return transfer.localPath;
  const i = Math.max(transfer.localPath.lastIndexOf("/"), transfer.localPath.lastIndexOf("\\"));
  return i > 0 ? transfer.localPath.slice(0, i) : null;
}

const STATE_KEY = {
  active: "sftp.transferring",
  done: "sftp.completed",
  error: "sftp.failed",
  cancelled: "sftp.cancelled",
} as const;

function TransferRow({ transfer }: { transfer: SFTPTransfer }) {
  const { t } = useTranslation();
  const cancelTransfer = useSFTPStore((s) => s.cancelTransfer);
  const clearTransfer = useSFTPStore((s) => s.clearTransfer);
  const retryTransfer = useSFTPStore((s) => s.retryTransfer);

  const active = transfer.status === "active";
  const percent = transfer.bytesTotal > 0 ? Math.round((transfer.bytesDone / transfer.bytesTotal) * 100) : 0;
  const fileName = transfer.currentFile ? transfer.currentFile.split("/").pop() || transfer.currentFile : "";
  const folder = containingFolder(transfer);
  const canRetry = (transfer.status === "error" || transfer.status === "cancelled") && !!transfer.retry;
  const StatusIcon = active
    ? Loader2
    : transfer.status === "done"
      ? CheckCircle2
      : transfer.status === "cancelled"
        ? Ban
        : XCircle;
  const iconClass = active
    ? "animate-spin text-primary"
    : transfer.status === "done"
      ? "text-success"
      : transfer.status === "cancelled"
        ? "text-muted-foreground"
        : "text-destructive";
  const DirIcon = transfer.direction === "upload" ? Upload : Download;

  return (
    <div className="text-xs space-y-0.5" data-testid={`sftp-transfer-row-${transfer.transferId}`}>
      <div className="flex items-center gap-1.5">
        <StatusIcon className={`h-3 w-3 shrink-0 ${iconClass}`} aria-hidden />
        <DirIcon className="h-3 w-3 text-muted-foreground shrink-0" aria-hidden />
        <span className="truncate min-w-0 flex-1" title={transfer.currentFile}>
          {fileName}
        </span>
        <span
          className={`shrink-0 ${transfer.status === "error" ? "text-destructive" : "text-muted-foreground"}`}
          data-testid={`sftp-transfer-state-${transfer.transferId}`}
        >
          {t(STATE_KEY[transfer.status])}
          {active && transfer.bytesTotal > 0 ? ` ${percent}%` : ""}
        </span>
        {!active && (
          <Button
            variant="ghost"
            size="icon-xs"
            className="shrink-0 h-5 w-5"
            onClick={() => clearTransfer(transfer.transferId)}
            title={t("sftp.clear")}
            aria-label={t("sftp.clear")}
          >
            <X className="h-3 w-3" />
          </Button>
        )}
      </div>

      {active && (
        <div className="h-1 rounded-full bg-muted overflow-hidden">
          <div
            className="h-full rounded-full bg-primary transition-all duration-300"
            style={{ width: `${percent}%` }}
          />
        </div>
      )}
      {(active || transfer.bytesTotal > 0) && (
        <div className={`flex items-center gap-2 text-muted-foreground ${active ? "" : "pl-[18px]"}`}>
          <span className="truncate">
            {formatBytes(transfer.bytesDone)}/{formatBytes(transfer.bytesTotal)}
          </span>
          {active && (
            <>
              <span className="shrink-0">{formatSpeed(transfer.speed)}</span>
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-5 shrink-0 px-1.5 text-xs"
                onClick={() => cancelTransfer(transfer.transferId)}
              >
                {t("sftp.cancelTransfer")}
              </Button>
            </>
          )}
        </div>
      )}

      {transfer.status === "error" && transfer.error && (
        <p className="text-destructive break-all pl-[18px]">{transfer.error}</p>
      )}

      {(canRetry || folder) && (
        <div className="flex items-center gap-1 pl-[18px]">
          {canRetry && (
            <Button
              variant="ghost"
              size="sm"
              className="h-5 gap-1 px-1.5 text-xs"
              onClick={() => void retryTransfer(transfer.transferId)}
            >
              <RotateCw className="h-3 w-3" aria-hidden />
              {t("sftp.retry")}
            </Button>
          )}
          {folder && (
            <Button
              variant="ghost"
              size="sm"
              className="h-5 gap-1 px-1.5 text-xs"
              onClick={() => void OpenDirectory(folder)}
            >
              <FolderOpen className="h-3 w-3" aria-hidden />
              {t("sftp.showInFolder")}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

export function TransferSection({ tabId, transfers }: TransferSectionProps) {
  const { t } = useTranslation();
  const clearCompletedForTab = useSFTPStore((s) => s.clearCompletedForTab);

  if (transfers.length === 0) {
    return null;
  }

  return (
    <div className="border-t shrink-0">
      <div className="flex items-center justify-between px-2 py-0.5">
        <span className="text-xs font-medium text-muted-foreground">{t("sftp.transfers")}</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-5 px-1.5 text-xs"
          onClick={() => clearCompletedForTab(tabId)}
          disabled={!transfers.some((tr) => tr.status !== "active")}
        >
          {t("sftp.clearCompleted")}
        </Button>
      </div>
      <ScrollArea className="max-h-48">
        <div className="px-2 pb-1 space-y-1.5">
          {transfers.map((transfer) => (
            <TransferRow key={transfer.transferId} transfer={transfer} />
          ))}
        </div>
      </ScrollArea>
    </div>
  );
}
