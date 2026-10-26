package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 导入时忽略外键：连接在检查外键时，PRAGMA foreign_keys 在事务里不起作用，所以在批的那条连接上、
// 事务之外关闭，结束后打开。
func TestExecuteBatchTogglesForeignKeysOutsideTheTransaction(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("PRAGMA foreign_keys").WillReturnRows(sqlmock.NewRows([]string{"foreign_keys"}).AddRow(1))
	mock.ExpectExec("PRAGMA foreign_keys = OFF").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "users" ("id") VALUES (1);`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("PRAGMA foreign_keys = ON").WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements:              []string{`INSERT INTO "users" ("id") VALUES (1);`},
		Atomic:                  true,
		DisableForeignKeyChecks: true,
		Table:                   "users",
	})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 在真实的库文件上：SQLite 默认不检查外键。忽略外键的导入结束后，这条连接回到导入前的样子——
// 不能被「恢复」成检查外键，否则连接池里只有这一条连接开始拒绝、级联外键。
func TestExecuteBatchLeavesForeignKeysAsTheyWereOnARealDatabase(t *testing.T) {
	session := openLocalSession(t, &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "fk.db"),
	})
	ctx := context.Background()
	foreignKeys := func() any {
		result, err := session.Execute(ctx, "PRAGMA foreign_keys")
		require.NoError(t, err)
		return result.Rows.Rows[0][0]
	}
	_, err := session.Execute(ctx, "CREATE TABLE parent (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	_, err = session.Execute(ctx, "CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id))")
	require.NoError(t, err)
	require.Equal(t, int64(0), foreignKeys())

	_, err = session.ExecuteBatch(ctx, dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO child (id, parent_id) VALUES (1, 99);"},
		Atomic:     true, DisableForeignKeyChecks: true, Database: "main", Table: "child",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), foreignKeys())
	_, err = session.Execute(ctx, "INSERT INTO child (id, parent_id) VALUES (2, 98)")
	require.NoError(t, err)
}

// 在真实的库文件上：连接串打开了外键检查时，忽略外键的导入照样写进孤儿行，结束后检查恢复。
func TestExecuteBatchDisablesAndRestoresEnabledForeignKeysOnARealDatabase(t *testing.T) {
	session := openLocalSession(t, &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "fk-on.db"), Params: "_pragma=foreign_keys(1)",
	})
	ctx := context.Background()
	_, err := session.Execute(ctx, "CREATE TABLE parent (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	_, err = session.Execute(ctx, "CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id))")
	require.NoError(t, err)

	result, err := session.ExecuteBatch(ctx, dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO child (id, parent_id) VALUES (1, 99);"},
		Atomic:     true, DisableForeignKeyChecks: true, Database: "main", Table: "child",
	})
	require.NoError(t, err)
	require.False(t, result.Failed())

	_, err = session.Execute(ctx, "INSERT INTO child (id, parent_id) VALUES (2, 98)")
	require.ErrorContains(t, err, "FOREIGN KEY")
}

// 在真实的库文件上：整批里一条失败，前面已经写进去的行随回滚一起撤销。
func TestExecuteBatchRollsBackOnARealDatabase(t *testing.T) {
	session := openLocalSession(t, &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "batch.db"),
	})
	ctx := context.Background()
	_, err := session.Execute(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)

	result, err := session.ExecuteBatch(ctx, dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO t (id) VALUES (1);", "INSERT INTO t (id) VALUES (1);"},
		Atomic:     true, DisableForeignKeyChecks: true, Database: "main", Table: "t",
	})
	require.NoError(t, err)
	assert.True(t, result.RolledBack)
	require.Len(t, result.Executed, 2)
	assert.NotEmpty(t, result.Executed[1].Error)

	count, err := session.Execute(ctx, "SELECT COUNT(*) FROM t")
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(0)}}, count.Rows.Rows)
}
