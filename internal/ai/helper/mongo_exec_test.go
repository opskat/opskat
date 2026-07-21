package helper

import (
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func mongoAsset(t *testing.T, defaultDB string) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{ID: 1, Name: "mongo-1", Type: asset_entity.AssetTypeMongoDB}
	if err := a.SetMongoDBConfig(&asset_entity.MongoDBConfig{
		Host: "127.0.0.1", Port: 27017, Database: defaultDB,
	}); err != nil {
		t.Fatalf("SetMongoDBConfig: %v", err)
	}
	return a
}

// TestCanonicalizeMongoCommand_YieldsFullCommand 锁住 canonicalizer 的输出形式：
// 完整命令串，不是裸 operation token。
//
// 权限检查、审批弹窗与审计看到的都是这个串。裸 op 曾是候选方案（策略侧的
// policyValueMatches 只认得 op），但那样审批弹窗只显示 "deleteMany"，看不到集合与
// 过滤条件——而不带 --query 的 deleteMany 会清空整个集合，用户无法从弹窗区分这两者。
// 现在策略侧改用 op 感知的 policy.MatchMongoRule，裸 op 规则照样命中富命令串。
func TestCanonicalizeMongoCommand_YieldsFullCommand(t *testing.T) {
	// database-requiring cases pass --db explicitly (asset has no default): this test is
	// about output *shape* (positional collection, flag order, quoting), not about
	// database resolution — that is covered separately by
	// TestCanonicalizeMongoCommand_InjectsAssetDefaultDatabase and
	// TestCanonicalizeMongoCommand_NoDefaultDatabaseRejectsDatabaseRequiringOp.
	asset := mongoAsset(t, "")
	cases := []struct{ in, want string }{
		{`find users --db=test --query='{"filter":{"a":1}}'`, `find users --db=test --query='{"filter":{"a":1}}'`},
		{`deleteMany logs --db=test --query='{"filter":{"level":"debug"}}'`, `deleteMany logs --db=test --query='{"filter":{"level":"debug"}}'`},
		{`listDatabases`, `listDatabases`},
		{`aggregate events --db=analytics --query='{"pipeline":[]}'`, `aggregate events --db=analytics --query='{"pipeline":[]}'`},
		// 规范化：多余空白塌缩，flag 按名字排序（--db 先于 --query），
		// 带空格的集合名保持单个 token。
		{`find    users   --query='{"filter":{}}'    --db=prod`, `find users --db=prod --query='{"filter":{}}'`},
		{`countDocuments 'my coll' --db=test`, `countDocuments 'my coll' --db=test`},
	}
	for _, c := range cases {
		got, err := CanonicalizeMongoCommand(asset, c.in)
		if err != nil {
			t.Fatalf("CanonicalizeMongoCommand(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("CanonicalizeMongoCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCanonicalizeMongoCommand_InjectsAssetDefaultDatabase 锁住资产默认库注入
// （与 k8s 注入 --context/--namespace 同一手法）：命令没写 --db 时补上资产配置里的
// 默认库，写了就不覆盖。执行路径 ExecMongoOnAsset 必须做同样的注入，否则"批准的命令"
// 与"实际执行的命令"会在库名上分叉。
func TestCanonicalizeMongoCommand_InjectsAssetDefaultDatabase(t *testing.T) {
	asset := mongoAsset(t, "appdb")
	cases := []struct{ in, want string }{
		{`find users`, `find users --db=appdb`},
		{`find users --db=analytics`, `find users --db=analytics`},
		{`listCollections`, `listCollections --db=appdb`},
	}
	for _, c := range cases {
		got, err := CanonicalizeMongoCommand(asset, c.in)
		if err != nil {
			t.Fatalf("CanonicalizeMongoCommand(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("CanonicalizeMongoCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCanonicalizeMongoCommand_NoDefaultDatabaseLeavesDatabaseFreeOpUnchanged：资产没配
// 默认库时不注入空 --db（`--db=` 会被解析成空库名，比不写更糟）——对不需要 database
// 的操作（listDatabases）命令保持原样。
func TestCanonicalizeMongoCommand_NoDefaultDatabaseLeavesDatabaseFreeOpUnchanged(t *testing.T) {
	got, err := CanonicalizeMongoCommand(mongoAsset(t, ""), "listDatabases")
	if err != nil {
		t.Fatalf("CanonicalizeMongoCommand unexpected error: %v", err)
	}
	if got != "listDatabases" {
		t.Fatalf("CanonicalizeMongoCommand = %q, want %q", got, "listDatabases")
	}
}

// TestCanonicalizeMongoCommand_NoDefaultDatabaseRejectsDatabaseRequiringOp is the
// regression lock for the finding that a database-requiring operation with no resolvable
// database (no asset default, no --db in the command) used to canonicalize successfully
// and only fail later at execution time — after the user had already been shown an
// approval dialog for a command that was always going to fail. resolveMongoCommand must
// reject this before CanonicalizeMongoCommand returns, so handleExec's canonicalize step
// (which runs before the permission check, see the ordering comment on handleExec in
// tool_handlers_unified.go) fails fast instead of interrupting the user. The
// ordering-level lock that proves the approval dialog itself never fires is
// TestHandleExec_MongoMissingDatabaseNeverReachesPermissionCheck in internal/ai/tool.
func TestCanonicalizeMongoCommand_NoDefaultDatabaseRejectsDatabaseRequiringOp(t *testing.T) {
	_, err := CanonicalizeMongoCommand(mongoAsset(t, ""), "find users")
	if err == nil {
		t.Fatal("CanonicalizeMongoCommand with no default database and no --db = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Fatalf("got %q, want it to name the missing database", err.Error())
	}
}

func TestCanonicalizeMongoCommand_RejectsBadSyntax(t *testing.T) {
	asset := mongoAsset(t, "appdb")
	cases := []string{
		"dropEverything users", // 未知操作
		"find",                 // 缺 collection
		"find users --nope=1",  // 未知 flag
		"find 'users",          // 引号未闭合
	}
	for _, in := range cases {
		if _, err := CanonicalizeMongoCommand(asset, in); err == nil {
			t.Fatalf("CanonicalizeMongoCommand(%q) = nil error, want rejection", in)
		}
	}
}

// TestCanonicalizeMongoCommand_RejectsNonMongoAsset：读取资产配置失败必须报错而不是
// 静默跳过注入——canonicalize 排在权限检查之前，这里失败等于在弹审批对话框之前失败。
func TestCanonicalizeMongoCommand_RejectsNonMongoAsset(t *testing.T) {
	_, err := CanonicalizeMongoCommand(&asset_entity.Asset{ID: 2, Type: asset_entity.AssetTypeRedis}, "find users")
	if err == nil {
		t.Fatal("CanonicalizeMongoCommand on a non-mongo asset = nil error, want the config error surfaced")
	}
	if !strings.Contains(err.Error(), "mongodb config") {
		t.Fatalf("got %q, want it to name the failing step (mongodb config)", err.Error())
	}
}
