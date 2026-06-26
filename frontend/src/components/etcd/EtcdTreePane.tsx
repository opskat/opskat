import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ChevronRight, ChevronDown, Folder, FileText } from "lucide-react";
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
  const loadPrefix = useEtcdStore((s) => s.loadPrefix);

  // treeCache / truncatedAt 按 `${assetId}:${prefix}` 索引,避免多 asset 同 prefix 互污。
  const nodesFor = (prefix: string) => treeCache.get(etcdCacheKey(assetId, prefix));
  const truncatedFor = (prefix: string) => truncatedAt.get(etcdCacheKey(assetId, prefix));

  const [expanded, setExpanded] = useState<Set<string>>(() => new Set([ROOT_PREFIX]));
  const [filter, setFilter] = useState("");

  function reportLoadError(e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    toast.error(`${t("etcd.tree.loadFailed")}: ${msg}`);
  }

  // 首次加载根 prefix —— 不需要显式展开根（根永远渲染顶层）
  useEffect(() => {
    loadPrefix(assetId, ROOT_PREFIX).catch(reportLoadError);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assetId, loadPrefix]);

  function toggleExpand(prefix: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(prefix)) {
        next.delete(prefix);
      } else {
        next.add(prefix);
        // 懒加载：首次展开拉取（store 内部对已缓存的会直接 return）
        if (!nodesFor(prefix)) {
          loadPrefix(assetId, prefix).catch(reportLoadError);
        }
      }
      return next;
    });
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
    const children = nodesFor(node.prefix) ?? [];
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
            {children.map((c) => renderNode(c, depth + 1))}
            {truncated && (
              <div
                data-testid="etcd-tree-truncated"
                className="px-2 py-1 text-[10px] italic text-muted-foreground"
                style={{ paddingLeft: padding + 16 }}
              >
                {t("etcd.tree.truncated", { count: "?", limit: LIST_LIMIT })}
              </div>
            )}
          </>
        )}
      </div>
    );
  }

  const rootNodes = nodesFor(ROOT_PREFIX) ?? [];
  const rootTruncated = truncatedFor(ROOT_PREFIX);

  return (
    <div className="flex h-full w-full flex-col">
      <div className="border-b p-2">
        <input
          type="text"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={t("etcd.tree.filterPlaceholder")}
          className="h-7 w-full rounded border bg-background px-2 text-xs outline-none focus:ring-1 focus:ring-ring"
        />
      </div>
      <div className="flex-1 overflow-auto">
        <div
          className="py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
          style={{ paddingLeft: 8 }}
        >
          {t("etcd.tree.title")}
        </div>
        {rootNodes.length === 0 && <div className="px-3 py-2 text-xs text-muted-foreground">…</div>}
        {rootNodes.map((n) => renderNode(n, 0))}
        {rootTruncated && (
          <div
            data-testid="etcd-tree-truncated"
            className="px-2 py-1 text-[10px] italic text-muted-foreground"
            style={{ paddingLeft: 16 }}
          >
            {t("etcd.tree.truncated", { count: "?", limit: LIST_LIMIT })}
          </div>
        )}
      </div>
    </div>
  );
}
