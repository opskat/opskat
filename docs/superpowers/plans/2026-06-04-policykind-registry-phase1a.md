# policyKind 注册表(阶段 0 + 1a)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 policy 测试链路从 `switch PolicyType` 改成 `policyKind` 注册表分发,引入规范 `policyKind` 词表与 asset/frontend→kind resolver,并顺带修复 etcd 策略在 app 层被错误拦截的 bug —— 全程行为保持(etcd 为修复)。

**Architecture:** 在 `internal/ai/policy` 内新增以 `policyKind`(`command/query/redis/mongo/kafka/k8s/etcd`,取值与 `policy_group_entity.PolicyType*` 一致)为键的处理器注册表(`decode` + `test`)。`TestPolicy` 改为查表分发,各 handler 委托现有 `testSSHPolicy`/`testQueryPolicy`/`testRedisPolicy`/`testK8sPolicy`/`testEtcdPolicy` 保持行为。`PolicyTestInput` 的 5 个 `Current*` 硬字段收敛为 `PolicyKind string` + `Current any`。app 层 `TestPolicyRule` 的 per-type Unmarshal switch 改为 `ResolvePolicyKind` + `DecodeCurrentPolicy`。mongo/kafka 因缺 `Effective*`/merge 机制本阶段不注册(阶段 1b),未注册 kind 经 resolver 返回 false → app 仍报 `unsupported policy type`,行为不变。

**Tech Stack:** Go 1.26;测试用 goconvey(`github.com/smartystreets/goconvey/convey`);后端校验用 `golangci-lint`(非 `go vet`)。

**Spec:** `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md`(第 0 节、第 1 节 / 阶段 0 + 1a)

---

## File Structure

- **Create** `internal/ai/policy/policy_kind.go` — `PolicyKind*` 常量、`policyKindHandler` 结构、`kindRegistry` + `registerPolicyKind`、`init()` 注册 5 个 kind、`assetTypeToKind` 映射、`ResolvePolicyKind`、`DecodeCurrentPolicy`。
- **Create** `internal/ai/policy/policy_kind_test.go` — 注册表成员、`DecodeCurrentPolicy`、`ResolvePolicyKind` 的单测。
- **Create** `internal/ai/policy/policy_dispatch_test.go` — `TestPolicy` 经注册表分发的单测(含 etcd 路由、未注册 kind 兜底)。
- **Modify** `internal/ai/policy/policy_tester.go:16-60` — 改 `PolicyTestInput` 结构;`TestPolicy` 去 switch 改查表。其余 `testXxx` 函数与所有 helper **不动**。
- **Modify** `internal/ai/policy/policy_tester_test.go:474-488` — 两处 `TestPolicy(... PolicyTestInput{PolicyType/CurrentEtcd})` 调用更新为新结构。
- **Modify** `internal/app/system/asset.go:25-101` — `PolicyTestRequest` 注释 + `TestPolicyRule` 的 per-type Unmarshal switch 改为 resolver + decode。

> 说明:`asset_entity.CommandPolicy` 等是 `policy.*Policy` 的类型别名(`asset_entity/asset.go:67,315,321,...`),tester 代码沿用 `asset_entity.*` 命名。`asset_entity` 在 `asset.go` 仍被 149-164 行使用,导入不动。

---

## Task 1: policyKind 注册表 + resolver + decode

**Files:**
- Create: `internal/ai/policy/policy_kind.go`
- Test: `internal/ai/policy/policy_kind_test.go`

- [ ] **Step 1: 写失败测试**

Create `internal/ai/policy/policy_kind_test.go`:

```go
package policy

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPolicyKindRegistry(t *testing.T) {
	Convey("policyKind 注册表", t, func() {
		Convey("内置 5 个 kind 已注册", func() {
			for _, k := range []string{PolicyKindCommand, PolicyKindQuery, PolicyKindRedis, PolicyKindK8s, PolicyKindEtcd} {
				_, ok := kindRegistry[k]
				So(ok, ShouldBeTrue)
			}
		})
		Convey("mongo/kafka 暂未注册(留待阶段 1b)", func() {
			_, ok := kindRegistry[PolicyKindMongo]
			So(ok, ShouldBeFalse)
			_, ok = kindRegistry[PolicyKindKafka]
			So(ok, ShouldBeFalse)
		})
	})
}

func TestDecodeCurrentPolicy(t *testing.T) {
	Convey("DecodeCurrentPolicy", t, func() {
		Convey("command → *CommandPolicy", func() {
			v, err := DecodeCurrentPolicy(PolicyKindCommand, []byte(`{"allow_list":["ls *"]}`))
			So(err, ShouldBeNil)
			cp, ok := v.(*asset_entity.CommandPolicy)
			So(ok, ShouldBeTrue)
			So(cp.AllowList, ShouldResemble, []string{"ls *"})
		})
		Convey("未注册 kind 报错", func() {
			_, err := DecodeCurrentPolicy(PolicyKindMongo, []byte(`{}`))
			So(err, ShouldNotBeNil)
		})
	})
}

func TestResolvePolicyKind(t *testing.T) {
	Convey("ResolvePolicyKind", t, func() {
		Convey("资产类型/前端 policyType → kind", func() {
			cases := map[string]string{
				"ssh":        PolicyKindCommand,
				"serial":     PolicyKindCommand,
				"local":      PolicyKindCommand,
				"database":   PolicyKindQuery,
				"redis":      PolicyKindRedis,
				"k8s":        PolicyKindK8s,
				"kubernetes": PolicyKindK8s,
				"etcd":       PolicyKindEtcd,
			}
			for in, want := range cases {
				k, ok := ResolvePolicyKind(in)
				So(ok, ShouldBeTrue)
				So(k, ShouldEqual, want)
			}
		})
		Convey("mongo/kafka 未注册 → false(保持 unsupported 行为)", func() {
			_, ok := ResolvePolicyKind("mongo")
			So(ok, ShouldBeFalse)
			_, ok = ResolvePolicyKind("kafka")
			So(ok, ShouldBeFalse)
		})
		Convey("未知类型 → false", func() {
			_, ok := ResolvePolicyKind("nope")
			So(ok, ShouldBeFalse)
		})
	})
}
```

- [ ] **Step 2: 运行,确认编译失败**

Run: `go test ./internal/ai/policy/ -run 'TestPolicyKindRegistry|TestDecodeCurrentPolicy|TestResolvePolicyKind' -count=1`
Expected: FAIL —— 编译错误,`undefined: PolicyKindCommand / kindRegistry / DecodeCurrentPolicy / ResolvePolicyKind` 等。

- [ ] **Step 3: 写实现**

Create `internal/ai/policy/policy_kind.go`:

```go
package policy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
)

// PolicyKind 是策略逻辑的规范化种类,是 policy 测试链路统一的 dispatch key。
// 取值与 policy_group_entity.PolicyType*（command/query/redis/mongo/kafka/etcd）保持一致,额外加上 k8s。
// 资产类型 / 前端 policyType 经 ResolvePolicyKind 映射到它。
const (
	PolicyKindCommand = "command"
	PolicyKindQuery   = "query"
	PolicyKindRedis   = "redis"
	PolicyKindMongo   = "mongo"
	PolicyKindKafka   = "kafka"
	PolicyKindK8s     = "k8s"
	PolicyKindEtcd    = "etcd"
)

// policyKindHandler 每个 policyKind 的测试/解码处理器。
type policyKindHandler struct {
	// decode 把前端传入的策略 JSON 还原成对应的具体策略指针(*CommandPolicy 等)。
	decode func(raw []byte) (any, error)
	// test 用当前策略 + 资产组链测试命令;current 为 decode 的产物或 nil。
	test func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput
}

var kindRegistry = map[string]policyKindHandler{}

func registerPolicyKind(kind string, h policyKindHandler) {
	kindRegistry[kind] = h
}

func init() {
	registerPolicyKind(PolicyKindCommand, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.CommandPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			cp, _ := current.(*asset_entity.CommandPolicy)
			return testSSHPolicy(ctx, cp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindQuery, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.QueryPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			qp, _ := current.(*asset_entity.QueryPolicy)
			return testQueryPolicy(ctx, qp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindRedis, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.RedisPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			rp, _ := current.(*asset_entity.RedisPolicy)
			return testRedisPolicy(ctx, rp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindK8s, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.K8sPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			kp, _ := current.(*asset_entity.K8sPolicy)
			return testK8sPolicy(ctx, kp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindEtcd, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.EtcdPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			ep, _ := current.(*asset_entity.EtcdPolicy)
			return testEtcdPolicy(ctx, ep, groups, command)
		},
	})
}

// assetTypeToKind 把资产类型 / 前端 policyType 字符串映射到规范 policyKind。
var assetTypeToKind = map[string]string{
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

// ResolvePolicyKind 把资产类型 / 前端 policyType 解析为已注册的 policyKind。
// 仅当目标 kind 有注册 handler 时返回 ok=true;未注册(如当前的 mongo/kafka)返回 false,
// 调用方据此保持 "unsupported policy type" 的既有行为。
func ResolvePolicyKind(s string) (string, bool) {
	kind, ok := assetTypeToKind[s]
	if !ok {
		kind = s // 允许直接传 kind
	}
	if _, has := kindRegistry[kind]; !has {
		return "", false
	}
	return kind, true
}

// DecodeCurrentPolicy 用对应 kind 的 handler 把策略 JSON 还原为具体策略指针。
func DecodeCurrentPolicy(kind string, raw []byte) (any, error) {
	h, ok := kindRegistry[kind]
	if !ok {
		return nil, fmt.Errorf("unsupported policy kind: %s", kind)
	}
	return h.decode(raw)
}
```

> 注:此时 `kindRegistry` 的 `test` 字段已赋值但尚未被调用(`TestPolicy` 仍走旧 switch),将在 Task 2 接上 —— 这是预期的中间态。本步只跑 `go test`,不跑 lint(lint 留到 Task 4 接好后再跑)。

- [ ] **Step 4: 运行,确认通过**

Run: `go test ./internal/ai/policy/ -run 'TestPolicyKindRegistry|TestDecodeCurrentPolicy|TestResolvePolicyKind' -count=1`
Expected: PASS(ok）。

- [ ] **Step 5: 提交**

```bash
git add internal/ai/policy/policy_kind.go internal/ai/policy/policy_kind_test.go
git commit -m "✨ 新增 policyKind 注册表与 resolver/decode #130"
```

---

## Task 2: TestPolicy 去 switch,改注册表分发

**Files:**
- Create: `internal/ai/policy/policy_dispatch_test.go`
- Modify: `internal/ai/policy/policy_tester.go:16-60`
- Modify: `internal/ai/policy/policy_tester_test.go:474-488`

- [ ] **Step 1: 写失败测试(新结构)**

Create `internal/ai/policy/policy_dispatch_test.go`:

```go
package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPolicyDispatch(t *testing.T) {
	ctx := context.Background()
	Convey("TestPolicy 按 policyKind 分发", t, func() {
		Convey("command kind 应用 Current 策略(deny 命中)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindCommand,
				Current:    &asset_entity.CommandPolicy{DenyList: []string{"curl *"}},
			}, "curl http://example.com")
			So(out.Decision, ShouldEqual, aictx.Deny)
		})
		Convey("redis kind 应用 Current 策略(把 NeedConfirm 提升为 Allow)", func() {
			base := TestPolicy(ctx, PolicyTestInput{PolicyKind: PolicyKindRedis}, "SET k v")
			So(base.Decision, ShouldEqual, aictx.NeedConfirm)
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindRedis,
				Current:    &asset_entity.RedisPolicy{AllowList: []string{"SET *"}},
			}, "SET k v")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("etcd kind 路由到 testEtcdPolicy(修复后非空策略可测)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindEtcd,
				Current:    &asset_entity.EtcdPolicy{Groups: []string{policy.BuiltinEtcdReadOnly}},
			}, "get /config")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("未注册 kind(mongo)返回 NeedConfirm", func() {
			out := TestPolicy(ctx, PolicyTestInput{PolicyKind: PolicyKindMongo}, "anything")
			So(out.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
```

- [ ] **Step 2: 运行,确认编译失败**

Run: `go test ./internal/ai/policy/ -run TestPolicyDispatch -count=1`
Expected: FAIL —— 编译错误,`PolicyTestInput` 无 `PolicyKind` / `Current` 字段。

- [ ] **Step 3: 改 PolicyTestInput 与 TestPolicy**

In `internal/ai/policy/policy_tester.go`,把第 16-28 行的 `PolicyTestInput` 结构替换为:

```go
// PolicyTestInput 策略测试入参
type PolicyTestInput struct {
	PolicyKind string // 规范 policyKind(command/query/redis/k8s/etcd/...);由 ResolvePolicyKind 得到
	AssetID    int64  // 资产ID（从资产的 groupID 开始解析组链）
	GroupID    int64  // 资产组ID（从父组开始解析,当前组策略由 Current 提供）

	// Current 当前编辑中的策略(DecodeCurrentPolicy 的产物,具体类型 *CommandPolicy 等),可为 nil。
	Current any
}
```

把第 44-60 行的 `TestPolicy` 函数体(含 `switch input.PolicyType { ... }`)整体替换为:

```go
// TestPolicy 统一的策略测试入口,按 policyKind 查表分发,解析资产组链并合并策略后检查命令。
func TestPolicy(ctx context.Context, input PolicyTestInput, command string) PolicyTestOutput {
	h, ok := kindRegistry[input.PolicyKind]
	if !ok {
		return PolicyTestOutput{Decision: aictx.NeedConfirm}
	}
	groups := resolveGroupChainForTest(ctx, input.AssetID, input.GroupID)
	return h.test(ctx, input.Current, groups, command)
}
```

> 其余所有函数(`testSSHPolicy`/`testQueryPolicy`/`testRedisPolicy`/`testK8sPolicy`/`testEtcdPolicy`、`collectGroupGenericRules`、`resolveGroupChainForTest`、merge* 等)**保持不动**。`aictx`、`context`、`group_entity`、`asset_entity` 导入仍被这些函数使用,无需改导入。

- [ ] **Step 4: 修现有 etcd 测试调用**

In `internal/ai/policy/policy_tester_test.go`,把第 474-488 行两处 Convey 块替换为(仅改 `PolicyType:`→`PolicyKind: PolicyKindEtcd`、`CurrentEtcd:`→`Current:`):

```go
		Convey("通过 TestPolicy 入口 — kind \"etcd\" 路由", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindEtcd,
				Current:    &asset_entity.EtcdPolicy{Groups: []string{policy.BuiltinEtcdReadOnly}},
			}, "get /config")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("通过 TestPolicy 入口 — member remove 被拒", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindEtcd,
				Current:    &asset_entity.EtcdPolicy{Groups: []string{policy.BuiltinEtcdDangerousDeny}},
			}, "member remove abc")
			So(out.Decision, ShouldEqual, aictx.Deny)
		})
```

- [ ] **Step 5: 运行整个 policy 包,确认全绿**

Run: `go test ./internal/ai/policy/ -count=1`
Expected: PASS —— 新增 `TestPolicyDispatch` 通过,且原有 `TestTestSSHPolicy/TestTestRedisPolicy/TestTestK8sPolicy/TestTestQueryPolicy/TestTestEtcdPolicy/TestCheckGenericDenyAllow` 全部仍绿(行为保持)。

- [ ] **Step 6: 提交**

```bash
git add internal/ai/policy/policy_tester.go internal/ai/policy/policy_tester_test.go internal/ai/policy/policy_dispatch_test.go
git commit -m "♻️ policy 测试入口去 switch 改 policyKind 注册表分发 #130"
```

---

## Task 3: app 层 TestPolicyRule 改用 resolver + decode(修复 etcd/mongo/kafka 闸门)

**Files:**
- Modify: `internal/app/system/asset.go:25-101`

- [ ] **Step 1: 改 PolicyTestRequest 注释**

In `internal/app/system/asset.go`,把第 26 行注释更新为(说明取值经 resolver 映射):

```go
	PolicyType string `json:"policyType"` // 前端资产 policyType(ssh/database/redis/k8s/etcd/...);经 ResolvePolicyKind 映射到 policyKind
```

- [ ] **Step 2: 替换 TestPolicyRule 的 per-type unmarshal switch**

把第 48-83 行(从 `input := policy.PolicyTestInput{` 到对应 `}` 之前 `result := ...` 之上,即原 input 构造 + `if req.PolicyJSON != "" { switch ... }`)整体替换为:

```go
	kind, ok := policy.ResolvePolicyKind(req.PolicyType)
	if !ok {
		return nil, fmt.Errorf("unsupported policy type: %s", req.PolicyType)
	}

	input := policy.PolicyTestInput{
		PolicyKind: kind,
		AssetID:    req.AssetID,
		GroupID:    req.GroupID,
	}
	if req.PolicyJSON != "" {
		current, err := policy.DecodeCurrentPolicy(kind, []byte(req.PolicyJSON))
		if err != nil {
			return nil, fmt.Errorf("invalid %s policy JSON: %w", req.PolicyType, err)
		}
		input.Current = current
	}
```

> 第 85 行 `result := policy.TestPolicy(...)` 及之后不变。`asset_entity` 仍被 149-164 行使用,导入保留;`json` 仍被本文件其他函数使用(如 `GetDefaultPolicy` 第 109 行),导入保留。

- [ ] **Step 3: 编译 + 跑受影响包**

Run: `go build ./... && go test ./internal/app/system/ ./internal/ai/policy/ -count=1`
Expected: 编译通过;两包测试 PASS。

> 行为差异(预期):`req.PolicyType` 为 `etcd` 且 `PolicyJSON` 非空时,旧代码返回 `unsupported policy type: etcd`,现在正确走 `testEtcdPolicy`(修复 spec 第 3 发现的潜在 bug)。`mongo`/`kafka` 经 `ResolvePolicyKind` 返回 false → 仍报 `unsupported policy type`,行为不变(待阶段 1b 补齐)。

- [ ] **Step 4: 提交**

```bash
git add internal/app/system/asset.go
git commit -m "🐛 修复 etcd 策略测试在 app 层被错误拦截,改用 policyKind resolver #130"
```

---

## Task 4: 全量校验与观测验收

**Files:** 无(仅校验)

- [ ] **Step 1: 受影响包全测**

Run: `go test ./internal/ai/policy/ ./internal/app/system/ -count=1`
Expected: PASS。

- [ ] **Step 2: 后端 lint(项目用 golangci-lint,非 go vet)**

Run: `golangci-lint run ./internal/ai/policy/... ./internal/app/system/...`
Expected: 无新增告警(特别确认 `policyKindHandler.test` 字段已被 `TestPolicy` 使用,不报 unused)。

- [ ] **Step 3: 观测验收 etcd 修复(GUI 不可点,按 AGENTS.md 用观测)**

按 `docs/testing-debugging-guide.md`:运行应用或 `opsctl`,对一个 etcd 资产,在策略编辑面板填非空 allow/deny 后点"测试",确认返回正常 allow/deny/need_confirm 而非 `unsupported policy type`;必要时读 `logs/opskat.log` 确认无 `unsupported policy type: etcd`。
> 若本机无 etcd 资产,可跳过手测 —— Task 2 的 `TestPolicyDispatch` 已在 dispatch 层覆盖 etcd 非空策略路由;此步仅为端到端旁证。

- [ ] **Step 4: 标记阶段完成**

确认 spec 中阶段 0 + 1a 已落地。后续 plan:阶段 1b(补 mongo/kafka 的 `Effective*`/merge + `testMongo/testKafka` 并注册)、阶段 1c(builtin groups 按 kind 拆分,entity 层)、阶段 2(assettype handler 收口:统一连接测试 binding + `GetConfig` 走注册表 + `PolicyKind()`)。

---

## Self-Review 备忘(写计划时已核对)

- **Spec 覆盖**:第 0 节词表 → Task 1;第 1 节注册表 B 去 switch → Task 2;app 边界 + etcd 修复 → Task 3;mongo/kafka 不在本阶段(spec 已标阶段 1b);builtin groups 拆分(注册表 A)不在本阶段(spec 阶段 1c)。✅
- **无占位符**:每个改动步骤均含完整代码与精确行号区间。✅
- **类型一致**:`PolicyKind*` 常量、`kindRegistry`、`policyKindHandler{decode,test}`、`ResolvePolicyKind`、`DecodeCurrentPolicy`、`PolicyTestInput{PolicyKind,AssetID,GroupID,Current}` 在 Task 1/2/3 用法一致。✅
- **行为保持**:5 个既有 kind 委托原 `testXxx`,原有测试不改逻辑只改 etcd 入口两处调用形态;唯一观测变化是 etcd app 层修复(已加 dispatch 测试 + 观测步骤)。✅
