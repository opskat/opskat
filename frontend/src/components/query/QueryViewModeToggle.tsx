import { useTranslation } from "react-i18next";
import { Braces, Table2 } from "lucide-react";
import { Segmented } from "@/components/asset/fields";

export type QueryViewMode = "table" | "json";

/**
 * 结果区的表格 / JSON 切换。直接用设计系统的 Segmented,尺寸按工具栏收紧 ——
 * 与 VNC / RDP 工具栏里的用法一致(轨道 / 胶囊的明暗两套配色由 Segmented 统一负责)。
 */
export function QueryViewModeToggle({
  value,
  onChange,
}: {
  value: QueryViewMode;
  onChange: (value: QueryViewMode) => void;
}) {
  const { t } = useTranslation();

  return (
    <Segmented<QueryViewMode>
      value={value}
      onChange={onChange}
      aria-label={t("query.resultView")}
      className="h-6 w-[128px] shrink-0 rounded-md p-0.5"
      options={[
        { value: "table", label: t("query.tableView"), icon: Table2 },
        { value: "json", label: t("query.jsonView"), icon: Braces },
      ]}
    />
  );
}
