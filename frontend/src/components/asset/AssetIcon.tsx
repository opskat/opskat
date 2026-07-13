import { createElement, type ComponentType, type CSSProperties } from "react";
import { Server } from "lucide-react";
import { getIconComponent, getIconColor } from "@/components/asset/IconPicker";
import { getAssetType } from "@/lib/assetTypes";
import type { asset_entity } from "../../../wailsjs/go/models";

export type AssetIconComponent = ComponentType<{
  className?: string;
  style?: CSSProperties;
  "aria-hidden"?: boolean;
  "data-testid"?: string;
}>;

interface EntityIconProps {
  icon?: string;
  fallback?: AssetIconComponent;
  className?: string;
  style?: CSSProperties;
  "aria-hidden"?: boolean;
  "data-testid"?: string;
}

export function EntityIcon({
  icon,
  fallback: Fallback = Server,
  className,
  style,
  "aria-hidden": ariaHidden,
  "data-testid": testId,
}: EntityIconProps) {
  const Icon = (icon ? getIconComponent(icon) : Fallback) as AssetIconComponent;
  const color = icon ? getIconColor(icon) : undefined;

  return createElement(Icon, {
    className,
    style: color ? { ...style, color } : style,
    "aria-hidden": ariaHidden,
    "data-testid": testId,
  });
}

export interface AssetIconProps {
  assets: asset_entity.Asset[];
  assetId: number | null | undefined;
  fallbackType?: string;
  className?: string;
  style?: CSSProperties;
  "aria-hidden"?: boolean;
  "data-testid"?: string;
}

export function AssetIcon({
  assets,
  assetId,
  fallbackType,
  className,
  style,
  "aria-hidden": ariaHidden,
  "data-testid": testId,
}: AssetIconProps) {
  const asset = assetId != null ? assets.find((a) => a.ID === assetId) : undefined;
  const typeKey = fallbackType ?? asset?.Type;
  const typeIcon = typeKey ? getAssetType(typeKey)?.icon : undefined;

  return (
    <EntityIcon
      icon={asset?.Icon || undefined}
      fallback={(typeIcon ?? Server) as AssetIconComponent}
      className={className}
      style={style}
      aria-hidden={ariaHidden}
      data-testid={testId}
    />
  );
}
