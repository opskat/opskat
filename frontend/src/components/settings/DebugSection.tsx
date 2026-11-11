import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Switch } from "@opskat/ui";
import { Bug, FolderOpen } from "lucide-react";
import { toast } from "sonner";
import { GetBugReportInfo, GetDebugMode, OpenLogsDir, SetDebugMode } from "../../../wailsjs/go/system/System";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import { notifySuccess } from "@/lib/notify";
import { errorMessage } from "@/lib/errorMessage";
import { REPOSITORY_URL } from "./UpdateSection";

/** 设置 › 关于 › 调试与日志：调试模式开关、反馈问题、打开日志目录。 */
export function DebugSection() {
  const { t } = useTranslation();
  const [debugMode, setDebugModeState] = useState(false);
  // 读到当前值之前开关不可用:读取失败时不能让用户对着一个猜出来的「关」去切换。
  const [debugModeLoaded, setDebugModeLoaded] = useState(false);

  useEffect(() => {
    GetDebugMode().then(
      (enabled) => {
        setDebugModeState(enabled);
        setDebugModeLoaded(true);
      },
      (e: unknown) => toast.error(errorMessage(e))
    );
  }, []);

  const handleDebugModeChange = async (enabled: boolean) => {
    setDebugModeState(enabled);
    try {
      await SetDebugMode(enabled);
      notifySuccess(enabled ? t("appUpdate.debugModeOn") : t("appUpdate.debugModeOff"));
    } catch (e: unknown) {
      setDebugModeState(!enabled);
      toast.error(errorMessage(e));
    }
  };

  const handleOpenLogsDir = async () => {
    try {
      await OpenLogsDir();
    } catch (e: unknown) {
      toast.error(errorMessage(e));
    }
  };

  const handleReportBug = async () => {
    const params = new URLSearchParams({ template: "bug_report.yml", labels: "bug" });
    try {
      const info = await GetBugReportInfo();
      const version = info.version + (info.commit ? ` (${info.commit})` : "");
      if (version.trim()) params.set("version", version);
      if (info.osLabel) params.set("os", info.osLabel);
    } catch {
      // 取不到诊断信息时仍然打开模板，让用户手动填写
    }
    BrowserOpenURL(`${REPOSITORY_URL}/issues/new?${params.toString()}`);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("settings.nav.debugLogs")}</CardTitle>
        <CardDescription>{t("settings.debugLogs.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex justify-between items-start text-sm">
          <div className="space-y-0.5">
            <div>{t("appUpdate.debugMode")}</div>
            <div className="text-xs text-muted-foreground">{t("appUpdate.debugModeDesc")}</div>
          </div>
          <Switch checked={debugMode} disabled={!debugModeLoaded} onCheckedChange={handleDebugModeChange} />
        </div>
        <div className="flex gap-2 flex-wrap">
          <Button onClick={handleReportBug} size="sm" variant="outline" title={t("appUpdate.reportBugDesc")}>
            <Bug className="h-3.5 w-3.5 mr-1.5" />
            {t("appUpdate.reportBug")}
          </Button>
          <Button onClick={handleOpenLogsDir} size="sm" variant="outline">
            <FolderOpen className="h-3.5 w-3.5 mr-1.5" />
            {t("appUpdate.openLogsDir")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
