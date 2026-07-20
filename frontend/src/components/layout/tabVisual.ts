import type { ComponentType, CSSProperties } from "react";
import { Folder, MessageSquare, Server } from "lucide-react";
import { getIconColor, getIconComponent } from "@/components/asset/IconPicker";
import type { InfoTabMeta, Tab } from "@/stores/tabStore";
import { getBuiltinPageMeta } from "./pageTabMeta";

export interface TabVisual {
  Icon: ComponentType<{ className?: string; style?: CSSProperties }>;
  iconStyle?: CSSProperties;
  indicatorColor?: string;
}

export function resolveTabVisual(tab: Tab): TabVisual {
  if (tab.type === "ai") return { Icon: MessageSquare };

  const pageMeta = getBuiltinPageMeta(tab);
  if (pageMeta) return { Icon: pageMeta.icon };

  const fallback = tab.type === "info" && (tab.meta as InfoTabMeta).targetType === "group" ? Folder : Server;
  const Icon = tab.icon ? getIconComponent(tab.icon) : fallback;
  const color = tab.icon ? getIconColor(tab.icon) : undefined;
  return {
    Icon,
    iconStyle: color ? { color } : undefined,
    indicatorColor: color,
  };
}
