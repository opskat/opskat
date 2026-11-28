import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight, ChevronDown, Folder, FileText } from "lucide-react";
import { Input } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import { PanelSidebar, SidebarRefreshButton } from "@/components/common/PanelSidebar";
import { useEtcdStore, etcdCacheKey, type EtcdTreeNode } from "@/stores/etcdStore";

export interface EtcdTreePaneProps {
  assetId: number;
  onSelectKey?: (key: string) => void;
  selectedKey?: string | null;
}

const ROOT_PREFIX = "/";
const LIST_LIMIT = 1000;

export function EtcdTreePane({ assetId, onSelectKey, selectedKey }: EtcdTreePaneProps) {
  const { t } = useTranslation();
  const treeCache = useEtcdStore((s) => s.treeCache);
  const truncatedAt = useEtcdStore((s) => s.truncatedAt);
  const prefixErrors = useEtcdStore((s) => s.prefixErrors);
  const loadingPrefixes = useEtcdStore((s) => s.loadingPrefixes);
  const refreshing = useEtcdStore((s) => s.refreshingAssets.has(assetId));
  const loadPrefix = useEtcdStore((s) => s.loadPrefix);
  const refreshAsset = useEtcdStore((s) => s.refreshAsset);

  // treeCache / truncatedAt / prefixErrors 按 `${assetId}:${prefix}` 索引,避免多 asset 同 prefix 互污。
  const nodesFor = (prefix: string) => treeCache.get(etcdCacheKey(assetId, prefix));
  const truncatedFor = (prefix: string) => truncatedAt.get(etcdCacheKey(assetId, prefix));
  const errorFor = (prefix: string) => prefixErrors.get(etcdCacheKey(assetId, prefix)) ?? null;
  const loadingFor = (prefix: string) => loadingPrefixes.has(etcdCacheKey(assetId, prefix));

  const [expanded, setExpanded] = useState<Set<string>>(() => new Set([ROOT_PREFIX]));
  const [filter, setFilter] = useState("");

  function load(prefix: string, opts?: { force?: boolean }) {
    return loadPrefix(assetId, prefix, opts).catch(() => undefined);
  }

  // 刷新 keyspace：清缓存后重拉根 + 当前所有已展开层级，保持展开状态不塌。
  function refreshTree() {
    return refreshAsset(assetId, expanded);
  }

  // 首次加载根 prefix —— 不需要显式展开根（根永远渲染顶层）
  useEffect(() => {
    loadPrefix(assetId, ROOT_PREFIX).catch(() => undefined);
  }, [assetId, loadPrefix]);

  function toggleExpand(prefix: string) {
    const willExpand = !expanded.has(prefix);
    setExpanded((prev) => {
      const next = new Set(prev);
      if (willExpand) next.add(prefix);
      else next.delete(prefix);
      return next;
    });
    // 懒加载：首次展开拉取;上次失败的层重新展开时也重试。
    if (willExpand && !nodesFor(prefix)) void load(prefix);
  }

  const normalizedFilter = filter.trim().toLowerCase();

  function renderNode(node: EtcdTreeNode, depth: number) {
    const padding = depth * 12;

    if (node.isLeaf) {
      if (normalizedFilter && !node.name.toLowerCase().includes(normalizedFilter)) {
        return null;
      }
      const isSelected = selectedKey === node.prefix;
      return (
        <button
          key={node.prefix}
          type="button"
          className={`flex w-full items-center gap-1 px-2 py-1 text-left text-xs transition-colors hover:bg-accent ${
            isSelected
              ? "border-l-2 border-primary bg-accent font-semibold text-accent-foreground"
              : "text-muted-foreground"
          }`}
          style={{ paddingLeft: padding + 8 }}
          onClick={() => onSelectKey?.(node.prefix)}
          title={node.prefix}
        >
          <FileText className={`size-3 shrink-0 ${isSelected ? "text-primary" : "text-muted-foreground/70"}`} />
          <span className="truncate">{node.name}</span>
        </button>
      );
    }

    const isExpanded = expanded.has(node.prefix);
    const children = nodesFor(node.prefix);
    const childError = errorFor(node.prefix);
    const childLoading = loadingFor(node.prefix);
    const truncated = truncatedFor(node.prefix);

    return (
      <div key={node.prefix}>
        <button
          type="button"
          className="flex w-full items-center gap-1 px-2 py-1 text-left text-xs hover:bg-accent"
          style={{ paddingLeft: padding }}
          onClick={() => toggleExpand(node.prefix)}
        >
          {isExpanded ? (
            <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
          )}
          <Folder className="size-3 shrink-0 text-warning" />
          <span className="truncate font-medium text-foreground">{node.name}</span>
        </button>
        {isExpanded && (
          <>
            {childError !== null && children !== undefined && !childLoading && (
              <AsyncState
                status="error"
                error={childError}
                onRetry={() => void load(node.prefix, { force: true })}
                className="items-start py-1 pr-2 text-left"
              />
            )}
            <AsyncState
              status={
                childLoading && !children
                  ? "loading"
                  : childError !== null && !children
                    ? "error"
                    : children
                      ? "ready"
                      : "loading"
              }
              error={childError}
              onRetry={() => void load(node.prefix, { force: true })}
              empty={children && children.length === 0 ? t("etcd.tree.empty") : undefined}
              className="items-start py-1 pr-2 text-left"
            >
              {children?.map((c) => renderNode(c, depth + 1))}
              {truncated && (
                <div
                  data-testid="etcd-tree-truncated"
                  className="px-2 py-1 text-xs italic text-muted-foreground"
                  style={{ paddingLeft: padding + 16 }}
                >
                  {t("etcd.tree.truncated", { limit: LIST_LIMIT })}
                </div>
              )}
            </AsyncState>
          </>
        )}
      </div>
    );
  }

  const rootNodes = nodesFor(ROOT_PREFIX);
  const rootTruncated = truncatedFor(ROOT_PREFIX);
  const rootError = errorFor(ROOT_PREFIX);
  const rootLoading = loadingFor(ROOT_PREFIX);
  const rootStatus =
    rootLoading && !rootNodes
      ? "loading"
      : rootError !== null && !rootNodes
        ? "error"
        : rootNodes
          ? "ready"
          : "loading";

  return (
    <PanelSidebar
      panelType="etcd"
      title={t("etcd.tree.title")}
      actions={<SidebarRefreshButton onClick={() => void refreshTree()} loading={refreshing || rootLoading} />}
      filter={
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={t("etcd.tree.filterPlaceholder")}
          className="h-7 text-xs"
        />
      }
    >
      {/* 根列表已缓存后重连/刷新失败时保留现有树可用，原因与强制重试置顶显示（与 MongoDB / Redis 面板一致）。 */}
      {rootError !== null && rootNodes !== undefined && !rootLoading && (
        <AsyncState status="error" error={rootError} onRetry={() => void load(ROOT_PREFIX, { force: true })} />
      )}
      <AsyncState
        status={rootStatus}
        error={rootError}
        onRetry={() => void load(ROOT_PREFIX, { force: true })}
        empty={rootNodes && rootNodes.length === 0 ? t("etcd.tree.empty") : undefined}
        className="flex-1"
      >
        <div className="flex-1 overflow-auto py-1">
          {(rootNodes ?? []).map((n) => renderNode(n, 0))}
          {rootTruncated && (
            <div
              data-testid="etcd-tree-truncated"
              className="px-2 py-1 text-xs italic text-muted-foreground"
              style={{ paddingLeft: 16 }}
            >
              {t("etcd.tree.truncated", { limit: LIST_LIMIT })}
            </div>
          )}
        </div>
      </AsyncState>
    </PanelSidebar>
  );
}
