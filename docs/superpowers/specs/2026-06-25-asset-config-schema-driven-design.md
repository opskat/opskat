# 资产配置重构设计 · 壳 hook + 字段 schema(配置驱动)

> 日期:2026-06-25 ·  状态:设计中 ·  范围:`frontend/` 资产表单各类型 `ConfigSection` 组件层
> 前序:`2026-06-04-asset-type-decoupling-design.md`(注册化)、`2026-06-05-assetform-registration-phase4-design.md`、`2026-06-24-asset-form-tabs-design.md`(标签页化)。本设计是注册化/标签页化之后的下一步:消除每类型一个手写组件的重复。

## 1. 背景与问题

标签页化之后,每个资产类型仍是一个独立的 `*ConfigSection.tsx` 组件(Redis 220 行、Etcd 183、MongoDB 188、Database 299、SSH 369、Kafka 692…),它们共享一套**几乎一字不差的骨架**:

```
forwardRef
  → useState(parse(editAsset) 或 DEFAULTS)
  → patch
  → useAssetCredential(editAsset)
  → useEffect 上报 SectionValidity(各家手写 dep 数组,易错)
  → useImperativeHandle({ buildConfig, buildTestConfig })   // 结构高度雷同
  → <ConfigTabs groups={[...]} />                           // 字段 JSX 大段重复
```

重复集中在三处(已与用户确认是本次要消除的痛点):

1. **每类型的接线样板**:上面这套 ~50–70 行骨架,每个 section 复制一遍,dep 数组各写各的。
2. **字段 JSX 重复堆叠**:host/port 横排、username、password(`PasswordSourceField` 整块在 5 个类型里一模一样)、TLS 开关 + insecure/SNI/CA/cert/key(Redis 与 etcd 完全相同)、数字字段(超时/页大小)、隧道(`ConnectionMethodFields` 整块复用但仍手写挂载)。
3. **新增类型成本高**:加一个常规类型要 copy 一整个组件文件,而不是写一份配置。

> 明确**不**追求的:把 SSH 密钥扫描 / Kafka 动态集群 / Database 驱动联动这类带条件、跨字段副作用、异步加载、动态列表的复杂逻辑也塞进配置模型。强行下沉会让配置 schema 长成「比 JSX 更难维护的 DSL」,得不偿失。

## 2. 目标 / 非目标

**目标**
- 用一个壳 hook 吃掉接线样板,**所有类型**(含复杂型)都用,一次抹平接线重复。
- 用声明式字段 schema + 通用渲染器吃掉常规字段 JSX(text/number/switch/select/segmented/textarea/row,以及高频复刻块 password/tunnel)。
- 常规类型(Redis/Etcd/MongoDB)≈ 一份字段 data + ~15 行胶水;新增常规类型不再 copy 组件。
- 复杂类型用声明式覆盖能覆盖的部分,其余走 `custom` escape-hatch 保留手写 JSX。

**非目标(明确不做)**
- 不改 `*.config.ts` 的 `build*`/`parse*` 序列化器(键序、条件省略等逻辑有微妙且已测覆盖,且各家签名不一)。
- 不改 `credentialConfig` / `proxyConfig` / `useAssetCredential` / `PasswordSourceField` / `ConnectionMethodFields` / `ConfigTabs` 的对外契约。
- 配置驱动重构**本身**不改后端 / config JSON 结构 / `asset_entity.Asset` 模型 → 无数据迁移。**唯一例外**:用户要求顺带补齐的 **K8s SOCKS5 代理支持**(见附录 A)会给 `K8sConfig` 增一个 `omitempty` 的 `proxy` 字段 + 一条后端拨号分支 —— 字段可选、不影响既有数据,**仍无需迁移**;此项作为**独立 feature 单独提交**,与纯重构隔离。
- 不改资产类型注册机制、不在 `AssetForm`(shell)里按类型分支(遵守 OCP)。
- **零视觉变化**:渲染器复刻现有布局/间距/样式,纯前端组件层重构。
- 不新增 i18n key(复用现有 label 键)。
- 不把 `build/buildTest` 胶水继续压薄成「绑定序列化器形态的约定」(会反向耦合不该动的序列化器;用户已确认接受保留显式闭包这一边界)。

## 3. 选定方案(方案对比)

| 方案 | 做法 | 结论 |
|---|---|---|
| A 纯配置 schema | 每类型一份 data,通用渲染器全包(含复杂型) | ✗ 为表达 SSH/Kafka/Database 的条件/副作用/异步/动态,schema 必然膨胀成 DSL —— 正是要避免的 |
| B 只抽壳 hook + 字段组件库 | 消除接线样板,字段仍手写 JSX | 省了接线,字段堆叠仍在 |
| **C 混合(选定)** | 壳 hook(所有类型)+ 声明式字段渲染器(常规字段)+ `custom` escape-hatch(复杂 UI) | 常规型≈纯 data,复杂型 data + 逃逸口;序列化器不动 |

## 4. 架构

新增 3 个件,均在 `frontend/src/components/asset/`。命名与位置待实现时按现有约定微调,但职责如下固定。

### 4.1 `useConfigSection<S>` —— 壳 hook(吃掉接线样板)

收编各 section 雷同的 state / patch / 校验上报 / imperative handle。**凭据留在 hook 外**:K8s 不用凭据(`useAssetCredential` 挂载即发 `ListCredentialsByType` 网络请求,不该空调),Kafka 还有伴随凭据 —— 由 section 自己调 `useAssetCredential`,把 `cred.value` 经 `deps` 喂进 hook。

```ts
function useConfigSection<S>(opts: {
  ref: Ref<AssetFormHandle>;
  editAsset?: asset_entity.Asset;
  onValidityChange: (v: SectionValidity) => void;
  init: (editAsset?: asset_entity.Asset) => S;          // parse(editAsset) 或 {...DEFAULTS}
  validate: (s: S) => SectionValidity;                   // 纯函数,仅依赖 state
  build: (s: S, ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  buildTest?: (s: S, ctx: AssetFormContext) => Promise<AssetTestConfig>;  // 省略 = 不可测(K8s)
  deps?: unknown[];                                      // build/validate 闭包捕获的额外身份(如 cred.value)
}): { state: S; setState: Dispatch<SetStateAction<S>>; patch: (p: Partial<S>) => void };
```

实现要点:
- `const [state, setState] = useState(() => init(editAsset))`;`patch = (p) => setState(s => ({...s, ...p}))`。
- 校验上报:`useEffect` 仅依赖 `[state]`(`validate` 纯函数,只读 state),内部算 `v = validate(state)`,与 **ref 暂存的上次值做浅比较**(`canTest`/`canSave`/`saveDisabledReason`/`invalidGroupKey`),仅在变化时才调 `onValidityChange(v)` 并更新 ref。这取代各家手写的脆弱 dep 数组(如 `[state.host, onValidityChange]`),且避免每次 keystroke 都向 shell 推一个新对象触发多余渲染 —— 行为严格优于现状。
- imperative handle:`useImperativeHandle(ref, () => ({ buildConfig: (c) => build(state, c), buildTestConfig: buildTest ? (c) => buildTest(state, c) : null }), [state, ...deps])`。`buildTest` 省略 → `buildTestConfig` 为 `null`(壳据此判定不可测,等价现状)。
- ESLint exhaustive-deps 在此按现状惯例由 `deps` 手动管理(现有 section 已是手写 `[state, cred.value]`)。

### 4.2 字段 schema + `<Fields>` 渲染器(吃掉常规字段 JSX)

字段描述符联合类型;渲染器把每个描述符映射成现有 UI(复刻 `Field` 包裹、数字框隐藏 spin 的 class、`row` 的 `flex items-end gap-3` 横排、testid 透传),保证零视觉变化。

```ts
type FieldDesc<S> = (
  | { kind: "text";      key: keyof S; label: string; placeholder?: string; required?: boolean; width?: string; testid?: string }
  | { kind: "number";    key: keyof S; label: string; placeholder?: string; min?: number; width?: string; testid?: string }
  | { kind: "switch";    key: keyof S; label: string }
  | { kind: "select";    key: keyof S; label: string; options: { value: string; label: string }[]; testid?: string }
  | { kind: "segmented"; key: keyof S; label?: string; options: SegmentedOption<string>[] }
  | { kind: "textarea";  key: keyof S; label: string; rows?: number; hint?: string; placeholder?: string }
  | { kind: "row";       fields: FieldDesc<S>[] }                                  // 横排
  | { kind: "password";  placeholder?: string; secretLabel?: string; selectSecretLabel?: string }  // → PasswordSourceField,读 ctx.cred
  | { kind: "tunnel";    tunnelOptionLabelKey?: string; tunnelSelectLabelKey?: string; excludeIds?: number[] }  // → ConnectionMethodFields(直连/跳板/代理三选)
  | { kind: "custom";    render: (s: S, patch: (p: Partial<S>) => void) => ReactNode }  // ★ escape hatch
) & { visibleWhen?: (s: S) => boolean };   // 任意 kind 可带;false 则不渲染
```

- `label` 为 i18n key,渲染器内部 `t()`。
- `visibleWhen` 覆盖:TLS 子项(`s => s.tls`)、Database 驱动联动(`s => s.driver === "postgresql"`)、sqlite 与 host 分支等**简单**条件。
- `password` / `tunnel` 是高频复刻块(5 个类型一模一样),作为一等 kind 由渲染器从 `ctx = { cred, editAsset }` 取依赖。这属于**常规重复**,不是复杂逻辑下沉。
- K8s 现状仅"直连 or 单个 SSH 隧道",无代理。本次**附带给 K8s 补齐 SOCKS5 代理支持**(见附录 A);补齐后 K8s 连接方式与数据库族一致,直接用标准 `{kind:"tunnel"}`(三选),**无需任何 selector-only 特例**(故 tunnel kind 不引入 variant 分支,守住不长成 DSL)。
- `select` 仅用于**无副作用**的简单下拉(如 Database 的 PostgreSQL `sslMode`);Database 的驱动选择带 `applyDriverChange` + `onIconChange` 副作用,走 `custom`。
- 渲染器签名:`<Fields fields={FieldDesc<S>[]} state={S} patch ctx={{ cred?, editAsset? }} />`。

### 4.3 组 schema 助手 `useConfigGroups`

把组级 schema 转成现有 `<ConfigTabs>` 吃的 `ConfigGroup[]`;声明式组自动包成 `<Fields>`,逃逸口组沿用 `render`。

```ts
type ConfigGroupSchema<S> =
  | { key: string; label: string; badge?: number; fields: FieldDesc<S>[] }   // 声明式
  | { key: string; label: string; badge?: number; render: () => ReactNode }; // escape hatch(等于现状)

function useConfigGroups<S>(
  schema: ConfigGroupSchema<S>[],
  args: { state: S; patch: (p: Partial<S>) => void; ctx: { cred?: UseAssetCredential; editAsset?: asset_entity.Asset } }
): ConfigGroup[];   // 声明式组 → { ...g, render: () => <Fields fields={g.fields} {...args} /> }
```

`<ConfigTabs>` 完全不变(已是消费 `ConfigGroup[]` 的稳定原语)。

## 5. 落地后 section 模式

### 5.1 常规型(Redis 示例)

`*.config.ts`(`RedisFormState`/`REDIS_DEFAULTS`/`buildRedisConfig`/`parseRedisConfig`)**不动**。组件收缩为:

```tsx
const cred = useAssetCredential(editAsset);
const { state, patch } = useConfigSection<RedisFormState>({
  ref, editAsset, onValidityChange,
  init: a => (a ? parseRedisConfig(a.Config, a.sshTunnelId || 0) : { ...REDIS_DEFAULTS }),
  validate: s => {
    const ok = !!s.host.trim();
    return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingHost" };
  },
  build: async (s, c) => ({
    configJSON: buildRedisConfig(s, await resolveSaveCredential(cred.value, c.encryptPassword), false, await resolveSaveProxyPassword(s, c.encryptPassword)),
    sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
  }),
  buildTest: async s => ({
    assetType: "redis",
    configJSON: buildRedisConfig(s, resolveTestCredential(cred.value), true, s.proxyPassword),
    password: cred.value.password,
  }),
  deps: [cred.value],
});
return <ConfigTabs groups={useConfigGroups(REDIS_GROUPS, { state, patch, ctx: { cred, editAsset } })} />;
```

`REDIS_GROUPS` 为纯数据(节选):

```ts
const REDIS_GROUPS: ConfigGroupSchema<RedisFormState>[] = [
  { key: "connection", label: "asset.tabConnection", fields: [
    { kind: "row", fields: [
      { kind: "text",   key: "host", label: "asset.host", required: true, placeholder: "example.com", testid: "redis-host-input", width: "flex-1" },
      { kind: "number", key: "port", label: "asset.port", placeholder: "6379", width: "w-[110px]", testid: "redis-port-input" },
    ]},
    { kind: "text",     key: "username", label: "asset.username" },
    { kind: "password" },
    { kind: "number",   key: "database", label: "asset.redisDatabase", min: 0 },
  ]},
  { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  { key: "tls", label: "asset.tabTls", fields: [
    { kind: "switch", key: "tls",           label: "asset.tls" },
    { kind: "switch", key: "tlsInsecure",   label: "asset.redisTlsInsecure",  visibleWhen: s => s.tls },
    { kind: "text",   key: "tlsServerName", label: "asset.redisTlsServerName", placeholder: "redis.example.com", visibleWhen: s => s.tls },
    { kind: "text",   key: "tlsCAFile",     label: "asset.redisTlsCAFile",     placeholder: "/path/to/ca.pem",   visibleWhen: s => s.tls },
    { kind: "text",   key: "tlsCertFile",   label: "asset.redisTlsCertFile",   placeholder: "/path/to/client.crt", visibleWhen: s => s.tls },
    { kind: "text",   key: "tlsKeyFile",    label: "asset.redisTlsKeyFile",    placeholder: "/path/to/client.key", visibleWhen: s => s.tls },
  ]},
  { key: "advanced", label: "asset.tabAdvanced", fields: [/* 两个数字 + key 分隔符,同上 */] },
];
```

> `init/validate/build/buildTest` 这 ~15 行是连接 state↔序列化器↔凭据的不可约胶水。因序列化器签名各异且**不动**,有意保留为显式闭包(见 §2 非目标末条)。Etcd / MongoDB 同此模式。

### 5.2 复杂型策略(每类型都用 hook;声明式 + `custom`)

- **Database**:host/port/user/password/库名/params/readOnly 走声明式 + `visibleWhen`(sqlite vs host、`sslMode` 限 postgresql、TLS 开关限 mysql/mssql);**驱动 Select**(触发 `applyDriverChange` + `onIconChange`)与 **sqliteSource 段控件**(切换时联动 `connectionType`/`sshTunnelId`)走 `custom`。
- **SSH**:host/port/username/password/authType 段控件声明式;**密钥扫描整块**(异步 `ListCredentialsByType`/`ListLocalSSHKeys`、勾选、文件浏览、passphrase)走 `custom`;tunnel 用 `{ kind: "tunnel", tunnelOptionLabelKey: "asset.connectionJumpHost", tunnelSelectLabelKey: "asset.selectJumpHost", excludeIds }`;凭据用 `useAssetCredential(editAsset, parseSSHPasswordCredentialConfig(...))`。
- **K8s**:**不调** `useAssetCredential`(`deps: [editAsset]`;代理密码经 state + `resolveSaveProxyPassword` 处理,非托管凭据);`K8sFormState` 扩入 `ConnectionFormFields`;kubeconfig 揭示/加密整块走 `custom`;namespace/context 声明式;tunnel 走标准 `{kind:"tunnel"}`(三选,因附录 A 已补齐代理);`buildTest` 省略 → 不可测。
- **Kafka**:收益**不止 hook**。其 **TLS 标签与 Redis/etcd 形状相同**(insecure/SNI/CA/cert/key),走声明式;SASL/简单字段同样能。仅**变长 + 嵌套**部分坚持 `custom`:动态 **broker 列表**、**N 个 Connect 集群**(各一个 `KafkaConnectClusterEditor`)、伴随项的 **enabled 开关**(开启 Connect 会播种一个默认集群,带副作用);其中伴随认证子表单的重复**已由本地组件 `KafkaCompanionAuthFields` 解决**(Schema Registry 与各 Connect 集群共用),无需下沉到全局 schema。Connect 集群数走 `ConfigGroupSchema.badge`。坚持 `custom` 的原因见 §4.2:声明式 schema 有意不建模变长数组 / 嵌套重复子表单(否则需 array-of-subschema = DSL 膨胀)。
- **Serial / Local**:单分组,`ConfigTabs` 自动退化为无标签单面板;字段尽量走声明式,串口扫描等设备相关走 `custom`。

要点:**所有类型都用 hook**(接线重复一次抹平);常规型字段全 data;复杂型 data + escape-hatch。

## 6. 不动的边界(复用清单 / grep before writing)

- 序列化:各 `*.config.ts` 的 `build*`/`parse*` —— 不动。
- 凭据:`useAssetCredential` / `credentialConfig`(`resolveSave/TestCredential`)—— 不动,由 section 调用、经渲染器 `ctx` 与 hook `deps` 传递。
- 代理/隧道:`proxyConfig`(`resolveSaveProxyPassword` 等)/ `ConnectionMethodFields` —— 不动,`{kind:"tunnel"}` 内部挂载。
- 密码控件:`PasswordSourceField` —— 不动,`{kind:"password"}` 内部挂载。
- 标签容器:`ConfigTabs` / `ConfigGroup` —— 不动。
- 字段原语:`Field` / `FieldLabel` / `Segmented`(`fields.tsx`)—— 不动,被渲染器复用。
- shell:`AssetForm` 仍只挂 `<sectionDef.ConfigSection>`、读单个聚合 `SectionValidity`、`buildConfig/buildTestConfig` 签名不变,**不按类型分支**。

## 7. 测试策略(TDD,vitest + RTL)

- **回归网(硬约束)**:现有 `*ConfigSection.test.tsx`(testid + 交互 + `buildConfig` 输出 JSON + 校验态)与 `*.config.test.ts`(序列化器)在重构全程保持绿。这反向约束渲染器必须透传 testid、复刻视觉、保持 build 行为。先跑一遍记录基线,迁移后逐个对齐。
- **新增单测**:
  - `useConfigSection`:初次挂载即上报校验;state 变化触发的校验仅在结果变化时上报(浅比较守卫);`buildConfig`/`buildTestConfig` 读到最新 state 与 deps;`buildTest` 省略时 `buildTestConfig === null`。
  - `<Fields>` 渲染器:各 kind 正确渲染与回写(text/number/switch/select/segmented/textarea);`row` 横排;`visibleWhen=false` 不渲染;`password` 从 ctx.cred 取值并回写;`tunnel` 渲染 `ConnectionMethodFields`;`custom` 调用 render。
  - `useConfigGroups`:声明式组包成 `<Fields>`;逃逸口组沿用 `render`;`badge` 透传。
- 无新增 i18n key → 无 i18n 测试改动。

## 8. 迁移顺序(每步独立可发,沿用前序阶段「逐类型 PR」风格)

1. 基础设施 + 单测:`useConfigSection`、`<Fields>` 渲染器、`useConfigGroups`(本步不改任何 section,纯新增 + 测试)。
2. 先迁一个常规型 **Redis** 验证模式(回归测试须全绿)→ Etcd → MongoDB。
3. 迁复杂型:Database → SSH → **K8s**(含附录 A 代理 feature,**独立提交**:先补后端代理分支 + `K8sConfig.Proxy` 字段 + 序列化器 round-trip 测试,再做配置化迁移 —— 此时 tunnel 直接用标准三选)→ Serial / Local → **Kafka**(最大,放最后)。
4. 收尾:确认无残留重复骨架;`adding-an-asset-type.md` 若提及「copy 一个 ConfigSection」则更新为「写一份字段 schema + ~15 行胶水」。

## 附录 A:K8s SOCKS5 代理支持(feature,非纯重构)

**动机**:用户要求顺带给 K8s 补齐与数据库族一致的代理能力。补齐后 K8s 连接方式 = 直连 / SSH 隧道 / SOCKS5 代理三选,前端直接复用标准 `{kind:"tunnel"}`,顺带消除了 §4.2 本要引入的 selector-only 特例。

**可行性(已核实代码)**:
- `internal/pkg/k8s/client.go::buildClient` 已支持 `WithDial(...)` 注入自定义拨号;注入后 client-go 走该 dialer 并禁用 HTTP proxy(`config.Dial = dial; config.Proxy = nil`)。
- `internal/app/k8s/k8s_ops.go::k8sClientOptions(asset, cfg)` 现已用 `WithDial` 接 SSH 隧道(`asset.SSHTunnelID`),只差一个代理分支。
- 共享代理拨号 `internal/pkg/socksdial.Dial(ctx, *asset_entity.ProxyConfig, addr)` 已存在(数据库族经 `connpool/dialer.go::proxyDialFunc` 在用)。
- K8s ops 已有解密设施:`k8s_ops.go` 用 `h.ResolvePassword(ctx, asset)` 解 kubeconfig,代理密码同法解密(`ProxyConfig.Password` 入 `socksdial` 前须明文,与数据库族约定一致)。

**后端改动**:
1. `asset_entity.K8sConfig` 增 `Proxy *ProxyConfig \`json:"proxy,omitempty"\``(可选,不迁移)。
2. `k8sClientOptions`:`asset.SSHTunnelID == 0 && cfg.Proxy != nil` 时,解密 `cfg.Proxy.Password` 为明文,`opts = append(opts, k8spkg.WithDial(func(ctx,_,address){ return socksdial.Dial(ctx, proxy, address) }))`。隧道与代理**互斥、隧道优先**(镜像其它 entity 的 `// SOCKS5 代理(与 SSH 隧道互斥,隧道优先)` 约定)。

**前端改动**:
1. `K8sFormState` 扩入 `ConnectionFormFields`(`...CONNECTION_DEFAULTS`)。
2. `parseK8sConfig`:用 `parseConnectionFields(cfg.proxy, assetTunnelId)` 派生 `connectionType` 与 proxy 字段(与 Redis/etc. 同模式),入参由 section 传 `editAsset.sshTunnelId`。
3. `buildK8sConfig`:`connectionType==="proxy"` 时经 `buildProxyJSON` 写 `proxy` 块;`sshTunnelId` 改为 `connectionType==="jumphost" ? id : 0`;build 闭包加 `resolveSaveProxyPassword`。
4. section 的隧道标签从单 `AssetSelect` 换成标准 `{kind:"tunnel"}`。

**测试(TDD)**:
- 后端:`k8sClientOptions` 在仅配代理时返回含 `WithDial` 的 opts;隧道+代理并存时隧道优先(单测,不必实拨)。
- 前端:`buildK8sConfig`/`parseK8sConfig` 代理 round-trip(golden)+ `connectionType` 派生;现有 K8s 测试保持绿。
- 验证:K8s 无"测试连接"按钮(`buildTest` 为 null),按 AGENTS.md「观察验证」经 `opsctl` / 结构化日志确认经代理可拉到集群信息。

**提交隔离**:作为**独立提交/PR**,置于 K8s 迁移步骤内或紧邻,使纯重构与该 feature 可分别 review / 回滚。

## 9. 风险 / 待确认

- **渲染器表达力边界**:声明式只覆盖「无副作用、条件可由 `visibleWhen` 表达」的字段;任何跨字段副作用/异步/动态列表一律走 `custom`。实现时若发现某「常规」字段需要副作用,宁可降级为 `custom`,**不**给 schema 加新能力(守住不长成 DSL)。
- **零视觉回归**:渲染器需逐像素复刻现有间距/宽度/spin 隐藏 class;以现有 `*ConfigSection.test.tsx` + 人工对照截图把关。
- **校验上报时机**:hook 用浅比较守卫避免多余 shell 渲染;须确保初次挂载仍上报一次(否则保存按钮初始态错误)。
- **`deps` 手动管理**:沿用现状惯例(手写 `[state, cred.value]`),exhaustive-deps 告警按现有约定处理;迁移时逐个核对 build 闭包捕获的身份是否都进了 `deps`。
- **Kafka 的 custom 边界**:收益 = 接线统一(hook)+ TLS/简单字段声明式;仅变长列表(brokers / connect 集群)与带副作用的 enabled 开关保留 `custom`。伴随认证重复已由本地组件 `KafkaCompanionAuthFields` 解决,本次不强行把变长/嵌套子表单下沉到全局 schema(守住不长成 DSL)。
- **K8s 代理(附录 A)触及连接拨号热路径**:严格遵 TDD + entity 注释的"隧道优先"互斥约定;K8s 无测试连接按钮,须经 `opsctl`/日志观察验证经代理实连;作为独立提交隔离风险与回滚面。
