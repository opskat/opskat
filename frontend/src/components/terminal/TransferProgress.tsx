import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
  X,
  Loader2,
  CheckCircle2,
  XCircle,
  Upload,
  Download,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { useSFTPStore, SFTPTransfer } from "@/stores/sftpStore";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + " " + units[i];
}

function TransferItem({ transfer }: { transfer: SFTPTransfer }) {
  const { t } = useTranslation();
  const cancelTransfer = useSFTPStore((s) => s.cancelTransfer);
  const clearTransfer = useSFTPStore((s) => s.clearTransfer);

  const percent =
    transfer.bytesTotal > 0
      ? Math.round((transfer.bytesDone / transfer.bytesTotal) * 100)
      : 0;

  return (
    <div className="flex items-start gap-2 py-1.5 text-xs">
      <div className="mt-0.5 shrink-0">
        {transfer.status === "active" && (
          <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />
        )}
        {transfer.status === "done" && (
          <CheckCircle2 className="h-3.5 w-3.5 text-green-500" />
        )}
        {(transfer.status === "error" || transfer.status === "cancelled") && (
          <XCircle className="h-3.5 w-3.5 text-destructive" />
        )}
      </div>

      <div className="flex-1 min-w-0 space-y-0.5">
        <div className="flex items-center justify-between gap-2">
          <span className="truncate font-medium">
            {transfer.currentFile ||
              (transfer.direction === "upload"
                ? t("sftp.upload")
                : t("sftp.download"))}
          </span>
          <span className="shrink-0 text-muted-foreground">
            {transfer.status === "active" && `${percent}%`}
            {transfer.status === "done" && t("sftp.completed")}
            {transfer.status === "error" && t("sftp.failed")}
            {transfer.status === "cancelled" && t("sftp.cancelled")}
          </span>
        </div>
        {transfer.status === "active" && (
          <div className="flex items-center gap-2">
            <div className="flex-1 h-1 rounded-full bg-muted overflow-hidden">
              <div
                className="h-full rounded-full bg-primary transition-all duration-300"
                style={{ width: `${percent}%` }}
              />
            </div>
            <span className="text-muted-foreground shrink-0">
              {formatBytes(transfer.speed)}/s
            </span>
          </div>
        )}
        {transfer.filesTotal > 1 && transfer.status === "active" && (
          <span className="text-muted-foreground">
            {t("sftp.filesProgress", {
              completed: transfer.filesCompleted,
              total: transfer.filesTotal,
            })}
          </span>
        )}
        {transfer.status === "error" && transfer.error && (
          <span
            className="text-destructive truncate block"
            title={transfer.error}
          >
            {transfer.error}
          </span>
        )}
      </div>

      {transfer.status === "active" ? (
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 mt-0.5"
          onClick={() => cancelTransfer(transfer.transferId)}
          title={t("sftp.cancelTransfer")}
        >
          <X className="h-3 w-3" />
        </Button>
      ) : (
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 mt-0.5"
          onClick={() => clearTransfer(transfer.transferId)}
          title={t("sftp.clear")}
        >
          <X className="h-3 w-3" />
        </Button>
      )}
    </div>
  );
}

export function TransferIndicator({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation();
  const allTransfers = useSFTPStore((s) => s.transfers);
  const clearCompleted = useSFTPStore((s) => s.clearCompleted);

  const transfers = useMemo(
    () => Object.values(allTransfers).filter((t) => t.sessionId === sessionId),
    [allTransfers, sessionId]
  );

  if (transfers.length === 0) return null;

  const active = transfers.filter((t) => t.status === "active");
  const finished = transfers.filter((t) => t.status !== "active");
  const hasError = transfers.some(
    (t) => t.status === "error" || t.status === "cancelled"
  );
  const uploading = active.filter((t) => t.direction === "upload").length;
  const downloading = active.filter((t) => t.direction === "download").length;

  // Overall progress for active transfers
  const totalBytes = active.reduce((s, t) => s + t.bytesTotal, 0);
  const doneBytes = active.reduce((s, t) => s + t.bytesDone, 0);
  const overallPercent =
    totalBytes > 0 ? Math.round((doneBytes / totalBytes) * 100) : 0;

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button className="flex items-center gap-1 px-1.5 py-0.5 rounded text-xs hover:bg-muted/50 transition-colors">
          {active.length > 0 ? (
            <>
              <Loader2 className="h-3 w-3 animate-spin text-primary" />
              <span className="text-muted-foreground">
                {uploading > 0 && (
                  <span className="inline-flex items-center gap-0.5">
                    <Upload className="h-2.5 w-2.5" />
                    {uploading}
                  </span>
                )}
                {uploading > 0 && downloading > 0 && " "}
                {downloading > 0 && (
                  <span className="inline-flex items-center gap-0.5">
                    <Download className="h-2.5 w-2.5" />
                    {downloading}
                  </span>
                )}
                {totalBytes > 0 && ` ${overallPercent}%`}
              </span>
            </>
          ) : hasError ? (
            <XCircle className="h-3 w-3 text-destructive" />
          ) : (
            <CheckCircle2 className="h-3 w-3 text-green-500" />
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent
        className="w-80 p-0"
        align="end"
        side="top"
        sideOffset={8}
      >
        <ScrollArea className="max-h-64">
          <div className="p-3 space-y-0.5">
            {active.length > 0 && (
              <>
                {active.map((t) => (
                  <TransferItem key={t.transferId} transfer={t} />
                ))}
              </>
            )}
            {active.length > 0 && finished.length > 0 && (
              <div className="border-t my-1.5" />
            )}
            {finished.map((t) => (
              <TransferItem key={t.transferId} transfer={t} />
            ))}
          </div>
        </ScrollArea>
        {finished.length > 0 && (
          <div className="border-t px-3 py-2">
            <Button
              variant="ghost"
              size="xs"
              className="w-full"
              onClick={clearCompleted}
            >
              {t("sftp.clear")}
            </Button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
