package sqldb

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 方言没有提供目录查询时，浏览操作明确报「不支持」，且不碰数据库（mock 没有预期任何语句）。
func TestBrowsingWithoutACatalogIsUnsupported(t *testing.T) {
	session, mock := newMockSession(t)
	ctx := context.Background()

	_, err := session.ListDatabases(ctx)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.ListTables(ctx, "db")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.TableMetadata(ctx, "db", "t")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.ListObjects(ctx, "db")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.ObjectSource(ctx, "db", "view", "v", "")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

// IndexBuilder 把键列与 INCLUDE 列分开：键列按加入的次序，INCLUDE 列单列，unique 取第一次出现的值。
func TestIndexBuilderAddsIncludeColumnsApartFromTheKeys(t *testing.T) {
	var builder IndexBuilder
	builder.Add("ix", false, "id")
	builder.AddInclude("ix", false, "name")
	builder.Add("ix", true, "at")
	builder.AddInclude("ix", true, "note")
	builder.Add("ux", true, "id")

	assert.Equal(t, []dbdriver.IndexMeta{
		{Name: "ix", Columns: []string{"id", "at"}, Unique: false, Include: []string{"name", "note"}},
		{Name: "ux", Columns: []string{"id"}, Unique: true},
	}, builder.Indexes())
}

// 外键查询多给两列时，最后两列是 ON UPDATE / ON DELETE 的动作原文；四列的查询照旧。
func TestQueryForeignKeysReadsTheActionsWhenGiven(t *testing.T) {
	session, mock := newMockSession(t)
	ctx := context.Background()
	mock.ExpectQuery("FK4").WillReturnRows(sqlmock.NewRows([]string{"c", "col", "rt", "rc"}).AddRow("fk", "user_id", "users", "id"))
	mock.ExpectQuery("FK6").WillReturnRows(sqlmock.NewRows([]string{"c", "col", "rt", "rc", "ru", "rd"}).
		AddRow("fk", "user_id", "users", "id", "CASCADE", "SET NULL"))
	conn, err := session.db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	keys, err := QueryForeignKeys(ctx, conn, "FK4")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{{Name: "fk", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id"}}, keys)

	keys, err = QueryForeignKeys(ctx, conn, "FK6")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{{
		Name: "fk", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id",
		OnUpdate: "CASCADE", OnDelete: "SET NULL",
	}}, keys)
	require.NoError(t, mock.ExpectationsWereMet())
}
