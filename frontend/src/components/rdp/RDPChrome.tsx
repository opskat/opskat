import {
  AlertTriangle,
  Clipboard,
  ClipboardCheck,
  Keyboard,
  Loader2,
  Maximize2,
  Minimize2,
  Monitor,
  Power,
  RefreshCw,
  Scaling,
  Settings2,
} from "lucide-react";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, cn } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/asset/fields";
import { formatDuration, SCANCODE } from "./rdpInput";

export type RDPViewMode = "fit" | "actual";
export type RDPStatus = "connecting" | "connected" | "error" | "closed";

const STATUS_PILL: Record<RDPStatus, string> = {
  connecting: "border-warning/25 bg-warning/15 text-warning",
  connected: "border-success/25 bg-success/15 text-success",
  error: "border-destructive/25 bg-destructive/15 text-destructive",
  closed: "border-border bg-muted text-muted-foreground",
};

const SPECIAL_KEYS = [
  { testid: "rdp-key-cad", label: "Ctrl + Alt + Del", scancodes: [SCANCODE.ctrl, SCANCODE.alt, SCANCODE.del] },
  { testid: "rdp-key-alt-tab", label: "Alt + Tab", scancodes: [SCANCODE.alt, SCANCODE.tab] },
  { testid: "rdp-key-esc", label: "Esc", scancodes: [SCANCODE.esc] },
];

export function RDPToolbar({
  assetName,
  host,
  port,
  status,
  viewMode,
  isFullscreen,
  clipboardEnabled,
  onViewModeChange,
  onSendChord,
  onToggleFullscreen,
  onToggleClipboard,
  onDisconnect,
}: {
  assetName: string;
  host: string;
  port: number;
  status: RDPStatus;
  viewMode: RDPViewMode;
  isFullscreen: boolean;
  clipboardEnabled: boolean;
  onViewModeChange: (mode: RDPViewMode) => void;
  onSendChord: (scancodes: number[]) => void;
  onToggleFullscreen: () => void;
  onToggleClipboard: () => void;
  onDisconnect: () => void;
}) {
  const { t } = useTranslation();
  const connected = status === "connected";

  return (
    <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/30 px-3 text-xs">
      <Monitor className="h-4 w-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0 truncate font-medium text-foreground">{assetName}</span>
      {host && (
        <span className="shrink-0 whitespace-nowrap rounded border bg-background/50 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
          {host}:{port}
        </span>
      )}
      <span
        data-testid="rdp-status"
        className={cn(
          "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-0.5 font-medium",
          STATUS_PILL[status]
        )}
      >
        <span className="h-1.5 w-1.5 rounded-full bg-current" />
        {status === "connected" ? t("asset.rdpConnected") : t(`asset.rdpStatus.${status}`)}
      </span>

      <div className="ml-auto flex shrink-0 items-center gap-2">
        <Segmented<RDPViewMode>
          value={viewMode}
          onChange={onViewModeChange}
          aria-label={t("asset.rdpViewMode")}
          className="h-7 w-[116px] rounded-md p-0.5"
          options={[
            { value: "fit", label: t("asset.rdpFit"), testid: "rdp-view-fit" },
            { value: "actual", label: t("asset.rdpActual"), testid: "rdp-view-actual" },
          ]}
        />

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-7 gap-1.5 px-2"
              data-testid="rdp-special-keys"
              disabled={!connected}
            >
              <Keyboard className="h-3.5 w-3.5" />
              Ctrl+Alt+Del
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {SPECIAL_KEYS.map((key) => (
              <DropdownMenuItem key={key.testid} data-testid={key.testid} onSelect={() => onSendChord(key.scancodes)}>
                {key.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <Button
          type="button"
          variant="outline"
          size="icon"
          className="h-7 w-7"
          data-testid="rdp-fullscreen"
          title={isFullscreen ? t("asset.rdpExitFullscreen") : t("asset.rdpFullscreen")}
          onClick={onToggleFullscreen}
        >
          {isFullscreen ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}
        </Button>

        <Button
          type="button"
          variant="outline"
          size="icon"
          data-testid="rdp-clipboard"
          data-state={clipboardEnabled ? "on" : "off"}
          title={clipboardEnabled ? t("asset.rdpClipboardOn") : t("asset.rdpClipboardOff")}
          className={cn("h-7 w-7", clipboardEnabled ? "text-success" : "text-muted-foreground/60")}
          disabled={!connected}
          onClick={onToggleClipboard}
        >
          {clipboardEnabled ? <ClipboardCheck className="h-3.5 w-3.5" /> : <Clipboard className="h-3.5 w-3.5" />}
        </Button>

        <Button
          type="button"
          variant="outline"
          size="icon"
          className="h-7 w-7 text-destructive hover:text-destructive"
          data-testid="rdp-disconnect"
          title={t("asset.rdpDisconnect")}
          disabled={!connected}
          onClick={onDisconnect}
        >
          <Power className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  );
}

export function RDPSessionOverlay({
  status,
  error,
  host,
  port,
  onReconnect,
  onEdit,
}: {
  status: RDPStatus;
  error: string;
  host: string;
  port: number;
  onReconnect: () => void;
  onEdit?: () => void;
}) {
  const { t } = useTranslation();
  if (status === "connected") return null;

  if (status === "connecting") {
    return (
      <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-background text-sm text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin text-primary" />
        <div className="font-medium text-foreground">{t("asset.rdpConnecting")}</div>
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
      <div className="text-base font-semibold text-foreground">
        {status === "error" ? t("asset.rdpError") : t("asset.rdpDisconnected")}
      </div>
      {status === "error" && error && <div className="max-w-xl break-words text-sm text-muted-foreground">{error}</div>}
      <div className="mt-1 flex items-center gap-2.5">
        <Button type="button" size="sm" className="gap-1.5" data-testid="rdp-reconnect" onClick={onReconnect}>
          <RefreshCw className="h-3.5 w-3.5" />
          {t("asset.rdpReconnect")}
        </Button>
        {onEdit && (
          <Button type="button" variant="outline" size="sm" className="gap-1.5" data-testid="rdp-edit" onClick={onEdit}>
            <Settings2 className="h-3.5 w-3.5" />
            {t("asset.rdpEditConnection")}
          </Button>
        )}
      </div>
    </div>
  );
}

export function RDPStatusBar({
  width,
  height,
  viewMode,
  connected,
  elapsed,
}: {
  width: number;
  height: number;
  viewMode: RDPViewMode;
  connected: boolean;
  elapsed: number;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex h-6 shrink-0 items-center gap-3 border-t bg-muted/30 px-3 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1.5 font-mono">
        <Scaling className="h-3 w-3" />
        {width} × {height}
      </span>
      {viewMode === "fit" && (
        <span className="rounded border border-info/25 bg-info/15 px-1.5 py-px text-[11px] text-info">
          {t("asset.rdpAutoFit")}
        </span>
      )}
      {connected && <span className="ml-auto font-mono tabular-nums">{formatDuration(elapsed)}</span>}
    </div>
  );
}
