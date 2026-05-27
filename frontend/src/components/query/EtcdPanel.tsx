import { useCallback, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useResizeHandle, ConfirmDialog } from "@opskat/ui";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { EtcdTreePane } from "@/components/etcd/EtcdTreePane";
import { EtcdQueryBar } from "@/components/etcd/EtcdQueryBar";
import { EtcdResultTable } from "@/components/etcd/EtcdResultTable";
import { EtcdKeyDetail } from "@/components/etcd/EtcdKeyDetail";
import { EtcdClusterBar } from "@/components/etcd/EtcdClusterBar";
import { useEtcdStore } from "@/stores/etcdStore";

export interface EtcdPanelProps {
  tabId: string;
}

type View = "tree" | "query";
type ConfirmAction = "execCommand" | "deleteKey" | "saveKey";

interface ConfirmRequest {
  action: ConfirmAction;
  payload: string;
}

export function EtcdPanel({ tabId }: EtcdPanelProps) {
  const { t } = useTranslation();
  const tab = useTabStore((s) => s.tabs.find((tt) => tt.id === tabId));
  const meta = tab?.meta as QueryTabMeta | undefined;
  const assetId = meta?.assetId;

  const [view, setView] = useState<View>("tree");
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const invalidate = useEtcdStore((s) => s.invalidate);
  const loadPrefix = useEtcdStore((s) => s.loadPrefix);

  // ConfirmDialog 统一接管两种破坏性操作：query bar 上的 put/del/txn 命令、key detail 上的删除按钮。
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null);
  const resolveRef = useRef<((ok: boolean) => void) | null>(null);

  const requestConfirm = useCallback((req: ConfirmRequest): Promise<boolean> => {
    return new Promise<boolean>((resolve) => {
      // 上一个还挂着（理论上不会，UI 是单实例）—— 防御性放掉
      resolveRef.current?.(false);
      resolveRef.current = resolve;
      setConfirmRequest(req);
      setConfirmOpen(true);
    });
  }, []);

  function settleConfirm(ok: boolean) {
    setConfirmOpen(false);
    const fn = resolveRef.current;
    resolveRef.current = null;
    fn?.(ok);
  }

  const requestDestructiveCommand = useCallback(
    (command: string) => requestConfirm({ action: "execCommand", payload: command }),
    [requestConfirm]
  );

  const requestDeleteKey = useCallback(
    (key: string) => requestConfirm({ action: "deleteKey", payload: key }),
    [requestConfirm]
  );

  const requestSaveKey = useCallback(
    (key: string) => requestConfirm({ action: "saveKey", payload: key }),
    [requestConfirm]
  );

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

  // 三种 action 对应三套文案：
  //   execCommand → query bar 提交的破坏性命令（put/del/txn），payload 是完整命令串
  //   deleteKey   → key detail 上的删除按钮，payload 是单个 key
  //   saveKey     → key detail 上的保存按钮，payload 是单个 key
  // 不要把 execCommand 折叠进 deleteKey 分支：payload 含 flag/value，套 "将删除 key {key}" 会错位。
  let dialogTitle = "";
  let dialogBody = "";
  switch (confirmRequest?.action) {
    case "saveKey":
      dialogTitle = t("etcd.detail.saveConfirmTitle");
      dialogBody = t("etcd.detail.saveConfirmBody", { key: confirmRequest.payload });
      break;
    case "execCommand":
      dialogTitle = t("etcd.query.execConfirmTitle");
      dialogBody = t("etcd.query.execConfirmBody", { command: confirmRequest.payload });
      break;
    case "deleteKey":
      dialogTitle = t("etcd.query.deleteConfirmTitle");
      dialogBody = t("etcd.query.deleteConfirmBody", { key: confirmRequest.payload });
      break;
  }

  return (
    <div className="flex h-full w-full flex-col" data-testid="etcd-panel">
      <EtcdClusterBar assetId={assetId} />

      <div className="flex min-h-0 flex-1">
        {/* Left: KV tree */}
        <div ref={sidebarRef} className="shrink-0 border-r" style={{ width: sidebarWidth }}>
          <EtcdTreePane assetId={assetId} onSelectKey={setSelectedKey} selectedKey={selectedKey} />
        </div>

        {/* Resize handle */}
        <div
          className="w-1 shrink-0 cursor-col-resize hover:bg-accent active:bg-accent"
          onMouseDown={handleMouseDown}
        />

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
              <EtcdKeyDetail
                assetId={assetId}
                selectedKey={selectedKey}
                onRequestDelete={requestDeleteKey}
                onRequestSave={requestSaveKey}
                onDeleted={(key) => {
                  if (selectedKey === key) setSelectedKey(null);
                  invalidate(assetId);
                  // 删除后强制重载 root，避免树空白
                  void loadPrefix(assetId, "/", { force: true });
                }}
                /* onSaved 不刷新树：编辑保存只是改 value，结构不变；
                   detail 已经在 handleSave 里自己重新 get 一次刷新 modRev/version 等元数据。 */
              />
            </div>

            {/* Query view */}
            <div className="absolute inset-0 flex flex-col" style={{ display: view === "query" ? "flex" : "none" }}>
              <EtcdQueryBar assetId={assetId} onDestructive={requestDestructiveCommand} />
              <div className="min-h-0 flex-1 overflow-hidden">
                <EtcdResultTable />
              </div>
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
        title={dialogTitle}
        description={dialogBody}
        cancelText={t("action.cancel")}
        confirmText={t("action.confirm")}
        onConfirm={() => settleConfirm(true)}
      />
    </div>
  );
}
