import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Popover, PopoverAnchor, PopoverContent } from "@opskat/ui";
import { toast } from "sonner";
import { notifyCopied } from "@/lib/notify";
import { formatModKey } from "@/stores/shortcutStore";
import { cellValueToText } from "@/lib/cellValue";

export interface CellExpandEditorProps {
  /** 浮层锚点(视口坐标,通常是被编辑单元格的左下角)。 */
  anchor: { x: number; y: number };
  column: string;
  /** 列类型标签;表格不知道类型时省略。 */
  columnType?: string;
  rowNumber: number;
  /** 单元格当前值(含未提交修改)。 */
  value: unknown;
  /** 只读:仅查看与复制,不提供设值与确定。 */
  readOnly: boolean;
  /** 列已知为 NOT NULL:「设为 NULL」禁用。 */
  notNull: boolean;
  /** 记录结果;text 为编辑区原文,null 为设为 NULL。内容未改动时不会调用。 */
  onCommit: (value: string | null) => void;
  /** 关闭(取消 / Esc / 提交后);调用方负责把焦点还给表格。 */
  onClose: () => void;
}

type Mode = "text" | "json";

function parseJson(text: string): { ok: true; value: unknown } | { ok: false } {
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch {
    return { ok: false };
  }
}

function isJsonContainer(value: unknown): boolean {
  return typeof value === "object" && value !== null;
}

const BUTTON_CLASS =
  "rounded-md border px-2 py-1 text-xs hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50";

export function CellExpandEditor({
  anchor,
  column,
  columnType,
  rowNumber,
  value,
  readOnly,
  notNull,
  onCommit,
  onClose,
}: CellExpandEditorProps) {
  const { t } = useTranslation();
  // 原文:不自动格式化。对象值(Mongo 等)按其 JSON 文本展示。
  const original = useMemo(() => cellValueToText(value), [value]);
  const [text, setText] = useState(original);
  const [mode, setMode] = useState<Mode>(() => {
    const parsed = parseJson(original);
    return parsed.ok && isJsonContainer(parsed.value) ? "json" : "text";
  });

  const parsed = mode === "json" ? parseJson(text) : null;

  const commit = () => {
    if (!readOnly && text !== original) onCommit(text);
    onClose();
  };

  // 「设为 NULL / 空字符串」立即记录并关闭;已经是该值时不产生修改。
  const setValue = (next: string | null) => {
    const isNull = value === null || value === undefined;
    const changed = next === null ? !isNull : isNull || original !== next;
    if (changed) onCommit(next);
    onClose();
  };

  const rewrite = (indent?: number) => {
    const result = parseJson(text);
    if (result.ok) setText(JSON.stringify(result.value, null, indent));
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      notifyCopied(t("query.copied"));
    } catch (e) {
      toast.error(String(e));
    }
  };

  return (
    <Popover open onOpenChange={(open) => !open && onClose()}>
      <PopoverAnchor asChild>
        <span aria-hidden className="pointer-events-none fixed h-0 w-0" style={{ left: anchor.x, top: anchor.y }} />
      </PopoverAnchor>
      <PopoverContent align="start" className="w-[28rem] max-w-[90vw] p-0" onCloseAutoFocus={(e) => e.preventDefault()}>
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <div className="min-w-0 flex-1 truncate text-sm font-medium" title={column}>
            {column}
            {columnType && (
              <span className="ml-2 font-mono text-xs font-normal text-muted-foreground">{columnType}</span>
            )}
            <span className="ml-2 text-xs font-normal text-muted-foreground">
              {t("query.expandEdit.row", { n: rowNumber })}
            </span>
          </div>
          <div role="radiogroup" className="flex shrink-0 overflow-hidden rounded-md border text-xs">
            {(["text", "json"] as const).map((m) => (
              <button
                key={m}
                type="button"
                role="radio"
                aria-checked={mode === m}
                className={`px-2 py-1 ${mode === m ? "bg-accent text-accent-foreground" : "hover:bg-accent/50"}`}
                onClick={() => setMode(m)}
              >
                {t(`query.expandEdit.${m}`)}
              </button>
            ))}
          </div>
        </div>
        <textarea
          autoFocus
          aria-label={t("query.expandEdit.title")}
          readOnly={readOnly}
          spellCheck={false}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              commit();
            }
          }}
          className="block h-60 w-full resize-none bg-background px-3 py-2 font-mono text-xs outline-none"
        />
        <div className="flex flex-wrap items-center gap-2 border-t px-3 py-2">
          {!readOnly && (
            <>
              <button type="button" className={BUTTON_CLASS} disabled={notNull} onClick={() => setValue(null)}>
                {t("query.setNull")}
              </button>
              <button type="button" className={BUTTON_CLASS} onClick={() => setValue("")}>
                {t("query.setEmptyString")}
              </button>
            </>
          )}
          {mode === "json" && (
            <>
              {!readOnly && (
                <>
                  <button type="button" className={BUTTON_CLASS} disabled={!parsed?.ok} onClick={() => rewrite(2)}>
                    {t("query.expandEdit.format")}
                  </button>
                  <button type="button" className={BUTTON_CLASS} disabled={!parsed?.ok} onClick={() => rewrite()}>
                    {t("query.expandEdit.compact")}
                  </button>
                </>
              )}
              <span className={`text-xs ${parsed?.ok ? "text-muted-foreground" : "text-warning"}`}>
                {parsed?.ok ? t("query.expandEdit.jsonValid") : t("query.expandEdit.jsonInvalid")}
              </span>
            </>
          )}
          {notNull && !readOnly && (
            <span className="text-xs text-muted-foreground">{t("query.expandEdit.notNullHint")}</span>
          )}
          <div className="ml-auto flex items-center gap-2">
            <button type="button" className={BUTTON_CLASS} onClick={() => void copy()}>
              {t("action.copy")}
            </button>
            <button type="button" className={BUTTON_CLASS} onClick={onClose}>
              {t("action.cancel")}
            </button>
            {!readOnly && (
              <button
                type="button"
                className="rounded-md bg-primary px-2 py-1 text-xs text-primary-foreground hover:bg-primary/90"
                onClick={commit}
              >
                {t("query.expandEdit.ok")}
                <span className="ml-1 opacity-70">{formatModKey("Enter")}</span>
              </button>
            )}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
