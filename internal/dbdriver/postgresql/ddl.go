package postgresql

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 建表语句：PostgreSQL 不存原文，从系统目录重建，写到能在空库上还原的程度——「查看 DDL」、表设计器与
// 转储用的都是它。表名是 "schema.table"（裸名按 public），一切都按表真正所在的 schema 查，语句里的表名
// 也带这个 schema。依次是：
//   - 表的列拥有的序列（serial）先建出，建表后再 OWNED BY 归属到列；身份列的序列随列建出，不单写；
//   - CREATE TABLE：列类型用 format_type 取数据库自己的完整写法（长度、精度），pg_catalog 之外的类型
//     一律带它所在的 schema——format_type 只给 search_path 外的类型加 schema，在 search_path 内的由这里
//     补上，结果不随会话的 search_path 变化；非空、默认值、GENERATED ... AS IDENTITY（带序列参数）、
//     生成列；主键、唯一、CHECK 与排除约束按 pg_get_constraintdef 的原文写在表内。外键不在这里；
//   - 不由约束建出的索引各自一条 pg_get_indexdef；
//   - 表与列的注释用 COMMENT ON。
//
// 默认值、身份列与生成表达式取 information_schema，各版本通用；生成列存储方式（PostgreSQL 18 起有
// VIRTUAL）只在 pg_attribute.attgenerated 里，经 to_jsonb 取，没有这一列的版本取到 NULL。
// 视图输出其定义（CREATE VIEW）。
var _ sqldb.DDL = dialect{}

const ddlColumnsQuery = "SELECT a.attname, " +
	"CASE WHEN tn.nspname = 'pg_catalog' OR NOT pg_type_is_visible(a.atttypid) " +
	"THEN format_type(a.atttypid, a.atttypmod) " +
	"ELSE quote_ident(tn.nspname) || '.' || format_type(a.atttypid, a.atttypmod) END, " +
	"NOT a.attnotnull, col.column_default, c.relkind::text, " +
	"CASE WHEN col.is_identity = 'YES' THEN col.identity_generation END, " +
	"col.identity_start, col.identity_increment, col.identity_minimum, col.identity_maximum, " +
	"COALESCE(col.identity_cycle = 'YES', false), " +
	"CASE WHEN col.is_generated = 'ALWAYS' THEN col.generation_expression END, " +
	"to_jsonb(a) ->> 'attgenerated', " +
	"col_description(c.oid, a.attnum), obj_description(c.oid, 'pg_class') " +
	"FROM pg_class c " +
	"JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped " +
	"JOIN pg_type t ON t.oid = a.atttypid " +
	"JOIN pg_namespace tn ON tn.oid = t.typnamespace " +
	"LEFT JOIN information_schema.columns col " +
	"ON col.table_schema = n.nspname AND col.table_name = c.relname AND col.column_name = a.attname " +
	"WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v') " +
	"ORDER BY a.attnum"

// ownedSequencesQuery 读表的列拥有的序列（serial 的自动依赖 'a'；身份列的序列是内部依赖 'i'，不在其中）
// 与它们的参数。
const ownedSequencesQuery = "SELECT seq.sequence_schema, seq.sequence_name, a.attname, seq.data_type, " +
	"seq.start_value, seq.increment, seq.minimum_value, seq.maximum_value, seq.cycle_option = 'YES' " +
	"FROM pg_class c " +
	"JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"JOIN pg_depend d ON d.refclassid = 'pg_class'::regclass AND d.refobjid = c.oid " +
	"AND d.classid = 'pg_class'::regclass AND d.deptype = 'a' " +
	"JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S' " +
	"JOIN pg_namespace sn ON sn.oid = s.relnamespace " +
	"JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = d.refobjsubid " +
	"JOIN information_schema.sequences seq ON seq.sequence_schema = sn.nspname AND seq.sequence_name = s.relname " +
	"WHERE n.nspname = $1 AND c.relname = $2 " +
	"ORDER BY sn.nspname, s.relname"

// tableConstraintsQuery 读写在表内的约束：主键、唯一、CHECK、排除（不含外键，也不含 18 起记在
// pg_constraint 里的 NOT NULL——它由列的非空写出）。主键在前，其余按种类与名字。
const tableConstraintsQuery = "SELECT con.conname, pg_get_constraintdef(con.oid) FROM pg_constraint con " +
	"JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"WHERE n.nspname = $1 AND c.relname = $2 AND con.contype IN ('p', 'u', 'c', 'x') " +
	"ORDER BY CASE con.contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'c' THEN 2 ELSE 3 END, con.conname"

// tableIndexesQuery 读不由约束建出的索引的定义（由主键、唯一、排除约束建出的随约束建出）。
const tableIndexesQuery = "SELECT pg_get_indexdef(i.indexrelid) FROM pg_index i " +
	"JOIN pg_class c ON c.oid = i.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"JOIN pg_class ic ON ic.oid = i.indexrelid " +
	"WHERE n.nspname = $1 AND c.relname = $2 AND NOT EXISTS (SELECT 1 FROM pg_constraint con " +
	"WHERE con.conrelid = i.indrelid AND con.conindid = i.indexrelid AND con.contype IN ('p', 'u', 'x')) " +
	"ORDER BY ic.relname"

// viewDefQuery 取视图定义：建表语句与对象源码共用。format('%I.%I', ...) 给标识符加引号，
// 混合大小写或带特殊字符的视图名才能解析到正确的 regclass。format 的参数是 variadic "any"，
// 占位符必须显式 ::text，否则服务端报 could not determine data type of parameter（42P18）。
const viewDefQuery = "SELECT pg_get_viewdef(format('%I.%I', $1::text, $2::text)::regclass, true)"

// sequenceOptions 是序列的参数，原样取自 information_schema 的数字文本。
type sequenceOptions struct {
	start, increment, minimum, maximum string
	cycle                              bool
}

func (o sequenceOptions) sql() string {
	s := "START WITH " + o.start + " INCREMENT BY " + o.increment + " MINVALUE " + o.minimum + " MAXVALUE " + o.maximum
	if o.cycle {
		s += " CYCLE"
	}
	return s
}

// ddlColumn 是建表语句里的一列。
type ddlColumn struct {
	name, dataType string
	nullable       bool
	// def 是默认值表达式原文；空串表示没有。
	def string
	// identity 是身份列的 ALWAYS / BY DEFAULT；空串表示不是身份列。
	identity        string
	identityOptions sequenceOptions
	// generated 是生成列的表达式；空串表示不是生成列。generatedKind 是 attgenerated（'v' 为虚拟）。
	generated, generatedKind string
	comment                  sql.NullString
}

func (c ddlColumn) sql() string {
	def := quoteIdent(c.name) + " " + c.dataType
	if !c.nullable {
		def += " NOT NULL"
	}
	if c.def != "" {
		def += " DEFAULT " + c.def
	}
	if c.identity != "" {
		def += " GENERATED " + c.identity + " AS IDENTITY (" + c.identityOptions.sql() + ")"
	}
	if c.generated != "" {
		storage := " STORED"
		if c.generatedKind == "v" {
			storage = " VIRTUAL"
		}
		def += " GENERATED ALWAYS AS (" + c.generated + ")" + storage
	}
	return def
}

// ddlRelation 是列查询读到的一张表 / 视图；对象不存在时没有列。
type ddlRelation struct {
	kind    string
	comment sql.NullString
	columns []ddlColumn
}

// ownedSequence 是表的某一列拥有的序列。
type ownedSequence struct {
	schema, name, column, dataType string
	options                        sequenceOptions
}

func (s ownedSequence) ref() string { return quoteIdent(s.schema) + "." + quoteIdent(s.name) }

func (s ownedSequence) createSQL() string {
	def := "CREATE SEQUENCE " + s.ref()
	if s.dataType != "bigint" { // bigint 是默认，也是 10 之前唯一的取值
		def += " AS " + s.dataType
	}
	return def + " " + s.options.sql() + ";"
}

func (d dialect) TableDDL(ctx context.Context, conn *sql.Conn, _, table string) (string, error) {
	return d.tableDDL(ctx, conn, table, true)
}

// tableDDL 是 TableDDL 的建表语句；withIndexes 为假时不含不由约束建出的索引（转储不勾「包含索引」时）。
func (d dialect) tableDDL(ctx context.Context, conn *sql.Conn, table string, withIndexes bool) (string, error) {
	schema, tbl := splitSchemaTable(table)
	rel, err := queryDDLRelation(ctx, conn, schema, tbl)
	if err != nil {
		return "", err
	}
	if len(rel.columns) == 0 {
		return "", nil
	}
	tableRef := quoteIdent(schema) + "." + quoteIdent(tbl)
	if rel.kind == "v" {
		var def string
		if err := conn.QueryRowContext(ctx, viewDefQuery, schema, tbl).Scan(&def); err != nil {
			return "", err
		}
		// 美化输出的定义以空白开头，去掉后接在 AS 之后。
		return "CREATE VIEW " + tableRef + " AS " + strings.TrimLeft(def, " \t\n"), nil
	}
	sequences, err := queryOwnedSequences(ctx, conn, schema, tbl)
	if err != nil {
		return "", err
	}
	constraints, err := queryTableConstraints(ctx, conn, schema, tbl)
	if err != nil {
		return "", err
	}
	var indexes []string
	if withIndexes {
		if indexes, err = sqldb.QueryNames(ctx, conn, tableIndexesQuery, schema, tbl); err != nil {
			return "", err
		}
	}

	var statements []string
	for _, seq := range sequences {
		statements = append(statements, seq.createSQL())
	}
	defs := make([]string, 0, len(rel.columns)+len(constraints))
	for _, column := range rel.columns {
		defs = append(defs, column.sql())
	}
	defs = append(defs, constraints...)
	statements = append(statements, "CREATE TABLE "+tableRef+" (\n  "+strings.Join(defs, ",\n  ")+"\n);")
	for _, seq := range sequences {
		statements = append(statements, "ALTER SEQUENCE "+seq.ref()+" OWNED BY "+tableRef+"."+quoteIdent(seq.column)+";")
	}
	for _, index := range indexes {
		statements = append(statements, index+";")
	}
	if rel.comment.String != "" {
		comment, err := d.TableCommentSQL(tableRef, rel.comment.String)
		if err != nil {
			return "", err
		}
		statements = append(statements, comment+";")
	}
	for _, column := range rel.columns {
		if column.comment.String == "" {
			continue
		}
		value, err := commentValue(column.comment.String)
		if err != nil {
			return "", err
		}
		statements = append(statements, "COMMENT ON COLUMN "+tableRef+"."+quoteIdent(column.name)+" IS "+value+";")
	}
	return strings.Join(statements, "\n\n"), nil
}

// queryDDLRelation 读一张表 / 视图的列、relkind 与表注释。
func queryDDLRelation(ctx context.Context, conn *sql.Conn, schema, table string) (ddlRelation, error) {
	rows, err := conn.QueryContext(ctx, ddlColumnsQuery, schema, table)
	if err != nil {
		return ddlRelation{}, err
	}
	defer func() { _ = rows.Close() }()
	var rel ddlRelation
	for rows.Next() {
		var c ddlColumn
		var def, identity, start, increment, minimum, maximum, generated, generatedKind sql.NullString
		if err := rows.Scan(&c.name, &c.dataType, &c.nullable, &def, &rel.kind,
			&identity, &start, &increment, &minimum, &maximum, &c.identityOptions.cycle,
			&generated, &generatedKind, &c.comment, &rel.comment); err != nil {
			return ddlRelation{}, err
		}
		c.def, c.identity, c.generated, c.generatedKind = def.String, identity.String, generated.String, generatedKind.String
		c.identityOptions.start, c.identityOptions.increment = start.String, increment.String
		c.identityOptions.minimum, c.identityOptions.maximum = minimum.String, maximum.String
		rel.columns = append(rel.columns, c)
	}
	return rel, rows.Err()
}

func queryOwnedSequences(ctx context.Context, conn *sql.Conn, schema, table string) ([]ownedSequence, error) {
	rows, err := conn.QueryContext(ctx, ownedSequencesQuery, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var sequences []ownedSequence
	for rows.Next() {
		var s ownedSequence
		o := &s.options
		if err := rows.Scan(&s.schema, &s.name, &s.column, &s.dataType,
			&o.start, &o.increment, &o.minimum, &o.maximum, &o.cycle); err != nil {
			return nil, err
		}
		sequences = append(sequences, s)
	}
	return sequences, rows.Err()
}

// queryTableConstraints 读表内约束，写成 CONSTRAINT "名字" 定义。
func queryTableConstraints(ctx context.Context, conn *sql.Conn, schema, table string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, tableConstraintsQuery, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var constraints []string
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		constraints = append(constraints, "CONSTRAINT "+quoteIdent(name)+" "+def)
	}
	return constraints, rows.Err()
}
