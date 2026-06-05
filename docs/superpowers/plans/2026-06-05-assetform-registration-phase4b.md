# AssetForm 组件注册化 — 阶段 4b(迁移 serial:首个可测类型 + 通用测试编排)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`).

**Goal:** 把 `serial` 迁移到注册化通用路径,并补齐 4a 刻意推迟的**通用测试编排**:`buildTestConfig` 接入共享 `TestAssetConnection` + testId 竞态/取消/toast,`validity.canTest` 接到测试按钮,section 上报 `saveDisabledReason` 以保留"缺串口"提示。

**Architecture:** 沿用 4a 的 ref 契约与 `.config.ts` sibling 模式。`serial` 是**首个可测**类型:其 section 同时暴露 `buildConfig` 与 `buildTestConfig`(serial 的测试 config 与保存 config 同形,复用 `buildSerialConfig`)。`onValidityChange` 负载扩为 `SectionValidity { canTest, canSave, saveDisabledReason? }`(local 仍只报 canTest/canSave,saveDisabledReason 可选不变)。壳新增 `handleGenericTestConnection`(镜像现有 `handleTest*` 的竞态/取消/toast),并把 `isTestableAssetType`/`isTestConnectionDisabled`/`handleRunTestConnection`/`saveDisabledReason` 在 `sectionDef?.ConfigSection` 时切到通用/反应式分支。删 serial 全部遗留。其余 7 类型仍走遗留 switch。

**Tech Stack:** React 19(forwardRef/useImperativeHandle),Vitest + @testing-library/react。

**Spec:** `docs/superpowers/specs/2026-06-05-assetform-registration-phase4-design.md`(§2 测试路径、§5);承 4a 完成记录的"4b 必做项"。Issue #130,累加到 #144。

---

## 现状锚点(AssetForm.tsx / SerialConfigSection.tsx,实现前必读)

serial 现状(行号为当前 HEAD,验证按内容定位):
- state 432–438:`serialPortPath`/`serialBaudRate`(115200)/`serialDataBits`(8)/`serialStopBits`("1")/`serialParity`("none")/`serialFlowControl`("none")。
- `loadSerialConfig`(893–905):`port_path||""`、`baud_rate||115200`、`data_bits||8`、`stop_bits||"1"`、`parity||"none"`、`flow_control||"none"`;失败→`resetSerialFields`。`resetSerialFields`(907–913)。
- 编辑分发 504(`loadSerialConfig`)、创建态 reset 531(`resetSerialFields()`)。
- 保存分支 1599–1608:`{port_path, baud_rate, data_bits, stop_bits, parity}` + `if (flowControl!=="none") flow_control`。
- 测试 `handleTestSerialConnection`(1211–1234):构建**同形** cfg → `TestAssetConnection(testId, "serial", JSON.stringify(cfg), "")`,竞态模式(`newTestId`→`activeTestIdRef.current=testId`→`setTesting(true)`→try 成功 `notifySuccess`/catch `toast.error`/finally 清 ref+`setTesting(false)`,均 guard `activeTestIdRef.current === testId`)。
- 测试编排:`cancelActiveTest`(1197–1203)、`handleCancelTest`(1205–1209,共享,不动);`isTestableAssetType`(1658–1665,含 serial)、`isTestConnectionDisabled`(1673–1687,serial→`!serialPortPath`)、`handleRunTestConnection`(1712–1725,serial→`handleTestSerialConnection`)、`testConnectionButton`(1727–1746,共享壳,不动)。
- `saveDisabledReason`(1689–1709):serial→`!serialPortPath.trim()`→`"asset.formMissingSerialPort"`。4a 已插入 `: sectionDef?.ConfigSection ? "" :` 短路(本计划增强为带 reason)。`saveDisabled`(1710):`saving || !!saveDisabledReason || (!!sectionDef?.ConfigSection && !validity.canSave)`。
- 渲染块 2092–2107(`SerialConfigSection` 12 个受控 props)。
- `SerialConfigSection.tsx`:现为受控组件(`SerialConfigSectionProps` 12 props),内部已有 `ports`/`loadingPorts`/`customMode` 本地 state + `fetchPorts`/端口下拉/手动模式逻辑(**这些保留**,只把 `portPath` 等 6 个受控 props 改为自持 `state` + `patch`)。
- 4a 已落地:`formContract.ts`、`AssetFormHandle`、`ConfigSectionProps`、`getAssetType`、`sectionRef`/`validity`/`ctx`/`persistAsset`、通用保存路径、`LocalConfigSection` + `.config.ts`。

**不变量**:serial 迁移后 `buildConfig`/`buildTestConfig` 产出的 config JSON 与旧 `handleSubmit`/`handleTestSerialConnection` 字节一致;编辑回填与旧 `loadSerialConfig` 一致;测试连接的竞态/取消/toast 语义不变;"缺串口"保存提示保留。**唯一刻意微调**:旧测试禁用用 `!serialPortPath`(不 trim),保存提示用 `.trim()`;迁移后统一 `!!portPath.trim()`(canTest=canSave),仅影响"纯空白端口"这一退化输入(原本测试可点、现禁用,与保存一致),可接受。

---

## File Structure

- **Create** `frontend/src/components/asset/SerialConfigSection.config.ts` — `SerialFormState`/`SERIAL_DEFAULTS`/`buildSerialConfig`/`parseSerialConfig`(纯函数,sibling 模式)。
- **Modify** `frontend/src/lib/assetTypes/formContract.ts` — `onValidityChange` 负载提取为 `SectionValidity { canTest, canSave, saveDisabledReason? }`。
- **Modify** `frontend/src/components/asset/SerialConfigSection.tsx` — 重写为 forwardRef 自持 state,暴露 `buildConfig`+`buildTestConfig`,上报 `SectionValidity`。
- **Modify** `frontend/src/lib/assetTypes/serial.ts` — 注册 `ConfigSection` + `testable: true`。
- **Modify** `frontend/src/components/asset/AssetForm.tsx` — 加 `handleGenericTestConnection` + 通用测试/校验分支;`validity` 用 `SectionValidity`;删 serial 遗留。
- **Create (test)** `frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx` — golden + ref 契约(含 buildTestConfig + validity reason)。

---

## Task 1: serial 配置纯函数 + golden(RED→GREEN)

**Files:**
- Create: `frontend/src/components/asset/SerialConfigSection.config.ts`
- Create: `frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx`

- [ ] **Step 1: 写失败测试**

创建 `frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import {
  buildSerialConfig,
  parseSerialConfig,
  SERIAL_DEFAULTS,
} from "@/components/asset/SerialConfigSection.config";

describe("buildSerialConfig (锁旧 handleSubmit/handleTestSerial 字节一致)", () => {
  it("flow_control=none 时省略该键", () => {
    expect(
      buildSerialConfig({ portPath: "/dev/ttyUSB0", baudRate: 115200, dataBits: 8, stopBits: "1", parity: "none", flowControl: "none" })
    ).toBe('{"port_path":"/dev/ttyUSB0","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none"}');
  });
  it("flow_control=hardware 时追加该键(末位)", () => {
    expect(
      buildSerialConfig({ portPath: "/dev/ttyS0", baudRate: 9600, dataBits: 7, stopBits: "2", parity: "even", flowControl: "hardware" })
    ).toBe('{"port_path":"/dev/ttyS0","baud_rate":9600,"data_bits":7,"stop_bits":"2","parity":"even","flow_control":"hardware"}');
  });
});

describe("parseSerialConfig (锁旧 loadSerialConfig)", () => {
  it("回填全字段", () => {
    expect(
      parseSerialConfig('{"port_path":"/dev/ttyS0","baud_rate":9600,"data_bits":7,"stop_bits":"2","parity":"even","flow_control":"hardware"}')
    ).toEqual({ portPath: "/dev/ttyS0", baudRate: 9600, dataBits: 7, stopBits: "2", parity: "even", flowControl: "hardware" });
  });
  it("缺字段用默认", () => {
    expect(parseSerialConfig("{}")).toEqual(SERIAL_DEFAULTS);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseSerialConfig("nope")).toEqual(SERIAL_DEFAULTS);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/SerialConfigSection.test.tsx`
Expected: FAIL — 模块/导出不存在。

- [ ] **Step 3: 实现纯函数**

创建 `frontend/src/components/asset/SerialConfigSection.config.ts`:

```ts
export interface SerialFormState {
  portPath: string;
  baudRate: number;
  dataBits: number;
  stopBits: string;
  parity: string;
  flowControl: string;
}

export const SERIAL_DEFAULTS: SerialFormState = {
  portPath: "",
  baudRate: 115200,
  dataBits: 8,
  stopBits: "1",
  parity: "none",
  flowControl: "none",
};

/** 保存序列化:镜像旧 handleSubmit serial 分支(键序 port_path→baud_rate→data_bits→stop_bits→parity→[flow_control])。 */
export function buildSerialConfig(state: SerialFormState): string {
  const cfg: Record<string, unknown> = {
    port_path: state.portPath,
    baud_rate: state.baudRate,
    data_bits: state.dataBits,
    stop_bits: state.stopBits,
    parity: state.parity,
  };
  if (state.flowControl !== "none") cfg.flow_control = state.flowControl;
  return JSON.stringify(cfg);
}

/** 编辑态回填:镜像旧 loadSerialConfig。解析失败→默认值。 */
export function parseSerialConfig(configJSON: string): SerialFormState {
  try {
    const cfg = JSON.parse(configJSON || "{}");
    return {
      portPath: cfg.port_path || "",
      baudRate: cfg.baud_rate || 115200,
      dataBits: cfg.data_bits || 8,
      stopBits: cfg.stop_bits || "1",
      parity: cfg.parity || "none",
      flowControl: cfg.flow_control || "none",
    };
  } catch {
    return { ...SERIAL_DEFAULTS };
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/SerialConfigSection.test.tsx`
Expected: PASS(5 用例)。

- [ ] **Step 5: tsc**

Run: `cd frontend && npx tsc --noEmit` → 0 error。

- [ ] **Step 6: Commit**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add frontend/src/components/asset/SerialConfigSection.config.ts frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx
git commit -m "✅ 抽 serial 配置纯函数 buildSerialConfig/parseSerialConfig + golden #130"
```

---

## Task 2: 迁移 serial + 通用测试编排 + 扩 validity 契约(删遗留)

组件 prop 接口变更会打破壳的遗留渲染(2092–2107)与遗留测试链,故一并落地、一次提交。

**Files:**
- Modify: `frontend/src/lib/assetTypes/formContract.ts`
- Modify: `frontend/src/components/asset/SerialConfigSection.tsx`
- Modify: `frontend/src/lib/assetTypes/serial.ts`
- Modify: `frontend/src/components/asset/AssetForm.tsx`
- Modify: `frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx`

- [ ] **Step 1: 扩 `onValidityChange` 契约**

在 `frontend/src/lib/assetTypes/formContract.ts`:把内联的校验负载提取为命名接口,并改 `ConfigSectionProps.onValidityChange`:

```ts
export interface SectionValidity {
  canTest: boolean;
  canSave: boolean;
  /** 保存禁用原因的 i18n key;空/缺省 = 可保存(壳据此显示提示)。 */
  saveDisabledReason?: string;
}
```
把 `ConfigSectionProps` 内
```ts
  onValidityChange: (v: { canTest: boolean; canSave: boolean }) => void;
```
改为
```ts
  onValidityChange: (v: SectionValidity) => void;
```
(local 的 `onValidityChange({ canTest: false, canSave: true })` 调用仍合法——`saveDisabledReason` 可选;4a local 测试 `toHaveBeenCalledWith({ canTest: false, canSave: true })` 仍通过。)

- [ ] **Step 2: 写失败测试(serial ref 契约 + buildTestConfig + validity reason)**

在 `frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx` 顶部补 import:

```tsx
import { render } from "@testing-library/react";
import { createRef } from "react";
import { vi } from "vitest";
import { SerialConfigSection } from "@/components/asset/SerialConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/serial/Serial", () => ({ ListSerialPorts: () => Promise.resolve([]) }));

const fakeCtx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => p };
```

加 describe:

```tsx
describe("SerialConfigSection ref 契约", () => {
  it("编辑态:buildConfig 与 buildTestConfig 同形,password 为空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "serial",
      Config: '{"port_path":"/dev/ttyUSB0","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SerialConfigSection ref={ref} editAsset={editAsset} ctx={fakeCtx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(fakeCtx);
    expect(built).toEqual({
      configJSON: '{"port_path":"/dev/ttyUSB0","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none"}',
      sshTunnelId: 0,
    });
    const tc = await ref.current!.buildTestConfig!(fakeCtx);
    expect(tc).toEqual({ assetType: "serial", configJSON: built.configJSON, password: "" });
  });

  it("创建态(无端口):上报 canSave/canTest=false + formMissingSerialPort", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<SerialConfigSection ref={ref} ctx={fakeCtx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingSerialPort",
    });
  });

  it("编辑态(有端口):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({ Type: "serial", Config: '{"port_path":"/dev/ttyS0"}' });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<SerialConfigSection ref={ref} editAsset={editAsset} ctx={fakeCtx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
```

Run: `cd frontend && npx vitest run src/components/asset/__tests__/SerialConfigSection.test.tsx` → 新 3 个 FAIL(组件未接受 ref/editAsset/ctx/onValidityChange)。

- [ ] **Step 3: 重写 SerialConfigSection 组件为 forwardRef 自持 state**

在 `frontend/src/components/asset/SerialConfigSection.tsx`:
- React import 改:`import { forwardRef, useCallback, useEffect, useImperativeHandle, useState } from "react";`
- 加:`import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";` 和 `import { buildSerialConfig, parseSerialConfig, SERIAL_DEFAULTS, type SerialFormState } from "./SerialConfigSection.config";`
- 删除旧 `export interface SerialConfigSectionProps { ... }`。
- 保留 `SerialPortInfo` 接口与 `CUSTOM_PORT`/`NO_PORTS_PLACEHOLDER`/`BAUD_RATES`/`DATA_BITS_OPTIONS`/`STOP_BITS_OPTIONS`/`PARITY_OPTIONS`/`FLOW_CONTROL_OPTIONS` 常量不变。
- 把 `export function SerialConfigSection({ portPath, setPortPath, ... }: SerialConfigSectionProps) { ... }` 整段替换为 forwardRef 版本:

```tsx
export const SerialConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(
  function SerialConfigSection({ editAsset, onValidityChange }, ref) {
    const { t } = useTranslation();
    const [ports, setPorts] = useState<SerialPortInfo[]>([]);
    const [loadingPorts, setLoadingPorts] = useState(false);
    const [customMode, setCustomMode] = useState(false);
    const [state, setState] = useState<SerialFormState>(() =>
      editAsset ? parseSerialConfig(editAsset.Config) : { ...SERIAL_DEFAULTS }
    );

    const patch = (p: Partial<SerialFormState>) => setState((s) => ({ ...s, ...p }));

    const fetchPorts = useCallback(async () => {
      setLoadingPorts(true);
      try {
        const list = await ListSerialPorts();
        setPorts(list || []);
      } catch {
        setPorts([]);
      } finally {
        setLoadingPorts(false);
      }
    }, []);

    useEffect(() => {
      fetchPorts();
    }, [fetchPorts]);

    // 已保存端口不在当前列表时自动切手动输入(单向开)。
    useEffect(() => {
      if (state.portPath && !ports.some((p) => p.name === state.portPath)) {
        setCustomMode(true);
      }
    }, [ports, state.portPath]);

    // serial 保存与测试都要 port_path;上报反应式校验 + 缺端口提示(onValidityChange 为壳 setState,身份稳定)。
    useEffect(() => {
      const ok = !!state.portPath.trim();
      onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingSerialPort" });
    }, [state.portPath, onValidityChange]);

    useImperativeHandle(
      ref,
      () => ({
        buildConfig: async () => ({ configJSON: buildSerialConfig(state), sshTunnelId: 0 }),
        buildTestConfig: async () => ({ assetType: "serial", configJSON: buildSerialConfig(state), password: "" }),
      }),
      [state]
    );

    const selectValue = customMode ? CUSTOM_PORT : state.portPath;

    const handlePortSelect = (value: string) => {
      if (value === CUSTOM_PORT) {
        setCustomMode(true);
      } else {
        setCustomMode(false);
        patch({ portPath: value });
      }
    };

    return (
      <div className="grid gap-3 border rounded-lg p-4">
        <div className="grid gap-2">
          <div className="flex items-center justify-between">
            <Label>{t("asset.serialPortPath")}</Label>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-6 px-2 text-xs"
              onClick={fetchPorts}
              disabled={loadingPorts}
            >
              <RefreshCw className={`h-3 w-3 mr-1 ${loadingPorts ? "animate-spin" : ""}`} />
              {t("asset.serialRefreshPorts")}
            </Button>
          </div>
          <Select value={selectValue} onValueChange={handlePortSelect}>
            <SelectTrigger>
              <SelectValue placeholder={t("asset.serialPortPathPlaceholder")} />
            </SelectTrigger>
            <SelectContent>
              {ports.map((p) => (
                <SelectItem key={p.name} value={p.name}>
                  {p.displayName}
                  {p.serialNumber ? ` (${p.serialNumber})` : ""}
                </SelectItem>
              ))}
              {ports.length === 0 && !loadingPorts && (
                <SelectItem value={NO_PORTS_PLACEHOLDER} disabled>
                  {t("asset.serialNoPortsDetected")}
                </SelectItem>
              )}
              <SelectItem value={CUSTOM_PORT}>{t("asset.serialManualInput")}</SelectItem>
            </SelectContent>
          </Select>
          {customMode && (
            <Input
              value={state.portPath}
              onChange={(e) => patch({ portPath: e.target.value })}
              placeholder={t("asset.serialPortPathPlaceholder")}
              className="font-mono"
            />
          )}
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="grid gap-2">
            <Label>{t("asset.serialBaudRate")}</Label>
            <Select value={String(state.baudRate)} onValueChange={(v) => patch({ baudRate: Number(v) })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {BAUD_RATES.map((rate) => (
                  <SelectItem key={rate} value={String(rate)}>
                    {rate}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-2">
            <Label>{t("asset.serialDataBits")}</Label>
            <Select value={String(state.dataBits)} onValueChange={(v) => patch({ dataBits: Number(v) })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {DATA_BITS_OPTIONS.map((bits) => (
                  <SelectItem key={bits} value={String(bits)}>
                    {bits}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="grid grid-cols-3 gap-3">
          <div className="grid gap-2">
            <Label>{t("asset.serialStopBits")}</Label>
            <Select value={state.stopBits} onValueChange={(v) => patch({ stopBits: v })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STOP_BITS_OPTIONS.map((bits) => (
                  <SelectItem key={bits} value={bits}>
                    {bits}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-2">
            <Label>{t("asset.serialParity")}</Label>
            <Select value={state.parity} onValueChange={(v) => patch({ parity: v })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PARITY_OPTIONS.map((p) => (
                  <SelectItem key={p} value={p}>
                    {p}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-2">
            <Label>{t("asset.serialFlowControl")}</Label>
            <Select value={state.flowControl} onValueChange={(v) => patch({ flowControl: v })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {FLOW_CONTROL_OPTIONS.map((fc) => (
                  <SelectItem key={fc} value={fc}>
                    {fc}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      </div>
    );
  }
);
```

Run section test → 5 纯函数 + 3 ref 契约 = 8 pass。

- [ ] **Step 4: 注册 serial(可测)**

`frontend/src/lib/assetTypes/serial.ts`:加 `import { SerialConfigSection } from "@/components/asset/SerialConfigSection";`,在 `registerAssetType({...})` 内 `DetailInfoCard` 之后加:
```ts
  ConfigSection: SerialConfigSection,
  testable: true,
```

- [ ] **Step 5: 壳 — 通用测试编排 + validity 类型**

`frontend/src/components/asset/AssetForm.tsx`:

5a. import 补 `SectionValidity`:把 4a 的 `import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";` 改为 `import type { AssetFormHandle, AssetFormContext, SectionValidity } from "@/lib/assetTypes/formContract";`

5b. `validity` 用命名类型:把 `const [validity, setValidity] = useState({ canTest: false, canSave: false });` 改为
```ts
  const [validity, setValidity] = useState<SectionValidity>({ canTest: false, canSave: false });
```

5c. 加通用测试处理器(放在 `handleTestSerialConnection` 附近、`cancelActiveTest` 之后即可;注意不要与 i18n `t` 重名,故用 `tc`):
```ts
  const handleGenericTestConnection = async () => {
    const build = sectionRef.current?.buildTestConfig;
    if (!build) return;
    let tc;
    try {
      tc = await build(ctx);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return;
    }
    const testId = newTestId();
    activeTestIdRef.current = testId;
    setTesting(true);
    try {
      await TestAssetConnection(testId, tc.assetType, tc.configJSON, tc.password);
      if (activeTestIdRef.current === testId) notifySuccess(t("asset.testConnectionSuccess"));
    } catch (e) {
      if (activeTestIdRef.current === testId) toast.error(`${t("asset.testConnectionFailed")}: ${String(e)}`);
    } finally {
      if (activeTestIdRef.current === testId) {
        activeTestIdRef.current = null;
        setTesting(false);
      }
    }
  };
```

5d. `isTestableAssetType`(1658–1665):改为 generic 优先 + 从遗留列表删 serial:
```ts
  const isTestableAssetType = sectionDef?.ConfigSection
    ? !!sectionDef.testable
    : assetType === "ssh" ||
      assetType === "database" ||
      assetType === "redis" ||
      assetType === "mongodb" ||
      assetType === "kafka" ||
      assetType === "etcd";
```

5e. `isTestConnectionDisabled`(1673–1687):generic 优先(`!validity.canTest`)+ 从遗留链删 serial 分支:
```ts
  const isTestConnectionDisabled =
    testing ||
    (sectionDef?.ConfigSection
      ? !validity.canTest
      : assetType === "kafka"
        ? kafkaBrokers().length === 0
        : assetType === "database" && driver === "sqlite"
          ? !path
          : assetType === "etcd"
            ? etcdEndpointsList().length === 0
            : assetType !== "mongodb"
              ? !host
              : mongoConnectionMode === "uri"
                ? !connectionURI
                : !host);
```

5f. `saveDisabledReason`(1689–):把 4a 的 `: sectionDef?.ConfigSection ? "" :` 增强为带 reason,并从遗留链删 serial 分支:
```ts
  const saveDisabledReason = !name.trim()
    ? "asset.formMissingName"
    : sectionDef?.ConfigSection
      ? validity.saveDisabledReason ?? ""
      : assetType === "database" && driver === "sqlite" && !path.trim()
        ? "asset.formMissingPath"
        : ["ssh", "redis"].includes(assetType) && !host.trim()
          ? "asset.formMissingHost"
          : assetType === "database" && driver !== "sqlite" && !host.trim()
            ? "asset.formMissingHost"
            : assetType === "mongodb" && mongoConnectionMode === "manual" && !host.trim()
              ? "asset.formMissingHost"
              : assetType === "mongodb" && mongoConnectionMode === "uri" && !connectionURI.trim()
                ? "asset.formMissingMongoUri"
                : assetType === "kafka" && kafkaBrokers().length === 0
                  ? "asset.formMissingKafkaBrokers"
                  : assetType === "k8s" && !kubeconfig.trim() && !editAsset
                    ? "asset.formMissingKubeconfig"
                    : assetType === "etcd" && etcdEndpointsList().length === 0
                      ? "etcd.error.endpointsRequired"
                      : "";
```
(即删除原 `: assetType === "serial" && !serialPortPath.trim() ? "asset.formMissingSerialPort"` 一支;`saveDisabled`(1710)不变,已含 `(!!sectionDef?.ConfigSection && !validity.canSave)`。)

5g. `handleRunTestConnection`(1712–1725):generic 优先 + 删 serial 分支:
```ts
  const handleRunTestConnection = sectionDef?.ConfigSection
    ? handleGenericTestConnection
    : assetType === "ssh"
      ? handleTestConnection
      : assetType === "database"
        ? handleTestDatabaseConnection
        : assetType === "mongodb"
          ? handleTestMongoDBConnection
          : assetType === "kafka"
            ? handleTestKafkaConnection
            : assetType === "etcd"
              ? handleTestEtcdConnection
              : handleTestRedisConnection;
```

- [ ] **Step 6: 壳 — 删 serial 遗留**

删除(按内容定位):
- serial state 432–438(6 个 `useState`)。
- `loadSerialConfig`(893–905)+ `resetSerialFields`(907–913)。
- 编辑分发 `} else if (editType === "serial") { loadSerialConfig(editAsset); }`(504 一支)—— 已被 4a 的 `getAssetType(editType)?.ConfigSection` 守卫覆盖,删该分支。
- 创建态 reset `resetSerialFields();`(531)。
- `handleTestSerialConnection`(1211–1234)。
- 保存分支 `} else if (assetType === "serial") { ... }`(1599–1608)。
- 渲染块 `{assetType === "serial" && (<SerialConfigSection portPath=.../>)}`(2092–2107)。
- 顶部 `import { SerialConfigSection } ...`(44):AssetForm 不再直接引用(经 registry),若 eslint 报 unused 则删。

- [ ] **Step 7: 验证**

- `cd frontend && npx tsc --noEmit` → 0(无残留 `serialPortPath`/`loadSerialConfig`/`handleTestSerialConnection`/`SerialConfigSectionProps`)。
- `npx vitest run src/components/asset/__tests__/SerialConfigSection.test.tsx` → 8 pass。
- `npx vitest run src/components/asset/__tests__/LocalConfigSection.test.tsx` → 10 pass(4a 未回归;local 的 onValidityChange 断言仍绿)。
- `npx vitest run` → 全绿。
- `npx eslint src/components/asset/AssetForm.tsx src/components/asset/SerialConfigSection.tsx src/components/asset/SerialConfigSection.config.ts src/lib/assetTypes` → 0(含 react-refresh:Serial 组件文件只导出组件)。
- `grep -nE 'assetType === "serial"|handleTestSerialConnection|serialPortPath' src/components/asset/AssetForm.tsx` → 仅可能在被删后无残留(报告留下什么)。

- [ ] **Step 8: Commit**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add frontend/src/lib/assetTypes/formContract.ts frontend/src/components/asset/SerialConfigSection.tsx frontend/src/lib/assetTypes/serial.ts frontend/src/components/asset/AssetForm.tsx frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx
git commit -m "♻️ 迁移 serial 注册化 + 通用测试编排(buildTestConfig + validity.canTest 接入) #130"
```

---

## Task 3: 观测验证(端到端)

- [ ] 跑 app,新增 `serial` 资产:无端口时"测试连接"按钮禁用 + 显示"缺串口"提示、保存禁用;填端口后两者可用;点测试连接确认成功/失败提示与取消按钮正常(竞态/取消语义不变)。
- [ ] 编辑已存 serial 资产:回填 port/baud/data/stop/parity/flow 正确;改 flow_control=hardware 保存,`opskat.db` config 出现 `flow_control` 键且 none 时省略。
- [ ] local(回归):确认 local 仍正常(不可测、保存=名称即可),未受通用测试编排影响。

---

## Self-Review

- **Spec 覆盖**:§2 测试路径(`buildTestConfig` + 共享 TestAssetConnection + 竞态/取消/toast)→ Task 2 Step 5c+5g;`validity.canTest` 接按钮 → 5d/5e;serial 迁移 → Task 1+2;`saveDisabledReason` 保留(契约扩展)→ Step 1 + 5f。
- **关闭备忘**:4a 完成记录的"首个可测类型迁移前接 `validity.canTest`"已在本计划落地;"每迁移一类同步收缩遗留链"已对 serial 执行(isTestable/isTestConnectionDisabled/handleRunTestConnection/saveDisabledReason 四链删 serial)。
- **Placeholder/类型一致**:`SectionValidity`、`SerialFormState`、`buildSerialConfig`/`parseSerialConfig`/`SERIAL_DEFAULTS`、`handleGenericTestConnection`、`sectionDef`/`validity`/`sectionRef`/`ctx` 全程一致;`AssetTestConfig{assetType,configJSON,password}` 与 5c 调用一致。
- **行为保持**:serial 保存/测试 config 经 golden 锁定;测试竞态 = 旧 `handleTestSerialConnection` body;"缺串口"提示经 `saveDisabledReason` 保留;唯一微调 = 纯空白端口现禁用测试(与保存一致),已注。
- **未覆盖(4c+)**:`etcd → redis → mongodb → database → k8s → kafka → ssh` 余 7 类型;扩展负向排除列表与 saveDisabledReason 遗留链随迁移继续缩;末 commit 删遗留 switch + 共享 host/port/username state。
