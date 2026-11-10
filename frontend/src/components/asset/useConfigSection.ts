import {
  createContext,
  use,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type Dispatch,
  type Ref,
  type SetStateAction,
} from "react";
import type { asset_entity } from "../../../wailsjs/go/models";
import type {
  AssetConfigBuildResult,
  CommonFields,
  AssetFormContext,
  AssetFormHandle,
  AssetTestConfig,
  SectionValidity,
} from "@/lib/assetTypes/formContract";

/**
 * 资产表单点「保存」被拒时的错误展示约定(壳提供,配置区块里的字段/页签消费)。
 * 校验在点击时发生:attempt 为 0 时什么都不标;每被拒一次加一,字段据此把错误画在自己下面,
 * 页签据此加标记并切到第一个出错的页签。把错误画到了字段上的组件调用 reportShown,
 * 壳据此知道这次不必再用 toast 报区块的原因。
 */
export interface AssetFormErrors {
  attempt: number;
  reportShown: () => void;
}

/** 资产表单之外(如自定义类型编辑器复用 ConfigTabs)没有保存尝试:不标任何错误。 */
export const AssetFormErrorsContext = createContext<AssetFormErrors>({ attempt: 0, reportShown: () => {} });

/** 第几次保存被拒;0 = 还没有。 */
export function useSaveAttempt(): number {
  return use(AssetFormErrorsContext).attempt;
}

/** 该处有错误且保存已被拒过时返回 true(调用方据此画错误),并向壳报告错误已显示。 */
export function useShownError(hasError: boolean): boolean {
  const { attempt, reportShown } = use(AssetFormErrorsContext);
  const shown = attempt > 0 && hasError;
  useEffect(() => {
    if (shown) reportShown();
  }, [shown, attempt, reportShown]);
  return shown;
}

/** 新建表单里换资产类型时,壳把上一个区块的通用连接字段经此交给新区块的初始 state。 */
export const AssetFormCarryContext = createContext<CommonFields>({});

const COMMON_KEYS = ["host", "port", "username"] as const;

function applyCommonFields<S>(state: S, carried: CommonFields): S {
  const next: Record<string, unknown> = { ...(state as Record<string, unknown>) };
  for (const key of COMMON_KEYS) {
    const value = carried[key];
    if (value !== undefined && typeof next[key] === typeof value) next[key] = value;
  }
  return next as S;
}

export interface UseConfigSectionOptions<S> {
  ref: Ref<AssetFormHandle> | undefined;
  editAsset?: asset_entity.Asset;
  onValidityChange: (v: SectionValidity) => void;
  /** parse(editAsset) 或 {...DEFAULTS}。也是「有改动」判断的基线。 */
  init: (editAsset?: asset_entity.Asset) => S;
  /** 用户此前填过、带进本区块的值(如编辑类型后重建的通用资产表单):只进 state 不进基线,
   *  这样带来的输入仍算作未保存的改动。 */
  seed?: (initial: S) => S;
  /** 纯函数,仅依赖 state;每次 state 变算一遍,变化时才上报。 */
  validate: (state: S) => SectionValidity;
  build: (state: S, ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  /** 省略 = 不可测,buildTestConfig 暴露为 null。 */
  buildTest?: (state: S, ctx: AssetFormContext) => Promise<AssetTestConfig>;
  /** build/buildTest 闭包捕获的额外身份(如 cred.value),驱动 imperative handle 重建。 */
  deps?: unknown[];
  /** validate 闭包捕获的、在 hook state 之外的额外身份(如 Kafka 的伴随子状态),驱动校验重算/上报。
   *  省略时校验仅依赖 state(与原行为一致)。 */
  validityDeps?: unknown[];
  /** 住在 hook state 之外、用户能改的可序列化子状态(如 cred.value、Kafka 伴随配置);关闭前的「有改动」判断把它们和 state 一起算。 */
  dirtyExtra?: unknown[];
}

export interface UseConfigSectionResult<S> {
  state: S;
  setState: Dispatch<SetStateAction<S>>;
  patch: (p: Partial<S>) => void;
  /** 非用户发起的更新(异步到达的默认值、编辑态解密回填):同时作用于 state 与「有改动」的基线,
   *  这样只打开不改的表单关闭时不会被当成有改动。 */
  settle: (update: (s: S) => S) => void;
}

function sameValidity(a: SectionValidity | null, b: SectionValidity): boolean {
  return (
    a !== null &&
    a.canTest === b.canTest &&
    a.canSave === b.canSave &&
    a.testable === b.testable &&
    (a.saveDisabledReason ?? "") === (b.saveDisabledReason ?? "")
  );
}

/** 收编各 ConfigSection 雷同的 state/patch/校验上报/imperative handle 样板。
 *  凭据留在 section 外(K8s 不用、Kafka 有伴随凭据),由调用方经 deps 喂入。 */
export function useConfigSection<S>(opts: UseConfigSectionOptions<S>): UseConfigSectionResult<S> {
  const {
    ref,
    editAsset,
    onValidityChange,
    init,
    seed,
    validate,
    build,
    buildTest,
    deps = [],
    validityDeps = [],
    dirtyExtra = [],
  } = opts;

  const carried = use(AssetFormCarryContext);
  const [initial, setInitial] = useState<S>(() => init(editAsset));
  // 换类型带过来的通用字段与 seed 都是用户之前的输入:只进 state,不进基线。
  const [state, setState] = useState<S>(() => {
    const seeded = seed ? seed(initial) : initial;
    return editAsset ? seeded : applyCommonFields(seeded, carried);
  });
  const [initialExtra] = useState(() => JSON.stringify(dirtyExtra));
  const patch = (p: Partial<S>) => setState((s) => ({ ...s, ...p }));
  const settle = useCallback((update: (s: S) => S) => {
    setState(update);
    setInitial(update);
  }, []);

  // 校验上报:浅比较守卫,仅在结果变化时推给壳,避免每次 keystroke 触发多余渲染。
  const lastValidity = useRef<SectionValidity | null>(null);
  useEffect(() => {
    const v = validate(state);
    if (!sameValidity(lastValidity.current, v)) {
      lastValidity.current = v;
      onValidityChange(v);
    }
    // validate/onValidityChange 身份稳定假设(纯函数 + 壳 setState)。校验默认仅依赖 state;
    // 若 validate 闭包捕获了 hook state 之外的身份(如伴随子状态),由调用方经 validityDeps 喂入。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state, ...validityDeps]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: (ctx: AssetFormContext) => build(state, ctx),
      buildTestConfig: buildTest ? (ctx: AssetFormContext) => buildTest(state, ctx) : null,
      commonFields: () => {
        const current = state as Record<string, unknown>;
        const defaults = init(undefined) as Record<string, unknown>;
        const out: CommonFields = {};
        for (const key of COMMON_KEYS) {
          // 本区块没有的字段:不表态,之前区块带着的值继续往下带。
          if (!(key in current)) continue;
          const value = current[key];
          // 还是本类型的默认值(含空)就是没填过或被清掉了:显式给 undefined,让壳丢掉之前带着的值——
          // 否则会盖掉新类型自己的默认端口/用户名,或把用户清掉的值又带回来。
          const unset =
            (typeof value !== "string" && typeof value !== "number") || value === "" || value === defaults[key];
          Object.assign(out, { [key]: unset ? undefined : value });
        }
        return out;
      },
      isDirty: () => JSON.stringify(state) !== JSON.stringify(initial) || JSON.stringify(dirtyExtra) !== initialExtra,
    }),
    // build/buildTest 捕获的额外身份由调用方经 deps 提供(如 cred.value)。
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [state, initial, ...deps, ...dirtyExtra]
  );

  return { state, setState, patch, settle };
}
