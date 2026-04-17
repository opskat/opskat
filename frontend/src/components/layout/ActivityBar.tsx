import { useTranslation } from "react-i18next";
import { Server, LayoutList } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger, cn } from "@opskat/ui";
import { useLayoutStore, type SidePanel } from "@/stores/layoutStore";

const items: Array<{ id: SidePanel; Icon: typeof Server; labelKey: string }> = [
  { id: "assets", Icon: Server, labelKey: "sideTabs.assetsPanel" },
  { id: "tabs", Icon: LayoutList, labelKey: "sideTabs.tabsPanel" },
];

export function ActivityBar() {
  const { t } = useTranslation();
  const activeSidePanel = useLayoutStore((s) => s.activeSidePanel);
  const leftPanelVisible = useLayoutStore((s) => s.leftPanelVisible);
  const setActivePanel = useLayoutStore((s) => s.setActivePanel);
  const toggleVisible = useLayoutStore((s) => s.toggleVisible);

  const onClickItem = (id: SidePanel) => {
    if (id === activeSidePanel && leftPanelVisible) {
      toggleVisible();
    } else {
      setActivePanel(id);
      if (!leftPanelVisible) toggleVisible();
    }
  };

  return (
    <div
      className="w-11 shrink-0 flex flex-col items-center py-2 gap-1 border-r bg-background"
      style={{ "--wails-draggable": "no-drag" } as React.CSSProperties}
    >
      {items.map(({ id, Icon, labelKey }) => {
        const isActive = id === activeSidePanel && leftPanelVisible;
        return (
          <Tooltip key={id} delayDuration={300}>
            <TooltipTrigger asChild>
              <button
                type="button"
                onClick={() => onClickItem(id)}
                className={cn(
                  "w-9 h-9 rounded-md flex items-center justify-center transition-colors",
                  isActive ? "bg-primary/10 text-primary" : "text-muted-foreground hover:text-foreground hover:bg-muted"
                )}
                aria-label={t(labelKey)}
              >
                <Icon className="h-4 w-4" />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">{t(labelKey)}</TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
}
