package sqldb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestBuildCreateTable(t *testing.T) {
	columns := []DDLColumn{
		{Name: "id", Type: "integer", Nullable: false, Default: "nextval('s')"},
		{Name: "note", Type: "text", Nullable: true},
		{Name: "n", Type: "int", Nullable: false},
	}
	assert.Equal(t, "CREATE TABLE <t> (\n  <id> integer NOT NULL DEFAULT nextval('s'),\n  <note> text,\n  <n> int NOT NULL,\n  PRIMARY KEY (<id>, <n>)\n);",
		BuildCreateTable("<t>", columns, []string{"id", "n"}, angle))
	assert.Equal(t, "CREATE TABLE <t> (\n  <note> text\n);",
		BuildCreateTable("<t>", columns[1:2], nil, angle))
	assert.Empty(t, BuildCreateTable("<t>", nil, []string{"id"}, angle), "a table without columns does not exist")
}

// 方言没有提供建表语句时，查看建表语句明确报「不支持」，且不碰数据库（mock 没有预期任何语句）。
func TestTableDDLWithoutDDLIsUnsupported(t *testing.T) {
	session, mock := newTablesSession(t)

	_, err := session.TableDDL(context.Background(), "db", "t")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 起始查询语句用方言的表引用，语法随方言；不提供表浏览的方言明确报「不支持」。
func TestStarterQuery(t *testing.T) {
	session, _ := newTablesSession(t)
	got, err := session.StarterQuery(context.Background(), "db", "t", 100)
	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM <db>.<t> LIMIT 100", got)

	plain, _ := newMockSession(t)
	_, err = plain.StarterQuery(context.Background(), "db", "t", 100)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
}
