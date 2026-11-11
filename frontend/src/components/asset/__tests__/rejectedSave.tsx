import type { ReactElement } from "react";
import { act, render } from "@testing-library/react";
import { vi } from "vitest";
import { AssetFormErrorsContext } from "@/components/asset/useConfigSection";

/**
 * 把一个配置区块放进资产表单的错误约定里渲染:起初还没点过保存,rejectSave() 模拟壳的
 * 「保存被拒」一次。reportShown 记录区块有没有把错误画到字段上(壳据此决定是否 toast)。
 */
export function renderForSave(ui: ReactElement) {
  const reportShown = vi.fn();
  let attempt = 0;
  const wrap = () => <AssetFormErrorsContext value={{ attempt, reportShown }}>{ui}</AssetFormErrorsContext>;
  const view = render(wrap());
  const rejectSave = () => {
    attempt += 1;
    act(() => view.rerender(wrap()));
  };
  return { ...view, reportShown, rejectSave };
}
