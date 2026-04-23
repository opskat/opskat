import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { FolderOpen, Folder, FileCode } from "lucide-react";
import { Button } from "@opskat/ui";
import { useSFTPStore } from "@/stores/sftpStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { useTabStore, type TerminalTabMeta } from "@/stores/tabStore";
import { SnippetPopover } from "@/components/snippet/SnippetPopover";
import { WriteSSH } from "../../../wailsjs/go/app/App";

interface TerminalToolbarProps {
  tabId: string;
}

// Chunked binary -> base64 to match Terminal.tsx behavior (avoid stack overflow on large pastes).
function bytesToBase64(bytes: Uint8Array): string {
  const CHUNK = 0x8000;
  let binary = "";
  for (let i = 0; i < bytes.length; i += CHUNK) {
    const slice = bytes.subarray(i, i + CHUNK);
    binary += String.fromCharCode.apply(null, slice as unknown as number[]);
  }
  return btoa(binary);
}

export function TerminalToolbar({ tabId }: TerminalToolbarProps) {
  const { t } = useTranslation();
  const tabData = useTerminalStore((s) => s.tabData[tabId]);
  const toggleFileManager = useSFTPStore((s) => s.toggleFileManager);
  const isOpen = useSFTPStore((s) => s.fileManagerOpenTabs[tabId]);
  const tab = useTabStore((s) => s.tabs.find((t) => t.id === tabId));

  const assetId = tab?.type === "terminal" ? (tab.meta as TerminalTabMeta).assetId : undefined;
  const activePaneId = tabData?.activePaneId;
  const activePaneConnected = activePaneId ? (tabData?.panes[activePaneId]?.connected ?? false) : false;

  const handleSnippetInsert = useCallback(
    (content: string, { withEnter }: { withEnter: boolean }) => {
      if (!activePaneId) return;
      const payload = withEnter ? content + "\r" : content;
      WriteSSH(activePaneId, bytesToBase64(new TextEncoder().encode(payload))).catch(console.error);
    },
    [activePaneId]
  );

  if (!tabData) return null;
  if (Object.keys(tabData.panes).length === 0) return null;

  const Icon = isOpen ? FolderOpen : Folder;

  return (
    <div className="flex items-center gap-1 px-2 py-1 border-t bg-background shrink-0">
      <div className="flex-1" />
      <SnippetPopover
        category="shell"
        assetId={assetId}
        showSendWithEnter
        onInsert={handleSnippetInsert}
        trigger={
          <Button
            variant="ghost"
            size="icon-xs"
            title={t("snippet.popover.triggerButton")}
            aria-label={t("snippet.popover.triggerButton")}
            disabled={!activePaneConnected}
          >
            <FileCode className="h-3.5 w-3.5" />
          </Button>
        }
      />
      <Button
        variant={isOpen ? "secondary" : "ghost"}
        size="icon-xs"
        title={t("sftp.fileManager")}
        onClick={() => toggleFileManager(tabId)}
      >
        <Icon className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}
