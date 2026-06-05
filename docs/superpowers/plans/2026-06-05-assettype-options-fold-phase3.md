# 资产类型选项注册化(阶段 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `frontend/src/lib/assetTypes/options.ts` 的 `BUILTIN_OPTIONS` 元数据(aliases / label / category / icon)折进各类型的 `AssetTypeDefinition`,`BUILTIN_OPTIONS` 改为从 registry 派生 —— 内置资产类型的展示元数据**一处声明**。

**Architecture:** `AssetTypeDefinition`(`types.ts`)新增 `aliases` / `label` / `category` 三个必填字段(`value`=`type`、`labelIsI18nKey`=true、`group`="builtin" 在派生处恒定,不入 def)。9 个 `assetTypes/*.ts` 各自声明这三项;5 个图标发散的类型(ssh/redis/mongodb/k8s/etcd)统一到选择器用的品牌图标。`options.ts` 删除 `BUILTIN_OPTIONS` 字面量数组,改为运行时从 `getBuiltinTypes()` 映射;`getAssetTypeOptions` 的扩展追加逻辑零改动。详情头(`AssetDetail.tsx:129`)与类型选择器从此共用同一 `def.icon`。

**Tech Stack:** React 19 + TypeScript,Vitest,registry 模式(`registerAssetType` / `_register.ts`)。

**Spec:** `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` 第 3 节。Issue #130。

**决策记录:**
- 图标发散(5/9 类型选择器与详情头不一致)→ **统一用品牌图标**(选择器侧为准):ssh→`Monitor`、redis→`RedisIcon`、mongodb→`MongodbIcon`、k8s→`KubernetesIcon`、etcd→`EtcdIcon`。详情头 redis/mongo/k8s/etcd 从通用 `Database`/`Container` 改为品牌图,ssh 从 `Server` 改为 `Monitor` —— 这是**唯一可见行为变化**,刻意修掉两 registry 的 icon drift。
- `policy_group_entity.PolicyType*` 收敛的未提交工作区改动**不属本阶段**,本计划不触碰。

---

## 现状与不变量(实现前必读)

**两条消费路径(折叠前各自独立):**
- registry `def.icon` → 资产详情头 `AssetDetail.tsx:129` `getAssetType(asset.Type)?.icon ?? Server`。
- options `opt.icon`(经 `getAssetTypeOptions`)→ 类型选择器/过滤器 `AssetTypePicker.tsx`、`AssetTypeFilterButton.tsx`。

**当前图标对照(folding 前):**

| type | 选择器(options.ts) | 详情头(*.ts) | 同？ | 折叠后统一为 |
|---|---|---|---|---|
| ssh | `Monitor` (lucide) | `Server` (lucide) | ✗ | `Monitor` |
| database | `Database` | `Database` | ✓ | `Database` |
| redis | `RedisIcon` (brand) | `Database` | ✗ | `RedisIcon` |
| mongodb | `MongodbIcon` (brand) | `Database` | ✗ | `MongodbIcon` |
| kafka | `KafkaIcon` (brand) | `KafkaIcon` | ✓ | `KafkaIcon` |
| k8s | `KubernetesIcon` (brand) | `Container` | ✗ | `KubernetesIcon` |
| serial | `Usb` | `Usb` | ✓ | `Usb` |
| local | `SquareTerminal` | `SquareTerminal` | ✓ | `SquareTerminal` |
| etcd | `EtcdIcon` (brand) | `Database` | ✗ | `EtcdIcon` |

**每类型的元数据(折进 def):**

| type | label | category | aliases |
|---|---|---|---|
| ssh | `nav.ssh` | `servers` | `["ssh"]` |
| database | `nav.database` | `databases` | `["database","mysql","postgresql"]` |
| redis | `nav.redis` | `databases` | `["redis"]` |
| mongodb | `nav.mongodb` | `databases` | `["mongodb","mongo"]` |
| kafka | `nav.kafka` | `middleware` | `["kafka"]` |
| k8s | `nav.k8s` | `middleware` | `["k8s","kubernetes"]` |
| serial | `nav.serial` | `servers` | `["serial","com","tty"]` |
| local | `nav.local` | `servers` | `["local","shell","terminal"]` |
| etcd | `nav.etcd` | `databases` | `["etcd"]` |

**安全网(必须保持全绿,零改动):**
- `frontend/src/__tests__/assetTypeOptions.test.ts` —— 已锁 options 顺序 / group / database aliases / 各 category / 各 label(`nav.*`)/ 扩展追加。派生后这些值必须逐一不变。
- `frontend/src/lib/assetTypes/__tests__/registry.test.ts` —— 锁 `getBuiltinTypes()` 顺序 + 必填字段存在。
- `frontend/src/__tests__/i18n.test.ts` —— 锁 policy i18n key 覆盖(本阶段不动 policy)。
- 无任何测试断言具体 icon 符号,故图标统一不会撞测试 —— 因此 Task 1 显式补一条守护测试锁住「单一来源」。

**无环校验:** folding 后 `options.ts → index.ts → {*.ts, types.ts}`,且 `index.ts`/`*.ts`/`types.ts` 均不 import `options.ts` → 无循环。`AssetTypeCategory` 从 `options.ts` 下沉到 `types.ts` 正是为避免 `types.ts ↔ options.ts` 成环;`options.ts` re-export 之以保持既有 import 路径不变。

---

## File Structure

- **Modify** `frontend/src/lib/assetTypes/types.ts` — 下沉 `AssetTypeCategory`;`AssetTypeDefinition` 增 `aliases`/`label`/`category`。
- **Modify** `frontend/src/lib/assetTypes/options.ts` — 删 `BUILTIN_OPTIONS` 字面量 + `AssetTypeCategory` 本地定义;改 re-export `AssetTypeCategory`,运行时从 `getBuiltinTypes()` 派生 builtin options。
- **Modify** `frontend/src/lib/assetTypes/{ssh,database,redis,mongodb,kafka,k8s,serial,local,etcd}.ts` — 各加 `aliases`/`label`/`category`;ssh/redis/mongodb/k8s/etcd 改 icon import + 字段。
- **Modify (test)** `frontend/src/__tests__/assetTypeOptions.test.ts` — 新增「单一来源」守护测试(Task 1)。
- **Modify (doc)** `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` — 追加「阶段 3 完成记录」(Task 4)。

---

## Task 1: RED — 锁「icon 单一来源」守护测试

折叠前 5 个类型的 `opt.icon !== def.icon`,此测试必然失败;它是本阶段唯一新增的行为契约(label/category/aliases 已被 `assetTypeOptions.test.ts` 既有用例覆盖)。

**Files:**
- Test: `frontend/src/__tests__/assetTypeOptions.test.ts`

- [ ] **Step 1: 写失败测试**

在 `frontend/src/__tests__/assetTypeOptions.test.ts` 顶部 import 增补 `getAssetType`:

```ts
import { getAssetType } from "@/lib/assetTypes";
```

在文件中新增一个 describe(放在现有 `describe("getAssetTypeOptions", ...)` 之后):

```ts
describe("built-in options derive from the registry (single source)", () => {
  it("each built-in option's icon is the same component as its registry definition's icon", () => {
    const builtins = getAssetTypeOptions({}).filter((o) => o.group === "builtin");
    expect(builtins.length).toBeGreaterThan(0);
    for (const opt of builtins) {
      const def = getAssetType(opt.value);
      expect(def).toBeDefined();
      expect(opt.icon).toBe(def!.icon); // 同一个组件引用，而非两处各自声明
    }
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && npx vitest run src/__tests__/assetTypeOptions.test.ts`
Expected: FAIL —— `built-in options derive from the registry` 这条挂掉(ssh/redis/mongodb/k8s/etcd 的 `opt.icon` 与 `def.icon` 不是同一引用)。其余既有用例仍 PASS。

- [ ] **Step 3: Commit(仅测试)**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add frontend/src/__tests__/assetTypeOptions.test.ts
git commit -m "✅ 锁资产类型 builtin 选项 icon 单一来源守护测试(RED) #130"
```

---

## Task 2: GREEN — 扩展 def 类型 + 9 文件折叠 + options 派生

本任务的中间态不可编译(`types.ts` 一旦加必填字段,9 个 `*.ts` 同时报错),故所有改动一并落地、一次性 tsc + vitest 验证、一次提交。

**Files:**
- Modify: `frontend/src/lib/assetTypes/types.ts`
- Modify: `frontend/src/lib/assetTypes/options.ts`
- Modify: `frontend/src/lib/assetTypes/{ssh,database,redis,mongodb,kafka,k8s,serial,local,etcd}.ts`

- [ ] **Step 1: `types.ts` —— 下沉 category 类型 + 扩展 def**

把 `frontend/src/lib/assetTypes/types.ts` 改为:

```ts
import type { ComponentType } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";

export interface DetailInfoCardProps {
  asset: asset_entity.Asset;
  sshTunnelName: (id?: number) => string | null;
}

export interface PolicyFieldDef {
  key: string;
  labelKey: string;
  placeholderKey: string;
  variant: "allow" | "deny" | "warn";
}

export interface PolicyDefinition {
  policyType: string;
  titleKey: string;
  hintKey: string;
  testPlaceholderKey: string;
  fields: PolicyFieldDef[];
}

/** 语义分组（资产类型选择器展示用）。 */
export type AssetTypeCategory = "servers" | "databases" | "middleware" | "extension";

export interface AssetTypeDefinition {
  type: string;
  icon: ComponentType<{ className?: string; style?: React.CSSProperties }>;
  /** 所有应匹配此类型的 `asset.Type` 值（含历史别名）。 */
  aliases: string[];
  /** 选择器展示标签的 i18n key（默认命名空间），如 `nav.ssh`。 */
  label: string;
  /** 选择器语义分组。 */
  category: AssetTypeCategory;
  canConnect: boolean;
  canConnectInNewTab: boolean;
  connectAction: "terminal" | "query";
  DetailInfoCard: ComponentType<DetailInfoCardProps>;
  policy?: PolicyDefinition;
}
```

- [ ] **Step 2: 9 个 registry 文件 —— 加 aliases/label/category(+ 改 5 个 icon)**

每个 `registerAssetType({...})` 在 `icon` 之后插入 `aliases` / `label` / `category` 三行。下面给出**逐文件**的精确改动(只示新增/变更行,其余保持原样)。

`ssh.ts` —— import 改 icon,字段改 `Server`→`Monitor`,加三行:

```ts
import { Monitor } from "lucide-react";
// ...
registerAssetType({
  type: "ssh",
  icon: Monitor,
  aliases: ["ssh"],
  label: "nav.ssh",
  category: "servers",
  canConnect: true,
  // ...其余不变
```

`database.ts` —— icon 不变(`Database`),加三行:

```ts
  type: "database",
  icon: Database,
  aliases: ["database", "mysql", "postgresql"],
  label: "nav.database",
  category: "databases",
  canConnect: true,
```

`redis.ts` —— import 改为品牌图,字段 `Database`→`RedisIcon`,加三行:

```ts
import { RedisIcon } from "@/components/asset/brand-icons";
// ...
  type: "redis",
  icon: RedisIcon,
  aliases: ["redis"],
  label: "nav.redis",
  category: "databases",
  canConnect: true,
```

`mongodb.ts` —— import 改为品牌图,字段 `Database`→`MongodbIcon`,加三行:

```ts
import { MongodbIcon } from "@/components/asset/brand-icons";
// ...
  type: "mongodb",
  icon: MongodbIcon,
  aliases: ["mongodb", "mongo"],
  label: "nav.mongodb",
  category: "databases",
  canConnect: true,
```

`kafka.ts` —— icon 不变(`KafkaIcon`),加三行:

```ts
  type: "kafka",
  icon: KafkaIcon,
  aliases: ["kafka"],
  label: "nav.kafka",
  category: "middleware",
  canConnect: true,
```

`k8s.ts` —— import 改为品牌图,字段 `Container`→`KubernetesIcon`,加三行:

```ts
import { KubernetesIcon } from "@/components/asset/brand-icons";
// ...
  type: "k8s",
  icon: KubernetesIcon,
  aliases: ["k8s", "kubernetes"],
  label: "nav.k8s",
  category: "middleware",
  canConnect: true,
```

`serial.ts` —— icon 不变(`Usb`),加三行:

```ts
  type: "serial",
  icon: Usb,
  aliases: ["serial", "com", "tty"],
  label: "nav.serial",
  category: "servers",
  canConnect: true,
```

`local.ts` —— icon 不变(`SquareTerminal`),加三行:

```ts
  type: "local",
  icon: SquareTerminal,
  aliases: ["local", "shell", "terminal"],
  label: "nav.local",
  category: "servers",
  canConnect: true,
```

`etcd.ts` —— import 改为品牌图,字段 `Database`→`EtcdIcon`,加三行:

```ts
import { EtcdIcon } from "@/components/asset/brand-icons";
// ...
  type: "etcd",
  icon: EtcdIcon,
  aliases: ["etcd"],
  label: "nav.etcd",
  category: "databases",
  canConnect: true,
```

> 注意 redis/mongodb/k8s/etcd 原 `import { Database/Container } from "lucide-react"` 行需删除(品牌图来自 `@/components/asset/brand-icons`),否则 `no-unused-vars` lint 报错。ssh 原 `import { Server } from "lucide-react"` 改为 `import { Monitor }`。

- [ ] **Step 3: `options.ts` —— 删字面量数组,改派生 + 下沉类型 re-export**

改 `frontend/src/lib/assetTypes/options.ts`:

1. 顶部 import 调整:删 `import { Monitor, Database, Server, Usb, SquareTerminal } from "lucide-react";` 中**仅 options 自用**的图标(`Monitor`/`Database`/`Usb`/`SquareTerminal` 不再需要;`Server` 仍被扩展兜底 `getAssetTypeOptions` 用到 —— 保留 `import { Server } from "lucide-react";`)。删 `import { KafkaIcon, EtcdIcon, RedisIcon, MongodbIcon, KubernetesIcon } from "@/components/asset/brand-icons";`(brand 图标现由各 registry 文件持有)。新增:

```ts
import { getBuiltinTypes } from "./index";
import type { AssetTypeCategory } from "./types";
```

2. 删除本地 `export type AssetTypeCategory = ...;` 定义,改为 re-export(保持 `from "@/lib/assetTypes/options"` 的既有 import 路径不变):

```ts
export type { AssetTypeCategory } from "./types";
```

3. 删除整段 `const BUILTIN_OPTIONS: AssetTypeOption[] = [ ... ];`(原 36–118 行),替换为派生函数:

```ts
/** 内置资产类型选项：从 registry 的 AssetTypeDefinition 派生（单一来源）。 */
function builtinOptions(): AssetTypeOption[] {
  return getBuiltinTypes().map((def) => ({
    value: def.type,
    aliases: def.aliases,
    label: def.label,
    labelIsI18nKey: true,
    icon: def.icon,
    group: "builtin",
    category: def.category,
  }));
}
```

4. `getAssetTypeOptions` 首行由 `const out: AssetTypeOption[] = [...BUILTIN_OPTIONS];` 改为:

```ts
  const out: AssetTypeOption[] = builtinOptions();
```

（`AssetTypeOption` interface、`getAssetTypeOptions` 的扩展追加循环、`matchSelectedTypes`/`buildAssetTypeGroups`/`filterAssetTypeOptions`/`resolveAssetTypeLabel`/`getAssetTypeLabel` 全部保持不变。）

- [ ] **Step 4: tsc 类型检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 0 error。(若某 registry 文件漏加字段,这里会报 `Property 'aliases' is missing`;若漏删 unused icon import,lint 阶段会报 —— tsc 不报 unused 但 Step 6 lint 会。)

- [ ] **Step 5: 跑全量前端测试(含 Task 1 守护 + 既有安全网)**

Run: `cd frontend && npx vitest run src/__tests__/assetTypeOptions.test.ts src/lib/assetTypes/__tests__/registry.test.ts src/__tests__/i18n.test.ts src/__tests__/AssetTreeTypeFilter.test.tsx`
Expected: ALL PASS。Task 1 的「single source」由 RED 转 GREEN;`assetTypeOptions.test.ts` 既有用例(顺序/group/aliases/category/label)逐条仍绿 —— 证明派生值与原字面量等价。

- [ ] **Step 6: lint(确认无 unused import)**

Run: `cd frontend && npx eslint src/lib/assetTypes`
Expected: 0 error(尤其确认 redis/mongodb/k8s/etcd 不再残留 unused 的 `Database`/`Container` import,ssh 不残留 `Server`)。

- [ ] **Step 7: Commit**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add frontend/src/lib/assetTypes/
git commit -m "♻️ 资产类型 BUILTIN_OPTIONS 元数据折进 AssetTypeDefinition,图标统一品牌图 #130"
```

---

## Task 3: 全量回归 + 构建校验

**Files:** 无(仅运行)

- [ ] **Step 1: 前端全量单测**

Run: `cd frontend && npx vitest run`
Expected: 全绿(无新增失败)。

- [ ] **Step 2: 前端类型检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 0 error。

> 后端零改动,无需 `go build`/`go test`/`wails generate`(本阶段不碰 binding)。

---

## Task 4: 阶段 3 完成记录(文档)

**Files:** Modify `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md`

- [ ] **Step 1: 追加完成记录**

在该文件末尾(阶段 2b 完成记录之后)追加:

```markdown
## 阶段 3 完成记录(2026-06-05)

计划见 `docs/superpowers/plans/2026-06-05-assettype-options-fold-phase3.md`,与前阶段同分支累加。

- **做了什么**:`AssetTypeDefinition` 增 `aliases`/`label`/`category` 三个必填字段(`AssetTypeCategory` 从 `options.ts` 下沉到 `types.ts` 避免成环,`options.ts` re-export 保持 import 路径);9 个 `assetTypes/*.ts` 各自声明这三项;`options.ts` 删除 `BUILTIN_OPTIONS` 字面量数组,改运行时从 `getBuiltinTypes()` 派生(`value`=type、`labelIsI18nKey`=true、`group`="builtin" 派生处恒定),`getAssetTypeOptions` 扩展追加逻辑零改动。**一处声明,而非两处。**
- **关闭了哪条备忘**:阶段 1a 备忘的「type→kind 三处重复」之前端侧 —— 前端各 `assetTypes/*.ts` 的展示元数据(label/aliases/category)与 `options.ts` 不再两处维护。
- **唯一可见行为变化(图标统一)**:折叠前类型选择器(options.ts)与详情头(registry)对 5 个类型用了不同 icon。统一到选择器侧的品牌图后,资产详情头 `AssetDetail.tsx:129` 的 ssh(`Server`→`Monitor`)、redis/mongodb/etcd(`Database`→品牌图)、k8s(`Container`→`KubernetesIcon`)改显品牌图 —— 修掉两 registry 的 icon drift。其余(database/kafka/serial/local)本就一致。
- **行为保持(选项侧)**:`getAssetTypeOptions` 对全部既有输入(顺序/group/database aliases/各 category/各 `nav.*` label/扩展追加)结果不变 —— 由 `assetTypeOptions.test.ts` 既有用例逐条锁定,派生后全绿。新增 `assetTypeOptions.test.ts`「single source」守护测试(builtin 选项 icon === registry def icon)。
- **验证**:`vitest run`(全绿)、`tsc --noEmit`(0)、`eslint src/lib/assetTypes`(0)。后端零改动。
- **仍留给后续**:阶段 4(AssetForm 组件注册化:`parseConfig`/`buildConfig`/`testConnection`/`validateForTest` + 通用壳)、阶段 5(`AssetTree.tsx` ssh 文件管理硬编码 → action 注册)、阶段 6(skill + 文档)。
```

- [ ] **Step 2: Commit**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md
git commit -m "📝 记录阶段 3 完成(BUILTIN_OPTIONS 折进 def + 图标统一) #130"
```

---

## Self-Review

- **Spec 覆盖**:设计 §3 要求「`options.ts:36-118` 的 `BUILTIN_OPTIONS` 元数据折进 `AssetTypeDefinition`;`BUILTIN_OPTIONS` 改为从 registry 派生;`getAssetTypeOptions` 不变;一处声明」→ Task 2 全覆盖。图标 drift 是 §3 折叠暴露出的子决策,已在决策记录与 Task 2 固定。
- **Placeholder 扫描**:无 TODO/TBD;每个代码步骤给出实际代码与精确文件。
- **类型一致**:`AssetTypeCategory` 全程同名,定义于 `types.ts`、re-export 自 `options.ts`;`builtinOptions()` 返回 `AssetTypeOption[]`,字段与 `AssetTypeOption` interface 逐一对应(value/aliases/label/labelIsI18nKey/icon/group/category)。
- **未覆盖项**:`policy_group_entity.PolicyType*` 收敛(工作区未提交改动)显式排除,不在本计划。
