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
// ALTER TABLE ... ADD CONSTRAINT 加上（勾了「包含外键」时）；删除段先用 ALTER TABLE IF EXISTS ... DROP CONSTRAINT
// IF EXISTS 拆掉要转储的表上的外键，被引用的表才删得掉，最后删枚举类型（都不带 CASCADE）。不勾「包含索引」时
// 建表语句不含索引。文件开头关掉函数体检查（函数可以先于它用到的对象建出），建出转储范围里
// public 之外的 schema，接着是枚举类型（整库 / 整个 schema 时是范围里的全部，加上范围里的表的列用到的别的
// schema 里的；选中表时是这些表的列用到的）；每张表的 INSERT 不写生成列，有 GENERATED ALWAYS 身份列的表加
// OVERRIDING SYSTEM VALUE，列拥有的序列在全部对象之后用 setval 同步到当前值；视图、函数与过程给出 pg_depend 里
// 的依赖（见 dependenciesQuery）；函数与过程按每个重载删除、建出；触发器的删除带上它的表。domain、复合、range 类型与扩展不写（见 spec 的 Out of scope）。
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

// enumUsedByTables 是「枚举类型 t 被一些表的列用到（含枚举的数组）」的条件，接在它后面的参数（JSON 数组，ListTables
// 的「schema.名字」）是这些表，再接 enumUsedByTablesEnd。
const (
	enumUsedByTables = "EXISTS (SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid " +
		"JOIN pg_namespace cn ON cn.oid = c.relnamespace JOIN pg_type at ON at.oid = a.atttypid " +
		"WHERE a.attnum > 0 AND NOT a.attisdropped AND t.oid IN (at.oid, at.typelem) " +
		"AND cn.nspname || '.' || c.relname IN (SELECT jsonb_array_elements_text("
	enumUsedByTablesEnd = "::jsonb)))"
)

// scopeEnumsQuery 读整个库（$1 为空）或整个 schema（$1）里的枚举类型，以及范围里的表（$2）的列用到的别的 schema
// 里的枚举类型：没有它们，这些表在空库上建不出来。
const scopeEnumsQuery = enumTypesSelect + "n.nspname NOT IN " + systemSchemas + " AND ($1::text = '' OR n.nspname = $1::text OR " +
	enumUsedByTables + "$2" + enumUsedByTablesEnd + ") ORDER BY n.nspname, t.typname"

// usedEnumsQuery 读 $1 这些表的列用到的枚举类型。
const usedEnumsQuery = enumTypesSelect + enumUsedByTables + "$1" + enumUsedByTablesEnd + " ORDER BY n.nspname, t.typname"

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

// dependenciesQuery 读一个对象（dependents 是 pg_depend 里依赖方的条件）在 pg_depend 里依赖的视图、函数与过程：
// 种类与名字（形式同 ListTables / ListObjects）。视图的重写规则与 BEGIN ATOMIC 的函数、过程体都记在 pg_depend
// 里：调用函数的视图、引用视图的函数都在其中；用到视图的行类型（RETURNS SETOF 视图、这种类型的参数）也算依赖
// 这个视图。字符串写的函数体（AS $$ … $$）没有记录，建出时也不检查（文件开头关掉了 check_function_bodies）。
func dependenciesQuery(dependents string) string {
	return "WITH refs AS (SELECT d.refclassid, d.refobjid FROM pg_depend d WHERE " + dependents + ") " +
		"SELECT '" + dbdriver.ObjectView + "', n.nspname || '.' || c.relname FROM refs JOIN pg_class c ON c.relkind = 'v' AND " +
		"((refs.refclassid = 'pg_class'::regclass AND c.oid = refs.refobjid) OR (refs.refclassid = 'pg_type'::regclass AND c.reltype = refs.refobjid)) " +
		"JOIN pg_namespace n ON n.oid = c.relnamespace " +
		"UNION SELECT CASE WHEN COALESCE(to_jsonb(p) ->> 'prokind', 'f') = 'p' THEN '" + dbdriver.ObjectProcedure + "' ELSE '" + dbdriver.ObjectFunction + "' END, " +
		"n.nspname || '.' || p.proname FROM refs JOIN pg_proc p ON refs.refclassid = 'pg_proc'::regclass AND p.oid = refs.refobjid " +
		"JOIN pg_namespace n ON n.oid = p.pronamespace ORDER BY 1, 2"
}

var (
	// viewDependenciesQuery 是视图（$1 schema、$2 名字）的重写规则依赖的对象。
	viewDependenciesQuery = dependenciesQuery("d.classid = 'pg_rewrite'::regclass AND d.objid IN " +
		"(SELECT r.oid FROM pg_rewrite r WHERE r.ev_class = format('%I.%I', $1::text, $2::text)::regclass)")
	// routineDependenciesQuery 是函数或过程（$1 schema、$2 名字、$3 prokind，同 routinesQuery）每个重载依赖的对象。
	routineDependenciesQuery = dependenciesQuery("d.classid = 'pg_proc'::regclass AND d.objid IN " +
		"(SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace " +
		"WHERE n.nspname = $1 AND p.proname = $2 AND COALESCE(to_jsonb(p) ->> 'prokind', 'f') = $3)")
)

func (dialect) DumpFile(ctx context.Context, conn *sql.Conn, scope dbdriver.DumpScope, script *dbdriver.DumpFileScript) error {
	schemas := map[string]bool{}
	for _, table := range scope.Tables {
		schema, _ := splitSchemaTable(table)
		schemas[schema] = true
	}
	tables, err := json.Marshal(scope.Tables)
	if err != nil {
		return err
	}
	var enums []enumType
	switch {
	case scope.Whole && scope.Schema != "":
		schemas[scope.Schema] = true
		enums, err = queryEnums(ctx, conn, scopeEnumsQuery, scope.Schema, string(tables))
	case scope.Whole:
		var all []string
		if all, err = sqldb.QueryNames(ctx, conn, dumpSchemasQuery); err != nil {
			return sqldb.Wrap("schemas", err)
		}
		for _, schema := range all {
			schemas[schema] = true
		}
		enums, err = queryEnums(ctx, conn, scopeEnumsQuery, "", string(tables))
	default:
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
		script.DropTypes = append(script.DropTypes, "DROP TYPE IF EXISTS "+enum.ref()+";")
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

func (e enumType) ref() string { return quoteIdent(e.schema) + "." + quoteIdent(e.name) }

func (e enumType) createSQL() (string, error) {
	labels := make([]string, len(e.labels))
	for i, label := range e.labels {
		literal, err := sqldb.DefinitionLiterals.Literal(label, "")
		if err != nil {
			return "", err
		}
		labels[i] = literal
	}
	return fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);", e.ref(), strings.Join(labels, ", ")), nil
}

func queryEnums(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]enumType, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
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
		used, err := sqldb.QueryDumpDependencies(ctx, conn, viewDependenciesQuery, schema, name)
		if err != nil {
			return sqldb.Wrap("view dependencies", err)
		}
		script.Dependencies = used
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
		used, err := sqldb.QueryDumpDependencies(ctx, conn, routineDependenciesQuery, schema, name, routineKinds[object.Kind])
		if err != nil {
			return sqldb.Wrap("dependencies", err)
		}
		script.Dependencies = used
		return nil
	}
}

// routineKeywords 是删除函数与过程的关键字。
var routineKeywords = map[string]string{dbdriver.ObjectFunction: "FUNCTION", dbdriver.ObjectProcedure: "PROCEDURE"}

// dumpTable 补上表的外键（全部数据之后加上、删除段里先拆掉），不包含索引时换成不含索引的建表语句；有数据时
// 还有 INSERT 的列、身份列的写法与序列的同步。
func dumpTable(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, schema, name string, script *dbdriver.DumpObjectScript) error {
	tableRef := quoteIdent(schema) + "." + quoteIdent(name)
	if !object.Options.Indexes {
		definition, err := dialect{}.tableDDL(ctx, conn, object.Name, false)
		if err != nil {
			return sqldb.Wrap("table definition", err)
		}
		script.Definition = []string{sqldb.TerminateStatement(definition)}
	}
	if err := foreignKeys(ctx, conn, schema, name, tableRef, script); err != nil {
		return sqldb.Wrap("foreign keys", err)
	}
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

// foreignKeys 给出表上的外键：加上它们的语句（pg_get_constraintdef 的原文）与存在则拆掉它们的语句。
func foreignKeys(ctx context.Context, conn *sql.Conn, schema, name, tableRef string, script *dbdriver.DumpObjectScript) error {
	rows, err := conn.QueryContext(ctx, dumpForeignKeysQuery, schema, name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var constraint, definition string
		if err := rows.Scan(&constraint, &definition); err != nil {
			return err
		}
		script.ForeignKeys = append(script.ForeignKeys, "ALTER TABLE "+tableRef+" ADD CONSTRAINT "+quoteIdent(constraint)+" "+definition+";")
		script.DropForeignKeys = append(script.DropForeignKeys,
			"ALTER TABLE IF EXISTS "+tableRef+" DROP CONSTRAINT IF EXISTS "+quoteIdent(constraint)+";")
	}
	return rows.Err()
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
