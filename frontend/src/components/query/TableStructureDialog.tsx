import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Dialog, DialogContent, DialogHeader, DialogTitle, ScrollArea } from "@opskat/ui";
import { KeyRound } from "lucide-react";
import { useQueryStore } from "@/stores/queryStore";

interface TableStructureDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tabId: string;
  database: string;
  table: string;
}

export function TableStructureDialog({ open, onOpenChange, tabId, database, table }: TableStructureDialogProps) {
  const { t } = useTranslation();
  const loadTableMeta = useQueryStore((s) => s.loadTableMeta);
  const meta = useQueryStore((s) => s.dbStates[tabId]?.tableMeta?.[`${database}.${table}`]);

  useEffect(() => {
    if (open && database && table) {
      void loadTableMeta(tabId, database, table);
    }
  }, [open, tabId, database, table, loadTableMeta]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("query.tableStructure", { table })}</DialogTitle>
        </DialogHeader>
        <ScrollArea className="max-h-[65vh]">
          {!meta ? (
            <div className="p-4 text-sm text-muted-foreground">{t("query.structLoading")}</div>
          ) : (
            <div className="space-y-4 p-1 text-xs">
              <section>
                <div className="mb-1 font-medium text-muted-foreground">{t("query.structColumns")}</div>
                <table className="w-full border-collapse">
                  <thead>
                    <tr className="border-b text-left text-muted-foreground">
                      <th className="py-1 pr-3 font-normal">{t("query.structName")}</th>
                      <th className="py-1 pr-3 font-normal">{t("query.structType")}</th>
                      <th className="py-1 pr-3 font-normal">{t("query.structNullable")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {meta.columns.map((col) => (
                      <tr key={col.name} className="border-b border-border/50">
                        <td className="py-1 pr-3 font-mono">
                          <span className="inline-flex items-center gap-1">
                            {col.primaryKey && <KeyRound className="h-3 w-3 text-amber-500" />}
                            {col.name}
                          </span>
                        </td>
                        <td className="py-1 pr-3 font-mono text-muted-foreground">{col.type}</td>
                        <td className="py-1 pr-3 text-muted-foreground">{col.nullable ? "YES" : "NO"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </section>

              {meta.indexes.length > 0 && (
                <section>
                  <div className="mb-1 font-medium text-muted-foreground">{t("query.structIndexes")}</div>
                  <ul className="space-y-0.5 font-mono">
                    {meta.indexes.map((idx) => (
                      <li key={idx.name} className="text-muted-foreground">
                        <span className="text-foreground">{idx.name}</span>
                        {idx.unique ? " (UNIQUE)" : ""} — {idx.columns.join(", ")}
                      </li>
                    ))}
                  </ul>
                </section>
              )}

              {meta.foreignKeys.length > 0 && (
                <section>
                  <div className="mb-1 font-medium text-muted-foreground">{t("query.structForeignKeys")}</div>
                  <ul className="space-y-0.5 font-mono">
                    {meta.foreignKeys.map((fk, i) => (
                      <li key={`${fk.name}-${i}`} className="text-muted-foreground">
                        <span className="text-foreground">{fk.column}</span> → {fk.referencedTable}.
                        {fk.referencedColumn}
                      </li>
                    ))}
                  </ul>
                </section>
              )}
            </div>
          )}
        </ScrollArea>
      </DialogContent>
    </Dialog>
  );
}
