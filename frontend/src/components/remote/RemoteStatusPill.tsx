import { cn } from "@opskat/ui";
import type { RemoteStatus } from "./remoteChrome";

const STATUS_PILL: Record<RemoteStatus, string> = {
  connecting: "border-warning/25 bg-warning/15 text-warning",
  connected: "border-success/25 bg-success/15 text-success",
  error: "border-destructive/25 bg-destructive/15 text-destructive",
  closed: "border-border bg-muted text-muted-foreground",
};

export function RemoteStatusPill({ status, label, testid }: { status: RemoteStatus; label: string; testid?: string }) {
  return (
    <span
      data-testid={testid}
      className={cn(
        "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-0.5 text-[11px] font-medium",
        STATUS_PILL[status]
      )}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {label}
    </span>
  );
}
