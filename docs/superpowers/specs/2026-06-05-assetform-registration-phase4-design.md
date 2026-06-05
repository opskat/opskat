# AssetForm 组件注册化(阶段 4)— 设计

- **Issue**: #130 [Feature] 资产类型 / policy 全链路注册化重构 — 阶段 4
- **日期**: 2026-06-05
- **状态**: 设计已敲定,待写实现计划
- **上游设计**: `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` 第 4 节
- **PR**: 累加到 #144(同分支 `refactor/asset-type-decoupling-130`)

## 背景与问题

`frontend/src/components/asset/AssetForm.tsx` 是资产类型横向耦合的最大集中点:**2183 行,52 处 `assetType === "x"` 分支**,约 122 个 per-type `useState` 字段全部住在父组件。9 个 `*ConfigSection.tsx` 组件**已抽成独立文件**,但只是"哑组件"——接收 6–46 个受控 props(state + setter 从父层逐个 drill 下去)。per-type 逻辑散落在:

| 关注点 | 当前位置(AssetForm.tsx) | 形态 |
|---|---|---|
| per-type 表单 state | 325–446 | ~122 个 `useState`,~50% 类型独占 |
| 编辑态回填 | 489–517 分发 → 540–926 各 `loadXxxConfig` | if-else on `editType` |
| 类型切换重置 | 934–951 `handleTypeChange` | 9 类型清字段 |
| 保存序列化 | 1385–1616 `handleSubmit` | 10 个 if 分支建 config JSON |
| 连接测试 | 1015–1253 七个 `handleTest*Connection` + 1712–1725 三元链 | per-type 建 config 对象 |
| 测试可用 | 1658–1665 `isTestableAssetType` + 1673–1687 `isTestConnectionDisabled` | per-type 链 |
| 渲染 | 1829–2145 九个 `assetType==="x" && <XConfigSection/>` | 6–46 props each |
| 默认值 | 219–244 `DEFAULT_PORTS`/`DEFAULT_ICONS` | per-type |

加一个新资产类型今天要在这一个文件里改 ~7 处。目标:把 AssetForm 收敛成**类型无关的通用壳**,per-type 知识全部下沉到各自的 ConfigSection,新增类型时只改它自己的组件 + 一行注册。

## 目标 / 非目标

**目标**:AssetForm **零 `assetType === "x"`**;每个资产类型的 state / 回填 / 序列化 / 测试 config / 校验 / 渲染**集中在它自己的 ConfigSection 组件**;新增类型 = 1 个组件文件 + `AssetTypeDefinition` 一行(OCP)。**行为完全保持**(序列化 JSON、测试调用、校验、提示逐一不变)。

**非目标**:
- 不改表单视觉 / 交互(纯重构)。不做 schema 驱动表单(沿用 bespoke ConfigSection)。
- 不动共享编排(凭据加密、testId 竞态、取消、toast)的语义——只把它们从 per-type 分支里提出来共享。
- 不顺手做无关重构 / 改名扫荡(AGENTS.md in-scope 约束)。
- 扩展类型(`ExtensionConfigForm`)保持现有独立路径,不强行套进 ConfigSection 契约(其 config 由 manifest schema 驱动,机制不同)。

## 决策摘要(brainstorm 已敲定)

| 维度 | 决策 |
|---|---|
| 状态所有权 | **A:section 自持 state**,经 `useImperativeHandle` 暴露 ref handle;父壳持 0 个 per-type state |
| 迁移方式 | **增量 vertical-slice**:contract + 通用壳 → 先迁最简单类型验证闭环 → 一类型一 commit → 末 commit 删遗留 switch |
| 过渡期 | 壳内 `def.ConfigSection ? 通用路径 : 遗留 switch`;双路径只在分支中间 commit,**不进 main**(末 commit 删除) |
| PR | 累加到 #144,同分支 |
| 行为保持验证 | **golden config-JSON characterization**:迁移前抓现有 `handleSubmit` 输出,迁移后断言 `buildConfig()` 产出字节一致 |

## 目标架构

### 第 1 节 · 契约(`frontend/src/lib/assetTypes/formContract.ts`,新建)

```ts
import type { ForwardRefExoticComponent, RefAttributes } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";

/** 父壳交给每个 section 的共享横切助手 + 数据。 */
export interface AssetFormContext {
  isEdit: boolean;
  /** 包装现有 encryptPasswordValue(走后端);明文→密文。 */
  encryptPassword: (plain: string) => Promise<string>;
  /** 托管凭据 / 密钥 / ssh 隧道选项,供 section 复用既有共享原语(PasswordSourceField、隧道选择器)。 */
  managedPasswords: ManagedCredential[];
  managedKeys: ManagedKey[];
  sshTunnelOptions: TunnelOption[];
}

/** 保存序列化结果。 */
export interface AssetConfigBuildResult {
  configJSON: string;
  sshTunnelId: number; // ssh_asset_id 关联(0 = 无)
  icon: string;        // 用户未选时的默认图标
}

/** 测试连接所需的最小信息(壳据此调 TestAssetConnection)。 */
export interface AssetTestConfig {
  assetType: string;
  configJSON: string;
  password: string; // serial 传 ""
}

/** 每个 ConfigSection 经 useImperativeHandle 暴露的命令式句柄。 */
export interface AssetFormHandle {
  buildConfig: (ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  /** 仅可测类型实现;不可测类型返回 null。 */
  buildTestConfig: ((ctx: AssetFormContext) => Promise<AssetTestConfig>) | null;
}

export interface ConfigSectionProps {
  /** 编辑态回填来源;创建态为 undefined。 */
  editAsset?: asset_entity.Asset;
  ctx: AssetFormContext;
  /** state 变化时上报,驱动壳的 Test/Save 按钮启用态(反应式)。 */
  onValidityChange: (v: { canTest: boolean; canSave: boolean }) => void;
}

export type ConfigSectionComponent = ForwardRefExoticComponent<
  ConfigSectionProps & RefAttributes<AssetFormHandle>
>;
```

> `AssetFormContext` 内 `ManagedCredential`/`ManagedKey`/`TunnelOption` 沿用 AssetForm 现有类型(实现计划核实精确定义并 import,不新造)。

`AssetTypeDefinition`(`types.ts`)增补:
```ts
/** 资产表单的 per-type config 区(注册化表单)。缺省 = 走遗留/扩展路径。 */
ConfigSection?: ConfigSectionComponent;
/** 是否支持"测试连接"(替代 isTestableAssetType 链)。 */
testable?: boolean;
```

每个 `XxxConfigSection` 重写为 `forwardRef<AssetFormHandle, ConfigSectionProps>`:自持全部字段 `useState`、mount 时若 `editAsset` 则回填(可异步)、每次 state 变化 `onValidityChange(...)`、`useImperativeHandle` 暴露 `buildConfig`/`buildTestConfig`。

### 第 2 节 · 通用壳(AssetForm 瘦身后)

共享 chrome(类型选择器 / 名称 / 图标 / 分组 / 描述)+ 核心:
```tsx
const def = getAssetType(assetType);
// 通用路径
<def.ConfigSection
  key={assetType}            // 类型切换→remount→各 section 自带默认值的全新 state(替代 9 个 reset)
  ref={sectionRef}
  editAsset={editAsset}      // 编辑态回填(替代 9 个 loadXxxConfig 分发)
  ctx={ctx}
  onValidityChange={setValidity}
/>
```
- **保存**:`const r = await sectionRef.current.buildConfig(ctx)` → 壳做共享 加密(已在 buildConfig 内经 ctx)/持久化/toast。
- **测试**:`const t = await sectionRef.current.buildTestConfig?.(ctx)` → 壳做共享 `TestAssetConnection(testId, t.assetType, t.configJSON, t.password)` + testId 竞态 + 取消 + toast(全部留壳,DRY)。
- **按钮启用**:读反应式 `validity`(来自 `onValidityChange`),不再有 per-type 链;"测试"按钮可见性 = `def.testable`。
- **零 `assetType === "x"`**。砍掉 ~122 useState + 9 load + 9 reset + 10 build + 7 test 分支 + 2 条三元链。

### 第 3 节 · 共享 `ctx`

凭据加密、托管凭据/密钥列表、ssh 隧道选项、`isEdit` 留**壳持有**,经 `ctx` 下发——section 复用既有共享原语(`PasswordSourceField`、隧道选择器),不重新派生。共享 编排(加密调用、testId 竞态、取消、toast)留壳;只把 per-type config 形状下沉。`buildConfig`/`buildTestConfig` 接受 `ctx` 以便在 section 内完成加密(异步)。

### 第 4 节 · 迁移顺序(增量,过渡双路径,全在 #144)

`local`(验证闭环:3 字段、不可测、无隧道)→ `serial`(简单 + 可测)→ `etcd` → `redis` → `mongodb` → `database` → `k8s` → `kafka` → `ssh`(最复杂,压轴)。

壳过渡期:`def.ConfigSection ? 通用路径 : 遗留 switch`。每个 commit 迁移一个类型 + 删它的遗留分支(load/reset/build/test/render/state);**末 commit 删除遗留 switch + 死字段 + `DEFAULT_PORTS`/`DEFAULT_ICONS` 等只剩壳用的残留**。双路径只活在分支中间 commit,不进 main。扩展类型路径保留(非 ConfigSection 契约)。

### 第 5 节 · 测试(行为保持证明)

- **golden config-JSON characterization(回归网)**:迁移某类型前,先对该类型代表性输入(创建态 + 编辑态)抓 **当前** `handleSubmit` 产出的 config JSON 与 `handleTest*` 的测试 config,落为 golden;迁移后断言新 `section.buildConfig()`/`buildTestConfig()` 产出**字节一致**。`sshTunnelId`/`icon` 同样锁定。
- **per-section 单测**:render section → 模拟输入 → 断言 ref 输出 + `onValidityChange`(创建默认值 + 编辑回填 round-trip)。
- **壳单测**:类型无关 render + 编排(save/test/cancel,用 fake section + fake handle),锁"壳调 buildConfig 后走共享加密/持久化"与"测试走 TestAssetConnection + testId 竞态"。
- 每个 commit 后全量 `vitest run` + `tsc --noEmit` + `eslint` 绿;末 commit 后确认无 `assetType ===` 残留(grep 计数归零或仅剩扩展路径)。

## 风险 / 待实现计划细化

- **SSH 最复杂**(connectionType / key source / managed keys / local keys / proxy / tunnel,46 props),压轴迁移;实现计划需核实其与 `PasswordSourceField`、密钥扫描、proxy 子表单的复用边界。
- **异步回填**:部分 `loadXxxConfig` 调后端(解析托管凭据 / 解密)。section 回填用 mount `useEffect` 异步,需处理"回填完成前 onValidityChange 的初值"与竞态(editAsset 切换)。
- **buildConfig 异步**:凭据加密走后端;`buildConfig` 返回 Promise,壳 await。错误处理沿用现有(加密失败 → toast.error,不静默)。
- **golden 抓取**:实现计划需先写一个临时 harness 或直接从现有 `handleSubmit`/`handleTest*` 抽纯函数抓 golden;优先"先抽纯函数 → golden 锁定 → 再搬进 section",避免迁移与锁定同 commit 导致无 RED。
- **扩展类型**:`ExtensionConfigForm` 不套契约;壳保留"`def.ConfigSection` 缺省 → 扩展/遗留路径"分叉,末 commit 后该分叉退化为"内置走 ConfigSection、扩展走 ExtensionConfigForm",非 type switch。
- **默认图标流**:图标选择器是共享 chrome(壳持有 icon state),但默认图标随类型/数据库 driver 变(现 `DEFAULT_ICONS` 含 mysql/postgresql/sqlite 等)。`buildConfig` 返回 `icon`(section 据自身 driver 等 state 算默认),壳保存时取 `用户所选 || result.icon`;表单内的"实时默认图标预览"(现 `handleTypeChange` 设 icon)需 section 经回调上报默认 icon-key,实现计划定其形态(候选:`onValidityChange` 扩展携带 `defaultIcon`,或独立 `onDefaultIconChange`)。
- **决定性验收(承上游设计)**:重构后加一次性 throwaway 类型(如 `telnet`),只动 1 个 ConfigSection + 1 行注册,确认端到端全通、AssetForm 零改动——证明 seam 真断开;阶段 6 skill 照此写。

## 验证策略

- 行为保持:golden JSON + 全量 vitest/tsc/eslint 每 commit 绿。
- 观测验证(后端不变,但端到端可经 opsctl/日志):创建/编辑各类型资产、点测试连接、保存,确认 DB 落库 config 与迁移前一致(抽查 `opskat.db` assets.config)。

## 阶段 4a 完成记录(2026-06-05)

计划见 `docs/superpowers/plans/2026-06-05-assetform-registration-phase4a.md`。子 agent 逐 Task 驱动(implementer + spec review + 质量 review),累加到 #144 分支。落地 4 个 commit:

- `9e046f7b` — ref 契约 `formContract.ts`(`AssetFormContext`/`AssetConfigBuildResult`/`AssetTestConfig`/`AssetFormHandle`/`ConfigSectionProps`/`ConfigSectionComponent`)+ `AssetTypeDefinition.{ConfigSection?,testable?}`。
- `fe73a9f1` — `local` 配置纯函数 `buildLocalConfig`/`parseLocalConfig`/`LOCAL_DEFAULTS` + golden(锁旧 `handleSubmit`/`loadLocalConfig` 字节一致)。
- `08f06184` — 迁移 `local`:`LocalConfigSection` 重写为 `forwardRef` 自持 state(`useImperativeHandle` 暴露 `buildConfig`/`buildTestConfig:null`,`onValidityChange` 上报);壳加通用路径 `def?.ConfigSection ? 通用 : 遗留 switch` + `persistAsset` 抽取 + 编辑回填 section 自填守卫;删全部 local 遗留(state/load/reset/save 分支/render/imports)。
- `188c273c` — 纯函数拆到 sibling `LocalConfigSection.config.ts`(消除 `react-refresh/only-export-components` 警告,确立 9 个 section 的统一模式)+ 去掉 extension guard 里的 `assetType !== "local"` 硬编码。

**做了什么(决策落地)**:状态所有权 = **A(section 自持 state via ref)**;迁移 = **增量 vertical-slice**,`local`(最简单:3 字段、不可测、无隧道)打头证明 seam。壳现为双路径,仅 `local` 设 `ConfigSection` 走通用路径,其余 8 类型仍走遗留 `assetType === "x"` switch(过渡双路径只在分支中间 commit,末 commit 删)。

**行为保持**:`local` 保存的 config JSON 与编辑回填经 golden-locked 纯函数,与旧 inline 字节一致;`sshTunnelId` 恒 0(与旧 `else` 分支等价,local 从不设隧道);`persistAsset` 是 create/update 持久化的纯抽取,无语义变化。全量 `vitest`(1116 测试)、`tsc`(0)、`eslint`(0)绿;`AssetForm.tsx` 无 `assetType === "local"` 残留。

**契约对后续 8 类型的结论(最终 review)**:`buildConfig`/`buildTestConfig` 分离正确预判了 SSH「测试发明文、保存发密文」的关键差异;`AssetTestConfig{assetType,configJSON,password}` 与后端 `TestAssetConnection` 1:1;`AssetFormContext{isEdit,encryptPassword}` 可按需扩(托管凭据等 section 内部直接调 wails)。无需现在改契约。

**仍留给 4b+**:
- **首个可测类型(serial)迁移前**:把 `validity.canTest` 接到 `isTestableAssetType`/测试按钮(本 4a 未接,local 不可测)。
- **每个类型迁移时同步收缩遗留链**:extension 渲染的负向类型排除列表、`saveDisabledReason`/`isTestableAssetType`/`isTestConnectionDisabled` 三条 per-type 链必须随迁移逐项缩短(防半迁移类型两路径都漏接)——作为每次迁移的 checklist 项。
- **多个 ConfigSection 类型共存后**:`validity` 在迁移类型间切换时的重置(现 `key={assetType}` remount 自纠,单类型不触发)。
- 迁移顺序:`serial → etcd → redis → mongodb → database → k8s → kafka → ssh`,末 commit 删遗留 switch + 共享 host/port/username 等残留 state + `DEFAULT_ICONS`/`DEFAULT_PORTS`(届时只剩壳用则按需)。
