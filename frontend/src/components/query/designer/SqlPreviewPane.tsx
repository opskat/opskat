import { useTranslation } from "react-i18next";
import { AlertTriangle, CircleAlert, Copy, Loader2, SquareTerminal } from "lucide-react";
import { Button } from "@opskat/ui";
import { notifyCopied } from "@/lib/notify";
import type { AlterPreview } from "./useAlterPreview";
import { statementsScript } from "./format";

interface SqlPreviewPaneProps {
  preview: AlterPreview;
  /** 草稿里删除的已有列与随之删除的已有索引(由草稿得出)。 */
  droppedColumns: string[];
  droppedIndexes: string[];
  onOpenInSqlTab: (script: string) => void;
}

/**
 * SQL 预览子页:驱动按当前草稿生成的语句,按执行次序编号;删除了已有列时顶部红色警告;
 * 可复制全部语句或放进新的 SQL 标签。
 */
export function SqlPreviewPane({ preview, droppedColumns, droppedIndexes, onOpenInSqlTab }: SqlPreviewPaneProps) {
  const { t } = useTranslation();
  const statements = preview.status === "ready" ? preview.statements : [];
  const script = statementsScript(statements);
  const copy = async () => {
    await navigator.clipboard.writeText(script);
    notifyCopied(t("query.copied"));
  };

  let body;
  if (preview.status === "blocked") {
    body = <Placeholder text={t("query.designer.sql.blocked")} />;
  } else if (preview.status === "loading") {
    body = (
      <div className="flex flex-1 items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden />
        {t("query.designer.sql.generating")}
      </div>
    );
  } else if (preview.status === "error") {
    body = (
      <div role="alert" className="m-3 space-y-1.5 rounded-md border border-destructive/40 bg-destructive/5 p-3">
        <div className="flex items-center gap-1.5 text-xs font-medium text-destructive">
          <CircleAlert className="h-3.5 w-3.5" aria-hidden />
          {t("query.designer.sql.failed")}
        </div>
        <code className="block font-mono text-[11px] break-all whitespace-pre-wrap text-muted-foreground select-text">
          {preview.error}
        </code>
      </div>
    );
  } else if (statements.length === 0) {
    body = <Placeholder text={t("query.designer.sql.noChanges")} />;
  } else {
    body = (
      <ol data-testid="designer-statements" className="min-h-0 flex-1 space-y-2 overflow-auto p-3">
        {statements.map((statement, i) => (
          <li key={i} className="flex gap-3 rounded-md border border-border bg-muted/20 px-3 py-2">
            <span
              data-testid="designer-statement-no"
              className="w-4 shrink-0 pt-px text-right font-mono text-[11px] text-muted-foreground tabular-nums"
            >
              {i + 1}
            </span>
            <pre className="min-w-0 flex-1 font-mono text-xs break-all whitespace-pre-wrap select-text">
              {statement}
            </pre>
          </li>
        ))}
      </ol>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border px-3 text-[11px] text-muted-foreground">
        {statements.length > 0 && <span>{t("query.designer.sql.count", { count: statements.length })}</span>}
        <div className="ml-auto flex items-center gap-1">
          <Button variant="ghost" size="xs" disabled={statements.length === 0} onClick={copy}>
            <Copy aria-hidden />
            {t("action.copy")}
          </Button>
          <Button variant="ghost" size="xs" disabled={statements.length === 0} onClick={() => onOpenInSqlTab(script)}>
            <SquareTerminal aria-hidden />
            {t("query.designer.sql.openInSqlTab")}
          </Button>
        </div>
      </div>
      {droppedColumns.length > 0 && (
        <div
          data-testid="designer-drop-warning"
          className="flex shrink-0 items-start gap-2 border-b border-destructive/30 bg-destructive/5 px-3 py-1.5 text-[11px] text-destructive"
        >
          <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
          <div className="space-y-0.5">
            <NameList label={t("query.designer.sql.dropColumns")} names={droppedColumns} />
            {droppedIndexes.length > 0 && (
              <NameList label={t("query.designer.sql.dropIndexes")} names={droppedIndexes} />
            )}
          </div>
        </div>
      )}
      {body}
    </div>
  );
}

function NameList({ label, names }: { label: string; names: string[] }) {
  return (
    <div className="flex flex-wrap items-center gap-1">
      <span>{label}</span>
      {names.map((name) => (
        <b key={name} className="font-mono font-semibold">
          {name}
        </b>
      ))}
    </div>
  );
}

function Placeholder({ text }: { text: string }) {
  return <div className="flex flex-1 items-center justify-center text-xs text-muted-foreground">{text}</div>;
}
