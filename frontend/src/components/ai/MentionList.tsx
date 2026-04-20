import { forwardRef, useImperativeHandle, useMemo, useState, useEffect } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { Server, Database, HardDrive, Leaf } from "lucide-react";
import { useAssetStore } from "@/stores/assetStore";
import type { asset_entity, group_entity } from "../../../wailsjs/go/models";

export interface MentionItem {
  id: number;
  label: string;
  type: string;
  groupPath: string;
}

export interface MentionListProps {
  query: string;
  command: (item: MentionItem) => void;
}

export interface MentionListRef {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean;
}

const MAX_ITEMS = 8;

function iconForType(type: string) {
  switch (type) {
    case "mysql":
    case "postgresql":
    case "mongo":
      return <Database className="h-3.5 w-3.5 text-muted-foreground" />;
    case "redis":
      return <HardDrive className="h-3.5 w-3.5 text-muted-foreground" />;
    case "ssh":
      return <Server className="h-3.5 w-3.5 text-muted-foreground" />;
    default:
      return <Leaf className="h-3.5 w-3.5 text-muted-foreground" />;
  }
}

function buildGroupPathMap(groups: group_entity.Group[]): Map<number, string> {
  const byId = new Map<number, group_entity.Group>();
  for (const g of groups) byId.set(g.ID, g);
  const cache = new Map<number, string>();
  const resolve = (id: number): string => {
    if (cache.has(id)) return cache.get(id)!;
    const g = byId.get(id);
    if (!g) return "";
    const parent = g.ParentID ? resolve(g.ParentID) : "";
    const full = parent ? `${parent}/${g.Name}` : g.Name;
    cache.set(id, full);
    return full;
  };
  const map = new Map<number, string>();
  for (const g of groups) map.set(g.ID, resolve(g.ID));
  return map;
}

function rank(a: asset_entity.Asset, groupPath: string, q: string): number {
  const name = a.Name.toLowerCase();
  const path = groupPath.toLowerCase();
  const query = q.toLowerCase();
  if (!query) return 2;
  if (name.startsWith(query)) return 0;
  if (name.includes(query)) return 1;
  if (path.includes(query)) return 2;
  return 3;
}

export const MentionList = forwardRef<MentionListRef, MentionListProps>(function MentionList({ query, command }, ref) {
  const { t } = useTranslation();
  const { assets, groups } = useAssetStore();
  const [selectedIndex, setSelectedIndex] = useState(0);

  const items: MentionItem[] = useMemo(() => {
    if (assets.length === 0) return [];
    const groupPathMap = buildGroupPathMap(groups);
    const q = query.trim().toLowerCase();
    const scored = assets
      .map((a) => {
        const gp = a.GroupID ? groupPathMap.get(a.GroupID) || "" : "";
        return { a, gp, r: rank(a, gp, q) };
      })
      .filter(({ a, gp, r }) => {
        if (!q) return true;
        return r < 3 && (a.Name.toLowerCase().includes(q) || gp.toLowerCase().includes(q));
      })
      .sort((x, y) => {
        if (x.r !== y.r) return x.r - y.r;
        return x.a.Name.localeCompare(y.a.Name, "zh-CN");
      })
      .slice(0, MAX_ITEMS)
      .map(({ a, gp }) => ({
        id: a.ID,
        label: a.Name,
        type: a.Type,
        groupPath: gp,
      }));
    return scored;
  }, [assets, groups, query]);

  useEffect(() => setSelectedIndex(0), [items.length]);

  useImperativeHandle(ref, () => ({
    onKeyDown: ({ event }) => {
      if (event.key === "ArrowUp") {
        flushSync(() => setSelectedIndex((i) => (i + items.length - 1) % Math.max(items.length, 1)));
        return true;
      }
      if (event.key === "ArrowDown") {
        flushSync(() => setSelectedIndex((i) => (i + 1) % Math.max(items.length, 1)));
        return true;
      }
      if (event.key === "Enter") {
        const item = items[selectedIndex];
        if (item) command(item);
        return true;
      }
      return false;
    },
  }));

  if (assets.length === 0) return null;

  if (items.length === 0) {
    return (
      <div className="bg-popover text-popover-foreground rounded-md border shadow-md px-3 py-2 text-xs text-muted-foreground">
        {t("ai.mentionNotFound", "未找到资产")}
      </div>
    );
  }

  return (
    <div
      role="listbox"
      className="bg-popover text-popover-foreground rounded-md border shadow-md overflow-hidden min-w-[240px] max-w-[360px]"
    >
      {items.map((item, idx) => (
        <button
          role="option"
          aria-selected={idx === selectedIndex}
          key={item.id}
          onClick={() => command(item)}
          className={
            "flex items-center gap-2 w-full px-2.5 py-1.5 text-xs text-left " +
            (idx === selectedIndex ? "bg-accent" : "hover:bg-accent/60")
          }
        >
          {iconForType(item.type)}
          <span className="flex-1 min-w-0 truncate">
            {item.groupPath && <span className="text-muted-foreground">{item.groupPath}/</span>}
            <span className="text-foreground">{item.label}</span>
          </span>
        </button>
      ))}
    </div>
  );
});
