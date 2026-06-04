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
- mongo/kafka:缺 `Effective*`/merge 机制,**本阶段不补**(见阶段 1b)。未注册 kind 经 `ResolvePolicyKind` 返回 false → app 仍报 `unsupported policy type`,行为不变。

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
   - **1b** 补齐 mongo/kafka:先补 `Effective*`/merge 机制,再写 `testMongo/testKafka` 并注册。新增行为,TDD。
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
