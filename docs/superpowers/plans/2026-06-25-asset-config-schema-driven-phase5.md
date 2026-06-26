# Asset Config Schema-Driven · Phase 5 — Serial / Local / Kafka Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the final three asset-config sections (Serial, Local, Kafka) onto the shared `useConfigSection` hook — completing the refactor so all 8 types share one wiring pattern — adding a tiny opt-in hook extension that Kafka needs for companion-state-dependent validity.

**Architecture:** Serial and Local adopt `useConfigSection` for state/patch/validity/imperative-handle but keep their device-specific field JSX verbatim (their fields don't fit the declarative kinds without growing the schema, which the spec forbids). The hook gains an optional `validityDeps?` so a section whose validity depends on state held *outside* the hook can re-report. Kafka adopts the hook + an in-component `ConfigGroupSchema[]`: the cleanly-matching fields go declarative; the `<Label>` switches, the companion-local schema_registry/connect tabs, and the `normalizedNumber` advanced numerics stay `custom`.

**Tech Stack:** React 19 + TypeScript, vitest + RTL, Wails v2 (frontend-only — no backend/Go in this phase).

## Global Constraints

- **Spec:** `docs/superpowers/specs/2026-06-25-asset-config-schema-driven-design.md` (§4, §5.2 Serial/Local/Kafka rows, §9). This phase implements the Serial/Local/Kafka rows of §5.2.
- **Frontend only.** No backend, no Go, no config-JSON shape changes, no data migration.
- **Do NOT change** the serializers (`SerialConfigSection.config.ts`, `LocalConfigSection.config.ts`, `KafkaConfigSection.config.ts`) — `build*`/`parse*`/defaults/types stay as-is. Do NOT change the credential/connection contracts (`useAssetCredential`, `credentialConfig`, `proxyConfig`, `PasswordSourceField`, `ConnectionMethodFields`, `ConfigTabs`). Consume them.
- **`useConfigSection` is this refactor's own infra** (added Phase 1) — extending it (Task 3) is in-scope. The extension MUST be backward-compatible: the 6 already-migrated sections (Redis/Etcd/Mongo/Database/SSH/K8s) pass no `validityDeps` and must behave identically.
- **Zero visual change** (hard constraint). The declarative renderer (`<Fields>`/`buildConfigGroups`) reproduces existing layout; where a field's current rendering differs from a declarative kind (e.g. Kafka switches use `@opskat/ui` `<Label>`, not the renderer's `FieldLabel`), keep it `custom` rather than change the visual. Prefer `custom` over growing the schema (§9).
- **No new i18n keys.** Reuse existing label keys.
- **Regression nets that MUST stay green unedited:** `__tests__/SerialConfigSection.test.tsx`, `__tests__/LocalConfigSection.test.tsx`, `__tests__/KafkaConfigSection.test.tsx`, and all three `*.config.test.ts`. These lock the ref contract + serializer behavior. Do not edit them.
- **Commits:** gitmoji prefix (`♻️` for the section migrations, `✨`/`♻️` for the hook extension), no PR/review number in subject.
- **Gates** (run from `frontend/`): `pnpm test <frag>`, `pnpm exec tsc --noEmit`, `pnpm lint`, full `pnpm test`.

## File Structure

- Modify `frontend/src/components/asset/SerialConfigSection.tsx` — adopt `useConfigSection`; keep ports/customMode local state + JSX verbatim. (Task 1)
- Modify `frontend/src/components/asset/LocalConfigSection.tsx` — adopt `useConfigSection`; keep shells local state + JSX verbatim. (Task 2)
- Modify `frontend/src/components/asset/useConfigSection.ts` — add optional `validityDeps?: unknown[]`. (Task 3)
- Test `frontend/src/components/asset/__tests__/useConfigSection.test.tsx` — extend with a `validityDeps` re-report test. (Task 3)
- Modify `frontend/src/components/asset/KafkaConfigSection.tsx` — rewrite the main component (imports + the `KafkaConfigSection` forwardRef function) to use `useConfigSection` + in-component `KAFKA_GROUPS`; keep all module-level helpers and the `KafkaCompanionAuthFields`/`KafkaConnectClusterEditor` sub-components byte-identical. (Task 4)

---

## Task 1: Serial → `useConfigSection`

**Files:**
- Modify: `frontend/src/components/asset/SerialConfigSection.tsx` (entire file)
- Regression test (do NOT edit): `frontend/src/components/asset/__tests__/SerialConfigSection.test.tsx`

**Interfaces:**
- Consumes: `useConfigSection` (`./useConfigSection`); `buildSerialConfig`/`parseSerialConfig`/`SERIAL_DEFAULTS`/`SerialFormState` (`./SerialConfigSection.config`).
- Produces: migrated `SerialConfigSection` with the unchanged ref contract — `buildConfig → { configJSON: buildSerialConfig(state), sshTunnelId: 0 }`, `buildTestConfig → { assetType:"serial", configJSON, password:"" }`, validity `{canTest:ok, canSave:ok, saveDisabledReason: ok?"":"asset.formMissingSerialPort"}` where `ok = !!portPath.trim()`.

- [ ] **Step 1: Confirm the regression net passes (baseline)**

Run (from `frontend/`): `pnpm test SerialConfigSection`
Expected: PASS. This is a refactor guarded by the existing test — no new test is authored; the test IS the contract.

- [ ] **Step 2: Rewrite the section**

Replace the contents of `frontend/src/components/asset/SerialConfigSection.tsx` with:

```tsx
import { forwardRef, useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { RefreshCw } from "lucide-react";
import { Field } from "@/components/asset/fields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { ListSerialPorts } from "../../../wailsjs/go/serial/Serial";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import {
  buildSerialConfig,
  parseSerialConfig,
  SERIAL_DEFAULTS,
  type SerialFormState,
} from "./SerialConfigSection.config";

interface SerialPortInfo {
  name: string;
  displayName: string;
  productId?: string;
  vendorId?: string;
  serialNumber?: string;
}

const CUSTOM_PORT = "__custom__";
const NO_PORTS_PLACEHOLDER = "__no_ports__";
const BAUD_RATES = [9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600];
const DATA_BITS_OPTIONS = [5, 6, 7, 8];
const STOP_BITS_OPTIONS = ["1", "1.5", "2"];
const PARITY_OPTIONS = ["none", "odd", "even", "mark", "space"];
// "hardware" 走 serial_svc.enableHardwareFlowControl（直接 ioctl 设 CRTSCTS / DCB），
// 因为 go.bug.st/serial v1.6.4 自身不暴露这条配置，而 nativeOpen 会强制关闭它。
const FLOW_CONTROL_OPTIONS = ["none", "hardware"];

export const SerialConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function SerialConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [ports, setPorts] = useState<SerialPortInfo[]>([]);
  const [loadingPorts, setLoadingPorts] = useState(false);
  const [customMode, setCustomMode] = useState(false);
  const { state, patch } = useConfigSection<SerialFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseSerialConfig(a.Config) : { ...SERIAL_DEFAULTS }),
    validate: (s) => {
      const ok = !!s.portPath.trim();
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingSerialPort" };
    },
    build: async (s) => ({ configJSON: buildSerialConfig(s), sshTunnelId: 0 }),
    buildTest: async (s) => ({ assetType: "serial", configJSON: buildSerialConfig(s), password: "" }),
  });

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

  // 已保存的端口在当前列表里没出现时（设备拔走、跨平台路径等），自动切到手动输入模式，
  // 让用户能看到原值。注意：这里只单向"开"不"关"——一旦进入手动模式就保留，
  // 用户主动从下拉里选了某个端口才会通过 handlePortSelect 切回非手动模式。
  // 这样刷新串口列表不会把正在编辑的内容覆盖掉。
  useEffect(() => {
    if (state.portPath && !ports.some((p) => p.name === state.portPath)) {
      setCustomMode(true);
    }
  }, [ports, state.portPath]);

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
    <div className="flex flex-col gap-4">
      <Field
        label={
          <span className="flex w-full items-center justify-between">
            {t("asset.serialPortPath")}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="-my-1 h-6 px-2 text-xs"
              onClick={fetchPorts}
              disabled={loadingPorts}
            >
              <RefreshCw className={`h-3 w-3 mr-1 ${loadingPorts ? "animate-spin" : ""}`} />
              {t("asset.serialRefreshPorts")}
            </Button>
          </span>
        }
        required
      >
        <Select value={selectValue} onValueChange={handlePortSelect}>
          <SelectTrigger className="w-full">
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
            className="mt-2 font-mono"
          />
        )}
      </Field>

      <div className="flex items-end gap-3">
        <Field label={t("asset.serialBaudRate")} className="flex-1">
          <Select value={String(state.baudRate)} onValueChange={(v) => patch({ baudRate: Number(v) })}>
            <SelectTrigger className="w-full">
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
        </Field>
        <Field label={t("asset.serialDataBits")} className="flex-1">
          <Select value={String(state.dataBits)} onValueChange={(v) => patch({ dataBits: Number(v) })}>
            <SelectTrigger className="w-full">
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
        </Field>
      </div>

      <div className="flex items-end gap-3">
        <Field label={t("asset.serialStopBits")} className="flex-1">
          <Select value={state.stopBits} onValueChange={(v) => patch({ stopBits: v })}>
            <SelectTrigger className="w-full">
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
        </Field>
        <Field label={t("asset.serialParity")} className="flex-1">
          <Select value={state.parity} onValueChange={(v) => patch({ parity: v })}>
            <SelectTrigger className="w-full">
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
        </Field>
        <Field label={t("asset.serialFlowControl")} className="flex-1">
          <Select value={state.flowControl} onValueChange={(v) => patch({ flowControl: v })}>
            <SelectTrigger className="w-full">
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
        </Field>
      </div>
    </div>
  );
});
```

> What changed vs the original: the manual `patch`, the validity `useEffect`, and the `useImperativeHandle` are gone — folded into `useConfigSection`. `ports`/`loadingPorts`/`customMode`/`fetchPorts` + the two device effects + the JSX are byte-identical. `useImperativeHandle` import dropped.

- [ ] **Step 3: Run the regression net**

Run (from `frontend/`): `pnpm test SerialConfigSection`
Expected: PASS (serializer goldens + the 3 ref-contract tests unchanged).

- [ ] **Step 4: Gates**

Run (from `frontend/`): `pnpm exec tsc --noEmit && pnpm lint`
Expected: tsc 0, lint 0. Fix any unused-import/prettier nits.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/SerialConfigSection.tsx
git commit -m "♻️ Serial 配置区改用 useConfigSection"
```

---

## Task 2: Local → `useConfigSection`

**Files:**
- Modify: `frontend/src/components/asset/LocalConfigSection.tsx` (entire file)
- Regression test (do NOT edit): `frontend/src/components/asset/__tests__/LocalConfigSection.test.tsx`

**Interfaces:**
- Consumes: `useConfigSection`; `buildLocalConfig`/`parseLocalConfig`/`LOCAL_DEFAULTS`/`LocalFormState` (`./LocalConfigSection.config`); `formatLocalShellArgs` (`@/lib/localShellArgs`).
- Produces: migrated `LocalConfigSection` with the unchanged ref contract — `buildConfig → { configJSON: buildLocalConfig(state), sshTunnelId: 0 }`, `buildTestConfig === null` (achieved by OMITTING `buildTest`), validity reported **exactly** `{ canTest: false, canSave: true }` (no `saveDisabledReason` — the test asserts `toHaveBeenCalledWith({ canTest: false, canSave: true })`).

- [ ] **Step 1: Confirm the regression net passes (baseline)**

Run (from `frontend/`): `pnpm test LocalConfigSection`
Expected: PASS.

- [ ] **Step 2: Rewrite the section**

Replace the contents of `frontend/src/components/asset/LocalConfigSection.tsx` with:

```tsx
import { forwardRef, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { ListLocalShells } from "../../../wailsjs/go/local/Local";
import type { localterm_svc } from "../../../wailsjs/go/models";
import { formatLocalShellArgs } from "@/lib/localShellArgs";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { buildLocalConfig, parseLocalConfig, LOCAL_DEFAULTS, type LocalFormState } from "./LocalConfigSection.config";

type ShellInfo = localterm_svc.ShellInfo;

export const LocalConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function LocalConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [shells, setShells] = useState<ShellInfo[]>([]);
  const { state, patch } = useConfigSection<LocalFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseLocalConfig(a.Config) : { ...LOCAL_DEFAULTS }),
    // local 无必填校验:始终可保存、不可测试。
    validate: () => ({ canTest: false, canSave: true }),
    build: async (s) => ({ configJSON: buildLocalConfig(s), sshTunnelId: 0 }),
    // buildTest 省略 → buildTestConfig 为 null。
  });

  useEffect(() => {
    ListLocalShells()
      .then((list) => setShells(list || []))
      .catch(() => setShells([]));
  }, []);

  const onSelectPreset = (val: string) => {
    if (val === "__default__") {
      patch({ shell: "", args: "" });
      return;
    }
    const s = shells[Number(val)];
    if (s) patch({ shell: s.path, args: formatLocalShellArgs(s.args || []) });
  };

  return (
    <div className="flex flex-col gap-4">
      <Field label={t("asset.localShell")}>
        <Select onValueChange={onSelectPreset}>
          <SelectTrigger className="w-full">
            <SelectValue placeholder={t("asset.localShellPreset")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__default__">{t("asset.localDefaultShell")}</SelectItem>
            {shells.map((s, i) => (
              <SelectItem key={`${s.path}-${i}`} value={String(i)}>
                {s.name}
                {s.args && s.args.length ? ` (${s.path} ${formatLocalShellArgs(s.args)})` : ` (${s.path})`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          value={state.shell}
          onChange={(e) => patch({ shell: e.target.value })}
          placeholder={t("asset.localShellPlaceholder")}
          className="font-mono"
        />
      </Field>
      <Field label={t("asset.localArgs")}>
        <Input
          value={state.args}
          onChange={(e) => patch({ args: e.target.value })}
          placeholder={t("asset.localArgsPlaceholder")}
          className="font-mono"
        />
      </Field>
      <Field label={t("asset.localCwd")}>
        <Input
          value={state.cwd}
          onChange={(e) => patch({ cwd: e.target.value })}
          placeholder={t("asset.localCwdPlaceholder")}
          className="font-mono"
        />
      </Field>
    </div>
  );
});
```

> `validate` returns exactly `{ canTest: false, canSave: true }` — adding `saveDisabledReason` would break `toHaveBeenCalledWith({ canTest: false, canSave: true })`. `useImperativeHandle`/`useTranslation`-unrelated imports dropped; `shells` state + the ListLocalShells effect + `onSelectPreset` + JSX unchanged.

- [ ] **Step 3: Run the regression net**

Run (from `frontend/`): `pnpm test LocalConfigSection`
Expected: PASS.

- [ ] **Step 4: Gates**

Run (from `frontend/`): `pnpm exec tsc --noEmit && pnpm lint`
Expected: tsc 0, lint 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/LocalConfigSection.tsx
git commit -m "♻️ Local 配置区改用 useConfigSection"
```

---

## Task 3: `useConfigSection` — opt-in `validityDeps`

**Files:**
- Modify: `frontend/src/components/asset/useConfigSection.ts`
- Test: `frontend/src/components/asset/__tests__/useConfigSection.test.tsx`

**Interfaces:**
- Produces: `UseConfigSectionOptions<S>` gains optional `validityDeps?: unknown[]`. The validity `useEffect` dep array becomes `[state, ...(validityDeps ?? [])]`. When `validityDeps` is omitted, the array is `[state]` — identical to the current behavior. Consumed by Task 4 (Kafka).

**Why:** the validity effect currently re-runs only on `[state]`. Kafka's validity also depends on section-local companion state (schemaRegistry / connect) held outside the hook; without this, toggling those would leave the Save button's enabled-state stale. The shallow-compare guard (`sameValidity`) already prevents redundant reports, so the only effect of this change for existing sections (which pass no `validityDeps`) is none.

- [ ] **Step 1: Write the failing test**

First, check the existing test file path and structure: the test lives at `frontend/src/components/asset/__tests__/useConfigSection.test.tsx`. Append this test inside it (add `useState` and `rerender` usage as needed; the snippet below is self-contained — adapt imports to those already at the top of the file, e.g. `render`, `createRef`, `vi`, `useConfigSection`, `AssetFormHandle`):

```tsx
import { useState } from "react";
// (the file already imports render, createRef, vi, useConfigSection, AssetFormHandle, describe/it/expect)

describe("useConfigSection validityDeps", () => {
  it("validityDeps 变化(state 不变)时按需重新上报校验", () => {
    const onValidity = vi.fn();

    function Harness() {
      const ref = createRef<AssetFormHandle>();
      const [blocked, setBlocked] = useState(true);
      useConfigSection<{ x: number }>({
        ref,
        onValidityChange: onValidity,
        init: () => ({ x: 0 }),
        // validate 读外部 blocked(经闭包),不改 state。
        validate: () => ({ canTest: !blocked, canSave: !blocked, saveDisabledReason: blocked ? "blocked" : "" }),
        build: async () => ({ configJSON: "{}", sshTunnelId: 0 }),
        validityDeps: [blocked],
      });
      return (
        <button data-testid="unblock" onClick={() => setBlocked(false)}>
          unblock
        </button>
      );
    }

    const { getByTestId } = render(<Harness />);
    // 挂载即上报一次(blocked)。
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: false, canSave: false, saveDisabledReason: "blocked" });

    // blocked 翻转(state 未变)→ validityDeps 变化 → 重新上报。
    getByTestId("unblock").click();
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `frontend/`): `pnpm test useConfigSection`
Expected: the new test FAILS — after clicking `unblock`, validity does NOT re-report (effect dep is `[state]` only, `state` unchanged), so the last call is still the `blocked` value.

- [ ] **Step 3: Implement `validityDeps`**

In `frontend/src/components/asset/useConfigSection.ts`:

(a) Add the field to `UseConfigSectionOptions<S>` (after `deps?`):

```ts
  /** build/buildTest 闭包捕获的额外身份(如 cred.value),驱动 imperative handle 重建。 */
  deps?: unknown[];
  /** validate 闭包捕获的、在 hook state 之外的额外身份(如 Kafka 的伴随子状态),驱动校验重算/上报。
   *  省略时校验仅依赖 state(与原行为一致)。 */
  validityDeps?: unknown[];
```

(b) Destructure it (line 44):

```ts
  const { ref, editAsset, onValidityChange, init, validate, build, buildTest, deps = [], validityDeps = [] } = opts;
```

(c) Change the validity effect dep array (line 59) from `[state]` to `[state, ...validityDeps]`, and update the comment:

```ts
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run (from `frontend/`): `pnpm test useConfigSection`
Expected: PASS (the new test + all existing `useConfigSection` tests).

- [ ] **Step 5: Verify the 6 already-migrated sections are unaffected**

Run (from `frontend/`): `pnpm test ConfigSection`
Expected: PASS — Redis/Etcd/MongoDB/Database/SSH/K8s section tests all green (they pass no `validityDeps`, so their validity effect is `[state]` exactly as before).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/asset/useConfigSection.ts frontend/src/components/asset/__tests__/useConfigSection.test.tsx
git commit -m "✨ useConfigSection 支持 validityDeps(校验依赖 hook 外子状态)"
```

---

## Task 4: Kafka → `useConfigSection` + in-component schema

**Files:**
- Modify: `frontend/src/components/asset/KafkaConfigSection.tsx` — rewrite ONLY the import block (lines 1–37) and the `KafkaConfigSection` forwardRef component (lines 236–571). **All module-level helper functions** (`defaultKafkaCompanionAuth`, `defaultKafkaSchemaRegistry`, `kafkaSchemaRegistryFromConfig`, `newKafkaConnectCluster`, `normalizedNumber`, `encryptKafkaCompanionPassword`, `applyKafkaCompanionAuth`, `applyKafkaCompanionTLS`, `validateKafkaCompanionAuth`, `validateKafkaCompanions`, `buildSchemaRegistryConfig`, `buildConnectConfig`) **and the two sub-components** (`KafkaCompanionAuthFields`, `kafkaCompanionAuthTypePatch`, `KafkaConnectClusterEditor`) and all the exported interfaces/types **stay BYTE-IDENTICAL** — do not touch them.
- Regression test (do NOT edit): `frontend/src/components/asset/__tests__/KafkaConfigSection.test.tsx`

**Interfaces:**
- Consumes: `useConfigSection` (Task 3, uses `validityDeps`); `buildConfigGroups`/`ConfigGroupSchema` (`./configFields`); the unchanged Kafka serializer helpers + the unchanged module-level helpers/sub-components in this file.
- Produces: migrated `KafkaConfigSection`, ref contract preserved exactly (`buildConfig`/`buildTestConfig`/validity as today). Companion state (`schemaRegistry`, `connectEnabled`, `connectClusters`) stays section-local (captured by `build`/`validate` closures + the `connect`/`schema_registry` custom render closures).

- [ ] **Step 1: Confirm the regression net passes (baseline)**

Run (from `frontend/`): `pnpm test KafkaConfigSection`
Expected: PASS (config goldens + the ref-contract tests). This is the contract the rewrite preserves.

- [ ] **Step 2: Replace the import block (lines 1–37)**

```tsx
import { forwardRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus, Trash2 } from "lucide-react";
import { Button, Input, Label, Switch } from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { resolveSaveProxyPassword } from "./proxyConfig";
import { credential_entity } from "../../../wailsjs/go/models";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  appendKafkaCredential,
  buildKafkaBaseConfig,
  kafkaBrokers,
  kafkaCompanionPlainSecretFromConfig,
  kafkaCompanionUsernameFromConfig,
  KAFKA_DEFAULTS,
  parseKafkaConfig,
  type KafkaConnectClusterConfig,
  type KafkaConnectConfig,
  type KafkaFormState,
  type KafkaSchemaRegistryConfig,
} from "./KafkaConfigSection.config";
```

> Dropped vs original: `useEffect`, `useImperativeHandle` (→ hook); `Select*`, `Textarea` (brokers/SASL now declarative); `ConnectionMethodFields` (→ `{kind:"tunnel"}`); `type ConfigGroup`, `AssetFormContext` (no longer referenced). Kept: `Label`/`Switch`/`Input`/`Button`/`Plus`/`Trash2`/`Segmented`/`Field`/`PasswordSourceField`/`credential_entity` — all still used by the unchanged sub-components and the new `custom` blocks.

- [ ] **Step 3: Replace the `KafkaConfigSection` component (lines 236–571)**

```tsx
export const KafkaConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function KafkaConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const cred = useAssetCredential(editAsset);

  // 伴随子状态:section 自持(各自带 encryptedPassword/credentialId/passwordSource,不走 useAssetCredential)。
  const [schemaRegistry, setSchemaRegistryState] = useState<KafkaSchemaRegistryForm>(() => {
    if (!editAsset) return defaultKafkaSchemaRegistry();
    try {
      const cfg = JSON.parse(editAsset.Config || "{}") as { schema_registry?: KafkaSchemaRegistryConfig };
      return kafkaSchemaRegistryFromConfig(cfg.schema_registry);
    } catch {
      return defaultKafkaSchemaRegistry();
    }
  });
  const setSchemaRegistry = (p: Partial<KafkaSchemaRegistryForm>) =>
    setSchemaRegistryState((current) => ({ ...current, ...p }));

  const [connectEnabled, setConnectEnabled] = useState<boolean>(() => {
    if (!editAsset) return false;
    try {
      const cfg = JSON.parse(editAsset.Config || "{}") as { connect?: KafkaConnectConfig };
      return !!cfg.connect?.enabled;
    } catch {
      return false;
    }
  });
  const [connectClusters, setConnectClusters] = useState<KafkaConnectClusterForm[]>(() => {
    if (!editAsset) return [];
    try {
      const cfg = JSON.parse(editAsset.Config || "{}") as { connect?: KafkaConnectConfig };
      return (cfg.connect?.clusters || []).map((cluster, index) => newKafkaConnectCluster(cluster, index));
    } catch {
      return [];
    }
  });

  const schemaRegistryInvalid = schemaRegistry.enabled && !schemaRegistry.url.trim();
  const connectInvalid =
    connectEnabled &&
    (() => {
      const clusters = connectClusters.filter((c) => c.name.trim() || c.url.trim());
      if (clusters.length === 0) return true;
      return clusters.some((c) => !c.name.trim() || !c.url.trim());
    })();

  const { state, patch } = useConfigSection<KafkaFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseKafkaConfig(a.Config, a.sshTunnelId || 0) : { ...KAFKA_DEFAULTS }),
    validate: (s) => {
      const brokersOk = kafkaBrokers(s.brokersText).length > 0;
      let saveDisabledReason = "";
      if (!brokersOk) {
        saveDisabledReason = "asset.formMissingKafkaBrokers";
      } else if (schemaRegistryInvalid) {
        saveDisabledReason = "asset.kafkaSchemaRegistryURLRequired";
      } else if (connectInvalid) {
        saveDisabledReason = "asset.kafkaConnectClusterInvalid";
      }
      return {
        canTest: brokersOk,
        canSave: brokersOk && !schemaRegistryInvalid && !connectInvalid,
        saveDisabledReason,
      };
    },
    validityDeps: [schemaRegistryInvalid, connectInvalid],
    build: async (s, ctx) => {
      validateKafkaCompanions(schemaRegistry, connectEnabled, connectClusters, t); // 非法 throw → handleSubmit toast
      const proxyPassword = await resolveSaveProxyPassword(s, ctx.encryptPassword);
      const cfg = buildKafkaBaseConfig(s, proxyPassword);
      if (s.saslMechanism !== "none") {
        appendKafkaCredential(cfg, await resolveSaveCredential(cred.value, ctx.encryptPassword));
      }
      const schemaRegistryConfig = await buildSchemaRegistryConfig(schemaRegistry, ctx.encryptPassword);
      if (schemaRegistry.enabled && schemaRegistryConfig) cfg.schema_registry = schemaRegistryConfig;
      const connectConfig = await buildConnectConfig(connectEnabled, connectClusters, ctx.encryptPassword);
      if (connectEnabled && connectConfig) cfg.connect = connectConfig;
      return {
        configJSON: JSON.stringify(cfg),
        sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
      };
    },
    buildTest: async (s) => {
      // 测试:proxy 密码仅明文(无加密)
      const cfg = buildKafkaBaseConfig(s, s.proxyPassword);
      if (s.saslMechanism !== "none") appendKafkaCredential(cfg, resolveTestCredential(cred.value));
      return { assetType: "kafka", configJSON: JSON.stringify(cfg), password: cred.value.password };
    },
    deps: [cred.value, schemaRegistry, connectEnabled, connectClusters, t],
  });

  const KAFKA_GROUPS: ConfigGroupSchema<KafkaFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "textarea",
          key: "brokersText",
          label: "asset.kafkaBrokers",
          required: true,
          mono: true,
          rows: 3,
          placeholder: "192.168.100.50:9092",
        },
        {
          kind: "select",
          key: "saslMechanism",
          label: "asset.kafkaSaslMechanism",
          options: [
            { value: "none", label: "asset.kafkaSaslNone" },
            { value: "plain", label: "PLAIN" },
            { value: "scram-sha-256", label: "SCRAM-SHA-256" },
            { value: "scram-sha-512", label: "SCRAM-SHA-512" },
          ],
        },
        { kind: "text", key: "username", label: "asset.username", visibleWhen: (s) => s.saslMechanism !== "none" },
        { kind: "password", visibleWhen: (s) => s.saslMechanism !== "none" },
      ],
    },
    { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
    {
      key: "tls",
      label: "asset.tabTls",
      fields: [
        {
          kind: "custom",
          render: (s, p) => (
            <div className="flex items-center justify-between">
              <Label>{t("asset.tls")}</Label>
              <Switch checked={s.tls} onCheckedChange={(v) => p({ tls: v })} />
            </div>
          ),
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.tls,
          render: (s, p) => (
            <div className="flex items-center justify-between">
              <Label>{t("asset.kafkaTlsInsecure")}</Label>
              <Switch checked={s.tlsInsecure} onCheckedChange={(v) => p({ tlsInsecure: v })} />
            </div>
          ),
        },
        {
          kind: "text",
          key: "tlsServerName",
          label: "asset.kafkaTlsServerName",
          placeholder: "kafka.example.com",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsCAFile",
          label: "asset.kafkaTlsCAFile",
          placeholder: "/path/to/ca.pem",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsCertFile",
          label: "asset.kafkaTlsCertFile",
          placeholder: "/path/to/client.crt",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsKeyFile",
          label: "asset.kafkaTlsKeyFile",
          placeholder: "/path/to/client.key",
          visibleWhen: (s) => s.tls,
        },
      ],
    },
    {
      key: "schema_registry",
      label: "asset.tabSchemaRegistry",
      render: () => (
        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between gap-3">
            <Label>{t("asset.kafkaSchemaRegistry")}</Label>
            <Switch
              data-testid="kafka-sr-enabled"
              checked={schemaRegistry.enabled}
              onCheckedChange={(enabled) => setSchemaRegistry({ enabled })}
            />
          </div>
          {schemaRegistry.enabled && (
            <>
              <Field label={t("asset.kafkaSchemaRegistryURL")} required>
                <Input
                  value={schemaRegistry.url}
                  onChange={(e) => setSchemaRegistry({ url: e.target.value })}
                  placeholder="http://schema-registry.example.com:8081"
                />
              </Field>
              <KafkaCompanionAuthFields
                value={schemaRegistry}
                onChange={setSchemaRegistry}
                managedPasswords={cred.managedPasswords}
              />
            </>
          )}
        </div>
      ),
    },
    {
      key: "connect",
      label: "asset.tabConnect",
      badge: connectEnabled ? connectClusters.length : undefined,
      render: () => (
        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between gap-3">
            <Label>{t("asset.kafkaConnect")}</Label>
            <Switch
              checked={connectEnabled}
              onCheckedChange={(enabled) => {
                setConnectEnabled(enabled);
                if (enabled && connectClusters.length === 0) {
                  setConnectClusters([newKafkaConnectCluster()]);
                }
              }}
            />
          </div>
          {connectEnabled && (
            <div className="flex flex-col gap-4">
              {connectClusters.map((cluster, index) => (
                <KafkaConnectClusterEditor
                  key={cluster.id}
                  index={index}
                  value={cluster}
                  onChange={(p) =>
                    setConnectClusters(
                      connectClusters.map((item, itemIndex) => (itemIndex === index ? { ...item, ...p } : item))
                    )
                  }
                  onRemove={() => setConnectClusters(connectClusters.filter((_, itemIndex) => itemIndex !== index))}
                  managedPasswords={cred.managedPasswords}
                />
              ))}
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 w-fit gap-1.5"
                onClick={() => setConnectClusters([...connectClusters, newKafkaConnectCluster()])}
              >
                <Plus className="h-3.5 w-3.5" />
                {t("asset.kafkaConnectAddCluster")}
              </Button>
            </div>
          )}
        </div>
      ),
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        { kind: "text", key: "clientId", label: "asset.kafkaClientId", placeholder: "opskat" },
        {
          kind: "custom",
          render: (s, p) => (
            <div className="flex items-end gap-3">
              <Field label={t("asset.kafkaRequestTimeout")} className="flex-1">
                <Input
                  className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  min={0}
                  max={300}
                  value={s.requestTimeoutSeconds}
                  onChange={(e) => p({ requestTimeoutSeconds: normalizedNumber(e.target.value, 30) })}
                />
              </Field>
              <Field label={t("asset.kafkaMessagePreviewBytes")} className="flex-1">
                <Input
                  className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  min={0}
                  value={s.messagePreviewBytes}
                  onChange={(e) => p({ messagePreviewBytes: normalizedNumber(e.target.value, 4096) })}
                />
              </Field>
              <Field label={t("asset.kafkaMessageFetchLimit")} className="flex-1">
                <Input
                  className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  min={0}
                  max={1000}
                  value={s.messageFetchLimit}
                  onChange={(e) => p({ messageFetchLimit: normalizedNumber(e.target.value, 50) })}
                />
              </Field>
            </div>
          ),
        },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(KAFKA_GROUPS, { state, patch, ctx: { cred, editAsset } })} />;
});
```

> Notes for the implementer:
> - The connection `{kind:"password"}` renders the SAME `PasswordSourceField` the old connection tab did (the renderer hardcodes `onUsernameChange → patch({username})`, reads `ctx.cred`, `editAssetId={ctx.editAsset?.ID}`) — Kafka's main credential key is `username`, so this matches exactly.
> - The TLS **switches stay `custom` with `<Label>`** (the declarative `{kind:"switch"}` renders `FieldLabel`, which would be a visual change here). Only the TLS **text** fields go declarative.
> - `schema_registry` / `connect` tabs are `custom render` closures over the section-local companion state + the unchanged sub-components. The advanced numerics stay `custom` (they use `normalizedNumber` + `max` attrs the `number` kind doesn't model).
> - `validityDeps: [schemaRegistryInvalid, connectInvalid]` makes the Save button re-evaluate when companion validity flips (Task 3 enables this). `deps` includes the companion state so `buildConfig`/`buildTestConfig` capture the latest.

- [ ] **Step 4: Run the regression net**

Run (from `frontend/`): `pnpm test KafkaConfigSection`
Expected: PASS — `KafkaConfigSection.test.tsx` (all ref-contract + companion build cases) and `KafkaConfigSection.config.test.ts` unchanged. If a build/validity test fails, fix the section (not the test): common culprits are `deps`/`validityDeps` missing a companion state, or `connectionType==="jumphost"` gating dropping the tunnel.

- [ ] **Step 5: Full gates**

Run (from `frontend/`): `pnpm exec tsc --noEmit && pnpm lint && pnpm test`
Expected: tsc 0, lint 0, full vitest suite green.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/asset/KafkaConfigSection.tsx
git commit -m "♻️ Kafka 配置区改用配置驱动渲染"
```

---

## Final gates (run once, after Task 4)

Run (from `frontend/`): `pnpm exec tsc --noEmit && pnpm lint && pnpm test`
- All 8 asset-config sections now use `useConfigSection`. Confirm no residual hand-written `useImperativeHandle` + validity-`useEffect` skeleton remains in any `*ConfigSection.tsx` (grep `useImperativeHandle` under `src/components/asset/*ConfigSection.tsx` — should return zero hits).

## Self-Review (completed during planning)

- **Spec coverage (§5.2):** Serial hook-adoption → Task 1; Local hook-adoption → Task 2; Kafka hook + declarative TLS/simple fields + custom brokers... wait, brokers is declarative textarea; custom for switches/companion/numerics → Task 4; "all types use the hook" → Tasks 1/2/4 complete the set (6→8). The hook gap that Kafka's companion-dependent validity exposes → Task 3 (`validityDeps`). ✅
- **Why Serial/Local stay verbatim (not declarative):** their selects are numeric (`baudRate`/`dataBits`) and/or need `flex-1` row sizing the `select` kind doesn't model, and their inputs are `font-mono` (the `text` kind doesn't model); per §9 prefer `custom`/verbatim over growing the schema. The hook still removes the wiring boilerplate (the agreed #1 pain). Documented.
- **Zero-visual-change risks identified & handled:** Kafka switches use `<Label>` (≠ renderer `FieldLabel`) → kept custom; Kafka advanced numerics use `normalizedNumber`+`max` → kept custom. Brokers textarea/`mono`, SASL select, username text, password, tunnel, TLS text fields, clientId text all verified to render identically to the declarative kinds.
- **Backward-compat of Task 3:** `validityDeps` defaults to `[]` → effect dep `[state]`, byte-identical to current for the 6 migrated sections; Step 5 re-runs their tests.
- **Regression compatibility (hand-traced):** Serial/Local/Kafka ref-contract tests preserved (validate/build/buildTest signatures map 1:1; Local validate returns exactly `{canTest:false,canSave:true}`; Kafka `deps`/`validityDeps` cover all companion state). The three `*.config.test.ts` untouched (serializers unchanged).
- **Type consistency:** `validityDeps?: unknown[]` defined in Task 3 and consumed in Task 4 match. Kafka's `buildConfigGroups(KAFKA_GROUPS, { state, patch, ctx: { cred, editAsset } })` matches the `buildConfigGroups` signature.
- **No placeholders:** every code step has complete code. ✅
- **Doc check:** `docs/adding-an-asset-type.md` does NOT instruct "copy a ConfigSection" (it lists architecture + add-file steps), so the spec's conditional 收尾 doc-update (§8.4) is a no-op; no doc task needed.
```
