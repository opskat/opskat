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

## 阶段 4b 完成记录(2026-06-05)

计划见 `docs/superpowers/plans/2026-06-05-assetform-registration-phase4b.md`。子 agent 逐 Task 驱动 + spec/质量/最终 review。落地 4 个 commit:

- `5fb2ef55` — `serial` 配置纯函数 `buildSerialConfig`/`parseSerialConfig`/`SERIAL_DEFAULTS`(sibling `SerialConfigSection.config.ts`)+ golden(锁旧保存/`loadSerialConfig` 字节一致;serial 的测试 config 与保存 config 同形)。
- `9dbe8472` — 迁移 `serial`(**首个可测类型**)+ 建**通用测试编排**:`SerialConfigSection` 重写为 `forwardRef` 自持 state,暴露 `buildConfig` + `buildTestConfig`(复用 `buildSerialConfig`,password "");`onValidityChange` 契约扩为 `SectionValidity{canTest,canSave,saveDisabledReason?}`;壳新增 `handleGenericTestConnection`(镜像旧 `handleTest*` 的 testId 竞态/取消/toast,只把 config 来源换成 `buildTestConfig`),`isTestableAssetType`/`isTestConnectionDisabled`/`handleRunTestConnection`/`saveDisabledReason` 在 `sectionDef?.ConfigSection` 时切到通用/反应式分支;删 serial 全部遗留。
- `18f69f15` — prettier 修正 serial 测试文件格式。
- `6d593b4d` — 去 extension guard 的 `assetType !== "serial"` 硬编码 + `handleTypeChange` 死行(承 serial 迁移,同 4a 处理 local 的先例)。

**关闭了 4a 的两条备忘**:① `validity.canTest` 已接到测试按钮(`isTestConnectionDisabled` 通用分支 `!validity.canTest`)——4a 推迟项落地;② serial 迁移时同步收缩了四条 per-type 遗留链(isTestable/isTestConnectionDisabled/handleRunTestConnection/saveDisabledReason 删 serial 分支)+ extension 负向列表删 serial。

**行为保持**:serial 保存/测试 config 经 golden 锁定;测试竞态/取消/toast = 旧 `handleTestSerialConnection` body;"缺串口"提示经 `SectionValidity.saveDisabledReason` 保留。**唯一刻意微调**:旧测试禁用用 `!serialPortPath`(不 trim)、保存用 `.trim()` —— 一个纯空白端口旧行为是"可测不可存"的隐性不一致;迁移后统一 `!!portPath.trim()`,纯空白端口现一致禁用两者(序列化仍写未 trim 的 port_path,golden 字节不变),最终 review 判为良性改进。全量 `vitest`(1124)/`tsc`/`eslint` 绿,local 测试仍 10 绿(契约扩展未回归 4a)。

**最终 review 的前瞻结论(余 7 类型)**:`SectionValidity` 直接泛化(各类型自报自己的 missing-field i18n key,壳零分支);**SSH 的"测试发明文、保存发密文"已被 `buildConfig`/`buildTestConfig` 分离 + 异步 build 的 try/catch 支持**,壳无需再改;`onValidityChange` 必须始终传壳的 `setValidity`(身份稳定),勿在壳侧包一层非稳定回调(否则 effect 自循环);**每个有密文字段的类型(redis/mongodb/database/kafka/ssh)迁移时,须把旧 `loadXxxConfig` 的解密/掩码逻辑搬进各自的 `parseXxxConfig`,不能像 serial 那样假设明文**。

**仍留给 4c+**:`etcd → redis → mongodb → database → k8s → kafka → ssh` 余 7 类型;末 commit 删遗留 switch + 共享 host/port/username state + `DEFAULT_PORTS`/`DEFAULT_ICONS`(届时只剩壳用按需)。

## 阶段 4c 完成记录(2026-06-05)

计划见 `docs/superpowers/plans/2026-06-05-assetform-registration-phase4c.md`。子 agent 逐 Task 驱动 + spec/质量/最终 review(关键迁移 commit 用 opus)。落地 4 个功能 commit(+ 2 个计划 commit):

- `3edc2940` — **抽 db 族共享凭据纯函数** `credentialConfig.ts`:`CredentialState`/`CredentialFragment`/`initCredentialFromConfig`(锁旧 `load*Config` credential 分支)/`resolveTestCredential`(锁旧 `applyTestPasswordSource`)/`resolveSaveCredential`(锁旧 save + `encryptPasswordValue`)+ golden。
- `ddf491b4` — `etcd` 配置纯函数 `EtcdConfigSection.config.ts`:`buildEtcdConfig`/`parseEtcdConfig`/`parseEtcdEndpoints`/`ETCD_DEFAULTS` + golden(**键序锁旧 save 分支**:`endpoints→username→credential→tls…→timeouts→ssh_asset_id`;旧 test 分支键序不同但 Go struct 反序列化无关,统一到 save 序)。
- `c7bd01b0`(原子)— **迁移 etcd + `useAssetCredential` hook**:hook 自持凭据子状态 + 加载 `ListCredentialsByType("password")` + 编辑回填;`EtcdConfigSection` 重写为 `forwardRef`,自持 `EtcdFormState`(非凭据字段)+ 组合凭据 hook,`buildConfig` = `resolveSaveCredential` 后 `buildEtcdConfig`(`sshTunnelId` = `editAsset.sshTunnelId || cfg.ssh_asset_id`),`buildTestConfig` = `resolveTestCredential` + 明文 4th-arg;`etcd.ts` 注册 `ConfigSection + testable`;壳删 etcd 全部配置/load/reset/test/save/render + 有编译依赖的死分支(`etcdEndpointsList` 及其两调用点、`handleRunTestConnection` etcd 三元);删旧 props 测试,加 ref 契约测试。
- `6ba91895` — 去壳里 etcd 剩余 3 处**无编译依赖**死项(`isTestableAssetType` 的 `|| etcd`、extension guard `!== etcd`、`handleTypeChange` 的 `if(newType==="etcd")setHost("")`)。

**首个 db 族 + 首个凭据/加密/TLS/隧道类型**:确立凭据三段(init/test/save)抽象,后续 redis/mongodb/database 可直接 `useAssetCredential(editAsset)` 复用。Task3/4 拆分按**编译依赖**重定:闭包引用已删 state/函数的死分支(`etcdEndpointsList`、`handleTestEtcdConnection` 引用)必须并入原子迁移 commit,只有纯字符串比较死项可延后。

**行为保持**:save/test/load 三段经 golden 锁定;`sshTunnelId` 优先级、managed-vs-inline 凭据、TLS 子键门控、endpoints 必填(同驱 Save+Test 按钮 + `saveDisabledReason: "etcd.error.endpointsRequired"`)均与旧一致。**唯一刻意微调**:加密失败旧用 `encryptPasswordValue` 返回 `undefined` + 硬编码英文 toast + 静默 abort;新版让 `ctx.encryptPassword` 的 reject 透传到 `handleSubmit` 既有 try/catch toast,语义等价(save 中止 + 错误 toast)且不再吞错。全量 `vitest`(1145)/`tsc`(0)/`eslint`(0)绿;`AssetForm.tsx` 仅剩 `DEFAULT_PORTS.etcd`/`DEFAULT_ICONS.etcd`/`AssetType` union 三处合法 etcd 残留;`grep EtcdConfigSection` 无旧 props API 引用。

**最终 review 的前瞻结论**:
- redis/mongodb/database 凭据模型与 etcd 同形,`useAssetCredential` + `resolve{Test,Save}Credential` 可直接复用,无 etcd 特化泄漏。
- **kafka 是最难一档**:有 N 个嵌套凭据子态(主 + schema-registry + 每个 connect cluster)+ `auth_type`/bearer/username-as-token 逻辑(`applyKafkaCompanionAuth`),单次 `useAssetCredential` 覆盖不了;该层**包裹**而非替换共享 resolver。且 kafka 各 companion 会各自重复 `ListCredentialsByType("password")` —— 届时应把 `managedPasswords` 提升为单次拉取传入,或 companions 仍走现有 helper。
- **凭据数值双默认**(`parseEtcdConfig` 的 `dial_timeout_seconds || 5` 等)是旧 `loadEtcdConfig` 的忠实迁移;因 `buildEtcdConfig` 在 0 时省略该键,0 永不落库,`||` 与 `??` 对往返数据等价 —— 不在 4c 改(保持行为锁定),留作跨类型统一项(若要修,单独一次性改全部类型,勿混入逐型迁移)。

**仍留给 4d+**:`redis → mongodb → database → k8s → kafka → ssh` 余 6 类型;末 commit 删遗留 switch + 共享 host/port/username/password state + `applyTestPasswordSource`/`encryptPasswordValue` + `DEFAULT_PORTS`/`DEFAULT_ICONS` 残留。

## 阶段 4d–4i 完成记录:全部 9 类型注册化 + 壳脚手架拆除(2026-06-05)

承 4c 的模板与共享凭据抽象,一次性完成余 6 类型迁移,**stage 4 收官**。子 agent 逐类型驱动(db 族用 sonnet、database/kafka/ssh 用 opus),每类型独立验证(tsc + 全量 vitest + golden + residue grep);etcd/kafka/ssh 加专项 review,ssh 末类型加收官 holistic review。

- **4d redis**(`10d6c5fb`/`c8d59e00`/`2b282088`)— 复用 `useAssetCredential`;host/port 入 `RedisFormState`;validity 要 host。
- **4e mongodb**(`6c512359`/`33137330`/`e491d364`)— manual/uri 双模;validity 按模式(host vs connectionURI,两种 saveDisabledReason)。
- **4f database**(`6dad6dae`/`9725084c`/`ad739cc7`)— driver 维(mysql/pg/mssql/sqlite),sqlite 无凭据走 path;**新增可选契约 `onIconChange`**:driver 变化驱动壳 icon(仅 database 用,其余 section 忽略);驱动选择器移入 section;`applyDriverChange` 纯函数。
- **4g k8s**(`4ebab983`/`80822351`)— 非 db 族、不可测(`buildTestConfig: null`);kubeconfig 经 `ctx.encryptPassword` 加密 + 编辑保留既有密文;SSH 隧道仅在 asset 顶层(config JSON 无 `ssh_asset_id`);validity:kubeconfig 仅新建必填。
- **4h kafka**(`2df5ebf8`/`6c8dc13a`/`51004a5a`)— 最复杂:主 SASL 凭据复用 `useAssetCredential`,**伴随**(schema_registry + N 个 connect cluster,各带 auth_type/bearer/TLS)逻辑内化进 section(包裹而非替换共享 resolver);校验改 `buildConfig` 内 throw i18n 文案(`handleSubmit` catch 单次 toast);测试 config 不含伴随。顺带删除 kafka-only 的共享 `tls` state(tsc 证实 ssh 无 TLS)。
- **4i ssh + 脚手架拆除**(`1e28da81`/`128ae526`/`4c964cfd`)— **末类型**:password auth 复用 `useAssetCredential`;key auth 用独立 `ssh_key` 凭据列表 + 本地密钥扫描(section 自加载);三处密文(password/passphrase/proxy);**save/test 关键分歧**:`jump_host_id` 仅 test config(save 走 asset 顶层 `sshTunnelId`),proxy 密码/passphrase test 明文、save 加密。迁移 ssh 后所有共享连接/凭据 state 成孤儿,**同提交拆除**:`host/port/username/password/credential/managedKeys/localKeys/connectionType/proxy*` state + `applyTestPasswordSource`/`encryptPasswordValue`/`encryptProxyPassword`/`resetSharedFields`/`loadSSHConfig`/`handleTestConnection` + `DEFAULT_PORTS`;`handleTypeChange` 简化为 setAssetType+setIcon;load 派发塌缩为 `ConfigSection?跳过:扩展加载`;校验链塌缩(`isTestableAssetType = ConfigSection?testable:false`)。

**收官状态**:9 类型(local/serial/etcd/redis/mongodb/database/k8s/kafka/ssh)全部经注册表 `ConfigSection` 渲染;`AssetForm.tsx` **2114 → 433 行**(纯壳:类型选择 + 名称/分组/图标/描述 + 通用 section 渲染/ref 接线 + 通用保存/测试编排 + 扩展处理 + 对话框);**零 `assetType === "x"` 类型分支**;全量 `vitest` 1295(123 文件)、`tsc` 0、`eslint` 0;每类型保存/测试序列化经 golden 字节锁定。共享凭据抽象(`useAssetCredential` + `credentialConfig`)被 6 个 db 族类型复用。

**统一的等价性变更(全 9 类型一致)**:加密失败由旧「`undefined` 哨兵 + 硬编码 toast + 静默 abort」改为 `ctx.encryptPassword` reject 透传到 `handleSubmit` 既有 try/catch(语义等价 + 不再吞错)。

**已知小项(非回归,留作后续)**:① ssh 托管密钥→用户名自动填充行为代码已保留(`SSHConfigSection.tsx` onValueChange),但原 4 例交互测试随旧组件删除未补回——行为在,专项覆盖缺;② `database`/`redis` 等 name 占位符 ternary 仍在壳里(纯展示,可后续派生自注册表);③ serial/local 无 `.config.test.ts`(早期迁移,密文无关)。

**stage 4 完成,后续**:阶段 5(`AssetTree.tsx` ssh 文件管理硬编码 → action 注册)、阶段 6(skill + 文档收尾)。
