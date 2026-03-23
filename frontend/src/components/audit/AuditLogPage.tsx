import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { CheckCircle2, XCircle, ChevronLeft, ChevronRight, Info } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ListAuditLogs } from "../../../wailsjs/go/main/App";
import { audit_entity } from "../../../wailsjs/go/models";

const PAGE_SIZE = 20;
const SOURCES = ["", "ai", "opsctl", "mcp"] as const;

export function AuditLogPage() {
  const { t } = useTranslation();
  const [logs, setLogs] = useState<audit_entity.AuditLog[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [source, setSource] = useState("");
  const [loading, setLoading] = useState(false);
  const [detailLog, setDetailLog] = useState<audit_entity.AuditLog | null>(null);

  const fetchLogs = useCallback(async () => {
    setLoading(true);
    try {
      const result = await ListAuditLogs(source, 0, page * PAGE_SIZE, PAGE_SIZE);
      setLogs(result?.items || []);
      setTotal(result?.total || 0);
    } finally {
      setLoading(false);
    }
  }, [source, page]);

  useEffect(() => {
    fetchLogs();
  }, [fetchLogs]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const formatTime = (ts: number) => {
    if (!ts) return "-";
    const d = new Date(ts * 1000);
    return d.toLocaleString();
  };

  const truncate = (s: string, max = 60) => {
    if (!s) return "-";
    return s.length > max ? s.slice(0, max) + "..." : s;
  };

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="px-4 py-3 border-b flex items-center justify-between gap-4">
        <h2 className="font-semibold shrink-0">{t("audit.title")}</h2>
        <div className="flex items-center gap-2">
          <Select
            value={source}
            onValueChange={(v) => {
              setSource(v === "all" ? "" : v);
              setPage(0);
            }}
          >
            <SelectTrigger className="w-32 h-8 text-sm">
              <SelectValue placeholder={t("audit.source")} />
            </SelectTrigger>
            <SelectContent>
              {SOURCES.map((s) => (
                <SelectItem key={s || "all"} value={s || "all"}>
                  {s ? s.toUpperCase() : t("audit.sourceAll")}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-xs text-muted-foreground">
            {t("audit.total", { total })}
          </span>
        </div>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 bg-background border-b">
            <tr className="text-left text-muted-foreground">
              <th className="px-4 py-2 font-medium">{t("audit.time")}</th>
              <th className="px-4 py-2 font-medium">{t("audit.source")}</th>
              <th className="px-4 py-2 font-medium">{t("audit.toolName")}</th>
              <th className="px-4 py-2 font-medium">{t("audit.assetName")}</th>
              <th className="px-4 py-2 font-medium">{t("audit.command")}</th>
              <th className="px-4 py-2 font-medium w-16 text-center">{t("audit.result")}</th>
              <th className="px-4 py-2 font-medium w-16"></th>
            </tr>
          </thead>
          <tbody>
            {logs.length === 0 && !loading && (
              <tr>
                <td colSpan={7} className="px-4 py-12 text-center text-muted-foreground">
                  {t("audit.empty")}
                </td>
              </tr>
            )}
            {logs.map((log) => (
              <tr
                key={log.ID}
                className="border-b hover:bg-muted/50 transition-colors"
              >
                <td className="px-4 py-2 text-xs text-muted-foreground whitespace-nowrap">
                  {formatTime(log.Createtime)}
                </td>
                <td className="px-4 py-2">
                  <span className="inline-block px-1.5 py-0.5 text-xs rounded bg-muted font-mono">
                    {log.Source}
                  </span>
                </td>
                <td className="px-4 py-2 font-mono text-xs">{log.ToolName}</td>
                <td className="px-4 py-2">{log.AssetName || "-"}</td>
                <td className="px-4 py-2 font-mono text-xs max-w-48 truncate" title={log.Command}>
                  {truncate(log.Command)}
                </td>
                <td className="px-4 py-2 text-center">
                  {log.Success === 1 ? (
                    <CheckCircle2 className="h-4 w-4 text-green-500 mx-auto" />
                  ) : (
                    <XCircle className="h-4 w-4 text-destructive mx-auto" />
                  )}
                </td>
                <td className="px-4 py-2">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => setDetailLog(log)}
                  >
                    <Info className="h-3.5 w-3.5" />
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="px-4 py-2 border-t flex items-center justify-center gap-2">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            disabled={page === 0}
            onClick={() => setPage(page - 1)}
          >
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <span className="text-sm text-muted-foreground">
            {page + 1} / {totalPages}
          </span>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            disabled={page >= totalPages - 1}
            onClick={() => setPage(page + 1)}
          >
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      )}

      {/* Detail dialog */}
      <Dialog open={!!detailLog} onOpenChange={(open) => !open && setDetailLog(null)}>
        <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t("audit.detail")}</DialogTitle>
          </DialogHeader>
          {detailLog && (
            <div className="space-y-3 text-sm">
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <span className="text-muted-foreground">{t("audit.source")}:</span>{" "}
                  <span className="font-mono">{detailLog.Source}</span>
                </div>
                <div>
                  <span className="text-muted-foreground">{t("audit.toolName")}:</span>{" "}
                  <span className="font-mono">{detailLog.ToolName}</span>
                </div>
                <div>
                  <span className="text-muted-foreground">{t("audit.assetName")}:</span>{" "}
                  {detailLog.AssetName || "-"}
                </div>
                <div>
                  <span className="text-muted-foreground">{t("audit.result")}:</span>{" "}
                  {detailLog.Success === 1 ? (
                    <span className="text-green-500">{t("audit.success")}</span>
                  ) : (
                    <span className="text-destructive">{t("audit.failed")}</span>
                  )}
                </div>
                <div className="col-span-2">
                  <span className="text-muted-foreground">{t("audit.time")}:</span>{" "}
                  {formatTime(detailLog.Createtime)}
                </div>
              </div>

              {detailLog.Command && (
                <div>
                  <div className="text-muted-foreground mb-1">{t("audit.command")}</div>
                  <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all">
                    {detailLog.Command}
                  </pre>
                </div>
              )}

              {detailLog.Request && (
                <div>
                  <div className="text-muted-foreground mb-1">{t("audit.request")}</div>
                  <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all max-h-40 overflow-y-auto">
                    {detailLog.Request}
                  </pre>
                </div>
              )}

              {detailLog.Result && (
                <div>
                  <div className="text-muted-foreground mb-1">{t("audit.response")}</div>
                  <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all max-h-40 overflow-y-auto">
                    {detailLog.Result}
                  </pre>
                </div>
              )}

              {detailLog.Error && (
                <div>
                  <div className="text-muted-foreground mb-1">{t("audit.error")}</div>
                  <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all text-destructive">
                    {detailLog.Error}
                  </pre>
                </div>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
