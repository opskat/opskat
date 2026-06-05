# policyKind 注册表(阶段 1b + 1c)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 mongo/kafka 接入 policyKind 测试注册表(阶段 1b,新增行为),并把 `policy_group_entity` 的内置权限组大数组按 kind 拆成注册表、去掉 `Validate()` 的 switch(阶段 1c,行为保持的清理)。

**Architecture:** 阶段 1b —— mongo/kafka 的 `Effective*`/merge/check/resolve 机制**已存在**(`policy_effective.go`、`mongo_policy.go`、`kafka_policy.go`、`policy_group_resolve.go`,均 5/31 落地),只缺测试链路的 `mergeMongo/KafkaPoliciesForTest` + `testMongo/KafkaPolicy` 分发函数 + `policy_kind.go` 注册。两者镜像现有 `testRedisPolicy`/`mergeRedisPoliciesForTest`(kafka)与 `mergeQueryPoliciesForTest`(mongo),组通用规则用 `MatchCommandRule`(与 runtime `checkMongoDBPermission`/`checkKafkaPermission` 完全对齐)。注册后 app 层 `TestPolicyRule` 经 `ResolvePolicyKind`+`DecodeCurrentPolicy` 自动放行 mongo/kafka(无需改 app 代码),修复其编辑非空策略点"测试"报 `unsupported policy type` 的闸门(与 1a 修 etcd 同性质)。阶段 1c —— 把 `BuiltinGroups()` 的 21 条内置组按 kind 移进 `builtinGroupsByKind` 注册表,`BuiltinGroups()` 改为按固定 kind 顺序拼装;`Validate()` 的 `switch` 改为 `isBuiltinKind(已注册) || hasExtensionPolicyType`,合法 kind 从注册数据派生(OCP)。

**Tech Stack:** Go 1.26;测试用 goconvey(`github.com/smartystreets/goconvey/convey`)/testify;后端校验用 `golangci-lint`(非 `go vet`)。

**Spec:** `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md`(第 1 节 / 阶段 1b、阶段 1c)。

> **对 spec 的修正(实现核实):** spec 第 103、148、176 行称 mongo/kafka "缺 `Effective*`/merge 机制,先补再注册"。核实代码:`EffectiveMongoPolicy`/`EffectiveKafkaPolicy`/`expandMongo|Kafka`/`checkMongo|KafkaPolicyRules`/`ResolveMongo|KafkaGroups` 早已存在。因此 1b 实际范围仅为**测试链路的 merge + dispatch + 注册**,不需要新增 `Effective*`。本计划 Task 4 顺带把 spec 这处陈述改正(in-scope doc drift)。

---

## File Structure

- **Modify** `internal/ai/policy/policy_tester.go` — 新增 `testMongoPolicy`/`testKafkaPolicy`(放在 `testEtcdPolicy` 之后、`// --- K8S ---` 之前)与 `mergeMongoPoliciesForTest`/`mergeKafkaPoliciesForTest`(放在 `mergeK8sPoliciesForTest` 之后)。其余函数不动。
- **Modify** `internal/ai/policy/policy_kind.go` — `init()` 末尾(第 94 行 etcd 块之后、第 95 行 `}` 之前)注册 mongo/kafka 两个 handler;改正第 112-114 行 `ResolvePolicyKind` 文档注释中"未注册(如当前的 mongo/kafka)"的陈述。
- **Modify** `internal/ai/policy/policy_kind_test.go` — `TestPolicyKindRegistry` 改断言 7 个 kind 全注册 + bogus 未注册;`TestDecodeCurrentPolicy` 错误用例改用 bogus kind 并加 mongo 解码正向用例;`TestResolvePolicyKind` 把 mongo/kafka 移入正向 case。
- **Modify** `internal/ai/policy/policy_dispatch_test.go` — 把"未注册 kind(mongo)"用例替换为 mongo/kafka 分发用例 + bogus-kind 兜底。
- **Modify** `internal/model/entity/policy_group_entity/policy_group.go` — 引入 `builtinKindOrder` / `builtinGroupsByKind` / `registerBuiltinGroups` / `isBuiltinKind`;`init()` 改为先注册各 kind 内置组再建 `builtinMap`;`BuiltinGroups()` 改为按 kind 顺序拼装;`Validate()` 去 switch。
- **Test(已存在,跑绿即可)** `internal/model/entity/policy_group_entity/policy_group_test.go` — `TestBuiltinGroups`(总数 21 + 各 kind 计数)、`TestPolicyGroup_Validate` 直接锁定 1c 行为保持。
- **Modify** `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` — 改正 mongo/kafka "缺 Effective*" 的陈述(Task 4)。

> **Layering 不变:** 1b 全在 `internal/ai/policy` 内;1c 全在 `policy_group_entity` 内。`internal/ai/policy → policy_group_entity` 的单向依赖(`FindBuiltin`)不变,无新增反向依赖。

---

# 阶段 1b — mongo/kafka 接入测试注册表

## Task 1: mongo/kafka 测试分发 + 注册(TDD)

**Files:**
- Modify: `internal/ai/policy/policy_tester.go`
- Modify: `internal/ai/policy/policy_kind.go`
- Test: `internal/ai/policy/policy_kind_test.go`、`internal/ai/policy/policy_dispatch_test.go`

- [ ] **Step 1: 改/加失败测试(RED)**

(1a) In `internal/ai/policy/policy_kind_test.go`,把 `TestPolicyKindRegistry`(第 10-25 行)整体替换为:

```go
func TestPolicyKindRegistry(t *testing.T) {
	Convey("policyKind 注册表", t, func() {
		Convey("内置 7 个 kind 已注册", func() {
			for _, k := range []string{
				PolicyKindCommand, PolicyKindQuery, PolicyKindRedis,
				PolicyKindMongo, PolicyKindKafka, PolicyKindK8s, PolicyKindEtcd,
			} {
				_, ok := kindRegistry[k]
				So(ok, ShouldBeTrue)
			}
		})
		Convey("未知 kind 未注册", func() {
			_, ok := kindRegistry["bogus"]
			So(ok, ShouldBeFalse)
		})
	})
}
```

(1b) 把 `TestDecodeCurrentPolicy`(第 27-41 行)整体替换为:

```go
func TestDecodeCurrentPolicy(t *testing.T) {
	Convey("DecodeCurrentPolicy", t, func() {
		Convey("command → *CommandPolicy", func() {
			v, err := DecodeCurrentPolicy(PolicyKindCommand, []byte(`{"allow_list":["ls *"]}`))
			So(err, ShouldBeNil)
			cp, ok := v.(*asset_entity.CommandPolicy)
			So(ok, ShouldBeTrue)
			So(cp.AllowList, ShouldResemble, []string{"ls *"})
		})
		Convey("mongo → *MongoPolicy", func() {
			v, err := DecodeCurrentPolicy(PolicyKindMongo, []byte(`{"allow_types":["find"]}`))
			So(err, ShouldBeNil)
			mp, ok := v.(*asset_entity.MongoPolicy)
			So(ok, ShouldBeTrue)
			So(mp.AllowTypes, ShouldResemble, []string{"find"})
		})
		Convey("未注册 kind 报错", func() {
			_, err := DecodeCurrentPolicy("bogus", []byte(`{}`))
			So(err, ShouldNotBeNil)
		})
	})
}
```

(1c) 在 `TestResolvePolicyKind` 的 `cases` map(第 46-55 行)中加入 mongo/kafka,并删除"mongo/kafka 未注册 → false"那段 Convey(第 67-72 行)。`cases` map 改为:

```go
			cases := map[string]string{
				"ssh":        PolicyKindCommand,
				"serial":     PolicyKindCommand,
				"local":      PolicyKindCommand,
				"database":   PolicyKindQuery,
				"redis":      PolicyKindRedis,
				"mongo":      PolicyKindMongo,
				"mongodb":    PolicyKindMongo,
				"kafka":      PolicyKindKafka,
				"k8s":        PolicyKindK8s,
				"kubernetes": PolicyKindK8s,
				"etcd":       PolicyKindEtcd,
			}
```

删除这一段(第 67-72 行):

```go
		Convey("mongo/kafka 未注册 → false(保持 unsupported 行为)", func() {
			_, ok := ResolvePolicyKind("mongo")
			So(ok, ShouldBeFalse)
			_, ok = ResolvePolicyKind("kafka")
			So(ok, ShouldBeFalse)
		})
```

(1d) In `internal/ai/policy/policy_dispatch_test.go`,把"未注册 kind(mongo)返回 NeedConfirm"那段 Convey(第 57-59 行附近)替换为 mongo/kafka 分发 + bogus 兜底:

```go
		Convey("mongo kind 路由到 testMongoPolicy(deny 命中)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindMongo,
				Current:    &asset_entity.MongoPolicy{DenyTypes: []string{"dropDatabase"}},
			}, "dropDatabase")
			So(out.Decision, ShouldEqual, aictx.Deny)
		})
		Convey("mongo kind 应用 Current allow(find 放行)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindMongo,
				Current:    &asset_entity.MongoPolicy{AllowTypes: []string{"find"}},
			}, "find")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("kafka kind 路由到 testKafkaPolicy(deny 命中)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindKafka,
				Current:    &asset_entity.KafkaPolicy{DenyList: []string{"topic.delete *"}},
			}, "topic.delete orders")
			So(out.Decision, ShouldEqual, aictx.Deny)
		})
		Convey("kafka kind 应用 Current allow(topic.read 放行)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindKafka,
				Current:    &asset_entity.KafkaPolicy{AllowList: []string{"topic.read *"}},
			}, "topic.read orders")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("未注册 kind(bogus)返回 NeedConfirm", func() {
			out := TestPolicy(ctx, PolicyTestInput{PolicyKind: "bogus"}, "anything")
			So(out.Decision, ShouldEqual, aictx.NeedConfirm)
		})
```

- [ ] **Step 2: 运行,确认编译失败(RED)**

Run: `go test ./internal/ai/policy/ -run 'TestPolicyKindRegistry|TestDecodeCurrentPolicy|TestResolvePolicyKind|TestPolicyDispatch' -count=1`
Expected: FAIL —— `TestPolicyDispatch` 现断言 mongo/kafka 走 Deny/Allow,但 mongo/kafka 未注册仍走 `NeedConfirm` 兜底;`TestPolicyKindRegistry` 断言 7 个 kind 全注册但 mongo/kafka 缺失;`TestResolvePolicyKind` 断言 mongo/kafka 解析成功但当前返回 false。(编译能过——只用到已存在的 `PolicyKindMongo`/`PolicyKindKafka` 常量与 `asset_entity.MongoPolicy`/`KafkaPolicy` 类型。)

- [ ] **Step 3: 实现 merge 助手 + testMongo/testKafka(GREEN 实现 1/2)**

In `internal/ai/policy/policy_tester.go`,在 `testEtcdPolicy` 结束(`// --- K8S ---` 注释)之前插入:

```go
// --- MongoDB ---

func testMongoPolicy(ctx context.Context, current *asset_entity.MongoPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	// 与真实 checkMongoDBPermission 对齐：Mongo 操作是单 token，组通用策略用 MatchCommandRule。
	groupDeny, groupAllow := collectGroupGenericRules(ctx, groups)
	if out := checkGenericDeny(groupDeny, command, MatchCommandRule); out != nil {
		out.Message = PolicyFmt(ctx, "MongoDB operation denied by group policy: %s", "MongoDB 操作被组策略禁止: %s", command)
		return *out
	}

	merged := mergeMongoPoliciesForTest(ctx, current, groups)
	result := checkMongoPolicyRules(ctx, EffectiveMongoPolicy(ctx, merged), command)
	if result.Decision == aictx.Deny {
		return PolicyTestOutput{
			Decision:       aictx.Deny,
			MatchedPattern: result.MatchedPattern,
			MatchedSource:  "", // 当前资产策略
			Message:        result.Message,
		}
	}

	// 与 runtime 一致：组通用 allow 只用来把 aictx.NeedConfirm 升为 aictx.Allow。
	if result.Decision == aictx.NeedConfirm {
		if out := checkGenericAllow(groupAllow, command, MatchCommandRule); out != nil {
			return *out
		}
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}
	return PolicyTestOutput{Decision: aictx.Allow}
}

// --- Kafka ---

func testKafkaPolicy(ctx context.Context, current *asset_entity.KafkaPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	// 与真实 checkKafkaPermission 对齐：组通用策略用 MatchCommandRule
	// （MatchKafkaRule 仅适用于 "<action> <resource>" 的类型专用规则，不能用于通用 CmdPolicy）。
	groupDeny, groupAllow := collectGroupGenericRules(ctx, groups)
	if out := checkGenericDeny(groupDeny, command, MatchCommandRule); out != nil {
		out.Message = PolicyFmt(ctx, "Kafka operation denied by group policy: %s", "Kafka 操作被组策略禁止: %s", command)
		return *out
	}

	merged := mergeKafkaPoliciesForTest(ctx, current, groups)
	result := checkKafkaPolicyRules(ctx, EffectiveKafkaPolicy(ctx, merged), command)
	if result.Decision == aictx.Deny {
		return PolicyTestOutput{
			Decision:       aictx.Deny,
			MatchedPattern: result.MatchedPattern,
			MatchedSource:  "", // 当前资产策略
			Message:        result.Message,
		}
	}

	if result.Decision == aictx.NeedConfirm {
		if out := checkGenericAllow(groupAllow, command, MatchCommandRule); out != nil {
			return *out
		}
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}
	return PolicyTestOutput{Decision: aictx.Allow}
}
```

并在 `mergeK8sPoliciesForTest`(以 `return merged }` 结束)之后插入两个 merge 助手:

```go
func mergeMongoPoliciesForTest(ctx context.Context, current *asset_entity.MongoPolicy, groups []*group_entity.Group) *asset_entity.MongoPolicy {
	var policies []*asset_entity.MongoPolicy
	if current != nil {
		policies = append(policies, current)
	}
	for _, g := range groups {
		p, err := g.GetMongoPolicy()
		if err == nil && p != nil {
			policies = append(policies, p)
		}
	}

	merged := &asset_entity.MongoPolicy{}
	for _, p := range policies {
		expanded := expandMongoPolicy(ctx, p)
		if len(merged.AllowTypes) == 0 && len(expanded.AllowTypes) > 0 {
			merged.AllowTypes = AppendUnique(merged.AllowTypes, expanded.AllowTypes...)
		}
		merged.DenyTypes = AppendUnique(merged.DenyTypes, expanded.DenyTypes...)
	}
	return merged
}

func mergeKafkaPoliciesForTest(ctx context.Context, current *asset_entity.KafkaPolicy, groups []*group_entity.Group) *asset_entity.KafkaPolicy {
	var policies []*asset_entity.KafkaPolicy
	if current != nil {
		policies = append(policies, current)
	}
	for _, g := range groups {
		p, err := g.GetKafkaPolicy()
		if err == nil && p != nil {
			policies = append(policies, p)
		}
	}

	merged := &asset_entity.KafkaPolicy{}
	for _, p := range policies {
		expanded := expandKafkaPolicy(ctx, p)
		if len(merged.AllowList) == 0 && len(expanded.AllowList) > 0 {
			merged.AllowList = AppendUnique(merged.AllowList, expanded.AllowList...)
		}
		merged.DenyList = AppendUnique(merged.DenyList, expanded.DenyList...)
	}
	return merged
}
```

> 这两个 merge 助手逐字镜像现有 `mergeQueryPoliciesForTest`(mongo,AllowTypes/DenyTypes,无 DenyFlags)与 `mergeRedisPoliciesForTest`(kafka),合并语义(allow 取第一个非空、deny 累积)与 runtime `collectMongoDBPolicies`/`collectKafkaPolicies` 一致。

- [ ] **Step 4: 注册 mongo/kafka + 改正注释(GREEN 实现 2/2)**

In `internal/ai/policy/policy_kind.go`,在 `init()` 的 etcd 注册块(以第 94 行 `})` 结束)之后、第 95 行 `}` 之前插入:

```go
	registerPolicyKind(PolicyKindMongo, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.MongoPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			mp, _ := current.(*asset_entity.MongoPolicy)
			return testMongoPolicy(ctx, mp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindKafka, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.KafkaPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			kp, _ := current.(*asset_entity.KafkaPolicy)
			return testKafkaPolicy(ctx, kp, groups, command)
		},
	})
```

并把 `ResolvePolicyKind` 上方注释(第 112-114 行)改正——去掉已过时的 mongo/kafka 举例:

```go
// ResolvePolicyKind 把资产类型 / 前端 policyType 解析为已注册的 policyKind。
// 仅当目标 kind 有注册 handler 时返回 ok=true;未注册的 kind 返回 false,
// 调用方据此保持 "unsupported policy type" 的既有行为。
```

- [ ] **Step 5: 跑整个 policy 包 + 编译,确认全绿(GREEN)**

Run: `go build ./... && go test ./internal/ai/policy/ -count=1`
Expected: PASS —— Step 1 改的 4 个测试全过;原有 `TestTestSSHPolicy/TestTestRedisPolicy/TestTestK8sPolicy/TestTestQueryPolicy/TestTestEtcdPolicy/TestCheckMongoDBPolicy_*` 等全部仍绿(mongo/kafka 既有 check 测试不受影响)。

- [ ] **Step 6: 提交**

```bash
git add internal/ai/policy/policy_tester.go internal/ai/policy/policy_kind.go internal/ai/policy/policy_kind_test.go internal/ai/policy/policy_dispatch_test.go
git commit -m "✨ mongo/kafka 接入 policyKind 测试注册表,修复编辑策略测试闸门 #130"
```

## Task 2: app 层观测验收(mongo/kafka 闸门修复)

**Files:** 无(仅验证;app 层经 `ResolvePolicyKind`+`DecodeCurrentPolicy` 自动放行,无需改 `asset.go`)

- [ ] **Step 1: app/system 包测试 + 受影响包全测**

Run: `go test ./internal/app/system/ ./internal/ai/policy/ -count=1`
Expected: PASS。

> 行为差异(预期,新增):`req.PolicyType` 为 `mongo`/`mongodb`/`kafka` 且 `PolicyJSON` 非空时,旧代码经 `ResolvePolicyKind` 返回 false → `unsupported policy type`;现在 mongo/kafka 已注册 → 正确走 `testMongoPolicy`/`testKafkaPolicy`(与 1a 修 etcd 同性质的闸门修复)。

- [ ] **Step 2: (观测,GUI 不可点)** 按 `docs/testing-debugging-guide.md`,对一个 mongo 或 kafka 资产,在策略编辑面板填非空 allow/deny 后点"测试",确认返回 allow/deny/need_confirm 而非 `unsupported policy type`;必要时读 `logs/opskat.log` 确认无 `unsupported policy type: mongo`/`kafka`。
> 若本机无 mongo/kafka 资产,可跳过手测 —— Task 1 的 `TestPolicyDispatch` 已在 dispatch 层覆盖 mongo/kafka 非空策略路由;此步仅为端到端旁证。

---

# 阶段 1c — 内置权限组按 kind 拆分(去 Validate switch)

## Task 3: builtin groups 注册表化 + Validate 去 switch(TDD)

**Files:**
- Modify: `internal/model/entity/policy_group_entity/policy_group.go`
- Test: `internal/model/entity/policy_group_entity/policy_group_test.go`(新增 1 个 `isBuiltinKind` 派生用例;`TestBuiltinGroups`/`TestPolicyGroup_Validate` 已存在,锁行为保持)

- [ ] **Step 1: 写失败测试(RED)**

In `internal/model/entity/policy_group_entity/policy_group_test.go`,在 `TestPolicyGroup_Validate` 函数之后新增:

```go
func TestIsBuiltinKind(t *testing.T) {
	convey.Convey("isBuiltinKind 从注册数据派生合法 kind", t, func() {
		convey.Convey("已注册的 6 个内置 kind 均为真", func() {
			for _, k := range []string{
				PolicyTypeCommand, PolicyTypeQuery, PolicyTypeRedis,
				PolicyTypeMongo, PolicyTypeKafka, PolicyTypeEtcd,
			} {
				assert.True(t, isBuiltinKind(k), "kind %s 应为已注册内置 kind", k)
			}
		})
		convey.Convey("未注册 kind 为假", func() {
			assert.False(t, isBuiltinKind("unknown"))
			assert.False(t, isBuiltinKind(""))
		})
	})
}
```

- [ ] **Step 2: 运行,确认编译失败(RED)**

Run: `go test ./internal/model/entity/policy_group_entity/ -run TestIsBuiltinKind -count=1`
Expected: FAIL —— 编译错误,`undefined: isBuiltinKind`。

- [ ] **Step 3: 重构 BuiltinGroups 为按 kind 注册表(GREEN 实现 1/2)**

In `internal/model/entity/policy_group_entity/policy_group.go`:

(3a) 把 `// --- 内置权限组 ---` 注释下、`mustMarshal` 之后到 `BuiltinGroups()` 函数(当前第 101-429 行)与 `builtinMap` 的 `init()`(当前第 431-439 行)替换为以下结构。**关键:21 条 `&PolicyGroup{...}` 字面量从现有 `BuiltinGroups()` 返回数组中按 kind 注释分段「原样剪切」进对应的 `registerBuiltinGroups(...)` 调用,逐字节不改**(字段、`mustMarshal(&policy.XxxPolicy{...})`、`BuiltinID` 全部保持)。各 kind 的字面量条数:command 5、query 2、redis 2、mongo 3、kafka 7、etcd 2(与 `TestBuiltinGroups` 断言一致)。

```go
// --- 内置权限组 ---

func mustMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// builtinKindOrder 决定 BuiltinGroups() 的拼装顺序,保持与历史一致(command→query→redis→mongo→kafka→etcd)。
var builtinKindOrder = []string{
	PolicyTypeCommand, PolicyTypeQuery, PolicyTypeRedis,
	PolicyTypeMongo, PolicyTypeKafka, PolicyTypeEtcd,
}

// builtinGroupsByKind 每个 policyKind 贡献的内置权限组(纯数据注册表)。
// 新增资产类型/策略 kind 时,在此处加一段 registerBuiltinGroups 即可,Validate / BuiltinGroups 自动覆盖。
var builtinGroupsByKind = map[string][]*PolicyGroup{}

func registerBuiltinGroups(kind string, groups ...*PolicyGroup) {
	builtinGroupsByKind[kind] = append(builtinGroupsByKind[kind], groups...)
}

func init() {
	registerBuiltinGroups(PolicyTypeCommand,
		// （此处剪切原数组中 "// SSH command 类型" 段的 5 条字面量:
		//   BuiltinLinuxReadOnly / BuiltinK8sReadOnly / BuiltinK8sDangerousDeny /
		//   BuiltinDockerReadOnly / BuiltinDangerousDeny）
	)
	registerBuiltinGroups(PolicyTypeQuery,
		// （"// Database query 类型" 段的 2 条:BuiltinSQLReadOnly / BuiltinSQLDangerousDeny）
	)
	registerBuiltinGroups(PolicyTypeRedis,
		// （"// Redis 类型" 段的 2 条:BuiltinRedisReadOnly / BuiltinRedisDangerousDeny）
	)
	registerBuiltinGroups(PolicyTypeMongo,
		// （"// MongoDB 类型" 段的 3 条:BuiltinMongoReadOnly / BuiltinMongoReadWrite / BuiltinMongoDangerousDeny）
	)
	registerBuiltinGroups(PolicyTypeKafka,
		// （"// Kafka 类型" 段的 7 条:Metadata/Message/Schema/Connect/Operator/Security/Dangerous）
	)
	registerBuiltinGroups(PolicyTypeEtcd,
		// （"// etcd 类型" 段的 2 条:BuiltinEtcdReadOnly / BuiltinEtcdDangerousDeny）
	)

	builtinMap = make(map[string]*PolicyGroup)
	for _, pg := range BuiltinGroups() {
		builtinMap[pg.BuiltinID] = pg
	}
}

// BuiltinGroups 返回所有内置权限组(按 kind 顺序拼装)
func BuiltinGroups() []*PolicyGroup {
	groups := make([]*PolicyGroup, 0, len(builtinMap))
	for _, kind := range builtinKindOrder {
		groups = append(groups, builtinGroupsByKind[kind]...)
	}
	return groups
}

// isBuiltinKind 判断 policyType 是否为已注册内置 kind(合法 kind 从注册数据派生,替代 Validate 的 switch)。
func isBuiltinKind(kind string) bool {
	_, ok := builtinGroupsByKind[kind]
	return ok
}
```

> 注:`builtinMap` 的声明 `var builtinMap map[string]*PolicyGroup`(当前第 432 行)保留;只是其赋值并入上面的 `init()`。`make(..., len(builtinMap))` 在首次 `BuiltinGroups()` 调用(init 内)时 `builtinMap` 尚为 nil → cap 0,正常 append,无副作用。
> **实例共享说明(已核实安全):** 重构后 `BuiltinGroups()` 与 `FindBuiltin` 返回同一组实例(原先 `BuiltinGroups()` 每次新建)。两个调用方(`policy_group.go` 的 `builtinMap` 构建、`policy_group_svc.List`)与 `FindBuiltin` 调用方均只读(`.PolicyType` 过滤、`.ToItem()` 复制),不改返回结构,故行为保持。

- [ ] **Step 4: Validate 去 switch(GREEN 实现 2/2)**

In `internal/model/entity/policy_group_entity/policy_group.go`,把 `Validate()`(当前第 44-56 行)的 `switch` 改为派生判定:

```go
// Validate 校验
func (pg *PolicyGroup) Validate() error {
	if pg.Name == "" {
		return errors.New("权限组名称不能为空")
	}
	if !isBuiltinKind(pg.PolicyType) && !hasExtensionPolicyType(pg.PolicyType) {
		return errors.New("无效的策略类型")
	}
	return nil
}
```

> 合法 kind 集合不变:`isBuiltinKind` 对 command/query/redis/mongo/kafka/etcd 为真(各有内置组),与原 switch 等价;扩展类型仍由 `hasExtensionPolicyType` 兜底。

- [ ] **Step 5: 跑 entity 包 + 直接依赖方,确认全绿(GREEN)**

Run: `go build ./... && go test ./internal/model/entity/policy_group_entity/ ./internal/service/policy_group_svc/ ./internal/ai/policy/ -count=1`
Expected: PASS —— 新增 `TestIsBuiltinKind` 通过;既有 `TestBuiltinGroups`(总数 21 + command5/query2/redis2/mongo3/kafka7/etcd2)、`TestBuiltinGroups_Etcd`、`TestPolicyGroup_Validate`、`TestFindBuiltin` 全绿(行为保持)。

- [ ] **Step 6: 提交**

```bash
git add internal/model/entity/policy_group_entity/policy_group.go internal/model/entity/policy_group_entity/policy_group_test.go
git commit -m "♻️ 内置权限组按 kind 拆为注册表,Validate 去 switch #130"
```

---

## Task 4: 全量校验 + 文档 drift 改正

**Files:** Modify `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md`

- [ ] **Step 1: 受影响包全测 + race**

Run: `go test ./internal/ai/policy/ ./internal/app/system/ ./internal/model/entity/policy_group_entity/ ./internal/service/policy_group_svc/ -count=1`
然后: `go test ./internal/ai/policy/ -race -count=1`
Expected: 均 PASS。

- [ ] **Step 2: 后端 lint(项目用 golangci-lint,非 go vet)**

Run: `golangci-lint run ./internal/ai/policy/... ./internal/model/entity/policy_group_entity/... ./internal/service/policy_group_svc/...`
Expected: 0 issues(确认新增 `testMongoPolicy`/`testKafkaPolicy`/`mergeMongo|KafkaPoliciesForTest`/`registerBuiltinGroups`/`isBuiltinKind` 均被使用,不报 unused)。

- [ ] **Step 3: 改正 spec 中 mongo/kafka "缺 Effective*" 的陈述**

In `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md`:
- 第 103 行 `mongo/kafka:缺 Effective*/merge 机制,本阶段不补(见阶段 1b)。...` → 改为说明 `Effective*` 已存在,1b 仅补测试链路(merge helper + test 分发 + 注册)。
- 第 148 行阶段 1b 描述 `先补 Effective*/merge 机制,再写 testMongo/testKafka 并注册` → 改为 `Effective*/merge/check 机制已就绪,本阶段补测试链路 mergeMongo|KafkaPoliciesForTest + testMongo|testKafka 并注册`。
- 第 176 行完成记录里的 1b 备忘同义改正。

- [ ] **Step 4: 在 spec 末尾追加阶段 1b/1c 完成记录**

In `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` 末尾追加一节"阶段 1b + 1c 完成记录(2026-06-04)",记录:1b 实际范围(Effective* 已存在,仅补测试链路 + 注册 + 闸门修复 mongo/kafka)、1c(BuiltinGroups 按 kind 注册表化 + Validate 去 switch,行为保持)、全量测试/race/lint 结果、以及仍留给阶段 2 的 type→kind 三处同步备忘(从 1a 延续)。

- [ ] **Step 5: 提交**

```bash
git add docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md
git commit -m "📝 改正 spec mongo/kafka Effective* 陈述 + 记录阶段 1b/1c 完成 #130"
```

---

## Self-Review 备忘(写计划时已核对)

- **Spec 覆盖**:阶段 1b(测试链路接入 mongo/kafka)→ Task 1/2;阶段 1c(builtin groups 按 kind 拆 + Validate 去 switch)→ Task 3;spec drift 改正 + 完成记录 → Task 4。✅
- **对 spec 的修正已显式标注**:`Effective*`/merge 早已存在,1b 范围收窄为测试链路 + 注册;Task 4 改正 spec。✅
- **无占位符(代码步骤)**:1b 全部给出完整代码与精确插入点;1c 因是 21 条字面量「原样剪切」,给出完整目标结构骨架 + 逐段剪切指令(逐字节不改),并由既有 `TestBuiltinGroups` 计数断言兜底防抄歪 —— 这是「移动而非重写」的恰当处理。✅
- **类型/命名一致**:`testMongoPolicy`/`testKafkaPolicy`、`mergeMongoPoliciesForTest`/`mergeKafkaPoliciesForTest`、`PolicyKindMongo`/`PolicyKindKafka`、`isBuiltinKind`、`builtinGroupsByKind`/`registerBuiltinGroups`/`builtinKindOrder` 在各 Task 用法一致;镜像的 `EffectiveMongoPolicy`/`EffectiveKafkaPolicy`/`checkMongoPolicyRules`/`checkKafkaPolicyRules`/`expandMongoPolicy`/`expandKafkaPolicy`/`GetMongoPolicy`/`GetKafkaPolicy`/`AppendUnique`/`MatchCommandRule` 均为现有 API(已核实存在)。✅
- **行为保持/新增边界清晰**:1b 唯一新增是 mongo/kafka 注册后端到端可测(含 app 层闸门修复);1c 行为完全保持(计数/校验既有测试锁定)。✅
- **Layering**:1b 限 `internal/ai/policy`,1c 限 `policy_group_entity`,无新增跨层依赖。✅
