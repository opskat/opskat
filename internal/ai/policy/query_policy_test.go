package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
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
			So(result.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("SELECT * FROM users allowed", func() {
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT", "SHOW"},
			}
			stmts, _ := ClassifyStatements("SELECT * FROM users LIMIT 1")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("INSERT not in allow_types → aictx.NeedConfirm", func() {
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT", "SHOW"},
			}
			stmts, _ := ClassifyStatements("INSERT INTO users (name) VALUES ('test')")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("explicit policy does not inherit an unselected dangerous deny group", func() {
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT"},
			}
			stmts, _ := ClassifyStatements("CREATE USER 'app'@'%'")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("DROP TABLE in deny_types → aictx.Deny", func() {
			p := &asset_entity.QueryPolicy{
				DenyTypes: []string{"DROP TABLE"},
			}
			stmts, _ := ClassifyStatements("DROP TABLE users")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.Deny)
		})

		Convey("nil policy uses default read-only allow", func() {
			stmts, _ := ClassifyStatements("SELECT 1")
			result := CheckQueryPolicy(ctx, nil, stmts)
			So(result.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("nil policy requires confirmation for non-default write SQL", func() {
			stmts, _ := ClassifyStatements("INSERT INTO users (name) VALUES ('test')")
			result := CheckQueryPolicy(ctx, nil, stmts)
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("nil policy applies default dangerous deny", func() {
			stmts, _ := ClassifyStatements("DROP TABLE users")
			result := CheckQueryPolicy(ctx, nil, stmts)
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})

		Convey("explicit allow_types with SELECT matches SELECT 1", func() {
			// 这是关键场景：AllowTypes 包含 "SELECT"，SQL 是 "SELECT 1"
			// ClassifyStatements 将 "SELECT 1" 分类为 Type="SELECT"
			// 类型规则比较 "SELECT" == "SELECT" → 匹配
			p := &asset_entity.QueryPolicy{
				AllowTypes: []string{"SELECT", "SHOW", "DESCRIBE", "EXPLAIN", "USE"},
			}
			stmts, _ := ClassifyStatements("SELECT 1")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("allow_types wildcard allows any non-dangerous SQL type", func() {
			p := &asset_entity.QueryPolicy{AllowTypes: []string{"*"}}
			stmts, _ := ClassifyStatements("INSERT INTO users (name) VALUES ('test')")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("allow_types wildcard allows operations when dangerous deny is not selected", func() {
			p := &asset_entity.QueryPolicy{AllowTypes: []string{"*"}}
			stmts, _ := ClassifyStatements("DROP TABLE users")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.Allow)
		})

		Convey("deny_types wildcard denies every SQL statement", func() {
			p := &asset_entity.QueryPolicy{DenyTypes: []string{"*"}}
			stmts, _ := ClassifyStatements("SELECT 1")
			result := CheckQueryPolicy(ctx, p, stmts)
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
			So(result.MatchedPattern, ShouldEqual, "*")
		})
	})
}

// 语句按它实际做的事归类：只读的集合查询算 SELECT；写服务器文件的 SELECT ... INTO
// OUTFILE/DUMPFILE 不算 SELECT；EXPLAIN ANALYZE 会真正执行里面的语句，按里面的语句归类。
// 默认策略（只读组 + 危险拒绝组）下的决定是对外的契约。
func TestClassifyStatementsByEffect(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name         string
		sql          string
		wantType     string
		wantReason   string
		wantDecision aictx.Decision
	}{
		{"UNION ALL of grouped counts",
			"SELECT 'hr_gender' dt, gender v, COUNT(*) n FROM `znzt-hrm`.hr_employee_profile WHERE deleted=0 GROUP BY gender UNION ALL SELECT 'hr_ethnicity', ethnicity_code, COUNT(*) FROM `znzt-hrm`.hr_employee_profile WHERE deleted=0 GROUP BY ethnicity_code UNION ALL SELECT 'hr_talent_category', category, COUNT(*) FROM `znzt-hrm`.hr_talent WHERE deleted=0 GROUP BY category",
			"SELECT", "", aictx.Allow},
		{"WITH then UNION", "WITH c AS (SELECT 1 a) SELECT * FROM c UNION ALL SELECT 2", "SELECT", "", aictx.Allow},
		{"braced UNION with ORDER BY and LIMIT", "(SELECT a FROM t) UNION (SELECT b FROM u) ORDER BY 1 LIMIT 5", "SELECT", "", aictx.Allow},
		{"INTERSECT", "SELECT a FROM t INTERSECT SELECT b FROM u", "SELECT", "", aictx.Allow},
		{"EXCEPT", "SELECT a FROM t EXCEPT SELECT b FROM u", "SELECT", "", aictx.Allow},

		{"SELECT INTO OUTFILE", "SELECT * FROM t INTO OUTFILE '/tmp/x'", "SELECT INTO OUTFILE", "", aictx.NeedConfirm},
		{"UNION INTO OUTFILE", "SELECT a FROM t UNION SELECT b FROM u INTO OUTFILE '/tmp/x'", "SELECT INTO OUTFILE", "", aictx.NeedConfirm},

		{"EXPLAIN ANALYZE DELETE", "EXPLAIN ANALYZE DELETE FROM t WHERE id = 1", "DELETE", "", aictx.NeedConfirm},
		{"EXPLAIN ANALYZE DELETE without WHERE", "EXPLAIN ANALYZE DELETE FROM t", "DELETE", "no_where_delete", aictx.Deny},
		{"EXPLAIN ANALYZE multi-table UPDATE", "EXPLAIN ANALYZE UPDATE t, u SET t.a = u.a WHERE t.id = u.id", "UPDATE", "", aictx.NeedConfirm},
		{"EXPLAIN ANALYZE SELECT", "EXPLAIN ANALYZE SELECT * FROM t", "SELECT", "", aictx.Allow},
		{"EXPLAIN ANALYZE SELECT INTO OUTFILE", "EXPLAIN ANALYZE SELECT * FROM t INTO OUTFILE '/tmp/x'", "SELECT INTO OUTFILE", "", aictx.NeedConfirm},
		{"plain EXPLAIN DELETE only plans", "EXPLAIN DELETE FROM t", "EXPLAIN", "", aictx.Allow},
	}

	Convey("ClassifyStatements by effect", t, func() {
		for _, tc := range cases {
			Convey(tc.name, func() {
				stmts, err := ClassifyStatements(tc.sql)
				So(err, ShouldBeNil)
				So(stmts, ShouldHaveLength, 1)
				So(stmts[0].Type, ShouldEqual, tc.wantType)
				So(stmts[0].Raw, ShouldEqual, tc.sql)
				So(stmts[0].Reason, ShouldEqual, tc.wantReason)
				So(CheckQueryPolicy(ctx, nil, stmts).Decision, ShouldEqual, tc.wantDecision)
			})
		}

		// TiDB 语法不认 INTO DUMPFILE，解析失败即拒绝执行。解析器哪天开始接受它，
		// 这里会失败，提醒给它像 INTO OUTFILE 一样的独立类型，而不是落成 SELECT。
		Convey("SELECT INTO DUMPFILE is rejected by the parser", func() {
			_, err := ClassifyStatements("SELECT a FROM t LIMIT 1 INTO DUMPFILE '/tmp/x'")
			So(err, ShouldNotBeNil)
		})
	})
}

func TestAppendUnique_CaseSensitive(t *testing.T) {
	Convey("AppendUnique 必须保留大小写不同的模式（Redis key / Kafka resource 都是大小写敏感的）", t, func() {
		Convey("Redis key 模式大小写不同时不能合并", func() {
			merged := AppendUnique([]string{"GET User:*"}, "GET user:*")
			So(merged, ShouldContain, "GET User:*")
			So(merged, ShouldContain, "GET user:*")
		})

		Convey("Kafka resource 模式大小写不同时不能合并", func() {
			merged := AppendUnique([]string{"topic.read Orders"}, "topic.read orders")
			So(merged, ShouldContain, "topic.read Orders")
			So(merged, ShouldContain, "topic.read orders")
		})

		Convey("完全相同的项仍然去重", func() {
			merged := AppendUnique([]string{"SELECT"}, "SELECT")
			So(merged, ShouldHaveLength, 1)
		})
	})
}
