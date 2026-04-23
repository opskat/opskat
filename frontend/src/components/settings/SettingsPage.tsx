import { useTranslation } from "react-i18next";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@opskat/ui";
import {
  Bot,
  Palette,
  HardDrive,
  Import,
  Keyboard,
  MonitorDot,
  Info,
  Activity,
  Puzzle,
  ArrowRightLeft,
  KeyRound,
  ScrollText,
} from "lucide-react";
import { ShortcutSettings } from "@/components/settings/ShortcutSettings";
import { AISettingsSection } from "@/components/settings/AISettingsSection";
import { ImportSection } from "@/components/settings/ImportSection";
import { BackupSection } from "@/components/settings/BackupSection";
import { AppearanceSection, TerminalSection } from "@/components/settings/AppearanceSection";
import { UpdateSection } from "@/components/settings/UpdateSection";
import { SystemStatusSection } from "@/components/settings/SystemStatusSection";
import { ExtensionSection } from "@/components/settings/ExtensionSection";
import { CredentialManager } from "@/components/settings/CredentialManager";
import { PortForwardPage } from "@/components/forward/PortForwardPage";
import { AuditLogPage } from "@/components/audit/AuditLogPage";

const settingTabValues = [
  "ai",
  "import",
  "backup",
  "shortcuts",
  "terminal",
  "appearance",
  "forward",
  "sshkeys",
  "audit",
  "about",
  "status",
  "extensions",
] as const;

type SettingsTabValue = (typeof settingTabValues)[number];

interface SettingsPageProps {
  initialTab?: string;
}

export function SettingsPage({ initialTab }: SettingsPageProps) {
  const { t } = useTranslation();
  const defaultTab = settingTabValues.includes((initialTab ?? "") as SettingsTabValue) ? initialTab! : "ai";

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 border-b">
        <h2 className="font-semibold">{t("nav.settings")}</h2>
      </div>
      <div className="flex-1 overflow-y-auto p-4">
        <Tabs key={defaultTab} defaultValue={defaultTab} className="space-y-4 max-w-4xl mx-auto">
          <TabsList>
            <TabsTrigger value="ai" className="gap-1">
              <Bot className="h-3.5 w-3.5" />
              AI
            </TabsTrigger>
            <TabsTrigger value="import" className="gap-1">
              <Import className="h-3.5 w-3.5" />
              {t("import.title")}
            </TabsTrigger>
            <TabsTrigger value="backup" className="gap-1">
              <HardDrive className="h-3.5 w-3.5" />
              {t("backup.title")}
            </TabsTrigger>
            <TabsTrigger value="shortcuts" className="gap-1">
              <Keyboard className="h-3.5 w-3.5" />
              {t("shortcut.title")}
            </TabsTrigger>
            <TabsTrigger value="terminal" className="gap-1">
              <MonitorDot className="h-3.5 w-3.5" />
              {t("terminal.title")}
            </TabsTrigger>
            <TabsTrigger value="appearance" className="gap-1">
              <Palette className="h-3.5 w-3.5" />
              {t("nav.appearance")}
            </TabsTrigger>
            <TabsTrigger value="forward" className="gap-1">
              <ArrowRightLeft className="h-3.5 w-3.5" />
              {t("nav.forward")}
            </TabsTrigger>
            <TabsTrigger value="sshkeys" className="gap-1">
              <KeyRound className="h-3.5 w-3.5" />
              {t("nav.sshKeys")}
            </TabsTrigger>
            <TabsTrigger value="audit" className="gap-1">
              <ScrollText className="h-3.5 w-3.5" />
              {t("nav.audit")}
            </TabsTrigger>
            <TabsTrigger value="about" className="gap-1">
              <Info className="h-3.5 w-3.5" />
              {t("appUpdate.title")}
            </TabsTrigger>
            <TabsTrigger value="status" className="gap-1">
              <Activity className="h-3.5 w-3.5" />
              {t("systemStatus.title")}
            </TabsTrigger>
            <TabsTrigger value="extensions" className="gap-1">
              <Puzzle className="h-3.5 w-3.5" />
              {t("extension.title")}
            </TabsTrigger>
          </TabsList>

          <TabsContent value="ai" className="space-y-4">
            <AISettingsSection />
          </TabsContent>

          <TabsContent value="import" className="space-y-4">
            <ImportSection />
          </TabsContent>

          <TabsContent value="backup" className="space-y-4">
            <BackupSection />
          </TabsContent>

          <TabsContent value="shortcuts" className="space-y-4">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t("shortcut.title")}</CardTitle>
                <CardDescription>{t("shortcut.desc")}</CardDescription>
              </CardHeader>
              <CardContent>
                <ShortcutSettings />
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="terminal" className="space-y-4">
            <TerminalSection />
          </TabsContent>

          <TabsContent value="appearance" className="space-y-4">
            <AppearanceSection />
          </TabsContent>

          <TabsContent value="forward" className="space-y-4">
            <Card>
              <CardContent className="p-0">
                <PortForwardPage embedded />
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="sshkeys" className="space-y-4">
            <Card>
              <CardContent className="pt-6">
                <CredentialManager />
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="audit" className="space-y-4">
            <Card>
              <CardContent className="p-0">
                <AuditLogPage />
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="about" className="space-y-4">
            <UpdateSection />
          </TabsContent>

          <TabsContent value="status" className="space-y-4">
            <SystemStatusSection />
          </TabsContent>

          <TabsContent value="extensions" className="space-y-4">
            <ExtensionSection />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
