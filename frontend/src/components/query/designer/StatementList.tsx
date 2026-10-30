import { useTranslation } from "react-i18next";
import { AlertTriangle, Check, Minus, Undo2, X } from "lucide-react";
import { cn } from "@opskat/ui";

/**
 * 一条语句执行后的状态。executed / failed / notExecuted 同 query_svc.SchemaStatementResult;rolledBack 是
 * 整批回滚时已经执行、又被撤回的语句。
 */
export type StatementStatus = "executed" | "failed" | "notExecuted" | "rolledBack";

export interface StatementItem {
  statement: string;
  /** 没有时显示序号(还没执行)。 */
  status?: StatementStatus;
  /** 失败时数据库给出的错误。 */
  error?: string;
}

interface StatementListProps {
  items: StatementItem[];
  className?: string;
  listTestId: string;
  itemTestId?: string;
}

const STATUS_ICON = {
  executed: <Check className="h-3.5 w-3.5 text-success" />,
  failed: <X className="h-3.5 w-3.5 text-destructive" />,
  notExecuted: <Minus className="h-3.5 w-3.5 text-muted-foreground" />,
  rolledBack: <Undo2 className="h-3.5 w-3.5 text-muted-foreground" />,
} satisfies Record<StatementStatus, unknown>;

/**
 * 按执行次序编号的语句列表(SQL 预览与保存确认共用);给了状态时序号换成执行结果的图标,失败的附数据库
 * 错误,没执行与已回滚的淡显。
 */
export function StatementList({ items, className, listTestId, itemTestId }: StatementListProps) {
  const { t } = useTranslation();
  return (
    <ol data-testid={listTestId} className={cn("space-y-2", className)}>
      {items.map(({ statement, status, error }, i) => (
        <li
          key={i}
          data-testid={itemTestId}
          data-status={status}
          className={cn(
            "flex gap-3 rounded-md border border-border bg-muted/20 px-3 py-2",
            (status === "notExecuted" || status === "rolledBack") && "opacity-60"
          )}
        >
          <span
            data-testid="designer-statement-no"
            className="flex w-4 shrink-0 justify-end pt-px font-mono text-[11px] text-muted-foreground tabular-nums"
            title={status ? t(`query.designer.save.status.${status}`) : undefined}
          >
            {status ? STATUS_ICON[status] : i + 1}
          </span>
          <div className="min-w-0 flex-1 space-y-1">
            <pre className="font-mono text-xs break-all whitespace-pre-wrap select-text">{statement}</pre>
            {error && (
              <div className="rounded bg-destructive/10 px-2 py-1 text-[11px] break-all whitespace-pre-wrap text-destructive select-text">
                {error}
              </div>
            )}
          </div>
        </li>
      ))}
    </ol>
  );
}

/** 将删除的已有列(及其中数据)与索引的红色警告(由草稿得出);列与索引都没有删除时不显示。 */
export function DropWarning({
  droppedColumns,
  droppedIndexes,
  className,
  testId,
}: {
  droppedColumns: string[];
  droppedIndexes: string[];
  className?: string;
  testId: string;
}) {
  const { t } = useTranslation();
  if (droppedColumns.length === 0 && droppedIndexes.length === 0) return null;
  return (
    <div data-testid={testId} className={cn("flex items-start gap-2 text-[11px] text-destructive", className)}>
      <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
      <div className="space-y-0.5">
        {droppedColumns.length > 0 && <NameList label={t("query.designer.sql.dropColumns")} names={droppedColumns} />}
        {droppedIndexes.length > 0 && (
          <NameList
            // 删了列时索引接在列后面说「并删除索引」,只删索引时单独说。
            label={t(
              droppedColumns.length > 0 ? "query.designer.sql.dropIndexes" : "query.designer.sql.dropIndexesOnly"
            )}
            names={droppedIndexes}
          />
        )}
      </div>
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
