import { useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw } from "lucide-react";
import { Button, cn, useResizeHandle } from "@opskat/ui";

/** Shared sidebar width range (px) for every data panel. */
const SIDEBAR_MIN_WIDTH = 160;
const SIDEBAR_MAX_WIDTH = 420;
const SIDEBAR_DEFAULT_WIDTH = 220;

export interface PanelSidebarProps {
  /**
   * Panel kind (`database`, `redis`, `mongodb`, ...). The dragged width is persisted per panel kind
   * under `${panelType}_sidebar_width`, so every tab of one kind shares one width.
   */
  panelType: string;
  /** Header title, left-aligned (≥12px). */
  title: ReactNode;
  /** Header icon actions, right-aligned; build the refresh one with `<SidebarRefreshButton>`. */
  actions?: ReactNode;
  /** Filter box shown under the header. Pass it only for panels that already filter; omit it otherwise. */
  filter?: ReactNode;
  /** localStorage key override, for a panel whose width was persisted under a legacy key (K8s: `k8s_sidebar_width`). */
  storageKey?: string;
  /** Sidebar content (tree / list / load states), laid out below the header and filter. */
  children: ReactNode;
}

/**
 * Left sidebar shell shared by the data panels (docs/DESIGN.md §7): title + icon actions header,
 * optional filter row, scrolling content slot and the drag handle. It renders the sidebar *and* its
 * drag handle as two siblings, so place it directly inside the panel's horizontal flex row, before
 * the content area.
 */
export function PanelSidebar({ panelType, title, actions, filter, storageKey, children }: PanelSidebarProps) {
  const sidebarRef = useRef<HTMLDivElement>(null);
  const { size, handleMouseDown } = useResizeHandle({
    defaultSize: SIDEBAR_DEFAULT_WIDTH,
    minSize: SIDEBAR_MIN_WIDTH,
    maxSize: SIDEBAR_MAX_WIDTH,
    storageKey: storageKey ?? `${panelType}_sidebar_width`,
    targetRef: sidebarRef,
  });

  return (
    <>
      <div
        ref={sidebarRef}
        data-testid={`${panelType}-sidebar`}
        className="flex h-full shrink-0 flex-col overflow-hidden border-r border-border bg-sidebar"
        style={{ width: size }}
      >
        <div className="flex shrink-0 items-center justify-between gap-2 border-b border-border px-2 py-1.5">
          <span className="truncate text-xs font-semibold uppercase tracking-wider text-muted-foreground">{title}</span>
          {actions && <div className="flex shrink-0 gap-0.5">{actions}</div>}
        </div>
        {filter && <div className="shrink-0 border-b border-border px-2 py-1.5">{filter}</div>}
        <div className="flex min-h-0 flex-1 flex-col">{children}</div>
      </div>
      <div
        role="separator"
        aria-orientation="vertical"
        data-testid={`${panelType}-sidebar-resize`}
        className="w-[3px] shrink-0 cursor-col-resize transition-colors hover:bg-ring/40 active:bg-ring/60"
        onMouseDown={handleMouseDown}
      />
    </>
  );
}

/** Header refresh action: the icon spins and the button is disabled while `loading`. */
export function SidebarRefreshButton({
  onClick,
  loading,
  className,
}: {
  onClick: () => void;
  loading?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <Button
      variant="ghost"
      size="icon"
      className={cn("h-6 w-6", className)}
      onClick={onClick}
      disabled={loading}
      title={t("query.refreshTree")}
      aria-label={t("query.refreshTree")}
    >
      <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
    </Button>
  );
}
