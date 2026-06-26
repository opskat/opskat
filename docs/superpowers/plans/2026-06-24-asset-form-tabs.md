# 资产表单标签页化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把资产添加/编辑表单的类型配置区从单列平铺改成顶部水平标签页,第一个「连接」标签即可建立连接,其余标签为可选高级项。

**Architecture:** 新增共享原语 `<ConfigTabs>`(封装 `@opskat/ui` 的 `Tabs`),单分组自动退化为无标签单面板;各 `ConfigSection` 保留原 state + `*.config.ts` 序列化逻辑不变,只把 JSX 重排进 `groups` 数组并聚合每分组校验;shell(`AssetForm`)只新增描述折叠条 + 底部「前往」跳转,不按类型分支(OCP)。

**Tech Stack:** React 19 + TypeScript,`@opskat/ui`(shadcn 封装),react-i18next,vitest 4 + @testing-library/react(测试在 `src/__tests__/`,`react-i18next` 被全局 mock 成 `t: (key) => key`)。

## Global Constraints

- 无后端/无 config JSON 结构/无 `asset_entity.Asset` 模型改动 → **无数据迁移**;各 `*.config.ts` 的 `build*/parse*` 不改。
- 不增删字段,只重排其分组/位置。
- shell `AssetForm` 不得按资产类型分支(`if assetType === ...`/`switch`);仍只挂 `<sectionDef.ConfigSection>`。
- 复用现有共享组件:隧道/代理 = `ConnectionMethodFields`;密码 = `PasswordSourceField`/`useAssetCredential`;标签原语 = `@opskat/ui` `Tabs`。**新建前先 grep**。
- 测试放 `frontend/src/__tests__/*.test.tsx`;`t(key)` 在测试里渲染为 key 字面量,断言用 key 字符串。
- 运行测试目录:`frontend/`。命令 `npm test`(= `vitest run`)或 `npx vitest run <file>`。
- 提交用 gitmoji(见 docs/DEVELOP.md);subject 不带 PR/评审号。
- 工作分支:当前在 `main`,**第一步先切分支**(见 Task 0)。

---

### Task 0: 切工作分支

**Files:** 无(仅 git)

- [ ] **Step 1: 从最新 main 切分支**

```bash
cd /Users/codfrm/Code/opskat/opskat
git checkout -b refactor/asset-form-tabs
```

- [ ] **Step 2: 确认干净起点**

Run: `git status`
Expected: 在 `refactor/asset-form-tabs` 分支上(README.md / docs/README_zh.md 的既有改动与本任务无关,勿提交)。

---

### Task 1: 新增 i18n 键

**Files:**
- Modify: `frontend/src/i18n/locales/en/common.json`(`asset` 对象内)
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`(`asset` 对象内)

**Interfaces:**
- Produces: i18n 键 `asset.tabConnection` / `asset.tabTunnel` / `asset.tabTls` / `asset.tabAdvanced` / `asset.tabSchemaRegistry` / `asset.tabConnect` / `asset.optional` / `asset.goToTab` / `asset.addDescription`,供 Task 2 起所有任务使用。

- [ ] **Step 1: 在 en/common.json 的 `asset` 对象里加键**

```json
"tabConnection": "Connection",
"tabTunnel": "SSH Tunnel / Proxy",
"tabTls": "TLS / Certificates",
"tabAdvanced": "Advanced",
"tabSchemaRegistry": "Schema Registry",
"tabConnect": "Connect",
"optional": "Optional",
"goToTab": "Go to",
"addDescription": "Add description",
```

- [ ] **Step 2: 在 zh-CN/common.json 的 `asset` 对象里加同名键**

```json
"tabConnection": "连接",
"tabTunnel": "SSH 隧道/代理",
"tabTls": "TLS/证书",
"tabAdvanced": "高级",
"tabSchemaRegistry": "Schema Registry",
"tabConnect": "Connect",
"optional": "可选",
"goToTab": "前往",
"addDescription": "添加备注",
```

- [ ] **Step 3: 校验 JSON 合法**

Run: `cd frontend && node -e "JSON.parse(require('fs').readFileSync('src/i18n/locales/en/common.json','utf8'));JSON.parse(require('fs').readFileSync('src/i18n/locales/zh-CN/common.json','utf8'));console.log('ok')"`
Expected: 输出 `ok`

- [ ] **Step 4: Commit**

```bash
git add frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git commit -m "🌐 资产表单标签页 i18n 键"
```

---

### Task 2: 契约扩展(formContract)

**Files:**
- Modify: `frontend/src/lib/assetTypes/formContract.ts`

**Interfaces:**
- Produces:
  - `SectionValidity.invalidGroupKey?: string`
  - `AssetFormHandle.focusGroup?: (key: string) => void`

> 纯类型改动,无运行时测试;由 `tsc` 与后续任务消费。

- [ ] **Step 1: 给 `AssetFormHandle` 加 `focusGroup`**

把 `AssetFormHandle` 改为:

```ts
export interface AssetFormHandle {
  buildConfig: (ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  /** 仅可测类型实现;不可测类型为 null。 */
  buildTestConfig: ((ctx: AssetFormContext) => Promise<AssetTestConfig>) | null;
  /** 切到指定分组标签(配合 SectionValidity.invalidGroupKey 的"前往"跳转);单面板类型可不实现。 */
  focusGroup?: (key: string) => void;
}
```

- [ ] **Step 2: 给 `SectionValidity` 加 `invalidGroupKey`**

```ts
export interface SectionValidity {
  canTest: boolean;
  canSave: boolean;
  /** 保存禁用原因的 i18n key;空/缺省 = 可保存(壳据此显示提示)。 */
  saveDisabledReason?: string;
  /** 保存被禁用时应跳转到的分组 key(壳 footer 渲染"前往")。 */
  invalidGroupKey?: string;
}
```

- [ ] **Step 3: 类型检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无新增错误(既有未改文件若有历史告警与本任务无关)。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/assetTypes/formContract.ts
git commit -m "🏗️ 表单契约支持分组校验跳转"
```

---

### Task 3: `ConfigTabs` 共享原语

**Files:**
- Create: `frontend/src/components/asset/ConfigTabs.tsx`
- Test: `frontend/src/__tests__/ConfigTabs.test.tsx`

**Interfaces:**
- Consumes: `@opskat/ui` `Tabs/TabsList/TabsTrigger/TabsContent`;i18n `asset.optional`(Task 1)。
- Produces:
  - `interface ConfigGroup { key: string; label: string; optional?: boolean; badge?: number; invalid?: boolean; render: () => React.ReactNode; }`
  - `interface ConfigTabsHandle { setActive: (key: string) => void; }`
  - `const ConfigTabs: ForwardRefExoticComponent<{ groups: ConfigGroup[] } & RefAttributes<ConfigTabsHandle>>`
  - testid 约定:标签 `config-tab-<key>`;红点 `config-tab-dot-<key>`。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/ConfigTabs.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import { act } from "react";
import { createRef } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConfigTabs, type ConfigTabsHandle, type ConfigGroup } from "@/components/asset/ConfigTabs";

const twoGroups: ConfigGroup[] = [
  { key: "connection", label: "asset.tabConnection", render: () => <div>conn-pane</div> },
  { key: "advanced", label: "asset.tabAdvanced", optional: true, render: () => <div>adv-pane</div> },
];

describe("ConfigTabs", () => {
  it("single group renders without a tablist", () => {
    render(
      <ConfigTabs groups={[{ key: "only", label: "asset.tabConnection", render: () => <div>only-pane</div> }]} />
    );
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.getByText("only-pane")).toBeInTheDocument();
  });

  it("renders tabs and switches panel on click", async () => {
    render(<ConfigTabs groups={twoGroups} />);
    expect(screen.getByRole("tablist")).toBeInTheDocument();
    expect(screen.getByText("conn-pane")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("config-tab-advanced"));
    expect(screen.getByText("adv-pane")).toBeInTheDocument();
  });

  it("shows a red dot on invalid groups", () => {
    render(<ConfigTabs groups={[twoGroups[0], { ...twoGroups[1], invalid: true }]} />);
    expect(screen.getByTestId("config-tab-dot-advanced")).toBeInTheDocument();
  });

  it("shows a numeric badge", () => {
    render(<ConfigTabs groups={[twoGroups[0], { ...twoGroups[1], badge: 2 }]} />);
    expect(screen.getByText("2")).toBeInTheDocument();
  });

  it("setActive(ref) switches the active tab", () => {
    const ref = createRef<ConfigTabsHandle>();
    render(<ConfigTabs ref={ref} groups={twoGroups} />);
    act(() => ref.current?.setActive("advanced"));
    expect(screen.getByText("adv-pane")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/ConfigTabs.test.tsx`
Expected: FAIL —— 找不到模块 `@/components/asset/ConfigTabs`。

- [ ] **Step 3: 实现 ConfigTabs**

`frontend/src/components/asset/ConfigTabs.tsx`:

```tsx
import { forwardRef, useImperativeHandle, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@opskat/ui";

export interface ConfigGroup {
  /** 稳定标识,用于 focusGroup 跳转。 */
  key: string;
  /** i18n key。 */
  label: string;
  /** true → 标签显示"可选"标(第一个「连接」默认 false)。 */
  optional?: boolean;
  /** 数量徽标(如 Connect 集群数);<=0 或 undefined 不显示。 */
  badge?: number;
  /** 红点:该分组"已启用但必填没填全"。 */
  invalid?: boolean;
  render: () => ReactNode;
}

export interface ConfigTabsHandle {
  setActive: (key: string) => void;
}

interface ConfigTabsProps {
  groups: ConfigGroup[];
}

/** 资产表单类型配置的标签容器:多分组出顶部标签,单分组退化为无标签单面板。 */
export const ConfigTabs = forwardRef<ConfigTabsHandle, ConfigTabsProps>(function ConfigTabs({ groups }, ref) {
  const { t } = useTranslation();
  const [active, setActive] = useState(groups[0]?.key ?? "");

  useImperativeHandle(ref, () => ({ setActive }), []);

  // 单分组:无标签,直接出内容。
  if (groups.length <= 1) {
    return <>{groups[0]?.render()}</>;
  }

  return (
    <Tabs value={active} onValueChange={setActive} className="w-full">
      <TabsList className="flex w-full justify-start overflow-x-auto">
        {groups.map((g) => (
          <TabsTrigger key={g.key} value={g.key} data-testid={`config-tab-${g.key}`} className="gap-1.5">
            {t(g.label)}
            {g.badge !== undefined && g.badge > 0 && (
              <span className="rounded-full bg-primary/10 px-1.5 text-[10px] font-semibold text-primary">
                {g.badge}
              </span>
            )}
            {g.optional && <span className="text-[10px] text-muted-foreground">{t("asset.optional")}</span>}
            {g.invalid && (
              <span
                data-testid={`config-tab-dot-${g.key}`}
                className="h-1.5 w-1.5 rounded-full bg-destructive ring-2 ring-destructive/20"
              />
            )}
          </TabsTrigger>
        ))}
      </TabsList>
      {groups.map((g) => (
        <TabsContent key={g.key} value={g.key} className="mt-3 space-y-3">
          {g.render()}
        </TabsContent>
      ))}
    </Tabs>
  );
});
```

- [ ] **Step 4: 运行测试,确认通过**

Run: `cd frontend && npx vitest run src/__tests__/ConfigTabs.test.tsx`
Expected: PASS(5 个用例全过)。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/ConfigTabs.tsx frontend/src/__tests__/ConfigTabs.test.tsx
git commit -m "✨ 新增 ConfigTabs 表单分组标签原语"
```

---

### Task 4: `DescriptionBar` 折叠备注

**Files:**
- Create: `frontend/src/components/asset/DescriptionBar.tsx`
- Test: `frontend/src/__tests__/DescriptionBar.test.tsx`

**Interfaces:**
- Consumes: `@opskat/ui` `Label/Textarea`;`lucide-react` `Plus`;i18n `asset.addDescription` / `asset.description`。
- Produces: `function DescriptionBar(props: { value: string; onChange: (v: string) => void }): JSX.Element`;testid `description-add` / `description-textarea`。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/DescriptionBar.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DescriptionBar } from "@/components/asset/DescriptionBar";

describe("DescriptionBar", () => {
  it("collapsed when empty, expands on click", async () => {
    render(<DescriptionBar value="" onChange={vi.fn()} />);
    expect(screen.getByTestId("description-add")).toBeInTheDocument();
    expect(screen.queryByTestId("description-textarea")).not.toBeInTheDocument();
    await userEvent.click(screen.getByTestId("description-add"));
    expect(screen.getByTestId("description-textarea")).toBeInTheDocument();
  });

  it("starts expanded when value present", () => {
    render(<DescriptionBar value="hello" onChange={vi.fn()} />);
    expect(screen.getByTestId("description-textarea")).toHaveValue("hello");
  });

  it("forwards edits via onChange", async () => {
    const onChange = vi.fn();
    render(<DescriptionBar value="" onChange={onChange} />);
    await userEvent.click(screen.getByTestId("description-add"));
    await userEvent.type(screen.getByTestId("description-textarea"), "x");
    expect(onChange).toHaveBeenCalledWith("x");
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/DescriptionBar.test.tsx`
Expected: FAIL —— 找不到模块 `@/components/asset/DescriptionBar`。

- [ ] **Step 3: 实现 DescriptionBar**

`frontend/src/components/asset/DescriptionBar.tsx`:

```tsx
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus } from "lucide-react";
import { Label, Textarea } from "@opskat/ui";

interface DescriptionBarProps {
  value: string;
  onChange: (v: string) => void;
}

/** 描述/备注:常态折成一行"添加备注",点击就地展开成文本框;编辑态有内容时直接展开。 */
export function DescriptionBar({ value, onChange }: DescriptionBarProps) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(!!value);

  if (!expanded) {
    return (
      <button
        type="button"
        data-testid="description-add"
        onClick={() => setExpanded(true)}
        className="flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <Plus className="h-3.5 w-3.5" />
        {t("asset.addDescription")}
      </button>
    );
  }

  return (
    <div className="grid gap-2">
      <Label>{t("asset.description")}</Label>
      <Textarea
        autoFocus
        data-testid="description-textarea"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={2}
      />
    </div>
  );
}
```

- [ ] **Step 4: 运行测试,确认通过**

Run: `cd frontend && npx vitest run src/__tests__/DescriptionBar.test.tsx`
Expected: PASS(3 个用例全过)。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/DescriptionBar.tsx frontend/src/__tests__/DescriptionBar.test.tsx
git commit -m "✨ 新增 DescriptionBar 折叠备注组件"
```

---

### Task 5: shell 接入(AssetForm)

**Files:**
- Modify: `frontend/src/components/asset/AssetForm.tsx`
- Test: `frontend/src/__tests__/AssetFormDescription.test.tsx`

**Interfaces:**
- Consumes: `DescriptionBar`(Task 4);`SectionValidity.invalidGroupKey` / `AssetFormHandle.focusGroup`(Task 2);i18n `asset.goToTab`。

- [ ] **Step 1: 写失败测试(描述折叠 + 渲染)**

`frontend/src/__tests__/AssetFormDescription.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetForm } from "@/components/asset/AssetForm";

// 资产 store 与扩展 store 在测试里走真实实现 + 全局 Wails mock(setup.ts)即可渲染创建态。
describe("AssetForm description bar", () => {
  it("renders the collapsible description bar in create mode", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    const add = await screen.findByTestId("description-add");
    expect(add).toBeInTheDocument();
    await userEvent.click(add);
    expect(screen.getByTestId("description-textarea")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/AssetFormDescription.test.tsx`
Expected: FAIL —— 当前 AssetForm 用的是 `Textarea`,无 `description-add` testid。

- [ ] **Step 3: 用 DescriptionBar 替换描述块**

在 `AssetForm.tsx` 顶部 import 区加:

```tsx
import { DescriptionBar } from "@/components/asset/DescriptionBar";
```

把当前 Description 块(`{/* Description */}` 那段 `div.grid.gap-2` 含 `Label` + `Textarea`)整体替换为:

```tsx
{/* Description(折叠成一行,贴近 footer) */}
<DescriptionBar value={description} onChange={setDescription} />
```

若替换后 `Textarea` / `Label` 在文件中不再被使用,删除其 import(`tsc` 会报未用)。

- [ ] **Step 4: footer 增加"前往"跳转**

把 footer 里 `saveDisabledReason` 提示块替换为(在 `<AlertCircle/>` 与文案后追加跳转按钮):

```tsx
{saveDisabledReason && (
  <p className="flex items-center gap-1 text-xs text-muted-foreground">
    <AlertCircle className="h-3.5 w-3.5 shrink-0" />
    {t(saveDisabledReason)}
    {name.trim() && validity.invalidGroupKey && (
      <button
        type="button"
        data-testid="goto-invalid-tab"
        className="underline underline-offset-2"
        onClick={() => sectionRef.current?.focusGroup?.(validity.invalidGroupKey!)}
      >
        {t("asset.goToTab")}
      </button>
    )}
  </p>
)}
```

- [ ] **Step 5: 运行测试,确认通过**

Run: `cd frontend && npx vitest run src/__tests__/AssetFormDescription.test.tsx`
Expected: PASS

- [ ] **Step 6: 类型检查 + 全量测试**

Run: `cd frontend && npx tsc --noEmit && npm test`
Expected: 无新增类型错误;既有测试套件全绿。

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/asset/AssetForm.tsx frontend/src/__tests__/AssetFormDescription.test.tsx
git commit -m "♻️ AssetForm 接入折叠备注与分组跳转"
```

---

### Task 6: 迁移 SSH(2 标签:连接 / SSH 隧道·代理)

**Files:**
- Modify: `frontend/src/components/asset/SSHConfigSection.tsx`
- Test: `frontend/src/__tests__/SSHConfigSection.tabs.test.tsx`

**Interfaces:**
- Consumes: `ConfigTabs` / `ConfigGroup` / `ConfigTabsHandle`(Task 3)。
- 分组 key:`connection`(必填,host)、`tunnel`(可选)。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/SSHConfigSection.tabs.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SSHConfigSection } from "@/components/asset/SSHConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("SSHConfigSection tabs", () => {
  it("splits into connection + tunnel tabs", () => {
    render(<SSHConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
  });

  it("reports connection group invalid until host filled", async () => {
    const onValidity = vi.fn();
    render(<SSHConfigSection ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith(
      expect.objectContaining({ canSave: false, invalidGroupKey: "connection" })
    );
    await userEvent.type(screen.getByTestId("ssh-host-input"), "example.com");
    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: true }));
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/SSHConfigSection.tabs.test.tsx`
Expected: FAIL —— 无 `config-tab-connection`(当前是平铺单块)。

- [ ] **Step 3: 重排为 groups + ConfigTabs**

在 `SSHConfigSection.tsx`:

1. import 区加:`import { useRef } from "react";`(若已 import 则合并)、`import { ConfigTabs, type ConfigGroup, type ConfigTabsHandle } from "@/components/asset/ConfigTabs";`
2. 组件内加:`const tabsRef = useRef<ConfigTabsHandle>(null);`
3. 校验 effect 增加 `invalidGroupKey`:

```tsx
useEffect(() => {
  const ok = !!state.host.trim();
  onValidityChange({
    canTest: ok,
    canSave: ok,
    saveDisabledReason: ok ? "" : "asset.formMissingHost",
    invalidGroupKey: ok ? undefined : "connection",
  });
}, [state.host, onValidityChange]);
```

4. `useImperativeHandle` 的返回对象里加 `focusGroup`:

```tsx
focusGroup: (key) => tabsRef.current?.setActive(key),
```

5. 把 `return ( ... )` 改为基于 groups 渲染。原 JSX 里:
   - 顶层 `<div className="grid gap-3 border rounded-lg p-3">` 包着:`ConnectionMethodFields`(隧道/代理)、Host+Port 块、Username+Auth type 块、password 块、key 配置块。
   - **拆分**:`ConnectionMethodFields` 移入 `tunnel` 分组;其余(Host+Port、Username+Auth、password、key 块)移入 `connection` 分组。删除原 `border rounded-lg` 外层 div(由 TabsContent 提供间距)。

```tsx
const groups: ConfigGroup[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    invalid: !state.host.trim(),
    render: () => (
      <div className="grid gap-3">
        {/* ↓↓ 原 Host+Port 块、Username+Auth type 块、password 块({state.authType === "password"})、
              key 配置块({state.authType === "key"}) 原样搬到这里 ↓↓ */}
      </div>
    ),
  },
  {
    key: "tunnel",
    label: "asset.tabTunnel",
    optional: true,
    render: () => (
      <ConnectionMethodFields
        value={state}
        onChange={patch}
        excludeIds={jumpHostExcludeIds}
        tunnelOptionLabelKey="asset.connectionJumpHost"
        tunnelSelectLabelKey="asset.selectJumpHost"
      />
    ),
  },
];

return <ConfigTabs ref={tabsRef} groups={groups} />;
```

- [ ] **Step 4: 运行测试,确认通过**

Run: `cd frontend && npx vitest run src/__tests__/SSHConfigSection.tabs.test.tsx`
Expected: PASS

- [ ] **Step 5: 类型检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无新增错误。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/asset/SSHConfigSection.tsx frontend/src/__tests__/SSHConfigSection.tabs.test.tsx
git commit -m "♻️ SSH 资产表单标签页化"
```

---

### Task 7: 迁移数据库(连接 / SSH 隧道·代理 / TLS·证书 / 高级)

**Files:**
- Modify: `frontend/src/components/asset/DatabaseConfigSection.tsx`
- Test: `frontend/src/__tests__/DatabaseConfigSection.tabs.test.tsx`

**Interfaces:**
- 分组 key:`connection`(驱动/主机/端口/用户/密码/库名,SQLite 源选择;必填校验沿用原逻辑)、`tunnel`(可选)、`tls`(可选:SSL 模式 + TLS 开关)、`advanced`(可选:参数 / 只读)。

> 先读 `DatabaseConfigSection.tsx` 现有 return,识别各字段块;`buildConfig/buildTestConfig`、`onIconChange`(driver→icon)、SQLite 远程源逻辑保持不变。数据库 TLS 无证书文件路径(仅 PG `sslMode` 下拉 + MySQL/MSSQL TLS 开关),全部归 `tls` 分组。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/DatabaseConfigSection.tabs.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("DatabaseConfigSection tabs", () => {
  it("renders connection / tunnel / tls / advanced tabs", () => {
    render(<DatabaseConfigSection ctx={ctx} onValidityChange={vi.fn()} onIconChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/DatabaseConfigSection.tabs.test.tsx`
Expected: FAIL

- [ ] **Step 3: 重排为 groups + ConfigTabs**

在 `DatabaseConfigSection.tsx`:加 `tabsRef`(同 Task 6 模式),`useImperativeHandle` 加 `focusGroup`,校验 effect 的 `onValidityChange` 调用追加 `invalidGroupKey: <canSave?undefined:"connection">`。return 改为:

```tsx
const groups: ConfigGroup[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    invalid: !canSaveConnection, // 用本 section 现有的"连接必填"布尔(host 或 sqlite path 等)
    render: () => (
      <div className="grid gap-3">
        {/* 驱动选择 + 主机/端口 + 用户 + 密码(PasswordSourceField)+ 库名 + SQLite 源选择 原样搬入 */}
      </div>
    ),
  },
  {
    key: "tunnel",
    label: "asset.tabTunnel",
    optional: true,
    render: () => (/* 原 <ConnectionMethodFields .../> 原样搬入 */ null),
  },
  {
    key: "tls",
    label: "asset.tabTls",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* SSL 模式(PG sslMode 下拉)+ TLS 开关(MySQL/MSSQL)原样搬入 */}
      </div>
    ),
  },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* 参数 / 只读 开关 原样搬入 */}
      </div>
    ),
  },
];
return <ConfigTabs ref={tabsRef} groups={groups} />;
```

> `canSaveConnection` 用该 section 校验 effect 里已算出的连接必填布尔(读现有代码命名,勿新造重复判断)。

- [ ] **Step 4: 运行测试 + 类型检查**

Run: `cd frontend && npx vitest run src/__tests__/DatabaseConfigSection.tabs.test.tsx && npx tsc --noEmit`
Expected: PASS,无新增类型错误。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/DatabaseConfigSection.tsx frontend/src/__tests__/DatabaseConfigSection.tabs.test.tsx
git commit -m "♻️ 数据库资产表单标签页化"
```

---

### Task 8: 迁移 Redis(连接 / SSH 隧道·代理 / TLS·证书 / 高级)

**Files:**
- Modify: `frontend/src/components/asset/RedisConfigSection.tsx`
- Test: `frontend/src/__tests__/RedisConfigSection.tabs.test.tsx`

**Interfaces:**
- 分组 key:`connection`(主机/端口/用户/密码/DB 号)、`tunnel`(可选)、`tls`(可选:TLS 开关 + insecure + server name + CA/cert/key 文件)、`advanced`(可选:命令超时 / scan 页大小 / key 分隔符)。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/RedisConfigSection.tabs.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { RedisConfigSection } from "@/components/asset/RedisConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("RedisConfigSection tabs", () => {
  it("renders connection / tunnel / tls / advanced tabs", () => {
    render(<RedisConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/RedisConfigSection.tabs.test.tsx`
Expected: FAIL

- [ ] **Step 3: 重排为 groups + ConfigTabs**

同 Task 6 模式加 `tabsRef` / `focusGroup`,校验 effect 追加 `invalidGroupKey: ok ? undefined : "connection"`。return:

```tsx
const groups: ConfigGroup[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    invalid: !state.host.trim(),
    render: () => (
      <div className="grid gap-3">
        {/* ConnectionMethodFields 不在这;主机/端口 + 用户 + 密码(PasswordSourceField)+ DB 号 搬入 */}
      </div>
    ),
  },
  {
    key: "tunnel",
    label: "asset.tabTunnel",
    optional: true,
    render: () => (/* 原 <ConnectionMethodFields .../> 搬入 */ null),
  },
  {
    key: "tls",
    label: "asset.tabTls",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* TLS 开关 + 嵌套(insecure / server name / CA / cert / key)搬入 */}
      </div>
    ),
  },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* 命令超时 + scan 页大小 + key 分隔符 搬入 */}
      </div>
    ),
  },
];
return <ConfigTabs ref={tabsRef} groups={groups} />;
```

> 原 Redis 把 DB 号放在主块、TLS/超时/scan/分隔符也在同块;按上面把 DB 号留 `connection`、TLS+证书进 `tls`、超时/scan/分隔符进 `advanced`。

- [ ] **Step 4: 运行测试 + 类型检查**

Run: `cd frontend && npx vitest run src/__tests__/RedisConfigSection.tabs.test.tsx && npx tsc --noEmit`
Expected: PASS,无新增类型错误。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/RedisConfigSection.tsx frontend/src/__tests__/RedisConfigSection.tabs.test.tsx
git commit -m "♻️ Redis 资产表单标签页化"
```

---

### Task 9: 迁移 MongoDB(连接 / SSH 隧道·代理 / TLS·证书 / 高级)

**Files:**
- Modify: `frontend/src/components/asset/MongoDBConfigSection.tsx`
- Test: `frontend/src/__tests__/MongoDBConfigSection.tabs.test.tsx`

**Interfaces:**
- 分组 key:`connection`(手动/URI 切换 + 主机/端口/用户/密码/默认库;校验沿用原 mode 依赖逻辑)、`tunnel`(可选)、`tls`(可选:TLS 开关,无证书文件)、`advanced`(可选:副本集 / authSource)。

> 注意:原 MongoDB 用内层 `Tabs`(manual/uri)做模式切换 —— 这是 `connection` 分组**内部**的子切换,与外层 ConfigTabs 不冲突,原样保留在 `connection.render` 里。副本集 / authSource 当前在 manual 子标签内,本次移到 `advanced` 分组(URI 模式下它们本就无意义);TLS 开关移到 `tls` 分组。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/MongoDBConfigSection.tabs.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MongoDBConfigSection } from "@/components/asset/MongoDBConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("MongoDBConfigSection tabs", () => {
  it("renders connection / tunnel / tls / advanced tabs", () => {
    render(<MongoDBConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/MongoDBConfigSection.tabs.test.tsx`
Expected: FAIL

- [ ] **Step 3: 重排为 groups + ConfigTabs**

加 `tabsRef` / `focusGroup`;校验 effect 追加 `invalidGroupKey: ok ? undefined : "connection"`(`ok` 用原 mode 依赖判断)。return:

```tsx
const groups: ConfigGroup[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    invalid: !(state.connectionMode === "uri" ? state.connectionURI.trim() : state.host.trim()),
    render: () => (
      <div className="grid gap-3">
        {/* 原 <Tabs manual/uri> 子切换(仅含 host/port 与 uri 字段)+ 用户 + 密码 + 默认库 搬入。
              副本集 / authSource 从 manual 子标签里移出到 advanced(见下)。 */}
      </div>
    ),
  },
  {
    key: "tunnel",
    label: "asset.tabTunnel",
    optional: true,
    render: () => (/* 原 <ConnectionMethodFields .../> 搬入 */ null),
  },
  {
    key: "tls",
    label: "asset.tabTls",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* TLS 开关搬入 */}
      </div>
    ),
  },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* 副本集(replicaSet)+ authSource 搬入 */}
      </div>
    ),
  },
];
return <ConfigTabs ref={tabsRef} groups={groups} />;
```

- [ ] **Step 4: 运行测试 + 类型检查**

Run: `cd frontend && npx vitest run src/__tests__/MongoDBConfigSection.tabs.test.tsx && npx tsc --noEmit`
Expected: PASS,无新增类型错误。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/MongoDBConfigSection.tsx frontend/src/__tests__/MongoDBConfigSection.tabs.test.tsx
git commit -m "♻️ MongoDB 资产表单标签页化"
```

---

### Task 10: 迁移 etcd(连接 / SSH 隧道·代理 / TLS·证书)

**Files:**
- Modify: `frontend/src/components/asset/EtcdConfigSection.tsx`
- Test: `frontend/src/__tests__/EtcdConfigSection.tabs.test.tsx`

**Interfaces:**
- 分组 key:`connection`(endpoints + 用户/密码)、`tunnel`(可选)、`tls`(可选:TLS 开关 + insecure + server name + CA/cert/key 文件)。etcd 高级项只有 TLS,移出后**无 `advanced` 标签**。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/EtcdConfigSection.tabs.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { EtcdConfigSection } from "@/components/asset/EtcdConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("EtcdConfigSection tabs", () => {
  it("renders connection / tunnel / tls tabs (no advanced)", () => {
    render(<EtcdConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.queryByTestId("config-tab-advanced")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/EtcdConfigSection.tabs.test.tsx`
Expected: FAIL

- [ ] **Step 3: 重排为 groups + ConfigTabs**

加 `tabsRef` / `focusGroup`;校验 effect 追加 `invalidGroupKey: ok ? undefined : "connection"`(`ok` 用原 endpoints 必填判断)。return:

```tsx
const groups: ConfigGroup[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    invalid: !endpointsOk, // 用原 section 已算的 endpoints 必填布尔
    render: () => (
      <div className="grid gap-3">
        {/* endpoints 文本域 + 用户 + 密码(PasswordSourceField)搬入 */}
      </div>
    ),
  },
  {
    key: "tunnel",
    label: "asset.tabTunnel",
    optional: true,
    render: () => (/* 原 <ConnectionMethodFields .../> 搬入 */ null),
  },
  {
    key: "tls",
    label: "asset.tabTls",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* TLS 开关 + 嵌套(insecure / server name / CA / cert / key)搬入 */}
      </div>
    ),
  },
];
return <ConfigTabs ref={tabsRef} groups={groups} />;
```

- [ ] **Step 4: 运行测试 + 类型检查**

Run: `cd frontend && npx vitest run src/__tests__/EtcdConfigSection.tabs.test.tsx && npx tsc --noEmit`
Expected: PASS,无新增类型错误。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/EtcdConfigSection.tsx frontend/src/__tests__/EtcdConfigSection.tabs.test.tsx
git commit -m "♻️ etcd 资产表单标签页化"
```

---

### Task 11: 迁移 Kafka(连接 / 隧道·代理 / TLS·证书 / Schema Registry / Connect / 高级)+ 反应式伴随校验

**Files:**
- Modify: `frontend/src/components/asset/KafkaConfigSection.tsx`
- Test: `frontend/src/__tests__/KafkaConfigSection.tabs.test.tsx`

**Interfaces:**
- 分组 key:`connection`(Brokers + SASL)、`tunnel`(可选)、`tls`(可选:主 TLS 开关 + insecure + server name + CA/cert/key 文件)、`schema_registry`(可选,启用空 URL → invalid)、`connect`(可选,集群名/URL 缺失 → invalid,badge=集群数)、`advanced`(可选:clientId / 超时 / 预览字节 / 拉取条数)。
- 主 TLS 块从「连接」移到 `tls` 分组;Schema Registry / Connect 各自的伴随 TLS 仍留在它们自己的标签内(per-companion,不并入 `tls`)。
- 把现有"只在 `buildConfig` throw"的伴随校验升级为反应式 `invalid` + 聚合到 `SectionValidity`。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/KafkaConfigSection.tabs.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { KafkaConfigSection } from "@/components/asset/KafkaConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("KafkaConfigSection tabs", () => {
  it("renders the six Kafka tabs", () => {
    render(<KafkaConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-schema_registry")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-connect")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });

  it("flags schema_registry invalid when enabled without URL", async () => {
    const onValidity = vi.fn();
    render(<KafkaConfigSection ctx={ctx} onValidityChange={onValidity} />);
    // 先填 brokers,让 connection 合法
    await userEvent.type(screen.getByPlaceholderText("192.168.100.50:9092"), "b1:9092");
    // 切到 Schema Registry 标签并启用开关
    await userEvent.click(screen.getByTestId("config-tab-schema_registry"));
    await userEvent.click(screen.getByRole("switch"));
    expect(onValidity).toHaveBeenLastCalledWith(
      expect.objectContaining({ canSave: false, invalidGroupKey: "schema_registry" })
    );
    expect(screen.getByTestId("config-tab-dot-schema_registry")).toBeInTheDocument();
  });
});
```

> 说明:`schema_registry.render` 内第一个 `Switch` 是启用开关,`getByRole("switch")` 在该标签面板里取到它(其它标签的开关因 Radix 未挂载而不在 DOM)。若该面板出现多个 switch,改用更精确的 testid(实现时给启用开关加 `data-testid="kafka-sr-enabled"` 并在测试里用它)。

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/KafkaConfigSection.tabs.test.tsx`
Expected: FAIL

- [ ] **Step 3: 加反应式伴随校验 + focusGroup**

在 `KafkaConfigSection.tsx` 组件体内,`useImperativeHandle` 之前,加派生与校验(替换现有那个只看 brokers 的 `useEffect`):

```tsx
const tabsRef = useRef<ConfigTabsHandle>(null); // 顶部 import 增加 useRef 与 ConfigTabs 相关类型

const brokersOk = kafkaBrokers(state.brokersText).length > 0;
const schemaRegistryInvalid = schemaRegistry.enabled && !schemaRegistry.url.trim();
const connectInvalid =
  connectEnabled &&
  (() => {
    const clusters = connectClusters.filter((c) => c.name.trim() || c.url.trim());
    if (clusters.length === 0) return true;
    return clusters.some((c) => !c.name.trim() || !c.url.trim());
  })();

useEffect(() => {
  let invalidGroupKey: string | undefined;
  let saveDisabledReason = "";
  if (!brokersOk) {
    invalidGroupKey = "connection";
    saveDisabledReason = "asset.formMissingKafkaBrokers";
  } else if (schemaRegistryInvalid) {
    invalidGroupKey = "schema_registry";
    saveDisabledReason = "asset.kafkaSchemaRegistryURLRequired";
  } else if (connectInvalid) {
    invalidGroupKey = "connect";
    saveDisabledReason = "asset.kafkaConnectClusterInvalid";
  }
  onValidityChange({
    canTest: brokersOk, // 测试仅连 brokers,伴随项不参与 TestAssetConnection
    canSave: brokersOk && !schemaRegistryInvalid && !connectInvalid,
    saveDisabledReason,
    invalidGroupKey,
  });
}, [brokersOk, schemaRegistryInvalid, connectInvalid, onValidityChange]);
```

`useImperativeHandle` 返回对象里加:`focusGroup: (key) => tabsRef.current?.setActive(key),`(`buildConfig`/`buildTestConfig` 不变,`validateKafkaCompanions` 的 throw 作为防御保留)。

- [ ] **Step 4: 重排 return 为 groups + ConfigTabs**

把现有 return 的各块拆入分组(给 Schema Registry 启用开关加 `data-testid="kafka-sr-enabled"`):

```tsx
const groups: ConfigGroup[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    invalid: !brokersOk,
    render: () => (
      <div className="grid gap-3">
        {/* Brokers 文本域 + SASL 机制 + (saslEnabled 时)用户/密码 搬入(TLS 块移到 tls 分组) */}
      </div>
    ),
  },
  {
    key: "tunnel",
    label: "asset.tabTunnel",
    optional: true,
    render: () => <ConnectionMethodFields value={state} onChange={patch} />,
  },
  {
    key: "tls",
    label: "asset.tabTls",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* 主 TLS 开关 + 嵌套(insecure / server name / CA / cert / key)块搬入 */}
      </div>
    ),
  },
  {
    key: "schema_registry",
    label: "asset.tabSchemaRegistry",
    optional: true,
    invalid: schemaRegistryInvalid,
    render: () => (
      <div className="grid gap-3">
        {/* 原 Schema Registry 块搬入;启用 Switch 加 data-testid="kafka-sr-enabled" */}
      </div>
    ),
  },
  {
    key: "connect",
    label: "asset.tabConnect",
    optional: true,
    badge: connectEnabled ? connectClusters.length : undefined,
    invalid: connectInvalid,
    render: () => (
      <div className="grid gap-3">
        {/* 原 Kafka Connect 块(开关 + 集群列表 + 添加按钮)搬入 */}
      </div>
    ),
  },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    optional: true,
    render: () => (
      <div className="grid gap-3">
        {/* clientId + requestTimeout/messagePreviewBytes/messageFetchLimit 的三列 grid 搬入 */}
      </div>
    ),
  },
];
return <ConfigTabs ref={tabsRef} groups={groups} />;
```

- [ ] **Step 5: 运行测试,确认通过**

Run: `cd frontend && npx vitest run src/__tests__/KafkaConfigSection.tabs.test.tsx`
Expected: PASS

- [ ] **Step 6: 类型检查 + 全量测试**

Run: `cd frontend && npx tsc --noEmit && npm test`
Expected: 无新增类型错误;全套件绿。

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/asset/KafkaConfigSection.tsx frontend/src/__tests__/KafkaConfigSection.tabs.test.tsx
git commit -m "♻️ Kafka 资产表单标签页化与反应式伴随校验"
```

---

### Task 12: 迁移 K8s(连接 / SSH 隧道)

**Files:**
- Modify: `frontend/src/components/asset/K8sConfigSection.tsx`
- Test: `frontend/src/__tests__/K8sConfigSection.tabs.test.tsx`

**Interfaces:**
- 分组 key:`connection`(kubeconfig / namespace / context;新增态 kubeconfig 必填)、`tunnel`(可选:单个 SSH 隧道 `AssetSelect`,无代理)。K8s 不可测(`buildTestConfig` 为 null,无 Test 按钮),无 TLS/高级分组。`buildConfig`/kubeconfig 加密逻辑不变。

- [ ] **Step 1: 写失败测试**

`frontend/src/__tests__/K8sConfigSection.tabs.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { K8sConfigSection } from "@/components/asset/K8sConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("K8sConfigSection tabs", () => {
  it("renders connection + tunnel tabs", () => {
    render(<K8sConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd frontend && npx vitest run src/__tests__/K8sConfigSection.tabs.test.tsx`
Expected: FAIL —— 当前是单 `border rounded-lg` 面板,无 `config-tab-connection`。

- [ ] **Step 3: 重排为 groups + ConfigTabs**

在 `K8sConfigSection.tsx`:import 加 `useRef` 与 `import { ConfigTabs, type ConfigGroup, type ConfigTabsHandle } from "@/components/asset/ConfigTabs";`;组件内加 `const tabsRef = useRef<ConfigTabsHandle>(null);`。

校验 effect 追加 `invalidGroupKey`:

```tsx
useEffect(() => {
  const canSave = !!editAsset || !!state.kubeconfig.trim();
  onValidityChange({
    canTest: false,
    canSave,
    saveDisabledReason: canSave ? "" : "asset.formMissingKubeconfig",
    invalidGroupKey: canSave ? undefined : "connection",
  });
}, [state.kubeconfig, editAsset, onValidityChange]);
```

`useImperativeHandle` 返回对象里加 `focusGroup: (key) => tabsRef.current?.setActive(key),`(`buildConfig`/`buildTestConfig` 不变)。

把 return 的 `<div className="grid gap-3 border rounded-lg p-4">...</div>` 改为(删外层 border,内容拆两组):

```tsx
const groups: ConfigGroup[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    invalid: !editAsset && !state.kubeconfig.trim(),
    render: () => (
      <div className="grid gap-3">
        {/* kubeconfig 显隐块 + namespace 输入 + context 输入 原样搬入 */}
      </div>
    ),
  },
  {
    key: "tunnel",
    label: "asset.tabTunnel",
    optional: true,
    render: () => (
      <div className="grid gap-2">
        {/* 原 SSH 隧道块(Label + <AssetSelect filterType="ssh" .../>)原样搬入 */}
      </div>
    ),
  },
];
return <ConfigTabs ref={tabsRef} groups={groups} />;
```

- [ ] **Step 4: 运行测试,确认通过**

Run: `cd frontend && npx vitest run src/__tests__/K8sConfigSection.tabs.test.tsx`
Expected: PASS

- [ ] **Step 5: 类型检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无新增错误。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/asset/K8sConfigSection.tsx frontend/src/__tests__/K8sConfigSection.tabs.test.tsx
git commit -m "♻️ K8s 资产表单标签页化"
```

---

### Task 13: 全量回归 + lint + 人工验证

**Files:** 无(验证)

- [ ] **Step 1: 全量前端测试**

Run: `cd frontend && npm test`
Expected: 全绿(含既有 `*.config.ts` 序列化测试,证明序列化逻辑未被破坏)。

- [ ] **Step 2: 类型 + lint**

Run: `cd frontend && npx tsc --noEmit && npm run lint`
Expected: 无新增错误/告警(lint 脚本若不存在则跳过并记录)。

- [ ] **Step 3: 人工验证(观察副作用)**

按 [docs/testing-debugging-guide.md](../../testing-debugging-guide.md) 运行应用,逐类型打开"添加资产":
- 单分组类型(串口 / 本地 / 扩展)**无标签条**,单面板;K8s 出 连接 + SSH 隧道 两标签;
- 其余类型出标签,第一个「连接」填完即可"测试连接"+"保存";
- Kafka:启用 Schema Registry 但空 URL → `schema_registry` 标签红点 + 底部"前往"可跳;Connect 标签带集群数徽标;
- 编辑既有资产回填正常,落点在「连接」标签;
- 保存后读 `opskat.db` 确认 config 与改造前一致(无结构变化)。

- [ ] **Step 4: 更新文档状态**

把 `docs/superpowers/specs/2026-06-24-asset-form-tabs-design.md` 顶部 `状态:待评审` 改为 `状态:已实现`。

```bash
git add docs/superpowers/specs/2026-06-24-asset-form-tabs-design.md
git commit -m "📝 标记资产表单标签页设计为已实现"
```

---

## Self-Review

**Spec coverage**(对照 design.md):
- §3 方案 B 顶部标签 → Task 3(ConfigTabs)+ Task 6–11(各 section)。✓
- §3 第一个「连接」标签即可连 → 各 section `connection` 分组 + 校验聚合(Task 6–11)。✓
- §3 独立「SSH 隧道/代理」标签复用 ConnectionMethodFields → `tunnel` 分组(Task 6–11)。✓
- §3 TLS/证书单列标签(方案 β,所有带 TLS 类型)→ `tls` 分组(Task 7 数据库 / Task 8 Redis / Task 9 MongoDB / Task 10 etcd / Task 11 Kafka)。✓
- §3 描述折底部一行 → Task 4 + Task 5。✓
- §3 单分组退化无标签 → Task 3 `groups.length <= 1`;串口/本地/扩展不改(本就平铺=单面板)。✓
- §4.1 ConfigTabs 原语 → Task 3。✓
- §4.2 契约扩展(invalidGroupKey/focusGroup)→ Task 2。✓
- §4.3 section 保留序列化、只重排 + 聚合校验 → Task 6–11。✓
- §5 校验/定位(红点 + 底部点名跳转)→ Task 3(红点)+ Task 5(footer 前往)+ Task 11(Kafka 反应式)。✓
- §6 shell 改动(身份固定、描述、footer,不分支)→ Task 5。✓
- §8 测试策略(ConfigTabs / DescriptionBar / Kafka 聚合 / 序列化回归)→ Task 3/4/11/13。✓
- §10 i18n 键 → Task 1。✓
- §11 K8s 单隧道选择器 → 为一致性拆 连接 + SSH 隧道 两标签 → Task 12。✓

**Placeholder scan:** 各 section 任务的"原样搬入 JSX"是对**已存在**字段块的移动指令(非待写新代码),所有新逻辑(ConfigTabs、DescriptionBar、契约、校验聚合、groups 数组、测试)均给出完整代码。无 TBD/TODO。

**Type consistency:** `ConfigGroup`/`ConfigTabsHandle`(Task 3)、`focusGroup`/`invalidGroupKey`(Task 2)在 Task 5–11 引用一致;分组 key 命名统一(`connection`/`tunnel`/`tls`/`advanced`/`schema_registry`/`connect`);testid 约定 `config-tab-<key>` / `config-tab-dot-<key>` 全程一致。i18n 键 `asset.tabTls`(Task 1)被 Task 7–11 引用一致。
