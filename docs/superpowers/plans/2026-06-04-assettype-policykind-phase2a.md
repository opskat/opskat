# 阶段 2a：assettype handler 声明 PolicyKind() + assetTypeToKind 派生 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `policyKind` 从 `assettype` handler 的新方法 `PolicyKind()` 派生(经注册表),删除 `ai/policy` 里手维护的 `assetTypeToKind` 字面量,关闭阶段 1a 留下的"type→kind 三处重复"备忘(其一)。

**Architecture:** 在最底层、无 gorm 依赖的 `internal/model/entity/policy` 包内,镜像既有 `RegisterDefaultPolicy/GetDefaultPolicyOf` 模式,新增 `RegisterAssetKind/AssetKindOf` 资产→kind 注册表与规范 `PolicyKind*` 常量。`assettype.AssetTypeHandler` 增补 `PolicyKind() string`,9 个内置 handler 各自声明;`assettype.Register(h)` 把 `h.PolicyKind()` 写入注册表(单一接线点,OCP)。`ai/policy.ResolvePolicyKind` 改读 `AssetKindOf` + 一条 `kubernetes` 别名 + kind 兜底,删除字面量;`ai/policy.PolicyKind*` 常量改为 alias 到 `entity/policy`,把 ai/policy⟷entity/policy⟷handler 三处词表收敛为一处定义。

**Tech Stack:** Go 1.26;goconvey/testify;golangci-lint。后端 only,无 `wails generate`、无前端、无 `app/system` 改动。

**关键事实(已核实)**
- 仅 9 个内置 handler 实现 `AssetTypeHandler`(扩展类型经 `pkg/extension/bridge.go` 直接调 `policy.RegisterDefaultPolicy`,不实现该接口);`registry_test.go` 的 `stubHandler` 也实现它 → 加接口方法需同步加 stub 实现。
- `ResolvePolicyKind` 生产唯一调用方:`internal/app/system/asset.go:48`,传前端 `policyType`。前端实际取值:`ssh/database/redis/mongo/kafka/k8s/etcd`(`serial` 发 `ssh`,`mongodb` 资产发 `mongo`)。`kubernetes` 前端从不发,仅被现有单测锁定 → 用 1 条别名保行为。
- `entity/policy` 干净(无 gorm / asset_entity / assettype),`assettype` 与 `ai/policy` 均已(直接或传递)依赖它 → 常量与注册表放此处不引入新依赖边、无环。
- `app/system` 传递依赖 `assettype` → 其测试二进制会触发 handler `init()` 注册,删除字面量后仍能解析真实类型;且 `app/system` 无 `TestPolicyRule`/`ResolvePolicyKind` 单测。
- 各 handler `Type()`→目标 kind:ssh/serial/local→command,database→query,redis→redis,mongodb→mongo,kafka→kafka,k8s→k8s,etcd→etcd。

---

## File Structure

- `internal/model/entity/policy/registry.go` — 既有默认策略注册表;**追加** kind 常量 + asset-kind 注册表。
- `internal/model/entity/policy/registry_test.go` — **新建/追加** asset-kind 注册表单测。
- `internal/assettype/registry.go` — 接口加 `PolicyKind()`;`Register()` 接线 `RegisterAssetKind`。
- `internal/assettype/{ssh,database,redis,mongodb,kafka,k8s,serial,local,etcd}.go` — 各加 `PolicyKind()` 实现。
- `internal/assettype/registry_test.go` — `stubHandler` 加 `PolicyKind()`;新增 handler→kind 接线断言。
- `internal/ai/policy/policy_kind.go` — `PolicyKind*` 改 alias;`ResolvePolicyKind` 改派生;删除 `assetTypeToKind`。
- `internal/ai/policy/policy_kind_test.go` — `TestResolvePolicyKind` 改为 seed fixtures 的 resolver 单测。

---

## Task 1: entity/policy 新增 kind 常量 + asset-kind 注册表

**Files:**
- Modify: `internal/model/entity/policy/registry.go`
- Test: `internal/model/entity/policy/registry_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/model/entity/policy/registry_test.go`(若不存在则新建,`package policy`):

```go
package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssetKindRegistry(t *testing.T) {
	RegisterAssetKind("faketype", PolicyKindCommand)
	defer UnregisterAssetKind("faketype")

	got, ok := AssetKindOf("faketype")
	assert.True(t, ok)
	assert.Equal(t, PolicyKindCommand, got)

	_, ok = AssetKindOf("never-registered")
	assert.False(t, ok)
}

func TestPolicyKindConstants(t *testing.T) {
	assert.Equal(t, "command", PolicyKindCommand)
	assert.Equal(t, "query", PolicyKindQuery)
	assert.Equal(t, "redis", PolicyKindRedis)
	assert.Equal(t, "mongo", PolicyKindMongo)
	assert.Equal(t, "kafka", PolicyKindKafka)
	assert.Equal(t, "k8s", PolicyKindK8s)
	assert.Equal(t, "etcd", PolicyKindEtcd)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/model/entity/policy/ -run 'TestAssetKindRegistry|TestPolicyKindConstants' -v`
Expected: 编译失败(`undefined: RegisterAssetKind/AssetKindOf/UnregisterAssetKind/PolicyKind*`)。

- [ ] **Step 3: 实现常量 + 注册表**

在 `internal/model/entity/policy/registry.go` 顶部(`import` 后)加规范 kind 词表常量:

```go
// PolicyKind* 是 policy 逻辑的规范化种类词表,是资产轴与 policy 轴的唯一映射目标。
// ai/policy.PolicyKind* alias 到这里;policy_group_entity.PolicyType* 暂未收敛(见阶段备忘)。
const (
	PolicyKindCommand = "command"
	PolicyKindQuery   = "query"
	PolicyKindRedis   = "redis"
	PolicyKindMongo   = "mongo"
	PolicyKindKafka   = "kafka"
	PolicyKindK8s     = "k8s"
	PolicyKindEtcd    = "etcd"
)

// assetKindRegistry 资产类型 → 规范 policyKind。由 assettype.Register 在 handler 注册时
// 经 h.PolicyKind() 写入,替代 ai/policy 里手维护的 assetTypeToKind 字面量。
var assetKindRegistry = struct {
	sync.RWMutex
	kinds map[string]string
}{
	kinds: make(map[string]string),
}

// RegisterAssetKind 注册资产类型所用的 policyKind。
func RegisterAssetKind(assetType, kind string) {
	assetKindRegistry.Lock()
	defer assetKindRegistry.Unlock()
	assetKindRegistry.kinds[assetType] = kind
}

// UnregisterAssetKind 注销资产类型的 policyKind(测试用)。
func UnregisterAssetKind(assetType string) {
	assetKindRegistry.Lock()
	defer assetKindRegistry.Unlock()
	delete(assetKindRegistry.kinds, assetType)
}

// AssetKindOf 返回资产类型对应的 policyKind 及是否已注册。
func AssetKindOf(assetType string) (string, bool) {
	assetKindRegistry.RLock()
	defer assetKindRegistry.RUnlock()
	k, ok := assetKindRegistry.kinds[assetType]
	return k, ok
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/model/entity/policy/ -v`
Expected: PASS。

---

## Task 2: assettype 接口加 PolicyKind() + 9 实现 + Register 接线

**Files:**
- Modify: `internal/assettype/registry.go`
- Modify: 9 个 handler 文件
- Test: `internal/assettype/registry_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/assettype/registry_test.go`:给 `stubHandler` 加方法:

```go
func (s *stubHandler) PolicyKind() string { return "" }
```

新增断言(放在 `TestRegistry` 之后,`package assettype`,需 import `policyent "github.com/opskat/opskat/internal/model/entity/policy"`):

```go
func TestHandlerPolicyKind(t *testing.T) {
	convey.Convey("内置 handler 声明 policyKind 并接线到注册表", t, func() {
		want := map[string]string{
			asset_entity.AssetTypeSSH:      policyent.PolicyKindCommand,
			asset_entity.AssetTypeSerial:   policyent.PolicyKindCommand,
			asset_entity.AssetTypeLocal:    policyent.PolicyKindCommand,
			asset_entity.AssetTypeDatabase: policyent.PolicyKindQuery,
			asset_entity.AssetTypeRedis:    policyent.PolicyKindRedis,
			asset_entity.AssetTypeMongoDB:  policyent.PolicyKindMongo,
			asset_entity.AssetTypeKafka:    policyent.PolicyKindKafka,
			asset_entity.AssetTypeK8s:      policyent.PolicyKindK8s,
			asset_entity.AssetTypeEtcd:     policyent.PolicyKindEtcd,
		}
		for typ, kind := range want {
			h, ok := Get(typ)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(h.PolicyKind(), convey.ShouldEqual, kind)
			got, ok := policyent.AssetKindOf(typ)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(got, convey.ShouldEqual, kind)
		}
	})
}

func TestRegisterSkipsEmptyKind(t *testing.T) {
	convey.Convey("PolicyKind 为空的 handler 不污染 asset-kind 注册表", t, func() {
		Register(&stubHandler{typ: "emptykindstub"})
		_, ok := policyent.AssetKindOf("emptykindstub")
		convey.So(ok, convey.ShouldBeFalse)
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/assettype/ -run 'TestHandlerPolicyKind|TestRegisterSkipsEmptyKind' -v`
Expected: 编译失败(`h.PolicyKind undefined` / 各 handler 缺方法)。

- [ ] **Step 3: 接口 + 9 实现 + Register 接线**

`internal/assettype/registry.go`:接口加方法(放在 `DefaultPolicy()` 后):

```go
	// PolicyKind 返回该资产类型所用的规范 policyKind(见 entity/policy.PolicyKind*)。
	// 经 Register 写入 entity/policy 的 asset-kind 注册表,供 ai/policy.ResolvePolicyKind 派生。
	PolicyKind() string
```

import 加 `"github.com/opskat/opskat/internal/model/entity/policy"`(包名 `policy`);`Register` 改为:

```go
func Register(h AssetTypeHandler) {
	mu.Lock()
	registry[h.Type()] = h
	mu.Unlock()
	if kind := h.PolicyKind(); kind != "" {
		policy.RegisterAssetKind(h.Type(), kind)
	}
}
```

各 handler 加 `PolicyKind()`(handler 已 import `entity/policy` 为 `policy`),放在各自 `DefaultPolicy()` 旁:

- `ssh.go`: `func (h *sshHandler) PolicyKind() string { return policy.PolicyKindCommand }`
- `serial.go`: `func (h *serialHandler) PolicyKind() string { return policy.PolicyKindCommand }`
- `local.go`: `func (h *localHandler) PolicyKind() string { return policy.PolicyKindCommand }`
- `database.go`: `func (h *databaseHandler) PolicyKind() string { return policy.PolicyKindQuery }`
- `redis.go`: `func (h *redisHandler) PolicyKind() string { return policy.PolicyKindRedis }`
- `mongodb.go`: `func (h *mongodbHandler) PolicyKind() string { return policy.PolicyKindMongo }`
- `kafka.go`: `func (h *kafkaHandler) PolicyKind() string { return policy.PolicyKindKafka }`
- `k8s.go`: `func (h *k8sHandler) PolicyKind() string { return policy.PolicyKindK8s }`
- `etcd.go`: `func (h *etcdHandler) PolicyKind() string { return policy.PolicyKindEtcd }`

> 注:`serial.go`/`local.go` 当前未 import `entity/policy` → 需加 import。先确认每个文件现有 import 再补。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/assettype/ -v`
Expected: PASS(含既有 handler 测试)。

---

## Task 3: ai/policy 派生 ResolvePolicyKind + alias 常量 + 删字面量

**Files:**
- Modify: `internal/ai/policy/policy_kind.go`
- Test: `internal/ai/policy/policy_kind_test.go`

- [ ] **Step 1: 改 TestResolvePolicyKind 为 seed-fixture 单测**

`ai/policy` 不 import `assettype`,隔离测试时 `AssetKindOf` 为空 → 测试自行 seed。改 `TestResolvePolicyKind`(import `policyent "github.com/opskat/opskat/internal/model/entity/policy"`):

```go
func TestResolvePolicyKind(t *testing.T) {
	// resolver 单测:自行 seed asset→kind(真实 handler 接线由 assettype 包测试覆盖)。
	seed := map[string]string{
		"ssh": PolicyKindCommand, "serial": PolicyKindCommand, "local": PolicyKindCommand,
		"database": PolicyKindQuery, "redis": PolicyKindRedis, "mongodb": PolicyKindMongo,
		"kafka": PolicyKindKafka, "k8s": PolicyKindK8s, "etcd": PolicyKindEtcd,
	}
	for typ, kind := range seed {
		policyent.RegisterAssetKind(typ, kind)
	}
	defer func() {
		for typ := range seed {
			policyent.UnregisterAssetKind(typ)
		}
	}()

	Convey("ResolvePolicyKind", t, func() {
		Convey("注册的资产类型 → kind", func() {
			for in, want := range seed {
				k, ok := ResolvePolicyKind(in)
				So(ok, ShouldBeTrue)
				So(k, ShouldEqual, want)
			}
		})
		Convey("前端别名 mongo(=kind)经 kind 兜底解析", func() {
			k, ok := ResolvePolicyKind("mongo")
			So(ok, ShouldBeTrue)
			So(k, ShouldEqual, PolicyKindMongo)
		})
		Convey("kubernetes 别名 → k8s", func() {
			k, ok := ResolvePolicyKind("kubernetes")
			So(ok, ShouldBeTrue)
			So(k, ShouldEqual, PolicyKindK8s)
		})
		Convey("直接传已注册 kind 原样返回", func() {
			k, ok := ResolvePolicyKind(PolicyKindCommand)
			So(ok, ShouldBeTrue)
			So(k, ShouldEqual, PolicyKindCommand)
		})
		Convey("未知类型 → false", func() {
			_, ok := ResolvePolicyKind("nope")
			So(ok, ShouldBeFalse)
		})
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ai/policy/ -run TestResolvePolicyKind -v`
Expected: 编译失败(`undefined: policyent` import 未用/`UnregisterAssetKind` 还没被 ai/policy 引用前会编译失败)或断言失败(旧 `assetTypeToKind` 不认 seed)。确认失败原因正确后继续。

- [ ] **Step 3: 改 policy_kind.go**

import 加 `policyent "github.com/opskat/opskat/internal/model/entity/policy"`。常量块改 alias:

```go
const (
	PolicyKindCommand = policyent.PolicyKindCommand
	PolicyKindQuery   = policyent.PolicyKindQuery
	PolicyKindRedis   = policyent.PolicyKindRedis
	PolicyKindMongo   = policyent.PolicyKindMongo
	PolicyKindKafka   = policyent.PolicyKindKafka
	PolicyKindK8s     = policyent.PolicyKindK8s
	PolicyKindEtcd    = policyent.PolicyKindEtcd
)
```

删除整个 `assetTypeToKind` 字面量(120-132 行);新增 1 条别名 + 改 `ResolvePolicyKind`:

```go
// assetTypeAlias 前端/历史别名 → 规范资产类型(再经注册表/兜底解析)。
// 资产类型→kind 的主映射由 assettype handler 经 entity/policy 注册表派生,不再手维护。
var assetTypeAlias = map[string]string{
	"kubernetes": PolicyKindK8s, // 前端 k8s 选择别名;policyType 实际发 "k8s"
}

func ResolvePolicyKind(s string) (string, bool) {
	if canon, ok := assetTypeAlias[s]; ok {
		s = canon
	}
	kind, ok := policyent.AssetKindOf(s)
	if !ok {
		kind = s // 允许直接传 kind
	}
	if _, has := kindRegistry[kind]; !has {
		return "", false
	}
	return kind, true
}
```

> 同步:`policy_kind.go` 顶部 `PolicyKind` 词表注释(13 行附近)若提到 `assetTypeToKind`,改为指向注册表派生。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ai/policy/ -v`
Expected: PASS(`TestResolvePolicyKind` + 既有 `TestPolicyKindRegistry`/`TestDecodeCurrentPolicy`/dispatch 全绿)。

---

## Task 4: 全量校验 + spec 记录 + commit

- [ ] **Step 1: 构建 + 全量测试**

Run:
```
go build ./...
go test ./internal/model/entity/policy/ ./internal/assettype/ ./internal/ai/policy/ ./internal/app/system/
go test ./internal/...
go test ./internal/ai/policy/ -race
```
Expected: 全绿。

- [ ] **Step 2: lint**

Run: `golangci-lint run ./internal/model/entity/policy/... ./internal/assettype/... ./internal/ai/policy/...`
Expected: 0 issues。

- [ ] **Step 3: spec 记录**

在 `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` 追加"阶段 2a 完成记录",标注:已做(PolicyKind 派生);未做(连接测试统一 binding=2b,GetConfig/Validate switch 受 layering 阻塞留 entity 层);三处重复中 ai/policy⟷entity/policy⟷handler 已收敛,policy_group_entity.PolicyType* 仍独立(待后续)。

- [ ] **Step 4: commit**

```
git add -A
git commit -m "♻️ assettype handler 声明 PolicyKind(),assetTypeToKind 改注册表派生 #130"
```

---

## Self-Review

- **行为保持**:`ResolvePolicyKind` 对全部现有输入(ssh/serial/local/database/redis/mongo/mongodb/kafka/k8s/kubernetes/etcd + 直接 kind + 未知)结果不变;`kubernetes` 经 1 条别名保留,`mongo` 经 kind 兜底。
- **无新依赖环**:常量/注册表落最底层 `entity/policy`;`assettype→entity/policy`、`ai/policy→entity/policy` 均为既有/单向边。
- **OCP**:新增资产类型只需 handler 实现 `PolicyKind()`,`Register` 自动接线;`ResolvePolicyKind`/`assetTypeToKind` 零改动。
- **收敛度**:删 11 条手维护字面量 → 9 条由 handler 派生 + 1 条真·别名;kind 词表常量三处→两处(policy_group_entity 待后续)。
</content>
</invoke>
