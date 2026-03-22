import { Home, Settings, Cat } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { ModeToggle } from "@/components/mode-toggle";
import { cn } from "@/lib/utils";

interface SidebarProps {
  activePage: string;
  onPageChange: (page: string) => void;
}

export function Sidebar({ activePage, onPageChange }: SidebarProps) {
  const { t } = useTranslation();

  const navItems = [
    { id: "home", icon: Home, label: t("nav.home") },
    { id: "settings", icon: Settings, label: t("nav.settings") },
  ];

  return (
    <div className="flex h-full w-14 flex-col items-center border-r border-sidebar-border bg-sidebar/80 backdrop-blur-sm">
      {/* Drag region for Wails window */}
      <div
        className="h-8 w-full shrink-0"
        style={{ "--wails-draggable": "drag" } as React.CSSProperties}
      />

      {/* App logo */}
      <div className="mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10">
        <Cat className="h-5 w-5 text-primary" />
      </div>

      {/* Navigation */}
      <div className="flex flex-col items-center gap-1">
        {navItems.map((item) => (
          <Tooltip key={item.id}>
            <TooltipTrigger asChild>
              <button
                className={cn(
                  "relative flex h-9 w-9 items-center justify-center rounded-lg transition-colors duration-150",
                  activePage === item.id
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
                )}
                onClick={() => onPageChange(item.id)}
              >
                {activePage === item.id && (
                  <span className="absolute left-0 top-1/2 -translate-y-1/2 -translate-x-[calc(50%+1px)] h-4 w-1 rounded-full bg-primary" />
                )}
                <item.icon className="h-4 w-4" />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">{item.label}</TooltipContent>
          </Tooltip>
        ))}
      </div>

      <div className="mt-auto pb-2">
        <ModeToggle />
      </div>
    </div>
  );
}
