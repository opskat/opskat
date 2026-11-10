import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardDescription, CardHeader, CardTitle, cn } from "@opskat/ui";
import {
  Bot,
  Palette,
  Import,
  Keyboard,
  MonitorDot,
  Info,
  Activity,
  Puzzle,
  PencilLine,
  Blocks,
  Globe,
  type LucideIcon,
} from "lucide-react";
import { ShortcutSettings } from "@/components/settings/ShortcutSettings";
import { CustomTypeSection } from "@/components/settings/CustomTypeSection";
import { AISettingsSection } from "@/components/settings/AISettingsSection";
import { CommandReviewSection } from "@/components/settings/CommandReviewSection";
import { ImportSection } from "@/components/settings/ImportSection";
import { BackupSection } from "@/components/settings/BackupSection";
import { AppearanceSection, TerminalSection } from "@/components/settings/AppearanceSection";
import { ConnectionSection } from "@/components/settings/ConnectionSection";
import { UpdateSection } from "@/components/settings/UpdateSection";
import { SystemStatusSection } from "@/components/settings/SystemStatusSection";
import { ExtensionSection } from "@/components/settings/ExtensionSection";
import { NetworkSection } from "@/components/settings/NetworkSection";
import { ExternalEditSection } from "@/components/settings/ExternalEditSection";
import { useExtensionUpdates, useExtensionUpdateSync } from "@/components/settings/extensionUpdates";
import { useSettingsUiStore, type SettingsTab } from "@/stores/settingsUiStore";

interface NavItem {
  /** 导航项的 id，也是它打开时写入 store 的 SettingsTab。 */
  id: SettingsTab;
  /** 该项承载的全部 SettingsTab id；旧入口按这些 id 落到本项。 */
  tabs: SettingsTab[];
  icon: LucideIcon;
  label: string;
  badge?: ReactNode;
}

export function SettingsPage() {
  const { t } = useTranslation();
  const activeTab = useSettingsUiStore((s) => s.activeTab);
  const setActiveTab = useSettingsUiStore((s) => s.setActiveTab);
  useExtensionUpdateSync();
  const updateCount = useExtensionUpdates((s) => Object.keys(s.updates).length);

  const groups: { id: string; label: string; items: NavItem[] }[] = [
    {
      id: "general",
      label: t("settings.group.general"),
      items: [
        { id: "appearance", tabs: ["appearance"], icon: Palette, label: t("nav.appearance") },
        { id: "shortcuts", tabs: ["shortcuts"], icon: Keyboard, label: t("shortcut.title") },
      ],
    },
    {
      id: "connection",
      label: t("settings.group.connection"),
      items: [
        { id: "terminal", tabs: ["terminal"], icon: MonitorDot, label: t("terminal.title") },
        { id: "network", tabs: ["network"], icon: Globe, label: t("network.title") },
        { id: "external-edit", tabs: ["external-edit"], icon: PencilLine, label: t("externalEdit.settings.tab") },
      ],
    },
    {
      id: "ai",
      label: t("settings.group.ai"),
      items: [{ id: "ai", tabs: ["ai"], icon: Bot, label: t("settings.nav.ai") }],
    },
    {
      id: "data",
      label: t("settings.group.data"),
      items: [
        { id: "import", tabs: ["import", "backup"], icon: Import, label: t("settings.nav.importBackup") },
        { id: "custom-types", tabs: ["custom-types"], icon: Blocks, label: t("customType.tabTitle") },
        {
          id: "extensions",
          tabs: ["extensions"],
          icon: Puzzle,
          label: t("extension.title"),
          badge:
            updateCount > 0 ? (
              <span className="rounded-full bg-primary/15 px-1.5 text-[11px] font-medium leading-4 text-primary">
                <span aria-hidden>{updateCount}</span>
                <span className="sr-only">{t("extension.updatesAvailable", { count: updateCount })}</span>
              </span>
            ) : null,
        },
      ],
    },
    {
      id: "about",
      label: t("settings.group.about"),
      items: [
        { id: "about", tabs: ["about"], icon: Info, label: t("appUpdate.title") },
        { id: "status", tabs: ["status"], icon: Activity, label: t("systemStatus.title") },
      ],
    },
  ];

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 border-b">
        <h2 className="font-semibold">{t("nav.settings")}</h2>
      </div>
      <div className="flex flex-1 min-h-0">
        <nav aria-label={t("settings.nav.label")} className="w-44 shrink-0 overflow-y-auto border-r p-2 space-y-3">
          {groups.map((group) => (
            <div key={group.id} role="group" aria-label={group.label} className="space-y-0.5">
              <div className="px-2 py-1 text-xs font-medium text-muted-foreground">{group.label}</div>
              {group.items.map((item) => {
                const current = item.tabs.includes(activeTab);
                return (
                  <button
                    key={item.id}
                    type="button"
                    aria-current={current ? "page" : undefined}
                    onClick={() => setActiveTab(item.id)}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
                      current ? "bg-accent text-accent-foreground font-medium" : "hover:bg-accent/50"
                    )}
                  >
                    <item.icon className="h-3.5 w-3.5 shrink-0" />
                    <span className="min-w-0 flex-1 truncate">{item.label}</span>
                    {item.badge}
                  </button>
                );
              })}
            </div>
          ))}
        </nav>
        <div className="flex-1 min-w-0 overflow-y-auto p-4 scroll-stable">
          <div className="space-y-4 max-w-4xl mx-auto">
            {activeTab === "ai" && (
              <>
                <AISettingsSection />
                <CommandReviewSection />
              </>
            )}
            {(activeTab === "import" || activeTab === "backup") && (
              <>
                <ImportSection />
                <BackupSection />
              </>
            )}
            {activeTab === "shortcuts" && (
              <Card>
                <CardHeader>
                  <CardTitle className="text-base">{t("shortcut.title")}</CardTitle>
                  <CardDescription>{t("shortcut.desc")}</CardDescription>
                </CardHeader>
                <CardContent>
                  <ShortcutSettings />
                </CardContent>
              </Card>
            )}
            {activeTab === "terminal" && (
              <>
                <TerminalSection />
                <ConnectionSection />
              </>
            )}
            {activeTab === "network" && <NetworkSection />}
            {activeTab === "external-edit" && <ExternalEditSection />}
            {activeTab === "appearance" && <AppearanceSection />}
            {activeTab === "custom-types" && <CustomTypeSection />}
            {activeTab === "about" && <UpdateSection />}
            {activeTab === "status" && <SystemStatusSection />}
            {activeTab === "extensions" && <ExtensionSection />}
          </div>
        </div>
      </div>
    </div>
  );
}
