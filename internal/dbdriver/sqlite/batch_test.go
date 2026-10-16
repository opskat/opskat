package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// 导入时忽略外键：PRAGMA foreign_keys 在事务里不起作用，所以在批的那条连接上、事务之外切换。
func TestExecuteBatchTogglesForeignKeysOutsideTheTransaction(t *testing.T) {
	session, mock := newMock(t)
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

// 在真实的库文件上：整批里一条失败，前面已经写进去的行随回滚一起撤销。
func TestExecuteBatchRollsBackOnARealDatabase(t *testing.T) {
	session := openLocalSession(t, &asset_entity.DatabaseConfig{
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
