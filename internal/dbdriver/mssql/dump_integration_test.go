//go:build integration

package mssql

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// dumpSourceBatches 建出往返用的样例库，每项一批：dbo 与 sales 两个 schema；IDENTITY(100, 5) 删过一行留下空号；
// decimal(18, 4)、nvarchar(max)（含单独一行的 GO 与引号）、varbinary(max)（不是 UTF-8 的字节）、uniqueidentifier、
// 2^53 以上的 bigint、持久化与不持久化的计算列、rowversion、具名与系统起名的默认值、主键 / 唯一 / CHECK / 外键（含
// 自引用与 ON DELETE CASCADE）、过滤索引；dbo 的视图依赖 sales 的视图（列出的次序在它前面）；函数、过程、DML
// 触发器，以及一个禁用的库级 DDL 触发器。
var dumpSourceBatches = []string{
	"CREATE SCHEMA [sales]",
	`CREATE TABLE [sales].[customers] (
  [id] int IDENTITY(100, 5) NOT NULL CONSTRAINT [PK_customers] PRIMARY KEY,
  [code] uniqueidentifier NOT NULL CONSTRAINT [DF_customers_code] DEFAULT NEWID(),
  [name] nvarchar(max) NULL,
  [parent_id] int NULL CONSTRAINT [FK_customers_parent] REFERENCES [sales].[customers] ([id]),
  [visits] int NOT NULL DEFAULT 0,
  CONSTRAINT [UQ_customers_code] UNIQUE ([code])
)`,
	`CREATE TABLE [dbo].[orders] (
  [id] bigint NOT NULL CONSTRAINT [PK_orders] PRIMARY KEY,
  [customer_id] int NOT NULL CONSTRAINT [FK_orders_customer] REFERENCES [sales].[customers] ([id]) ON DELETE CASCADE,
  [price] decimal(18, 4) NOT NULL CONSTRAINT [DF_orders_price] DEFAULT 0,
  [qty] int NOT NULL DEFAULT 1,
  [total] AS ([price] * [qty]) PERSISTED,
  [label] AS (N'#' + CAST([id] AS nvarchar(30))),
  [payload] varbinary(max) NULL,
  [rv] rowversion,
  CONSTRAINT [CK_orders_qty] CHECK ([qty] > 0)
)`,
	"CREATE NONCLUSTERED INDEX [IX_orders_priced] ON [dbo].[orders] ([customer_id]) INCLUDE ([price]) WHERE [price] > 0",
	"CREATE VIEW [sales].[v_customer_names] AS SELECT [id], [name] FROM [sales].[customers]",
	`CREATE VIEW [dbo].[v_named_orders] AS
SELECT o.[id], n.[name], o.[total] FROM [dbo].[orders] o JOIN [sales].[v_customer_names] n ON n.[id] = o.[customer_id]`,
	`CREATE FUNCTION [dbo].[f_net](@price decimal(18, 4)) RETURNS decimal(18, 4)
AS
BEGIN
  RETURN @price * 0.9;
END`,
	`CREATE PROCEDURE [sales].[p_rename] @id int, @name nvarchar(100) AS
UPDATE [sales].[customers] SET [name] = @name WHERE [id] = @id;`,
	`CREATE TRIGGER [sales].[tr_customers_visit] ON [sales].[customers] AFTER UPDATE AS
SET NOCOUNT ON;
IF NOT UPDATE([visits])
  UPDATE c SET [visits] = c.[visits] + 1 FROM [sales].[customers] c JOIN inserted i ON i.[id] = c.[id];`,
	"CREATE TRIGGER [tr_ddl_guard] ON DATABASE FOR CREATE_TABLE AS PRINT 'ddl'",
	"DISABLE TRIGGER [tr_ddl_guard] ON DATABASE",
	"INSERT INTO [sales].[customers] ([name]) VALUES (N'root')",
	"INSERT INTO [sales].[customers] ([name], [parent_id]) VALUES (N'multi' + NCHAR(10) + N'GO' + NCHAR(10) + N'line O''Reilly 中文', 100)",
	"INSERT INTO [sales].[customers] ([name]) VALUES (N'gone')",
	"DELETE FROM [sales].[customers] WHERE [id] = 110",
	"UPDATE [sales].[customers] SET [name] = [name] WHERE [id] = 100",
	`INSERT INTO [dbo].[orders] ([id], [customer_id], [price], [qty], [payload]) VALUES
  (9007199254740993, 100, 12.3456, 2, 0xFF00FE80C3), (1, 105, 0, 3, NULL), (2, 105, 99999999999999.9999, 1, 0x)`,
}

// 往返：把样例库整库转储（结构和数据），在另一个名字的空库（预先建好 sales schema）上运行这个文件，两边的列、
// 约束、索引、外键、IDENTITY 的当前值、视图 / 函数 / 过程 / 触发器的原文与状态、每张表的行数与数据校验和都一样，
// 两边再插一行得到的 IDENTITY 值也一样。
func TestIntegrationDumpRestoresIntoAnotherDatabase(t *testing.T) {
	master := openDDLDatabase(t, "master")
	suffix := time.Now().UnixNano()
	source, target := fmt.Sprintf("opskat_dump_src_%d", suffix), fmt.Sprintf("opskat_dump_dst_%d", suffix)
	createScratchDatabase(t, master, source)
	createScratchDatabase(t, master, target)
	sourceDB, targetDB := openDDLDatabase(t, source), openDDLDatabase(t, target)
	for _, batch := range dumpSourceBatches {
		_, err := sourceDB.Exec(batch)
		require.NoError(t, err, batch)
	}
	_, err := targetDB.Exec("CREATE SCHEMA [sales]")
	require.NoError(t, err)

	ctx := context.Background()
	var file bytes.Buffer
	dumped, err := newSession(sourceDB).Dump(ctx, dbdriver.DumpRequest{
		Database: source, Whole: true, WithData: true, Writer: &file,
		Header: dbdriver.DumpHeader{Version: "test", Asset: "mssql", Time: time.Now()},
	})
	require.NoError(t, err)
	assert.Equal(t, 8, dumped.Objects)
	assert.EqualValues(t, 5, dumped.Rows)

	result, err := newSession(targetDB).RunScript(ctx, dbdriver.ScriptRequest{
		Database: target, Script: bytes.NewReader(file.Bytes()), Size: int64(file.Len()),
	})
	require.NoError(t, err)
	require.Nil(t, result.Failure, "the dump must run on an empty database: %+v\n%s", result.Failure, file.String())

	want := databaseSnapshot(t, sourceDB)
	assert.Equal(t, want, databaseSnapshot(t, targetDB))
	assert.Regexp(t, `dbo\.orders 3 rows .*sales\.customers 2 rows `, strings.Join(want.rows, " "), "the sample has the rows it was given")

	for _, db := range []*sql.DB{sourceDB, targetDB} {
		var id int
		require.NoError(t, db.QueryRow("INSERT INTO [sales].[customers] ([name]) OUTPUT inserted.[id] VALUES (N'next')").Scan(&id))
		assert.Equal(t, 115, id, "the next IDENTITY value continues after the gap")
	}
}

// snapshot 是一个库里要比对的全部内容，每项按行排好的文字。
type snapshot struct {
	columns, constraints, indexes, foreignKeys, identities, modules, triggers, rows []string
}

func databaseSnapshot(t *testing.T, db *sql.DB) snapshot {
	t.Helper()
	s := snapshot{
		columns: queryLines(t, db, "SELECT s.name + '.' + o.name, c.name, ty.name, c.max_length, c.precision, c.scale, c.is_nullable, "+
			"c.is_computed, ISNULL(cc.definition, ''), ISNULL(cc.is_persisted, 0), ISNULL(dc.definition, ''), "+
			"ISNULL(CASE WHEN dc.is_system_named = 0 THEN dc.name END, '') "+
			"FROM sys.columns c JOIN sys.objects o ON o.object_id = c.object_id JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"JOIN sys.types ty ON ty.user_type_id = c.user_type_id "+
			"LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id "+
			"LEFT JOIN sys.default_constraints dc ON dc.object_id = c.default_object_id "+
			"WHERE o.type IN ('U', 'V') AND o.is_ms_shipped = 0"),
		constraints: queryLines(t, db, "SELECT OBJECT_SCHEMA_NAME(parent_object_id) + '.' + OBJECT_NAME(parent_object_id), name, definition, is_disabled "+
			"FROM sys.check_constraints"),
		indexes: queryLines(t, db, "SELECT OBJECT_SCHEMA_NAME(i.object_id) + '.' + OBJECT_NAME(i.object_id), i.name, i.type_desc, i.is_unique, "+
			"i.is_primary_key, i.is_unique_constraint, ISNULL(i.filter_definition, ''), col.name, ixc.key_ordinal, ixc.is_included_column, ixc.is_descending_key "+
			"FROM sys.indexes i JOIN sys.index_columns ixc ON ixc.object_id = i.object_id AND ixc.index_id = i.index_id "+
			"JOIN sys.columns col ON col.object_id = ixc.object_id AND col.column_id = ixc.column_id "+
			"WHERE OBJECTPROPERTY(i.object_id, 'IsUserTable') = 1"),
		foreignKeys: queryLines(t, db, "SELECT OBJECT_SCHEMA_NAME(fk.parent_object_id) + '.' + OBJECT_NAME(fk.parent_object_id), fk.name, pc.name, "+
			"OBJECT_SCHEMA_NAME(fk.referenced_object_id) + '.' + OBJECT_NAME(fk.referenced_object_id), rc.name, "+
			"fk.delete_referential_action_desc, fk.update_referential_action_desc, fk.is_disabled, fk.is_not_trusted "+
			"FROM sys.foreign_keys fk JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id "+
			"JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id "+
			"JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id"),
		identities: queryLines(t, db, "SELECT OBJECT_SCHEMA_NAME(object_id) + '.' + OBJECT_NAME(object_id), name, "+
			"CAST(seed_value AS nvarchar(40)), CAST(increment_value AS nvarchar(40)), ISNULL(CAST(last_value AS nvarchar(40)), 'NULL'), "+
			"CAST(IDENT_CURRENT(OBJECT_SCHEMA_NAME(object_id) + '.' + OBJECT_NAME(object_id)) AS nvarchar(40)) "+
			"FROM sys.identity_columns WHERE OBJECTPROPERTY(object_id, 'IsUserTable') = 1"),
		modules: queryLines(t, db, "SELECT ISNULL(s.name + '.', '') + COALESCE(o.name, tr.name), COALESCE(o.type, tr.type), m.definition "+
			"FROM sys.sql_modules m LEFT JOIN sys.objects o ON o.object_id = m.object_id LEFT JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"LEFT JOIN sys.triggers tr ON tr.object_id = m.object_id"),
		triggers: queryLines(t, db, "SELECT tr.name, tr.parent_class, ISNULL(OBJECT_SCHEMA_NAME(tr.parent_id) + '.' + OBJECT_NAME(tr.parent_id), ''), "+
			"tr.is_disabled, tr.is_instead_of_trigger FROM sys.triggers tr"),
	}
	for _, table := range queryLines(t, db, "SELECT s.name + '.' + t.name FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id") {
		s.rows = append(s.rows, tableRows(t, db, table))
	}
	require.Len(t, s.modules, 6, "two views, a function, a procedure, the DML trigger and the DDL trigger: %v", s.modules)
	return s
}

// tableRows 是一张表的行数与数据校验和（全部列按主键排序，rowversion 除外：它由目标库重新生成）。
func tableRows(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	columns := queryLines(t, db, "SELECT QUOTENAME(name) FROM sys.columns WHERE object_id = OBJECT_ID(@p1) AND system_type_id <> 189",
		sql.Named("p1", table))
	rows, err := db.Query("SELECT " + strings.Join(columns, ", ") + " FROM " + objectRef(table) + " ORDER BY 1") //nolint:gosec // names from the scratch database's catalog, quoted
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		require.NoError(t, rows.Scan(pointers...))
		for _, value := range values {
			_, err := fmt.Fprintf(hash, "%T:%v|", value, value)
			require.NoError(t, err)
		}
		count++
	}
	require.NoError(t, rows.Err())
	return fmt.Sprintf("%s %d rows %s", table, count, hex.EncodeToString(hash.Sum(nil)))
}

// queryLines 执行一条查询，每行的各列用「 | 」连成一行文字，按文字排序。
func queryLines(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(query, args...)
	require.NoError(t, err, query)
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	require.NoError(t, err)
	var lines []string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		require.NoError(t, rows.Scan(pointers...))
		parts := make([]string, len(values))
		for i, value := range values {
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			parts[i] = fmt.Sprint(value)
		}
		lines = append(lines, strings.Join(parts, " | "))
	}
	require.NoError(t, rows.Err())
	slices.Sort(lines)
	return lines
}
