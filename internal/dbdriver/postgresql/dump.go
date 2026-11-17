package postgresql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 转储：PostgreSQL 不能在会话里关闭外键检查，所以外键不在建表语句里（见 TableDDL），在全部表与数据之后用
// ALTER TABLE ... ADD CONSTRAINT 加上。文件开头关掉函数体检查（函数可以先于它用到的对象建出），建出转储范围里
// public 之外的 schema，接着是枚举类型（整库 / 整个 schema 时是范围里的全部，选中表时是这些表的列用到的）；
// 每张表的 INSERT 不写生成列，有 GENERATED ALWAYS 身份列的表加 OVERRIDING SYSTEM VALUE，列拥有的序列在
// 全部对象之后用 setval 同步到当前值；视图给出 pg_depend 里的依赖；函数与过程按每个重载删除、建出；触发器的
// 删除带上它的表。domain、复合、range 类型与扩展不写（见 spec 的 Out of scope）。
var (
	_ sqldb.DumpFileWriter   = dialect{}
	_ sqldb.DumpObjectWriter = dialect{}
)

// dumpSchemasQuery 读库里用户的 schema（public 不写：空库里本来就有）。
const dumpSchemasQuery = "SELECT nspname FROM pg_namespace WHERE nspname NOT IN " + systemSchemas +
	" AND nspname NOT LIKE 'pg\\_%' AND nspname <> 'public' ORDER BY nspname"

// enumTypesSelect 读枚举类型：schema、名字与按次序的标签（JSON 数组）。
const enumTypesSelect = "SELECT n.nspname, t.typname, (SELECT COALESCE(json_agg(e.enumlabel ORDER BY e.enumsortorder), '[]') " +
	"FROM pg_enum e WHERE e.enumtypid = t.oid) FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE t.typtype = 'e' AND "

// scopeEnumsQuery 读整个库（$1 为空）或整个 schema（$1）里的枚举类型。
const scopeEnumsQuery = enumTypesSelect + "n.nspname NOT IN " + systemSchemas + " AND ($1::text = '' OR n.nspname = $1::text) " +
	"ORDER BY n.nspname, t.typname"

// usedEnumsQuery 读 $1（JSON 数组，ListTables 的「schema.名字」）这些表的列用到的枚举类型（含枚举的数组）。
const usedEnumsQuery = enumTypesSelect + "EXISTS (SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid " +
	"JOIN pg_namespace cn ON cn.oid = c.relnamespace JOIN pg_type at ON at.oid = a.atttypid " +
	"WHERE a.attnum > 0 AND NOT a.attisdropped AND t.oid IN (at.oid, at.typelem) " +
	"AND cn.nspname || '.' || c.relname IN (SELECT jsonb_array_elements_text($1::jsonb))) " +
	"ORDER BY n.nspname, t.typname"

// dumpForeignKeysQuery 读表上的外键：名字与 pg_get_constraintdef 的原文。
const dumpForeignKeysQuery = "SELECT con.conname, pg_get_constraintdef(con.oid) FROM pg_constraint con " +
	"JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"WHERE con.contype = 'f' AND n.nspname = $1 AND c.relname = $2 ORDER BY con.conname"

// dumpColumnsQuery 读 INSERT 写入的列（去掉生成列），以及它是不是 GENERATED ALWAYS 身份列。attgenerated（12 起）
// 与 attidentity（10 起）经 to_jsonb 取，没有这一列的版本取到 NULL。
const dumpColumnsQuery = "SELECT a.attname, COALESCE(to_jsonb(a) ->> 'attidentity', '') = 'a' FROM pg_attribute a " +
	"WHERE a.attrelid = format('%I.%I', $1::text, $2::text)::regclass AND a.attnum > 0 AND NOT a.attisdropped " +
	"AND COALESCE(to_jsonb(a) ->> 'attgenerated', '') = '' ORDER BY a.attnum"

// dumpSequencesQuery 读表的列拥有的序列（serial 的 'a'、身份列的 'i'）的当前值；没取过值的序列 last_value 为
// NULL，不用同步。
const dumpSequencesQuery = "SELECT a.attname, s.last_value FROM pg_attribute a " +
	"JOIN pg_depend d ON d.refclassid = 'pg_class'::regclass AND d.refobjid = a.attrelid AND d.refobjsubid = a.attnum " +
	"AND d.classid = 'pg_class'::regclass AND d.deptype IN ('a', 'i') " +
	"JOIN pg_class sc ON sc.oid = d.objid AND sc.relkind = 'S' " +
	"JOIN pg_namespace sn ON sn.oid = sc.relnamespace " +
	"JOIN pg_sequences s ON s.schemaname = sn.nspname AND s.sequencename = sc.relname " +
	"WHERE a.attrelid = format('%I.%I', $1::text, $2::text)::regclass AND s.last_value IS NOT NULL ORDER BY a.attnum"

// viewDependenciesQuery 读视图（的重写规则）在 pg_depend 里依赖的表与视图，名字的形式同 ListTables。
const viewDependenciesQuery = "SELECT DISTINCT rn.nspname || '.' || rc.relname FROM pg_rewrite r " +
	"JOIN pg_depend d ON d.classid = 'pg_rewrite'::regclass AND d.objid = r.oid AND d.refclassid = 'pg_class'::regclass " +
	"JOIN pg_class rc ON rc.oid = d.refobjid JOIN pg_namespace rn ON rn.oid = rc.relnamespace " +
	"WHERE r.ev_class = format('%I.%I', $1::text, $2::text)::regclass AND rc.oid <> r.ev_class ORDER BY 1"

func (dialect) DumpFile(ctx context.Context, conn *sql.Conn, scope dbdriver.DumpScope, script *dbdriver.DumpFileScript) error {
	schemas := map[string]bool{}
	for _, table := range scope.Tables {
		schema, _ := splitSchemaTable(table)
		schemas[schema] = true
	}
	var enums []enumType
	var err error
	switch {
	case scope.Whole && scope.Schema != "":
		schemas[scope.Schema] = true
		enums, err = queryEnums(ctx, conn, scopeEnumsQuery, scope.Schema)
	case scope.Whole:
		var all []string
		if all, err = sqldb.QueryNames(ctx, conn, dumpSchemasQuery); err != nil {
			return sqldb.Wrap("schemas", err)
		}
		for _, schema := range all {
			schemas[schema] = true
		}
		enums, err = queryEnums(ctx, conn, scopeEnumsQuery, "")
	default:
		var tables []byte
		if tables, err = json.Marshal(scope.Tables); err != nil {
			return err
		}
		enums, err = queryEnums(ctx, conn, usedEnumsQuery, string(tables))
	}
	if err != nil {
		return sqldb.Wrap("enum types", err)
	}
	for _, enum := range enums {
		schemas[enum.schema] = true
		statement, err := enum.createSQL()
		if err != nil {
			return err
		}
		script.Types = append(script.Types, statement)
	}
	script.Opening = append(script.Opening, "SET check_function_bodies = false;")
	delete(schemas, "public")
	for _, schema := range slices.Sorted(maps.Keys(schemas)) {
		script.Opening = append(script.Opening, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(schema)+";")
	}
	script.Closing = append(script.Closing, "RESET check_function_bodies;")
	return nil
}

// enumType 是一个枚举类型与它按次序的标签。
type enumType struct {
	schema, name string
	labels       []string
}

func (e enumType) createSQL() (string, error) {
	labels := make([]string, len(e.labels))
	for i, label := range e.labels {
		literal, err := sqldb.DefinitionLiterals.Literal(label, "")
		if err != nil {
			return "", err
		}
		labels[i] = literal
	}
	return fmt.Sprintf("CREATE TYPE %s.%s AS ENUM (%s);", quoteIdent(e.schema), quoteIdent(e.name), strings.Join(labels, ", ")), nil
}

func queryEnums(ctx context.Context, conn *sql.Conn, query string, arg string) ([]enumType, error) {
	rows, err := conn.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var enums []enumType
	for rows.Next() {
		var e enumType
		var labels string
		if err := rows.Scan(&e.schema, &e.name, &labels); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(labels), &e.labels); err != nil {
			return nil, fmt.Errorf("enum labels of %s.%s: %w", e.schema, e.name, err)
		}
		enums = append(enums, e)
	}
	return enums, rows.Err()
}

func (dialect) DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	schema, name := splitSchemaTable(object.Name)
	switch object.Kind {
	case dbdriver.DumpTable:
		return dumpTable(ctx, conn, object, schema, name, script)
	case dbdriver.ObjectView:
		used, err := sqldb.QueryNames(ctx, conn, viewDependenciesQuery, schema, name)
		if err != nil {
			return sqldb.Wrap("view dependencies", err)
		}
		script.DependsOn = used
		return nil
	case dbdriver.ObjectTrigger:
		// 触发器名只在表内唯一，删除要带上表（与触发器同在一个 schema）。
		script.Drop = []string{"DROP TRIGGER IF EXISTS " + quoteIdent(name) + " ON " + quoteIdent(schema) + "." + quoteIdent(object.Table) + ";"}
		return nil
	default: // 函数与过程：每个重载各自删除（带完整的参数，同名有重载时只写名字删不掉）、各自建出
		overloads, err := queryRoutines(ctx, conn, object.Kind, schema, name)
		if err != nil {
			return sqldb.Wrap("overloads", err)
		}
		script.Drop, script.Definition = nil, nil
		for _, overload := range overloads {
			script.Drop = append(script.Drop, fmt.Sprintf("DROP %s IF EXISTS %s.%s(%s);",
				routineKeywords[object.Kind], quoteIdent(schema), quoteIdent(name), overload.arguments))
			script.Definition = append(script.Definition, sqldb.TerminateStatement(overload.definition))
		}
		return nil
	}
}

// routineKeywords 是删除函数与过程的关键字。
var routineKeywords = map[string]string{dbdriver.ObjectFunction: "FUNCTION", dbdriver.ObjectProcedure: "PROCEDURE"}

// dumpTable 补上表的外键（全部数据之后加上）；有数据时还有 INSERT 的列、身份列的写法与序列的同步。
func dumpTable(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, schema, name string, script *dbdriver.DumpObjectScript) error {
	tableRef := quoteIdent(schema) + "." + quoteIdent(name)
	foreignKeys, err := addForeignKeys(ctx, conn, schema, name, tableRef)
	if err != nil {
		return sqldb.Wrap("foreign keys", err)
	}
	script.ForeignKeys = foreignKeys
	if !object.WithData {
		return nil
	}
	if err := dataColumns(ctx, conn, schema, name, script); err != nil {
		return sqldb.Wrap("columns", err)
	}
	postData, err := sequenceValues(ctx, conn, schema, name, tableRef)
	if err != nil {
		return sqldb.Wrap("sequences", err)
	}
	script.PostData = postData
	return nil
}

// addForeignKeys 是给表加上它的外键的语句（pg_get_constraintdef 的原文）。
func addForeignKeys(ctx context.Context, conn *sql.Conn, schema, name, tableRef string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, dumpForeignKeysQuery, schema, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var statements []string
	for rows.Next() {
		var constraint, definition string
		if err := rows.Scan(&constraint, &definition); err != nil {
			return nil, err
		}
		statements = append(statements, "ALTER TABLE "+tableRef+" ADD CONSTRAINT "+quoteIdent(constraint)+" "+definition+";")
	}
	return statements, rows.Err()
}

// dataColumns 是 INSERT 写入的列（不含生成列）；有 GENERATED ALWAYS 身份列时，INSERT 要 OVERRIDING SYSTEM VALUE
// 才能写入原来的值。
func dataColumns(ctx context.Context, conn *sql.Conn, schema, name string, script *dbdriver.DumpObjectScript) error {
	rows, err := conn.QueryContext(ctx, dumpColumnsQuery, schema, name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	script.DataColumns = []string{}
	for rows.Next() {
		var column string
		var identityAlways bool
		if err := rows.Scan(&column, &identityAlways); err != nil {
			return err
		}
		script.DataColumns = append(script.DataColumns, column)
		if identityAlways {
			script.InsertModifier = "OVERRIDING SYSTEM VALUE"
		}
	}
	return rows.Err()
}

// sequenceValues 是把表的列拥有的序列同步到当前值的语句。序列按列经 pg_get_serial_sequence 找：还原出的序列
// 名字不一定与原来的相同（身份列的序列随列建出）。
func sequenceValues(ctx context.Context, conn *sql.Conn, schema, name, tableRef string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, dumpSequencesQuery, schema, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	table, err := sqldb.DefinitionLiterals.Literal(tableRef, "")
	if err != nil {
		return nil, err
	}
	var statements []string
	for rows.Next() {
		var column string
		var value int64
		if err := rows.Scan(&column, &value); err != nil {
			return nil, err
		}
		columnLiteral, err := sqldb.DefinitionLiterals.Literal(column, "")
		if err != nil {
			return nil, err
		}
		statements = append(statements, "SELECT pg_catalog.setval(pg_catalog.pg_get_serial_sequence("+table+", "+columnLiteral+"), "+
			strconv.FormatInt(value, 10)+", true);")
	}
	return statements, rows.Err()
}
