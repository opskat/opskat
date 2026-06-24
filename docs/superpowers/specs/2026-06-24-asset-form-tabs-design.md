# 资产表单重构设计 · 标签页化(方案 B)

> 日期:2026-06-24 ·  状态:已实现 ·  范围:`frontend/` 资产添加/编辑表单 UI/UX

## 1. 背景与问题

资产添加/编辑表单(`frontend/src/components/asset/AssetForm.tsx`)是一个固定单列弹窗(`max-w-2xl`,`85vh`)。顶部是身份字段(类型 / 图标+名称 / 分组),中间挂类型专属的 `ConfigSection`,底部是描述 + Test/Save。

每个 `ConfigSection` 把**所有字段平铺成一条竖列**:必填的连接项(主机/账号/密码)和高级旋钮(TLS 及嵌套证书路径、跳板/代理、超时、scan 页大小、key 分隔符、Schema Registry、N 个 Kafka Connect 集群……)挤在同一列。SSH/数据库/Redis 已经很长,**Kafka 最严重**(Brokers + SASL + TLS + Schema Registry + 任意多个 Connect 集群 + 超时/预览/拉取参数)。继续往下堆会越来越乱。

## 2. 目标 / 非目标

**目标**
- 用顶部水平标签页组织类型配置,消除"高级项往下无限堆叠"。
- **第一个「连接」标签 = 建立连接所需的全部必填**:填完它就能测试连接 + 保存,无需进入其它标签。
- 其余标签全部是**可选**高级项;只在用户主动启用某功能却未填全时才提示。
- 把 B 方案固有的"必填字段藏在没打开的标签里"缺点降到最低。

**非目标(明确不做)**
- 不改后端、不改 config JSON 结构、不改 `asset_entity.Asset` 模型 → **无数据迁移**。
- 不增删任何字段,只重排其分组/位置。
- 现有序列化器(各 `*.config.ts` 的 `build*/parse*`)保持不变。
- 不改资产类型注册机制、不在 shell 里按类型分支(遵守 OCP)。

## 3. 选定方案

方案 B:**顶部水平标签页**。决策(均已与用户确认):

1. **导航形式**:顶部水平标签(非左导航)。标签条溢出时横向滚动,不换行、不挤压。
2. **第一个标签固定叫「连接」**,装下建立连接所需的全部必填。Kafka 的 Brokers 作为该标签内的子标题。
3. **跳板/代理独立成「SSH 隧道/代理」标签**(沿用此名),直接复用现有 `ConnectionMethodFields` 组件。
4. **描述折成底部一行**:常态显示「＋ 添加备注」,点击就地展开成文本框,不占顶部高度。
5. **单分组类型不显示标签条**:只有一个分组时(串口/本地/扩展)退化为单面板,无标签 chrome。

### 各类型标签规划

| 类型 | 连接(必填,填完即可连) | SSH 隧道/代理 | TLS/证书 | 高级 | 其它标签 |
|---|---|---|---|---|---|
| SSH | 主机 / 端口 / 用户 / 认证(密码或密钥,含本地密钥扫描) | ✓ | — | — | — |
| 数据库 | 驱动 / 主机 / 端口 / 用户 / 密码 / 库名(SQLite 源) | ✓ | SSL 模式 + TLS 开关 | 参数 / 只读 | — |
| Redis | 主机 / 端口 / 用户 / 密码 / DB 号 | ✓ | 开关·insecure·SNI·CA/cert/key | 命令超时 / scan / key 分隔符 | — |
| MongoDB | 手动/URI · 主机 / 端口 / 用户 / 密码 / 默认库 | ✓ | TLS 开关 | 副本集 / authSource | — |
| etcd | endpoints / 用户 / 密码 | ✓ | 开关·insecure·SNI·CA/cert/key | —(无,不出此标签) | — |
| Kafka | Brokers + SASL(机制/用户/密码) | ✓ | 开关·insecure·SNI·CA/cert/key | clientId / 超时 / 预览字节 / 拉取条数 | Schema Registry · Connect(集群数徽标) |
| K8s | kubeconfig / namespace / context | ✓(仅隧道选择器,无代理) | — | — | — |
| 串口 / 本地 / 扩展 | 单面板,无标签 | — | — | — | — |

> 说明:**TLS/证书统一单列为一个标签(方案 β)**,所有带 TLS 的类型(数据库/Redis/MongoDB/etcd/Kafka)都有。Kafka 的主 TLS 从「连接」移到「TLS/证书」;其伴随项(Schema Registry/Connect)各自的 TLS 仍留在各自标签内(per-companion,不并入共享 TLS 标签)。etcd 的高级项仅 TLS,移出后无「高级」标签。K8s 为一致性也拆成 连接(kubeconfig/namespace/context)+ SSH 隧道(单个隧道选择器,无代理)两标签 —— 该「SSH 隧道」标签复用 `asset.tabTunnel` 文案但内容仅一个隧道下拉(K8s 无直连/跳板/代理三选)。串口/本地/扩展仍是单分组,由 `ConfigTabs` 自动退化为无标签单面板。

> **范围澄清:本次为纯 UI 重排,所有字段/选项均为现有且后端已支持**;各 `*.config.ts` 序列化器不改(同样 config JSON 进出),无后端/无 config 结构/无数据迁移改动。

## 4. 架构

### 4.1 共享主键 `<ConfigTabs>`

新增共享组件 `frontend/src/components/asset/ConfigTabs.tsx`,封装在 `@opskat/ui` 的 `Tabs` 之上(MongoDB section 已在用 `Tabs`,复用同一原语)。任何 `ConfigSection` 把自己的字段拆成一组 `ConfigGroup` 描述符传入:

```ts
export interface ConfigGroup {
  key: string;                 // 稳定标识,用于 focusGroup 跳转
  label: string;               // i18n key
  optional?: boolean;          // true → 标签上显示"可选"标(第一个「连接」默认 false)
  badge?: number;              // 数量徽标(如 Connect 集群数)
  invalid?: boolean;           // 红点:该分组"已启用但必填没填全"
  render: () => React.ReactNode;
}
```

`<ConfigTabs>` 职责:
- 渲染顶部标签条(溢出 `overflow-x-auto`)+ 当前面板(`min-height` 固定,切换不跳动)。
- `groups.length === 1` 时**不渲染标签条**,直接渲染该分组内容(单面板退化)。
- `invalid` → 标签上红点;`badge` → 数量徽标;`optional` → "可选"标。
- 自持当前激活标签;经 ref 暴露 `setActive(key)` 供 section 实现 `focusGroup`。

### 4.2 契约改动(最小化,不动 shell 分发)

`frontend/src/lib/assetTypes/formContract.ts`:
- `SectionValidity` 增加可选 `invalidGroupKey?: string`(保存被禁用时应跳转到哪个标签)。
- `AssetFormHandle` 增加可选 `focusGroup?: (key: string) => void`(由 section 转调 `ConfigTabs.setActive`)。

**shell 契约不变**:`AssetForm` 仍然只挂 `<sectionDef.ConfigSection>`、只读取聚合后的单个 `SectionValidity`、`buildConfig/buildTestConfig` 签名不变。section 内部如何分标签是其私有实现。符合 Reuse-first(复用 `ConfigTabs`/`ConnectionMethodFields`/`PasswordSourceField`)+ OCP(shell 不按类型分支)。

### 4.3 各 ConfigSection 的改造模式

每个 section **保留原有 state + `*.config.ts` 序列化逻辑不变**,只做两件事:
1. 把现有 JSX 重排进 `groups` 数组,渲染 `<ConfigTabs groups={...} />`。「SSH 隧道/代理」标签 = `<ConnectionMethodFields/>` 原样搬入。
2. 计算每个分组的 `invalid`,并把"连接标签必填 + 各已启用可选分组必填"**聚合**成现有 `SectionValidity`(`canTest`/`canSave`/`saveDisabledReason`/`invalidGroupKey`)经 `onValidityChange` 上报。

> Kafka 现状:伴随项校验(Schema Registry URL、Connect 集群名/URL、bearer token)只在 `buildConfig` 里 `throw`(`validateKafkaCompanions`)。本次改造把这些升级为**反应式 `invalid`**,落到对应标签的红点上;`buildConfig` 的 `throw` 作为防御保留。

## 5. 校验与定位模型

- **连接标签必填驱动 Test/Save**:与现状一致(SSH `host`、Kafka `brokers`、etcd `endpoints` 等)。因为这些必填都在**默认显示的第一个标签**,常规路径下其它标签永不冒红点 —— 这把 B 方案"必填藏起来"的缺点基本消掉。
- **可选标签的条件必填**:仅当用户主动启用某功能(如开了 Schema Registry 没填 URL、加了 Connect 集群没填名/URL),该标签 `invalid=true` 并阻断保存。
- **底部点名 + 跳转**:`saveDisabledReason` 文案点名分组(如「认证 缺少 SASL 密码」);若 `invalidGroupKey` 存在,footer 渲染「前往 ›」调用 `sectionRef.focusGroup(key)` 切到该标签。测试失败仍走现有 toast。
- **连接齐备提示(已延后,未实现)**:原设想连接标签必填齐全、无 `invalid` 时 footer 显示「✓ 连接项已齐,可保存」。本次实现**未做**此正向提示(footer 仅在不可保存时显示禁用原因+「前往」;可保存时直接启用保存按钮已足够);`asset.connectReady` 文案也未加。作为可选增强保留待后续。

## 6. shell(AssetForm.tsx)改动

- **身份固定区不变**:类型(仅新增态)+ 图标/名称 + 分组 常驻顶部。
- **描述**:从底部 `Textarea` 改为 footer 上方一行的 `DescriptionBar`(折叠态「＋ 添加备注」,有内容时显示截断文本;点击就地展开 `Textarea`)。新增小组件 `frontend/src/components/asset/DescriptionBar.tsx`。
- **footer**:Test/Save 不变;`saveDisabledReason` 旁按 `invalidGroupKey` 增「前往 ›」。
- **容器**:沿用 `max-w-2xl`;标签条溢出横向滚动。面板设 `min-height` 防抖。
- shell **不**按类型分支,挂载逻辑不变。

## 7. 复用清单(grep before writing)

- 隧道/代理标签 = `ConnectionMethodFields`(已存在,SSH/DB/Redis/Mongo/etcd/Kafka 共用)。
- 密码/凭据 = `PasswordSourceField` + `useAssetCredential`(不变)。
- 标签原语 = `@opskat/ui` `Tabs`(MongoDB 已用)。
- 资产/分组选择 = `AssetSelect` / `GroupSelect`(不变)。
- 序列化 = 各 `*.config.ts`(不变)。

## 8. 测试策略(TDD,vitest + RTL)

先写测试再写实现:
- **`ConfigTabs`**:≥2 分组渲染标签条;单分组不渲染标签条直接出内容;点击切换面板;`invalid` 出红点;`badge` 出数量;`focusGroup`/`setActive` 切到指定标签。
- **section 聚合校验**(以 Kafka 为重点):
  - 仅填 Brokers → 连接标签 ok,可测试/可保存,其它标签无红点。
  - 开 Schema Registry 但空 URL → 保存禁用,`invalidGroupKey="schema_registry"`,该标签红点,点「前往」切过去。
  - 加 Connect 集群但空名/URL → 同上。
- **`DescriptionBar`**:折叠/展开;有内容时回显。
- **回归**:各 `*.config.ts` 的 `build*/parse*` 既有测试保持绿(逻辑未动)。

## 9. 迁移顺序(每步独立可发)

1. `ConfigTabs` 原语 + 测试。
2. shell:`DescriptionBar` + footer「前往」+ `formContract` 契约扩展(向后兼容,字段可选)。
3. 按类型逐个迁移,每个独立成 PR/提交:
   - 先 **SSH**(2 标签:连接 + 隧道)验证模式 → 数据库 / Redis / MongoDB(连接 + 隧道 + TLS/证书 + 高级)/ etcd(连接 + 隧道 + TLS/证书)→ **Kafka**(6 标签,展示位)→ K8s(连接 + SSH 隧道,2 标签)。
   - 串口 / 本地 / 扩展:本就是平铺单面板,`ConfigTabs` 单分组退化后零视觉变化,可不改或顺手统一。

## 10. i18n 新增键(`frontend` 默认命名空间)

- 标签:`asset.tabConnection`(连接)、`asset.tabTunnel`(SSH 隧道/代理)、`asset.tabTls`(TLS/证书)、`asset.tabAdvanced`(高级)、`asset.tabSchemaRegistry`、`asset.tabConnect`。
- 标记:`asset.optional`(可选)、`asset.goToTab`(前往)、`asset.addDescription`(添加备注)。(`asset.connectReady` 随"连接齐备提示"一并延后,未加。)
- 中英双语都补(项目 i18n 现状)。

## 11. 风险 / 待确认

- **标签溢出**:窄弹窗下 Kafka 6 个标签横向滚动是否够用;实现时实测,必要时合并。
- **TLS 移出「连接」与"第一标签即可连"的张力**:对强制 TLS 的环境,严格说连接要到「TLS/证书」标签开启 TLS 才算配全;但 TLS 开关默认关(明文),常见情况下「连接」标签单独仍能连,故不把 TLS 列为连接必填(不冒红点)。
- **编辑态落点**:打开已配置高级/TLS 项的资产,默认仍落在「连接」标签;红点/徽标提示已配置内容。
- **「连接」与「SSH 隧道/代理」语义边界**:连接=连什么(主机/账号),隧道/代理=怎么够到(网络路径),文案上避免混淆。
