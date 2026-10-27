import { useMemo } from "react";
import { useAssetStore } from "@/stores/assetStore";
import { parseDatabaseConfig } from "@/components/asset/DatabaseConfigSection.config";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import { designerAccess, type DesignerAccess } from "./model";

/** 数据库资产是否开启了只读连接(资产配置的 read_only)。 */
export function useAssetReadOnly(assetId: number | undefined): boolean {
  const config = useAssetStore((s) => s.assets.find((a) => a.ID === assetId)?.Config);
  return useMemo(() => parseDatabaseConfig(config ?? "").readOnly, [config]);
}

/** 这个资产上的设计器能不能改表结构,不能时为什么。 */
export function useDesignerAccess(
  descriptor: DriverDescriptor | undefined,
  assetId: number | undefined
): DesignerAccess {
  const assetReadOnly = useAssetReadOnly(assetId);
  return useMemo(() => designerAccess(descriptor, assetReadOnly), [descriptor, assetReadOnly]);
}
