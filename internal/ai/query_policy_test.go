package ai

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	. "github.com/smartystreets/goconvey/convey"
)

func TestClassifyStatements(t *testing.T) {
	Convey("ClassifyStatements", t, func() {
		Convey("SELECT 1", func() {
			stmts, err := ClassifyStatements("SELECT 1")
			So(err, ShouldBeNil)
			So(stmts, ShouldHaveLength, 1)
			So(stmts[0].Type, ShouldEqual, "SELECT")
		})

		Convey("SELECT * FROM users", func() {
			stmts, err := ClassifyStatements("SELECT * FROM users")
			So(err, ShouldBeNil)
			So(stmts, ShouldHaveLength, 1)
			So(stmts[0].Type, ShouldEqual, "SELECT")
		})

		Convey("INSERT", func() {
			stmts, err := ClassifyStatements("INSERT INTO users (name) VALUES ('test')")
			So(err, ShouldBeNil)
			So(stmts[0].Type, ShouldEqual, "INSERT")
		})

		Convey("DELETE without WHERE is dangerous", func() {
			stmts, err := ClassifyStatements("DELETE FROM users")
			So(err, ShouldBeNil)
			So(stmts[0].Type, ShouldEqual, "DELETE")
			So(stmts[0].Dangerous, ShouldBeTrue)
			So(stmts[0].Reason, ShouldEqual, "no_where_delete")
		})

		Convey("DROP TABLE", func() {
			stmts, err := ClassifyStatements("DROP TABLE users")
			So(err, ShouldBeNil)
			So(stmts[0].Type, ShouldEqual, "DROP TABLE")
		})

		Convey("SHOW TABLES", func() {
			stmts, err := ClassifyStatements("SHOW TABLES")
			So(err, ShouldBeNil)
			So(stmts[0].Type, ShouldEqual, "SHOW")
		})

		Convey("multiple statements", func() {
			stmts, err := ClassifyStatements("SELECT 1; SHOW TABLES")
			So(err, ShouldBeNil)
			So(stmts, ShouldHaveLength, 2)
			So(stmts[0].Type, ShouldEqual, "SELECT")
			So(stmts[1].Type, ShouldEqual, "SHOW")
		})
	})
}

func TestCheckQueryPolicy(t *testing.T) {
	ctx := context.Background()

	Convey("CheckQueryPolicy", t, func() {
		Convey("SELECT allowed by allow_types", func() {
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT", "SHOW"},
			}
			stmts, _ := ClassifyStatements("SELECT 1")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, Allow)
		})

		Convey("SELECT * FROM users allowed", func() {
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT", "SHOW"},
			}
			stmts, _ := ClassifyStatements("SELECT * FROM users LIMIT 1")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, Allow)
		})

		Convey("INSERT not in allow_types → NeedConfirm", func() {
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT", "SHOW"},
			}
			stmts, _ := ClassifyStatements("INSERT INTO users (name) VALUES ('test')")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, NeedConfirm)
		})

		Convey("DROP TABLE in deny_types → Deny", func() {
			p := &asset_entity.QueryPolicy{
				DenyTypes: []string{"DROP TABLE"},
			}
			stmts, _ := ClassifyStatements("DROP TABLE users")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, Deny)
		})

		Convey("nil policy — no AllowTypes check, all allowed", func() {
			// DefaultQueryPolicy 只有 Groups 引用，mergeQueryPolicy 不解析 Groups
			// 所以 nil policy 时 AllowTypes/DenyTypes 都为空 → Allow
			stmts, _ := ClassifyStatements("SELECT 1")
			result := CheckQueryPolicy(ctx, nil, stmts)
			So(result.Decision, ShouldEqual, Allow)
		})

		Convey("nil policy — DROP TABLE also allowed (defaults not resolved)", func() {
			// 这是一个已知限制：DefaultQueryPolicy 的 Groups 引用在 mergeQueryPolicy 中不被解析
			// 实际场景中 collectQueryPolicies 会解析 Groups
			stmts, _ := ClassifyStatements("DROP TABLE users")
			result := CheckQueryPolicy(ctx, nil, stmts)
			So(result.Decision, ShouldEqual, Allow)
		})

		Convey("explicit allow_types with SELECT matches SELECT 1", func() {
			// 这是关键场景：AllowTypes 包含 "SELECT"，SQL 是 "SELECT 1"
			// ClassifyStatements 将 "SELECT 1" 分类为 Type="SELECT"
			// containsStrFold 比较 "SELECT" == "SELECT" → 匹配
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT", "SHOW", "DESCRIBE", "EXPLAIN", "USE"},
			}
			stmts, _ := ClassifyStatements("SELECT 1")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, Allow)
		})
	})
}
