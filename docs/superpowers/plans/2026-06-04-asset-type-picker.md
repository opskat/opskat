# 资产类型选择器（AssetTypePicker）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `AssetForm` 新建资产的"类型"纯文字下拉，替换为 IconPicker 同款的"图标 + 分组 + 搜索"选择器，并统一到既有 `getAssetTypeOptions` 单一清单。

**Architecture:** 扩展 `lib/assetTypes/options.ts`（加 `category` 与三个纯函数辅助）→ 新建 `AssetTypePicker.tsx`（Popover：触发器 + 搜索 + 分组卡片网格，消费 `getAssetTypeOptions`）→ 在 `AssetForm.tsx` 用它替换写死的 `<Select>` 并复用标签查表。文案改名走 i18n。逻辑集中在纯函数里以便 TDD，组件做轻量冒烟测试。

**Tech Stack:** React 19 + TypeScript、`@opskat/ui`（Radix Popover/Input/Button）、react-i18next、vitest + @testing-library/react、pnpm。

参考规范：`docs/superpowers/specs/2026-06-04-asset-type-picker-design.md`

**约定**
- 前端测试：`cd frontend && pnpm exec vitest run <文件>`（全量：`pnpm test`）。
- 前端 lint：`cd frontend && pnpm exec eslint <文件>`。
- 提交用 gitmoji；本功能无关联 issue，commit 不带 `#编号`。

---

### Task 1: 落地图标库扩容（独立 commit）

工作区已有未提交改动 `brand-icons.tsx`（新增数十个品牌图标）+ `IconPicker.tsx`（接入分类）。这是 IconPicker 图标库扩容，先独立成一个 commit，与选择器解耦。

**Files:**
- Modify（已改，未提交）：`frontend/src/components/asset/brand-icons.tsx`
- Modify（已改，未提交）：`frontend/src/components/asset/IconPicker.tsx`

- [ ] **Step 1: 确认两文件 lint 通过**

Run: `cd frontend && pnpm exec eslint src/components/asset/brand-icons.tsx src/components/asset/IconPicker.tsx`
Expected: 无报错（exit 0）。

- [ ] **Step 2: 确认既有测试未被破坏**

Run: `cd frontend && pnpm test`
Expected: 全部通过（PASS）。

- [ ] **Step 3: 提交**

```bash
git add frontend/src/components/asset/brand-icons.tsx frontend/src/components/asset/IconPicker.tsx
git commit -m "✨ IconPicker 品牌图标库扩容（云厂商/数据库/中间件/系统）"
```

---

### Task 2: `options.ts` 增加 `category`、品牌图标与三个纯函数辅助

给 `AssetTypeOption` 加 `category`，内置项归类；把 redis/mongodb/k8s 换成品牌图标；新增 `buildAssetTypeGroups` / `filterAssetTypeOptions` / `getAssetTypeLabel` 三个纯函数供组件与表单复用。

**Files:**
- Modify: `frontend/src/lib/assetTypes/options.ts`
- Test: `frontend/src/__tests__/assetTypeOptions.test.ts`

- [ ] **Step 1: 先写失败测试（追加到现有文件末尾）**

在 `frontend/src/__tests__/assetTypeOptions.test.ts` 末尾追加：

```ts
import {
  buildAssetTypeGroups,
  filterAssetTypeOptions,
  getAssetTypeLabel,
} from "@/lib/assetTypes/options";

describe("category classification", () => {
  it("assigns the expected category to each built-in option", () => {
    const byValue = Object.fromEntries(getAssetTypeOptions({}).map((o) => [o.value, o.category]));
    expect(byValue).toEqual({
      ssh: "servers",
      local: "servers",
      serial: "servers",
      database: "databases",
      redis: "databases",
      mongodb: "databases",
      etcd: "databases",
      kafka: "middleware",
      k8s: "middleware",
    });
  });

  it("marks extension options as category 'extension'", () => {
    const opts = getAssetTypeOptions({
      ext: {
        manifest: {
          name: "ext",
          version: "1",
          icon: "Server",
          i18n: { displayName: "Foo", description: "" },
          assetTypes: [{ type: "foo", i18n: { name: "Foo" } }],
        },
      },
    } as never);
    expect(opts.find((o) => o.value === "foo")!.category).toBe("extension");
  });
});

describe("buildAssetTypeGroups", () => {
  it("orders groups servers → databases → middleware → extension and drops empty groups", () => {
    const groups = buildAssetTypeGroups(getAssetTypeOptions({}));
    expect(groups.map((g) => g.category)).toEqual(["servers", "databases", "middleware"]);
    expect(groups[0].options.map((o) => o.value)).toEqual(["ssh", "serial", "local"]);
    expect(groups[1].options.map((o) => o.value)).toEqual(["database", "redis", "mongodb", "etcd"]);
  });
});

describe("filterAssetTypeOptions", () => {
  const opts = getAssetTypeOptions({});
  const resolve = (o: { value: string }) => o.value; // label==value for test

  it("returns all when query is blank", () => {
    expect(filterAssetTypeOptions(opts, "  ", resolve).length).toBe(opts.length);
  });

  it("matches by value substring, case-insensitive", () => {
    expect(filterAssetTypeOptions(opts, "RED", resolve).map((o) => o.value)).toEqual(["redis"]);
  });

  it("matches by resolved label", () => {
    const labelResolve = (o: { value: string }) => (o.value === "k8s" ? "Kubernetes" : o.value);
    expect(filterAssetTypeOptions(opts, "kuber", labelResolve).map((o) => o.value)).toEqual(["k8s"]);
  });
});

describe("getAssetTypeLabel", () => {
  const opts = getAssetTypeOptions({});
  const t = (k: string) => `T(${k})`;

  it("resolves i18n-key labels via t", () => {
    expect(getAssetTypeLabel("ssh", t, opts)).toBe("T(nav.ssh)");
  });

  it("returns the raw type for unknown values", () => {
    expect(getAssetTypeLabel("nope", t, opts)).toBe("nope");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && pnpm exec vitest run src/__tests__/assetTypeOptions.test.ts`
Expected: FAIL —— `buildAssetTypeGroups` / `filterAssetTypeOptions` / `getAssetTypeLabel` 未导出，且 `category` 为 undefined。

- [ ] **Step 3: 实现 `options.ts`**

修改 `import` 行（去掉不再使用的 `Cylinder, Leaf, Container`，从 brand-icons 增引 `RedisIcon, MongodbIcon, KubernetesIcon`）：

```ts
import type { ComponentType } from "react";
import { Monitor, Database, Server, Usb, SquareTerminal } from "lucide-react";
import { getIconComponent } from "@/components/asset/IconPicker";
import { KafkaIcon, EtcdIcon, RedisIcon, MongodbIcon, KubernetesIcon } from "@/components/asset/brand-icons";
import type { ExtManifest } from "@/extension/types";
import type { asset_entity } from "../../../wailsjs/go/models";

export type AssetTypeCategory = "servers" | "databases" | "middleware" | "extension";
```

在 `AssetTypeOption` 接口中新增字段（紧跟 `group` 后）：

```ts
  group: "builtin" | "extension";
  /** 语义分组（选择器展示用）。 */
  category: AssetTypeCategory;
```

更新 `BUILTIN_OPTIONS`：为每项补 `category`，并替换 redis/mongodb/k8s 的 `icon`。最终为：

```ts
const BUILTIN_OPTIONS: AssetTypeOption[] = [
  { value: "ssh",      aliases: ["ssh"],                          label: "nav.ssh",      labelIsI18nKey: true, icon: Monitor,        group: "builtin", category: "servers" },
  { value: "database", aliases: ["database", "mysql", "postgresql"], label: "nav.database", labelIsI18nKey: true, icon: Database,        group: "builtin", category: "databases" },
  { value: "redis",    aliases: ["redis"],                        label: "nav.redis",    labelIsI18nKey: true, icon: RedisIcon,      group: "builtin", category: "databases" },
  { value: "mongodb",  aliases: ["mongodb", "mongo"],             label: "nav.mongodb",  labelIsI18nKey: true, icon: MongodbIcon,    group: "builtin", category: "databases" },
  { value: "kafka",    aliases: ["kafka"],                        label: "nav.kafka",    labelIsI18nKey: true, icon: KafkaIcon,      group: "builtin", category: "middleware" },
  { value: "k8s",      aliases: ["k8s", "kubernetes"],            label: "nav.k8s",      labelIsI18nKey: true, icon: KubernetesIcon, group: "builtin", category: "middleware" },
  { value: "serial",   aliases: ["serial", "com", "tty"],         label: "nav.serial",   labelIsI18nKey: true, icon: Usb,            group: "builtin", category: "servers" },
  { value: "local",    aliases: ["local", "shell", "terminal"],   label: "nav.local",    labelIsI18nKey: true, icon: SquareTerminal, group: "builtin", category: "servers" },
  { value: "etcd",     aliases: ["etcd"],                         label: "nav.etcd",     labelIsI18nKey: true, icon: EtcdIcon,       group: "builtin", category: "databases" },
];
```

在 `getAssetTypeOptions` 内 push 扩展项时补 `category: "extension"`：

```ts
      out.push({
        value: at.type,
        aliases: [at.type],
        label: at.i18n?.name ?? at.type,
        labelIsI18nKey: false,
        icon: m.icon ? getIconComponent(m.icon) : Server,
        group: "extension",
        category: "extension",
      });
```

在文件末尾追加三个纯函数：

```ts
export interface AssetTypeGroup {
  category: AssetTypeCategory;
  options: AssetTypeOption[];
}

const CATEGORY_ORDER: AssetTypeCategory[] = ["servers", "databases", "middleware", "extension"];

/** 按固定分类顺序分组，丢弃空组（保持各组内 options 原顺序）。 */
export function buildAssetTypeGroups(options: AssetTypeOption[]): AssetTypeGroup[] {
  return CATEGORY_ORDER.map((category) => ({
    category,
    options: options.filter((o) => o.category === category),
  })).filter((g) => g.options.length > 0);
}

/** 按解析后的显示名或 value 子串过滤（大小写不敏感）；空查询返回全部。 */
export function filterAssetTypeOptions(
  options: AssetTypeOption[],
  query: string,
  resolveLabel: (o: AssetTypeOption) => string
): AssetTypeOption[] {
  const q = query.trim().toLowerCase();
  if (!q) return options;
  return options.filter(
    (o) => resolveLabel(o).toLowerCase().includes(q) || o.value.toLowerCase().includes(q)
  );
}

/** 取某类型的展示标签；未命中返回原始 type（兼容未知/未加载扩展）。 */
export function getAssetTypeLabel(
  type: string,
  t: (key: string) => string,
  options: AssetTypeOption[]
): string {
  const opt = options.find((o) => o.value === type);
  if (!opt) return type;
  return opt.labelIsI18nKey ? t(opt.label) : opt.label;
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd frontend && pnpm exec vitest run src/__tests__/assetTypeOptions.test.ts`
Expected: PASS（含原有用例：values 顺序、aliases、extension 合并仍绿）。

- [ ] **Step 5: lint**

Run: `cd frontend && pnpm exec eslint src/lib/assetTypes/options.ts src/__tests__/assetTypeOptions.test.ts`
Expected: 无报错。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/lib/assetTypes/options.ts frontend/src/__tests__/assetTypeOptions.test.ts
git commit -m "✨ assetType options：category 分组 + 分组/过滤/标签辅助 + 品牌图标"
```

---

### Task 3: i18n —— "SQL 数据库"改名 + 分组/搜索文案

**Files:**
- Modify: `frontend/src/i18n/locales/en/common.json`
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`

- [ ] **Step 1: 改 `nav.database` 文案**

en（`frontend/src/i18n/locales/en/common.json`，`"nav"` 块内）：将
```json
    "database": "Database",
```
改为
```json
    "database": "SQL Database",
```

zh-CN（`frontend/src/i18n/locales/zh-CN/common.json`，`"nav"` 块内，第 25 行）：将
```json
    "database": "数据库",
```
改为
```json
    "database": "SQL 数据库",
```

> 注意：`nav` 块内可能存在多个同名 key（如 `database` 也出现在其它块）。仅改 `"nav"` 对象内、与 `"ssh"/"redis"` 相邻的那一处。

- [ ] **Step 2: 新增 `assetType` 顶层块**

en —— 作为一个新的顶层 key（与 `"nav"` 同级，建议紧跟 `"nav"` 块之后）加入：

```json
  "assetType": {
    "searchPlaceholder": "Search type…",
    "noResults": "No matching type",
    "group": {
      "servers": "Servers & Terminals",
      "databases": "Databases",
      "middleware": "Middleware & Platforms",
      "extension": "Extensions"
    }
  },
```

zh-CN —— 同样位置加入：

```json
  "assetType": {
    "searchPlaceholder": "搜索类型…",
    "noResults": "无匹配类型",
    "group": {
      "servers": "服务器与终端",
      "databases": "数据库",
      "middleware": "中间件与平台",
      "extension": "扩展"
    }
  },
```

- [ ] **Step 3: 校验 JSON 合法**

Run: `cd frontend && node -e "JSON.parse(require('fs').readFileSync('src/i18n/locales/en/common.json','utf8')); JSON.parse(require('fs').readFileSync('src/i18n/locales/zh-CN/common.json','utf8')); console.log('ok')"`
Expected: 输出 `ok`（无解析异常）。

- [ ] **Step 4: 确认既有测试未受影响**

Run: `cd frontend && pnpm exec vitest run src/__tests__/AssetTreeTypeFilter.test.tsx`
Expected: PASS（该测试以 i18n key 原文断言，key 未变）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git commit -m "🌐 类型选择器 i18n：SQL 数据库改名 + 分组/搜索文案"
```

---

### Task 4: 新建 `AssetTypePicker` 组件

IconPicker 同款 Popover：触发器（图标 + 名称 + ▾）→ 搜索 + 分组卡片网格。

**Files:**
- Create: `frontend/src/components/asset/AssetTypePicker.tsx`
- Test: `frontend/src/__tests__/AssetTypePicker.test.tsx`

- [ ] **Step 1: 先写失败测试**

创建 `frontend/src/__tests__/AssetTypePicker.test.tsx`：

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";

// i18n: 返回 key 原文，方便断言
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (k: string) => k }),
}));

// 扩展 store：无扩展
vi.mock("@/extension", () => ({
  useExtensionStore: (sel: (s: { extensions: Record<string, unknown> }) => unknown) =>
    sel({ extensions: {} }),
}));

describe("AssetTypePicker", () => {
  it("shows the current type label on the trigger", () => {
    render(<AssetTypePicker value="redis" onChange={() => {}} />);
    expect(screen.getByRole("combobox")).toHaveTextContent("nav.redis");
  });

  it("opens to grouped options and filters by search", async () => {
    const user = userEvent.setup();
    render(<AssetTypePicker value="ssh" onChange={() => {}} />);
    await user.click(screen.getByRole("combobox"));

    // 分组标题与若干项可见
    expect(screen.getByText("assetType.group.servers")).toBeTruthy();
    expect(screen.getByText("assetType.group.databases")).toBeTruthy();
    expect(screen.getByText("nav.mongodb")).toBeTruthy();

    // 搜索过滤
    await user.type(screen.getByPlaceholderText("assetType.searchPlaceholder"), "mongo");
    expect(screen.queryByText("nav.ssh")).toBeNull();
    expect(screen.getByText("nav.mongodb")).toBeTruthy();
  });

  it("calls onChange with the option value when an item is clicked", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<AssetTypePicker value="ssh" onChange={onChange} />);
    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByText("nav.mongodb"));
    expect(onChange).toHaveBeenCalledWith("mongodb");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && pnpm exec vitest run src/__tests__/AssetTypePicker.test.tsx`
Expected: FAIL —— 模块 `@/components/asset/AssetTypePicker` 不存在。

- [ ] **Step 3: 实现组件**

创建 `frontend/src/components/asset/AssetTypePicker.tsx`：

```tsx
import { useState, useMemo, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Search, ChevronDown } from "lucide-react";
import { cn, Popover, PopoverContent, PopoverTrigger, Input, Button } from "@opskat/ui";
import { useExtensionStore } from "@/extension";
import {
  getAssetTypeOptions,
  buildAssetTypeGroups,
  filterAssetTypeOptions,
  type AssetTypeOption,
} from "@/lib/assetTypes/options";

interface AssetTypePickerProps {
  value: string;
  onChange: (type: string) => void;
  disabled?: boolean;
}

export function AssetTypePicker({ value, onChange, disabled }: AssetTypePickerProps) {
  const { t } = useTranslation();
  const extensions = useExtensionStore((s) => s.extensions);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");

  const options = useMemo(() => getAssetTypeOptions(extensions), [extensions]);
  const resolveLabel = useCallback(
    (o: AssetTypeOption) => (o.labelIsI18nKey ? t(o.label) : o.label),
    [t]
  );

  const selected = options.find((o) => o.value === value);
  const SelectedIcon = selected?.icon;

  const groups = useMemo(
    () => buildAssetTypeGroups(filterAssetTypeOptions(options, search, resolveLabel)),
    [options, search, resolveLabel]
  );

  const handleSelect = (type: string) => {
    onChange(type);
    setOpen(false);
  };

  return (
    <Popover
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) setSearch("");
      }}
    >
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          disabled={disabled}
          className="w-full justify-between font-normal h-9"
        >
          <div className="flex items-center gap-2">
            {SelectedIcon && <SelectedIcon className="h-4 w-4 shrink-0" />}
            <span className="truncate">{selected ? resolveLabel(selected) : value}</span>
          </div>
          <ChevronDown className="ml-auto h-4 w-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[320px] p-0" align="start">
        <div className="p-2 pb-1">
          <div className="relative">
            <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input
              placeholder={t("assetType.searchPlaceholder")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8 h-8 text-sm"
            />
          </div>
        </div>
        <div
          className="max-h-[300px] overflow-y-auto p-2 pt-1 space-y-2"
          onWheel={(e) => e.stopPropagation()}
        >
          {groups.length === 0 && (
            <div className="text-center text-sm text-muted-foreground py-6">
              {t("assetType.noResults")}
            </div>
          )}
          {groups.map((g) => (
            <div key={g.category}>
              <div className="text-[11px] font-medium text-muted-foreground px-0.5 mb-1">
                {t(`assetType.group.${g.category}`)}
              </div>
              <div className="grid grid-cols-3 gap-1">
                {g.options.map((o) => {
                  const Icon = o.icon;
                  const isSelected = o.value === value;
                  return (
                    <button
                      key={o.value}
                      type="button"
                      onClick={() => handleSelect(o.value)}
                      className={cn(
                        "flex flex-col items-center gap-1.5 rounded-md p-2 transition-colors",
                        isSelected
                          ? "bg-primary text-primary-foreground"
                          : "hover:bg-muted text-muted-foreground hover:text-foreground"
                      )}
                    >
                      <Icon className="h-5 w-5" />
                      <span className="text-xs text-center leading-tight">{resolveLabel(o)}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd frontend && pnpm exec vitest run src/__tests__/AssetTypePicker.test.tsx`
Expected: PASS。
（若 Radix Popover 在 jsdom 下打开后内容查询不到，参照既有 `src/__tests__/AssetTreeTypeFilter.test.tsx` 的打开/查询写法对齐——同为 Popover 交互。）

- [ ] **Step 5: lint**

Run: `cd frontend && pnpm exec eslint src/components/asset/AssetTypePicker.tsx src/__tests__/AssetTypePicker.test.tsx`
Expected: 无报错。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/components/asset/AssetTypePicker.tsx frontend/src/__tests__/AssetTypePicker.test.tsx
git commit -m "✨ AssetTypePicker 组件（图标 + 分组 + 搜索）"
```

---

### Task 5: 接入 `AssetForm`，统一类型清单

用 `AssetTypePicker` 替换写死的 `<Select>`，`typeLabel` 改查表，删除 `availableTypes` / `resolveExtDisplayName` / `GetAvailableAssetTypes` 三处死代码。

**Files:**
- Modify: `frontend/src/components/asset/AssetForm.tsx`

- [ ] **Step 1: 增加 import**

在组件 import 区加入：

```ts
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";
import { getAssetTypeOptions, getAssetTypeLabel } from "@/lib/assetTypes/options";
```

并从第 28 行的 import 去掉 `GetAvailableAssetTypes`（保留 `GetDecryptedExtensionConfig`）：

```ts
import { GetDecryptedExtensionConfig } from "../../../wailsjs/go/extension/Extension";
```

- [ ] **Step 2: 删除 `availableTypes` 状态与 `resolveExtDisplayName`**

删除约 325–332 行：

```ts
  const [availableTypes, setAvailableTypes] = useState<
    { type: string; extensionName?: string; displayName: string; sshTunnel?: boolean }[]
  >([]);

  // Extension display name is already translated by the backend
  const resolveExtDisplayName = useCallback((at: { displayName: string }) => {
    return at.displayName;
  }, []);
```

- [ ] **Step 3: 删除加载 `availableTypes` 的 effect 片段**

删除 `open` effect 内（约 483 行）这段：

```ts
      GetAvailableAssetTypes()
        .then((types) => setAvailableTypes(types || []))
        .catch(() => setAvailableTypes([]));
```

- [ ] **Step 4: 增加 options memo 并替换 `typeLabel`**

在 `assetType` state 附近（如 `const { t } = useTranslation();` 之后）加：

```ts
  const extensions = useExtensionStore((s) => s.extensions);
  const assetTypeOptions = useMemo(() => getAssetTypeOptions(extensions), [extensions]);
```

> `useExtensionStore` 已在本文件导入；若缺 `useMemo`，在 `react` 的 import 里补上。

把约 1666–1690 行的 `typeLabel` 大三元整体替换为：

```ts
  const typeLabel = getAssetTypeLabel(assetType, t, assetTypeOptions);
```

- [ ] **Step 5: 替换类型 `<Select>` 为 `AssetTypePicker`**

把约 1801–1828 行的整块（`{!editAsset && ( ... <Select> ... </Select> ... )}`）替换为：

```tsx
            {/* Asset Type */}
            {!editAsset && (
              <div className="grid gap-2">
                <Label>{t("asset.type")}</Label>
                <AssetTypePicker value={assetType} onChange={(v) => handleTypeChange(v as AssetType)} />
              </div>
            )}
```

- [ ] **Step 6: 确认无残留引用**

Run: `cd frontend && grep -n "availableTypes\|resolveExtDisplayName\|GetAvailableAssetTypes" src/components/asset/AssetForm.tsx`
Expected: 无输出（全部清除）。

- [ ] **Step 7: lint + 全量测试**

Run: `cd frontend && pnpm exec eslint src/components/asset/AssetForm.tsx && pnpm test`
Expected: lint 无报错；vitest 全绿。
（若报 `useMemo`/`useCallback` 未使用或未导入，按提示在 `react` import 调整。）

- [ ] **Step 8: 提交**

```bash
git add frontend/src/components/asset/AssetForm.tsx
git commit -m "♻️ AssetForm 类型选择改用 AssetTypePicker，统一类型清单"
```

---

### Task 6: 最终验证

- [ ] **Step 1: 全量前端测试 + lint**

Run: `cd frontend && pnpm test && pnpm exec eslint .`
Expected: 全绿、无 lint 报错。

- [ ] **Step 2: 人工观察（GUI 无法由 agent 点击，按需由用户执行）**

启动应用，打开"新建资产"：
- 类型选择器显示当前类型图标 + 名称 + ▾；
- 点开有"服务器与终端 / 数据库 / 中间件与平台 /（有扩展时）扩展"分组，卡片带品牌图标；
- 数据库组首项显示"SQL 数据库"；
- 搜索框输入 `re` 仅剩 Redis；
- 选中某类型后，下方配置区随之切换，保存正常。

- [ ] **Step 3: 收尾**

确认 `git status` 干净、各 commit 语义清晰；分支 `feat/asset-type-picker` 可发起 PR。

---

## 自查（Self-Review）

- **Spec 覆盖**
  - §4.1 category + 品牌图标 → Task 2 ✓
  - §4.2 SQL 数据库改名 + 新 i18n key → Task 3 ✓
  - §4.3 AssetTypePicker（触发器/搜索/分组网格/空组隐藏/noResults）+ getAssetTypeLabel → Task 4、Task 2 ✓
  - §4.4 AssetForm 接线 + 清理死代码 → Task 5 ✓
  - §4.5 图标库扩容独立 commit → Task 1 ✓
  - §7 测试（options 扩展、picker、filter 同步）→ Task 2/4/3 ✓
- **占位符扫描**：无 TBD/TODO；每个代码步骤含完整代码。
- **类型一致性**：`AssetTypeCategory`、`AssetTypeOption.category`、`AssetTypeGroup`、`buildAssetTypeGroups`/`filterAssetTypeOptions`/`getAssetTypeLabel` 签名在 Task 2 定义，Task 4/5 调用一致；组件 props `value/onChange/disabled` 一致。
