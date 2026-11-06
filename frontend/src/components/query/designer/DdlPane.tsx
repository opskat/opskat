import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { Button } from "@opskat/ui";
import { CodeEditor } from "@/components/CodeEditor";
import { useTableDDL } from "./useTableStructure";

/** 建表语句子页:驱动给出的 DDL(只读)。 */
export function DdlPane({ assetId, database, table }: { assetId: number; database: string; table: string }) {
  const { t } = useTranslation();
  const { state, reload } = useTableDDL(assetId, database, table);
  if (state.status === "loading") {
    return (
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-label={t("query.loadingDDL")} />
      </div>
    );
  }
  if (state.status === "error") {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6">
        <code className="max-w-lg rounded-md bg-muted px-3 py-2 font-mono text-[11px] text-muted-foreground select-text">
          {state.error}
        </code>
        <Button size="sm" variant="outline" className="h-7 text-xs" onClick={reload}>
          {t("action.retry")}
        </Button>
      </div>
    );
  }
  return (
    <div className="min-h-0 flex-1">
      <CodeEditor
        value={state.value || t("query.ddlEmpty")}
        language="sql"
        readOnly
        options={{ lineNumbers: "off", folding: false, glyphMargin: false, lineDecorationsWidth: 0 }}
      />
    </div>
  );
}
