package sqldb

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// plainDumpDialect 能转储，但什么钩子都没实现：得到的全是默认写法。
type plainDumpDialect struct{ tablesDialect }

func (plainDumpDialect) DumpSyntax() DumpSyntax { return DumpSyntax{} }

// hookedDumpDialect 用上了全部钩子：文件开头结尾、批分隔符、类型，以及对象的每一处。
type hookedDumpDialect struct {
	tablesDialect
	conns *[]*sql.Conn
}

func (hookedDumpDialect) DumpSyntax() DumpSyntax {
	return DumpSyntax{Opening: []string{"SET FK = 0;"}, Closing: []string{"SET FK = 1;"}, BatchTerminator: "GO"}
}

func (d hookedDumpDialect) DumpFile(_ context.Context, conn *sql.Conn, scope dbdriver.DumpScope, script *dbdriver.DumpFileScript) error {
	*d.conns = append(*d.conns, conn)
	for _, table := range scope.Tables {
		script.Types = append(script.Types, "CREATE TYPE "+table+"_kind;")
	}
	return nil
}

func (d hookedDumpDialect) DumpObject(_ context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	*d.conns = append(*d.conns, conn)
	script.Definition = append([]string{"DELIMITER ;;"}, script.Definition...)
	script.DataColumns = []string{"id"}
	script.DataPrologue = []string{"IDENTITY ON " + object.Name + ";"}
	return nil
}

// 方言没有实现 DumpDialect：以 ErrUnsupported 拒绝，不拿连接、不写任何内容。
func TestDumpIsRefusedWhenTheDialectCannotDump(t *testing.T) {
	session, mock := newTablesSession(t)
	var out bytes.Buffer

	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{Database: "db", Tables: []string{"t"}, Writer: &out})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, out.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// 什么钩子都没实现的方言：文件没有开头结尾与批分隔符；对象的存在则删除按种类写、用方言的表引用，
// 定义补上结束的分号（已经有的不重复），INSERT 写读出的全部列。
func TestDumpDefaultsWhenTheDialectHasNoHooks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dialect := newDumpDialect(NewSession(db, plainDumpDialect{}), plainDumpDialect{}, plainDumpDialect{})
	ctx := context.Background()

	file, err := dialect.DumpFile(ctx, dbdriver.DumpScope{Database: "db", Tables: []string{"t"}})
	require.NoError(t, err)
	assert.Equal(t, dbdriver.DumpFileScript{}, *file)

	table, err := dialect.DumpObject(ctx, dbdriver.DumpObject{Database: "db", Kind: dbdriver.DumpTable, Name: "t", Definition: "CREATE TABLE t (id int)\n"})
	require.NoError(t, err)
	assert.Equal(t, dbdriver.DumpObjectScript{
		Drop:       []string{"DROP TABLE IF EXISTS <db>.<t>;"},
		Definition: []string{"CREATE TABLE t (id int);"},
	}, *table)

	view, err := dialect.DumpObject(ctx, dbdriver.DumpObject{Database: "db", Kind: dbdriver.ObjectView, Name: "v", Definition: "CREATE VIEW v AS SELECT 1;"})
	require.NoError(t, err)
	assert.Equal(t, []string{"DROP VIEW IF EXISTS <db>.<v>;"}, view.Drop)
	assert.Equal(t, []string{"CREATE VIEW v AS SELECT 1;"}, view.Definition)

	for kind, keyword := range map[string]string{dbdriver.ObjectFunction: "FUNCTION", dbdriver.ObjectProcedure: "PROCEDURE", dbdriver.ObjectTrigger: "TRIGGER"} {
		object, err := dialect.DumpObject(ctx, dbdriver.DumpObject{Database: "db", Kind: kind, Name: "o", Definition: "CREATE " + keyword + " o"})
		require.NoError(t, err, kind)
		assert.Equal(t, dbdriver.DumpObjectScript{
			Drop:       []string{"DROP " + keyword + " IF EXISTS <db>.<o>;"},
			Definition: []string{"CREATE " + keyword + " o;"},
		}, *object, kind)
	}

	_, err = dialect.DumpObject(ctx, dbdriver.DumpObject{Database: "db", Kind: "sequence", Name: "s", Definition: "CREATE SEQUENCE s"})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 方言的钩子在一条连接上拿到默认写法再改：文件一次、每个对象一次。
func TestDumpHooksAdjustTheDefaultsOnAConnection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var conns []*sql.Conn
	hooked := hookedDumpDialect{conns: &conns}
	dialect := newDumpDialect(NewSession(db, hooked), hooked, hooked)
	ctx := context.Background()

	file, err := dialect.DumpFile(ctx, dbdriver.DumpScope{Database: "db", Tables: []string{"t"}})
	require.NoError(t, err)
	assert.Equal(t, dbdriver.DumpFileScript{
		Opening: []string{"SET FK = 0;"}, Types: []string{"CREATE TYPE t_kind;"}, Closing: []string{"SET FK = 1;"}, BatchTerminator: "GO",
	}, *file)

	table, err := dialect.DumpObject(ctx, dbdriver.DumpObject{Database: "db", Kind: dbdriver.DumpTable, Name: "t", Definition: "CREATE TABLE t (id int)", WithData: true})
	require.NoError(t, err)
	assert.Equal(t, dbdriver.DumpObjectScript{
		Drop:         []string{"DROP TABLE IF EXISTS <db>.<t>;"},
		Definition:   []string{"DELIMITER ;;", "CREATE TABLE t (id int);"},
		DataColumns:  []string{"id"},
		DataPrologue: []string{"IDENTITY ON t;"},
	}, *table)
	require.Len(t, conns, 2)
	assert.NotNil(t, conns[0])
	require.NoError(t, mock.ExpectationsWereMet())
}
