package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
)

// 导入时忽略外键：SQL Server 的外键检查不是会话开关，而是按表停用约束——停用的是导入的目标表
// 的约束，导入后 WITH CHECK 重新启用（同时校验已有数据）。
func TestExecuteBatchDisablesForeignKeysOnTheImportTargetTable(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectExec("ALTER TABLE [dbo].[users] NOCHECK CONSTRAINT ALL").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO [dbo].[users] ([id]) VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("ALTER TABLE [dbo].[users] WITH CHECK CHECK CONSTRAINT ALL").WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements:              []string{"INSERT INTO [dbo].[users] ([id]) VALUES (1);"},
		ContinueOnError:         true,
		DisableForeignKeyChecks: true,
		Database:                "appdb",
		Table:                   "dbo.users",
	})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}
