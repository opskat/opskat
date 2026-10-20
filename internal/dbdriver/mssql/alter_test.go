package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// mssqlColumn 是改表时读列定义的一行；nil 的 maxLen / precision / scale / timePrecision / def 是 NULL。
type mssqlColumn struct {
	name, dataType                  string
	maxLen                          any
	precision, scale, timePrecision any
	nullable                        string
	def                             any
}

func expectAlterColumns(mock sqlmock.Sqlmock, schema, table string, columns ...mssqlColumn) {
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "CHARACTER_MAXIMUM_LENGTH",
		"NUMERIC_PRECISION", "NUMERIC_SCALE", "DATETIME_PRECISION", "IS_NULLABLE", "COLUMN_DEFAULT"})
	for _, c := range columns {
		rows.AddRow(c.name, c.dataType, c.maxLen, c.precision, c.scale, c.timePrecision, c.nullable, c.def)
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
// AlterTableDialog.test.ts 的断言）。
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
			"EXEC sp_rename 'users', 'users_v2'",
			"ALTER TABLE [users_v2] ADD [email] VARCHAR(255) NOT NULL",
			"ALTER TABLE [users_v2] ALTER COLUMN [id] BIGINT NOT NULL",
			"ALTER TABLE [users_v2] DROP COLUMN [name]",
		},
		Table: "users_v2",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ALTER COLUMN 必须写出完整的类型：只改可空性时写回的类型是这一列现在的类型，精度、小数位与时间精度
// 都要带上——写成裸的 decimal 会把 decimal(10,2) 改成 decimal(18,0)、舍掉已有数据的小数。长度只属于
// 字符与二进制类型（xml、text 的 CHARACTER_MAXIMUM_LENGTH 不是类型的一部分）。
func TestAlterTableKeepsTheFullSQLServerTypeWhenOnlyNullabilityChanges(t *testing.T) {
	session, mock := newMock(t)
	columns := []mssqlColumn{
		{name: "amount", dataType: "decimal", precision: int64(10), scale: int64(2), nullable: "YES"},
		{name: "rate", dataType: "numeric", precision: int64(5), scale: int64(0), nullable: "YES"},
		{name: "at", dataType: "datetime2", timePrecision: int64(3), nullable: "YES"},
		{name: "doc", dataType: "xml", maxLen: int64(-1), nullable: "YES"},
		{name: "notes", dataType: "ntext", maxLen: int64(1073741823), nullable: "YES"},
		{name: "count", dataType: "int", precision: int64(10), scale: int64(0), nullable: "YES"},
	}
	expectAlterColumns(mock, "dbo", "users", columns...)
	model, err := session.AlterTableModel(context.Background(), "appdb", "users")
	require.NoError(t, err)
	types := make([]string, len(model.Columns))
	for i, column := range model.Columns {
		types[i] = column.Type
	}
	assert.Equal(t, []string{"decimal(10,2)", "numeric(5,0)", "datetime2(3)", "xml", "ntext", "int"}, types)

	expectAlterColumns(mock, "dbo", "users", columns...)
	req := dbdriver.AlterTableRequest{Name: "users"}
	for _, column := range model.Columns {
		req.Columns = append(req.Columns, dbdriver.AlterColumn{OriginalName: column.Name, Name: column.Name, Type: column.Type})
	}
	result, err := session.AlterTableStatements(context.Background(), "appdb", "users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE [users] ALTER COLUMN [amount] decimal(10,2) NOT NULL",
		"ALTER TABLE [users] ALTER COLUMN [rate] numeric(5,0) NOT NULL",
		"ALTER TABLE [users] ALTER COLUMN [at] datetime2(3) NOT NULL",
		"ALTER TABLE [users] ALTER COLUMN [doc] xml NOT NULL",
		"ALTER TABLE [users] ALTER COLUMN [notes] ntext NOT NULL",
		"ALTER TABLE [users] ALTER COLUMN [count] int NOT NULL",
	}, result.Statements)
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
			{Name: "tag", Type: "VARBINARY(4)", Nullable: true, DefaultValue: "ab"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			"ALTER TABLE [users] ADD [nick] NVARCHAR(20) DEFAULT '匿名'",
			"ALTER TABLE [users] ADD [tag] VARBINARY(4) DEFAULT 'ab'",
			"EXEC sp_rename 'users.full_name', 'display_name', 'COLUMN'",
			"ALTER TABLE [users] ALTER COLUMN [display_name] NVARCHAR(200) NOT NULL",
		},
		Table: "users",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改名只给不带 schema 的新名字：sp_rename 的新名是 'users_v2'（改动前是 'dbo.users_v2'，表会被真的
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
			"EXEC sp_rename 'dbo.users', 'users_v2'",
			"EXEC sp_rename 'dbo.users_v2.c', 'd', 'COLUMN'",
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
