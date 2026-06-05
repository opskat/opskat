# 新增资产类型接入指南

> 本文是「往 opskat 里加一个新的内置资产类型」的具体 how-to:要实现哪些接口、按什么顺序改哪些文件、哪些是「注册即接入」、哪些仍需改共享代码。
>
> 工程**原则**(SOLID / 高内聚低耦合 / Reuse first / Fix policy)见 [`../AGENTS.md`](../AGENTS.md);**架构与子系统总图**见 [`./DEVELOP.md → Architecture`](./DEVELOP.md#architecture)。本文不重复这两者,只讲资产类型这条线的接口与流程。改本文前先读 [`./DOC-MAINTENANCE.md`](./DOC-MAINTENANCE.md)。

## 核心理念:注册,而非 switch

资产类型走的是**注册表扩展**(OCP):新类型 = 在两个注册表里各登记一份 + 提供这个类型自己的表单/详情/序列化/连接逻辑。**不要**在共享代码里写 `if assetType == "xxx"` / `switch protocol`——那正是注册表要消灭的耦合。

- **后端注册表**:`internal/assettype/`,实现 `AssetTypeHandler` 并在文件 `init()` 里 `Register(&xxxHandler{})`。
- **前端注册表**:`frontend/src/lib/assetTypes/`,调用 `registerAssetType({...})`。

内置类型集合是**可枚举的**,不要在任何地方硬编码数量。后端枚举:

```bash
git grep -hn "Register(&" -- internal/assettype/*.go | grep -v _test
```

当前内置:`ssh / database / redis / mongodb / kafka / k8s / etcd / serial / local`。前端同名注册见 `frontend/src/lib/assetTypes/index.ts` 的 side-effect import 列表。

## 大图:接入面取决于类型的能力

不是每个新类型都要碰下面所有点——**改动量取决于这个类型需要什么能力**。最小的类型(像 `local`:无凭据、无策略、无连接池、复用已有 policy kind)只碰极少几处;最重的是 **query 型**(像 database/redis),因为查询路由 / 面板选择 / tab 状态目前**还没**注册化(见[§7 仍需改共享代码的耦合点](#7-仍需改共享代码的耦合点))。

| 维度 | 注册即接入(OCP) | 仍需改共享代码 |
| --- | --- | --- |
| 资产 CRUD / AI 工具 add·update·safe-view | ✅ 全经 `assettype.Get(type)` 派生 | — |
| 连接测试(表单 Test 按钮) | ✅ binder `New()` 里 `conntest.Register` | — |
| 策略测试 / 内置策略组(**复用**已有 kind) | ✅ handler 的 `PolicyKind()` 返回已有 kind | — |
| 类型选择器 / 过滤 / 分组 / 标签 / 详情卡选择 / 表单 section 渲染 / 连接动作分发 / 新标签页 / 文件管理菜单项 / Test 按钮 | ✅ 全经前端注册表能力位派生 | — |
| entity 类型常量 + config struct | — | `asset_entity/asset.go`(entity 层字面量) |
| 前端注册 side-effect import | — | `assetTypes/index.ts` 加一行 `import` |
| i18n | — | 两个 `common.json` 都加 key |
| **全新** policy kind(策略语义前所未有) | 各自独立文件 + 注册,无中心 switch | 几处 const 块 + groups 表迁移 |
| 连接池 `DialXxx`(需要联网) | 自己的 `connpool/<type>.go`,被 binder 按名调用 | 新文件,非注册 |
| App binder(需要运行时面板) | — | `main.go` 三处接线 |
| **query 路由 / terminal transport / AI @-mention** | ❌ 尚未注册化 | `queryStore.ts` / `terminalStore.ts` / `MainPanel.tsx` / `ai/*` |

---

# 后端接入

所有路径相对仓库根。引用文件与符号名而非行号(行号易漂移,用 `git grep <符号>` 定位)。

## B1. entity:类型常量 + config 结构 + 访问器 —— `internal/model/entity/asset_entity/asset.go`

新增(entity 层字面量,不经注册表):

- `AssetTypeXxx = "xxx"` 常量(与既有 `AssetTypeRedis` 等同块)。
- `IsXxx()` helper(与既有 `IsRedis()` 等同块)。
- `XxxConfig` 结构体 + `GetXxxConfig()` / `SetXxxConfig()` 访问器。资产配置是**单个 JSON `config` 列**,不是每类型一张表——所以**加资产类型本身不需要数据库迁移**。

## B2. 核心 handler —— `internal/assettype/<type>.go`

实现 `AssetTypeHandler` 接口(`internal/assettype/registry.go`,逐字):

```go
type AssetTypeHandler interface {
	Type() string
	DefaultPort() int
	SafeView(a *asset_entity.Asset) map[string]any
	ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error)
	DefaultPolicy() any
	// PolicyKind 返回该资产类型所用的规范 policyKind(见 entity/policy.PolicyKind*）。
	// 经 Register 写入 entity/policy 的 asset-kind 注册表,供 ai/policy.ResolvePolicyKind 派生。
	PolicyKind() string
	// ValidateCreateArgs 校验 AI 工具创建资产时的必填字段。
	// 由 handleAddAsset 在 ApplyCreateArgs 之前调用，每种类型自行声明所需字段。
	ValidateCreateArgs(args map[string]any) error
	ApplyCreateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error
	ApplyUpdateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error
}
```

| 方法 | 职责 / 何时调用 |
| --- | --- |
| `Type()` | 规范类型串(= `AssetTypeXxx`),注册表 key,全局分发 key |
| `DefaultPort()` | 默认端口(redis 6379 / ssh 22 / etcd 2379;无端口类型返回 0) |
| `SafeView(a)` | 脱敏字段投影,喂 AI / 列表视图——**禁止暴露密码 / 密钥 / 证书路径**。经 `internal/ai/tool/tool_handlers_asset.go` 的 `h.SafeView(a)` 调用 |
| `ResolvePassword(ctx, a)` | 解密 / 解析明文凭据。db 族用 `credential_resolver.Default().ResolvePasswordGeneric`;ssh 用 `ResolveSSHCredentials`;无凭据类型返回 `"", nil` |
| `DefaultPolicy()` | 该类型的默认策略结构(如 `asset_entity.DefaultRedisPolicy()`) |
| `PolicyKind()` | 返回规范 policy kind(`entity/policy.PolicyKind*`)。**`Register()` 据此自动写 asset→kind 表**。复用已有 kind 直接返回它(`local` 即返回 `PolicyKindCommand`);空串=不登记 kind |
| `ValidateCreateArgs(args)` | AI 创建时的必填校验,在 `ApplyCreateArgs` 前调用 |
| `ApplyCreateArgs/UpdateArgs` | 从 AI 工具参数填 / 改 config;密文经 `credential_svc.Default().Encrypt` |

注册(`init()`,以 redis 为例):

```go
func init() {
	Register(&redisHandler{})
	policy.RegisterDefaultPolicy("redis", func() any { return asset_entity.DefaultRedisPolicy() })
}
```

`Register(h)`(registry.go)做两件事:存入 handler 注册表,并在 `h.PolicyKind() != ""` 时调 `policy.RegisterAssetKind(h.Type(), h.PolicyKind())`——**asset→kind 映射自动接线,无需手维护**。第二行 `RegisterDefaultPolicy` 是另一个(默认策略 provider)注册表,供 `System.GetDefaultPolicy` 用;无策略类型(如 `local`)省略这行。

**复用共享 arg 解析,不要重复 parse**:`ArgString` / `ArgInt` / `ArgInt64` / `ArgBool` / `ArgStringSlice`(均在 `registry.go`)、`validateRemoteServerArgs`(ssh/database/redis/mongodb 共用的 host/port/username 校验)。

> AI 工具 `handleAddAsset` / `handleUpdateAsset`(`internal/ai/tool/tool_handlers_asset.go`)**全经 `assettype.Get(type)` 分发**,无 per-type 代码——实现 handler 即自动接入。可选:在 `internal/ai/tool/tools_asset.go` 的 `assetTools()` JSON schema 里登记本类型的 create 参数(文档性,不影响功能)。

## B3. 策略:复用已有 kind 还是新建 kind

**复用已有 kind(多数情况)**:`PolicyKind()` 返回已有常量即可,**B3 余下、内置组、策略测试、迁移全都不用碰**——自动继承该 kind 的全部行为。

**全新 policy kind(策略语义前所未有)**,才需要:

1. **kind 常量** —— `internal/model/entity/policy/registry.go` 加 `PolicyKindXxx`;`internal/ai/policy/policy_kind.go` 顶部 alias 一份。(这两处 const 块是仅有的字面量编辑。)
2. **策略结构 + 默认** —— `entity/policy/policy.go` 加 `XxxPolicy` + `DefaultXxxPolicy()`,`asset_entity/asset.go` alias。
3. **策略测试 handler** —— `internal/ai/policy/policy_kind.go` 的 `init()` 里 `registerPolicyKind(PolicyKindXxx, ...)`;`policy_tester.go` 加 `testXxxPolicy`;`policy_group_resolve.go` 加 `ResolveXxxGroups`。**全是各自独立文件里的新增,无中心 switch。**
4. **内置策略组(可选)** —— `internal/model/entity/policy_group_entity/policy_group.go` 的 `init()` 里加一个 `registerBuiltinGroups(PolicyTypeXxx, ...)` 块 + `PolicyTypeXxx` alias const。`Validate` / `BuiltinGroups` / 顺序全自动派生(无 switch)。注意 layering:此包在 entity 层,**不能 import `assettype`**。
5. **groups 表迁移** —— 仅当新 kind 支持组继承时:`groups` 表每个 kind 一列。新建 `migrations/<ts>_group_<kind>_policy.go`(`ALTER TABLE groups ADD COLUMN <kind>_policy TEXT`,参考 `migrations/202605260001_group_etcd_policy.go`),**追加**到 `migrations/migrations.go` 的迁移 slice(append-only,唯一的「改共享」清单),并在 `group_entity.Group` 加 `<Kind>Pol` 字段 + 访问器。

## B4. 连接池 —— `internal/connpool/<type>.go`(仅当需要联网)

connpool **不是注册表**,是一组显式 `DialXxx` 函数,由 binder/tester 按名调用。需要联网的新类型加自己的 `internal/connpool/<type>.go`,提供 `DialXxx(ctx, asset, cfg, password, sshPool) (...)`。复用 `NewSSHTunnel` 与 `BuildTLSConfig`。

SSH 隧道解析约定(顶层列优先,config 字段仅向后兼容):

```go
tunnelID := asset.SSHTunnelID
if tunnelID == 0 {
	tunnelID = cfg.SSHAssetID // backward compat
}
```

> 这正是前端 SAVE 不再往 config 写 `ssh_asset_id`、而把隧道存在 asset 顶层 `sshTunnelId` 的原因(见 [§F3](#f3-configsection--纯-configts-序列化)):后端优先读顶层列。无连接池的类型(serial/local)此处不加任何东西。

## B5. 连接测试 —— `internal/service/conntest`

tester 是 binder 实例方法(持有 live pool/manager),不能在 `init()` 注册,改在 binder `New()` 时注册:

```go
// TestFunc = func(ctx context.Context, configJSON, plainPassword string) error
conntest.Register(asset_entity.AssetTypeXxx, b.testConnection)
```

唯一 binding `System.TestAssetConnection`(`internal/app/system/asset.go`)统一施加信封(i18n ctx + 10s 超时 + `testreg` 取消)后查表分发——**永不编辑该 dispatcher**。tester body = 解析 config → 必要时 resolve 密码 → 调对应 `connpool.DialXxx` → 关闭。不提供 tester 的类型就没有「测试连接」(刻意留白,非 switch)。

## B6. App binder —— `main.go`(仅当需要运行时面板)

需要自己的运行时面板(终端 / 查询等)的类型,加 `internal/app/<type>/` binder,并在 `main.go` 三处接线:构造 `xxxB := xxx.New(...)`、加入 `binders []Lifecycle` slice、加入 wails `Bind: []interface{}{...}` 列表。纯配置 + 连接测试的类型可从已有 binder 注册 tester,无需新 binder。

## 后端清单(全新内置类型,有序)

1. entity 常量 + `XxxConfig` + 访问器(`asset_entity/asset.go`)— **改共享**(entity 字面量,无 switch)
2. `internal/assettype/<type>.go` 实现 `AssetTypeHandler` + `init(){ Register(...) }` — **注册即接入**
3. 策略 kind:复用→无;新建→const 块 + 结构 + 测试 handler + 内置组 +(组继承时)迁移 — **见 B3**
4. 连接池 `connpool/<type>.go`(需联网时)— **改共享(新文件,按名调用)**
5. 连接测试 `conntest.Register`(支持 Test 时)— **注册即接入**
6. App binder `main.go` 三处(需运行时面板时)— **改共享**
7. AI add/update/safe-view — **无需改**(全注册化)

> **复用已有 kind 的纯配置类型(如 `local`)实际只碰 1、2,以及可选的 5。**

---

# 前端接入

所有路径相对 `frontend/`。

## F1. 注册表 —— `src/lib/assetTypes/`

注册 + 访问器:

```ts
// _register.ts
export const registry = new Map<string, AssetTypeDefinition>();
export function registerAssetType(def: AssetTypeDefinition) { registry.set(def.type, def); }

// index.ts
export function getAssetType(type: string): AssetTypeDefinition | undefined { return registry.get(type); }
export function isBuiltinType(type: string): boolean { return registry.has(type); }
export function getBuiltinTypes(): AssetTypeDefinition[] { return [...registry.values()]; }
```

`AssetTypeDefinition`(`types.ts`,逐字):

```ts
export interface AssetTypeDefinition {
  type: string;
  icon: ComponentType<{ className?: string; style?: React.CSSProperties }>;
  /** 所有应匹配此类型的 `asset.Type` 值（含历史别名）。 */
  aliases: string[];
  /** 选择器展示标签的 i18n key（默认命名空间），如 `nav.ssh`。 */
  label: string;
  /** 选择器语义分组。 */
  category: AssetTypeCategory; // "servers" | "databases" | "middleware" | "extension"
  canConnect: boolean;
  canConnectInNewTab: boolean;
  connectAction: "terminal" | "query";
  /** 是否在右键菜单暴露 SFTP 文件管理动作;缺省 = 不暴露。 */
  canOpenFileManager?: boolean;
  DetailInfoCard: ComponentType<DetailInfoCardProps>;
  /** 资产表单的 per-type config 区;缺省 = 走遗留/扩展路径。 */
  ConfigSection?: ConfigSectionComponent;
  /** 是否支持"测试连接"。 */
  testable?: boolean;
  policy?: PolicyDefinition;
}
```

注册示例(`redis.ts`):

```ts
registerAssetType({
  type: "redis",
  icon: RedisIcon,              // @/components/asset/brand-icons
  aliases: ["redis"],
  label: "nav.redis",
  category: "databases",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: RedisDetailInfoCard,
  ConfigSection: RedisConfigSection,
  testable: true,
  policy: {
    policyType: "redis",
    titleKey: "asset.redisPolicy",
    hintKey: "asset.redisPolicyHint",
    testPlaceholderKey: "asset.policyTestRedisPlaceholder",
    fields: [
      { key: "allow_list", labelKey: "asset.redisPolicyAllowList", placeholderKey: "asset.redisPolicyPlaceholder", variant: "allow" },
      { key: "deny_list",  labelKey: "asset.redisPolicyDenyList",  placeholderKey: "asset.redisPolicyPlaceholder", variant: "deny"  },
    ],
  },
});
```

可参考的模板差异:`ssh.ts`(`connectAction:"terminal"` + `canConnectInNewTab:true` + `canOpenFileManager:true`)、`local.ts`(`policy: undefined`、无 `testable`——无凭据无策略模板)、`k8s.ts`(`aliases:["k8s","kubernetes"]`、无 `testable`)、`database.ts`(`aliases:["database","mysql","postgresql"]`,driver 维度折进单一类型)。

> **唯一不可避免的「改共享」一步**:注册是 import side-effect——`registerAssetType` 只在模块被 import 时执行。`src/lib/assetTypes/index.ts` 底部那串 `import "./ssh"; import "./redis"; ...` 是唯一把每个类型文件拉进来的地方。**新类型文件必须在这里加一行 `import "./<newtype>";`,否则永不注册。** 同时 `__tests__/registry.test.ts` 断言了 `getBuiltinTypes()` 的精确顺序,也要把新类型加进去。

进了注册表后,类型选择器 / 过滤按钮 / 分组 / 标签 / 详情卡选择 / 连接动作分发(按 `connectAction`)/ 新标签页(按 `canConnectInNewTab`)/ 文件管理菜单项(按 `canOpenFileManager`)/ Test 按钮(按 `testable`)**全部自动派生,无 type-string 分支**。

## F2. 表单契约 —— `src/lib/assetTypes/formContract.ts`

```ts
export interface AssetFormContext {
  isEdit: boolean;
  encryptPassword: (plain: string) => Promise<string>; // 明文→密文(后端 EncryptPassword)
}
export interface AssetConfigBuildResult {
  configJSON: string;
  sshTunnelId: number; // 无隧道类型恒 0
}
export interface AssetTestConfig { assetType: string; configJSON: string; password: string; }
export interface AssetFormHandle {
  buildConfig: (ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  buildTestConfig: ((ctx: AssetFormContext) => Promise<AssetTestConfig>) | null; // 不可测类型为 null
}
export interface SectionValidity { canTest: boolean; canSave: boolean; saveDisabledReason?: string; }
export interface ConfigSectionProps {
  editAsset?: asset_entity.Asset;   // 编辑态回填来源;创建态 undefined
  ctx: AssetFormContext;
  onValidityChange: (v: SectionValidity) => void; // 反应式上报,驱动壳 Test/Save 按钮
  onIconChange?: (icon: string) => void;          // 仅 database 用(driver→icon)
}
export type ConfigSectionComponent = ForwardRefExoticComponent<ConfigSectionProps & RefAttributes<AssetFormHandle>>;
```

**契约**:ConfigSection 是 `forwardRef<AssetFormHandle, ConfigSectionProps>`,**自持表单 state**,经 `onValidityChange` 反应式上报校验,经 `useImperativeHandle` 暴露 `buildConfig`(必有)/ `buildTestConfig`(可测才有,否则 `null`)。壳 `AssetForm.tsx` **泛型渲染**(`key={assetType}` 触发切类型重挂载),保存时调 `sectionRef.current.buildConfig(ctx)`、测试时调 `buildTestConfig`——壳里**没有 per-type 分支**(仅剩两处装饰性残留见 §7)。

## F3. ConfigSection + 纯 `.config.ts` 序列化

每个类型两个文件 + golden 测试(以 redis 为例):

- **`<Name>ConfigSection.tsx`** —— `forwardRef` 组件:从 `editAsset` 初始化 state(`parseRedisConfig(editAsset.Config)`,`sshTunnelId` 用 `editAsset.sshTunnelId || parsed.sshTunnelId` 覆盖)、`onValidityChange` 上报、`useImperativeHandle` 暴露 build。
- **`<Name>ConfigSection.config.ts`** —— **纯函数** `buildXxxConfig` / `parseXxxConfig` + `XXX_DEFAULTS` + `XxxFormState`。无 React、无副作用。
- **`__tests__/<Name>ConfigSection.config.test.ts`** —— **golden 字节锁**:断言 `buildXxxConfig` 输出的精确 JSON 串(key 顺序 + 省略规则)。

**为何拆纯函数**:序列化的 JSON key 顺序与「默认值省略」规则是**字节锁定**的,纯函数能在不渲染 React 的前提下用 golden 测试逐字断言。

**SAVE / TEST 序列化分歧(重要,已锁)**:隧道存在 asset 顶层 `sshTunnelId` 列,所以 **SAVE 的 config 不写 `ssh_asset_id`**;但**测试时没有 asset 行**,隧道必须塞进 config,所以 **TEST 写 `ssh_asset_id`**。redis/mongo 用 `buildXxxConfig(state, cred, includeSshAssetId=false)` 开关实现(`buildConfig` 默认 false 省略,`buildTestConfig` 传 `true`);ssh 用同型的 `SSHBuildOptions.includeJumpHost`(SAVE 省略 `jump_host_id`、TEST 写)。新类型若带隧道,照此分歧处理并写 golden 锁两路。

## F4. 共享凭据层(db 族)—— `credentialConfig.ts` + `useAssetCredential.ts`

带密码 / 托管凭据的 db 族(etcd / redis / mongodb / database / kafka)**复用**:

- `useAssetCredential(editAsset)` hook:持有凭据子 state、加载 `ListCredentialsByType("password")` 托管列表、从 `editAsset.Config` 回填。
- `credentialConfig.ts` 纯函数:`initCredentialFromConfig` / `resolveTestCredential` / `resolveSaveCredential(s, encrypt)`(managed→`credential_id`;inline→`encrypt(password)` 或沿用既有密文;加密失败**抛出**不吞)。
- UI 用共享 `PasswordSourceField` 原语。

**规则**:密码 / 托管凭据类型**复用这套**,别重写 password-source / 加密逻辑。特殊密钥类型自理:`k8s` 自己 `ctx.encryptPassword(kubeconfig)`、`ssh` 自有双认证 / 密钥处理;`local` / `serial` 无凭据。

## F5. 详情卡 —— `src/components/asset/detail/<Name>DetailInfoCard.tsx`

实现 `DetailInfoCardProps`(`{ asset, sshTunnelName }`),在注册里引用(`DetailInfoCard: XxxDetailInfoCard`)。复用 `parseDetailConfig<T>`、`DetailSection` / `DetailGrid` / `InfoItem` / `TunnelInfo`(`./InfoItem`)、`MASKED_SECRET` / `ENABLED_VALUE`(`./utils`)。详情面板按 `getAssetType(...).DetailInfoCard` 选卡,无 type-string 分支。

## F6. i18n —— `src/i18n/locales/{en,zh-CN}/common.json`(namespace `common`)

**两个 locale 都要加**(无跨语言兜底):

- **`nav.<type>`** —— `AssetTypeDefinition.label` 引用的标签。
- **`asset.*` 字段标签** —— section 里 `t(...)` 用到的(`asset.host` / `asset.port` / 类型特有如 `asset.redisDatabase` 等)。
- **`asset.formMissing*`** —— section 经 `onValidityChange` 返回的校验原因 key(如新必填字段需自己的 `asset.formMissingXxx`)。
- 若有策略:`policy.titleKey` / `hintKey` / `testPlaceholderKey` / 各 field 的 `labelKey` / `placeholderKey`。

## 前端清单(有序)

1. `src/lib/assetTypes/<newtype>.ts` 调 `registerAssetType` — **新文件**
2. `src/lib/assetTypes/index.ts` 加 `import "./<newtype>";` — **改共享(唯一不可避免)**
3. `src/lib/assetTypes/__tests__/registry.test.ts` 把类型加进有序断言 — **改共享**
4. `src/components/asset/<Name>ConfigSection.config.ts` 纯 build/parse — **新文件**
5. `src/components/asset/__tests__/<Name>ConfigSection.config.test.ts` golden — **新文件**
6. `src/components/asset/<Name>ConfigSection.tsx` forwardRef 组件(带凭据则复用 `useAssetCredential` + `PasswordSourceField`)— **新文件**
7. `src/components/asset/detail/<Name>DetailInfoCard.tsx` — **新文件**
8. `src/i18n/locales/en/common.json` + `zh-CN/common.json` 加 key — **改共享**
9. (可选装饰)`AssetForm.tsx` 的 `DEFAULT_ICONS` 默认图标 + name 占位符 — **改共享**

---

## §7 仍需改共享代码的耦合点

下列能力**还没**注册化,仍按 type-string 分支。**新类型若需要这些能力,必须额外改这些文件**(用 `git grep` 定位当前位置):

| 文件 | 分支于 | 门控的能力 | 何时要改 |
| --- | --- | --- | --- |
| `src/stores/terminalStore.ts`(`transportForAsset`) | `serial`/`local` else `ssh` | 终端 **transport 种类** | 新终端类型且 transport 非 ssh |
| `src/stores/queryStore.ts`(`openQueryTab` + 持久化 rehydrate + `QueryTabMeta.assetType` union) | `database`/`mongodb`/`redis` | per-type **查询 tab meta / config 解析 / 初始 state / 持久化恢复** | `connectAction:"query"` 类型 |
| `src/components/layout/MainPanel.tsx` | `database`/`redis`/`kafka`/`etcd` else mongodb | **渲染哪个查询面板** | 需要自己的查询面板 |
| `src/App.tsx`(`handleConnectAsset`) | `k8s` | 专属页 tab(`k8s-cluster`)而非通用连接 | 需要 bespoke 页(罕见) |
| `src/App.tsx`(`handleOpenFileManager`) | `!== "ssh"` 早退 | 文件管理打开(菜单项本身已注册化,此 handler 仍硬判 ssh) | 需要 SFTP 文件管理 |
| `src/components/asset/CommandPolicyCard.tsx` / `PolicyGroupManager.tsx` | `k8s`/`database`/`mongodb`/`kafka` | 策略编辑器标签集 | 策略需自定义标签 |
| `src/components/ai/MentionList.tsx` / `ai/input/content.ts` / `lib/mentionXml.ts` / `lib/openMentionTarget.ts` | `database` | AI **@-mention** 库 / 表(DB 专属) | 需要 AI mention 自动补全 |

**已注册化、不要再分支**:连接动作分发(`connectAction`)、新标签页(`canConnectInNewTab`)、文件管理菜单项(`canOpenFileManager`)、Test 按钮(`testable`)、类型选择器 / 过滤 / 分组 / 标签(`getBuiltinTypes()` 派生)、详情卡选择、整条 ConfigSection 渲染路径。

> **结论**:**terminal 型**(像 ssh)接入很轻,可能只加注册表 +(若新 transport)`transportForAsset` 一条。**query 型**(像 database/redis)最重——查询路由 / 面板选择 / tab 状态尚未注册化,需改 `queryStore.ts`(3 处 + union)、`MainPanel.tsx` 并新增面板组件。需要 AI-mention 还要改 `ai/*` 那组。

---

## 验证

- **后端**:`go build ./...`、`go test ./internal/...`(改动包加 `-race`)、`golangci-lint run ./internal/...`(用 golangci-lint,不是裸 `go vet`)。连接测试经 `conntest` 注册表 + `System.TestAssetConnection` 分发。
- **前端**(在 `frontend/` 下跑):`npx tsc --noEmit`、`npx vitest run`、`npx eslint src`。`.config.ts` 序列化必须有 golden 字节锁测试;注册顺序由 `registry.test.ts` 锁定。
- **wails 绑定**:`frontend/wailsjs` 是生成物(gitignore);新增/改 binding 后 `wails generate module`,但**校验后端真值看 `internal/app/*.go`**,别把生成的 `.ts` 当真值。
- **可观测验证**(GUI 点不动):用 `opsctl` 跑无头流程,读结构化日志 `logs/opskat.log` 与 DB `opskat.db`(尤其 `audit_logs`)。how-to 见 [`./testing-debugging-guide.md`](./testing-debugging-guide.md)。

## 参考:本接入面的设计演进

资产类型 / policy 全链路注册化重构(把 type-string 分支逐步收敛进上述注册表)的设计与分阶段记录,见 `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` 及同目录 `2026-06-05-assetform-registration-phase4-design.md`(date-named 归档是某次工作的快照,非当前真值——当前真值以本分支代码为准)。
