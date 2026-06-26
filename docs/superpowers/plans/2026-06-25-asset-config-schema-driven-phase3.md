# 资产配置驱动重构实现计划 · Phase 3(Database + SSH)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Database 与 SSH —— 两个最复杂的资产配置区 —— 迁移到配置驱动基础设施:常规字段走声明式 `FieldDesc` schema,而带副作用/异步/分支的部分(驱动切换+图标联动、SQLite 源、SSH 密钥扫描)走 `custom` escape-hatch 闭包。

**Architecture:** 复杂型的 groups schema 定义在**组件内**(而非模块常量),使 `custom` 的 `render: () => (...)` 闭包能就地引用 section 的 `state`/`patch`/`t`/`handleDriverChange`/`managedKeys` 等 —— 复杂 JSX 原样保留,只是搬进 `custom` 渲染闭包。常规字段(host/port/username/password/库名/sslMode/TLS/params/readOnly)走声明式 + `visibleWhen`。渲染器仅需补一处 `segmented.width`(SSH 认证方式段控件在行内需定宽)。`*.config.ts` 序列化器不动。

**Tech Stack:** React 19 + TypeScript、`@opskat/ui`、react-i18next、vitest + @testing-library/react(含 `@testing-library/user-event` 驱动 Radix Select)、pnpm。

依据:`docs/superpowers/specs/2026-06-25-asset-config-schema-driven-design.md`(§5.2 复杂型策略)。前序:Phase 1(infra + Redis)、Phase 2(renderer ext + Etcd + MongoDB)。Phase 1/2 已交付 `useConfigSection`、`configFields`(`FieldDesc`/`Fields`/`buildConfigGroups`,含 `text`/`number`/`switch`/`select`/`segmented`/`textarea`/`row`/`password`/`tunnel`/`custom`,placeholder 走 `t()`,textarea `required`/`mono`,segmented `ariaLabel`)。

## Global Constraints

- **不动序列化器**:`DatabaseConfigSection.config.ts`(`buildDatabaseConfig`/`parseDatabaseConfig`/`applyDriverChange`/`driverIcon`/`DATABASE_DEFAULTS`)、`SSHConfigSection.config.ts`(`buildSSHConfig`/`parseSSHConfig`/`parseSSHPasswordCredentialConfig`/`SSH_DEFAULTS`/`SSHBuildOptions`)一律不改。
- **不动既有测试**:`__tests__/DatabaseConfigSection.test.tsx`(10 用例,含 userEvent 驱动 driver Select)、`__tests__/SSHConfigSection.test.tsx`(11 用例,含 key-auth build、proxy、jumphost、用户名 autofill ×4)及两者 `.config.test.ts` 是回归网,不得修改,必须保持绿。`__tests__/configFields.test.tsx` 只允许**追加**用例。
- **不动共享契约**:`useConfigSection`、`useAssetCredential`、`credentialConfig`、`proxyConfig`、`PasswordSourceField`、`ConnectionMethodFields`、`ConfigTabs`、`AssetSelect`、`fields.tsx` 不改。
- **无新增 i18n key**。
- **复杂 JSX 原样保留**:`custom` 的渲染内容是从现有文件**逐字搬入**的 JSX(只是包进 `render: () => (...)`),不重写其逻辑/结构 —— 这是 spec §5.2 的 escape-hatch。
- **零视觉变化**为目标;已知有意微调(见各 Task)记录在案,均不被回归测试断言。
- **测试命令**(在 `frontend/`):`pnpm test <片段>`;`pnpm test` 全量;`pnpm exec tsc --noEmit` 0 错误;`pnpm lint` 0 问题(每个 Task 都跑)。
- **提交规范**:gitmoji 前缀,subject 不带 PR/评审号。路径别名 `@/` → `frontend/src/`。

## 关于两个被推迟的 Phase 1 forward-decision(本 Phase 不做,附理由)

- **`custom.render` 加 `ctx` 第三参**:不做。复杂型 schema 定义在组件内,`custom` 闭包直接引用 section 的 `cred`/`editAsset`/handlers,无需经 `ctx` 传入;且 TS 中给回调类型加形参对既有 `() => JSX` / `(s,p) => JSX` 闭包是**向后兼容**的(实参多余被忽略),将来真有模块级 schema 的 custom 需要时再加也非破坏性改动。故按 YAGNI 推迟。
- **`password` 的 `usernameKey?`**:不做。Database/SSH 的 password 字段均把用户名写入 `username`(默认行为已正确),`{kind:"password"}` 直接可用;现有 `as unknown as Partial<S>` 在运行时无误。属纯代码风格优化,继续推迟(SSH 的 autofill 回归测试仍覆盖该路径)。

---

### Task 1: 渲染器补 `segmented.width` + 补 `select` 写回交互测试

**Files:**
- Modify: `frontend/src/components/asset/configFields.tsx`(`segmented` 成员加 `width?`;`segmented` case 的 `<Field>` 加 `className`)
- Test: `frontend/src/components/asset/__tests__/configFields.test.tsx`(**追加**:`segmented` width 用例 + `select` 写回交互用例;后者补 happy-dom 的 Radix 桩)

**Interfaces:**
- Consumes:Phase 1/2 的 `Fields`/`FieldDesc`/`Field`/`Segmented`。
- Produces(Task 3 依赖):`segmented` kind 新增可选 `width?: string`(作为其 `<Field>` 的 `className`,供行内定宽,如 SSH 认证方式 `w-[190px] shrink-0`)。

> 本任务也补上 Phase 1/2 推迟的 deferred-item:`select` kind 之前只有"trigger 渲染"的近空测试;这里加一个真正用 userEvent 驱动 Radix Select 选项、断言写回 state 的交互测试(happy-dom 下需桩 `scrollIntoView`/`hasPointerCapture`/`releasePointerCapture`,模式同 `DatabaseConfigSection.test.tsx`)。

- [ ] **Step 1: 追加失败测试**

在 `configFields.test.tsx`:顶部 import 处追加 `import userEvent from "@testing-library/user-event";`,并在现有 import 之后、首个 `describe` 之前加(若文件已 import `beforeAll`/`vi` 则复用):

```tsx
import { beforeAll } from "vitest";

// Radix Select 在 happy-dom 无 layout/pointer-capture,补齐 userEvent 驱动所需最小桩。
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});
```

并在文件末尾追加:

```tsx
describe("Fields 渲染器 · Phase 3 扩展", () => {
  it("segmented: width 作为 Field 的 className", () => {
    const { container } = render(
      <Harness
        fields={[
          {
            kind: "segmented",
            key: "mode",
            width: "w-[190px] shrink-0",
            options: [
              { value: "manual", label: "Manual" },
              { value: "uri", label: "URI" },
            ],
          },
        ]}
      />
    );
    expect(container.innerHTML).toContain("w-[190px]");
  });

  it("select: 点击选项写回 state", async () => {
    const user = userEvent.setup();
    const { getByTestId, findByRole } = render(
      <Harness
        fields={[
          {
            kind: "select",
            key: "driver",
            label: "asset.driver",
            testid: "f-driver",
            options: [
              { value: "mysql", label: "MySQL" },
              { value: "postgresql", label: "PostgreSQL" },
            ],
          },
        ]}
      />
    );
    await user.click(getByTestId("f-driver"));
    await user.click(await findByRole("option", { name: "PostgreSQL" }));
    expect(stateOf(getByTestId("state")).driver).toBe("postgresql");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test configFields`
Expected: FAIL —— segmented-width 用例失败(`segmented` 尚无 `width`,`w-[190px]` 不出现)。select 交互用例此时应已通过(`select` 写回逻辑 Phase 1 已实现;本用例是补测,非改行为)。

- [ ] **Step 3: 给 `segmented` 加 `width`**

在 `configFields.tsx`:把 `FieldDesc<S>` 的 `segmented` 成员整行替换为(在既有 `ariaLabel?` 基础上追加 `width?`):

```ts
    | { kind: "segmented"; key: keyof S; label?: string; ariaLabel?: string; width?: string; options: { value: string; label: string; testid?: string }[] }
```

并把 `FieldNode` 的 `segmented` case 的 `<Field ...>` 开标签改为带 `className`(其余不变):

```tsx
        <Field label={field.label ? t(field.label) : undefined} className={field.width}>
```

- [ ] **Step 4: 跑测试确认通过 + 门禁**

Run: `pnpm test configFields`(全绿,含 2 个新用例)。
Run: `pnpm exec tsc --noEmit`(0)、`pnpm lint`(0)、`pnpm test`(全量绿)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/asset/configFields.tsx frontend/src/components/asset/__tests__/configFields.test.tsx
git commit -m "✨ 字段 schema 补 segmented width + select 写回交互测试"
```

---

### Task 2: Database 配置区迁移

**Files:**
- Modify(整文件改写): `frontend/src/components/asset/DatabaseConfigSection.tsx`
- Test(回归网,**不改**): `__tests__/DatabaseConfigSection.test.tsx` + `DatabaseConfigSection.config.test.ts`

**Interfaces:**
- Consumes:`useConfigSection`、`buildConfigGroups`/`ConfigGroupSchema`、`ConfigTabs`、`useAssetCredential`/`resolveSaveCredential`/`resolveTestCredential`/`resolveSaveProxyPassword`、`AssetSelect`、`Field`/`Segmented`、`@opskat/ui`(Button/Input/Select*)、`SelectSQLiteFile`(wails)、`buildDatabaseConfig`/`parseDatabaseConfig`/`applyDriverChange`/`driverIcon`/`DATABASE_DEFAULTS`/`DatabaseFormState`(不动)。
- Produces:`DatabaseConfigSection`(签名不变,仍读 `onIconChange`)。

> 已知有意微调(记录在案):①TLS 标签内开关标签由 `<FieldLabel>`(原已是)保持;②端口字段去掉"按驱动动态变化的 placeholder"——端口恒有默认值(`applyDriverChange` 置 3306/5432/1433),placeholder 实际几乎不可见,改为无 placeholder。均不被回归测试断言。driver Select / SQLite 源 / SQLite 路径(含浏览)/ 远程 SSH 选择器走 `custom`(闭包引用 section 的 `t`/`handleDriverChange`/`state`/`patch`/`SelectSQLiteFile`);其余声明式 + `visibleWhen`。

- [ ] **Step 1: 跑回归测试确认基线为绿**

Run: `pnpm test DatabaseConfigSection`
Expected: PASS(10 用例 + 序列化器测试)。

- [ ] **Step 2: 整文件改写 DatabaseConfigSection.tsx**

用以下内容**整体替换** `frontend/src/components/asset/DatabaseConfigSection.tsx`:

```tsx
import { forwardRef } from "react";
import { useTranslation } from "react-i18next";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { resolveSaveProxyPassword } from "./proxyConfig";
import { SelectSQLiteFile } from "../../../wailsjs/go/system/System";
import {
  applyDriverChange,
  buildDatabaseConfig,
  driverIcon,
  parseDatabaseConfig,
  DATABASE_DEFAULTS,
  type DatabaseFormState,
} from "./DatabaseConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

export const DatabaseConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function DatabaseConfigSection(
  { editAsset, onValidityChange, onIconChange },
  ref
) {
  const { t } = useTranslation();
  const cred = useAssetCredential(editAsset);
  const { state, setState, patch } = useConfigSection<DatabaseFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseDatabaseConfig(a.Config, a.sshTunnelId || 0) : { ...DATABASE_DEFAULTS }),
    validate: (s) => {
      const isSqlite = s.driver === "sqlite";
      const isRemoteSqlite = isSqlite && s.sqliteSource === "remote_ssh_vfs";
      const canSave = isSqlite ? !!s.path.trim() && (!isRemoteSqlite || s.sshTunnelId > 0) : !!s.host.trim();
      const saveDisabledReason = canSave
        ? ""
        : isRemoteSqlite && s.path.trim() && s.sshTunnelId <= 0
          ? "asset.formMissingSQLiteSSH"
          : isSqlite
            ? "asset.formMissingPath"
            : "asset.formMissingHost";
      return { canTest: canSave, canSave, saveDisabledReason };
    },
    build: async (s, ctx) => ({
      configJSON: buildDatabaseConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        await resolveSaveProxyPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "database",
      configJSON: buildDatabaseConfig(s, resolveTestCredential(cred.value), s.proxyPassword),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  // driver 切换:section 自有字段复位(纯函数)+ 壳 icon 副作用(onIconChange)。
  const handleDriverChange = (newDriver: string) => {
    setState((s) => applyDriverChange(s, newDriver));
    onIconChange?.(driverIcon(newDriver));
  };

  const groups: ConfigGroupSchema<DatabaseFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "custom",
          render: () => (
            <Field label={t("asset.driver")}>
              <Select value={state.driver} onValueChange={handleDriverChange}>
                <SelectTrigger data-testid="database-driver-select" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="mysql" data-testid="database-driver-option-mysql">
                    {t("asset.driverMySQL")}
                  </SelectItem>
                  <SelectItem value="postgresql" data-testid="database-driver-option-postgresql">
                    {t("asset.driverPostgreSQL")}
                  </SelectItem>
                  <SelectItem value="mssql" data-testid="database-driver-option-mssql">
                    {t("asset.driverMSSQL")}
                  </SelectItem>
                  <SelectItem value="sqlite" data-testid="database-driver-option-sqlite">
                    {t("asset.driverSQLite")}
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
          ),
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.driver === "sqlite",
          render: () => (
            <Field label={t("asset.sqliteSource")}>
              <Segmented
                value={state.sqliteSource}
                onChange={(v) => {
                  if (v === "remote_ssh_vfs") {
                    patch({ sqliteSource: "remote_ssh_vfs", connectionType: "jumphost" });
                  } else {
                    patch({ sqliteSource: "local", sshTunnelId: 0, connectionType: "direct" });
                  }
                }}
                aria-label={t("asset.sqliteSource")}
                options={[
                  { value: "local", label: t("asset.sqliteSourceLocal"), testid: "database-sqlite-source-option-local" },
                  {
                    value: "remote_ssh_vfs",
                    label: t("asset.sqliteSourceRemoteSSH"),
                    testid: "database-sqlite-source-option-remote",
                  },
                ]}
              />
            </Field>
          ),
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.driver === "sqlite",
          render: () => {
            const isRemoteSqlite = state.sqliteSource === "remote_ssh_vfs";
            return (
              <Field label={t("asset.sqliteFilePath")}>
                <div className="flex gap-2">
                  <Input
                    data-testid="database-sqlite-path-input"
                    value={state.path}
                    onChange={(e) => patch({ path: e.target.value })}
                    placeholder={isRemoteSqlite ? "/var/lib/app/app.db" : t("asset.sqliteFilePathPlaceholder")}
                  />
                  {!isRemoteSqlite && (
                    <Button
                      type="button"
                      variant="secondary"
                      onClick={async () => {
                        const selected = await SelectSQLiteFile();
                        if (selected) patch({ path: selected });
                      }}
                    >
                      {t("asset.sqliteFilePathBrowse")}
                    </Button>
                  )}
                </div>
              </Field>
            );
          },
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.driver === "sqlite" && s.sqliteSource === "remote_ssh_vfs",
          render: () => (
            <Field label={t("asset.sqliteRemoteSSH")}>
              <AssetSelect
                value={state.sshTunnelId}
                onValueChange={(v) => patch({ sshTunnelId: v, connectionType: "jumphost" })}
                filterType="ssh"
                placeholder={t("asset.jumpHostNone")}
                testId="database-sqlite-ssh-select"
              />
            </Field>
          ),
        },
        {
          kind: "row",
          visibleWhen: (s) => s.driver !== "sqlite",
          fields: [
            { kind: "text", key: "host", label: "asset.host", required: true, placeholder: "example.com", width: "flex-1", testid: "database-host-input" },
            { kind: "number", key: "port", label: "asset.port", width: "w-[110px] shrink-0", blankWhenZero: true, testid: "database-port-input" },
          ],
        },
        { kind: "text", key: "username", label: "asset.username", testid: "database-username-input", visibleWhen: (s) => s.driver !== "sqlite" },
        { kind: "password", visibleWhen: (s) => s.driver !== "sqlite" },
        { kind: "text", key: "database", label: "asset.database", placeholder: "asset.databasePlaceholder", testid: "database-name-input", visibleWhen: (s) => s.driver !== "sqlite" },
      ],
    },
    { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
    {
      key: "tls",
      label: "asset.tabTls",
      fields: [
        {
          kind: "select",
          key: "sslMode",
          label: "asset.sslMode",
          visibleWhen: (s) => s.driver === "postgresql",
          options: [
            { value: "disable", label: "disable" },
            { value: "require", label: "require" },
            { value: "verify-ca", label: "verify-ca" },
            { value: "verify-full", label: "verify-full" },
          ],
        },
        { kind: "switch", key: "tls", label: "TLS", visibleWhen: (s) => s.driver === "mysql" || s.driver === "mssql" },
      ],
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        { kind: "text", key: "params", label: "asset.params", placeholder: "asset.paramsPlaceholder" },
        { kind: "switch", key: "readOnly", label: "asset.readOnly" },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
});
```

- [ ] **Step 3: 跑回归测试确认仍为绿**

Run: `pnpm test DatabaseConfigSection`
Expected: PASS —— 10 用例(含 userEvent 切 driver→sqlite 验 onIconChange('sqlite')+formMissingPath、postgresql/sqlite/managed build、remote sqlite)+ 序列化器测试全绿。

- [ ] **Step 4: 门禁**

Run: `pnpm exec tsc --noEmit`(0)、`pnpm lint`(0)。

- [ ] **Step 5: 全量 + 提交**

Run: `pnpm test`(全量绿)。

```bash
git add frontend/src/components/asset/DatabaseConfigSection.tsx
git commit -m "♻️ 数据库配置区改用配置驱动渲染"
```

---

### Task 3: SSH 配置区迁移

**Files:**
- Modify(整文件改写): `frontend/src/components/asset/SSHConfigSection.tsx`
- Test(回归网,**不改**): `__tests__/SSHConfigSection.test.tsx` + `SSHConfigSection.config.test.ts`

**Interfaces:**
- Consumes:`useConfigSection`、`buildConfigGroups`/`ConfigGroupSchema`(含 Task 1 的 `segmented.width`)、`ConfigTabs`、`useAssetCredential`/`resolveSaveCredential`/`resolveTestCredential`、`Field`/`Segmented`、`@opskat/ui`(Button/Input/Select*/Tooltip*)、icons、`toast`、wails(`ListCredentialsByType`/`ListLocalSSHKeys`/`SelectSSHKeyFile`)、models、`buildSSHConfig`/`parseSSHConfig`/`parseSSHPasswordCredentialConfig`/`SSH_DEFAULTS`/`SSHFormState`(不动)。
- Produces:`SSHConfigSection`(签名不变)。

> 关键:密钥认证(`authType === "key"`)整块 UI —— 托管 key 选择 / 本地 key 扫描勾选 / 浏览 / passphrase —— **逐字搬入** `custom` 渲染闭包,不重写。它引用 `state`/`patch`/`t`/`managedKeys`/`localKeys`/`scanningKeys`/`setLocalKeys`/`toast`/`SelectSSHKeyFile`,在新文件中全部在 scope。host/port、username+认证方式段控件、password、隧道走声明式;`{kind:"tunnel"}` 用 jumphost 文案 + `excludeIds`(排除自身)。`useAssetCredential` 用 `parseSSHPasswordCredentialConfig` 第二参初始化 password 凭据(SSH 特有)。

- [ ] **Step 1: 跑回归测试确认基线为绿**

Run: `pnpm test SSHConfigSection`
Expected: PASS(11 用例 + 序列化器测试,含 key-auth build、proxy、jumphost、用户名 autofill ×4)。

- [ ] **Step 2: 整文件改写 SSHConfigSection.tsx**

用以下内容**整体替换** `frontend/src/components/asset/SSHConfigSection.tsx`。**其中标注 `/* ↓↓↓ 逐字搬入 … ↓↓↓ */` 的 `custom.render` 返回体,直接从当前(改写前)`SSHConfigSection.tsx` 中 `{state.authType === "key" && ( ... )}` 内的整个 `<div className="flex flex-col gap-4"> … </div>` 原样复制进来,不做任何逻辑/结构改动**(它已引用 `state`/`patch`/`t`/`managedKeys`/`localKeys`/`scanningKeys`/`setLocalKeys`/`toast`/`SelectSSHKeyFile`,均在新文件 scope 内):

```tsx
import { forwardRef, useEffect, useMemo, useState } from "react";
import { Trash2, FolderOpen, Loader2, Lock } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  Button,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { ListCredentialsByType } from "../../../wailsjs/go/system/System";
import { ListLocalSSHKeys, SelectSSHKeyFile } from "../../../wailsjs/go/ssh/SSH";
import { credential_entity, ssh as ssh_models } from "../../../wailsjs/go/models";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildSSHConfig,
  parseSSHConfig,
  parseSSHPasswordCredentialConfig,
  SSH_DEFAULTS,
  type SSHFormState,
} from "./SSHConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

export const SSHConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function SSHConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  // password-auth 凭据复用 db 族抽象;key-auth ssh_key 凭据 + 本地密钥由本 section 自持。
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseSSHPasswordCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);

  const { state, patch } = useConfigSection<SSHFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseSSHConfig(a.Config, a.sshTunnelId || 0) : { ...SSH_DEFAULTS }),
    validate: (s) => {
      const ok = !!s.host.trim();
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingHost" };
    },
    build: async (s, ctx) => {
      // password-auth 凭据加密;passphrase / proxy 密码:明文优先加密,否则沿用既有密文。
      const passwordCred = await resolveSaveCredential(cred.value, ctx.encryptPassword);
      const passphrase = s.privateKeyPassphrase
        ? await ctx.encryptPassword(s.privateKeyPassphrase)
        : s.encryptedPrivateKeyPassphrase;
      const proxyPassword = s.proxyPassword ? await ctx.encryptPassword(s.proxyPassword) : s.encryptedProxyPassword;
      return {
        configJSON: buildSSHConfig(s, {
          passwordCred,
          keyCredentialId: s.credentialId,
          passphrase,
          proxyPassword,
          includeJumpHost: false, // save:隧道写 asset 顶层 sshTunnelId,不入 config.jump_host_id
        }),
        sshTunnelId: s.connectionType === "jumphost" && s.sshTunnelId > 0 ? s.sshTunnelId : 0,
      };
    },
    buildTest: async (s) => ({
      assetType: "ssh",
      // 测试:passphrase / proxy 用明文(passphrase 缺明文时沿用既有密文;proxy 仅明文),后端从 config.jump_host_id 读隧道。
      configJSON: buildSSHConfig(s, {
        passwordCred: resolveTestCredential(cred.value),
        keyCredentialId: s.credentialId,
        passphrase: s.privateKeyPassphrase || s.encryptedPrivateKeyPassphrase,
        proxyPassword: s.proxyPassword,
        includeJumpHost: true,
      }),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const [managedKeys, setManagedKeys] = useState<credential_entity.Credential[]>([]);
  const [localKeys, setLocalKeys] = useState<ssh_models.LocalSSHKeyInfo[]>([]);
  // 挂载即扫描,初始 true(避免在 effect 内同步 setState 触发级联渲染)。
  const [scanningKeys, setScanningKeys] = useState(true);

  // 自加载 ssh_key 凭据列表 + 扫描本地密钥(镜像旧壳 open 时的合并 load)。
  useEffect(() => {
    ListCredentialsByType("ssh_key")
      .then((keys) => setManagedKeys(keys || []))
      .catch(() => setManagedKeys([]));
    ListLocalSSHKeys()
      .then((keys) => setLocalKeys(keys || []))
      .catch(() => setLocalKeys([]))
      .finally(() => setScanningKeys(false));
  }, []);

  // 排除自身,不能把自己选作跳板机 / SSH 隧道。
  const jumpHostExcludeIds = editAsset?.ID ? [editAsset.ID] : undefined;

  const groups: ConfigGroupSchema<SSHFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "row",
          fields: [
            { kind: "text", key: "host", label: "asset.host", required: true, placeholder: "example.com", width: "flex-1", testid: "ssh-host-input" },
            { kind: "number", key: "port", label: "asset.port", width: "w-[110px] shrink-0", blankWhenZero: true, placeholder: "22", testid: "ssh-port-input" },
          ],
        },
        {
          kind: "row",
          fields: [
            { kind: "text", key: "username", label: "asset.username", width: "flex-1", testid: "ssh-username-input" },
            {
              kind: "segmented",
              key: "authType",
              label: "asset.authType",
              width: "w-[190px] shrink-0",
              options: [
                { value: "password", label: "asset.authPassword", testid: "ssh-auth-type-option-password" },
                { value: "key", label: "asset.authKey", testid: "ssh-auth-type-option-key" },
              ],
            },
          ],
        },
        { kind: "password", placeholder: "asset.passwordPlaceholder", visibleWhen: (s) => s.authType === "password" },
        {
          kind: "custom",
          visibleWhen: (s) => s.authType === "key",
          render: () => (
            /* ↓↓↓ 逐字搬入:当前 SSHConfigSection.tsx 中 {state.authType === "key" && ( ... )} 内的整个
                   <div className="flex flex-col gap-4"> ... </div>,原样不改 ↓↓↓ */
            <div className="flex flex-col gap-4">{/* …key-source UI… */}</div>
          ),
        },
      ],
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      fields: [
        {
          kind: "tunnel",
          tunnelOptionLabelKey: "asset.connectionJumpHost",
          tunnelSelectLabelKey: "asset.selectJumpHost",
          excludeIds: jumpHostExcludeIds,
        },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
});
```

> 实现者注意:上面 `render: () => (<div className="flex flex-col gap-4">{/* …key-source UI… */}</div>)` 是占位 —— 必须把当前文件里 `state.authType === "key"` 分支的**完整 `<div className="flex flex-col gap-4">…</div>`**(托管/本地密钥选择、勾选、`<Tooltip>`/`<Lock>`、浏览 `SelectSSHKeyFile`、额外路径列表、passphrase `<Field>`)逐字粘进去,替换该占位 div。该块不引用 `s`/`ctx`(用闭包里的 `state`/`patch`/`t`/`managedKeys`/`localKeys`/`scanningKeys`/`setLocalKeys`/`toast`),无需改写。

- [ ] **Step 3: 跑回归测试确认仍为绿**

Run: `pnpm test SSHConfigSection`
Expected: PASS —— 11 用例全绿,含:password/key-auth build round-trip、passphrase 加密/沿用、proxy 加密/明文、jumphost(save 顶层 sshTunnelId / test config jump_host_id)、key-auth→password 切换不复用 ssh_key credential_id、用户名 autofill(password ×2 / key ×2)。

- [ ] **Step 4: 门禁**

Run: `pnpm exec tsc --noEmit`(0)、`pnpm lint`(0)。

- [ ] **Step 5: 全量 + 提交**

Run: `pnpm test`(全量绿)。

```bash
git add frontend/src/components/asset/SSHConfigSection.tsx
git commit -m "♻️ SSH 配置区改用配置驱动渲染"
```

---

## Phase 3 完成判据

- 渲染器补 `segmented.width` + `select` 写回交互测试;新增测试绿;既有行为不变。
- Database(driver/sqlite custom + 网络字段声明式 + sslMode/TLS/params/readOnly)、SSH(host/port/username/authType/password 声明式 + key-auth 整块 custom 逐字保留 + jumphost 隧道)各自由手写组件迁移到 hook + schema;10/11 既有回归用例零改动且全绿。
- `pnpm lint` 0、`tsc` 0、全量 vitest 绿。
- 至此五个内置类型中 Redis/Etcd/MongoDB/Database/SSH 全部配置驱动;余 K8s(+代理 feature)、Serial/Local、Kafka 在 Phase 4/5。

## Self-Review(对照 spec / 前序 Phase)

- **覆盖**:Task 1 补 SSH 行内段控件定宽所需的 `segmented.width` + 兑现 deferred 的 select 写回测试;Task 2/3 落地 spec §5.2 的 Database / SSH(声明式 + `custom` escape-hatch)。
- **占位扫描**:除 SSH key-block 明确标注"逐字搬入当前文件该块"(有完整识别 + scope 说明,非内容缺失)外,无 TBD/TODO;其余代码步骤含完整代码与确切命令/预期。
- **类型一致**:`segmented.width` 在 Task 1 定义、Task 3 使用;`useConfigSection`/`buildConfigGroups`/`ConfigGroupSchema`/各 `FieldDesc` kind 沿用 Phase 1/2;Database/SSH 的 `init`/`validate`/`build`/`buildTest` 与各自现有 `*.test.tsx` 断言逐条对齐(Database:isSqlite/remote/network 校验三分支、buildDatabaseConfig 无 includeSshAssetId 故 save/test 同形含 ssh_asset_id、driver 切换 onIconChange;SSH:passphrase/proxy 明文密文分支、includeJumpHost save=false/test=true、autofill 经 password kind 默认写 username + key 块内 patch username)。
- **推迟项**:`custom(ctx)`、`password.usernameKey` 附理由推迟(见上"关于…forward-decision");`select` 写回测试本 Phase 兑现。
