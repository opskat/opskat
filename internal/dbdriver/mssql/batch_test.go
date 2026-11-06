package mssql

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

const enabledForeignKeysSQL = "SELECT name FROM sys.foreign_keys WHERE parent_object_id = OBJECT_ID(@p1) AND is_disabled = 0 ORDER BY name"

func ignoreForeignKeys(statements ...string) dbdriver.BatchRequest {
	return dbdriver.BatchRequest{
		Statements:              statements,
		DisableForeignKeyChecks: true,
		Database:                "appdb",
		Table:                   "dbo.users",
	}
}

// 导入时忽略外键：SQL Server 的外键检查不是会话开关，而是按表停用约束——只停用导入的目标表上
// 当前启用着的外键（不连带 CHECK 约束、不碰原本就停用的），导入后只把它们重新启用，且不校验已有
// 数据（WITH NOCHECK）：导入了违反外键的行时约束照样恢复为启用，而不是报 547 留在停用状态。
func TestExecuteBatchDisablesOnlyTheEnabledForeignKeysOfTheImportTarget(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(enabledForeignKeysSQL).WithArgs(sql.Named("p1", "[dbo].[users]")).
		WillReturnRows(names("name", "fk_users_org", "fk_users]x"))
	mock.ExpectExec("ALTER TABLE [dbo].[users] NOCHECK CONSTRAINT [fk_users_org], [fk_users]]x]").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO [dbo].[users] ([id]) VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("ALTER TABLE [dbo].[users] WITH NOCHECK CHECK CONSTRAINT [fk_users_org], [fk_users]]x]").WillReturnResult(sqlmock.NewResult(0, 0))

	request := ignoreForeignKeys("INSERT INTO [dbo].[users] ([id]) VALUES (1);")
	request.ContinueOnError = true
	result, err := session.ExecuteBatch(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 语句失败时也恢复那些外键；恢复成功时导入的结果按语句的执行情况报告，不是整批出错。
func TestExecuteBatchRestoresTheForeignKeysAfterAFailedStatement(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(enabledForeignKeysSQL).WithArgs(sql.Named("p1", "[dbo].[users]")).
		WillReturnRows(names("name", "fk_users_org"))
	mock.ExpectExec("ALTER TABLE [dbo].[users] NOCHECK CONSTRAINT [fk_users_org]").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO [dbo].[users] ([id]) VALUES (1);").WillReturnError(errors.New("duplicate key"))
	mock.ExpectExec("ALTER TABLE [dbo].[users] WITH NOCHECK CHECK CONSTRAINT [fk_users_org]").WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := session.ExecuteBatch(context.Background(),
		ignoreForeignKeys("INSERT INTO [dbo].[users] ([id]) VALUES (1);", "INSERT INTO [dbo].[users] ([id]) VALUES (2);"))
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, Error: "duplicate key"}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 目标表上没有启用着的外键：不停用也不「恢复」任何约束。
func TestExecuteBatchLeavesATableWithoutEnabledForeignKeysAlone(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(enabledForeignKeysSQL).WithArgs(sql.Named("p1", "[dbo].[users]")).
		WillReturnRows(names("name"))
	mock.ExpectExec("INSERT INTO [dbo].[users] ([id]) VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := session.ExecuteBatch(context.Background(), ignoreForeignKeys("INSERT INTO [dbo].[users] ([id]) VALUES (1);"))
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}
