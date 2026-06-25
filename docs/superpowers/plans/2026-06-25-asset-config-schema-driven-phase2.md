# 资产配置驱动重构实现计划 · Phase 2(Etcd + MongoDB)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Etcd 与 MongoDB 两个常规资产配置区迁移到 Phase 1 的配置驱动基础设施(`useConfigSection` + `FieldDesc` schema + `buildConfigGroups`),并为此补齐渲染器三处小能力。

**Architecture:** 先做一次有界的渲染器扩展(`textarea` 补 `required`/`mono`,`segmented` 补 `ariaLabel`,`text`/`number`/`textarea` 的 `placeholder` 走 `t()`)—— 这些是 Etcd/Mongo 用到、Phase 1 Redis 未触及的呈现/ i18n 属性,非逻辑、非条件、非 DSL。然后两个 section 各自收缩为「~15 行胶水 + 纯数据字段 schema」,由现有回归测试把关。`*.config.ts` 序列化器不动。

**Tech Stack:** React 19 + TypeScript、`@opskat/ui`、react-i18next、vitest + @testing-library/react、pnpm。

依据:`docs/superpowers/specs/2026-06-25-asset-config-schema-driven-design.md`(§4 架构、§5.1 常规型范例)。Phase 1 已交付 `useConfigSection.ts` + `configFields.tsx`(`FieldDesc`/`Fields`/`buildConfigGroups`)+ Redis 迁移(commits 740d2f8f/347aaa61/549c249f/42efe965/200b34fe)。

## Global Constraints

- **不动序列化器**:`EtcdConfigSection.config.ts`、`MongoDBConfigSection.config.ts` 的 `build*`/`parse*`/`parseEtcdEndpoints`/`*_DEFAULTS` 一律不改。
- **不动既有测试**:`__tests__/EtcdConfigSection.test.tsx`、`__tests__/MongoDBConfigSection.test.tsx`、两者的 `.config.test.ts` 是回归网,不得修改,必须保持绿。`__tests__/configFields.test.tsx`(Phase 1)只允许**追加**新用例,不改既有用例。
- **不动共享契约**:`useAssetCredential`/`credentialConfig`/`proxyConfig`/`PasswordSourceField`/`ConnectionMethodFields`/`ConfigTabs`/`fields.tsx` 不改。`useConfigSection.ts` 不改。
- **无新增 i18n key**:复用现有键。
- **渲染器扩展只允许新增可选属性**,不得改变既有 `FieldDesc` 字段的默认行为(Phase 1 Redis 行为必须不变 —— 其 placeholder 均为字面量,`t()` 后原样返回)。
- **零视觉变化**为目标;两处**已知有意微调**(见各 Task)记录在案,均不被回归测试断言。
- **测试命令**(在 `frontend/` 目录执行):`pnpm test <文件名片段>` 跑单文件;`pnpm test` 跑全量;`pnpm exec tsc --noEmit` 与 `pnpm lint` 必须 0 错误/0 告警(每个 Task 都要跑 `pnpm lint`)。
- **提交规范**:gitmoji 前缀;subject 不带 PR/评审号。
- **路径别名**:`@/` → `frontend/src/`。

---

### Task 1: 渲染器扩展(textarea required/mono、segmented ariaLabel、placeholder i18n)

**Files:**
- Modify: `frontend/src/components/asset/configFields.tsx`(`FieldDesc` 的 `textarea`/`segmented` 成员加可选属性;`FieldNode` 的 `text`/`number`/`textarea`/`segmented` 四个 case 调整)
- Test: `frontend/src/components/asset/__tests__/configFields.test.tsx`(**追加**用例,不改既有)

**Interfaces:**
- Consumes:Phase 1 的 `Fields`/`FieldDesc`/`FieldNode`(同文件)、`Field`/`FieldLabel`/`Segmented`(`@/components/asset/fields`)、`Textarea`/`Input`/`Switch`/`Select*`(`@opskat/ui`)、`useTranslation`。
- Produces(Task 2/3 依赖):
  - `textarea` kind 新增可选 `required?: boolean`、`mono?: boolean`。
  - `segmented` kind 新增可选 `ariaLabel?: string`(i18n key;无可见 `label` 时用于 radiogroup 的 aria-label)。
  - `text`/`number`/`textarea` 的 `placeholder` 经 `t()` 解析(字面量原样返回,i18n key 被翻译)。

- [ ] **Step 1: 追加失败测试**

在 `frontend/src/components/asset/__tests__/configFields.test.tsx` 末尾追加(`Harness`/`S`/`stateOf` 均为 Phase 1 既有,`S` 已含 `host`/`note`/`mode` 字段):

```tsx
describe("Fields 渲染器 · Phase 2 扩展", () => {
  it("textarea: required 渲染必填星号, mono 加等宽类", () => {
    const { getByRole, container } = render(
      <Harness fields={[{ kind: "textarea", key: "note", label: "asset.endpoints", required: true, mono: true }]} />
    );
    const ta = getByRole("textbox") as HTMLTextAreaElement;
    expect(ta.className).toContain("font-mono");
    expect(container.textContent).toContain("*"); // FieldLabel 在必填时渲染 " *"
  });

  it("textarea: 无 mono 时不加等宽类", () => {
    const { getByRole } = render(<Harness fields={[{ kind: "textarea", key: "note", label: "asset.endpoints" }]} />);
    expect((getByRole("textbox") as HTMLTextAreaElement).className).not.toContain("font-mono");
  });

  it("segmented: ariaLabel 提供时 radiogroup 有 aria-label(无可见 label 也生效)", () => {
    const { getByRole } = render(
      <Harness
        fields={[
          {
            kind: "segmented",
            key: "mode",
            ariaLabel: "asset.mongoUri",
            options: [
              { value: "manual", label: "Manual" },
              { value: "uri", label: "URI" },
            ],
          },
        ]}
      />
    );
    // label 未给 → aria-label 唯一来源是 ariaLabel;断言其非空(不依赖 i18n 是否翻译该键)
    expect(getByRole("radiogroup").getAttribute("aria-label")).toBeTruthy();
  });

  it("text: 字面量 placeholder 经 t() 后原样透出(Phase 1 行为不变)", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "text", key: "host", label: "asset.host", placeholder: "example.com", testid: "f-host" }]} />
    );
    expect((getByTestId("f-host") as HTMLInputElement).placeholder).toBe("example.com");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test configFields`
Expected: FAIL —— 新增的 textarea-mono / segmented-ariaLabel 用例失败(当前 `textarea` 无 `mono`、`segmented` 无 `ariaLabel`)。text-placeholder 用例此时应已通过(字面量行为)。

- [ ] **Step 3: 改 `FieldDesc` 两个成员的类型**

在 `configFields.tsx` 的 `FieldDesc<S>` 联合里,把 `textarea` 与 `segmented` 两个成员整行替换为:

```ts
    | { kind: "textarea"; key: keyof S; label: string; rows?: number; hint?: string; placeholder?: string; required?: boolean; mono?: boolean }
    | { kind: "segmented"; key: keyof S; label?: string; ariaLabel?: string; options: { value: string; label: string; testid?: string }[] }
```

- [ ] **Step 4: 改 `FieldNode` 的四个 case**

在 `configFields.tsx` 的 `FieldNode` switch 中,把 `text`、`number`、`textarea`、`segmented` 四个 case **整段**替换为下面版本(其余 case 不动)。要点:`placeholder` 改为 `field.placeholder ? t(field.placeholder) : undefined`;`textarea` 加 `required`/`mono`;`segmented` 的 aria-label 优先用 `ariaLabel`。

```tsx
    case "text":
      return (
        <Field label={t(field.label)} required={field.required} className={field.width}>
          <Input
            data-testid={field.testid}
            value={String(state[field.key] ?? "")}
            placeholder={field.placeholder ? t(field.placeholder) : undefined}
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
            placeholder={field.placeholder ? t(field.placeholder) : undefined}
            onChange={(e) => {
              const n = Number(e.target.value);
              const next = field.min !== undefined ? Math.max(field.min, n || 0) : n;
              patch({ [field.key]: next } as Partial<S>);
            }}
          />
        </Field>
      );
    }

    case "textarea":
      return (
        <Field label={t(field.label)} required={field.required}>
          <Textarea
            value={String(state[field.key] ?? "")}
            rows={field.rows}
            placeholder={field.placeholder ? t(field.placeholder) : undefined}
            className={field.mono ? "font-mono text-sm" : undefined}
            onChange={(e) => patch({ [field.key]: e.target.value } as Partial<S>)}
          />
          {field.hint && <p className="text-xs text-muted-foreground">{t(field.hint)}</p>}
        </Field>
      );

    case "segmented":
      return (
        <Field label={field.label ? t(field.label) : undefined}>
          <Segmented
            value={String(state[field.key] ?? "")}
            onChange={(v) => patch({ [field.key]: v } as Partial<S>)}
            aria-label={field.ariaLabel ? t(field.ariaLabel) : field.label ? t(field.label) : undefined}
            options={field.options.map((o) => ({ value: o.value, label: t(o.label), testid: o.testid }))}
          />
        </Field>
      );
```

> 注:`text`/`number`/`textarea` 三处仅 `placeholder` 那一行从 `field.placeholder` 变为 `field.placeholder ? t(field.placeholder) : undefined`;Phase 1 Redis 的 placeholder 均为字面量(`example.com`/`6379`/`:` 等),`t()` 找不到对应 key 时原样返回,故 Redis 视觉不变。`number` 的 `display`/`min`/clamp 逻辑保持 Phase 1 原样。

- [ ] **Step 5: 跑测试确认通过 + 门禁**

Run: `pnpm test configFields`
Expected: PASS(Phase 1 既有用例 + 4 个新用例全绿)。
Run: `pnpm exec tsc --noEmit`(0 错误)、`pnpm lint`(0 问题)、`pnpm test`(全量绿,含 Redis 回归不变)。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/components/asset/configFields.tsx frontend/src/components/asset/__tests__/configFields.test.tsx
git commit -m "✨ 字段 schema 补 textarea required/mono、segmented ariaLabel、placeholder i18n"
```

---

### Task 2: Etcd 配置区迁移

**Files:**
- Modify(整文件改写): `frontend/src/components/asset/EtcdConfigSection.tsx`
- Test(回归网,**不改**): `frontend/src/components/asset/__tests__/EtcdConfigSection.test.tsx` + `EtcdConfigSection.config.test.ts`

**Interfaces:**
- Consumes:`useConfigSection`(`@/components/asset/useConfigSection`)、`buildConfigGroups`/`ConfigGroupSchema`(`@/components/asset/configFields`,含 Task 1 的 `textarea` `required`/`mono`)、`ConfigTabs`(`@/components/asset/ConfigTabs`)、`useAssetCredential`/`resolveSaveCredential`/`resolveTestCredential`/`resolveSaveProxyPassword`、`buildEtcdConfig`/`parseEtcdConfig`/`parseEtcdEndpoints`/`ETCD_DEFAULTS`/`EtcdFormState`(`./EtcdConfigSection.config`,不动)。
- Produces:`EtcdConfigSection`(`ConfigSectionComponent`,签名不变)。

> 已知有意微调(记录在案):TLS 标签内的开关标签从 `@opskat/ui` 的 `<Label>`(14px)改为渲染器的 `<FieldLabel>`(11px,v3 字段标签风格,与 Redis 一致)。纯外观归一,不被回归测试断言。Etcd 无「高级」标签(超时项在「连接」内),迁移后维持 3 标签(连接/隧道/TLS)。Etcd 序列化器始终在 jumphost 时写 `ssh_asset_id`(无 `includeSshAssetId` 参数),故 `build` 与 `buildTest` 的 configJSON 同形——这与现有 `EtcdConfigSection.test.tsx` 的断言一致。

- [ ] **Step 1: 跑回归测试确认基线为绿**

Run: `pnpm test EtcdConfigSection`
Expected: PASS —— `EtcdConfigSection.test.tsx`(3 用例)+ `EtcdConfigSection.config.test.ts` 全绿。

- [ ] **Step 2: 整文件改写 EtcdConfigSection.tsx**

用以下内容**整体替换** `frontend/src/components/asset/EtcdConfigSection.tsx`:

```tsx
import { forwardRef } from "react";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { resolveSaveProxyPassword } from "./proxyConfig";
import {
  buildEtcdConfig,
  parseEtcdConfig,
  parseEtcdEndpoints,
  ETCD_DEFAULTS,
  type EtcdFormState,
} from "./EtcdConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

const ETCD_GROUPS: ConfigGroupSchema<EtcdFormState>[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    fields: [
      {
        kind: "textarea",
        key: "endpoints",
        label: "etcd.form.endpoints",
        required: true,
        mono: true,
        rows: 3,
        placeholder: "10.0.0.1:2379\n10.0.0.2:2379",
        hint: "etcd.form.endpointsHint",
      },
      { kind: "text", key: "username", label: "asset.username" },
      { kind: "password" },
      {
        kind: "row",
        fields: [
          { kind: "number", key: "dialTimeoutSeconds", label: "etcd.form.dialTimeout", min: 0, width: "flex-1" },
          { kind: "number", key: "commandTimeoutSeconds", label: "etcd.form.commandTimeout", min: 0, width: "flex-1" },
        ],
      },
    ],
  },
  { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  {
    key: "tls",
    label: "asset.tabTls",
    fields: [
      { kind: "switch", key: "tls", label: "asset.tls" },
      { kind: "switch", key: "tlsInsecure", label: "etcd.form.tlsInsecure", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsServerName", label: "etcd.form.tlsServerName", placeholder: "etcd.example.com", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsCAFile", label: "etcd.form.tlsCAFile", placeholder: "/path/to/ca.pem", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsCertFile", label: "etcd.form.tlsCertFile", placeholder: "/path/to/client.crt", visibleWhen: (s) => s.tls },
      { kind: "text", key: "tlsKeyFile", label: "etcd.form.tlsKeyFile", placeholder: "/path/to/client.key", visibleWhen: (s) => s.tls },
    ],
  },
];

export const EtcdConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function EtcdConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const cred = useAssetCredential(editAsset);
  const { state, patch } = useConfigSection<EtcdFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseEtcdConfig(a.Config, a.sshTunnelId || 0) : { ...ETCD_DEFAULTS }),
    validate: (s) => {
      const ok = parseEtcdEndpoints(s.endpoints).length > 0;
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "etcd.error.endpointsRequired" };
    },
    build: async (s, ctx) => ({
      configJSON: buildEtcdConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        await resolveSaveProxyPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "etcd",
      configJSON: buildEtcdConfig(s, resolveTestCredential(cred.value), s.proxyPassword),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups = buildConfigGroups(ETCD_GROUPS, { state, patch, ctx: { cred, editAsset } });
  return <ConfigTabs groups={groups} />;
});
```

- [ ] **Step 3: 跑回归测试确认仍为绿**

Run: `pnpm test EtcdConfigSection`
Expected: PASS —— 3 个 ref 契约/校验用例 + 序列化器测试全绿(零回归)。

- [ ] **Step 4: 门禁**

Run: `pnpm exec tsc --noEmit`(0 错误)、`pnpm lint`(0 问题)。

- [ ] **Step 5: 全量 + 提交**

Run: `pnpm test`(全量绿)。

```bash
git add frontend/src/components/asset/EtcdConfigSection.tsx
git commit -m "♻️ etcd 配置区改用配置驱动渲染"
```

---

### Task 3: MongoDB 配置区迁移

**Files:**
- Modify(整文件改写): `frontend/src/components/asset/MongoDBConfigSection.tsx`
- Test(回归网,**不改**): `frontend/src/components/asset/__tests__/MongoDBConfigSection.test.tsx` + `MongoDBConfigSection.config.test.ts`

**Interfaces:**
- Consumes:`useConfigSection`、`buildConfigGroups`/`ConfigGroupSchema`(含 Task 1 的 `segmented` `ariaLabel` 与 placeholder `t()`)、`ConfigTabs`、`useAssetCredential`/`resolveSaveCredential`/`resolveTestCredential`/`resolveSaveProxyPassword`、`buildMongoDBConfig`/`parseMongoDBConfig`/`MONGODB_DEFAULTS`/`MongoDBFormState`(`./MongoDBConfigSection.config`,不动)。
- Produces:`MongoDBConfigSection`(`ConfigSectionComponent`,签名不变)。

> 已知有意微调(记录在案):TLS 开关标签 `<Label>`→`<FieldLabel>`(同 Etcd,v3 归一)。连接模式 `Manual|URI` 段控件无可见 label(与现状一致),保留 aria-label="asset.mongoUri"(经新 `ariaLabel`)。host/port 行(manual)与 URI 字段(uri)互斥用 `visibleWhen`。`build` 用 `includeSshAssetId=false`、`buildTest` 用 `true`(与 Redis 同模式,匹配现有 Mongo 测试)。

- [ ] **Step 1: 跑回归测试确认基线为绿**

Run: `pnpm test MongoDBConfigSection`
Expected: PASS —— `MongoDBConfigSection.test.tsx`(7 用例)+ `MongoDBConfigSection.config.test.ts` 全绿。

- [ ] **Step 2: 整文件改写 MongoDBConfigSection.tsx**

用以下内容**整体替换** `frontend/src/components/asset/MongoDBConfigSection.tsx`:

```tsx
import { forwardRef } from "react";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { resolveSaveProxyPassword } from "./proxyConfig";
import {
  buildMongoDBConfig,
  parseMongoDBConfig,
  MONGODB_DEFAULTS,
  type MongoDBFormState,
} from "./MongoDBConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

const MONGODB_GROUPS: ConfigGroupSchema<MongoDBFormState>[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    fields: [
      {
        kind: "segmented",
        key: "connectionMode",
        ariaLabel: "asset.mongoUri",
        options: [
          { value: "manual", label: "Manual" },
          { value: "uri", label: "URI" },
        ],
      },
      {
        kind: "row",
        visibleWhen: (s) => s.connectionMode === "manual",
        fields: [
          { kind: "text", key: "host", label: "asset.host", placeholder: "example.com", width: "flex-1" },
          { kind: "number", key: "port", label: "asset.port", placeholder: "27017", width: "w-[110px] shrink-0", blankWhenZero: true },
        ],
      },
      {
        kind: "text",
        key: "connectionURI",
        label: "asset.mongoUri",
        placeholder: "asset.mongoUriPlaceholder",
        visibleWhen: (s) => s.connectionMode === "uri",
      },
      { kind: "text", key: "username", label: "asset.username" },
      { kind: "password" },
      { kind: "text", key: "database", label: "asset.mongoDefaultDatabase", placeholder: "asset.mongoDefaultDatabasePlaceholder" },
    ],
  },
  { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  { key: "tls", label: "asset.tabTls", fields: [{ kind: "switch", key: "tls", label: "asset.tls" }] },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    fields: [
      { kind: "text", key: "replicaSet", label: "asset.mongoReplicaSet", placeholder: "asset.mongoReplicaSetPlaceholder" },
      { kind: "text", key: "authSource", label: "asset.mongoAuthSource", placeholder: "asset.mongoAuthSourcePlaceholder" },
    ],
  },
];

export const MongoDBConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function MongoDBConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const cred = useAssetCredential(editAsset);
  const { state, patch } = useConfigSection<MongoDBFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseMongoDBConfig(a.Config, a.sshTunnelId || 0) : { ...MONGODB_DEFAULTS }),
    validate: (s) => {
      const ok = s.connectionMode === "uri" ? !!s.connectionURI.trim() : !!s.host.trim();
      const saveDisabledReason = ok ? "" : s.connectionMode === "uri" ? "asset.formMissingMongoUri" : "asset.formMissingHost";
      return { canTest: ok, canSave: ok, saveDisabledReason };
    },
    build: async (s, ctx) => ({
      configJSON: buildMongoDBConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        false,
        await resolveSaveProxyPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "mongodb",
      configJSON: buildMongoDBConfig(s, resolveTestCredential(cred.value), true, s.proxyPassword),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups = buildConfigGroups(MONGODB_GROUPS, { state, patch, ctx: { cred, editAsset } });
  return <ConfigTabs groups={groups} />;
});
```

- [ ] **Step 3: 跑回归测试确认仍为绿**

Run: `pnpm test MongoDBConfigSection`
Expected: PASS —— 7 个 ref 契约/校验用例(manual/uri 校验、inline/managed 凭据 build、uri build 不含 host/port)+ 序列化器测试全绿(零回归)。

- [ ] **Step 4: 门禁**

Run: `pnpm exec tsc --noEmit`(0 错误)、`pnpm lint`(0 问题)。

- [ ] **Step 5: 全量 + 提交**

Run: `pnpm test`(全量绿)。

```bash
git add frontend/src/components/asset/MongoDBConfigSection.tsx
git commit -m "♻️ MongoDB 配置区改用配置驱动渲染"
```

---

## Phase 2 完成判据

- 渲染器补齐 textarea(required/mono)、segmented(ariaLabel)、placeholder i18n;新增单测绿;Phase 1 Redis 行为不变。
- Etcd(3 标签)、MongoDB(4 标签,manual/uri 互斥)各自由手写组件收缩为「胶水 + 纯数据 schema」,既有回归测试零改动且全绿。
- `pnpm lint` 0 问题、`tsc` 0 错误、全量 vitest 绿。

## Self-Review(对照 spec / Phase 1)

- **覆盖**:Task 1 补足 Etcd/Mongo 所需的渲染器呈现/ i18n 属性(spec §5.1 常规型);Task 2/3 落地 spec §5.1 与计划路线图的 Etcd/MongoDB。
- **占位扫描**:无 TBD/TODO;每个代码步骤含完整代码与确切命令/预期。
- **类型一致**:`textarea`/`segmented` 新增属性在 Task 1 定义、Task 2/3 使用;`useConfigSection`/`buildConfigGroups`/`ConfigGroupSchema` 签名沿用 Phase 1;Etcd/Mongo 的 `build`/`buildTest` 与各自现有 `*.test.tsx` 断言的 JSON/字段对齐(Etcd 同形含 ssh_asset_id;Mongo save 省略、test 含)。
- **遗留**:Phase 1 final review 的 3 个 forward-decision(`custom(ctx)` 签名、`password` 的 `usernameKey`、`select` 写回断言)属 Phase 3 范畴,本 Phase 不涉及(Etcd/Mongo 不用 `custom`,password 均映射到 `username`)。
