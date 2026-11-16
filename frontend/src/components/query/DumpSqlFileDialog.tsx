import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, Download, ExternalLink, FolderOpen, Loader2 } from "lucide-react";
import { toast } from "sonner";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@opskat/ui";
import { CancelDumpSQLFile, SelectDumpFile, StartDumpSQLFile } from "../../../wailsjs/go/query/Query";
import { OpenDirectory } from "../../../wailsjs/go/system/System";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { errorMessage } from "@/lib/errorMessage";
import { formatBytes } from "@/lib/formatBytes";

/** 后端发出转储事件的事件名(Go 侧 query.DumpSQLFileEventName)。 */
export const DUMP_SQL_FILE_EVENT = "query:dump-sql-file";

/** 后端的一个转储事件(Go 侧 query.DumpSQLFileEvent)。 */
export interface DumpSqlFileEvent {
  dumpId: string;
  status: "running" | "done" | "failed" | "canceled";
  progress: {
    objects: number;
    object: number;
    name: string;
    rows: number;
    /** 一个对象写完时给出它写了什么。 */
    finished?: { name: string; kind: string; data: boolean; rows: number };
  };
  elapsedMs: number;
  result?: { objects: number; rows: number; bytes: number };
  /** failed / canceled 时停在的对象。 */
  object?: string;
  error?: string;
}

/** 要转储的对象:选中的表与视图(树上的名字,按选中的次序)、整个库,或整个 schema。 */
export type DumpTarget =
  { kind: "tables"; tables: string[] } | { kind: "database" } | { kind: "schema"; schema: string };

interface DumpSqlFileDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetId: number;
  assetName: string;
  database: string;
  target: DumpTarget;
  /** 「转储内容」的初始值:取自所选的子菜单项。 */
  withData: boolean;
  /** 驱动能生成行语句时才有「结构和数据」。 */
  canDumpData: boolean;
}

type Phase = "idle" | "starting" | "running" | "finished";
type Content = "data" | "structure";

/** 默认文件名:单张表是 <表名>.sql,其余是 <库名或 schema 名>_<YYYYMMDD>.sql。 */
function defaultFileName(database: string, target: DumpTarget): string {
  if (target.kind === "tables" && target.tables.length === 1) return `${target.tables[0]}.sql`;
  const now = new Date();
  const date = `${now.getFullYear()}${String(now.getMonth() + 1).padStart(2, "0")}${String(now.getDate()).padStart(2, "0")}`;
  return `${target.kind === "schema" ? target.schema : database}_${date}.sql`;
}

/** 副标题里要转储的对象:选中的表名,或「整个库」「整个 schema」。 */
function targetObjects(t: (key: string, opts?: Record<string, unknown>) => string, target: DumpTarget): string {
  switch (target.kind) {
    case "tables":
      return target.tables.join(", ");
    case "database":
      return t("query.dumpSqlFileWholeDatabase");
    case "schema":
      return t("query.dumpSqlFileWholeSchema", { schema: target.schema });
  }
}

function containingDirectory(filePath: string): string {
  const slash = Math.max(filePath.lastIndexOf("/"), filePath.lastIndexOf("\\"));
  return slash <= 0 ? filePath : filePath.slice(0, slash);
}

/** 打开文件或所在文件夹;失败要让用户看到。 */
async function openPath(path: string) {
  try {
    await OpenDirectory(path);
  } catch (e) {
    toast.error(errorMessage(e));
  }
}

/** 取消一次转储;取消不了是用户该知道的事。 */
async function requestCancel(dumpId: string) {
  try {
    await CancelDumpSQLFile(dumpId);
  } catch (e) {
    toast.error(errorMessage(e));
  }
}

export function DumpSqlFileDialog({
  open,
  onOpenChange,
  assetId,
  assetName,
  database,
  target: dumpTarget,
  withData,
  canDumpData,
}: DumpSqlFileDialogProps) {
  const { t } = useTranslation();
  const [content, setContent] = useState<Content>(withData && canDumpData ? "data" : "structure");
  const [path, setPath] = useState("");
  const [phase, setPhase] = useState<Phase>("idle");
  const [latest, setLatest] = useState<DumpSqlFileEvent | null>(null);
  const [startError, setStartError] = useState("");
  const [logLines, setLogLines] = useState<string[]>([]);
  const [cancelling, setCancelling] = useState(false);
  /** 已经写完的对象数:进度条按它走。 */
  const [finishedObjects, setFinishedObjects] = useState(0);
  const unsubscribeRef = useRef<(() => void) | null>(null);
  /** 进行中的转储 id(未开始或已结束时为 null);对话框消失(关闭资产标签页)时据此取消它。 */
  const activeDumpRef = useRef<string | null>(null);
  const mountedRef = useRef(true);

  const stopListening = useCallback(() => {
    unsubscribeRef.current?.();
    unsubscribeRef.current = null;
  }, []);
  useEffect(
    () => () => {
      mountedRef.current = false;
      stopListening();
      if (activeDumpRef.current) void requestCancel(activeDumpRef.current);
    },
    [stopListening]
  );

  const busy = phase === "starting" || phase === "running";
  const target = `${assetName} / ${database}`;

  const apply = useCallback(
    (event: DumpSqlFileEvent) => {
      setLatest(event);
      const lines: string[] = [];
      const finished = event.progress.finished;
      if (finished) {
        lines.push(`[DMP] ${finished.name}: ${t("query.dumpSqlFileLogStructure")}`);
        if (finished.data) {
          lines.push(
            `[DMP] ${finished.name}: ${t("query.dumpSqlFileLogData", { count: finished.rows.toLocaleString() })}`
          );
        }
        setFinishedObjects((count) => count + 1);
      }
      if (event.status === "running") {
        if (lines.length) setLogLines((prev) => [...prev, ...lines]);
        return;
      }
      activeDumpRef.current = null;
      if (event.status === "done") {
        lines.push(`[DMP] ${t("query.dumpSqlFileLogDone", { seconds: (event.elapsedMs / 1000).toFixed(1) })}`);
      } else if (event.status === "failed") {
        lines.push(`[ERR] ${event.object ? `${event.object}: ` : ""}${event.error ?? ""}`);
      } else {
        lines.push(`[DMP] ${t("query.dumpSqlFileLogCanceled")}`);
      }
      setLogLines((prev) => [...prev, ...lines]);
      stopListening();
      setCancelling(false);
      setPhase("finished");
    },
    [stopListening, t]
  );

  const handleChooseLocation = useCallback(async () => {
    try {
      const chosen = await SelectDumpFile(defaultFileName(database, dumpTarget));
      if (chosen) setPath(chosen);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }, [database, dumpTarget]);

  const handleStart = useCallback(async () => {
    if (!path) return;
    const dumpData = content === "data";
    setPhase("starting");
    setStartError("");
    setCancelling(false);
    setLatest(null);
    setFinishedObjects(0);
    setLogLines([
      `[DMP] ${t("query.dumpSqlFileLogStart", {
        target,
        content: t(dumpData ? "query.dumpSqlFileStructureAndData" : "query.dumpSqlFileStructureOnly"),
      })}`,
    ]);
    // 先订阅再开始:转储 id 回来之前到达的事件先攒着,拿到 id 后只认自己的。
    let dumpId: string | null = null;
    const early: DumpSqlFileEvent[] = [];
    unsubscribeRef.current = EventsOn(DUMP_SQL_FILE_EVENT, (event: DumpSqlFileEvent) => {
      if (dumpId === null) early.push(event);
      else if (event.dumpId === dumpId) apply(event);
    });
    try {
      dumpId = await StartDumpSQLFile(assetId, {
        database,
        schema: dumpTarget.kind === "schema" ? dumpTarget.schema : "",
        tables: dumpTarget.kind === "tables" ? dumpTarget.tables : [],
        whole: dumpTarget.kind !== "tables",
        withData: dumpData,
        path,
      });
    } catch (e) {
      stopListening();
      setStartError(errorMessage(e));
      setPhase("idle");
      return;
    }
    if (!mountedRef.current) {
      // 等转储 id 的时候对话框已经消失(资产标签页关了):刚开始的转储不留着。
      void requestCancel(dumpId);
      return;
    }
    activeDumpRef.current = dumpId;
    setPhase("running");
    for (const event of early) {
      if (event.dumpId === dumpId) apply(event);
    }
  }, [apply, assetId, content, database, dumpTarget, path, stopListening, t, target]);

  const handleCancel = useCallback(() => {
    if (!activeDumpRef.current) return;
    setCancelling(true);
    void requestCancel(activeDumpRef.current);
  }, []);

  // 整库或整个 schema 有几个对象要等第一个事件才知道。
  const objects = latest?.progress.objects || (dumpTarget.kind === "tables" ? dumpTarget.tables.length : 0);
  const done = latest?.status === "done";
  const stopped = latest?.status === "failed" || latest?.status === "canceled";
  const percent = done ? 100 : objects === 0 ? 0 : Math.min(100, Math.round((finishedObjects / objects) * 100));

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!busy) onOpenChange(next);
      }}
    >
      <DialogContent
        className="max-h-[85vh] overflow-y-auto"
        showCloseButton={!busy}
        onEscapeKeyDown={(e) => busy && e.preventDefault()}
        onInteractOutside={(e) => busy && e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{t("query.dumpSqlFileTitle")}</DialogTitle>
          <DialogDescription>
            {t("query.dumpSqlFileDesc", { target, objects: targetObjects(t, dumpTarget) })}
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-1.5">
            <Label className="text-xs">{t("query.dumpSqlFileContent")}</Label>
            <Select value={content} disabled={phase !== "idle"} onValueChange={(value) => setContent(value as Content)}>
              <SelectTrigger size="sm" className="h-8 w-48 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {canDumpData && (
                  <SelectItem value="data" className="text-xs">
                    {t("query.dumpSqlFileStructureAndData")}
                  </SelectItem>
                )}
                <SelectItem value="structure" className="text-xs">
                  {t("query.dumpSqlFileStructureOnly")}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-2">
            <div className="flex items-center justify-between gap-2">
              <Label className="text-xs">{t("query.exportFile")}</Label>
              <Button
                variant="outline"
                size="sm"
                className="h-7 gap-1 text-xs"
                onClick={handleChooseLocation}
                disabled={busy}
              >
                <FolderOpen className="h-3.5 w-3.5" />
                {t("query.exportChooseFile")}
              </Button>
            </div>
            <div className="min-h-8 break-all rounded-md border border-input bg-muted/30 px-3 py-2 font-mono text-xs text-muted-foreground">
              {path || t("query.exportNoFileSelected")}
            </div>
          </div>

          {phase === "idle" && (
            <div className="rounded-md border border-border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
              {t("query.dumpSqlFileNote")}
            </div>
          )}

          {startError && (
            <div
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
            >
              {t("query.dumpSqlFileStartError", { error: startError })}
            </div>
          )}

          {phase !== "idle" && (
            <div className="grid gap-1.5">
              <div className="flex items-center justify-between text-xs">
                <span>{progressLabel(t, latest, finishedObjects, objects)}</span>
                <span className="text-muted-foreground">{percent}%</span>
              </div>
              <div className="h-1.5 rounded-full bg-muted">
                <div
                  className={`h-full rounded-full transition-all ${stopped ? "bg-destructive" : "bg-primary"}`}
                  style={{ width: `${percent}%` }}
                />
              </div>
            </div>
          )}

          {stopped && latest && (
            <div
              role="alert"
              className="grid gap-1 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
            >
              <span>
                {latest.status === "failed"
                  ? t("query.dumpSqlFileFailedAt", { object: latest.object || target })
                  : t("query.dumpSqlFileCanceledAt", { object: latest.object || target })}
              </span>
              {latest.error && <span className="whitespace-pre-wrap break-all font-mono">{latest.error}</span>}
              <span>{t("query.dumpSqlFileDeleted")}</span>
            </div>
          )}

          {done && latest?.result && (
            <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2 text-xs">
              <dt className="text-muted-foreground">{t("query.dumpSqlFileObjects")}</dt>
              <dd className="text-right font-mono">{latest.result.objects.toLocaleString()}</dd>
              <dt className="text-muted-foreground">{t("query.dumpSqlFileRows")}</dt>
              <dd className="text-right font-mono">{latest.result.rows.toLocaleString()}</dd>
              <dt className="text-muted-foreground">{t("query.dumpSqlFileSize")}</dt>
              <dd className="text-right font-mono">{formatBytes(latest.result.bytes)}</dd>
              <dt className="text-muted-foreground">{t("query.dumpSqlFileElapsed")}</dt>
              <dd className="text-right font-mono">
                {t("query.dumpSqlFileSeconds", { seconds: (latest.elapsedMs / 1000).toFixed(1) })}
              </dd>
            </dl>
          )}

          {logLines.length > 0 && (
            <div className="max-h-40 select-text overflow-auto rounded-md border bg-muted/10 p-3 font-mono text-xs">
              {logLines.map((line, index) => (
                <div
                  key={index}
                  className={line.startsWith("[ERR]") ? "whitespace-pre-wrap text-destructive" : "whitespace-pre-wrap"}
                >
                  {line}
                </div>
              ))}
            </div>
          )}
        </div>

        <DialogFooter>
          {busy ? (
            <Button variant="outline" size="sm" disabled={phase !== "running" || cancelling} onClick={handleCancel}>
              {cancelling && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t("action.cancel")}
            </Button>
          ) : phase === "finished" ? (
            <>
              {done && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="outline" size="sm" className="gap-1">
                      <ExternalLink className="h-3.5 w-3.5" />
                      {t("query.openExport")}
                      <ChevronDown className="h-3.5 w-3.5" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem className="text-xs" onClick={() => void openPath(containingDirectory(path))}>
                      {t("query.openExportFolder")}
                    </DropdownMenuItem>
                    <DropdownMenuItem className="text-xs" onClick={() => void openPath(path)}>
                      {t("query.dumpSqlFileOpenFile")}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
              <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
                {t("action.close")}
              </Button>
              {stopped && (
                <Button size="sm" className="gap-1" onClick={handleStart}>
                  <Download className="h-3.5 w-3.5" />
                  {t("query.dumpSqlFileRestart")}
                </Button>
              )}
            </>
          ) : (
            <>
              <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
                {t("action.cancel")}
              </Button>
              <Button size="sm" className="gap-1" disabled={!path} onClick={handleStart}>
                <Download className="h-3.5 w-3.5" />
                {t("query.dumpSqlFileStart")}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 进度条上方的一行:进行中是第几个对象 / 共几个与已写的行数,结束后是结果。 */
function progressLabel(
  t: (key: string, opts?: Record<string, unknown>) => string,
  event: DumpSqlFileEvent | null,
  finishedObjects: number,
  objects: number
): string {
  switch (event?.status) {
    case "done":
      return t("query.dumpSqlFileDone");
    case "failed":
    case "canceled":
      return t("query.dumpSqlFileStopped", { object: finishedObjects, objects });
    default:
      return t("query.dumpSqlFileProgress", {
        object: event?.progress.object ?? 0,
        objects,
        rows: (event?.progress.rows ?? 0).toLocaleString(),
      });
  }
}
