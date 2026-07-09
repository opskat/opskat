import { createElement, type UIEvent } from "react";
import { useTranslation } from "react-i18next";
import { Checkbox } from "@opskat/ui";
import { Folder, Download } from "lucide-react";
import type { oss_svc } from "../../../wailsjs/go/models";
import { prefixLeafName } from "@/lib/ossPrefixTree";
import { formatBytes } from "@/lib/formatBytes";
import { typeIcon, typeIconColor } from "@/lib/objectContentType";

export { formatBytes } from "@/lib/formatBytes";

/** 抽出滚动到底判定，happy-dom 无布局无法驱动真实 scroll —— 单测这个纯函数，滚动绑定人工验证。 */
export function shouldLoadNextPage(
  scrollTop: number,
  clientHeight: number,
  scrollHeight: number,
  truncated: boolean,
  loadingPage: boolean,
  threshold = 48
): boolean {
  if (!truncated || loadingPage) return false;
  return scrollTop + clientHeight >= scrollHeight - threshold;
}

export interface OSSObjectListProps {
  prefixes: string[];
  objects: oss_svc.ObjectItem[];
  selection: Set<string>;
  loading: boolean;
  loadingPage: boolean;
  truncated: boolean;
  onNavigatePrefix: (prefix: string) => void;
  onToggleSelect: (key: string) => void;
  onScrollNearBottom: () => void;
  onDownload?: (key: string) => void;
  focusedKey?: string | null;
  onFocusObject?: (key: string) => void;
}

export function OSSObjectList({
  prefixes,
  objects,
  selection,
  loading,
  loadingPage,
  truncated,
  onNavigatePrefix,
  onToggleSelect,
  onScrollNearBottom,
  onDownload,
  focusedKey,
  onFocusObject,
}: OSSObjectListProps) {
  const { t } = useTranslation();

  const handleScroll = (e: UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (shouldLoadNextPage(el.scrollTop, el.clientHeight, el.scrollHeight, truncated, loadingPage)) {
      onScrollNearBottom();
    }
  };

  if (loading) {
    return (
      <div className="p-3 text-xs text-muted-foreground" data-testid="oss-list-loading">
        {t("oss.browser.loading")}
      </div>
    );
  }
  if (prefixes.length === 0 && objects.length === 0) {
    return (
      <div className="p-6 text-center text-xs text-muted-foreground" data-testid="oss-list-empty">
        {t("oss.browser.emptyDir")}
      </div>
    );
  }

  return (
    <div className="min-h-0 flex-1 overflow-auto" onScroll={handleScroll} data-testid="oss-object-list">
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-muted/30 text-left text-muted-foreground">
          <tr>
            <th className="w-6 px-2 py-1" />
            <th className="px-2 py-1">{t("oss.browser.colName")}</th>
            <th className="px-2 py-1">{t("oss.browser.colSize")}</th>
            <th className="px-2 py-1">{t("oss.browser.colStorageClass")}</th>
            <th className="px-2 py-1">{t("oss.browser.colModified")}</th>
            <th className="w-8 px-2 py-1" />
          </tr>
        </thead>
        <tbody>
          {prefixes.map((p) => (
            <tr
              key={p}
              className="cursor-pointer hover:bg-accent/50"
              onDoubleClick={() => onNavigatePrefix(p)}
              data-testid={`oss-folder-${p}`}
            >
              <td className="px-2 py-1" />
              <td className="px-2 py-1">
                <span className="flex items-center gap-1">
                  <Folder className="size-3 text-warning" />
                  {prefixLeafName(p)}
                </span>
              </td>
              <td className="px-2 py-1 text-muted-foreground">—</td>
              <td className="px-2 py-1 text-muted-foreground">—</td>
              <td className="px-2 py-1 text-muted-foreground">—</td>
              <td className="px-2 py-1" />
            </tr>
          ))}
          {objects.map((o) => (
            <tr
              key={o.key}
              className={`group cursor-pointer hover:bg-accent/50 ${o.key === focusedKey ? "bg-accent" : ""}`}
              onClick={() => onFocusObject?.(o.key)}
              data-testid={`oss-object-${o.key}`}
            >
              <td className="px-2 py-1" onClick={(e) => e.stopPropagation()}>
                <Checkbox
                  checked={selection.has(o.key)}
                  onCheckedChange={() => onToggleSelect(o.key)}
                  data-testid={`oss-select-${o.key}`}
                />
              </td>
              <td className="px-2 py-1">
                <span className="flex items-center gap-1">
                  {createElement(typeIcon(o.contentType, o.key), {
                    className: `size-3 shrink-0 ${typeIconColor(o.contentType, o.key)}`,
                  })}
                  <span className="truncate">{prefixLeafName(o.key)}</span>
                </span>
              </td>
              <td className="px-2 py-1 text-muted-foreground">{formatBytes(o.size)}</td>
              <td className="px-2 py-1">
                {o.storageClass ? (
                  <span className="inline-flex items-center rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                    {o.storageClass}
                  </span>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </td>
              <td className="px-2 py-1 text-muted-foreground">
                {o.lastModified ? new Date(o.lastModified * 1000).toLocaleString() : "—"}
              </td>
              <td className="px-2 py-1 text-right">
                {onDownload && (
                  <button
                    type="button"
                    className="opacity-0 group-hover:opacity-100"
                    onClick={(e) => {
                      e.stopPropagation();
                      onDownload(o.key);
                    }}
                    title={t("oss.transfer.download")}
                    data-testid={`oss-download-${o.key}`}
                  >
                    <Download className="size-3" />
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {loadingPage && (
        <div className="p-2 text-center text-xs text-muted-foreground" data-testid="oss-list-page-spinner">
          {t("oss.browser.loadingMore")}
        </div>
      )}
    </div>
  );
}
