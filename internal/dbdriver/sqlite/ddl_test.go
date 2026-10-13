package sqlite

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lookupSQL = "SELECT sql FROM sqlite_master WHERE type IN ('table', 'view') AND name = ? LIMIT 1"

// 建表语句就是 sqlite_master.sql 里存的原文。
func TestTableDDL(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(lookupSQL).WithArgs("users").
		WillReturnRows(sqlmock.NewRows([]string{"sql"}).AddRow("CREATE TABLE users (id INTEGER PRIMARY KEY)"))

	ddl, err := session.TableDDL(context.Background(), "", "users")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE users (id INTEGER PRIMARY KEY)", ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 库名是 schema 名，加引号放在 sqlite_master 前。
func TestTableDDLInAttachedSchema(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(`SELECT sql FROM "aux".sqlite_master WHERE type IN ('table', 'view') AND name = ? LIMIT 1`).WithArgs("t").
		WillReturnRows(sqlmock.NewRows([]string{"sql"}).AddRow("CREATE TABLE t (a)"))

	ddl, err := session.TableDDL(context.Background(), "aux", "t")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE t (a)", ddl)
}

// 没有这张表，或它的 sql 为 NULL（内部表）：给空串，由界面显示「没有建表语句」。
func TestTableDDLWithoutText(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(lookupSQL).WithArgs("gone").WillReturnRows(sqlmock.NewRows([]string{"sql"}))
	mock.ExpectQuery(lookupSQL).WithArgs("internal").WillReturnRows(sqlmock.NewRows([]string{"sql"}).AddRow(nil))

	for _, name := range []string{"gone", "internal"} {
		ddl, err := session.TableDDL(context.Background(), "", name)
		require.NoError(t, err)
		assert.Empty(t, ddl)
	}
}

func TestStarterQuery(t *testing.T) {
	session, _ := newMock(t)
	got, err := session.StarterQuery(context.Background(), "main", "users", 100)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "main"."users" LIMIT 100`, got)
}
