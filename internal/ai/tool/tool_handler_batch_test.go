package tool

import (
	"context"
	"strings"
	"sync"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// batchTestEnv is the fixture shared by the TestHandleBatch_* tests below: a ctx wired
// with a permission checker, plus a counter recording how many times each named asset's
// executor actually ran.
type batchTestEnv struct {
	ctx       context.Context
	execCalls map[string]int
}

// replaceExecutorForTest swaps a resource type's *production* executor (registered once,
// by execimpl's init, the moment package tool is loaded — see tool_registry.go's blank
// import) for a fake one, and restores the original exec/help/canonicalize/precheck via
// t.Cleanup.
//
// Dispatch must be exercised against the real canonical asset types (mongodb/database/
// redis) — that's what permission.CanonicalTypeFor's alias table and
// permission.CheckPermission's routing are keyed on — so a brand-new fake type name (the
// pattern tool_handlers_unified_test.go's fakeType uses elsewhere in this package) would
// let a "sql" assertion resolve without ever exercising the routing this file exists to
// lock. RegisterExecutor panics on a duplicate registration (by design — see its doc
// comment), hence the swap-and-restore dance instead of a second RegisterExecutor call.
func replaceExecutorForTest(t *testing.T, assetType string, fake permission.ExecFunc) {
	t.Helper()

	origExec, hadExec := permission.ExecutorFor(assetType)
	origHelp, _ := permission.HelpFor(assetType)
	origCanon, hadCanon := permission.CanonicalizeFor(assetType)
	origPrecheck, hadPrecheck := permission.PrecheckFor(assetType)

	permission.UnregisterExecutorForTest(assetType)
	if hadCanon {
		permission.RegisterExecutor(assetType, fake, origHelp, origCanon)
	} else {
		permission.RegisterExecutor(assetType, fake, origHelp)
	}
	if hadPrecheck {
		permission.RegisterPrecheck(assetType, origPrecheck)
	}

	t.Cleanup(func() {
		permission.UnregisterExecutorForTest(assetType)
		if !hadExec {
			return
		}
		if hadCanon {
			permission.RegisterExecutor(assetType, origExec, origHelp, origCanon)
		} else {
			permission.RegisterExecutor(assetType, origExec, origHelp)
		}
		if hadPrecheck {
			permission.RegisterPrecheck(assetType, origPrecheck)
		}
	})
}

// setupBatch wires a mock asset repo with three fixture assets — "docs-1" (mongodb),
// "prod-db" (database), "cache-1" (redis) — swaps in a counting fake executor for all
// three real types, and injects a permission checker whose confirm callback always
// denies.
//
// Every command used by the tests below is a read op that the built-in read-only policy
// group for its type resolves straight to Allow (mongo "aggregate", SQL "SELECT", redis
// "PING" are each in their type's builtin *-readonly group — see
// internal/model/entity/policy_group_entity/policy_group.go), so the always-deny confirm
// callback is never actually invoked when dispatch is correct. It exists so that the
// mutation check called for by the task brief (hardcode CheckPermission's asset-type
// argument to asset_entity.AssetTypeSSH and rerun) is actually detectable: none of these
// three commands match any pattern in the SSH/shell builtin allow or deny lists, so a
// wrong-type check falls through checkCommandPolicyPermission to NeedConfirm instead of
// resolving directly — which reaches this always-deny callback and drops execCalls to 0,
// distinct from "coincidentally still allowed".
func setupBatch(t *testing.T) *batchTestEnv {
	t.Helper()
	m := setupUnified(t)

	env := &batchTestEnv{execCalls: make(map[string]int)}
	var mu sync.Mutex
	fakeExec := func(_ context.Context, asset *asset_entity.Asset, _, _ string) (string, error) {
		mu.Lock()
		env.execCalls[asset.Name]++
		mu.Unlock()
		return "ok", nil
	}
	for _, typ := range []string{
		asset_entity.AssetTypeMongoDB,
		asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis,
	} {
		replaceExecutorForTest(t, typ, fakeExec)
	}

	docs1 := &asset_entity.Asset{ID: 201, Name: "docs-1", Type: asset_entity.AssetTypeMongoDB}
	if err := docs1.SetMongoDBConfig(&asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: 27017, Database: "app"}); err != nil {
		t.Fatalf("SetMongoDBConfig: %v", err)
	}
	prodDB := &asset_entity.Asset{ID: 202, Name: "prod-db", Type: asset_entity.AssetTypeDatabase}
	cache1 := &asset_entity.Asset{ID: 203, Name: "cache-1", Type: asset_entity.AssetTypeRedis}

	for _, a := range []*asset_entity.Asset{docs1, prodDB, cache1} {
		m.EXPECT().FindByName(gomock.Any(), a.Name).Return([]*asset_entity.Asset{a}, nil).AnyTimes()
		m.EXPECT().Find(gomock.Any(), a.ID).Return(a, nil).AnyTimes()
	}

	confirm := func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		return permission.ApprovalResponse{Decision: "deny"}
	}
	checker := permission.NewCommandPolicyChecker(confirm)
	env.ctx = permission.WithPolicyChecker(context.Background(), checker)
	return env
}

// batch 条目的 type 从"handler 选择器"变成"可选断言"后，未注册执行器的类型必须
// 不再被硬编码的三路 switch 挡在门外。
//
// Deviates from the brief's literal command string ("find app.users {}"): the actual
// mongo command grammar (ParseMongoCommand, internal/ai/helper/mongo_command.go) is
// "<op> [collection] [--db=...] [--query=...]" with at most one positional argument, so
// "find app.users {}" (two bare positional args, no --query= flag) fails to parse
// regardless of dispatch — it would never have reached the switch this test locks.
// "aggregate logs" is a real mongo read op (in the mongo-readonly builtin group, same as
// "find") that parses and canonicalizes cleanly against docs-1's configured default
// database.
func TestHandleBatch_DispatchesByAssetType(t *testing.T) {
	env := setupBatch(t) // 注册 mongodb 假执行器，资产 "docs-1" 是 mongodb 类型

	out, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"docs-1","command":"aggregate logs"}]`,
	})
	if err != nil {
		t.Fatalf("mongodb asset must be dispatchable in batch, got %v", err)
	}
	if env.execCalls["docs-1"] != 1 {
		t.Errorf("executor for the asset's real type must run exactly once, got %d", env.execCalls["docs-1"])
	}
	if strings.Contains(out, "unknown type") {
		t.Errorf("output still mentions the retired type switch: %s", out)
	}
}

// 旧写法（协议别名前缀）继续可用——opsctl batch 的 'sql:2:SELECT 1' 走的就是这条。
func TestHandleBatch_TypeAliasStillAccepted(t *testing.T) {
	env := setupBatch(t)

	if _, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"prod-db","type":"sql","command":"SELECT 1"}]`,
	}); err != nil {
		t.Fatalf("legacy protocol alias must still be accepted, got %v", err)
	}
	if env.execCalls["prod-db"] != 1 {
		t.Errorf("aliased item must execute, got %d calls", env.execCalls["prod-db"])
	}
}

// 断言不符的条目被拒，且**不执行**；同批次里其它条目不受影响。
func TestHandleBatch_TypeMismatchDeniesOnlyThatItem(t *testing.T) {
	env := setupBatch(t)

	out, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"cache-1","type":"database","command":"PING"},
		              {"asset":"prod-db","command":"SELECT 1"}]`,
	})
	if err != nil {
		t.Fatalf("one bad item must not fail the whole batch: %v", err)
	}
	if env.execCalls["cache-1"] != 0 {
		t.Errorf("mismatched item must not execute, got %d calls", env.execCalls["cache-1"])
	}
	if env.execCalls["prod-db"] != 1 {
		t.Errorf("the sibling item must still run, got %d calls", env.execCalls["prod-db"])
	}
	if !strings.Contains(out, "type=redis") {
		t.Errorf("result must name the real type for the denied item: %s", out)
	}
}

// 空 type 不再被默认成 "exec"：默认值是选择器时代的产物，断言语义下它会让
// 每条未声明 type 的条目都强行断言 ssh。
func TestHandleBatch_EmptyTypeIsNoAssertion(t *testing.T) {
	env := setupBatch(t)

	if _, err := handleBatchCommand(env.ctx, map[string]any{
		"commands": `[{"asset":"cache-1","command":"PING"}]`,
	}); err != nil {
		t.Fatalf("an item without type must dispatch by the asset's real type, got %v", err)
	}
	if env.execCalls["cache-1"] != 1 {
		t.Errorf("redis item without type must execute, got %d calls", env.execCalls["cache-1"])
	}
}
