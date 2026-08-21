import {
  Clipboard,
  ClipboardCheck,
  FolderOpen,
  Keyboard,
  Maximize2,
  Minimize2,
  ScreenShare,
  Unplug,
} from "lucide-react";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, cn } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/asset/fields";
import { RemoteStatusPill } from "@/components/remote/RemoteStatusPill";
import type { RemoteStatus } from "@/components/remote/remoteChrome";

export type VNCViewMode = "fit" | "original";
export type VNCSpecialKey = "ctrl-alt-del" | "alt-tab" | "esc";

const SPECIAL_KEYS: { id: VNCSpecialKey; testid: string; label: string }[] = [
  { id: "ctrl-alt-del", testid: "vnc-key-cad", label: "Ctrl + Alt + Del" },
  { id: "alt-tab", testid: "vnc-key-alt-tab", label: "Alt + Tab" },
  { id: "esc", testid: "vnc-key-esc", label: "Esc" },
];

export function VNCToolbar({
  assetName,
  host,
  port,
  status,
  statusLabel,
  viewMode,
  clipboardEnabled,
  filesEnabled,
  filesOpen,
  isFullscreen,
  onViewModeChange,
  onSendSpecialKey,
  onToggleClipboard,
  onToggleFiles,
  onToggleFullscreen,
  onDisconnect,
}: {
  assetName: string;
  host: string;
  port: number;
  status: RemoteStatus;
  statusLabel: string;
  viewMode: VNCViewMode;
  clipboardEnabled: boolean;
  filesEnabled: boolean;
  filesOpen: boolean;
  isFullscreen: boolean;
  onViewModeChange: (m: VNCViewMode) => void;
  onSendSpecialKey: (k: VNCSpecialKey) => void;
  onToggleClipboard: () => void;
  onToggleFiles: () => void;
  onToggleFullscreen: () => void;
  onDisconnect: () => void;
}) {
  const { t } = useTranslation();
  const connected = status === "connected";

  return (
    <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/30 px-3 text-xs">
      <ScreenShare className="h-4 w-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0 truncate font-medium text-foreground">{assetName}</span>
      {host && (
        <span className="shrink-0 whitespace-nowrap rounded border bg-background/50 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
          {host}:{port}
        </span>
      )}
      <RemoteStatusPill status={status} label={statusLabel} testid="vnc-status" />

      <div className="ml-auto flex shrink-0 items-center gap-2">
        <Segmented<VNCViewMode>
          value={viewMode}
          onChange={onViewModeChange}
          aria-label={t("vnc.viewMode")}
          className="h-7 w-[116px] rounded-md p-0.5"
          options={[
            { value: "fit", label: t("vnc.viewFit"), testid: "vnc-view-fit" },
            { value: "original", label: t("vnc.viewOriginal"), testid: "vnc-view-original" },
          ]}
        />

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-7 gap-1.5 px-2"
              data-testid="vnc-special-keys"
              title={t("vnc.specialKeys")}
              disabled={!connected}
            >
              <Keyboard className="h-3.5 w-3.5" />
              Ctrl+Alt+Del
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {SPECIAL_KEYS.map((key) => (
              <DropdownMenuItem key={key.id} data-testid={key.testid} onSelect={() => onSendSpecialKey(key.id)}>
                {key.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <Button
          type="button"
          variant="outline"
          size="icon"
          data-testid="vnc-clipboard"
          data-state={clipboardEnabled ? "on" : "off"}
          title={clipboardEnabled ? t("vnc.clipboardOn") : t("vnc.clipboardOff")}
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
          data-testid="vnc-files"
          data-state={filesOpen ? "on" : "off"}
          title={filesEnabled ? t("vnc.files") : t("vnc.fileChannelUnavailable")}
          className={cn("h-7 w-7", filesOpen && "text-primary")}
          disabled={!filesEnabled}
          onClick={onToggleFiles}
        >
          <FolderOpen className="h-3.5 w-3.5" />
        </Button>

        <Button
          type="button"
          variant="outline"
          size="icon"
          className="h-7 w-7"
          data-testid="vnc-fullscreen"
          title={isFullscreen ? t("vnc.exitFullscreen") : t("vnc.fullscreen")}
          onClick={onToggleFullscreen}
        >
          {isFullscreen ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}
        </Button>

        <Button
          type="button"
          variant="outline"
          size="icon"
          className="h-7 w-7 text-destructive hover:text-destructive"
          data-testid="vnc-disconnect"
          title={t("vnc.disconnect")}
          disabled={!connected}
          onClick={onDisconnect}
        >
          <Unplug className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  );
}
