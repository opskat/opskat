import type { ReactNode } from "react";
import { Scaling } from "lucide-react";
import { formatDuration } from "./remoteChrome";

export function RemoteStatusBar({
  width,
  height,
  showFit,
  fitLabel,
  connected,
  elapsed,
  extra,
}: {
  width: number;
  height: number;
  showFit: boolean;
  fitLabel: string;
  connected: boolean;
  elapsed: number;
  extra?: ReactNode;
}) {
  return (
    <div className="flex h-6 shrink-0 items-center gap-3 border-t bg-muted/30 px-3 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1.5 font-mono">
        <Scaling className="h-3 w-3" />
        {width > 0 ? `${width} × ${height}` : "—"}
      </span>
      {showFit && (
        <span className="rounded border border-info/25 bg-info/15 px-1.5 py-px text-[11px] text-info">{fitLabel}</span>
      )}
      {extra}
      {connected && <span className="ml-auto font-mono tabular-nums">{formatDuration(elapsed)}</span>}
    </div>
  );
}
