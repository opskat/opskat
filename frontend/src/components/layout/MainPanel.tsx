import { useTranslation } from "react-i18next";
import { X, TerminalSquare, Cat } from "lucide-react";
import { AssetDetail } from "@/components/asset/AssetDetail";
import { Terminal } from "@/components/terminal/Terminal";
import { SettingsPage } from "@/components/settings/SettingsPage";
import { useTerminalStore } from "@/stores/terminalStore";
import { cn } from "@/lib/utils";
import { asset_entity } from "../../../wailsjs/go/models";

interface MainPanelProps {
  activePage: string;
  selectedAsset: asset_entity.Asset | null;
  onEditAsset: (asset: asset_entity.Asset) => void;
  onDeleteAsset: (id: number) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
}

export function MainPanel({
  activePage,
  selectedAsset,
  onEditAsset,
  onDeleteAsset,
  onConnectAsset,
}: MainPanelProps) {
  const { t } = useTranslation();
  const { tabs, activeTabId, setActiveTab, removeTab } = useTerminalStore();

  if (activePage === "settings") {
    return (
      <div className="flex flex-1 flex-col min-w-0">
        <SettingsPage />
      </div>
    );
  }

  const showTerminal = activeTabId && tabs.some((tab) => tab.id === activeTabId);

  return (
    <div className="flex flex-1 flex-col min-w-0">
      {/* Tab bar */}
      {tabs.length > 0 && (
        <div className="flex items-center border-b overflow-x-auto bg-background">
          {selectedAsset && (
            <button
              className={cn(
                "relative flex items-center gap-1.5 px-3 py-2 text-sm shrink-0 cursor-pointer transition-colors duration-150",
                !showTerminal
                  ? "text-foreground"
                  : "text-muted-foreground hover:text-foreground hover:bg-muted/50"
              )}
              onClick={() => setActiveTab(null)}
            >
              {selectedAsset.Name}
              {!showTerminal && (
                <span className="absolute bottom-0 left-1 right-1 h-0.5 rounded-full bg-primary" />
              )}
            </button>
          )}
          {tabs.map((tab) => (
            <div
              key={tab.id}
              className={cn(
                "relative flex items-center gap-1.5 px-3 py-2 text-sm shrink-0 cursor-pointer transition-colors duration-150",
                activeTabId === tab.id
                  ? "text-foreground"
                  : "text-muted-foreground hover:text-foreground hover:bg-muted/50"
              )}
              onClick={() => setActiveTab(tab.id)}
            >
              <TerminalSquare className="h-3.5 w-3.5" />
              <span className="max-w-24 truncate">{tab.assetName}</span>
              {!tab.connected && (
                <span className="h-1.5 w-1.5 rounded-full bg-destructive shrink-0" />
              )}
              <button
                className="ml-1.5 rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors duration-150"
                onClick={(e) => {
                  e.stopPropagation();
                  removeTab(tab.id);
                }}
              >
                <X className="h-3 w-3" />
              </button>
              {activeTabId === tab.id && (
                <span className="absolute bottom-0 left-1 right-1 h-0.5 rounded-full bg-primary" />
              )}
            </div>
          ))}
        </div>
      )}

      {/* Content */}
      <div className="flex-1 relative">
        {tabs.map((tab) => (
          <div
            key={tab.id}
            className="absolute inset-0"
            style={{
              display: activeTabId === tab.id ? "block" : "none",
            }}
          >
            <Terminal sessionId={tab.id} active={activeTabId === tab.id} />
          </div>
        ))}

        {!showTerminal && selectedAsset && (
          <AssetDetail
            asset={selectedAsset}
            onEdit={() => onEditAsset(selectedAsset)}
            onDelete={() => onDeleteAsset(selectedAsset.ID)}
            onConnect={() => onConnectAsset(selectedAsset)}
          />
        )}

        {!showTerminal && !selectedAsset && (
          <div className="flex items-center justify-center h-full bg-gradient-to-br from-background via-background to-primary/5">
            <div className="text-center space-y-4">
              <div className="mx-auto flex h-16 w-16 items-center justify-center rounded-2xl bg-primary/10">
                <Cat className="h-8 w-8 text-primary" />
              </div>
              <div className="space-y-1">
                <h2 className="text-2xl font-semibold tracking-tight">
                  {t("app.title")}
                </h2>
                <p className="text-sm text-muted-foreground">
                  {t("app.subtitle")}
                </p>
              </div>
              <p className="text-xs text-muted-foreground/60">
                {t("app.hint")}
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
