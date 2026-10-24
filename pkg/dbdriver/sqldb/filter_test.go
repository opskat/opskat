package sqldb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb/sqldbtest"
)

// emptyNullDialect 是空串就是 NULL 的测试方言（如 Oracle）。
type emptyNullDialect struct{ tablesDialect }

func (emptyNullDialect) EmptyStringIsNull() bool { return true }

// 共用的筛选语义在测试方言上成立：没实现 EmptyStringNull 的方言「为空」与空串比较，实现了的写成
// IS NULL / IS NOT NULL，其余运算符两者相同。
func TestFilterSemanticsWithAndWithoutEmptyStringNull(t *testing.T) {
	for name, d := range map[string]struct {
		dialect           Dialect
		emptyStringIsNull bool
	}{
		"empty string is a value": {tablesDialect{}, false},
		"empty string is NULL":    {emptyNullDialect{}, true},
	} {
		t.Run(name, func(t *testing.T) {
			sqldbtest.RunFilterSemantics(t, sqldbtest.Dialect{
				NewSession:        func(db *sql.DB) dbdriver.Session { return NewSession(db, d.dialect) },
				Quote:             angle,
				Database:          "db",
				Table:             "t",
				TableRef:          "<db>.<t>",
				EmptyStringIsNull: d.emptyStringIsNull,
			})
		})
	}
}

// 翻页与整表读取同样按方言写「不为空」：空串就是 NULL 的方言里「不等于空串」永远不成立。
func TestTableRowsAndStreamHonourEmptyStringNull(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	session := NewSession(db, emptyNullDialect{})
	query := dbdriver.TableQuery{Filters: []dbdriver.FilterItem{
		{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "memo", Operator: dbdriver.OpIsNotEmpty},
	}}

	mock.ExpectQuery("SELECT * FROM <db>.<t> WHERE <memo> IS NOT NULL LIMIT 10 OFFSET 0").
		WillReturnRows(sqlmock.NewRows([]string{"memo"}).AddRow("x"))
	_, err = session.TableRows(context.Background(), "db", "t", query, dbdriver.Page{Size: 10})
	require.NoError(t, err)

	mock.ExpectQuery("SELECT * FROM <db>.<t> WHERE <memo> IS NOT NULL").
		WillReturnRows(sqlmock.NewRows([]string{"memo"}).AddRow("x"))
	stream, err := session.StreamTableRows(context.Background(), "db", "t", query)
	require.NoError(t, err)
	assert.True(t, stream.Next())
	require.NoError(t, stream.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}
