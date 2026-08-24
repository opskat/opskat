import { Clipboard, ClipboardCheck, Keyboard, Maximize2, Minimize2, Monitor, Unplug } from "lucide-react";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, cn } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/asset/fields";
import { SCANCODE } from "./rdpInput";
import { RemoteStatusPill } from "@/components/remote/RemoteStatusPill";
import { RemoteStatusBar } from "@/components/remote/RemoteStatusBar";
import { RemoteConnectionOverlay } from "@/components/remote/RemoteConnectionOverlay";

export type RDPViewMode = "fit" | "actual";
export type RDPStatus = "connecting" | "connected" | "error" | "closed";

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
      <RemoteStatusPill
        status={status}
        label={status === "connected" ? t("asset.rdpConnected") : t(`asset.rdpStatus.${status}`)}
        testid="rdp-status"
      />

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
          <Unplug className="h-3.5 w-3.5" />
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
  return (
    <RemoteConnectionOverlay
      status={status}
      error={error}
      host={host}
      port={port}
      labels={{
        connecting: t("asset.rdpConnecting"),
        error: t("asset.rdpError"),
        closed: t("asset.rdpDisconnected"),
        reconnect: t("asset.rdpReconnect"),
        edit: t("asset.rdpEditConnection"),
      }}
      onReconnect={onReconnect}
      onEdit={onEdit}
      reconnectTestId="rdp-reconnect"
      editTestId="rdp-edit"
    />
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
    <RemoteStatusBar
      width={width}
      height={height}
      showFit={viewMode === "fit"}
      fitLabel={t("asset.rdpAutoFit")}
      connected={connected}
      elapsed={elapsed}
    />
  );
}
