import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { Copy, Search } from "lucide-react";

interface TerminalSelectionToolbarProps {
  position: { left: number; top: number };
  onCopy: () => void;
  onFind?: () => void;
}

export function TerminalSelectionToolbar({ position, onCopy, onFind }: TerminalSelectionToolbarProps) {
  const { t } = useTranslation();

  return (
    <div
      data-terminal-selection-toolbar
      className="absolute z-20 h-8 gap-1.5 shadow-md"
      style={{
        left: position.left,
        top: position.top,
        transform: "translateX(-50%)",
      }}
      onMouseDown={(event) => {
        event.preventDefault();
        event.stopPropagation();
      }}
      onMouseUp={(event) => {
        event.preventDefault();
        event.stopPropagation();
      }}
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
      }}
    >
      <div className="flex h-8 items-center rounded-md border bg-popover p-0.5 text-popover-foreground shadow-md">
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          title={t("ssh.contextMenu.copy")}
          aria-label={t("ssh.contextMenu.copy")}
          onClick={onCopy}
        >
          <Copy className="h-3.5 w-3.5" />
        </Button>
        {onFind && (
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className="h-7 w-7"
            title={t("ssh.contextMenu.find")}
            aria-label={t("ssh.contextMenu.find")}
            onClick={onFind}
          >
            <Search className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>
    </div>
  );
}
