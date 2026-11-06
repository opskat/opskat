import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@opskat/ui";

export interface ColumnInfo {
  /** 空字符串表示没有注释。 */
  comment: string;
  /** undefined 表示没有默认值;空字符串是真实的「空串」默认值。 */
  default?: string;
  type: string;
  nullable: boolean;
  primaryKey: boolean;
  /** 包含该列的索引名。 */
  indexes: string[];
}

const HOVER_DELAY_MS = 500;

// 表头列名悬停后浮出的信息卡。Radix Tooltip 不抢焦点、鼠标移开即消失;
// 没有该列的元数据(未加载或加载失败)时只渲染列名本身。
export function ColumnHeaderCard({ name, info, children }: { name: string; info?: ColumnInfo; children: ReactNode }) {
  const { t } = useTranslation();
  if (!info) return <>{children}</>;
  return (
    <TooltipProvider delayDuration={HOVER_DELAY_MS}>
      <Tooltip disableHoverableContent>
        <TooltipTrigger asChild>{children}</TooltipTrigger>
        <TooltipContent
          side="bottom"
          align="start"
          sideOffset={4}
          className="max-w-80 border border-border bg-popover text-popover-foreground shadow-md [&>svg]:hidden"
        >
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <span className="font-semibold break-all">{name}</span>
              {info.primaryKey && (
                <span className="rounded bg-primary/15 px-1 text-[10px] text-primary">
                  {t("query.columnInfoPrimaryKey")}
                </span>
              )}
            </div>
            <div className="text-muted-foreground">
              {info.type} · {info.nullable ? t("query.columnInfoNullable") : t("query.columnInfoNotNull")}
            </div>
            {info.default !== undefined && (
              <div>
                <span className="text-muted-foreground">{t("query.columnInfoDefault")}: </span>
                <span className="font-mono break-all">{info.default === "" ? "''" : info.default}</span>
              </div>
            )}
            {info.comment !== "" && (
              <div>
                <span className="text-muted-foreground">{t("query.columnInfoComment")}: </span>
                <span className="break-words whitespace-pre-wrap">{info.comment}</span>
              </div>
            )}
            {info.indexes.length > 0 && (
              <div>
                <span className="text-muted-foreground">{t("query.columnInfoIndexes")}: </span>
                <span className="font-mono break-all">{info.indexes.join(", ")}</span>
              </div>
            )}
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
