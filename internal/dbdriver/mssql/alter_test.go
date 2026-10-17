package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
)

// mssqlColumn 是改表时读列定义的一行；nil 的 maxLen / def 是 NULL。
type mssqlColumn struct {
	name, dataType string
	maxLen         any
	nullable       string
	def            any
}

func expectAlterColumns(mock sqlmock.Sqlmock, schema, table string, columns ...mssqlColumn) {
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "CHARACTER_MAXIMUM_LENGTH", "IS_NULLABLE", "COLUMN_DEFAULT"})
	for _, c := range columns {
		rows.AddRow(c.name, c.dataType, c.maxLen, c.nullable, c.def)
	}
	mock.ExpectQuery(alterColumnsSQL).WithArgs(schema, table).WillReturnRows(rows)
}

// 打开改表对话框：列从 INFORMATION_SCHEMA 按表所在的 schema 读（裸名按 dbo），类型带上长度
// （-1 写成 max），与改动前对话框显示的一致；注释走扩展属性、不可在此编辑，不读。改名输入框里是
// 不带 schema 的表名。
func TestAlterTableModelForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	expectAlterColumns(mock, "dbo", "users",
		mssqlColumn{name: "id", dataType: "int", nullable: "NO"},
		mssqlColumn{name: "name", dataType: "nvarchar", maxLen: int64(50), nullable: "YES", def: "(N'anon')"},
		mssqlColumn{name: "bio", dataType: "varchar", maxLen: int64(-1), nullable: "YES"},
	)

	model, err := session.AlterTableModel(context.Background(), "appdb", "dbo.users")
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableModel{
		Name: "users",
		Columns: []dbdriver.ColumnDefinition{
			{Name: "id", Type: "int"},
			{Name: "name", Type: "nvarchar(50)", Nullable: true, DefaultValue: "(N'anon')"},
			{Name: "bio", Type: "varchar(max)", Nullable: true},
		},
	}, model)
	require.NoError(t, mock.ExpectationsWereMet())
}

// sp_rename / ADD / ALTER COLUMN / DROP COLUMN 与方括号：与改动前前端生成的文本一致（原
// AlterTableDialog.test.ts 的断言），sp_rename 的名字带 N 前缀。
func TestAlterTableStatementsForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	expectAlterColumns(mock, "dbo", "users",
		mssqlColumn{name: "id", dataType: "INT", nullable: "NO"},
		mssqlColumn{name: "name", dataType: "VARCHAR", maxLen: int64(100), nullable: "YES"},
	)

	result, err := session.AlterTableStatements(context.Background(), "appdb", "users", dbdriver.AlterTableRequest{
		Name: "users_v2",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "id", Name: "id", Type: "BIGINT", Nullable: false},
			{Name: "email", Type: "VARCHAR(255)", Nullable: false},
		},
		Drops: []string{"name"},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			"EXEC sp_rename N'users', N'users_v2'",
			"ALTER TABLE [users_v2] ADD [email] VARCHAR(255) NOT NULL",
			"ALTER TABLE [users_v2] ALTER COLUMN [id] BIGINT NOT NULL",
			"ALTER TABLE [users_v2] DROP COLUMN [name]",
		},
		Table: "users_v2",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAlterTableRenamesSQLServerColumnsThenAltersThemByTheNewName(t *testing.T) {
	session, mock := newMock(t)
	expectAlterColumns(mock, "dbo", "users", mssqlColumn{name: "full_name", dataType: "nvarchar", maxLen: int64(200), nullable: "YES"})

	result, err := session.AlterTableStatements(context.Background(), "appdb", "users", dbdriver.AlterTableRequest{
		Name: "users",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "full_name", Name: "display_name", Type: "NVARCHAR(200)", Nullable: false},
			{Name: "nick", Type: "NVARCHAR(20)", Nullable: true, DefaultValue: "匿名"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			"ALTER TABLE [users] ADD [nick] NVARCHAR(20) DEFAULT N'匿名'",
			"EXEC sp_rename N'users.full_name', N'display_name', 'COLUMN'",
			"ALTER TABLE [users] ALTER COLUMN [display_name] NVARCHAR(200) NOT NULL",
		},
		Table: "users",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改名只给不带 schema 的新名字：sp_rename 的新名是 N'users_v2'（改动前是 'dbo.users_v2'，表会被真的
// 命名为 dbo.users_v2），表仍在 dbo，之后给列改名用的是改名后的表。
func TestAlterTableRenameGivesSpRenameTheBareNewName(t *testing.T) {
	session, mock := newMock(t)
	expectAlterColumns(mock, "dbo", "users", mssqlColumn{name: "c", dataType: "int", nullable: "YES"})

	result, err := session.AlterTableStatements(context.Background(), "appdb", "dbo.users", dbdriver.AlterTableRequest{
		Name:    "users_v2",
		Columns: []dbdriver.AlterColumn{{OriginalName: "c", Name: "d", Type: "int", Nullable: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			"EXEC sp_rename N'dbo.users', N'users_v2'",
			"EXEC sp_rename N'dbo.users_v2.c', N'd', 'COLUMN'",
		},
		Table: "dbo.users_v2",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 已有列的默认值与列注释、表注释不可在此修改（驱动没有声明）：要改它们的请求生成失败，什么也不执行。
func TestAlterTableRefusesAttributesSQLServerDoesNotDeclare(t *testing.T) {
	ctx := context.Background()
	for name, req := range map[string]dbdriver.AlterTableRequest{
		"column default": {Columns: []dbdriver.AlterColumn{{OriginalName: "c", Name: "c", Type: "int", Nullable: true, DefaultValue: "1"}}},
		"column comment": {Columns: []dbdriver.AlterColumn{{OriginalName: "c", Name: "c", Type: "int", Nullable: true, Comment: "x"}}},
		"new column comment": {Columns: []dbdriver.AlterColumn{
			{OriginalName: "c", Name: "c", Type: "int", Nullable: true}, {Name: "d", Type: "int", Comment: "x"},
		}},
		"table comment": {Comment: "x", Columns: []dbdriver.AlterColumn{{OriginalName: "c", Name: "c", Type: "int", Nullable: true}}},
	} {
		t.Run(name, func(t *testing.T) {
			session, mock := newMock(t)
			expectAlterColumns(mock, "dbo", "users", mssqlColumn{name: "c", dataType: "int", nullable: "YES"})
			_, err := session.AlterTableStatements(ctx, "appdb", "dbo.users", req)
			assert.Error(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
