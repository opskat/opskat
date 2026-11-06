import { Suspense, type ReactNode } from "react";
import { Loader2 } from "lucide-react";

/**
 * 资产类型槽位（表单配置区块 / 详情卡）的 Suspense 边界：内置类型的这两个组件按需加载，
 * 加载期间只在槽位里转圈，表单其余字段与详情页通用信息照常可用。
 */
export function AssetTypeSectionSuspense({ children }: { children: ReactNode }) {
  return (
    <Suspense
      fallback={
        <div data-testid="asset-type-section-loading" className="flex h-24 items-center justify-center">
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        </div>
      }
    >
      {children}
    </Suspense>
  );
}
