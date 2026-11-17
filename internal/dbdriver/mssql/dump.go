package mssql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 转储：每条结构语句与每一块数据之后单独一行 GO，CREATE VIEW / FUNCTION / PROCEDURE / TRIGGER 因此都在
// 一批的开头。存在则删除用 SQL Server 2016 起的 DROP ... IF EXISTS（库级 DDL 触发器带 ON DATABASE）。
// 表的定义是 TableDDL（不含外键；不勾「包含索引」时也不含索引）；外键在全部数据之后用 ALTER TABLE ... ADD
// CONSTRAINT 加上（勾了「包含外键」时），禁用的、不受信任的保持原状；SQL Server 不能在会话里关闭外键检查，删除段
// 先拆掉要转储的表上的外键（表或外键不存在时跳过），被引用的表才删得掉。INSERT 不写计算列与 rowversion；有 IDENTITY 列的表，数据前后开、关
// IDENTITY_INSERT，全部数据之后用 DBCC CHECKIDENT 把当前值设回源库的。视图、函数、过程与触发器写服务器
// 保存的原文（OBJECT_DEFINITION），不补分号：GO 切分去掉批末尾的空白，原文才与源库一样；视图、函数与过程的依赖
// 取自 sys.sql_expression_dependencies（见 dumpDependenciesQuery）；禁用的触发器建出后再禁用。扩展属性、权限与 schema 的创建不写（导入前
// 目标库里要已有这些 schema）。
var (
	_ sqldb.DumpDialect      = dialect{}
	_ sqldb.DumpObjectWriter = dialect{}
)

func (dialect) DumpSyntax() sqldb.DumpSyntax { return sqldb.DumpSyntax{BatchTerminator: "GO"} }

func (dialect) DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	switch object.Kind {
	case dbdriver.DumpTable:
		return dumpTable(ctx, conn, object, script)
	case dbdriver.ObjectView:
		return dumpView(ctx, conn, object, script)
	case dbdriver.ObjectTrigger:
		script.Definition = []string{moduleDefinition(object.Definition)}
		return dumpTrigger(ctx, conn, object, script)
	default: // 函数与过程
		script.Definition = []string{moduleDefinition(object.Definition)}
		return dumpDependencies(ctx, conn, object, script)
	}
}

// objectRef 是 OBJECT_ID 认的 [schema].[name]（裸名按 dbo）。
func objectRef(name string) string {
	schema, object := splitSchemaTable(name)
	return quoteIdent(schema) + "." + quoteIdent(object)
}

// moduleDefinition 是视图、函数、过程或触发器写进文件的原文：去掉两头的空白（GO 切分也会去掉），不补分号。
func moduleDefinition(definition string) string {
	return strings.Trim(definition, " \t\r\n")
}

// dumpDataColumnsQuery 是 INSERT 写入的列：去掉计算列与 rowversion（system_type_id 189），它们不能写入。
const dumpDataColumnsQuery = "SELECT name FROM sys.columns WHERE object_id = OBJECT_ID(@p1) " +
	"AND is_computed = 0 AND system_type_id <> 189 ORDER BY column_id"

// dumpIdentityQuery 给出表的 IDENTITY 列的当前值（没有 IDENTITY 列时没有行，还没用过时为 NULL）。
const dumpIdentityQuery = "SELECT CAST(last_value AS NVARCHAR(40)) FROM sys.identity_columns WHERE object_id = OBJECT_ID(@p1)"

// dumpForeignKeysQuery 是表上的外键，每列一行，按外键名与列的次序。
const dumpForeignKeysQuery = "SELECT fk.name, OBJECT_SCHEMA_NAME(fk.referenced_object_id), OBJECT_NAME(fk.referenced_object_id), " +
	"pc.name, rc.name, fk.delete_referential_action_desc, fk.update_referential_action_desc, " +
	"fk.is_disabled, fk.is_not_trusted, fk.is_not_for_replication " +
	"FROM sys.foreign_keys fk JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id " +
	"JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id " +
	"JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id " +
	"WHERE fk.parent_object_id = OBJECT_ID(@p1) ORDER BY fk.name, fkc.constraint_column_id"

func dumpTable(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	tableRef := sqldb.QuoteSchemaTable(object.Name, quoteIdent)
	id := objectRef(object.Name)
	if !object.Options.Indexes {
		definition, err := tableDDL(ctx, conn, object.Name, false)
		if err != nil {
			return sqldb.Wrap("table definition", err)
		}
		script.Definition = []string{sqldb.TerminateStatement(definition)}
	}
	if err := foreignKeyStatements(ctx, conn, tableRef, id, script); err != nil {
		return sqldb.Wrap("foreign keys", err)
	}
	identity, err := queryIdentity(ctx, conn, id)
	if err != nil {
		return sqldb.Wrap("identity", err)
	}
	if identity.present && identity.current.Valid {
		script.PostData = []string{fmt.Sprintf("DBCC CHECKIDENT (%s, RESEED, %s);", unicodeString(tableRef), identity.current.String)}
	}
	if !object.WithData {
		return nil
	}
	if script.DataColumns, err = sqldb.QueryNames(ctx, conn, dumpDataColumnsQuery, sql.Named("p1", id)); err != nil {
		return sqldb.Wrap("columns", err)
	}
	if identity.present {
		script.DataPrologue = []string{"SET IDENTITY_INSERT " + tableRef + " ON;"}
		script.DataEpilogue = []string{"SET IDENTITY_INSERT " + tableRef + " OFF;"}
	}
	return nil
}

// tableIdentity 是表的 IDENTITY 列：present 表示有，current 是它的当前值（还没用过时无效）。
type tableIdentity struct {
	present bool
	current sql.NullString
}

func queryIdentity(ctx context.Context, conn *sql.Conn, id string) (tableIdentity, error) {
	var identity tableIdentity
	err := conn.QueryRowContext(ctx, dumpIdentityQuery, sql.Named("p1", id)).Scan(&identity.current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return tableIdentity{}, nil
	case err != nil:
		return tableIdentity{}, err
	}
	identity.present = true
	return identity, nil
}

// dumpForeignKey 是一个外键，行按外键名聚合。
type dumpForeignKey struct {
	name, referenced             string
	columns, referencedColumns   []string
	onDelete, onUpdate           string
	disabled, untrusted, notRepl bool
}

// foreignKeyStatements 写出表上的外键：ALTER TABLE ... ADD CONSTRAINT，不受信任的用 WITH NOCHECK（不检查已有
// 的行），禁用的加上之后再 NOCHECK CONSTRAINT；以及删除段里拆掉它们的语句（只在这张表上有这个外键时才拆，
// 表不存在时也不报错）。
func foreignKeyStatements(ctx context.Context, conn *sql.Conn, tableRef, id string, script *dbdriver.DumpObjectScript) error {
	foreignKeys, err := queryForeignKeys(ctx, conn, id)
	if err != nil {
		return err
	}
	var statements []string
	for _, fk := range foreignKeys {
		script.DropForeignKeys = append(script.DropForeignKeys, fmt.Sprintf(
			"IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = %s AND parent_object_id = OBJECT_ID(%s)) ALTER TABLE %s DROP CONSTRAINT %s;",
			unicodeString(fk.name), unicodeString(id), tableRef, quoteIdent(fk.name)))
		check := "WITH CHECK"
		if fk.untrusted {
			check = "WITH NOCHECK"
		}
		statement := fmt.Sprintf("ALTER TABLE %s %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
			tableRef, check, quoteIdent(fk.name), strings.Join(fk.columns, ", "), fk.referenced, strings.Join(fk.referencedColumns, ", "))
		statement += referentialAction("DELETE", fk.onDelete) + referentialAction("UPDATE", fk.onUpdate)
		if fk.notRepl {
			statement += " NOT FOR REPLICATION"
		}
		statements = append(statements, statement+";")
		if fk.disabled {
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s NOCHECK CONSTRAINT %s;", tableRef, quoteIdent(fk.name)))
		}
	}
	script.ForeignKeys = statements
	return nil
}

// referentialAction 写 ON DELETE / ON UPDATE 的动作（*_referential_action_desc：NO_ACTION、CASCADE、SET_NULL、
// SET_DEFAULT）；NO ACTION 是默认，不写。
func referentialAction(event, action string) string {
	if action == "NO_ACTION" {
		return ""
	}
	return " ON " + event + " " + strings.ReplaceAll(action, "_", " ")
}

func queryForeignKeys(ctx context.Context, conn *sql.Conn, id string) ([]*dumpForeignKey, error) {
	rows, err := conn.QueryContext(ctx, dumpForeignKeysQuery, sql.Named("p1", id))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var foreignKeys []*dumpForeignKey
	for rows.Next() {
		var (
			name, refSchema, refTable, column, refColumn string
			onDelete, onUpdate                           string
			disabled, untrusted, notRepl                 bool
		)
		if err := rows.Scan(&name, &refSchema, &refTable, &column, &refColumn, &onDelete, &onUpdate,
			&disabled, &untrusted, &notRepl); err != nil {
			return nil, err
		}
		if len(foreignKeys) == 0 || foreignKeys[len(foreignKeys)-1].name != name {
			foreignKeys = append(foreignKeys, &dumpForeignKey{
				name: name, referenced: quoteIdent(refSchema) + "." + quoteIdent(refTable),
				onDelete: onDelete, onUpdate: onUpdate, disabled: disabled, untrusted: untrusted, notRepl: notRepl,
			})
		}
		fk := foreignKeys[len(foreignKeys)-1]
		fk.columns = append(fk.columns, quoteIdent(column))
		fk.referencedColumns = append(fk.referencedColumns, quoteIdent(refColumn))
	}
	return foreignKeys, rows.Err()
}

// unicodeString 写一个 N'…' 字符串字面量。
func unicodeString(text string) string {
	return "N'" + strings.ReplaceAll(text, "'", "''") + "'"
}

// dumpViewDefinitionQuery 是视图保存的原文（会话给出的定义是 TableDDL 按列拼的建表语句，不能用）。
const dumpViewDefinitionQuery = "SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))"

// dumpDependenciesQuery 是视图、函数或过程用到的视图、函数与过程：种类与名字（ListTables / ListObjects 的
// schema.name）。sys.sql_expression_dependencies 记着模块里写到的对象：调用函数的视图、引用视图的函数（含
// SCHEMABINDING 的）都在其中；种类与 ListObjects 列出它们的一样（函数是 FN、IF、TF，过程是 sys.procedures 的）。
const dumpDependenciesQuery = "SELECT DISTINCT CASE WHEN o.type = 'V' THEN '" + dbdriver.ObjectView + "' " +
	"WHEN o.type IN ('FN', 'IF', 'TF') THEN '" + dbdriver.ObjectFunction + "' ELSE '" + dbdriver.ObjectProcedure + "' END, " +
	"s.name + '.' + o.name FROM sys.sql_expression_dependencies d " +
	"JOIN sys.objects o ON o.object_id = d.referenced_id JOIN sys.schemas s ON s.schema_id = o.schema_id " +
	"WHERE d.referencing_id = OBJECT_ID(@p1) AND (o.type IN ('V', 'FN', 'IF', 'TF') OR o.object_id IN (SELECT object_id FROM sys.procedures))"

// dumpDependencies 给出视图、函数或过程用到的视图、函数与过程（DumpObjectScript.Dependencies）。
func dumpDependencies(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	used, err := sqldb.QueryDumpDependencies(ctx, conn, dumpDependenciesQuery, sql.Named("p1", objectRef(object.Name)))
	if err != nil {
		return sqldb.Wrap("dependencies", err)
	}
	script.Dependencies = used
	return nil
}

func dumpView(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	id := objectRef(object.Name)
	var definition sql.NullString
	if err := conn.QueryRowContext(ctx, dumpViewDefinitionQuery, sql.Named("p1", id)).Scan(&definition); err != nil {
		return sqldb.Wrap("view definition", err)
	}
	if strings.TrimSpace(definition.String) == "" {
		return fmt.Errorf("the database gave no definition for view %s (it may be created WITH ENCRYPTION)", object.Name)
	}
	script.Definition = []string{moduleDefinition(definition.String)}
	return dumpDependencies(ctx, conn, object, script)
}

// triggerLookup 找出一个触发器：ListObjects 给库级 DDL 触发器的是裸名（@p1，它们不属于 schema，OBJECT_ID 找
// 不到），给 DML 触发器的是 schema.name（@p2 是它的 [schema].[name]）；同名时 DDL 触发器在前。
const triggerLookup = "FROM sys.triggers tr WHERE (tr.parent_class = 0 AND tr.name = @p1) OR tr.object_id = OBJECT_ID(@p2) " +
	"ORDER BY tr.parent_class"

// dumpTriggerQuery 是触发器属于库（parent_class 0）还是表、是否禁用，以及它所在的表。
const dumpTriggerQuery = "SELECT TOP 1 tr.parent_class, tr.is_disabled, OBJECT_SCHEMA_NAME(tr.parent_id), OBJECT_NAME(tr.parent_id) " +
	triggerLookup

func triggerArgs(name string) []any {
	return []any{sql.Named("p1", name), sql.Named("p2", objectRef(name))}
}

func dumpTrigger(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	var (
		parentClass        int64
		disabled           bool
		tableSchema, table sql.NullString
	)
	if err := conn.QueryRowContext(ctx, dumpTriggerQuery, triggerArgs(object.Name)...).Scan(&parentClass, &disabled, &tableSchema, &table); err != nil {
		return sqldb.Wrap("trigger", err)
	}
	ref := sqldb.QuoteSchemaTable(object.Name, quoteIdent)
	on := ""
	if parentClass == 0 {
		on = " ON DATABASE"
		script.Drop = []string{"DROP TRIGGER IF EXISTS " + ref + on + ";"}
	} else {
		on = " ON " + quoteIdent(tableSchema.String) + "." + quoteIdent(table.String)
	}
	if disabled {
		script.Definition = append(script.Definition, "DISABLE TRIGGER "+ref+on+";")
	}
	return nil
}
