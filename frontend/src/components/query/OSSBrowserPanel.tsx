import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";
import { useResizeHandle, ConfirmDialog, Button } from "@opskat/ui";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { useOssBrowserStore } from "@/stores/ossBrowserStore";
import { flattenPrefixTree } from "@/lib/ossPrefixTree";
import { notifySuccess } from "@/lib/notify";
import { OSSBucketTree } from "@/components/oss/OSSBucketTree";
import { OSSBreadcrumb } from "@/components/oss/OSSBreadcrumb";
import { OSSObjectList } from "@/components/oss/OSSObjectList";

export interface OSSBrowserPanelProps {
  tabId: string;
}

export function OSSBrowserPanel({ tabId }: OSSBrowserPanelProps) {
  const { t } = useTranslation();
  const tab = useTabStore((s) => s.tabs.find((tt) => tt.id === tabId));
  const meta = tab?.meta as QueryTabMeta | undefined;
  const assetId = meta?.assetId;

  const state = useOssBrowserStore((s) => s.tabs[tabId]);
  const loadBuckets = useOssBrowserStore((s) => s.loadBuckets);
  const selectBucket = useOssBrowserStore((s) => s.selectBucket);
  const navigateToPrefix = useOssBrowserStore((s) => s.navigateToPrefix);
  const expandNode = useOssBrowserStore((s) => s.expandNode);
  const loadNextPage = useOssBrowserStore((s) => s.loadNextPage);
  const toggleSelect = useOssBrowserStore((s) => s.toggleSelect);
  const deleteSelected = useOssBrowserStore((s) => s.deleteSelected);
  const refresh = useOssBrowserStore((s) => s.refresh);

  const [confirmOpen, setConfirmOpen] = useState(false);

  const fail = useCallback((e: unknown) => toast.error(`${t("oss.browser.loadFailed")}: ${String(e)}`), [t]);

  useEffect(() => {
    if (assetId) void loadBuckets(tabId, assetId).catch(fail);
  }, [assetId, tabId, loadBuckets, fail]);

  const rows = useMemo(() => (state ? flattenPrefixTree(state.tree, state.expanded, "") : []), [state]);

  const sidebarRef = useRef<HTMLDivElement>(null);
  const { size: sidebarWidth, handleMouseDown } = useResizeHandle({
    defaultSize: 260,
    minSize: 180,
    maxSize: 480,
    targetRef: sidebarRef,
  });

  const onNavigate = useCallback(
    (prefix: string) => void navigateToPrefix(tabId, prefix).catch(fail),
    [navigateToPrefix, tabId, fail]
  );
  const onExpand = useCallback(
    (prefix: string) => void expandNode(tabId, prefix).catch(fail),
    [expandNode, tabId, fail]
  );
  const onSelectBucket = useCallback(
    (bucket: string) => void selectBucket(tabId, bucket).catch(fail),
    [selectBucket, tabId, fail]
  );

  const confirmDelete = async () => {
    setConfirmOpen(false);
    try {
      await deleteSelected(tabId);
      notifySuccess(t("oss.browser.deleteSuccess"));
    } catch (e) {
      toast.error(`${t("oss.browser.deleteFailed")}: ${String(e)}`);
    }
  };

  if (!assetId) {
    return <div className="p-3 text-xs text-destructive">{t("oss.browser.missingAsset")}</div>;
  }

  const selectionCount = state?.selection.size ?? 0;
  const confirmBody =
    selectionCount === 1
      ? t("oss.browser.deleteConfirmOne", { key: Array.from(state?.selection ?? [])[0] })
      : t("oss.browser.deleteConfirmMany", { count: selectionCount });

  return (
    <div className="flex h-full w-full flex-col" data-testid="oss-browser-panel">
      <div className="flex min-h-0 flex-1">
        {/* Left: bucket list + lazy prefix tree */}
        <div ref={sidebarRef} className="shrink-0 border-r" style={{ width: sidebarWidth }}>
          <OSSBucketTree
            buckets={state?.buckets ?? null}
            currentBucket={state?.currentBucket ?? ""}
            rows={rows}
            loadingBuckets={state?.loading.buckets ?? false}
            onSelectBucket={onSelectBucket}
            onToggleExpand={onExpand}
            onNavigatePrefix={onNavigate}
          />
        </div>

        {/* Resize handle */}
        <div
          className="w-1 shrink-0 cursor-col-resize hover:bg-accent active:bg-accent"
          onMouseDown={handleMouseDown}
        />

        {/* Right: breadcrumb + (selection bar) + object list */}
        <div className="flex min-w-0 flex-1 flex-col">
          {state?.currentBucket ? (
            <>
              <OSSBreadcrumb
                bucket={state.currentBucket}
                prefix={state.currentPrefix}
                onNavigate={onNavigate}
                onRefresh={() => void refresh(tabId).catch(fail)}
              />
              {selectionCount > 0 && (
                <div
                  className="flex items-center gap-2 border-b bg-muted/20 px-3 py-1 text-xs"
                  data-testid="oss-selection-bar"
                >
                  <span>{t("oss.browser.selectedCount", { count: selectionCount })}</span>
                  <Button
                    size="sm"
                    variant="destructive"
                    onClick={() => setConfirmOpen(true)}
                    data-testid="oss-delete-selected"
                  >
                    <Trash2 className="size-3" /> {t("oss.browser.deleteSelected")}
                  </Button>
                </div>
              )}
              <OSSObjectList
                prefixes={state.listing?.prefixes ?? []}
                objects={state.listing?.objects ?? []}
                selection={state.selection}
                loading={state.loading.listing}
                loadingPage={state.loading.page}
                truncated={state.listing?.truncated ?? false}
                onNavigatePrefix={onNavigate}
                onToggleSelect={(key) => toggleSelect(tabId, key)}
                onScrollNearBottom={() => void loadNextPage(tabId).catch(fail)}
              />
            </>
          ) : (
            <div
              className="flex flex-1 items-center justify-center text-xs text-muted-foreground"
              data-testid="oss-no-bucket"
            >
              {t("oss.browser.selectBucket")}
            </div>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("oss.browser.deleteConfirmTitle")}
        description={confirmBody}
        cancelText={t("action.cancel")}
        confirmText={t("action.confirm")}
        onConfirm={() => void confirmDelete()}
      />
    </div>
  );
}
