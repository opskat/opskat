import { useCallback, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { AlertTriangle, FolderOpen, Loader2, Square } from "lucide-react";
import { toast } from "sonner";
import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Label,
} from "@opskat/ui";
import { SelectSQLFile, StartRunSQLFile, StopRunSQLFile } from "../../../wailsjs/go/query/Query";
import { errorMessage } from "@/lib/errorMessage";
import { formatBytes } from "@/lib/formatBytes";
import { useBackendJob, type BackendJobSpec } from "./useBackendJob";

/** 后端发出运行事件的事件名(Go 侧 query.RunSQLFileEventName)。 */
export const RUN_SQL_FILE_EVENT = "query:run-sql-file";

/** 后端的一个运行事件(Go 侧 query.RunSQLFileEvent)。 */
export interface RunSqlFileEvent {
  runId: string;
  status: "running" | "done" | "failed" | "stopped" | "error";
  progress: { succeeded: number; failed: number; bytesRead: number; totalBytes: number };
  elapsedMs: number;
  failure?: { number: number; line: number; error: string; excerpt: string };
  /** 自上一个事件以来新失败的语句(勾了「遇到错误时继续」时,后端最多逐条列出 100 条)。 */
  failures?: { number: number; line: number; error: string; excerpt: string }[];
  /** 后面的失败不再逐条列出(只在超过上限的那个事件上为真一次)。 */
  failuresOmitted?: boolean;
  invalidUtf8Line?: number;
  /** 停在了文件里不支持的写法上(之前的语句已执行):写法、该改用的命令行工具与行号,提示由对话框按界面语言写。 */
  unsupported?: { construct: string; tool: string; line: number };
  error?: string;
}

/**
 * error 终态的提示(错误框与日志共用):文件不是 UTF-8、停在不支持的写法上时按界面语言用字段写出,
 * 不用后端的文字;其余是运行本身出错的原文。
 */
function runErrorMessage(event: RunSqlFileEvent, t: TFunction): string {
  if (event.invalidUtf8Line) {
    return t("query.runSqlFileNotUtf8", { line: event.invalidUtf8Line.toLocaleString() });
  }
  if (event.unsupported) {
    const { construct, tool, line } = event.unsupported;
    return t("query.runSqlFileUnsupported", { construct, tool, line: line.toLocaleString() });
  }
  return t("query.runSqlFileErrorSummary", { error: event.error ?? "" });
}

/** 各终态在日志里的最后一行。 */
const FINISHED_LOG: Record<Exclude<RunSqlFileEvent["status"], "running">, string> = {
  done: "query.runSqlFileLogDone",
  failed: "query.runSqlFileLogStopped",
  stopped: "query.runSqlFileLogStopped",
  error: "query.runSqlFileLogError",
};

/** 日志每执行这么多条语句写一行。 */
const LOG_EVERY = 10000;

interface RunSqlFileDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetId: number;
  assetName: string;
  database: string;
  /** 运行结束(执行过语句)后调用:刷新树上这个库的表与对象。 */
  onFinished: () => void;
}

type Phase = "idle" | "starting" | "running" | "finished";

/** 运行任务:停下是要求它在当前语句之后停下;对话框消失(关闭资产标签页)时停下进行中的运行。 */
const RUN_JOB: BackendJobSpec<RunSqlFileEvent> = {
  eventName: RUN_SQL_FILE_EVENT,
  jobId: (event) => event.runId,
  isFinal: (event) => event.status !== "running",
  stop: StopRunSQLFile,
};

export function RunSqlFileDialog({
  open,
  onOpenChange,
  assetId,
  assetName,
  database,
  onFinished,
}: RunSqlFileDialogProps) {
  const { t } = useTranslation();
  const [file, setFile] = useState<{ path: string; size: number } | null>(null);
  const [continueOnError, setContinueOnError] = useState(false);
  const [phase, setPhase] = useState<Phase>("idle");
  const [latest, setLatest] = useState<RunSqlFileEvent | null>(null);
  const [startError, setStartError] = useState("");
  const [logLines, setLogLines] = useState<string[]>([]);
  const loggedRef = useRef(0);
  const [stopping, setStopping] = useState(false);

  const busy = phase === "starting" || phase === "running";

  const apply = useCallback(
    (event: RunSqlFileEvent) => {
      setLatest(event);
      const executed = event.progress.succeeded + event.progress.failed;
      const lines: string[] = [];
      while (Math.floor(executed / LOG_EVERY) > loggedRef.current) {
        loggedRef.current += 1;
        lines.push(
          `[RUN] ${t("query.runSqlFileLogProgress", { count: (loggedRef.current * LOG_EVERY).toLocaleString() })}`
        );
      }
      for (const failure of event.failures ?? []) {
        lines.push(
          `[ERR] ${t("query.runSqlFileFailedAt", { number: failure.number.toLocaleString(), line: failure.line.toLocaleString() })} ${failure.error}`
        );
      }
      if (event.failuresOmitted) lines.push(`[ERR] ${t("query.runSqlFileFailuresOmitted")}`);
      if (event.status === "running") {
        if (lines.length) setLogLines((prev) => [...prev, ...lines]);
        return;
      }
      if (event.failure) {
        lines.push(
          `[ERR] ${t("query.runSqlFileFailedAt", { number: event.failure.number.toLocaleString(), line: event.failure.line.toLocaleString() })} ${event.failure.error}`
        );
      } else if (event.status === "error") {
        lines.push(`[ERR] ${runErrorMessage(event, t)}`);
      }
      lines.push(`[RUN] ${t(FINISHED_LOG[event.status])}`);
      setLogLines((prev) => [...prev, ...lines]);
      setPhase("finished");
      onFinished();
    },
    [onFinished, t]
  );
  const { start: startJob, stopActive } = useBackendJob(RUN_JOB, apply);

  const handleChooseFile = useCallback(async () => {
    try {
      const chosen = await SelectSQLFile();
      if (chosen) setFile({ path: chosen.path, size: chosen.size });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }, []);

  const handleStart = useCallback(async () => {
    if (!file) return;
    setPhase("starting");
    setStartError("");
    setStopping(false);
    setLatest(null);
    loggedRef.current = 0;
    setLogLines([`[RUN] ${t("query.runSqlFileLogStart", { file: file.path, target: `${assetName} / ${database}` })}`]);
    try {
      await startJob(
        () => StartRunSQLFile(assetId, database, { path: file.path, continueOnError }),
        () => setPhase("running")
      );
    } catch (e) {
      setStartError(errorMessage(e));
      setPhase("idle");
    }
  }, [assetId, assetName, continueOnError, database, file, startJob, t]);

  const handleStop = useCallback(() => {
    if (stopActive()) setStopping(true);
  }, [stopActive]);

  const progress = latest?.progress;
  const totalBytes = progress?.totalBytes || file?.size || 0;
  const percent = progress && totalBytes > 0 ? Math.min(100, Math.round((progress.bytesRead / totalBytes) * 100)) : 0;
  const failed = latest?.status === "failed" || latest?.status === "error";

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
          <DialogTitle>{t("query.runSqlFileTitle")}</DialogTitle>
          <DialogDescription>{t("query.runSqlFileDesc", { target: `${assetName} / ${database}` })}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-2">
            <div className="flex items-center justify-between gap-2">
              <Label className="text-xs">{t("query.runSqlFileFile")}</Label>
              <Button
                variant="outline"
                size="sm"
                className="h-7 gap-1 text-xs"
                onClick={handleChooseFile}
                disabled={phase !== "idle"}
              >
                <FolderOpen className="h-3.5 w-3.5" />
                {t("query.runSqlFileChoose")}
              </Button>
            </div>
            <div className="min-h-8 break-all rounded-md border border-input bg-muted/30 px-3 py-2 font-mono text-xs text-muted-foreground">
              {file?.path || t("query.runSqlFileNoFile")}
            </div>
            {file && phase === "idle" && (
              <div className="flex items-center justify-between rounded-md border border-border px-3 py-2 text-xs">
                <span className="text-muted-foreground">{t("query.runSqlFileSize")}</span>
                <span className="font-mono">{formatBytes(file.size)}</span>
              </div>
            )}
          </div>

          <div className="flex items-start gap-2">
            <Checkbox
              id="run-sql-file-continue"
              checked={continueOnError}
              disabled={phase !== "idle"}
              onCheckedChange={(checked) => setContinueOnError(checked === true)}
            />
            <div className="grid gap-0.5">
              <Label htmlFor="run-sql-file-continue" className="text-xs font-medium">
                {t("query.runSqlFileContinueOnError")}
              </Label>
              <span className="text-xs text-muted-foreground">{t("query.runSqlFileContinueOnErrorHint")}</span>
            </div>
          </div>

          {phase === "idle" && (
            <div className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{t("query.runSqlFileWarning", { database })}</span>
            </div>
          )}

          {startError && (
            <div
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
            >
              {t("query.runSqlFileErrorSummary", { error: startError })}
            </div>
          )}

          {phase !== "idle" && (
            <div className="grid gap-1.5">
              <div className="flex items-center justify-between text-xs">
                <span>{progressLabel(t, latest, totalBytes)}</span>
                <span className="text-muted-foreground">{percent}%</span>
              </div>
              <div className="h-1.5 rounded-full bg-muted">
                <div
                  className={`h-full rounded-full transition-all ${failed ? "bg-destructive" : "bg-primary"}`}
                  style={{ width: `${percent}%` }}
                />
              </div>
            </div>
          )}

          {latest?.failure && (
            <div
              role="alert"
              className="grid gap-1 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
            >
              <span>
                {t("query.runSqlFileFailedAt", {
                  number: latest.failure.number.toLocaleString(),
                  line: latest.failure.line.toLocaleString(),
                })}
              </span>
              <span className="whitespace-pre-wrap break-all font-mono">{latest.failure.error}</span>
              <span className="whitespace-pre-wrap break-all font-mono text-muted-foreground">
                {latest.failure.excerpt}
              </span>
              <span>{t("query.runSqlFileNotRolledBack", { count: latest.failure.number - 1 })}</span>
            </div>
          )}

          {latest?.status === "error" && (
            <div
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
            >
              {runErrorMessage(latest, t)}
            </div>
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
          {phase === "finished" ? (
            <Button size="sm" onClick={() => onOpenChange(false)}>
              {t("action.close")}
            </Button>
          ) : busy ? (
            <Button variant="outline" size="sm" disabled={phase !== "running" || stopping} onClick={handleStop}>
              {stopping ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3.5 w-3.5" />}
              {stopping ? t("query.runSqlFileStopping") : t("query.runSqlFileStop")}
            </Button>
          ) : (
            <>
              <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
                {t("action.cancel")}
              </Button>
              <Button size="sm" disabled={!file} onClick={handleStart}>
                {t("query.runSqlFileStart")}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 进度条上方的一行:进行中是已执行条数与已读字节,结束后是汇总。 */
function progressLabel(
  t: (key: string, opts?: Record<string, unknown>) => string,
  event: RunSqlFileEvent | null,
  totalBytes: number
): string {
  const progress = event?.progress ?? { succeeded: 0, failed: 0, bytesRead: 0, totalBytes };
  const counts = {
    succeeded: progress.succeeded.toLocaleString(),
    failed: progress.failed.toLocaleString(),
    seconds: ((event?.elapsedMs ?? 0) / 1000).toFixed(1),
  };
  switch (event?.status) {
    case "done":
      return t("query.runSqlFileDoneSummary", counts);
    case "failed":
    case "stopped":
      return t("query.runSqlFileStoppedSummary", counts);
    case "error":
      return t("query.runSqlFileErrorTitle");
    default:
      return t("query.runSqlFileProgress", {
        count: (progress.succeeded + progress.failed).toLocaleString(),
        read: formatBytes(progress.bytesRead),
        total: formatBytes(totalBytes),
      });
  }
}
