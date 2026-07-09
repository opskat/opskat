import type { ComponentType, CSSProperties } from "react";
import { Server } from "lucide-react";
import { getIconComponent, getIconColor } from "@/components/asset/IconPicker";
import { getAssetType } from "@/lib/assetTypes";
import type { asset_entity } from "../../wailsjs/go/models";

export type AssetIconComponent = ComponentType<{ className?: string; style?: CSSProperties }>;

export interface ResolvedAssetIcon {
  Icon: AssetIconComponent;
  color: string | undefined;
}

/**
 * 统一解析资产图标 + 颜色,复用 canonical helper:
 * 优先用资产自身的 `Icon` 字段(getIconComponent/getIconColor);
 * 资产不在 store(如已删除)才回退到资产类型默认图标(getAssetType),无颜色。
 * 供 AI 的绑定 chip / 已打开终端列表 / 会话头像共用,避免各处重复实现。
 */
export function resolveAssetIcon(
  assets: asset_entity.Asset[],
  assetId: number | null | undefined,
  fallbackType?: string
): ResolvedAssetIcon {
  const asset = assetId != null ? assets.find((a) => a.ID === assetId) : undefined;
  if (asset?.Icon) {
    return { Icon: getIconComponent(asset.Icon) as AssetIconComponent, color: getIconColor(asset.Icon) };
  }
  const typeIcon = fallbackType ? getAssetType(fallbackType)?.icon : undefined;
  return { Icon: (typeIcon ?? Server) as AssetIconComponent, color: undefined };
}
