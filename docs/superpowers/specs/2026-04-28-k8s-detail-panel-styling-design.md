# K8s 资源详情面板样式重构设计

## 目标

统一 K8s 集群页面中所有资源详情面板（Pod、Deployment、Service、ConfigMap、Secret，以及 Overview、Node、Namespace）的视觉风格，使其与 OpsKat 整体 UI 规范（`AssetDetail`、`RedisDetailInfoCard` 等）保持一致，并提取可复用组件以降低后续维护成本。

## 范围

- `frontend/src/components/k8s/K8sClusterPage.tsx` 中所有资源详情渲染逻辑
- 涉及面板：Overview、Node、Namespace、ns-res、Pod、Service、ConfigMap、Secret
- 不修改侧边栏树结构、不修改 tab 系统、不修改 Wails 绑定

## 现有问题

| 元素 | 整体规范（AssetDetail / InfoCards） | K8s 页面现状 |
|------|-------------------------------------|--------------|
| 卡片内边距 | `p-4` | `p-6` |
| 区块间距 | `space-y-4` | `space-y-6` |
| 页面边距 | `p-4` | `p-6` |
| 元数据展示 | `InfoItem`（label + value，无背景色块） | `rounded-lg bg-muted/50 p-3` 大色块 |
| Section 标题 | `text-xs font-semibold uppercase tracking-wider text-muted-foreground` | `text-sm font-semibold` |
| 重复代码 | 无 | 每个面板重复手写 `rounded-xl border bg-card p-6` 及内部结构 |

`K8sClusterPage.tsx` 当前 2107 行，详情面板 JSX 占约 1100 行，大量重复模式。

## 设计方案

### 样式基线

所有详情面板统一外层容器：
```tsx
<div className="max-w-5xl mx-auto p-4 space-y-4">
```

Section 标题统一为：
```
text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3
```

元数据统一使用项目已有 `InfoItem` 组件，不再使用 `bg-muted/50` 大色块。

### 新增共享组件（`frontend/src/components/k8s/`）

#### 1. `K8sSectionCard`
通用区块卡片。

Props:
- `title?: string`
- `icon?: LucideIcon`
- `children: ReactNode`
- `className?: string`

样式: `rounded-xl border bg-card p-4`

#### 2. `K8sResourceHeader`
资源详情头部（名称 + 状态）。

Props:
- `name: string`
- `subtitle?: string`
- `status?: { text: string; variant: "success" | "warning" | "error" | "info" | "neutral" }`

样式:
- 名称: `font-mono text-sm font-medium`
- 副标题: `text-xs text-muted-foreground mt-0.5`
- 状态 badge: `text-xs px-2 py-0.5 rounded-full font-medium`

#### 3. `K8sMetadataGrid`
元数据网格。

Props:
- `items: { label: string; value: string; mono?: boolean }[]`
- `className?: string`

实现: 复用 `InfoItem`，网格 `grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-4`

#### 4. `K8sTableSection`
带标题的表格区块。

Props:
- `title: string`
- `columns: { key: string; label: ReactNode; className?: string }[]`
- `data: T[]`
- `renderRow: (item: T, index: number) => ReactNode`
- `emptyText?: string`

样式:
- 基于 `K8sSectionCard`
- 表头: `border-b text-xs text-muted-foreground font-medium py-2`
- 行: `border-b last:border-0 py-2`

#### 5. `K8sConditionList`
条件列表（Pod Conditions）。

Props:
- `conditions: { type: string; status: string; reason?: string; message?: string }[]`

样式:
- 两列网格 `grid grid-cols-1 sm:grid-cols-2 gap-3`
- 每项: `rounded-lg border p-3`，内部 flex between + status badge
- reason/message: `text-xs text-muted-foreground`

#### 6. `K8sTagList`
标签/注解展示。

Props:
- `tags: Record<string, string>`

样式:
- `flex flex-wrap gap-2`
- 标签: `inline-flex items-center rounded-md border bg-muted/50 px-2 py-0.5 text-xs font-mono`

#### 7. `K8sCodeBlock`
YAML / 数据展示。

Props:
- `code: string`
- `maxHeight?: string`（默认 `max-h-96`）

样式: `bg-muted/50 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap overflow-y-auto`

#### 8. `K8sLogsPanel`
Pod 日志面板。

Props:
- `containers: string[]`
- `namespace: string`
- `podName: string`
- 以及现有所有日志控制回调和状态

结构:
- 外层 `K8sSectionCard`
- 标题行: 容器选择器 + tail 输入框 + 开始/停止按钮
- 日志区: `bg-black rounded-lg p-3 text-xs font-mono max-h-96 overflow-y-auto`

### 辅助工具

#### `frontend/src/components/k8s/utils.ts`

```ts
export function getK8sStatusColor(
  status: string
): "success" | "warning" | "error" | "info" | "neutral";

export function getContainerStateColor(
  state: string
): "success" | "warning" | "error" | "neutral";
```

替代各面板中重复的内联颜色逻辑。

### 各面板组件映射

| 面板 | 结构 |
|------|------|
| Overview | `K8sSectionCard`(统计: `InfoItem` ×3) + `K8sSectionCard`(Nodes: 卡片列表) + `K8sSectionCard`(Namespaces: `K8sTagList`) |
| Node | `K8sResourceHeader` + `K8sMetadataGrid` |
| Namespace | `K8sResourceHeader` + `K8sMetadataGrid` |
| ns-res | `K8sSectionCard`(资源类型图标 + 名称 + namespace badge + 数量统计) |
| Pod | `K8sResourceHeader` + `K8sMetadataGrid` + `K8sTableSection`(containers) + `K8sConditionList` + `K8sTableSection`(events) + `K8sTagList`(labels) + `K8sCodeBlock`(YAML) + `K8sLogsPanel` |
| Service | `K8sResourceHeader` + `K8sMetadataGrid` + `K8sTableSection`(ports) |
| ConfigMap | `K8sResourceHeader` + `K8sMetadataGrid` + `K8sSectionCard`(Data entries，内部 `K8sCodeBlock`) |
| Secret | `K8sResourceHeader` + `K8sMetadataGrid` + `K8sSectionCard`(Data entries，内部 `K8sCodeBlock` + decode) |

### 预期效果

- `K8sClusterPage.tsx` 行数从 ~2100 降至 ~1200-1400
- 所有 K8s 详情面板与 `AssetDetail`、`RedisPanel` 等页面风格一致
- 新增 K8s 资源类型时直接复用 `K8sResourceHeader` + `K8sMetadataGrid` + `K8sSectionCard`

## 风险

- `K8sClusterPage.tsx` 较大，重构过程中需确保类型引用不丢失
- 日志面板涉及较多交互状态，提取为 `K8sLogsPanel` 时需保持回调链完整
- 表格渲染使用泛型，TypeScript 类型需正确推导

## 测试策略

- 前端 lint 和 vitest 通过
- 手动验证所有 8 个面板的渲染正确性（Overview / Node / Namespace / ns-res / Pod / Service / ConfigMap / Secret）
