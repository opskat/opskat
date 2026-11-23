import { useState } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw, Unplug } from "lucide-react";
import { Button, Tabs, TabsList, TabsTrigger } from "@opskat/ui";
import { AuditLogsView } from "./AuditLogsView";
import { AuditPoolView } from "./AuditPoolView";

export function AuditLogPage() {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState("logs");
  const [poolRefreshKey, setPoolRefreshKey] = useState(0);

  const tabs = (
    <Tabs value={activeTab} onValueChange={setActiveTab}>
      <TabsList className="h-8">
        <TabsTrigger value="logs" className="text-xs px-3">
          {t("audit.tabLogs")}
        </TabsTrigger>
        <TabsTrigger value="pool" className="text-xs px-3">
          <Unplug className="h-3.5 w-3.5 mr-1" />
          {t("audit.tabPool")}
        </TabsTrigger>
      </TabsList>
    </Tabs>
  );

  return (
    <div className="flex flex-col h-full">
      {activeTab === "logs" ? (
        <AuditLogsView tabs={tabs} />
      ) : (
        <>
          <div className="px-4 py-3 border-b flex items-center justify-between gap-4">
            {tabs}
            <Button variant="ghost" size="sm" className="h-8" onClick={() => setPoolRefreshKey((k) => k + 1)}>
              <RefreshCw className="h-3.5 w-3.5 mr-1" />
              {t("audit.poolRefresh")}
            </Button>
          </div>
          <AuditPoolView refreshKey={poolRefreshKey} />
        </>
      )}
    </div>
  );
}
