import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { createPortal } from "react-dom";
import {
  Clipboard,
  Copy,
  Download,
  Edit3,
  FilePlus,
  FolderDown,
  FolderOpen,
  FolderPlus,
  Info,
  KeyRound,
  PencilLine,
  RefreshCw,
  Scissors,
  Terminal,
  Trash2,
} from "lucide-react";
import { cn, computeContextMenuPosition } from "@opskat/ui";
import { type CtxMenuState } from "./types";

interface FloatingMenuProps {
  canPaste: boolean;
  ctx: CtxMenuState;
  onAction: (action: string) => void;
  onClose: () => void;
}

export function FloatingMenu({ canPaste, ctx, onAction, onClose }: FloatingMenuProps) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ top: 0, left: 0 });
  const [visible, setVisible] = useState(false);
  const [interactive, setInteractive] = useState(false);

  useLayoutEffect(() => {
    if (!ref.current) return;
    const rect = ref.current.getBoundingClientRect();
    const next = computeContextMenuPosition({
      anchorX: ctx.x,
      anchorY: ctx.y,
      width: rect.width,
      height: rect.height,
      viewportWidth: window.innerWidth,
      viewportHeight: window.innerHeight,
    });
    setPos({ top: next.top, left: next.left });
    setVisible(true);
  }, [ctx.x, ctx.y]);

  useEffect(() => {
    const timer = setTimeout(() => setInteractive(true), 150);
    return () => clearTimeout(timer);
  }, []);

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    const handlePointer = (e: PointerEvent) => {
      if (ref.current?.contains(e.target as Node)) return;
      onClose();
    };
    const timer = setTimeout(() => {
      document.addEventListener("pointerdown", handlePointer, true);
    }, 50);
    document.addEventListener("keydown", handleKey);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("pointerdown", handlePointer, true);
      document.removeEventListener("keydown", handleKey);
    };
  }, [onClose]);

  const item = (
    action: string,
    icon: ReactNode,
    label: string,
    opts: { destructive?: boolean; disabled?: boolean } = {}
  ) => (
    <button
      key={action}
      type="button"
      disabled={opts.disabled}
      className={cn(
        "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm cursor-default select-none disabled:pointer-events-none disabled:opacity-40 [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        opts.disabled && "pointer-events-none opacity-40",
        opts.destructive
          ? "text-destructive hover:bg-destructive/10 [&_svg]:text-destructive"
          : "hover:bg-accent hover:text-accent-foreground [&_svg:not([class*='text-'])]:text-muted-foreground"
      )}
      onClick={() => !opts.disabled && onAction(action)}
    >
      {icon}
      {label}
    </button>
  );

  const separator = <div className="-mx-1 my-1 h-px bg-border" />;
  const multiCount = ctx.selectedEntries.length;
  const multi = multiCount > 1;

  return createPortal(
    <div
      ref={ref}
      className={cn(
        "z-50 min-w-[13rem] overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md",
        visible && "animate-in fade-in-0 zoom-in-95"
      )}
      style={{
        position: "fixed",
        top: pos.top,
        left: pos.left,
        visibility: visible ? "visible" : "hidden",
        pointerEvents: interactive ? "auto" : "none",
      }}
    >
      {multi ? (
        <>
          {item("downloadSelected", <Download />, t("sftp.menu.downloadSelected", { count: multiCount }))}
          {separator}
          {item("cutSelected", <Scissors />, t("sftp.menu.cutSelected", { count: multiCount }))}
          {item("copySelected", <Copy />, t("sftp.menu.copySelected", { count: multiCount }))}
          {item("copySelectedFilePaths", <Copy />, t("sftp.menu.copySelectedFilePaths", { count: multiCount }))}
          {item("paste", <Clipboard />, t("sftp.menu.paste"), { disabled: !canPaste })}
          {separator}
          {item("deleteSelected", <Trash2 />, t("sftp.menu.deleteSelected", { count: multiCount }), {
            destructive: true,
          })}
        </>
      ) : ctx.entry ? (
        ctx.entry.isDir ? (
          <>
            {item("open", <FolderOpen />, t("action.open"))}
            {item("openTerminal", <Terminal />, t("sftp.menu.openTerminal"))}
            {item("downloadDir", <FolderDown />, t("sftp.menu.downloadFolder"))}
            {separator}
            {item("cut", <Scissors />, t("sftp.menu.cut"))}
            {item("copy", <Copy />, t("sftp.menu.copy"))}
            {item("copyFilePath", <Copy />, t("sftp.menu.copyFilePath"))}
            {item("paste", <Clipboard />, t("sftp.menu.paste"), { disabled: !canPaste })}
            {separator}
            {item("rename", <Edit3 />, t("sftp.menu.rename"))}
            {item("permission", <KeyRound />, t("sftp.menu.permission"))}
            {item("properties", <Info />, t("sftp.menu.properties"))}
            {separator}
            {item("delete", <Trash2 />, t("sftp.menu.delete"), { destructive: true })}
          </>
        ) : (
          <>
            {ctx.canExternalEdit && (
              <>
                {item("externalEdit", <PencilLine />, t("externalEdit.actions.open"))}
                {separator}
              </>
            )}
            {item("download", <Download />, t("sftp.menu.downloadFile"))}
            {separator}
            {item("cut", <Scissors />, t("sftp.menu.cut"))}
            {item("copy", <Copy />, t("sftp.menu.copy"))}
            {item("copyFilePath", <Copy />, t("sftp.menu.copyFilePath"))}
            {item("paste", <Clipboard />, t("sftp.menu.paste"), { disabled: !canPaste })}
            {separator}
            {item("rename", <Edit3 />, t("sftp.menu.rename"))}
            {item("permission", <KeyRound />, t("sftp.menu.permission"))}
            {item("properties", <Info />, t("sftp.menu.properties"))}
            {separator}
            {item("delete", <Trash2 />, t("sftp.menu.delete"), { destructive: true })}
          </>
        )
      ) : (
        <>
          {item("refresh", <RefreshCw />, t("action.refresh"))}
          {separator}
          {item("newFolder", <FolderPlus />, t("sftp.newFolder"))}
          {item("newFile", <FilePlus />, t("sftp.menu.newFile"))}
          {item("paste", <Clipboard />, t("sftp.menu.paste"), { disabled: !canPaste })}
          {item("copyCurrentPath", <Copy />, t("sftp.menu.copyCurrentPath"))}
        </>
      )}
    </div>,
    document.body
  );
}
