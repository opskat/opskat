import { useTranslation } from "react-i18next";
import { ArrowUp, FolderSync, Home, RefreshCw } from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
} from "@opskat/ui";
import { type TerminalDirectoryFollowMode } from "@/stores/terminalStore";

export type DirectorySyncMenuMode = "panel-from-terminal" | "terminal-from-panel" | "follow" | null;

interface PathToolbarProps {
  activeSyncMode: DirectorySyncMenuMode;
  currentPath: string;
  directoryFollowMode: TerminalDirectoryFollowMode;
  onFollowToggle: () => void;
  onGoHome: () => void;
  onGoUp: () => void;
  onPathInputChange: (path: string) => void;
  onPathSubmit: (path: string) => void;
  onRefresh: () => void;
  onSyncPanelFromTerminal: () => void;
  onSyncTerminalToPath: () => void;
  paneConnected: boolean;
  pathInput: string;
}

export function PathToolbar({
  activeSyncMode,
  currentPath,
  directoryFollowMode,
  onFollowToggle,
  onGoHome,
  onGoUp,
  onPathInputChange,
  onPathSubmit,
  onRefresh,
  onSyncPanelFromTerminal,
  onSyncTerminalToPath,
  paneConnected,
  pathInput,
}: PathToolbarProps) {
  const { t } = useTranslation();
  const followEnabled = directoryFollowMode === "always";

  return (
    <div className="flex items-center gap-0.5 px-1 py-1 border-b shrink-0">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant={followEnabled ? "secondary" : "ghost"}
            size="xs"
            className="px-1.5 text-[10px] font-medium"
            title={t("sftp.sync.followToggle")}
            aria-label={t("sftp.sync.followShort")}
            disabled={!paneConnected}
          >
            <FolderSync className="h-3.5 w-3.5" />
            {followEnabled ? t("sftp.sync.followActive") : t("sftp.sync.followShort")}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuCheckboxItem
            checked={!followEnabled && activeSyncMode === "panel-from-terminal"}
            onCheckedChange={onSyncPanelFromTerminal}
          >
            {t("sftp.sync.panelFromTerminal")}
          </DropdownMenuCheckboxItem>
          <DropdownMenuCheckboxItem
            checked={!followEnabled && activeSyncMode === "terminal-from-panel"}
            onCheckedChange={onSyncTerminalToPath}
          >
            {t("sftp.sync.terminalFromPanel")}
          </DropdownMenuCheckboxItem>
          <DropdownMenuSeparator />
          <DropdownMenuCheckboxItem checked={followEnabled} onCheckedChange={onFollowToggle}>
            {t("sftp.sync.followToggle")}
          </DropdownMenuCheckboxItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onGoUp}
        disabled={currentPath === "/"}
        title={t("sftp.parentDir")}
      >
        <ArrowUp className="h-3.5 w-3.5" />
      </Button>
      <Button variant="ghost" size="icon-xs" onClick={onGoHome} title={t("sftp.home")}>
        <Home className="h-3.5 w-3.5" />
      </Button>
      <Input
        className="h-6 text-xs flex-1 min-w-0"
        value={pathInput}
        onChange={(e) => onPathInputChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") onPathSubmit(pathInput.trim());
        }}
      />
      <Button variant="ghost" size="icon-xs" onClick={onRefresh} title={t("sftp.refresh")}>
        <RefreshCw className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}
