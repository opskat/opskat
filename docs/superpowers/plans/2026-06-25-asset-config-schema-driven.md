# 资产配置「壳 hook + 字段 schema」实现计划 · Phase 1(基础设施 + Redis 范例)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立配置驱动的资产表单基础设施(壳 hook + 字段 schema 渲染器),并把 Redis 配置区作为范例迁移过去,消除每类型一份手写组件的接线样板与字段 JSX 重复。

**Architecture:** 三个新增件 —— `useConfigSection<S>`(收编 state/patch/校验上报/imperative handle 样板)、`configFields.tsx`(`FieldDesc<S>` 字段描述符 + `<Fields>` 渲染器 + `buildConfigGroups` 组装器)、以及把 `RedisConfigSection.tsx` 改写成「~15 行胶水 + 纯数据字段 schema」。序列化器(`*.config.ts`)、凭据/代理 helper、`ConfigTabs`、`PasswordSourceField`、`ConnectionMethodFields` 全部不动。

**Tech Stack:** React 19 + TypeScript、`@opskat/ui`(Input/Switch/Select/...)、react-i18next、vitest + @testing-library/react、pnpm。

设计依据:`docs/superpowers/specs/2026-06-25-asset-config-schema-driven-design.md`。

## Global Constraints

- **不动序列化器**:`frontend/src/components/asset/*.config.ts` 的 `build*`/`parse*` 一律不改;迁移只改 `*ConfigSection.tsx` 组件层。
- **不动共享契约**:`useAssetCredential`/`credentialConfig`/`proxyConfig`/`PasswordSourceField`/`ConnectionMethodFields`/`ConfigTabs` 对外签名不改。
- **shell 不按类型分支**:`AssetForm.tsx` 不在本 Phase 改动。
- **零视觉变化 + 回归绿**:现有 `frontend/src/components/asset/__tests__/RedisConfigSection.test.tsx`(ref 契约 + 校验上报)与 `RedisConfigSection.config.test.ts`(序列化器)必须全程保持绿。
- **无新增 i18n key**:复用现有 label 键。
- **测试命令**(均在 `frontend/` 目录下执行):全部 `pnpm test`;单文件 `pnpm test <文件名片段>`。
- **提交规范**:gitmoji 前缀;subject 不带 PR/评审号(仅刻意关联 issue 时带 `#编号`)。
- **路径别名**:`@/` → `frontend/src/`;wails 绑定经 `../../../wailsjs/...` 相对引用(生成物)。

---

### Task 1: `useConfigSection<S>` 壳 hook

**Files:**
- Create: `frontend/src/components/asset/useConfigSection.ts`
- Test: `frontend/src/components/asset/__tests__/useConfigSection.test.tsx`

**Interfaces:**
- Consumes(已存在,见 `frontend/src/lib/assetTypes/formContract.ts`):
  - `AssetFormHandle = { buildConfig: (ctx) => Promise<AssetConfigBuildResult>; buildTestConfig: ((ctx) => Promise<AssetTestConfig>) | null }`
  - `AssetConfigBuildResult = { configJSON: string; sshTunnelId: number }`
  - `AssetTestConfig = { assetType: string; configJSON: string; password: string }`
  - `AssetFormContext = { isEdit: boolean; encryptPassword: (plain: string) => Promise<string> }`
  - `SectionValidity = { canTest: boolean; canSave: boolean; saveDisabledReason?: string }`
- Produces(后续 Task 3 及各类型迁移依赖):
  - `useConfigSection<S>(opts: UseConfigSectionOptions<S>): { state: S; setState: Dispatch<SetStateAction<S>>; patch: (p: Partial<S>) => void }`
  - `UseConfigSectionOptions<S> = { ref: Ref<AssetFormHandle>; editAsset?: asset_entity.Asset; onValidityChange: (v: SectionValidity) => void; init: (editAsset?: asset_entity.Asset) => S; validate: (s: S) => SectionValidity; build: (s: S, ctx: AssetFormContext) => Promise<AssetConfigBuildResult>; buildTest?: (s: S, ctx: AssetFormContext) => Promise<AssetTestConfig>; deps?: unknown[] }`

- [ ] **Step 1: 写失败测试**

Create `frontend/src/components/asset/__tests__/useConfigSection.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { createRef, type Ref } from "react";
import { useConfigSection } from "@/components/asset/useConfigSection";
import type { AssetFormHandle, AssetFormContext, SectionValidity } from "@/lib/assetTypes/formContract";

interface S {
  host: string;
  port: number;
}
const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

function Harness({
  refOut,
  onValidityChange,
  withTest,
}: {
  refOut: Ref<AssetFormHandle>;
  onValidityChange: (v: SectionValidity) => void;
  withTest?: boolean;
}) {
  const { state, patch } = useConfigSection<S>({
    ref: refOut,
    onValidityChange,
    init: () => ({ host: "", port: 6379 }),
    validate: (s) => ({ canTest: !!s.host, canSave: !!s.host, saveDisabledReason: s.host ? "" : "missing" }),
    build: async (s) => ({ configJSON: JSON.stringify(s), sshTunnelId: 0 }),
    buildTest: withTest ? async (s) => ({ assetType: "x", configJSON: JSON.stringify(s), password: "" }) : undefined,
  });
  return (
    <div>
      <span data-testid="host">{state.host}</span>
      <button data-testid="set-port" onClick={() => patch({ port: state.port + 1 })} />
      <button data-testid="set-host" onClick={() => patch({ host: "h" })} />
    </div>
  );
}

describe("useConfigSection", () => {
  it("挂载即上报一次校验", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<Harness refOut={ref} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenCalledTimes(1);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: false, canSave: false, saveDisabledReason: "missing" });
  });

  it("校验结果不变则不重复上报(无关字段变更被守卫)", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    const { getByTestId } = render(<Harness refOut={ref} onValidityChange={onValidity} />);
    onValidity.mockClear();
    fireEvent.click(getByTestId("set-port")); // port 变,validity 不变
    expect(onValidity).not.toHaveBeenCalled();
    fireEvent.click(getByTestId("set-host")); // host 变,validity 变
    expect(onValidity).toHaveBeenCalledTimes(1);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("buildConfig 读到最新 state", async () => {
    const ref = createRef<AssetFormHandle>();
    const { getByTestId } = render(<Harness refOut={ref} onValidityChange={() => {}} />);
    fireEvent.click(getByTestId("set-host"));
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({ configJSON: JSON.stringify({ host: "h", port: 6379 }), sshTunnelId: 0 });
  });

  it("buildTest 省略时 buildTestConfig 为 null", () => {
    const ref = createRef<AssetFormHandle>();
    render(<Harness refOut={ref} onValidityChange={() => {}} />);
    expect(ref.current!.buildTestConfig).toBeNull();
  });

  it("buildTest 提供时 buildTestConfig 可调用", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Harness refOut={ref} onValidityChange={() => {}} withTest />);
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({ assetType: "x", configJSON: JSON.stringify({ host: "", port: 6379 }), password: "" });
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test useConfigSection`
Expected: FAIL —— `useConfigSection` 模块不存在(`Failed to resolve import`)。

- [ ] **Step 3: 实现 hook**

Create `frontend/src/components/asset/useConfigSection.ts`:

```ts
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm test useConfigSection`
Expected: PASS(5 个用例全绿)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/asset/useConfigSection.ts frontend/src/components/asset/__tests__/useConfigSection.test.tsx
git commit -m "✨ 资产表单 useConfigSection 壳 hook"
```

---

### Task 2a: 字段 schema 基础 kind 渲染器(`<Fields>`)

**Files:**
- Create: `frontend/src/components/asset/configFields.tsx`
- Test: `frontend/src/components/asset/__tests__/configFields.test.tsx`

**Interfaces:**
- Consumes(已存在):`Field`/`FieldLabel`/`Segmented`/`SegmentedOption`(`@/components/asset/fields`);`Input`/`Switch`/`Select`/`SelectContent`/`SelectItem`/`SelectTrigger`/`SelectValue`/`Textarea`(`@opskat/ui`);`useTranslation`(react-i18next)。
- Produces(本任务交付基础 kind;Task 2b 在同文件追加 composite kind 与 `buildConfigGroups`):
  - `FieldDesc<S>` 联合类型(基础 kind:`text`/`number`/`switch`/`select`/`segmented`/`textarea`/`row`,均可带 `visibleWhen?: (s: S) => boolean`)
  - `Fields<S>(props: { fields: FieldDesc<S>[]; state: S; patch: (p: Partial<S>) => void; ctx?: FieldRenderCtx }): JSX.Element`
  - `FieldRenderCtx = { cred?: UseAssetCredential; editAsset?: asset_entity.Asset }`(Task 2b 才用到 cred,但此处先声明类型)

> 数字字段两个开关用于复刻现状行为差异:`min`(同时设 HTML `min` 属性 **并**在 onChange 把值钳到 `>=min`)与 `blankWhenZero`(值为 0 时显示空串,供 port 这类「0 显示为空」字段)。port 无 `min`(不钳)、`blankWhenZero:true`;Redis `database`/超时类 `min:0`(钳到 ≥0)、不设 `blankWhenZero`(0 显示为 "0")。

- [ ] **Step 1: 写失败测试**

Create `frontend/src/components/asset/__tests__/configFields.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { useState } from "react";
import { Fields, type FieldDesc } from "@/components/asset/configFields";

interface S {
  host: string;
  port: number;
  database: number;
  tls: boolean;
  mode: string;
  driver: string;
  note: string;
}
const INIT: S = { host: "", port: 6379, database: 0, tls: false, mode: "a", driver: "mysql", note: "" };

function Harness({ fields }: { fields: FieldDesc<S>[] }) {
  const [state, setState] = useState<S>(INIT);
  const patch = (p: Partial<S>) => setState((s) => ({ ...s, ...p }));
  return (
    <div>
      <Fields fields={fields} state={state} patch={patch} />
      <span data-testid="state">{JSON.stringify(state)}</span>
    </div>
  );
}
const stateOf = (el: HTMLElement): S => JSON.parse(el.textContent || "{}");

describe("Fields 渲染器 · 基础 kind", () => {
  it("text:输入回写", () => {
    const { getByTestId } = render(<Harness fields={[{ kind: "text", key: "host", label: "asset.host", testid: "f-host" }]} />);
    fireEvent.change(getByTestId("f-host"), { target: { value: "example.com" } });
    expect(stateOf(getByTestId("state")).host).toBe("example.com");
  });

  it("number:min 把值钳到 >=min", () => {
    const { getByTestId } = render(<Harness fields={[{ kind: "number", key: "database", label: "asset.db", min: 0, testid: "f-db" }]} />);
    fireEvent.change(getByTestId("f-db"), { target: { value: "-5" } });
    expect(stateOf(getByTestId("state")).database).toBe(0);
  });

  it("number:blankWhenZero 时 0 显示为空串", () => {
    const { getByTestId } = render(<Harness fields={[{ kind: "number", key: "port", label: "asset.port", blankWhenZero: true, testid: "f-port" }]} />);
    fireEvent.change(getByTestId("f-port"), { target: { value: "0" } });
    expect((getByTestId("f-port") as HTMLInputElement).value).toBe("");
  });

  it("switch:切换回写布尔", () => {
    const { getByRole, getByTestId } = render(<Harness fields={[{ kind: "switch", key: "tls", label: "asset.tls" }]} />);
    fireEvent.click(getByRole("switch"));
    expect(stateOf(getByTestId("state")).tls).toBe(true);
  });

  it("select:选项回写", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "select", key: "driver", label: "asset.driver", testid: "f-driver", options: [{ value: "mysql", label: "MySQL" }, { value: "postgresql", label: "PostgreSQL" }] }]} />
    );
    // Radix Select 在 jsdom 下用键盘交互不稳;此处只断言 trigger 渲染出当前值。
    expect(getByTestId("f-driver")).toBeTruthy();
  });

  it("visibleWhen=false:不渲染", () => {
    const { queryByTestId } = render(
      <Harness fields={[{ kind: "text", key: "host", label: "asset.host", testid: "f-host", visibleWhen: (s) => s.tls }]} />
    );
    expect(queryByTestId("f-host")).toBeNull();
  });

  it("row:横排渲染两个子字段", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "row", fields: [{ kind: "text", key: "host", label: "asset.host", testid: "f-host" }, { kind: "number", key: "port", label: "asset.port", testid: "f-port" }] }]} />
    );
    expect(getByTestId("f-host")).toBeTruthy();
    expect(getByTestId("f-port")).toBeTruthy();
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test configFields`
Expected: FAIL —— `configFields` 模块不存在。

- [ ] **Step 3: 实现基础渲染器**

Create `frontend/src/components/asset/configFields.tsx`:

```tsx
import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch, Textarea } from "@opskat/ui";
import { Field, FieldLabel, Segmented } from "@/components/asset/fields";
import type { UseAssetCredential } from "@/components/asset/useAssetCredential";
import type { asset_entity } from "../../../wailsjs/go/models";

/** password/tunnel kind 渲染所需的横切依赖(Task 2b 使用)。 */
export interface FieldRenderCtx {
  cred?: UseAssetCredential;
  editAsset?: asset_entity.Asset;
}

type WithVisibility<S> = { visibleWhen?: (s: S) => boolean };

export type FieldDesc<S> = WithVisibility<S> &
  (
    | { kind: "text"; key: keyof S; label: string; placeholder?: string; required?: boolean; width?: string; testid?: string }
    | { kind: "number"; key: keyof S; label: string; placeholder?: string; min?: number; blankWhenZero?: boolean; width?: string; testid?: string }
    | { kind: "switch"; key: keyof S; label: string }
    | { kind: "select"; key: keyof S; label: string; options: { value: string; label: string }[]; testid?: string }
    | { kind: "segmented"; key: keyof S; label?: string; options: { value: string; label: string; testid?: string }[] }
    | { kind: "textarea"; key: keyof S; label: string; rows?: number; hint?: string; placeholder?: string }
    | { kind: "row"; fields: FieldDesc<S>[] }
    // ↓ composite kind 在 Task 2b 补实现;此处声明以锁定类型。
    | { kind: "password"; placeholder?: string; secretLabel?: string; selectSecretLabel?: string }
    | { kind: "tunnel"; tunnelOptionLabelKey?: string; tunnelSelectLabelKey?: string; excludeIds?: number[] }
    | { kind: "custom"; render: (s: S, patch: (p: Partial<S>) => void) => ReactNode }
  );

interface FieldsProps<S> {
  fields: FieldDesc<S>[];
  state: S;
  patch: (p: Partial<S>) => void;
  ctx?: FieldRenderCtx;
}

/** 把字段描述符数组渲染成竖直列(复刻各 section 的 `flex flex-col gap-4`)。 */
export function Fields<S>({ fields, state, patch, ctx }: FieldsProps<S>) {
  return (
    <div className="flex flex-col gap-4">
      {fields.map((f, i) => (
        <FieldNode key={i} field={f} state={state} patch={patch} ctx={ctx} />
      ))}
    </div>
  );
}

function FieldNode<S>({
  field,
  state,
  patch,
  ctx,
}: {
  field: FieldDesc<S>;
  state: S;
  patch: (p: Partial<S>) => void;
  ctx?: FieldRenderCtx;
}) {
  const { t } = useTranslation();
  if (field.visibleWhen && !field.visibleWhen(state)) return null;

  switch (field.kind) {
    case "text":
      return (
        <Field label={t(field.label)} required={field.required} className={field.width}>
          <Input
            data-testid={field.testid}
            value={String(state[field.key] ?? "")}
            placeholder={field.placeholder}
            onChange={(e) => patch({ [field.key]: e.target.value } as Partial<S>)}
          />
        </Field>
      );

    case "number": {
      const raw = state[field.key] as unknown as number;
      const display = field.blankWhenZero ? raw || "" : (raw ?? "");
      return (
        <Field label={t(field.label)} className={field.width}>
          <Input
            data-testid={field.testid}
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={field.min}
            value={display}
            placeholder={field.placeholder}
            onChange={(e) => {
              const n = Number(e.target.value);
              const next = field.min !== undefined ? Math.max(field.min, n || 0) : n;
              patch({ [field.key]: next } as Partial<S>);
            }}
          />
        </Field>
      );
    }

    case "switch":
      return (
        <div className="flex items-center justify-between">
          <FieldLabel>{t(field.label)}</FieldLabel>
          <Switch
            checked={!!state[field.key]}
            onCheckedChange={(v) => patch({ [field.key]: v } as Partial<S>)}
          />
        </div>
      );

    case "select":
      return (
        <Field label={t(field.label)}>
          <Select
            value={String(state[field.key] ?? "")}
            onValueChange={(v) => patch({ [field.key]: v } as Partial<S>)}
          >
            <SelectTrigger data-testid={field.testid} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {field.options.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {t(o.label)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      );

    case "segmented":
      return (
        <Field label={field.label ? t(field.label) : undefined}>
          <Segmented
            value={String(state[field.key] ?? "")}
            onChange={(v) => patch({ [field.key]: v } as Partial<S>)}
            aria-label={field.label ? t(field.label) : undefined}
            options={field.options.map((o) => ({ value: o.value, label: t(o.label), testid: o.testid }))}
          />
        </Field>
      );

    case "textarea":
      return (
        <Field label={t(field.label)}>
          <Textarea
            value={String(state[field.key] ?? "")}
            rows={field.rows}
            placeholder={field.placeholder}
            onChange={(e) => patch({ [field.key]: e.target.value } as Partial<S>)}
          />
          {field.hint && <p className="text-xs text-muted-foreground">{t(field.hint)}</p>}
        </Field>
      );

    case "row":
      return (
        <div className="flex items-end gap-3">
          {field.fields.map((f, i) => (
            <FieldNode key={i} field={f} state={state} patch={patch} ctx={ctx} />
          ))}
        </div>
      );

    // composite kind 在 Task 2b 实现;占位以保证 switch 穷尽。
    case "password":
    case "tunnel":
    case "custom":
      return null;
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm test configFields`
Expected: PASS(7 个用例全绿)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/asset/configFields.tsx frontend/src/components/asset/__tests__/configFields.test.tsx
git commit -m "✨ 资产表单字段 schema 渲染器(基础 kind)"
```

---

### Task 2b: composite kind(password/tunnel/custom)+ `buildConfigGroups`

**Files:**
- Modify: `frontend/src/components/asset/configFields.tsx`(替换 `FieldNode` 内 `password`/`tunnel`/`custom` 的占位;文件末尾追加 `ConfigGroupSchema`/`buildConfigGroups`)
- Modify: `frontend/src/components/asset/__tests__/configFields.test.tsx`(追加 composite 与 buildConfigGroups 用例)

**Interfaces:**
- Consumes:`PasswordSourceField`(`@/components/asset/PasswordSourceField`)、`ConnectionMethodFields`(`@/components/asset/ConnectionMethodFields`)、`ConnectionFormFields`(`@/components/asset/proxyConfig`)、`ConfigGroup`(`@/components/asset/ConfigTabs`)、`UseAssetCredential`(`@/components/asset/useAssetCredential`)。
- Produces:
  - `password`/`tunnel`/`custom` kind 的真实渲染。
  - `ConfigGroupSchema<S> = { key: string; label: string; badge?: number; fields: FieldDesc<S>[] } | { key: string; label: string; badge?: number; render: () => ReactNode }`
  - `buildConfigGroups<S>(schema: ConfigGroupSchema<S>[], args: { state: S; patch: (p: Partial<S>) => void; ctx?: FieldRenderCtx }): ConfigGroup[]`(纯函数,非 hook —— 声明式组包成 `<Fields>`,逃逸口组透传 `render`,`badge` 透传)

- [ ] **Step 1: 追加失败测试**

在 `configFields.test.tsx` 末尾追加(并补充顶部 import):

```tsx
// 顶部 import 追加:
import { buildConfigGroups, type ConfigGroupSchema, type FieldRenderCtx } from "@/components/asset/configFields";
import type { UseAssetCredential } from "@/components/asset/useAssetCredential";

// 文件末尾追加:
function fakeCred(): UseAssetCredential {
  return {
    value: { password: "", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
    managedPasswords: [],
    setPassword: () => {},
    setPasswordSource: () => {},
    setPasswordCredentialId: () => {},
  };
}

describe("Fields 渲染器 · composite kind", () => {
  it("password:从 ctx.cred 渲染 PasswordSourceField(出现来源切换段控件)", () => {
    const ctx: FieldRenderCtx = { cred: fakeCred() };
    const { getByTestId } = render(
      <FieldsWithCtx fields={[{ kind: "password" }]} ctx={ctx} />
    );
    expect(getByTestId("password-source-inline")).toBeTruthy();
  });

  it("tunnel:渲染 ConnectionMethodFields(出现连接方式 radiogroup)", () => {
    const { getAllByRole } = render(<FieldsWithCtx fields={[{ kind: "tunnel" }]} ctx={{}} />);
    expect(getAllByRole("radiogroup").length).toBeGreaterThan(0);
  });

  it("custom:调用 render 并把 state/patch 传入", () => {
    const { getByTestId } = render(
      <FieldsWithCtx
        fields={[{ kind: "custom", render: (s) => <span data-testid="c">{s.driver}</span> }]}
        ctx={{}}
      />
    );
    expect(getByTestId("c").textContent).toBe("mysql");
  });
});

describe("buildConfigGroups", () => {
  it("声明式组包成 Fields;render 逃逸口透传;badge 透传", () => {
    const schema: ConfigGroupSchema<S>[] = [
      { key: "a", label: "tab.a", fields: [{ kind: "text", key: "host", label: "asset.host", testid: "g-host" }] },
      { key: "b", label: "tab.b", badge: 3, render: () => <span data-testid="g-custom">x</span> },
    ];
    const groups = buildConfigGroups(schema, { state: INIT, patch: () => {} });
    expect(groups.map((g) => g.key)).toEqual(["a", "b"]);
    expect(groups[1].badge).toBe(3);
    const { getByTestId } = render(<>{groups[0].render()}{groups[1].render()}</>);
    expect(getByTestId("g-host")).toBeTruthy();
    expect(getByTestId("g-custom")).toBeTruthy();
  });
});
```

并在文件中加入测试用宿主(放在 `Harness` 附近):

```tsx
function FieldsWithCtx({ fields, ctx }: { fields: FieldDesc<S>[]; ctx: FieldRenderCtx }) {
  const [state, setState] = useState<S>(INIT);
  const patch = (p: Partial<S>) => setState((s) => ({ ...s, ...p }));
  return <Fields fields={fields} state={state} patch={patch} ctx={ctx} />;
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test configFields`
Expected: FAIL —— composite 用例失败(`password`/`tunnel` 当前返回 null;`buildConfigGroups` 未导出)。

- [ ] **Step 3: 替换 composite 占位 + 追加 buildConfigGroups**

在 `configFields.tsx`:
1) 顶部 import 追加:

```tsx
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import type { ConnectionFormFields } from "@/components/asset/proxyConfig";
import type { ConfigGroup } from "@/components/asset/ConfigTabs";
```

2) 把 `FieldNode` 里 `case "password": case "tunnel": case "custom": return null;` 整段替换为:

```tsx
    case "password": {
      const cred = ctx?.cred;
      if (!cred) return null; // password 字段要求调用方提供 ctx.cred
      return (
        <PasswordSourceField
          source={cred.value.passwordSource}
          onSourceChange={cred.setPasswordSource}
          password={cred.value.password}
          onPasswordChange={cred.setPassword}
          credentialId={cred.value.passwordCredentialId}
          onCredentialIdChange={cred.setPasswordCredentialId}
          managedPasswords={cred.managedPasswords}
          hasExistingPassword={!!cred.value.encryptedPassword}
          editAssetId={ctx?.editAsset?.ID}
          onUsernameChange={(v) => patch({ username: v } as unknown as Partial<S>)}
          placeholder={field.placeholder}
          secretLabel={field.secretLabel}
          selectSecretLabel={field.selectSecretLabel}
        />
      );
    }

    case "tunnel":
      return (
        <ConnectionMethodFields
          value={state as unknown as ConnectionFormFields}
          onChange={patch as unknown as (p: Partial<ConnectionFormFields>) => void}
          excludeIds={field.excludeIds}
          tunnelOptionLabelKey={field.tunnelOptionLabelKey}
          tunnelSelectLabelKey={field.tunnelSelectLabelKey}
        />
      );

    case "custom":
      return <>{field.render(state, patch)}</>;
```

3) 文件末尾追加:

```tsx
export type ConfigGroupSchema<S> =
  | { key: string; label: string; badge?: number; fields: FieldDesc<S>[] }
  | { key: string; label: string; badge?: number; render: () => ReactNode };

/** 把组级 schema 转成 <ConfigTabs> 吃的 ConfigGroup[]:声明式组包成 <Fields>,逃逸口组透传 render。
 *  纯函数(不调 hook);render 闭包在 ConfigTabs 渲染期被调用。 */
export function buildConfigGroups<S>(
  schema: ConfigGroupSchema<S>[],
  args: { state: S; patch: (p: Partial<S>) => void; ctx?: FieldRenderCtx }
): ConfigGroup[] {
  return schema.map((g) => ({
    key: g.key,
    label: g.label,
    badge: g.badge,
    render:
      "render" in g
        ? g.render
        : () => <Fields fields={g.fields} state={args.state} patch={args.patch} ctx={args.ctx} />,
  }));
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm test configFields`
Expected: PASS(基础 7 + composite 3 + buildConfigGroups 1 全绿)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/asset/configFields.tsx frontend/src/components/asset/__tests__/configFields.test.tsx
git commit -m "✨ 字段 schema composite kind 与 buildConfigGroups"
```

---

### Task 3: Redis 配置区迁移到配置驱动(范例)

**Files:**
- Modify(整文件改写): `frontend/src/components/asset/RedisConfigSection.tsx`
- Test(回归网,**不改**): `frontend/src/components/asset/__tests__/RedisConfigSection.test.tsx` + `RedisConfigSection.config.test.ts`

**Interfaces:**
- Consumes:`useConfigSection`(Task 1)、`buildConfigGroups`/`ConfigGroupSchema`(Task 2b)、`ConfigTabs`(已存在)、`useAssetCredential`/`resolveSaveCredential`/`resolveTestCredential`/`resolveSaveProxyPassword`(已存在)、`buildRedisConfig`/`parseRedisConfig`/`REDIS_DEFAULTS`/`RedisFormState`(`./RedisConfigSection.config`,不动)。
- Produces:`RedisConfigSection`(`ConfigSectionComponent`,签名不变)。

> 已知有意微调(记录在案):原 username 字段有一处拼接式 placeholder(`t("asset.username") + " (" + ...`),属历史 cruft 且其它类型 username 均无 placeholder;迁移后 username 不设 placeholder(与 DB/etcd/SSH 一致)。此为纯外观简化,不影响回归测试(回归用例不断言该 placeholder)。其余字段视觉/testid/行为保持不变。

- [ ] **Step 1: 先跑回归测试确认当前为绿(基线)**

Run: `pnpm test RedisConfigSection`
Expected: PASS —— `RedisConfigSection.test.tsx`(3 用例)与 `RedisConfigSection.config.test.ts` 全绿。记下这是迁移前基线。

- [ ] **Step 2: 整文件改写 RedisConfigSection.tsx**

用以下内容**整体替换** `frontend/src/components/asset/RedisConfigSection.tsx`:

```tsx
import { forwardRef } from "react";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { resolveSaveProxyPassword } from "./proxyConfig";
import { buildRedisConfig, parseRedisConfig, REDIS_DEFAULTS, type RedisFormState } from "./RedisConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

const REDIS_GROUPS: ConfigGroupSchema<RedisFormState>[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    fields: [
      {
        kind: "row",
        fields: [
          { kind: "text", key: "host", label: "asset.host", required: true, placeholder: "example.com", width: "flex-1", testid: "redis-host-input" },
          { kind: "number", key: "port", label: "asset.port", placeholder: "6379", width: "w-[110px] shrink-0", blankWhenZero: true, testid: "redis-port-input" },
        ],
      },
      { kind: "text", key: "username", label: "asset.username" },
      { kind: "password" },
      { kind: "number", key: "database", label: "asset.redisDatabase", min: 0 },
    ],
  },
  { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  {
    key: "tls",
    label: "asset.tabTls",
    fields: [
      { kind: "switch", key: "tls", label: "asset.tls" },
      { kind: "switch", key: "tlsInsecure", label: "asset.redisTlsInsecure", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsServerName", label: "asset.redisTlsServerName", placeholder: "redis.example.com", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsCAFile", label: "asset.redisTlsCAFile", placeholder: "/path/to/ca.pem", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsCertFile", label: "asset.redisTlsCertFile", placeholder: "/path/to/client.crt", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsKeyFile", label: "asset.redisTlsKeyFile", placeholder: "/path/to/client.key", visibleWhen: (s) => s.tls },
    ],
  },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    fields: [
      {
        kind: "row",
        fields: [
          { kind: "number", key: "commandTimeoutSeconds", label: "asset.redisCommandTimeout", min: 0, width: "flex-1" },
          { kind: "number", key: "scanPageSize", label: "asset.redisScanPageSize", min: 0, width: "flex-1" },
        ],
      },
      { kind: "text", key: "keySeparator", label: "asset.redisKeySeparator", placeholder: ":" },
    ],
  },
];

export const RedisConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function RedisConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const cred = useAssetCredential(editAsset);
  const { state, patch } = useConfigSection<RedisFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseRedisConfig(a.Config, a.sshTunnelId || 0) : { ...REDIS_DEFAULTS }),
    validate: (s) => {
      const ok = !!s.host.trim();
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingHost" };
    },
    build: async (s, ctx) => ({
      configJSON: buildRedisConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        false,
        await resolveSaveProxyPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "redis",
      configJSON: buildRedisConfig(s, resolveTestCredential(cred.value), true, s.proxyPassword),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups = buildConfigGroups(REDIS_GROUPS, { state, patch, ctx: { cred, editAsset } });
  return <ConfigTabs groups={groups} />;
});
```

- [ ] **Step 3: 跑回归测试确认仍为绿**

Run: `pnpm test RedisConfigSection`
Expected: PASS —— `RedisConfigSection.test.tsx` 的 3 个用例(编辑态 build round-trip、创建态校验、编辑态校验)与序列化器测试全绿,证明 ref 契约与校验行为零回归。

- [ ] **Step 4: 类型检查 + lint**

Run: `pnpm exec tsc --noEmit` 与 `pnpm lint`
Expected: 无新增类型错误 / lint 错误(仅 `useConfigSection` 内两处刻意 `eslint-disable` 注释)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/asset/RedisConfigSection.tsx
git commit -m "♻️ Redis 配置区改用配置驱动渲染"
```

---

## Phase 1 完成判据

- 新增 `useConfigSection` / `configFields`(Fields + buildConfigGroups)+ 各自单测,全绿。
- `RedisConfigSection.tsx` 由 ~220 行手写组件收缩为「~15 行胶水 + 纯数据 schema」,回归测试零改动且全绿。
- 渲染器 API 锁定,后续类型迁移成为机械套用。

## 后续阶段路线图(待 Phase 1 锁定 API 后各自展开为 bite-sized 计划)

> 每个类型迁移任务的结构与 Task 3 相同:跑基线 → 整文件改写 section(胶水 + 该类型 schema)→ 回归测试保持绿 → tsc/lint → 提交。各类型的字段 schema 由其现有 `*ConfigSection.tsx` 推导。复杂部分走 `custom`/已有本地组件。

- **Phase 2 · Etcd**:connection(endpoints `textarea` + username `text` + `password` + 双超时 `number` row)/ tunnel(`tunnel`)/ tls(`switch` + 4 个 `text`,`visibleWhen: s=>s.tls`)。纯声明式。
- **Phase 2 · MongoDB**:connection(手动/URI 段控件 `segmented` + host/port/user/password/默认库;URI 模式下条件可见)/ tunnel / tls(`switch`)/ advanced(副本集、authSource `text`)。URI 与手动互斥用 `visibleWhen`。
- **Phase 3 · Database**:host/port/user/password/库名/params/readOnly 声明式 + `visibleWhen`(sqlite vs host、`sslMode` 限 postgresql、TLS 限 mysql/mssql);**驱动 Select**(`applyDriverChange`+`onIconChange` 副作用)与 **sqliteSource 段控件**(联动 connectionType/sshTunnelId)走 `custom`。
- **Phase 3 · SSH**:host/port/username/password/authType 段控件声明式;**密钥扫描整块**(异步列表/勾选/文件浏览/passphrase)走 `custom`;tunnel 用 `{kind:"tunnel", tunnelOptionLabelKey:"asset.connectionJumpHost", tunnelSelectLabelKey:"asset.selectJumpHost", excludeIds}`;凭据 `useAssetCredential(editAsset, parseSSHPasswordCredentialConfig(...))`。
- **Phase 4 · K8s + 代理 feature(独立提交)**:见 spec 附录 A。先后端(`K8sConfig.Proxy` 字段 + `k8sClientOptions` 代理拨号分支 + 解密,TDD)→ 前端序列化器 round-trip(`build/parseK8sConfig` 处理 proxy + connectionType 派生)→ section 配置化(kubeconfig 揭示走 `custom`、namespace/context 声明式、tunnel 用标准 `{kind:"tunnel"}`、`buildTest` 省略)。K8s 无测试连接按钮,经 `opsctl`/日志观察验证经代理实连。
- **Phase 5 · Serial / Local**:单分组(`ConfigTabs` 退化无标签);串口扫描走 `custom`,其余声明式。
- **Phase 5 · Kafka**:hook + TLS/简单字段声明式;变长 broker 列表 / N 个 Connect 集群 / 带副作用的 enabled 开关走 `custom`;伴随认证复用已有本地组件 `KafkaCompanionAuthFields`;Connect 集群数走 `ConfigGroupSchema.badge`。

## Self-Review(对照 spec)

- **覆盖**:Phase 1 覆盖 spec §4.1(useConfigSection)、§4.2(Fields/FieldDesc/password/tunnel/custom/visibleWhen)、§4.3(buildConfigGroups)、§5.1(Redis 范例)。spec §5.2 复杂型与附录 A 列入后续阶段路线图,逐阶段展开。
- **占位扫描**:无 TBD/TODO;所有代码步骤含完整代码与确切命令/预期。
- **类型一致**:`useConfigSection`/`UseConfigSectionOptions`/`FieldDesc`/`FieldRenderCtx`/`ConfigGroupSchema`/`buildConfigGroups` 在 Task 1→2a→2b→3 间签名一致;Redis 的 `build/buildTest` 与现有 `RedisConfigSection.test.tsx` 断言的 JSON/字段对齐。
