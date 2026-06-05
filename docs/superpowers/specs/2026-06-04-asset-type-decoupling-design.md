# 资产类型 / policy 全链路注册化 — 重构设计

- **Issue**: #130 [Feature] 重构组件
- **日期**: 2026-06-04
- **状态**: 设计已敲定,待写实现计划

## 背景与问题

资产类型(SSH / Database / Redis / MongoDB / Kafka / K8s / etcd / Serial / Local)与 policy 的逻辑**横向耦合**:加一个新类型今天要在前端 ~9 处、后端 ~7 处分散改动,且多处是"照抄上一个类型再改",容易抄歪、引入副作用。issue 的诉求是把这些散点收敛成"每类型一处注册",并在重构后用一个 skill 固化接入流程。

代码里**已有注册式骨架**,但只覆盖一部分:
- 后端 `internal/assettype/`(`AssetTypeHandler` + `init()` 里 `Register()`)— 覆盖 port / safeview / password / 默认 policy / AI 工具的 create/update args。
- 前端 `frontend/src/lib/assetTypes/`(`registerAssetType()`)— 覆盖 icon / 详情卡 / policy 定义。

骨架之外仍是硬编码分支,这是本次重构的主战场。

### 三个关键发现(塑造了设计)

1. **连接测试 binding 七个签名各不相同**,无法统一调用:
   - `internal/app/ssh/ssh_ops.go:319` `TestSSHConnection(testID, configJSON, plainPassword) error`
   - `internal/app/kafka/kafka_ops.go:18` `TestKafkaConnection(testID, configJSON, plainPassword) error`
   - `internal/app/query/query_ops.go:68/107/326` `TestDatabaseConnection / TestRedisConnection / TestMongoDBConnection(testID, configJSON, plainPassword) error`
   - `internal/app/serial/serial_ops.go:111` `TestSerialConnection(testID, configJSON) error`(无密码)
   - `internal/app/etcd/etcd_ops.go:20` `EtcdTestConnection(assetID int64) error`(**outlier**:名字顺序、入参都不同)

2. **"policy type" 一词在代码里有三套互不相同的词表**,是混乱的核心来源:
   - 前端 `PolicyDefinition.policyType`:`ssh` / …(`frontend/src/lib/assetTypes/ssh.ts:13`)
   - 后端 tester `PolicyTestInput.PolicyType`:`ssh / database / redis / k8s / etcd`(`internal/ai/policy/policy_tester.go:18,47`)
   - 后端 group `policy_group_entity.PolicyType`:`command / query / redis / mongo / kafka / etcd`(`internal/model/entity/policy_group_entity/policy_group.go:15-20`)

3. **覆盖缺口 / 潜在 bug**:`mongo`/`kafka` 有 builtin policy groups(`policy_group.go:259-393`)却**没进** `TestPolicy` 的 switch(`policy_tester.go:47-58`);`k8s` 有 test 路径却没有自己的 group policyType 常量。更具体地:app 层 `TestPolicyRule` 的 switch(`internal/app/system/asset.go:55`)只认 `ssh/database/redis/k8s`,对 `etcd/mongo/kafka` 走 `default` 直接报 `unsupported policy type` —— 而前端 `PolicyTestPanel` 对 etcd/mongo/kafka 资产**确实**会发这些 policyType。结果:编辑 etcd/mongo/kafka 策略后点"测试"(PolicyJSON 非空)当前直接报错;etcd 甚至已有可用的 `testEtcdPolicy` 被这道 app 层闸门挡住。注册表化会强制把这些缺口暴露出来(每个注册的 kind 必须给出 test 函数)。

### 加新类型今天要动的散点(touch-point map)

**前端**
- `frontend/src/lib/assetTypes/<type>.ts` — `registerAssetType()`(icon/detailCard/policy)✅ 已注册式
- `frontend/src/lib/assetTypes/index.ts:18-26` — 副作用 import
- `frontend/src/lib/assetTypes/options.ts:36-118` — `BUILTIN_OPTIONS`(label/aliases/category)❌ 第二套注册表
- `frontend/src/components/asset/AssetForm.tsx`(2187 行)❌ 最大耦合点:
  - 编辑态回填:每类型一个 `JSON.parse(asset.Config)` + `set*` 块(~546-792)
  - 类型切换重置:`951-960`
  - 保存序列化:`handleSave` 每类型一个 config 构建分支(~1394+)
  - 连接测试:`handleTest*Connection` 每类型一个
  - `isTestableAssetType`(~1662)+ 测试按钮禁用逻辑
  - 表单渲染:`{assetType === "x" && <XConfigSection/>}`(~1833-2130)
- `frontend/src/components/layout/AssetTree.tsx:1151` — `asset.Type === "ssh"` 的文件管理特例

**后端**
- `internal/assettype/<type>.go` — `Register()` + `RegisterDefaultPolicy()` ✅ 已注册式
- `internal/model/entity/asset_entity/asset.go` — 类型常量、`IsXxx()` 谓词族、`GetConfig()` 的 switch dispatcher
- `internal/ai/policy/policy_tester.go:47-58` — `switch PolicyType` + `PolicyTestInput.Current*` 五个硬字段(23-27)+ 每类型 test/merge 函数
- `internal/model/entity/policy_group_entity/policy_group.go` — `BuiltinGroups()` 大数组(102-429)+ `Validate()` 的 switch(48-51)+ policyType 常量
- `internal/app/<module>/*_ops.go` — 七个签名不一的连接测试 binding

## 目标 / 非目标

**目标**:把上述散点收敛成"每个资产类型一处注册、每个 policyKind 一处注册",新增类型时**只**改它自己的注册文件 + 它的 config section 组件,其余文件零改动。

**非目标**:
- 不做 schema 驱动表单(已决定走组件注册,沿用 `DetailInfoCard` 模式,bespoke `*ConfigSection.tsx` 保留)。
- 不抹掉类型化 config getter(`GetSSHConfig()` 等类型安全,不是耦合)。
- 不顺手做无关重构 / 改名扫荡 / 格式化(遵守 AGENTS.md 的 in-scope 约束)。

## 决策摘要

| 维度 | 决策 |
|---|---|
| 范围 | 全链路统一(理想 OCP) |
| 前端表单 | 组件注册(沿用 `DetailInfoCard` 模式) |
| 后端 policy | 独立的 `policyKind` 注册表(policy 与 asset 解耦,各类型声明所用 kind) |
| skill | 重构稳定后写,作为收尾 |
| 阶段顺序 | 后端优先 |

## 目标架构

### 第 0 节 · 词表统一(地基)

确立**唯一**的 `policyKind` 词表:`command / query / redis / mongo / kafka / k8s / etcd`。三处全部对齐到它(group policy 类型、tester dispatch key、前端 `PolicyDefinition.policyType`)。每个资产类型声明"我用哪个 policyKind"(ssh/serial/local → `command`,etcd → `etcd`,database → `query` …),policy 轴与 asset 轴解耦但有明确映射。

> 注:`command` 现同时承载 SSH 与 K8s 的 builtin 命令组(`policy_group.go:145,160`),而 K8s 资产策略另有 `K8sPolicy`。统一时需明确:K8s 资产 policyKind = `k8s`(用 `K8sPolicy`),它**额外引用** `command` 类内置组的能力保留。

### 第 1 节 · 后端 policyKind 注册表(独立轴)

**关键 layering 约束(实现核实)**:`internal/ai/policy → policy_group_entity`(单向,`policy_group_resolve.go` 依赖 `FindBuiltin`)。而 builtin groups 在 `policy_group_entity` 的 `init()` 里被消费(`builtinMap`)。因此 builtin groups **不能**由 ai/policy 层的 handler 反向供给(会成环 / init 顺序错)。结论:**拆成两个注册表,同一 `policyKind` 词表**:

- **注册表 A(entity 层)** — builtin groups per kind。在 `policy_group_entity` 内把 `BuiltinGroups()` 大数组(102-429)按 kind 拆分贡献;合法 kind = 已注册 kind(替代 `Validate()` 的 switch 48-51 与 `hasExtensionPolicyType`)。纯数据,留在 entity 层符合 DIP。
- **注册表 B(ai/policy 层)** — 测试/解码 handler:

```go
// internal/ai/policy
type policyKindHandler struct {
    decode func(raw []byte) (any, error)                                  // 替代 app 层 per-type Unmarshal
    test   func(ctx, current any, groups []*group_entity.Group, cmd string) PolicyTestOutput
}
```

（merge/Effective 已内联在各 `testXxx` 函数里,无需进 handler 接口。）

改动:
- `policy_tester.go` 的 `switch`(47-58) → 注册表 B 查表;各 handler 委托现有 `testSSHPolicy`/`testQueryPolicy`/… 保持行为。
- `PolicyTestInput` 的 `CurrentSSH/Query/Redis/K8s/Etcd` 五个硬字段(23-27) → `PolicyKind string` + `Current any`(由 handler `decode` 在 app 边界产出)。
- app 层 `TestPolicyRule`(`asset.go:42-101`)的 per-type Unmarshal switch → `ResolvePolicyKind` + `DecodeCurrentPolicy`。**顺带修复 etcd**(已有 `testEtcdPolicy`,注册后非空 JSON 也能测)。
- mongo/kafka:**本阶段(1a)不注册**(见阶段 1b)。未注册 kind 经 `ResolvePolicyKind` 返回 false → app 仍报 `unsupported policy type`,行为不变。〔1b 实现核实:`EffectiveMongoPolicy`/`EffectiveKafkaPolicy`/`checkMongo|KafkaPolicyRules`/`ResolveMongo|KafkaGroups` 早已存在,1b 实际只需补测试链路 merge + dispatch + 注册,无需新增 `Effective*`。〕

### 第 2 节 · 后端 assettype handler 收口

`AssetTypeHandler` 增补:
- `PolicyKind() string` — dispatch 不再靠猜。
- `TestConnection(ctx, configJSON string, plainPassword string) error` — 七个散落 binding 收敛到一个 `App.TestAssetConnection(testID, assetType, configJSON, plainPassword)`,内部查表分发;etcd outlier 拉齐到统一签名。
- `Asset.GetConfig()` 的 switch dispatcher 走注册表。

**保留**:类型化 getter(`GetSSHConfig()` 等,类型安全)与 `IsSSH()` 廉价谓词族 — 不是耦合,不在本次清除范围。

### 第 3 节 · 前端注册表合并

把 `options.ts:36-118` 的 `BUILTIN_OPTIONS` 元数据(label/i18nKey/aliases/category)折进 `AssetTypeDefinition`;`BUILTIN_OPTIONS` 改为从 registry 派生。扩展追加逻辑(`getAssetTypeOptions`)不变。**一处声明,而非两处。**

### 第 4 节 · 前端 AssetForm 组件注册化(最大块)

`AssetTypeDefinition` 扩出表单契约:

```ts
ConfigSection: ComponentType<ConfigSectionProps>;   // 已有 bespoke 组件直接挂
defaults: { port: number; username?: string };
parseConfig(asset): FormState;                       // 编辑态回填(替代 546-792)
buildConfig(formState): { configJSON: string; /* 凭据处理结果 */ };  // 保存序列化(替代 handleSave 分支)
testConnection(formState): Promise<TestResult>;      // 替代 7 个 handleTest*Connection
validateForTest(formState): boolean;                 // 替代 isTestableAssetType + 禁用逻辑
```

`AssetForm` 瘦身成通用壳:共享 chrome(名称 / 分组 / 图标 / SSH 隧道)+ 查 def 渲染 `def.ConfigSection`、调 `def.parseConfig/buildConfig/testConnection/validateForTest`。**零 `assetType === "x"`。** 预计从 2187 行砍到几百行。

> 共享字段如何在通用壳与 ConfigSection 间传递,是 ConfigSectionProps 的接口设计点 — 留待实现计划细化(候选:受控的 `formState` + `onChange`,或各 section 自持局部 state 经 ref 暴露 `parse/build`)。

### 第 5 节 · AssetTree 收尾

`AssetTree.tsx:1151` 的 `asset.Type === "ssh"` 文件管理特例 → 注册表能力位(如 `canOpenFileManager`)或 action 注册,去掉硬编码。

### 第 6 节 · skill 收尾

重构稳定后,写 `.claude/skills/` 下"接入新资产类型" skill,内容直接引用上面 6 个注册点。同步更新 `AGENTS.md` 与 `docs/DEVELOP.md` 的资产类型章节(遵守 `docs/DOC-MAINTENANCE.md`)。

## 阶段拆分(后端优先,每阶段独立可交付、行为保持,各自 spec→plan→PR)

0. **词表统一** — 引入 `policyKind` 词表(`command/query/redis/mongo/kafka/k8s/etcd`)+ asset/frontend→kind resolver。地基,体量小。**与 1a 合并交付**(词表是注册表的前置,二者强耦合)。
1. **后端 policyKind 注册表**(因 layering 与机制成熟度拆为三个独立子计划):
   - **1a** 测试链路 de-switch(注册表 B):迁移现有 5 个 kind(command/query/redis/k8s/etcd)进注册表,改 `PolicyTestInput`,改 app 边界,顺带修复 etcd。行为保持(仅 etcd 为修复)。**← 本轮先做这个 plan。**
   - **1b** 补齐 mongo/kafka:`Effective*`/merge/check 机制已就绪(5/31 已落地),只需补测试链路 `mergeMongo|KafkaPoliciesForTest` + `testMongo|testKafka` 并在 `policy_kind.go` 注册;注册后 app 层自动放行(修复 mongo/kafka 编辑策略测试闸门,同 1a 修 etcd)。新增行为,TDD。
   - **1c** builtin groups 拆分(注册表 A,entity 层):把大数组按 kind 拆,去掉 `Validate()` switch。独立小清理。
2. **后端 assettype 收口** — 统一连接测试 binding(`TestAssetConnection`)、`GetConfig` 走注册表、加 `PolicyKind()`。需 `wails generate` 重生 binding。
3. **前端注册表合并** — `options.ts` 元数据折进 `AssetTypeDefinition`。小且安全。
4. **前端 AssetForm 组件注册化** — 表单契约 + 通用壳重写。最大、最险,放在后端契约稳定之后。
5. **AssetTree action 注册** — 去掉 ssh 文件管理硬编码。
6. **skill + 文档** — 收尾,固化接入流程。

各阶段大体独立;阶段 4 的 `testConnection` 在阶段 2 完成前可暂调现有 per-type binding,阶段 2 后切到统一 binding。

## 验证策略(对齐 AGENTS.md 的 TDD / 观测验证)

- 重构**行为保持**:缺测试处先补 characterization test;`go test` / `vitest` 全程绿。
- **决定性验收**:重构后加一个**一次性 throwaway 资产类型**(如 `telnet`),只动它一个注册文件 + 一个 config section,确认端到端(树 / 表单 / 保存 / policy)全通、其它文件零改动 —— 证明 seam 真断开。skill 随后照这套触点写。
- 后端 GUI 不可点:经 `opsctl` headless 或读 `logs/opskat.log`、`opskat.db`(尤其 `audit_logs`)做观测验证(见 `docs/testing-debugging-guide.md`)。

## 风险 / 待实现计划细化的点

- **ConfigSectionProps 接口形态**(受控 vs ref 暴露)— 影响 AssetForm 与各 section 的边界,阶段 4 实现计划定。
- **policyKind 解码的类型安全**:`json.RawMessage` + `Decode` 把编译期类型检查换成运行期;需保证每 kind 的 round-trip 测试。
- **K8s 双重 policy 关系**(`k8s` kind 与 `command` 内置组)需在阶段 1 明确,避免回归。
- **wails binding 重生**:阶段 2 改 binding 签名后,前端 `wailsjs/` 是 gitignore 生成物,按 CI 流程 `wails generate`(见 reference:Wails binding/CI flow)。

## 阶段 1a 完成记录(2026-06-04,最终评审 Ready to merge)

落地于 3 个 commit(`policy_kind.go` 注册表 + `TestPolicy` 去 switch + app 边界改 resolver),全量 `go test ./internal/...` + `-race` + `golangci-lint` 全绿,etcd 闸门 bug 已修复且为唯一行为变化。评审给出 3 条**留给后续阶段**的非阻塞备忘:

- **type→kind 知识三处重复,需保持同步**:`assetTypeToKind`(`policy_kind.go`)、`policy_group_entity.PolicyType*`、前端各 `assetTypes/*.ts` 的 `policyType` 字段 + `RegisterDefaultPolicy`。阶段 1c/2 应考虑让 kind 从 `assettype` handler 派生(`PolicyKind()`),而非手维护 map。
- **阶段 1b 接入 mongo/kafka**:`Effective*`/merge/check 已存在,只需补测试链路 `mergeMongo|KafkaPoliciesForTest` + `testMongo|testKafka` 并在 `policy_kind.go` 的 `init()` 注册,app 层无需改动;但要**同步更新**断言 mongo/kafka 未注册的负向测试(`policy_kind_test.go`、`policy_dispatch_test.go`)。
- **未注册 kind 的两种返回不一致**:`DecodeCurrentPolicy` 对未注册 kind 返回 error,而 `TestPolicy` 返回 `NeedConfirm`。当前 app 流程先走 `ResolvePolicyKind` 不会撞上;若将来出现 `TestPolicy` 的非 app 直接调用方,需留意未注册 kind 会静默得到 `NeedConfirm` 而非报错。

## 阶段 1b + 1c 完成记录(2026-06-04)

计划见 `docs/superpowers/plans/2026-06-04-policykind-registry-phase1b-1c.md`,落地于 2 个实现 commit + 1 个文档 commit,与阶段 1a 同分支(`refactor/asset-type-decoupling-130`)累加。

- **阶段 1b(mongo/kafka 接入测试注册表)**:核实发现 spec 原假设有误 —— mongo/kafka 的 `Effective*`/expand/merge/check/resolve 机制(`policy_effective.go`、`mongo_policy.go`、`kafka_policy.go`、`policy_group_resolve.go`)**早已存在**。故 1b 实际范围收窄为:`policy_tester.go` 新增 `testMongoPolicy`/`testKafkaPolicy`(镜像 `testRedisPolicy`,组通用规则用 `MatchCommandRule`,与 runtime `checkMongoDBPermission`/`checkKafkaPermission` 对齐)+ `mergeMongo|KafkaPoliciesForTest`(镜像 `mergeQuery|RedisPoliciesForTest`),并在 `policy_kind.go` 的 `init()` 注册两个 handler。**唯一行为变化**:mongo/kafka 资产编辑非空策略点"测试"不再报 `unsupported policy type`(app 层经 `ResolvePolicyKind`+`DecodeCurrentPolicy` 自动放行,无需改 `asset.go`),与 1a 修 etcd 同性质。负向测试已同步翻正(7 个 kind 全注册、bogus 兜底)。
- **阶段 1c(内置组按 kind 拆分)**:`policy_group_entity` 引入 `builtinGroupsByKind` 注册表 + `registerBuiltinGroups` + `builtinKindOrder`;`BuiltinGroups()` 改为按 kind 顺序拼装;`Validate()` 的 `switch` 改为 `isBuiltinKind(派生自注册数据) || hasExtensionPolicyType`。21 条内置组字面量原样移入对应 `registerBuiltinGroups` 调用,**行为完全保持**(既有 `TestBuiltinGroups` 总数 21 + 各 kind 计数、`TestPolicyGroup_Validate` 锁定)。
- **验证**:`go test ./internal/ai/policy ./internal/app/system ./internal/model/entity/policy_group_entity ./internal/service/policy_group_svc` 全绿,`-race`(policy)干净,`golangci-lint` 0 issues。
- **仍留给后续阶段**:阶段 1a 备忘的 type→kind 三处重复(`assetTypeToKind` / `PolicyType*` / 前端 `policyType`)未在本轮收敛,留待阶段 2 让 kind 从 `assettype` handler 的 `PolicyKind()` 派生;builtin groups 注册表目前仍在 `policy_group_entity` 内集中声明(layering 要求其留在 entity 层),阶段 2/6 接入新类型时按此 seam 追加。

## 阶段 2a 完成记录(2026-06-04)

计划见 `docs/superpowers/plans/2026-06-04-assettype-policykind-phase2a.md`,与前阶段同分支累加。**阶段 2 拆为 2a(后端纯)+ 2b(跨前端)**,本轮只做 2a。

- **做了什么(2a)**:`AssetTypeHandler` 增补 `PolicyKind() string`,9 个内置 handler 各自声明所用 kind;最底层、无 gorm 的 `entity/policy` 镜像既有 `RegisterDefaultPolicy` 模式,新增规范 `PolicyKind*` 常量 + `RegisterAssetKind/AssetKindOf` 资产→kind 注册表;`assettype.Register(h)` 单点接线 `RegisterAssetKind(Type(), PolicyKind())`(空 kind 跳过,不污染);`ai/policy.ResolvePolicyKind` 改为「别名 → `AssetKindOf` → kind 兜底」,**删除手维护的 11 条 `assetTypeToKind` 字面量**,仅留 1 条真·前端别名(`kubernetes`→k8s);`ai/policy.PolicyKind*` 常量改 alias 到 `entity/policy`。
- **关闭了哪条备忘**:阶段 1a/1b 留下的「type→kind 三处重复」中,`assetTypeToKind`(ai/policy)与 kind 词表常量已收敛 —— 资产→kind 由 handler 的 `PolicyKind()` 派生(OCP:新增类型只需 handler 实现该方法,`Register` 自动接线,`ResolvePolicyKind` 零改动);ai/policy⟷entity/policy⟷handler 三处 kind 常量统一为 entity/policy 一处定义。
- **行为保持**:`ResolvePolicyKind` 对全部既有输入(ssh/serial/local/database/redis/mongo/mongodb/kafka/k8s/kubernetes/etcd + 直接传 kind + 未知)结果不变(`mongo` 经 kind 兜底,`kubernetes` 经别名);无 `app/system`/前端/binding 改动,无 `wails generate`。`TestResolvePolicyKind` 改为 seed-fixture 的 resolver 单测(ai/policy 不依赖 assettype),真实 handler→kind 接线由 `assettype` 包新测试 `TestHandlerPolicyKind` 覆盖。
- **验证**:`go build ./...`、`go test ./internal/...`(全绿)、`go test ./internal/ai/policy -race`(干净)、`golangci-lint`(改动包 0 issues)。
- **仍留给后续**:
  - **2b**(本阶段未做):7 个签名各异的连接测试 binding(`TestSSHConnection`/`TestDatabaseConnection`/… + etcd outlier `EtcdTestConfig`)统一为一个 `App.TestAssetConnection`,需 runtime 连接测试注册表(测试函数是各 binder 的实例方法,持有 live manager/pool,无法 `init()` 注册)+ 重写 `AssetForm.tsx` 的 7 个 `handleTest*` 与三元链 + `wails generate`。
  - **GetConfig/Validate switch 不收口**:核实无 `Asset.GetConfig()` dispatcher;最接近的是 `asset_entity.Validate()` 的 type switch,但 `asset_entity` 在 `assettype` **之下**(导入 `assettype` 会成环),与阶段 1c 内置组留在 entity 层是同一 layering 约束 —— 该 switch **不**经 `assettype` 注册表收口。
  - `policy_group_entity.PolicyType*` 仍是独立常量集(缺 `PolicyTypeK8s`,且被内置组 + `Validate` 引用),未并入 `entity/policy.PolicyKind*`;三处重复的最后一处留待后续(代价 vs 收益不划算,需单独评估)。

## 阶段 2b 完成记录(2026-06-04)

计划见 `docs/superpowers/plans/2026-06-04-assettype-conntest-phase2b.md`,与前阶段同分支累加。本轮完成阶段 2 的跨前端部分:连接测试 binding 统一。

- **做了什么(2b)**:新建 runtime 注册表 `internal/service/conntest`(镜像 `internal/service/testreg`):`TestFunc = func(ctx, configJSON, plainPassword string) error` + `Register/Lookup/Unregister`。各 binder(ssh/query/kafka/serial/etcd)把原公开 `Test*Connection` binding **改成去掉信封的私有 `testConnection`**,在 `New()` 里 `conntest.Register(assetType, tester)`(query 注册 database/redis/mongodb 三类)。`System` binder 新增**唯一**绑定 `TestAssetConnection(testID, assetType, configJSON, plainPassword)`:共享信封(`i18n.Ctx` + 10s 超时 + `testreg.Begin` 取消)在此施加一次,查表分发,未注册类型返回 `unsupported asset type`(无 type switch,满足 OCP)。7 个公开 binding 全部删除;`wails generate module` 重生 `frontend/wailsjs`;`AssetForm.tsx` 删 5 行 test-only import、7 个 `handleTest*` 内的 `await TestXxx(...)` 统一换成 `await TestAssetConnection(testId, "<type>", JSON.stringify(cfg), password)`(serial 末参传 `""`)。
- **关闭了哪条备忘**:阶段 2a 列出的 2b ——「7 个签名各异的连接测试 binding 统一为一个 `App.TestAssetConnection`」已完成;etcd outlier(原 `EtcdTestConfig`,名字/入参都不同)拉齐到统一签名并入注册表。
- **行为保持**:各 tester body = 原 body 去掉信封,运行时等价(所有 binder 的 `ctx`=同一 wails ctx,`lang` 同源 System,信封移到 System 不改变 i18n ctx/超时/取消语义)。**唯一可见差异**:etcd resolve-password 错误日志去掉 `zap.String("testID", …)` 字段(testID 现由 host 信封持有,tester 不再入参)。`EtcdTestConnection(assetID int64)`(测**已存**资产,非表单流程)保留不动。
- **测试**:`conntest` 注册表机制单测;`System.TestAssetConnection` 分发/未知类型/`testreg` 取消信封单测(白盒 fake tester);5 个 binder 各加坏-JSON characterization 单测(白盒 `&Binder{}`,锁定「解析先于拨号」契约 + RED 驱动方法抽取),无触网。
- **验证**:`wails generate`(System 出现 `TestAssetConnection`、旧 7 binding 消失、`EtcdTestConnection` 保留)、`frontend tsc --noEmit`(0)、`go build ./...`、`go test ./internal/...`(EXIT 0,0 FAIL)、改动包 `-race`(干净)、`golangci-lint ./internal/...`(0 issues)。
- **仍留给后续**:阶段 3(`options.ts` 的 `BUILTIN_OPTIONS` 元数据折进 `AssetTypeDefinition`)、阶段 4(AssetForm 组件注册化:把各 `handleTest*` 的 per-type config 构建 + `handleRunTestConnection` 三元链 + `isTestableAssetType` 收进 `AssetTypeDefinition` 的 `buildConfig/testConnection/validateForTest`,本阶段刻意未动)、阶段 5(`AssetTree.tsx` ssh 文件管理硬编码)、阶段 6(skill + 文档)。GetConfig/Validate switch 与 `policy_group_entity.PolicyType*` 的 layering 约束同 2a 记录,不变。

## 阶段 3 完成记录(2026-06-05)

计划见 `docs/superpowers/plans/2026-06-05-assettype-options-fold-phase3.md`,与前阶段同分支累加。本阶段是前端注册表合并(纯前端,后端零改动)。

- **做了什么**:`AssetTypeDefinition`(`types.ts`)增 `aliases`/`label`/`category` 三个必填字段(`AssetTypeCategory` 从 `options.ts` 下沉到 `types.ts` 避免 `types↔options` 成环,`options.ts` re-export 保持既有 import 路径);9 个 `assetTypes/*.ts` 各自声明这三项;`options.ts` 删除 `BUILTIN_OPTIONS` 字面量数组,改运行时从 `getBuiltinTypes()` 派生(`value`=type、`labelIsI18nKey`=true、`group`="builtin" 在派生处恒定,不入 def),`getAssetTypeOptions` 的扩展追加逻辑零改动。**内置类型展示元数据一处声明,而非两处。**
- **关闭了哪条备忘**:阶段 1a 备忘「type→kind 三处重复」的前端侧 —— 各 `assetTypes/*.ts` 的展示元数据(label/aliases/category)与 `options.ts` 不再两处维护;`AssetTypeDefinition.icon` 现同时喂资产详情头与类型选择器,单一来源。
- **唯一可见行为变化(图标统一)**:折叠前类型选择器(options.ts)与资产详情头(registry)对 5 个类型用了不同 icon。统一到选择器侧的品牌图后,`AssetDetail.tsx:129` 的 ssh(`Server`→`Monitor`)、redis/mongodb/etcd(`Database`→品牌图)、k8s(`Container`→`KubernetesIcon`)改显品牌图 —— 修掉两 registry 的 icon drift;database/kafka/serial/local 本就一致。新增 `assetTypeOptions.test.ts`「single source」守护测试(每个 builtin 选项 icon === registry def icon,RED→GREEN 驱动折叠)。
- **行为保持(选项侧)**:`getAssetTypeOptions` 对全部既有输入(选项顺序 ssh→…→etcd、group="builtin"、database aliases、各 category、各 `nav.*` label、扩展追加 + ext-<name> 命名空间)结果不变 —— 由 `assetTypeOptions.test.ts`/`registry.test.ts`/`i18n.test.ts` 既有用例逐条锁定,派生后全绿,证明派生值与原字面量等价。
- **验证**:`frontend npx vitest run`(110 文件 / 1106 测试 全绿)、`npx tsc --noEmit`(0)、`npx eslint src/lib/assetTypes`(0,无残留 unused icon import)。后端零改动,无需 `go build`/`wails generate`。
- **仍留给后续**:阶段 4(AssetForm 组件注册化:`parseConfig`/`buildConfig`/`testConnection`/`validateForTest` + 通用壳)、阶段 5(`AssetTree.tsx` ssh 文件管理硬编码 → action 注册)、阶段 6(skill + 文档)。`policy_group_entity.PolicyType*` 收敛见独立未提交工作区改动,不属本阶段。

## 阶段 5 完成记录(2026-06-05)

`AssetTree.tsx` 收尾(无独立计划文档:本阶段仅 5 行生产代码,直接 TDD 内联;设计见本文档第 5 节),与前阶段同分支累加。阶段 4 完成记录见 `2026-06-05-assetform-registration-phase4-design.md`。

- **做了什么**:`AssetTypeDefinition`(`types.ts`)新增可选能力位 `canOpenFileManager?: boolean`(与既有 `canConnect`/`canConnectInNewTab` 同型,缺省=不暴露);ssh def 声明 `canOpenFileManager: true`;`AssetTree.tsx:1151` 的 `asset.Type === "ssh"` 右键文件管理特例改为 `getAssetType(asset.Type)?.canOpenFileManager`。AssetTree 至此**零 `asset.Type === "<字面量>"` 分支**(connect / newTab / 文件管理全经注册表能力位派生)。
- **设计取舍**:第 5 节给的两选项(能力位 vs 通用 action 注册表)中取**能力位**。文件管理是目前唯一的 per-type 树动作,其 icon(`FolderOpen`)+ label(`sftp.fileManager`)+ handler(`onOpenFileManager` prop)与同处的 connect/newTab 项一样仍在 AssetTree 内、由能力位门控 —— 与既有 seam 完全一致,YAGNI;待出现第二个 per-type 树动作再泛化为 action 列表。
- **行为保持**:ssh 资产右键仍显文件管理项、点击仍调 `onOpenFileManager(asset)`(`onClick` 未动);非 ssh 类型仍不显。新增 `registry.test.ts`「only ssh exposes the file-manager action」+ `AssetTreeContextMenu.test.tsx`「ssh 显 / 非 ssh(redis)不显」守护测试(registry 测 RED→GREEN 驱动能力位落地)。
- **验证**:`frontend npx vitest run`(123 文件 / 1312 测试 全绿)、`npx tsc --noEmit`(0)、`npx eslint`(0)。后端零改动,无 `go build` / `wails generate`。
- **仍留给后续**:阶段 6(skill + 文档收尾)。`policy_group_entity.PolicyType*` 三处重复最后一处的收敛同前记录,代价/收益待单独评估。
