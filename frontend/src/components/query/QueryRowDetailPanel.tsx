import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Braces, X } from "lucide-react";
import { Button } from "@opskat/ui";
import { CodeEditor } from "@/components/CodeEditor";
import { resultRowsToJson } from "@/lib/resultJson";

interface QueryRowDetailPanelProps {
  rows: Record<string, unknown>[];
  columns: string[];
  /** 当前行在 rows 中的下标;无当前行时为 null。 */
  rowIdx: number | null;
  /** 行号列显示的序号偏移,与网格保持一致。 */
  rowNumberOffset?: number;
  edits?: Map<string, unknown>;
  onClose: () => void;
}

/**
 * 结果网格右侧的行详情:把当前行渲染成只读 JSON。宽表与长文本字段在网格里
 * 只能靠横向滚动读,这里给它们一个纵向的完整视图。
 */
export function QueryRowDetailPanel({
  rows,
  columns,
  rowIdx,
  rowNumberOffset = 0,
  edits,
  onClose,
}: QueryRowDetailPanelProps) {
  const { t } = useTranslation();
  const row = rowIdx == null ? undefined : rows[rowIdx];

  const json = useMemo(() => {
    if (rowIdx == null || !row) return null;
    // 单行走同一个序列化器:JSON 数组去掉首尾方括号与一层缩进,得到单个文档。
    const text = resultRowsToJson([row], columns, shiftEditsToFirstRow(edits, rowIdx, columns));
    return text.slice(text.indexOf("\n") + 1, text.lastIndexOf("\n")).replace(/^ {2}/gm, "");
  }, [columns, edits, row, rowIdx]);

  return (
    <div className="flex h-full w-[320px] shrink-0 flex-col border-l border-border bg-background">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border bg-muted/20 px-2">
        <Braces className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-xs text-foreground">{t("query.rowDetail")}</span>
        {rowIdx != null && (
          <span className="text-[11px] text-muted-foreground">
            {t("query.rowDetailIndex", { index: rowNumberOffset + rowIdx + 1 })}
          </span>
        )}
        <Button variant="ghost" size="icon-xs" className="ml-auto" title={t("action.close")} onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="min-h-0 flex-1">
        {json == null ? (
          <div className="flex h-full items-center justify-center px-4 text-center text-xs text-muted-foreground">
            {t("query.rowDetailEmpty")}
          </div>
        ) : (
          <CodeEditor value={json} language="json" readOnly />
        )}
      </div>
    </div>
  );
}

/**
 * resultRowsToJson 按传入数组的下标查 edits,而这里只传当前一行。把该行的
 * 未提交编辑重新映射到下标 0,面板才能和网格显示一致。
 */
function shiftEditsToFirstRow(
  edits: Map<string, unknown> | undefined,
  rowIdx: number,
  columns: string[]
): Map<string, unknown> | undefined {
  if (!edits || edits.size === 0) return undefined;
  const shifted = new Map<string, unknown>();
  for (const column of columns) {
    const key = `${rowIdx}:${column}`;
    if (edits.has(key)) shifted.set(`0:${column}`, edits.get(key));
  }
  return shifted;
}
