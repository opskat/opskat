import { AlertTriangle, Loader2, Power, RefreshCw, Settings2 } from "lucide-react";
import { Button } from "@opskat/ui";
import type { RemoteStatus } from "./remoteChrome";

export function RemoteConnectionOverlay({
  status,
  error,
  host,
  port,
  labels,
  onReconnect,
  onEdit,
  reconnectTestId,
  editTestId,
}: {
  status: RemoteStatus;
  error: string;
  host: string;
  port: number;
  labels: { connecting: string; error: string; closed: string; reconnect: string; edit?: string };
  onReconnect: () => void;
  onEdit?: () => void;
  reconnectTestId?: string;
  editTestId?: string;
}) {
  if (status === "connected") return null;

  if (status === "connecting") {
    return (
      <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-background text-sm text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin text-primary" />
        <div className="font-medium text-foreground">{labels.connecting}</div>
        {host && (
          <div className="font-mono text-xs">
            {host}:{port}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-background px-6 text-center">
      {status === "error" ? (
        <AlertTriangle className="h-8 w-8 text-destructive" />
      ) : (
        <Power className="h-8 w-8 text-muted-foreground" />
      )}
      <div className="text-base font-semibold text-foreground">{status === "error" ? labels.error : labels.closed}</div>
      {status === "error" && error && <div className="max-w-xl break-words text-sm text-muted-foreground">{error}</div>}
      <div className="mt-1 flex items-center gap-2.5">
        <Button type="button" size="sm" className="gap-1.5" data-testid={reconnectTestId} onClick={onReconnect}>
          <RefreshCw className="h-3.5 w-3.5" />
          {labels.reconnect}
        </Button>
        {onEdit && labels.edit && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="gap-1.5"
            data-testid={editTestId}
            onClick={onEdit}
          >
            <Settings2 className="h-3.5 w-3.5" />
            {labels.edit}
          </Button>
        )}
      </div>
    </div>
  );
}
