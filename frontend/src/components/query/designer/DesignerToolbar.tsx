import { useTranslation } from "react-i18next";
import { Lock, RefreshCw } from "lucide-react";
import { Button, Tooltip, TooltipContent, TooltipTrigger, cn } from "@opskat/ui";
import type { DesignerReadOnlyReason } from "./model";

interface ReadOnlyToolbarProps {
  tableName: string;
  /** 表注释,读不出或没有时为空串。 */
  comment: string;
  /** 只读的原因;null 表示没有具体原因(只说只读)。 */
  readOnlyReason: DesignerReadOnlyReason | null;
  loading: boolean;
  onReload: () => void;
}

/** 只读设计器的工具栏:表名与表注释是文本,标出只读及原因,只有重新读取。 */
export function ReadOnlyToolbar({ tableName, comment, readOnlyReason, loading, onReload }: ReadOnlyToolbarProps) {
  const { t } = useTranslation();
  const reasonText =
    readOnlyReason === "readOnlyConnection"
      ? t("query.designer.readOnly.readOnlyConnection")
      : readOnlyReason === "driverCannotAlter"
        ? t("query.designer.readOnly.driverCannotAlter")
        : t("query.designer.readOnly.generic");
  return (
    <div className="flex h-10 shrink-0 items-center gap-3 border-b border-border px-3">
      <span data-testid="designer-table-name" className="truncate font-mono text-sm font-medium select-text">
        {tableName}
      </span>
      {comment && <span className="truncate text-xs text-muted-foreground select-text">{comment}</span>}
      <span
        data-testid="designer-readonly"
        className="flex shrink-0 items-center gap-1 rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground"
      >
        <Lock className="h-3 w-3" aria-hidden />
        {reasonText}
      </span>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="icon-xs"
            className="ml-auto"
            aria-label={t("query.designer.reload")}
            disabled={loading}
            onClick={onReload}
          >
            <RefreshCw className={cn(loading && "animate-spin")} />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("query.designer.reload")}</TooltipContent>
      </Tooltip>
    </div>
  );
}
