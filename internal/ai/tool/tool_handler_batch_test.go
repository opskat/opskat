package tool

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
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

// TestHandleBatch_SerialPrecheckBlocksApprovalDialog is handleBatchCommand's counterpart to
// TestHandleExec_SerialPrecheckBlocksApprovalDialog (tool_handlers_unified_test.go): a serial
// asset with no active session must be denied by permission.PrecheckFor before the item ever
// reaches the permission check — no approval dialog, no execution. Serial could not reach
// handleBatchCommand at all before the type-string-selector → optional-assertion rewrite (the
// old three-way switch had no serial branch); this is the test that locks the ordering the
// rewrite had to add at the same time it made serial reachable, to avoid the same
// double-failure handleExec already guards against: approve first, then discover there's no
// session to run the command on.
func TestHandleBatch_SerialPrecheckBlocksApprovalDialog(t *testing.T) {
	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 11, Name: "console-1", Type: asset_entity.AssetTypeSerial}
	m.EXPECT().FindByName(gomock.Any(), "console-1").Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(11)).Return(asset, nil).AnyTimes()

	var execCalls int
	replaceExecutorForTest(t, asset_entity.AssetTypeSerial, func(_ context.Context, _ *asset_entity.Asset, _, _ string) (string, error) {
		execCalls++
		return "ok", nil
	})

	var confirmCalled bool
	confirm := func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		confirmCalled = true
		return permission.ApprovalResponse{Decision: "allow"}
	}
	checker := permission.NewCommandPolicyChecker(confirm)

	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	// Pre-mark the gate documented so this test isolates the precheck: without that, the
	// item would be denied by the doc gate before ever reaching the precheck, and this test
	// wouldn't prove anything about precheck-vs-permission-check ordering.
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeSerial)
	ctx = helper.WithSerialManager(ctx, noSessionSerialManager{})

	out, err := handleBatchCommand(ctx, map[string]any{
		"commands": `[{"asset":"console-1","command":"display version"}]`,
	})
	if err != nil {
		t.Fatalf("handleBatchCommand must not fail the whole batch for one denied item: %v", err)
	}
	if execCalls != 0 {
		t.Errorf("serial item without an active session must not execute, got %d calls", execCalls)
	}
	if confirmCalled {
		t.Fatal("an approval dialog fired for a session-less serial asset — the precheck " +
			"must deny it before the permission check ever runs")
	}
	if !strings.Contains(out, "no active serial session") {
		t.Errorf("result must carry the precheck's reason, got %s", out)
	}
}

// TestHandleBatch_UndocumentedTypeDeniesWithoutExecutingOrConfirming is handleBatchCommand's
// counterpart to TestHandleExec_UndocumentedTypeReturnsGuidance: an item whose asset type the
// model hasn't called help() for yet must be denied before the permission check — no approval
// dialog — and the denial message must be execGuidance's wording (the same guidance handleExec
// gives, so the model can self-correct in the same turn: call help, then retry).
//
// Without this gate, batch_exec is a bypass for the entire doc-gate mechanism: a model could
// execute a type sight-unseen through batch even though the single-command exec path would
// have refused and told it to read the docs first.
func TestHandleBatch_UndocumentedTypeDeniesWithoutExecutingOrConfirming(t *testing.T) {
	env := setupBatch(t)

	var confirmCalled bool
	confirm := func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		confirmCalled = true
		return permission.ApprovalResponse{Decision: "allow"}
	}
	checker := permission.NewCommandPolicyChecker(confirm)
	ctx := permission.WithPolicyChecker(env.ctx, checker)
	ctx = WithDocGate(ctx, NewDocGate()) // fresh gate — nothing marked documented yet

	// SET, not GET/PING: redis's built-in read-only allowlist would resolve a read straight
	// to Allow and never touch the confirm callback, making *confirmCalled vacuously false
	// regardless of whether the gate check actually ran first (same trap documented on
	// newRecordingChecker in tool_handlers_unified_test.go, and on setupBatch above).
	out, err := handleBatchCommand(ctx, map[string]any{
		"commands": `[{"asset":"cache-1","command":"SET foo bar"}]`,
	})
	if err != nil {
		t.Fatalf("handleBatchCommand must not fail the whole batch for one denied item: %v", err)
	}
	if env.execCalls["cache-1"] != 0 {
		t.Errorf("undocumented-type item must not execute, got %d calls", env.execCalls["cache-1"])
	}
	if confirmCalled {
		t.Fatal("an approval dialog fired for a type the model hasn't called help() for — " +
			"the doc gate must deny it before the permission check ever runs")
	}
	// out is the JSON-marshaled result, so execGuidance's embedded double quotes come back
	// backslash-escaped (`\"cache-1\"`), not literal (`"cache-1"`).
	if !strings.Contains(out, `call help(asset=\"cache-1\")`) {
		t.Errorf("result must carry execGuidance's wording, got %s", out)
	}
	if !strings.Contains(out, "type=redis") {
		t.Errorf("guidance must name the resolved type, got %s", out)
	}
}

// TestHandleBatch_CanonicalizeChecksCanonicalExecutesRaw table-drives the invariant
// handleExec's ordering comment states for steps 8/9 (tool_handlers_unified.go): the
// permission check must see the canonicalized command; the executor must see the raw one.
// batch_exec dispatches through the same permission.CanonicalizeFor/ExecutorFor pair
// (tool_handler_batch.go's checkCommand/executeBatchItem), so it is exactly as easy to get
// backwards there — feed the executor the canonicalized (lossy) string, or feed the policy
// check the raw (unmatchable) one — and there was no batch-level test locking it before this
// one.
//
// Kafka and etcd are the only two batch-eligible types registered with a CanonicalizeFunc
// (execimpl/register.go: canonicalizeK8sCommand is the third, but k8s isn't used here because
// its canonicalization needs a configured kubeconfig to reach CheckForAsset, which would
// couple this test to k8s config plumbing it doesn't need). Every other type's checkCommand
// equals its raw command, so only kafka/etcd can distinguish "fed the executor the check
// string" from "fed it the raw string" at all — a test built on any other type would pass
// vacuously even if that swap happened.
func TestHandleBatch_CanonicalizeChecksCanonicalExecutesRaw(t *testing.T) {
	cases := []struct {
		name         string
		assetType    string
		assetID      int64
		assetName    string
		rawCommand   string
		wantCheckCmd string
	}{
		{
			// Mirrors TestHandleExec_KafkaChecksTwoTokenPolicyString's fixture (same
			// package, tool_handlers_unified_kafka_test.go): "topic create" is deliberately
			// far from its two-token policy string (spacing, target, two flags) so a
			// canonicalization bug can't coincidentally produce matching text on both sides.
			name:         "kafka",
			assetType:    asset_entity.AssetTypeKafka,
			assetID:      401,
			assetName:    "kafka-1",
			rawCommand:   "topic   create   orders --partitions=3 --replication-factor=2",
			wantCheckCmd: "topic.create orders",
		},
		{
			// etcd_svc.ParseCommand lowercases the op; FormatCommand renders it back
			// lowercase. Upper-casing "PUT" in the raw command is what makes the raw and
			// canonical strings byte-different without changing what the command does.
			name:         "etcd",
			assetType:    asset_entity.AssetTypeEtcd,
			assetID:      402,
			assetName:    "etcd-1",
			rawCommand:   "PUT mykey myvalue",
			wantCheckCmd: "put mykey myvalue",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := setupUnified(t)

			var gotExecCommand string
			replaceExecutorForTest(t, tc.assetType, func(_ context.Context, _ *asset_entity.Asset, command, _ string) (string, error) {
				gotExecCommand = command
				return "ok", nil
			})

			asset := &asset_entity.Asset{ID: tc.assetID, Name: tc.assetName, Type: tc.assetType}
			m.EXPECT().FindByName(gomock.Any(), tc.assetName).Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
			m.EXPECT().Find(gomock.Any(), tc.assetID).Return(asset, nil).AnyTimes()

			var gotCheckCommand string
			confirm := func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
				if len(items) > 0 {
					gotCheckCommand = items[0].Command
				}
				// "allow", not "deny": the raw-vs-canonical assertion on the executor side
				// needs the item to actually reach executeBatchItem. A "deny" response would
				// still exercise the check-string capture above but never call the executor,
				// leaving gotExecCommand permanently empty and the second assertion vacuous.
				return permission.ApprovalResponse{Decision: "allow"}
			}
			checker := permission.NewCommandPolicyChecker(confirm)
			ctx := permission.WithPolicyChecker(context.Background(), checker)

			cmdJSON, err := json.Marshal([]batchCommandItem{{Asset: tc.assetName, Command: tc.rawCommand}})
			if err != nil {
				t.Fatalf("marshal commands: %v", err)
			}

			if _, err := handleBatchCommand(ctx, map[string]any{"commands": string(cmdJSON)}); err != nil {
				t.Fatalf("handleBatchCommand: %v", err)
			}

			if gotCheckCommand != tc.wantCheckCmd {
				t.Errorf("permission check saw %q, want the canonicalized form %q", gotCheckCommand, tc.wantCheckCmd)
			}
			if gotExecCommand != tc.rawCommand {
				t.Errorf("executor saw %q, want the raw command %q — the canonicalized form must never reach exec", gotExecCommand, tc.rawCommand)
			}
		})
	}
}
