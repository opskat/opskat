package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const createUsers = "CREATE TABLE `users` (\n  `id` int NOT NULL,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB"

// 建表语句就是 SHOW CREATE TABLE 返回的那一段文本；表名只加引号，不带库名（连接已限定库）。
func TestTableDDL(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SHOW CREATE TABLE `users`").
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow("users", createUsers))

	ddl, err := session.TableDDL(context.Background(), "appdb", "users")
	require.NoError(t, err)
	assert.Equal(t, createUsers, ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 视图的 SHOW CREATE 里没有以 CREATE TABLE / CREATE VIEW 开头的列，取第二列（定义）。
func TestTableDDLOfAView(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SHOW CREATE TABLE `v_users`").
		WillReturnRows(sqlmock.NewRows([]string{"View", "Create View"}).AddRow("v_users", "CREATE ALGORITHM=UNDEFINED VIEW `v_users` AS select 1"))

	ddl, err := session.TableDDL(context.Background(), "appdb", "v_users")
	require.NoError(t, err)
	assert.Equal(t, "CREATE ALGORITHM=UNDEFINED VIEW `v_users` AS select 1", ddl)
}

func TestTableDDLOfAMissingTableIsEmpty(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SHOW CREATE TABLE `gone`").WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}))

	ddl, err := session.TableDDL(context.Background(), "appdb", "gone")
	require.NoError(t, err)
	assert.Empty(t, ddl)
}

func TestTableDDLReturnsTheDatabaseError(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SHOW CREATE TABLE `gone`").WillReturnError(assert.AnError)

	_, err := session.TableDDL(context.Background(), "appdb", "gone")
	require.ErrorIs(t, err, assert.AnError)
}

func TestStarterQuery(t *testing.T) {
	session, _ := newMock(t)
	got, err := session.StarterQuery(context.Background(), "appdb", "users", 100)
	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM `appdb`.`users` LIMIT 100", got)
}
