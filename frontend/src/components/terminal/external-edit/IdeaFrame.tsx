import type { ReactNode } from "react";
import { cn } from "@opskat/ui";

interface IdeaFrameProps {
  actions?: ReactNode;
  children: ReactNode;
  fileName: string;
  helper: string;
  layoutLabel: string;
  mode: "compare" | "merge";
  remotePath: string;
  sidebarLabel: string;
  status: string;
  subtitle?: string;
  testId: string;
  title: string;
}

export function ExternalEditIdeaFrame({
  actions,
  children,
  fileName,
  helper,
  layoutLabel,
  mode,
  remotePath,
  sidebarLabel,
  status,
  subtitle,
  testId,
  title,
}: IdeaFrameProps) {
  return (
    <div
      className={cn(
        "fixed z-50 flex overflow-hidden rounded-xl border border-border bg-background text-foreground shadow-2xl",
        mode === "compare" ? "inset-4" : "inset-3"
      )}
      data-idea-workbench={mode}
      data-testid={testId}
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <div className="flex w-56 shrink-0 flex-col border-r border-border bg-card">
        <div className="border-b border-border px-4 py-3 text-[11px] font-semibold uppercase tracking-[0.18em] text-muted-foreground">
          {sidebarLabel}
        </div>
        <div className="flex-1 px-3 py-4">
          <div
            className={cn(
              "rounded-md px-3 py-2 text-sm font-medium",
              mode === "merge"
                ? "border border-warning/40 bg-warning/10 text-warning"
                : "bg-secondary text-secondary-foreground"
            )}
            data-testid={`external-edit-${mode}-idea-file`}
          >
            {fileName}
          </div>
          <div className="mt-3 break-all text-xs leading-5 text-muted-foreground">{remotePath}</div>
        </div>
        <div className="border-t border-border px-3 py-3 text-[11px] text-muted-foreground">{helper}</div>
      </div>
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-12 items-center justify-between border-b border-border bg-card px-4">
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">{title}</div>
            <div className="truncate text-[11px] text-muted-foreground">{subtitle || remotePath}</div>
          </div>
          {actions}
        </div>
        {children}
        <div className="flex h-8 items-center justify-between border-t border-border bg-card px-4 text-[11px] text-muted-foreground">
          <span>{status}</span>
          <span>{layoutLabel}</span>
        </div>
      </div>
    </div>
  );
}

interface IdeaEditorPaneProps {
  actions?: ReactNode;
  badge: string;
  children: ReactNode;
  tone: "local" | "final" | "remote";
  title: string;
}

export function ExternalEditIdeaEditorPane({ actions, badge, children, tone, title }: IdeaEditorPaneProps) {
  return (
    <div
      className={cn("flex min-h-0 flex-col bg-background", tone === "final" && "ring-1 ring-warning/40")}
      data-idea-pane={tone}
      data-testid={`external-edit-idea-pane-${tone}`}
    >
      <div
        className={cn(
          "flex h-9 items-center justify-between border-b px-3 text-xs",
          tone === "final" ? "border-warning/30 bg-warning/10" : "border-border bg-card"
        )}
      >
        <span
          className={cn(
            "font-semibold",
            tone === "local" && "text-success",
            tone === "remote" && "text-info",
            tone === "final" && "text-warning"
          )}
        >
          {title}
        </span>
        <div className="flex items-center gap-1.5">
          {actions}
          <span
            className={cn(
              "rounded px-2 py-0.5 text-[10px] uppercase tracking-wide",
              tone === "final" ? "bg-warning/20 text-warning" : "bg-muted text-muted-foreground"
            )}
          >
            {badge}
          </span>
        </div>
      </div>
      {children}
    </div>
  );
}
