package mysql

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 导入时忽略外键：MySQL 的 FOREIGN_KEY_CHECKS 是会话级的，在批的那条连接上、事务之外关闭，
// 回滚之后恢复。
func TestExecuteBatchTogglesForeignKeyChecksAroundTheTransaction(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SELECT @@SESSION.foreign_key_checks").WillReturnRows(sqlmock.NewRows([]string{"fk"}).AddRow(1))
	mock.ExpectExec("SET FOREIGN_KEY_CHECKS = 0").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `appdb`.`users`;").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO `appdb`.`users` (`id`) VALUES (1);").WillReturnError(errors.New("duplicate"))
	mock.ExpectRollback()
	mock.ExpectExec("SET FOREIGN_KEY_CHECKS = 1").WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements:              []string{"DELETE FROM `appdb`.`users`;", "INSERT INTO `appdb`.`users` (`id`) VALUES (1);"},
		Atomic:                  true,
		DisableForeignKeyChecks: true,
		Database:                "appdb",
		Table:                   "users",
	})
	require.NoError(t, err)
	assert.True(t, result.RolledBack)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 会话本来就没在检查外键（FOREIGN_KEY_CHECKS = 0）：不关闭，结束后也不把它打开。
func TestExecuteBatchLeavesForeignKeyChecksOffWhenTheSessionHadThemOff(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SELECT @@SESSION.foreign_key_checks").WillReturnRows(sqlmock.NewRows([]string{"fk"}).AddRow(0))
	mock.ExpectExec("INSERT INTO `appdb`.`users` (`id`) VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements:              []string{"INSERT INTO `appdb`.`users` (`id`) VALUES (1);"},
		ContinueOnError:         true,
		DisableForeignKeyChecks: true,
		Database:                "appdb",
		Table:                   "users",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
