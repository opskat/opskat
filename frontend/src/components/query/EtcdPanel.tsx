import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useResizeHandle, ConfirmDialog } from "@opskat/ui";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { EtcdTreePane } from "@/components/etcd/EtcdTreePane";
import { EtcdQueryBar } from "@/components/etcd/EtcdQueryBar";
import { EtcdResultTable } from "@/components/etcd/EtcdResultTable";
import { EtcdKeyDetail } from "@/components/etcd/EtcdKeyDetail";

export interface EtcdPanelProps {
  tabId: string;
}

type View = "tree" | "query";

export function EtcdPanel({ tabId }: EtcdPanelProps) {
  const { t } = useTranslation();
  const tab = useTabStore((s) => s.tabs.find((tt) => tt.id === tabId));
  const meta = tab?.meta as QueryTabMeta | undefined;
  const assetId = meta?.assetId;

  const [view, setView] = useState<View>("tree");
  const [selectedKey, setSelectedKey] = useState<string | null>(null);

  // Destructive confirm — 用 Promise + ref 把 ConfirmDialog 的 onConfirm/onOpenChange 转成 await。
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [confirmCommand, setConfirmCommand] = useState("");
  const resolveRef = useRef<((ok: boolean) => void) | null>(null);

  function requestDestructive(command: string): Promise<boolean> {
    setConfirmCommand(command);
    setConfirmOpen(true);
    return new Promise<boolean>((resolve) => {
      // 上一个还挂着（理论上不会，UI 是单实例）—— 防御性放掉
      resolveRef.current?.(false);
      resolveRef.current = resolve;
    });
  }

  function settleConfirm(ok: boolean) {
    setConfirmOpen(false);
    const fn = resolveRef.current;
    resolveRef.current = null;
    fn?.(ok);
  }

  const sidebarRef = useRef<HTMLDivElement>(null);
  const { size: sidebarWidth, handleMouseDown } = useResizeHandle({
    defaultSize: 260,
    minSize: 180,
    maxSize: 480,
    targetRef: sidebarRef,
  });

  if (!assetId) {
    return <div className="p-3 text-xs text-destructive">missing asset id</div>;
  }

  return (
    <div className="flex h-full w-full">
      {/* Left: KV tree */}
      <div ref={sidebarRef} className="shrink-0 border-r" style={{ width: sidebarWidth }}>
        <EtcdTreePane assetId={assetId} onSelectKey={setSelectedKey} selectedKey={selectedKey} />
      </div>

      {/* Resize handle */}
      <div className="w-1 shrink-0 cursor-col-resize hover:bg-accent active:bg-accent" onMouseDown={handleMouseDown} />

      {/* Right: tabs (tree-detail / query-table) */}
      <div className="flex min-w-0 flex-1 flex-col">
        <div role="tablist" className="flex h-8 shrink-0 items-stretch border-b bg-muted/30 text-xs">
          <button
            role="tab"
            aria-selected={view === "tree"}
            className={`px-3 ${view === "tree" ? "bg-background" : "text-muted-foreground hover:bg-background/50"}`}
            onClick={() => setView("tree")}
          >
            {t("etcd.tree.title")}
          </button>
          <button
            role="tab"
            aria-selected={view === "query"}
            className={`px-3 ${view === "query" ? "bg-background" : "text-muted-foreground hover:bg-background/50"}`}
            onClick={() => setView("query")}
          >
            {t("etcd.query.execute")}
          </button>
        </div>

        <div className="relative min-h-0 flex-1">
          {/* Tree-detail view */}
          <div className="absolute inset-0 flex flex-col" style={{ display: view === "tree" ? "flex" : "none" }}>
            <EtcdKeyDetail assetId={assetId} selectedKey={selectedKey} />
          </div>

          {/* Query view */}
          <div className="absolute inset-0 flex flex-col" style={{ display: view === "query" ? "flex" : "none" }}>
            <EtcdQueryBar assetId={assetId} onDestructive={requestDestructive} />
            <div className="min-h-0 flex-1 overflow-hidden">
              <EtcdResultTable />
            </div>
          </div>
        </div>
      </div>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={(open) => {
          // AlertDialog 关闭（按 Cancel / 点遮罩 / Esc）= 取消
          if (!open) settleConfirm(false);
        }}
        title={t("etcd.query.deleteConfirmTitle")}
        description={t("etcd.query.deleteConfirmBody", { key: confirmCommand })}
        onConfirm={() => settleConfirm(true)}
      />
    </div>
  );
}
