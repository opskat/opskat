# 阶段 2b：连接测试 binding 统一(TestAssetConnection)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans。用 TDD 逐 task 执行,checkbox 跟踪。

**Goal:** 把 7 个签名各异的「资产表单连接测试」binding 收敛成单个 `App.TestAssetConnection(testID, assetType, configJSON, plainPassword)`,内部经 runtime 注册表分发,去掉前端的 7 路 binding 调用。

**Architecture:** 测试函数是各 binder 的实例方法(持有 live manager/pool),无法在 `init()` 注册 → 新建 runtime 注册表 `internal/service/conntest`(镜像 `internal/service/testreg`),各 binder 在 `New()` 时把「去掉信封的私有 tester」注册进去;`System` binder(已承载资产 CRUD + i18n/ctx/testreg 信封)新增 `TestAssetConnection`,做一次共享信封后查表分发。无 type switch(满足 OCP)。

**Tech Stack:** Go 1.26 / Wails v2 / React 19。改 binding 签名 → 需 `wails generate` 重生 `frontend/wailsjs`(gitignore 生成物)。

**行为保持:** 各 tester body = 原 body 去掉信封(timeout + `testreg.Begin` + `i18n.Ctx`);信封移到 `System` 后等价(运行时所有 binder 的 `ctx`=同一 wails ctx,`lang` 同源 System)。唯一可见差异:etcd resolve-password 错误日志去掉 `testID` 字段(testID 现由 host 信封持有)。

**非目标(留给阶段 4):** AssetForm 的 per-type config 构建 + `handleRunTestConnection` 三元链不动,本阶段只把 7 个 `await TestXxx(...)` 调用换成统一 binding。

---

## 文件结构

- **新建** `internal/service/conntest/registry.go` — runtime tester 注册表(`TestFunc` + `Register`/`Lookup`/`Unregister`,`sync.RWMutex`)。
- **新建** `internal/service/conntest/registry_test.go` — 注册表机制单测(plain `testing`,镜像 `testreg`)。
- **改** `internal/app/system/asset.go` — 新增 `TestAssetConnection` 绑定方法。
- **新建** `internal/app/system/asset_conntest_test.go` — 分发 / 未知类型 / 取消信封单测(白盒 fake tester)。
- **改** `internal/app/ssh/ssh_ops.go` + `ssh/ssh.go` — 私有 `testConnection` + `New()` 注册;删 `TestSSHConnection`。
- **改** `internal/app/query/query_ops.go` + `query/query.go` — 3 个私有 tester + 注册;删 3 个公开 binding。
- **改** `internal/app/kafka/kafka_ops.go` + `kafka/kafka.go`。
- **改** `internal/app/serial/serial_ops.go` + `serial/serial.go`。
- **改** `internal/app/etcd/etcd_ops.go` + `etcd/etcd.go` — 仅 `EtcdTestConfig`(`EtcdTestConnection(assetID)` 是测已存资产,保留)。
- **新建** 各 binder 包 `*_conntest_test.go` — 坏 JSON characterization(白盒 `&Binder{}`,无网络)。
- **改** `frontend/src/components/asset/AssetForm.tsx` — 7 路调用换成 `TestAssetConnection`。
- `frontend/wailsjs/**`(生成物,`wails generate` 重生,不手改、不提交)。

---

### Task 1: conntest runtime 注册表

**Files:**
- Create: `internal/service/conntest/registry.go`
- Test: `internal/service/conntest/registry_test.go`

- [ ] **Step 1: 写失败测试**

```go
package conntest

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterAndLookup(t *testing.T) {
	defer Unregister("dummy")
	want := errors.New("boom")
	Register("dummy", func(_ context.Context, cfg, pw string) error {
		if cfg != "C" || pw != "P" {
			t.Fatalf("tester got cfg=%q pw=%q", cfg, pw)
		}
		return want
	})
	fn, ok := Lookup("dummy")
	if !ok {
		t.Fatal("expected dummy registered")
	}
	if got := fn(context.Background(), "C", "P"); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown type should not be found")
	}
}

func TestUnregister(t *testing.T) {
	Register("temp", func(context.Context, string, string) error { return nil })
	Unregister("temp")
	if _, ok := Lookup("temp"); ok {
		t.Fatal("temp should be gone after Unregister")
	}
}
```

- [ ] **Step 2: 跑测试看失败**

Run: `go test ./internal/service/conntest/`
Expected: FAIL — 包/符号不存在(undefined: Register/Lookup/Unregister）。

- [ ] **Step 3: 写最小实现**

```go
// Package conntest 维护资产「表单连接测试」的 runtime 分发注册表。
//
// 各连接测试是 binder 的实例方法(持有 live manager/pool),无法在 init() 注册;
// 故 binder 在 New() 时把去掉信封的 tester 注册进来,由 system binder 的
// TestAssetConnection 统一查表分发(共享 i18n ctx + 超时 + testreg 取消信封)。
package conntest

import (
	"context"
	"sync"
)

// TestFunc 用给定 ctx(已含超时/取消)测试一份未保存的资产配置。
// configJSON 是前端配置的 JSON;plainPassword 为空时由 tester 自行兜底解析。
type TestFunc func(ctx context.Context, configJSON, plainPassword string) error

var (
	mu        sync.RWMutex
	testers   = make(map[string]TestFunc)
)

// Register 登记某资产类型的 tester(同类型重复登记以最后一次为准)。
func Register(assetType string, fn TestFunc) {
	mu.Lock()
	testers[assetType] = fn
	mu.Unlock()
}

// Unregister 移除某资产类型的 tester(主要供测试清理)。
func Unregister(assetType string) {
	mu.Lock()
	delete(testers, assetType)
	mu.Unlock()
}

// Lookup 取某资产类型的 tester;未注册返回 ok=false。
func Lookup(assetType string) (TestFunc, bool) {
	mu.RLock()
	fn, ok := testers[assetType]
	mu.RUnlock()
	return fn, ok
}
```

- [ ] **Step 4: 跑测试看通过**

Run: `go test ./internal/service/conntest/`
Expected: PASS。`gofmt -w internal/service/conntest/registry.go`。

---

### Task 2: System.TestAssetConnection 绑定 + 信封

**Files:**
- Modify: `internal/app/system/asset.go`
- Test: `internal/app/system/asset_conntest_test.go`

- [ ] **Step 1: 写失败测试**(白盒,`package system`)

```go
package system

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/testreg"
)

func newTestSystem() *System {
	s := New(context.Background(), SkillContent{})
	s.ctx = context.Background()
	return s
}

func TestTestAssetConnectionDispatch(t *testing.T) {
	defer conntest.Unregister("dummy")
	want := errors.New("dial failed")
	var gotCfg, gotPw string
	conntest.Register("dummy", func(_ context.Context, cfg, pw string) error {
		gotCfg, gotPw = cfg, pw
		return want
	})
	err := newTestSystem().TestAssetConnection("tid", "dummy", "CFG", "PW")
	if err != want {
		t.Fatalf("got %v, want %v", err, want)
	}
	if gotCfg != "CFG" || gotPw != "PW" {
		t.Fatalf("tester got cfg=%q pw=%q", gotCfg, gotPw)
	}
}

func TestTestAssetConnectionUnknownType(t *testing.T) {
	if err := newTestSystem().TestAssetConnection("tid", "nope", "{}", ""); err == nil {
		t.Fatal("expected error for unknown asset type")
	}
}

func TestTestAssetConnectionCancellable(t *testing.T) {
	defer conntest.Unregister("blocker")
	started := make(chan struct{})
	conntest.Register("blocker", func(ctx context.Context, _, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	done := make(chan error, 1)
	go func() { done <- newTestSystem().TestAssetConnection("cancel-me", "blocker", "{}", "") }()
	<-started
	testreg.Cancel("cancel-me")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected non-nil error after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TestAssetConnection did not unblock on cancel")
	}
}
```

- [ ] **Step 2: 跑测试看失败**

Run: `go test ./internal/app/system/ -run TestTestAssetConnection`
Expected: FAIL — `TestAssetConnection` 未定义。

- [ ] **Step 3: 写最小实现**(加到 `asset.go`;补 `context`/`time`/`conntest`/`testreg` import)

```go
// TestAssetConnection 测试一份未保存的资产配置(资产表单「测试连接」)。
// testID 配合 CancelTest 中断;assetType 经 conntest 注册表分发到对应 binder 的 tester。
// 共享信封(i18n ctx + 10s 超时 + testreg 取消)在此统一施加,各 tester 只做解析/解析凭据/拨号。
func (s *System) TestAssetConnection(testID, assetType, configJSON, plainPassword string) error {
	fn, ok := conntest.Lookup(assetType)
	if !ok {
		return fmt.Errorf("unsupported asset type: %s", assetType)
	}
	parent, cancel := context.WithTimeout(i18n.Ctx(s.ctx, s.Lang()), 10*time.Second)
	defer cancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()
	return fn(ctx, configJSON, plainPassword)
}
```

- [ ] **Step 4: 跑测试看通过**

Run: `go test ./internal/app/system/ -run TestTestAssetConnection`
Expected: PASS。

---

### Task 3: SSH binder — 抽私有 tester + 注册

**Files:**
- Modify: `internal/app/ssh/ssh_ops.go`(`TestSSHConnection` → 私有 `testConnection`,去信封)
- Modify: `internal/app/ssh/ssh.go`(`New()` 末尾注册)
- Test: `internal/app/ssh/ssh_conntest_test.go`

- [ ] **Step 1: 写失败测试**(白盒 `&SSH{}`,坏 JSON 不触网)

```go
package ssh

import (
	"context"
	"testing"
)

func TestSSHTesterBadJSON(t *testing.T) {
	if err := (&SSH{}).testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
```

- [ ] **Step 2: 跑测试看失败**

Run: `go test ./internal/app/ssh/ -run TestSSHTesterBadJSON`
Expected: FAIL — `testConnection` 未定义。

- [ ] **Step 3: 改实现** — 把 `func (s *SSH) TestSSHConnection(testID string, configJSON string, plainPassword string) error` 改名为 `func (s *SSH) testConnection(ctx context.Context, configJSON string, plainPassword string) error`,**删掉**信封三行:

```go
	parent, parentCancel := context.WithTimeout(i18n.Ctx(s.ctx, s.lang.Lang()), 10*time.Second)
	defer parentCancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()
```

(unmarshal 在最前、原样保留;后续 body 不变,继续用 `ctx`/`s.manager`/`credential_svc.Default()`。)在 `ssh.go` 的 `New()` `return` 前注册:

```go
	conntest.Register(asset_entity.AssetTypeSSH, b.testConnection)
```

(`b` = 即将返回的 `*SSH` 变量名,按现有构造体改;补 `conntest` + `asset_entity` import,若未引入。)删除现在不再使用的 import:`ssh_ops.go` 里的 `testreg`,以及若不再被其它方法引用的 `time`。

- [ ] **Step 4: 跑测试 + 构建**

Run: `go build ./... && go test ./internal/app/ssh/ -run TestSSHTesterBadJSON`
Expected: PASS,无 unused import。

- [ ] **Step 5: gofmt + lint**

Run: `gofmt -w internal/app/ssh/ && golangci-lint run ./internal/app/ssh/...`
Expected: 0 issues。

---

### Task 4: Query binder — 3 个私有 tester + 注册

**Files:**
- Modify: `internal/app/query/query_ops.go`(`TestDatabaseConnection`/`TestRedisConnection`/`TestMongoDBConnection` → 私有 `testDatabaseConnection`/`testRedisConnection`/`testMongoConnection`,各去信封)
- Modify: `internal/app/query/query.go`(`New()` 注册 3 类)
- Test: `internal/app/query/query_conntest_test.go`

- [ ] **Step 1: 写失败测试**

```go
package query

import (
	"context"
	"testing"
)

func TestQueryTestersBadJSON(t *testing.T) {
	q := &Query{}
	for _, fn := range []func(context.Context, string, string) error{
		q.testDatabaseConnection, q.testRedisConnection, q.testMongoConnection,
	} {
		if err := fn(context.Background(), "{not json", ""); err == nil {
			t.Fatal("expected parse error for malformed config JSON")
		}
	}
}
```

- [ ] **Step 2: 跑测试看失败**

Run: `go test ./internal/app/query/ -run TestQueryTestersBadJSON`
Expected: FAIL — 方法未定义。

- [ ] **Step 3: 改实现** — 3 个方法各改名为私有 + 签名首参 `ctx context.Context`(替代 `testID string`),删信封三行,body 其余不变(继续用 `q.pool`/`credential_resolver.Default()`/`connpool.Dial*`)。`query.go` `New()` 注册:

```go
	conntest.Register(asset_entity.AssetTypeDatabase, q.testDatabaseConnection)
	conntest.Register(asset_entity.AssetTypeRedis, q.testRedisConnection)
	conntest.Register(asset_entity.AssetTypeMongoDB, q.testMongoConnection)
```

删 `query_ops.go` 的 `testreg` import(3 处用法全移除后)及不再用的 `time`(若有)。

- [ ] **Step 4: 构建 + 测试**

Run: `go build ./... && go test ./internal/app/query/ -run TestQueryTestersBadJSON`
Expected: PASS。

- [ ] **Step 5: gofmt + lint**

Run: `gofmt -w internal/app/query/ && golangci-lint run ./internal/app/query/...`
Expected: 0 issues。

---

### Task 5: Kafka binder

**Files:**
- Modify: `internal/app/kafka/kafka_ops.go`(`TestKafkaConnection` → `testConnection`)
- Modify: `internal/app/kafka/kafka.go`(`New()` 注册)
- Test: `internal/app/kafka/kafka_conntest_test.go`

- [ ] **Step 1: 写失败测试**

```go
package kafka

import (
	"context"
	"testing"
)

func TestKafkaTesterBadJSON(t *testing.T) {
	if err := (&Kafka{}).testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
```

- [ ] **Step 2: 跑测试看失败** — `go test ./internal/app/kafka/ -run TestKafkaTesterBadJSON` → FAIL。
- [ ] **Step 3: 改实现** — `TestKafkaConnection(testID,...)` → `testConnection(ctx context.Context, configJSON, plainPassword string)`,删信封,body 末尾 `k.service.TestConnection(ctx, &cfg, plainPassword, 0)` 不变;`kafka.go` `New()` 注册 `conntest.Register(asset_entity.AssetTypeKafka, k.testConnection)`;删 `kafka_ops.go` 的 `testreg`、不再用的 `time`/`i18n`(核实:`i18n` 仍被其它 Kafka* 方法用 → 保留)。
- [ ] **Step 4: 构建 + 测试** — `go build ./... && go test ./internal/app/kafka/ -run TestKafkaTesterBadJSON` → PASS。
- [ ] **Step 5: gofmt + lint** — `gofmt -w internal/app/kafka/ && golangci-lint run ./internal/app/kafka/...` → 0 issues。

---

### Task 6: Serial binder(无密码)

**Files:**
- Modify: `internal/app/serial/serial_ops.go`(`TestSerialConnection` → `testConnection`,新增并忽略 `plainPassword` 参数以匹配 `TestFunc`)
- Modify: `internal/app/serial/serial.go`(`New()` 注册)
- Test: `internal/app/serial/serial_conntest_test.go`

- [ ] **Step 1: 写失败测试**

```go
package serial

import (
	"context"
	"testing"
)

func TestSerialTesterBadJSON(t *testing.T) {
	if err := (&Serial{}).testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
```

- [ ] **Step 2: 跑测试看失败** — FAIL。
- [ ] **Step 3: 改实现** — `TestSerialConnection(testID, configJSON string)` → `testConnection(ctx context.Context, configJSON string, _ string)`(末参占位匹配 `TestFunc`),删信封,`s.manager.TestConnection(ctx, ...)` 不变;`serial.go` `New()` 注册 `conntest.Register(asset_entity.AssetTypeSerial, s.testConnection)`;删 `testreg`、按需 `time`(`i18n` 仍被 `WriteSerial` 等用 → 保留)。
- [ ] **Step 4: 构建 + 测试** — PASS。
- [ ] **Step 5: gofmt + lint** — 0 issues。

---

### Task 7: Etcd binder(仅 EtcdTestConfig)

**Files:**
- Modify: `internal/app/etcd/etcd_ops.go`(`EtcdTestConfig` → 私有 `testConnection`;保留 `EtcdTestConnection(assetID)`)
- Modify: `internal/app/etcd/etcd.go`(`New()` 注册)
- Test: `internal/app/etcd/etcd_conntest_test.go`

- [ ] **Step 1: 写失败测试**

```go
package etcd

import (
	"context"
	"testing"
)

func TestEtcdTesterBadJSON(t *testing.T) {
	if err := (&Etcd{}).testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
```

- [ ] **Step 2: 跑测试看失败** — FAIL。
- [ ] **Step 3: 改实现** — `EtcdTestConfig(testID,...)` → `testConnection(ctx context.Context, configJSON, plainPassword string)`,删信封;resolve-password 错误日志去掉 `zap.String("testID", testID)`(testID 不再入参),其余 `logger.Ctx(ctx).Error(...)` 保留;末尾 `e.service.TestConfig(ctx, &cfg, password)` 不变。`etcd.go` `New()` 注册 `conntest.Register(asset_entity.AssetTypeEtcd, e.testConnection)`;删 `testreg`、按需 `time`(`i18n` 仍被 `EtcdTestConnection`/`EtcdExec` 用 → 保留;`logger`/`zap` 仍用 → 保留)。
- [ ] **Step 4: 构建 + 测试** — PASS。
- [ ] **Step 5: gofmt + lint** — 0 issues。

---

### Task 8: 删公开 binding 善后 + wails generate + 前端切换 + 全量校验

**Files:**
- Modify: `frontend/src/components/asset/AssetForm.tsx`
- (生成物)`frontend/wailsjs/**` 由 `wails generate` 重生

- [ ] **Step 1: 确认 7 个公开 binding 已全部移除** — Task 3–7 已把它们改成私有 tester。grep 复核:

Run: `grep -rn "func (.*) TestSSHConnection\|TestDatabaseConnection\|TestRedisConnection\|TestMongoDBConnection\|TestKafkaConnection\|TestSerialConnection\|EtcdTestConfig" internal/app/`
Expected: 无输出(全部消失);`EtcdTestConnection` 仍在(保留)。

- [ ] **Step 2: 重生 wails binding**

Run: `wails generate module`
Expected: 成功;`frontend/wailsjs/go/system/System.d.ts` 出现 `TestAssetConnection`,`ssh/SSH.d.ts` 等不再有旧 `Test*` 方法。

- [ ] **Step 3: 改 AssetForm import** — 删第 30–34 行的 7 个 test binding import;`system` import 行加入 `TestAssetConnection`:

```ts
import { ListCredentialsByType, CancelTest, TestAssetConnection } from "../../../wailsjs/go/system/System";
import { ListLocalSSHKeys } from "../../../wailsjs/go/ssh/SSH";
```

(若 `ListLocalSSHKeys` 是 ssh 唯一剩余 import 则保留该行;`query`/`etcd`/`kafka`/`serial` 的 test-only import 行整行删除,除非该行还引入了别的符号——核实后处理。)

- [ ] **Step 4: 改 7 个 handler 的调用** — 每个 `handleTest*` 内把
  - `await TestSSHConnection(testId, JSON.stringify(sshConfig), password);` → `await TestAssetConnection(testId, "ssh", JSON.stringify(sshConfig), password);`
  - `await TestDatabaseConnection(testId, JSON.stringify(cfg), password);` → `await TestAssetConnection(testId, "database", JSON.stringify(cfg), password);`
  - Redis → `"redis"`;MongoDB → `"mongodb"`;Kafka → `"kafka"`;Etcd `EtcdTestConfig(...)` → `TestAssetConnection(testId, "etcd", JSON.stringify(cfg), password)`;
  - Serial `await TestSerialConnection(testId, JSON.stringify(cfg));` → `await TestAssetConnection(testId, "serial", JSON.stringify(cfg), "");`

  (各 handler 内 `assetType` 等于该字面量,直接用字面量更清晰;config 构建、testId/notify/取消逻辑全不动。)

- [ ] **Step 5: 前端类型检查**

Run: `cd frontend && npx tsc --noEmit`(或项目既有 `pnpm typecheck` 脚本)
Expected: 0 errors(无残留旧 binding 引用)。

- [ ] **Step 6: 后端全量校验**

Run: `go build ./... && go test ./internal/...`
Expected: 全绿。

- [ ] **Step 7: race + lint**

Run: `go test ./internal/service/conntest ./internal/app/system -race && golangci-lint run ./internal/...`
Expected: 干净,0 issues。

- [ ] **Step 8: 更新 spec 记录** — 在 `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md` 追加「阶段 2b 完成记录」:统一 binding、conntest 注册表、删 7 公开 binding、前端切换、行为保持(etcd 日志去 testID 为唯一差异)、`EtcdTestConnection(assetID)` 保留、`wails generate` 已跑。标注仍留给后续:阶段 3(options.ts 元数据折叠)/ 4(AssetForm 组件注册化:把 config 构建 + 三元链收进注册表)/ 5(AssetTree 文件管理硬编码)/ 6(skill)。

---

## Self-Review

- **Spec 覆盖**:第 2 节「7 个连接测试 binding 收敛到一个 `TestAssetConnection`,etcd outlier 拉齐」→ Task 1/2 + 3–7 + 8。`GetConfig` switch 阶段 2a 已核实 layering-blocked,不在 2b。
- **Placeholder**:无 TBD;每 step 有具体代码/命令/期望。
- **类型一致**:`TestFunc = func(ctx, configJSON, plainPassword string) error` 贯穿 conntest / System 分发 / 5 binder tester / 前端 4 参调用,签名一致;serial 末参占位、etcd 去 testID 已显式说明。
