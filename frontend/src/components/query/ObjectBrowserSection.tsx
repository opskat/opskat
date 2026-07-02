import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronRight, Eye, FunctionSquare, Workflow, Zap } from "lucide-react";
import { toast } from "sonner";
import { GetObjectSource } from "../../../wailsjs/go/query/Query";
import type { query_svc } from "../../../wailsjs/go/models";
import { useQueryStore } from "@/stores/queryStore";

interface ObjectBrowserSectionProps {
  tabId: string;
  assetId: number;
  database: string;
}

type GroupKey = "views" | "procedures" | "functions" | "triggers";

const GROUP_META: { key: GroupKey; type: string; icon: typeof Eye; labelKey: string }[] = [
  { key: "views", type: "view", icon: Eye, labelKey: "query.objViews" },
  { key: "procedures", type: "procedure", icon: Workflow, labelKey: "query.objProcedures" },
  { key: "functions", type: "function", icon: FunctionSquare, labelKey: "query.objFunctions" },
  { key: "triggers", type: "trigger", icon: Zap, labelKey: "query.objTriggers" },
];

export function ObjectBrowserSection({ tabId, assetId, database }: ObjectBrowserSectionProps) {
  const { t } = useTranslation();
  const loadObjects = useQueryStore((s) => s.loadObjects);
  const openSqlTab = useQueryStore((s) => s.openSqlTab);
  const objects = useQueryStore((s) => s.dbStates[tabId]?.objects?.[database]);
  const [openGroups, setOpenGroups] = useState<Record<GroupKey, boolean>>({
    views: false,
    procedures: false,
    functions: false,
    triggers: false,
  });

  useEffect(() => {
    if (!objects) void loadObjects(tabId, database);
  }, [objects, tabId, database, loadObjects]);

  const handleOpenSource = async (type: string, name: string, table?: string) => {
    try {
      const src = await GetObjectSource(assetId, database, type, name, table ?? "");
      openSqlTab(tabId, database, src);
    } catch (e) {
      toast.error(String(e));
    }
  };

  if (!objects) return null;

  const groups = GROUP_META.map((g) => ({ ...g, items: (objects[g.key] ?? []) as query_svc.ObjectMeta[] })).filter(
    (g) => g.items.length > 0
  );
  if (groups.length === 0) return null;

  return (
    <div className="mt-0.5">
      {groups.map((group) => {
        const Icon = group.icon;
        const isOpen = openGroups[group.key];
        return (
          <div key={group.key}>
            <div
              className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer hover:bg-accent"
              onClick={() => setOpenGroups((prev) => ({ ...prev, [group.key]: !prev[group.key] }))}
            >
              {isOpen ? (
                <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
              ) : (
                <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
              )}
              <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate text-muted-foreground">
                {t(group.labelKey)} ({group.items.length})
              </span>
            </div>
            {isOpen && (
              <div className="ml-5">
                {group.items.map((item) => (
                  <div
                    key={item.table ? `${item.name}@${item.table}` : item.name}
                    className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer hover:bg-accent"
                    title={item.table ? t("query.objViewSourceOn", { table: item.table }) : t("query.objViewSource")}
                    onClick={() => handleOpenSource(group.type, item.name, item.table)}
                  >
                    <span className="truncate font-mono">{item.name}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
