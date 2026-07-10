import type React from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";
import { useResizeHandle, ConfirmDialog, Button } from "@opskat/ui";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { useOssBrowserStore } from "@/stores/ossBrowserStore";
import { useOssTransferStore } from "@/stores/ossTransferStore";
import { flattenPrefixTree } from "@/lib/ossPrefixTree";
import { formatBytes } from "@/lib/formatBytes";
import { notifySuccess } from "@/lib/notify";
import { OSSBucketTree } from "@/components/oss/OSSBucketTree";
import { OSSBreadcrumb } from "@/components/oss/OSSBreadcrumb";
import { OSSObjectList } from "@/components/oss/OSSObjectList";
import { OSSObjectGrid } from "@/components/oss/OSSObjectGrid";
import { OSSObjectDetail } from "@/components/oss/OSSObjectDetail";
import { OSSPresignDialog } from "@/components/oss/OSSPresignDialog";
import { OSSTransferDock } from "@/components/oss/OSSTransferDock";
import { useOssFileDrop } from "@/components/oss/useOssFileDrop";

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
  const setViewMode = useOssBrowserStore((s) => s.setViewMode);
  const focusObject = useOssBrowserStore((s) => s.focusObject);
  const ensureThumbnail = useOssBrowserStore((s) => s.ensureThumbnail);
  const deleteObject = useOssBrowserStore((s) => s.deleteObject);
  const createFolder = useOssBrowserStore((s) => s.createFolder);
  const copyObject = useOssBrowserStore((s) => s.copyObject);
  const moveObject = useOssBrowserStore((s) => s.moveObject);

  const transferTab = useOssTransferStore((s) => s.tabs[tabId]);
  const startUpload = useOssTransferStore((s) => s.startUpload);
  const startDownload = useOssTransferStore((s) => s.startDownload);
  const cancelTransfer = useOssTransferStore((s) => s.cancel);
  const clearTransfer = useOssTransferStore((s) => s.clear);
  const clearCompleted = useOssTransferStore((s) => s.clearCompleted);
  const transfers = useMemo(() => Object.values(transferTab?.transfers ?? {}), [transferTab]);

  const [confirmOpen, setConfirmOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [detailDeleteOpen, setDetailDeleteOpen] = useState(false);

  const fail = useCallback((e: unknown) => toast.error(`${t("oss.browser.loadFailed")}: ${String(e)}`), [t]);

  useEffect(() => {
    if (assetId) void loadBuckets(tabId, assetId).catch(fail);
  }, [assetId, tabId, loadBuckets, fail]);

  const rows = useMemo(() => (state ? flattenPrefixTree(state.tree, state.expanded, "") : []), [state]);

  const focusedObject = useMemo(() => state?.listing?.objects.find((o) => o.key === state.focusedKey) ?? null, [state]);

  const sidebarRef = useRef<HTMLDivElement>(null);
  const { size: sidebarWidth, handleMouseDown } = useResizeHandle({
    defaultSize: 260,
    minSize: 180,
    maxSize: 480,
    targetRef: sidebarRef,
  });
  const detailRef = useRef<HTMLDivElement>(null);
  const { size: detailWidth, handleMouseDown: handleDetailResize } = useResizeHandle({
    defaultSize: 320,
    minSize: 260,
    maxSize: 520,
    reverse: true,
    targetRef: detailRef,
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

  const onDetailDownload = useCallback(() => {
    if (!assetId || !state?.currentBucket || !state.focusedKey) return;
    void startDownload(tabId, assetId, state.currentBucket, state.focusedKey).catch(() =>
      toast.error(t("oss.transfer.downloadFailed"))
    );
  }, [assetId, state, startDownload, tabId, t]);

  const promptDestination = useCallback(
    async (mode: "copy" | "move" | "rename") => {
      if (!state?.focusedKey) return;
      const source = state.focusedKey;
      const defaultKey = mode === "rename" ? `${state.currentPrefix}${source.split("/").pop() ?? ""}` : source;
      const destination = window.prompt(t(`oss.detail.${mode}Prompt`), defaultKey)?.trim();
      if (!destination || destination === source) return;
      try {
        if (mode === "copy") await copyObject(tabId, source, destination);
        else await moveObject(tabId, source, destination);
        notifySuccess(t(`oss.detail.${mode}Success`));
      } catch (error) {
        toast.error(String(error));
      }
    },
    [copyObject, moveObject, state, tabId, t]
  );

  const confirmDetailDelete = async () => {
    setDetailDeleteOpen(false);
    if (!state?.focusedKey) return;
    try {
      await deleteObject(tabId, state.focusedKey);
      notifySuccess(t("oss.browser.deleteSuccess"));
    } catch (e) {
      toast.error(`${t("oss.browser.deleteFailed")}: ${String(e)}`);
    }
  };

  const contentRef = useRef<HTMLDivElement>(null);
  const isDragOver = useOssFileDrop({
    dropRef: contentRef,
    tabId,
    assetId: assetId ?? 0,
    bucket: state?.currentBucket ?? "",
    prefix: state?.currentPrefix ?? "",
    active: !!assetId && !!state?.currentBucket,
  });

  const onUpload = useCallback(() => {
    if (!assetId || !state?.currentBucket) return;
    void startUpload(tabId, assetId, state.currentBucket, state.currentPrefix).catch(() =>
      toast.error(t("oss.transfer.uploadFailed"))
    );
  }, [assetId, state, startUpload, tabId, t]);

  const onDownload = useCallback(
    (key: string) => {
      if (!assetId || !state?.currentBucket) return;
      void startDownload(tabId, assetId, state.currentBucket, key).catch(() =>
        toast.error(t("oss.transfer.downloadFailed"))
      );
    },
    [assetId, state, startDownload, tabId, t]
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
        <div
          ref={contentRef}
          className="relative flex min-w-0 flex-1 flex-col"
          style={{ "--wails-drop-target": state?.currentBucket ? "drop" : undefined } as React.CSSProperties}
        >
          {state?.currentBucket ? (
            <>
              <OSSBreadcrumb
                bucket={state.currentBucket}
                prefix={state.currentPrefix}
                onNavigate={onNavigate}
                onRefresh={() => void refresh(tabId).catch(fail)}
                onUpload={onUpload}
                onNewFolder={() => {
                  const name = window.prompt(t("oss.browser.newFolderPrompt"))?.trim();
                  if (name) {
                    void createFolder(tabId, name)
                      .then(() => notifySuccess(t("oss.browser.newFolderSuccess")))
                      .catch(fail);
                  }
                }}
                viewMode={state.viewMode}
                onViewModeChange={(m) => setViewMode(tabId, m)}
              />
              {selectionCount > 0 && state.viewMode === "list" && (
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
              {state.viewMode === "grid" ? (
                <OSSObjectGrid
                  prefixes={state.listing?.prefixes ?? []}
                  objects={state.listing?.objects ?? []}
                  focusedKey={state.focusedKey}
                  loading={state.loading.listing}
                  loadingPage={state.loading.page}
                  truncated={state.listing?.truncated ?? false}
                  thumbnails={state.thumbnails}
                  onNavigatePrefix={onNavigate}
                  onFocusObject={(key) => focusObject(tabId, key)}
                  onEnsureThumbnail={(key) => void ensureThumbnail(tabId, key)}
                  onScrollNearBottom={() => void loadNextPage(tabId).catch(fail)}
                />
              ) : (
                <OSSObjectList
                  prefixes={state.listing?.prefixes ?? []}
                  objects={state.listing?.objects ?? []}
                  selection={state.selection}
                  loading={state.loading.listing}
                  loadingPage={state.loading.page}
                  truncated={state.listing?.truncated ?? false}
                  focusedKey={state.focusedKey}
                  onNavigatePrefix={onNavigate}
                  onToggleSelect={(key) => toggleSelect(tabId, key)}
                  onFocusObject={(key) => focusObject(tabId, key)}
                  onScrollNearBottom={() => void loadNextPage(tabId).catch(fail)}
                  onDownload={onDownload}
                />
              )}
              {transfers.length > 0 && (
                <OSSTransferDock
                  transfers={transfers}
                  onCancel={cancelTransfer}
                  onClear={(id) => clearTransfer(tabId, id)}
                  onClearCompleted={() => clearCompleted(tabId)}
                />
              )}
              {state.listing && (
                <div
                  className="flex items-center justify-between border-t px-3 py-1 text-[11px] text-muted-foreground"
                  data-testid="oss-list-footer"
                >
                  <span>
                    {t("oss.browser.footFolders", { count: state.listing.prefixes.length })} ·{" "}
                    {t("oss.browser.footObjects", { count: state.listing.objects.length })} ·{" "}
                    {formatBytes(state.listing.objects.reduce((a, o) => a + o.size, 0))}
                    {state.listing.truncated ? " · …" : ""}
                  </span>
                  {selectionCount > 0 && <span>{t("oss.browser.selectedCount", { count: selectionCount })}</span>}
                </div>
              )}
            </>
          ) : (
            <div
              className="flex flex-1 items-center justify-center text-xs text-muted-foreground"
              data-testid="oss-no-bucket"
            >
              {t("oss.browser.selectBucket")}
            </div>
          )}
          {isDragOver && (
            <div
              className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-primary/10 text-sm text-primary"
              data-testid="oss-drop-hint"
            >
              {t("oss.transfer.dropHint")}
            </div>
          )}
        </div>

        {focusedObject && (
          <>
            <div
              className="w-1 shrink-0 cursor-col-resize hover:bg-accent active:bg-accent"
              onMouseDown={handleDetailResize}
            />
            <div ref={detailRef} className="shrink-0 border-l" style={{ width: detailWidth }}>
              <OSSObjectDetail
                object={focusedObject}
                thumbnailUrl={state?.thumbnails[focusedObject.key]}
                onEnsureThumbnail={() => void ensureThumbnail(tabId, focusedObject.key)}
                onShare={() => setShareOpen(true)}
                onDownload={onDetailDownload}
                onRename={() => void promptDestination("rename")}
                onCopy={() => void promptDestination("copy")}
                onMove={() => void promptDestination("move")}
                onDelete={() => setDetailDeleteOpen(true)}
                onClose={() => focusObject(tabId, null)}
              />
            </div>
          </>
        )}
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

      {focusedObject && (
        <OSSPresignDialog
          open={shareOpen}
          onOpenChange={setShareOpen}
          assetId={assetId}
          bucket={state?.currentBucket ?? ""}
          objectKey={focusedObject.key}
          contentType={focusedObject.contentType}
        />
      )}
      <ConfirmDialog
        open={detailDeleteOpen}
        onOpenChange={setDetailDeleteOpen}
        title={t("oss.browser.deleteConfirmTitle")}
        description={t("oss.browser.deleteConfirmOne", { key: state?.focusedKey ?? "" })}
        cancelText={t("action.cancel")}
        confirmText={t("action.confirm")}
        onConfirm={() => void confirmDetailDelete()}
      />
    </div>
  );
}
