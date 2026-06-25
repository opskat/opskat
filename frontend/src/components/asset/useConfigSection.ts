import { useEffect, useImperativeHandle, useRef, useState, type Dispatch, type Ref, type SetStateAction } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";
import type {
  AssetConfigBuildResult,
  AssetFormContext,
  AssetFormHandle,
  AssetTestConfig,
  SectionValidity,
} from "@/lib/assetTypes/formContract";

export interface UseConfigSectionOptions<S> {
  ref: Ref<AssetFormHandle>;
  editAsset?: asset_entity.Asset;
  onValidityChange: (v: SectionValidity) => void;
  /** parse(editAsset) 或 {...DEFAULTS}。 */
  init: (editAsset?: asset_entity.Asset) => S;
  /** 纯函数,仅依赖 state;每次 state 变算一遍,变化时才上报。 */
  validate: (state: S) => SectionValidity;
  build: (state: S, ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  /** 省略 = 不可测,buildTestConfig 暴露为 null。 */
  buildTest?: (state: S, ctx: AssetFormContext) => Promise<AssetTestConfig>;
  /** build/buildTest 闭包捕获的额外身份(如 cred.value),驱动 imperative handle 重建。 */
  deps?: unknown[];
}

export interface UseConfigSectionResult<S> {
  state: S;
  setState: Dispatch<SetStateAction<S>>;
  patch: (p: Partial<S>) => void;
}

function sameValidity(a: SectionValidity | null, b: SectionValidity): boolean {
  return (
    a !== null &&
    a.canTest === b.canTest &&
    a.canSave === b.canSave &&
    (a.saveDisabledReason ?? "") === (b.saveDisabledReason ?? "")
  );
}

/** 收编各 ConfigSection 雷同的 state/patch/校验上报/imperative handle 样板。
 *  凭据留在 section 外(K8s 不用、Kafka 有伴随凭据),由调用方经 deps 喂入。 */
export function useConfigSection<S>(opts: UseConfigSectionOptions<S>): UseConfigSectionResult<S> {
  const { ref, editAsset, onValidityChange, init, validate, build, buildTest, deps = [] } = opts;

  const [state, setState] = useState<S>(() => init(editAsset));
  const patch = (p: Partial<S>) => setState((s) => ({ ...s, ...p }));

  // 校验上报:浅比较守卫,仅在结果变化时推给壳,避免每次 keystroke 触发多余渲染。
  const lastValidity = useRef<SectionValidity | null>(null);
  useEffect(() => {
    const v = validate(state);
    if (!sameValidity(lastValidity.current, v)) {
      lastValidity.current = v;
      onValidityChange(v);
    }
    // validate/onValidityChange 身份稳定假设(纯函数 + 壳 setState);校验仅依赖 state。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: (ctx: AssetFormContext) => build(state, ctx),
      buildTestConfig: buildTest ? (ctx: AssetFormContext) => buildTest(state, ctx) : null,
    }),
    // build/buildTest 捕获的额外身份由调用方经 deps 提供(如 cred.value)。
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [state, ...deps]
  );

  return { state, setState, patch };
}
