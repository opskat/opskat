import { useTranslation } from "react-i18next";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
} from "@opskat/ui";
import { cellValueToDisplayText } from "@/lib/cellValue";

/** One line of the summary: a changed cell of a saved row, or a whole unsaved new row. */
export type UnsavedEditSummaryItem =
  | { kind: "update"; row: number; column: string; oldValue: unknown; newValue: unknown }
  | { kind: "insert"; row: number; values: { column: string; value: unknown }[] };

interface UnsavedEditsDialogProps {
  open: boolean;
  /** Each item is one 「处」; the title count is the number of items. */
  summary: UnsavedEditSummaryItem[];
  /** What continuing would do, e.g. "Going to page 2 will discard them." Defaults to a generic wording. */
  description?: string;
  onStay: () => void;
  onDiscard: () => void;
  onSubmit: () => void;
}

const MAX_SUMMARY_LINES = 10;

export function UnsavedEditsDialog({
  open,
  summary,
  description,
  onStay,
  onDiscard,
  onSubmit,
}: UnsavedEditsDialogProps) {
  const { t } = useTranslation();
  const displayValue = (value: unknown) => (value == null ? t("query.nullValue") : cellValueToDisplayText(value));
  const shown = summary.slice(0, MAX_SUMMARY_LINES);
  const hidden = summary.length - shown.length;

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onStay();
      }}
    >
      <AlertDialogContent className="sm:max-w-xl">
        <AlertDialogHeader>
          <AlertDialogTitle>{t("query.unsavedEditsTitle", { count: summary.length })}</AlertDialogTitle>
          <AlertDialogDescription>{description ?? t("query.unsavedEditsDescDefault")}</AlertDialogDescription>
        </AlertDialogHeader>
        <ul className="max-h-64 overflow-auto rounded-md border border-border bg-muted/30 px-3 py-2 font-mono text-xs leading-6 select-text">
          {shown.map((item) => (
            <li
              key={item.kind === "update" ? `${item.row}:${item.column}` : `new:${item.row}`}
              data-testid="unsaved-edit-line"
              className="truncate"
            >
              {t("query.unsavedEditsRow", { row: item.row })}
              {" · "}
              {item.kind === "update" ? (
                <>
                  {item.column}
                  {": "}
                  <del className="text-muted-foreground">{displayValue(item.oldValue)}</del>
                  {" → "}
                  {displayValue(item.newValue)}
                </>
              ) : (
                <>
                  {t("query.unsavedEditsNewRow")}
                  {item.values.length > 0 &&
                    `: ${item.values.map(({ column, value }) => `${column} = ${displayValue(value)}`).join(", ")}`}
                </>
              )}
            </li>
          ))}
          {hidden > 0 && <li className="text-muted-foreground">{t("query.unsavedEditsMore", { count: hidden })}</li>}
        </ul>
        <AlertDialogFooter>
          <Button variant="outline" onClick={onStay}>
            {t("query.unsavedEditsStay")}
          </Button>
          <Button variant="outline" className="text-destructive hover:text-destructive" onClick={onDiscard}>
            {t("query.unsavedEditsDiscard")}
          </Button>
          <Button onClick={onSubmit}>{t("query.unsavedEditsSubmit")}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
