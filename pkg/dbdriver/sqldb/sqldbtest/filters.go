// Package sqldbtest 是建在 sqldb 之上的驱动共用的测试：筛选条件的语义在各方言里相同（只有空串
// 就是 NULL 的方言写「为空 / 不为空」不同），每个驱动都用自己的会话跑一遍，把它实际发给数据库的
// 语句钉住。
package sqldbtest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// Dialect 是被测驱动的方言在语句里的样子，由驱动的测试按字面给出（不是取自实现）。
type Dialect struct {
	// NewSession 把一个 *sql.DB 包成该驱动的会话。
	NewSession func(db *sql.DB) dbdriver.Session
	// Quote 给一个不含引号字符的标识符加上该方言的引号。
	Quote func(name string) string
	// Database / Table 是传给会话的库名与表名，TableRef 是它们在 FROM 里应有的写法。
	Database, Table, TableRef string
	// EmptyStringIsNull 为真表示该方言里空串就是 NULL（Oracle）：「为空」应是 IS NULL、「不为空」
	// 应是 IS NOT NULL，不与 '' 比较。
	EmptyStringIsNull bool
}

func condition(column string, op dbdriver.FilterOperator, value any) dbdriver.FilterItem {
	return dbdriver.FilterItem{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: column, Operator: op, Value: value}
}

func unset(column string, op dbdriver.FilterOperator) dbdriver.FilterItem {
	return dbdriver.FilterItem{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: column, Operator: op, Unset: true}
}

func group(items ...dbdriver.FilterItem) dbdriver.FilterItem {
	return dbdriver.FilterItem{Kind: dbdriver.FilterGroup, Join: dbdriver.JoinAnd, Items: items}
}

func or(item dbdriver.FilterItem) dbdriver.FilterItem {
	item.Join = dbdriver.JoinOr
	return item
}

func disabled(item dbdriver.FilterItem) dbdriver.FilterItem {
	item.Disabled = true
	return item
}

func negated(item dbdriver.FilterItem) dbdriver.FilterItem {
	item.Negated = true
	return item
}

type filterCase struct {
	name    string
	filters []dbdriver.FilterItem
	// where 是 WHERE 后应有的表达式；空串表示语句不带 WHERE。
	where string
}

func filterCases(q func(string) string, emptyStringIsNull bool) []filterCase {
	name, email, age, memo := q("name"), q("email"), q("age"), q("memo")
	isEmpty, isNotEmpty := "("+memo+" IS NULL OR "+memo+" = '')", "("+memo+" IS NOT NULL AND "+memo+" <> '')"
	if emptyStringIsNull {
		isEmpty, isNotEmpty = memo+" IS NULL", memo+" IS NOT NULL"
	}
	one := func(item dbdriver.FilterItem) []dbdriver.FilterItem { return []dbdriver.FilterItem{item} }
	return []filterCase{
		// --- 每个运算符 ---
		{"equal", one(condition("name", dbdriver.OpEqual, "A")), name + " = 'A'"},
		{"not equal", one(condition("name", dbdriver.OpNotEqual, "A")), name + " <> 'A'"},
		{"less", one(condition("age", dbdriver.OpLess, "5")), age + " < '5'"},
		{"less or equal", one(condition("age", dbdriver.OpLessOrEqual, "5")), age + " <= '5'"},
		{"greater", one(condition("age", dbdriver.OpGreater, "5")), age + " > '5'"},
		{"greater or equal", one(condition("age", dbdriver.OpGreaterOrEqual, "5")), age + " >= '5'"},
		{"contains", one(condition("name", dbdriver.OpContains, "bob")), name + " LIKE '%bob%'"},
		{"not contains", one(condition("name", dbdriver.OpNotContains, "bob")), name + " NOT LIKE '%bob%'"},
		{"like is contains", one(condition("name", dbdriver.OpLike, "bob")), name + " LIKE '%bob%'"},
		{"not like is not contains", one(condition("name", dbdriver.OpNotLike, "bob")), name + " NOT LIKE '%bob%'"},
		{"begins with", one(condition("name", dbdriver.OpBeginsWith, "Al")), name + " LIKE 'Al%'"},
		{"not begins with", one(condition("name", dbdriver.OpNotBeginsWith, "Al")), name + " NOT LIKE 'Al%'"},
		{"ends with", one(condition("name", dbdriver.OpEndsWith, "ce")), name + " LIKE '%ce'"},
		{"not ends with", one(condition("name", dbdriver.OpNotEndsWith, "ce")), name + " NOT LIKE '%ce'"},
		{"is null", one(condition("memo", dbdriver.OpIsNull, nil)), memo + " IS NULL"},
		{"is not null", one(condition("memo", dbdriver.OpIsNotNull, nil)), memo + " IS NOT NULL"},
		{"is empty", one(condition("memo", dbdriver.OpIsEmpty, nil)), isEmpty},
		{"is not empty", one(condition("memo", dbdriver.OpIsNotEmpty, nil)), isNotEmpty},
		{"between", one(condition("age", dbdriver.OpBetween, []any{18, 30})), age + " BETWEEN '18' AND '30'"},
		{"not between", one(condition("age", dbdriver.OpNotBetween, []any{18, 30})), age + " NOT BETWEEN '18' AND '30'"},
		{"in list", one(condition("name", dbdriver.OpInList, []any{"active", "pending"})), name + " IN ('active', 'pending')"},
		{"not in list", one(condition("name", dbdriver.OpNotInList, []any{"active", "pending"})), name + " NOT IN ('active', 'pending')"},

		// --- 值怎么写成字面量 ---
		{"a quote in the value is doubled", one(condition("name", dbdriver.OpEqual, "O'Reilly")), name + " = 'O''Reilly'"},
		{"a number is written as a string literal", one(condition("age", dbdriver.OpEqual, float64(42))), age + " = '42'"},
		{"a fraction keeps its digits", one(condition("age", dbdriver.OpGreater, 1.5)), age + " > '1.5'"},
		{"a boolean is written as its word", one(condition("name", dbdriver.OpEqual, true)), name + " = 'true'"},
		{"wildcards in the value are not escaped", one(condition("name", dbdriver.OpContains, "50%_")), name + " LIKE '%50%_%'"},
		{"an empty text still matches by pattern", one(condition("name", dbdriver.OpContains, "")), name + " LIKE '%%'"},
		{"a list given to a scalar operator is its elements joined by commas",
			one(condition("age", dbdriver.OpEqual, []any{"18", "30"})), age + " = '18,30'"},

		// --- NULL 与还没给的值 ---
		{"equal to NULL is IS NULL", one(condition("memo", dbdriver.OpEqual, nil)), memo + " IS NULL"},
		{"not equal to NULL is IS NOT NULL", one(condition("memo", dbdriver.OpNotEqual, nil)), memo + " IS NOT NULL"},
		{"another comparison with NULL is skipped", one(condition("memo", dbdriver.OpGreater, nil)), ""},
		{"a pattern on NULL is skipped", one(condition("memo", dbdriver.OpContains, nil)), ""},
		{"a condition without a value yet is skipped", one(unset("name", dbdriver.OpEqual)), ""},
		{"operators that take no value ignore a missing one", one(unset("memo", dbdriver.OpIsNull)), memo + " IS NULL"},

		// --- 区间与列表的值 ---
		{"a range needs a list", one(condition("age", dbdriver.OpBetween, "18")), ""},
		{"a range needs both ends", one(condition("age", dbdriver.OpBetween, []any{18})), ""},
		{"a range uses its first two elements", one(condition("age", dbdriver.OpBetween, []any{1, 2, 3})), age + " BETWEEN '1' AND '2'"},
		{"a range end may be NULL", one(condition("age", dbdriver.OpBetween, []any{nil, nil})), age + " BETWEEN NULL AND NULL"},
		{"a list from comma separated text",
			one(condition("name", dbdriver.OpInList, " a, b ,,c ")), name + " IN ('a', 'b', 'c')"},
		{"a list from blank text is skipped", one(condition("name", dbdriver.OpInList, " , ")), ""},
		{"an empty list is skipped", one(condition("name", dbdriver.OpInList, []any{})), ""},
		{"a scalar is a list of one", one(condition("age", dbdriver.OpNotInList, float64(5))), age + " NOT IN ('5')"},
		{"a list element may be NULL", one(condition("age", dbdriver.OpInList, []any{1, nil})), age + " IN ('1', NULL)"},

		// --- 连接、停用、分组、取反 ---
		{"items join with AND by default",
			[]dbdriver.FilterItem{condition("name", dbdriver.OpLike, "bob"), condition("email", dbdriver.OpNotLike, "@test.com")},
			name + " LIKE '%bob%' AND " + email + " NOT LIKE '%@test.com%'"},
		{"an item's join links it to the next one",
			[]dbdriver.FilterItem{or(condition("email", dbdriver.OpEqual, "11223")), group(condition("age", dbdriver.OpEqual, 2))},
			email + " = '11223' OR (" + age + " = '2')"},
		{"joins are flat, without added precedence",
			[]dbdriver.FilterItem{
				or(condition("name", dbdriver.OpEqual, "A")), condition("name", dbdriver.OpEqual, "B"), condition("age", dbdriver.OpGreater, 1),
			},
			name + " = 'A' OR " + name + " = 'B' AND " + age + " > '1'"},
		{"a disabled item is skipped together with its join",
			[]dbdriver.FilterItem{
				or(condition("name", dbdriver.OpEqual, "A")), disabled(condition("name", dbdriver.OpEqual, "B")), condition("age", dbdriver.OpGreater, 1),
			},
			name + " = 'A' OR " + age + " > '1'"},
		{"a skipped first item leaves no dangling join",
			[]dbdriver.FilterItem{or(unset("name", dbdriver.OpEqual)), condition("age", dbdriver.OpGreater, 1)},
			age + " > '1'"},
		{"everything disabled means no condition",
			[]dbdriver.FilterItem{disabled(condition("name", dbdriver.OpEqual, "A")), disabled(group(condition("age", dbdriver.OpEqual, 1)))},
			""},
		{"no filter means no condition", nil, ""},
		{"an empty group is skipped", []dbdriver.FilterItem{condition("name", dbdriver.OpEqual, "A"), group()}, name + " = 'A'"},
		{"a group with nothing effective is skipped",
			[]dbdriver.FilterItem{group(unset("name", dbdriver.OpEqual), disabled(condition("age", dbdriver.OpEqual, 1)))}, ""},
		{"a negated group, with a disabled item inside, between plain conditions",
			[]dbdriver.FilterItem{
				condition("name", dbdriver.OpEqual, "A"),
				negated(group(
					or(condition("name", dbdriver.OpEqual, "B")),
					condition("age", dbdriver.OpGreater, 100),
					disabled(condition("memo", dbdriver.OpEqual, nil)),
				)),
				condition("memo", dbdriver.OpIsEmpty, nil),
			},
			name + " = 'A' AND NOT (" + name + " = 'B' OR " + age + " > '100') AND " + isEmpty},
		{"groups nest",
			[]dbdriver.FilterItem{group(or(condition("name", dbdriver.OpEqual, "A")), negated(group(condition("age", dbdriver.OpLess, 3))))},
			"(" + name + " = 'A' OR NOT (" + age + " < '3'))"},
	}
}

// RunFilterSemantics 用驱动自己的会话把每一种筛选条件都变成计数语句，核对它发给数据库的
// 正是预期的那一条：每个运算符、值怎么写成字面量、NULL 与缺值、连接、停用、分组与取反。
func RunFilterSemantics(t *testing.T, d Dialect) {
	t.Helper()
	for _, tc := range filterCases(d.Quote, d.EmptyStringIsNull) {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			want := "SELECT COUNT(*) FROM " + d.TableRef
			if tc.where != "" {
				want += " WHERE " + tc.where
			}
			mock.ExpectQuery(want).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

			total, err := d.NewSession(db).CountTableRows(context.Background(), d.Database, d.Table, tc.filters)
			require.NoError(t, err)
			assert.Equal(t, int64(7), total)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
