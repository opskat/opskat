# 资产类型选择器（AssetTypePicker）设计

- 日期：2026-06-04
- 分支：`feat/asset-type-picker`
- 关联：新建资产体验优化（类型多、难识别）

## 1. 背景与问题

`AssetForm` 新建资产时，"类型"是一个**纯文字下拉**（`AssetForm.tsx` 内写死的 `<Select>` + 9 个 `<SelectItem>`，扩展类型从后端 `GetAvailableAssetTypes()` 追加在末尾）。

随着支持的资产类型增多（内置 9 种 + 扩展若干），平铺的纯文字列表：

- **没有图标**，无法一眼区分；
- **没有语义分组**，相近类型散落；
- **没有搜索**，扩展越多越难定位。

同时，前端已存在另一份"带图标 + 分组"的类型清单 `getAssetTypeOptions`（`lib/assetTypes/options.ts`，被资产树类型筛选 `AssetTree` 使用）。**新建表单与筛选器各持一份类型清单，重复且会漂移。**

## 2. 目标 / 非目标

**目标**

- 新建表单的类型选择器改为"带图标的下拉选择框"（参考现有 `IconPicker` 的 Popover 模式）：触发器显示当前类型（图标 + 名称），点开是**搜索 + 分组卡片网格**。
- **统一类型清单**：选择器与资产树筛选共用 `getAssetTypeOptions`，消除重复。
- 保持**单弹窗**，其余表单与流程不变。

**非目标**

- 不改后端、不改 `GetAvailableAssetTypes()` 的 IPC。
- 不改数据库配置区的"引擎下拉"（SQL 引擎仍由 `database` 类型下的 `driver` 选择，类型卡片只到"SQL 数据库"粒度）。
- 不重做资产树筛选 UI（仅因共享 `options` 而文案/图标随之一致）。
- 不改编辑态（编辑已有资产时不展示类型选择，沿用现有 `!editAsset`）。

## 3. 现状结构（最新 main）

- `lib/assetTypes/options.ts`
  - `AssetTypeOption = { value, aliases, label, labelIsI18nKey, icon, group: "builtin" | "extension" }`
  - `getAssetTypeOptions(extensions)`：内置 9 项 + 遍历扩展 manifest 追加；扩展图标用 `getIconComponent(manifest.icon)`。
  - `matchSelectedTypes(...)`：按 `aliases` 过滤资产（筛选器用）。
- `lib/assetTypes/{registry,index,_register,*.ts}`：`AssetTypeDefinition`（连接行为 / 详情卡 / policy），与本设计无关，不改。
- `AssetForm.tsx`：类型选择仍是写死的 `<Select>`（约 1804–1826），`typeLabel` 是一段大三元（约 1666–1690），扩展类型来自后端 `availableTypes`。
- `extensions`：`useExtensionStore((s) => s.extensions)`，`Record<string, { manifest }>`。
- `IconPicker.tsx`：Popover（全宽触发器 → 搜索 + 分组图标网格），可复用其交互范式与 `getIconComponent` / `getIconColor`。

## 4. 设计

### 4.1 扩展 `AssetTypeOption`：增加 `category`

在 `options.ts` 的 `AssetTypeOption` 增加：

```ts
category: "servers" | "databases" | "middleware" | "extension";
```

内置项归类（顺序即展示顺序）：

| category | 成员（value） |
|---|---|
| `servers`（服务器与终端） | `ssh`、`local`、`serial` |
| `databases`（数据库） | `database`、`redis`、`mongodb`、`etcd` |
| `middleware`（中间件与平台） | `kafka`、`k8s` |
| `extension`（扩展） | 所有扩展类型 |

扩展项在 `getAssetTypeOptions` 内追加时 `category: "extension"`。

**图标识别度提升**：把内置项中识别度低的通用 lucide 图标换成品牌图标——`redis` → `RedisIcon`、`mongodb` → `MongodbIcon`、`k8s` → `KubernetesIcon`；`ssh`（保持 `Monitor`）、`database`（保持 `Database`，因其泛指多引擎）、`kafka`/`etcd`（已是品牌图标）、`serial`（`Usb`）、`local`（`SquareTerminal`）维持。

> `group: "builtin" | "extension"` 字段保留不动（筛选器/测试仍用），`category` 是叠加的更细分组。

### 4.2 文案：`nav.database` → "SQL 数据库"

`nav.database` 当前为 "数据库" / "Database"，**仅经 `getAssetTypeOptions` 被使用**（选择器 + 筛选器 + 一处测试），不涉及侧边栏导航。

为消除"数据库分组里又有个'数据库'项"的重名，将其改为：

- zh-CN：`"SQL 数据库"`
- en：`"SQL Database"`

新增 i18n key：

- 分组名 `assetType.group.servers` / `.databases` / `.middleware` / `.extension`
- 搜索占位 `assetType.searchPlaceholder`
- 无结果 `assetType.noResults`

（沿用现有 `nav.*` 作为各项标签；仅 `database` 这一项的标签语义被改名。）

### 4.3 新组件 `AssetTypePicker.tsx`

位置：`frontend/src/components/asset/AssetTypePicker.tsx`。复用 `@opskat/ui` 的 `Popover`/`PopoverTrigger`/`PopoverContent`/`Input`/`Button`，交互范式对齐 `IconPicker`。

**Props**

```ts
interface AssetTypePickerProps {
  value: string;                 // 当前 assetType
  onChange: (type: string) => void;
  disabled?: boolean;
}
```

组件内部自取 `extensions = useExtensionStore((s) => s.extensions)` 并 `getAssetTypeOptions(extensions)`，与 `AssetTree` 同源。

**渲染**

- 触发器：全宽 `Button`（`role="combobox"`），左侧渲染当前选项 `icon`、其后选项 `label`（`labelIsI18nKey ? t(label) : label`），右侧 `ChevronDown`。
- 弹层（`PopoverContent`，宽度对齐 IconPicker 的 `w-[320px]` 量级）：
  - 顶部搜索框（`Input` + `Search` 图标），占位 `assetType.searchPlaceholder`；
  - 滚动区：按 `category` 顺序渲染分组，组标题 `t(assetType.group.<category>)`；每组是 3 列卡片网格，卡片 = 图标在上、名称在下，当前 `value` 高亮（`bg-primary`）；
  - 搜索：按解析后的显示名（label）+ `value` 大小写不敏感匹配；**空组隐藏**；全空时显示 `assetType.noResults`。
- 选中即 `onChange(value)` 并关闭弹层；关闭时清空搜索（同 IconPicker）。

**导出 `getAssetTypeLabel`**（供弹窗标题复用）

```ts
function getAssetTypeLabel(type: string, t, options: AssetTypeOption[]): string
```

按 `options` 找到 `value === type` 的项返回其解析标签；未命中返回 `type` 原值（兼容未知/未加载扩展）。**置于 `options.ts` 导出**（它操作 `AssetTypeOption` 且被 `AssetForm` 复用）。

### 4.4 `AssetForm.tsx` 接线（最小改动）

- 删除写死的 `<Select>…</Select>`（约 1804–1826），换成：
  ```tsx
  <AssetTypePicker value={assetType} onChange={(v) => handleTypeChange(v as AssetType)} />
  ```
  （仍包在现有 `{!editAsset && (...)}` 内。）
- `typeLabel` 大三元 → `getAssetTypeLabel(assetType, t, options)`，弹窗标题 `新建/编辑 {typeLabel}` 不变。
- 移除随之不再需要的本地 `availableTypes` / `resolveExtDisplayName`（其职责被 `getAssetTypeOptions` 覆盖）——**仅当确认无其他引用时删除**，否则保留。
- `handleTypeChange` 不动（已处理端口/用户名/图标等按类型重置）。

### 4.5 图标库扩容（独立 commit）

工作区已有未提交改动：`brand-icons.tsx`（新增 DigitalOcean/SQLServer/Oracle/Cassandra/Neo4j/NATS/Terraform 等数十个品牌图标）+ `IconPicker.tsx`（接入这些图标到分类）。这是 IconPicker 图标库的扩容，**与选择器相邻但独立**。

- 先作为**单独一个 commit** 落地；
- 选择器实现再按需引用其中品牌图标（如 4.1 的 redis/mongodb/k8s，已存在的图标即可，不强依赖本次扩容）。

## 5. 数据流

```
useExtensionStore.extensions
        │
        ▼
getAssetTypeOptions(extensions)  ──►  AssetTree 类型筛选（既有）
        │
        ▼
AssetTypePicker（分组 + 搜索 + 卡片）
        │  onChange(value)
        ▼
AssetForm.handleTypeChange(value)  ──►  按类型渲染配置区（既有逻辑）
```

## 6. 错误处理 / 边界

- 未知或未加载的扩展类型：`getAssetTypeLabel` 回退到原始 `type` 字符串；触发器图标回退为 `getAssetTypeOptions` 给扩展的默认 `Server` 图标（既有行为）。
- 搜索无匹配：显示 `assetType.noResults`，不渲染空组。
- 无扩展时：`extension` 组无成员 → 整组隐藏（空组隐藏规则覆盖）。
- 不在 Go/IPC 边界新增校验（纯前端展示层）。

## 7. 测试（TDD）

- `__tests__/assetTypeOptions.test.ts`（扩展既有）
  - 每个内置项带正确 `category`；分组顺序 servers→databases→middleware；
  - `database` 项标签为 `nav.database`，其值为 "SQL 数据库" / "SQL Database"（或在组件测试断言解析后文案）；
  - 扩展项 `category === "extension"`。
- `__tests__/AssetTypePicker.test.tsx`（新增）
  - 展开后渲染分组标题与各项图标/名称；
  - 搜索过滤：输入 "re" 命中 Redis、空组隐藏；无匹配显示 noResults；
  - 点击某项触发 `onChange(value)`；
  - `getAssetTypeLabel` 对内置/扩展/未知三种返回正确。
- `__tests__/AssetTreeTypeFilter.test.tsx`（预计无需改）
  - 该测试以 i18n **key 原文**断言（如 `getByText("nav.database")`，测试环境 i18n 返回键名而非译文）。`nav.database` 的 key 不变、仅 value 改名，故断言不受影响。若 CI 显示失败再同步。

## 8. 影响面

| 文件 | 改动 |
|---|---|
| `lib/assetTypes/options.ts` | 加 `category` 字段与归类；部分内置图标换品牌图标；（可选）导出 `getAssetTypeLabel` |
| `components/asset/AssetTypePicker.tsx` | 新增组件 |
| `components/asset/AssetForm.tsx` | 替换类型 `<Select>`；`typeLabel` 改查表；清理 `availableTypes`/`resolveExtDisplayName`（确认无引用后） |
| `i18n/{en,zh-CN}/common.json` | `nav.database` 改名；新增 `assetType.group.*` / `searchPlaceholder` / `noResults` |
| `components/asset/{brand-icons,IconPicker}.tsx` | 图标库扩容（独立 commit，已在工作区） |
| `__tests__/*` | 扩 options 测试、新增 picker 测试、同步 filter 测试 |

## 9. 已定决策

- 形态：IconPicker 同款下拉，**单弹窗内**（非两步、非常驻网格）。
- 弹层内容：图标在上、名称在下的卡片网格 + 顶部搜索。
- 分组与归属：servers(ssh/local/serial) / databases(database/redis/mongodb/etcd) / middleware(kafka/k8s) / extension。
- 重名解法：泛指 SQL 项改名 **"SQL 数据库" / "SQL Database"**（B 方案），引擎下拉保持。
- 统一：选择器与资产树筛选共用 `getAssetTypeOptions`（文案/图标一致性随之提升）。
