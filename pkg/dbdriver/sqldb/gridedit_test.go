package sqldb

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// editDialect 在行语句的测试方言上加表格编辑：没有物理行标识，只删一行以 ONE ROW 收尾。
type editDialect struct{ rowsDialect }

func (editDialect) InsertRowSQL(tableRef, columns, values string, _ bool) string {
	return "INSERT INTO " + tableRef + " (" + columns + ") VALUES (" + values + ");"
}

func (editDialect) DeleteRowSQL(tableRef, where string) string {
	return "DELETE FROM " + tableRef + " WHERE " + where + " ONE ROW;"
}

func (editDialect) ExactRow(string, string) (string, bool) { return "", false }

// lobDialect 的 clob 列不能用 = 与原值比较（如 Oracle 的 LOB），定位时换成 LOB_EQ。
type lobDialect struct{ editDialect }

func (lobDialect) EqualitySQL(columnType, column, literal string) string {
	if columnType == "clob" {
		return "LOB_EQ(" + column + ", " + literal + ")"
	}
	return column + " = " + literal
}

func newLocateSession(t *testing.T, dialect Dialect) (*Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, dialect), mock
}

func expectLOBColumns(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("COLUMNS").WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type"}).
		AddRow("id", "int").AddRow("doc", "clob").AddRow("memo", "text"))
}

// 没有主键时按原值定位一行：实现 EqualityComparer 的方言按列类型决定每列怎么与原值比较，原值是
// NULL 的列仍是 IS NULL；没实现的方言一律 col = 字面量。表格编辑与「复制为 UPDATE」都这样定位。
func TestLocatingByOriginalValuesUsesTheDialectsEquality(t *testing.T) {
	ctx := context.Background()
	row := map[string]any{"id": float64(1), "doc": "长文", "memo": nil}
	grid := dbdriver.GridEditRequest{
		Columns: []string{"id", "doc", "memo"},
		Updates: []dbdriver.GridUpdate{{Row: row, Set: []dbdriver.ColumnValue{{Column: "memo", Value: "x"}}}},
		Deletes: []map[string]any{row},
	}
	copyAsUpdate := dbdriver.RowStatementsRequest{Kind: dbdriver.RowUpdate, Columns: []string{"id", "doc", "memo"}, Rows: []map[string]any{row}}

	for name, tc := range map[string]struct {
		dialect Dialect
		where   string
	}{
		"dialect equality": {lobDialect{}, "<id> = '1' AND LOB_EQ(<doc>, '长文') AND <memo> IS NULL"},
		"plain equality":   {editDialect{}, "<id> = '1' AND <doc> = '长文' AND <memo> IS NULL"},
	} {
		t.Run(name, func(t *testing.T) {
			session, mock := newLocateSession(t, tc.dialect)
			expectLOBColumns(mock)
			statements, err := session.GridEditStatements(ctx, "db", "t", grid)
			require.NoError(t, err)
			assert.Equal(t, []string{"UPDATE <db>.<t> SET <memo> = 'x' WHERE " + tc.where + " ONE ROW;"}, statements.Updates)
			assert.Equal(t, []string{"DELETE FROM <db>.<t> WHERE " + tc.where + " ONE ROW;"}, statements.Deletes)

			expectLOBColumns(mock)
			updates, err := dbdrivertest.RowStatements(ctx, session, "db", "t", copyAsUpdate)
			require.NoError(t, err)
			assert.Equal(t, []string{"UPDATE <db>.<t> SET <id> = '1', <doc> = '长文', <memo> = NULL WHERE " + tc.where + " ONE ROW;"}, updates)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
