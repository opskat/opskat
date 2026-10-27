package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 表设计器：常用类型、默认值的四种写法、新列自增与索引编辑。声明与实现同源——描述的 ExplicitDefaults /
// AutoIncrement / IndexEditing 取自这里实现的 sqldb 方言接口。MySQL 的 DDL 会隐式提交、不能回滚，所以不声明
// TransactionalDDL：改表的列与索引改动合成一条 ALTER TABLE（MySQL 8 的单条 DDL 是原子的）。
var (
	_ sqldb.DefaultClassifier = dialect{}
	_ sqldb.AutoIncrementer   = dialect{}
	_ sqldb.TableIndexer      = dialect{}
	_ sqldb.TableCreator      = dialect{}
)

// columnTypes 是类型下拉里的常用类型；分组名是宿主多语言的键。
var columnTypes = &dbdriver.ColumnTypes{
	Groups: []dbdriver.ColumnTypeGroup{
		{Label: "query.designer.typeGroup.numeric", Types: []string{
			"TINYINT", "SMALLINT", "INT", "BIGINT", "DECIMAL(10,2)", "FLOAT", "DOUBLE", "BOOLEAN", "BIT(1)",
		}},
		{Label: "query.designer.typeGroup.string", Types: []string{
			"CHAR(36)", "VARCHAR(255)", "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT",
		}},
		{Label: "query.designer.typeGroup.datetime", Types: []string{"DATE", "TIME", "DATETIME", "TIMESTAMP", "YEAR"}},
		{Label: "query.designer.typeGroup.binary", Types: []string{"BINARY(16)", "VARBINARY(255)", "BLOB", "MEDIUMBLOB", "LONGBLOB"}},
		{Label: "query.designer.typeGroup.json", Types: []string{"JSON"}},
	},
	NewColumn:  "VARCHAR(255)",
	PrimaryKey: "BIGINT",
}

// indexMethodOption 是索引的方法选项（USING BTREE / HASH），值是 SHOW INDEX 的 Index_type 原文。
const indexMethodOption = "method"

// indexEditing 是 MySQL 的索引编辑声明：普通 / 唯一 / 全文与主键（可改），降序、前缀长度与注释，名称在表内
// 唯一，方法选项。
var indexEditing = &dbdriver.IndexEditing{
	Types: []dbdriver.IndexTypeDecl{
		{ID: dbdriver.IndexTypeNormal, Label: "query.designer.indexType.normal"},
		{ID: dbdriver.IndexTypeUnique, Label: "query.designer.indexType.unique"},
		{ID: dbdriver.IndexTypeFullText, Label: "query.designer.indexType.fulltext"},
		{ID: dbdriver.IndexTypePrimary, Label: "query.designer.indexType.primary"},
	},
	AlterPrimaryKey: true,
	Descending:      true,
	PrefixLength:    true,
	Comments:        true,
	NameScope:       dbdriver.IndexScopeTable,
	Options: []dbdriver.IndexOptionDecl{
		{Key: indexMethodOption, Label: "query.designer.indexOption.method", Type: dbdriver.FieldTypeEnum, Enum: []string{"BTREE", "HASH"}},
	},
}

// primaryKeyName 是 MySQL 主键索引的名字（固定，不能用作别的索引名）。
const primaryKeyName = "PRIMARY"

// 设计器无法原样重建的已有索引的原因（给用户看）：只读，只能删除。
const (
	readOnlyExpression = "It has an expression (functional) key part, which the designer cannot recreate."
	readOnlySpatial    = "It is a SPATIAL index, which the designer cannot recreate."
	readOnlyInvisible  = "It is an invisible index; recreating it in the designer would make it visible."
)

var (
	// temporalTypePattern 匹配能以时间函数为默认值的类型。
	temporalTypePattern = regexp.MustCompile(`(?i)^\s*(datetime|timestamp)\b`)
	// literalNotationPattern 匹配目录里位串与十六进制写法的默认值（b'101'、0x6162）：它们不是值的原文，
	// 原样写回才不改变定义。
	literalNotationPattern = regexp.MustCompile(`(?i)^(b'[01]*'|0x[0-9a-f]*)$`)
	// numericTypePattern 匹配数值类型：值写法的数字与布尔不加引号（BIT 列的 '1' 是字符 1，会超出范围）。
	numericTypePattern  = regexp.MustCompile(`(?i)^\s*(tinyint|smallint|mediumint|int|integer|bigint|decimal|dec|numeric|fixed|float|double|real|bit|bool|boolean|serial)\b`)
	numericValuePattern = regexp.MustCompile(`(?i)^([-+]?\d+(\.\d+)?|true|false)$`)
	// integerTypePattern 匹配能自增的整数类型。
	integerTypePattern = regexp.MustCompile(`(?i)^\s*(tinyint|smallint|mediumint|int|integer|bigint)\b`)
)

// ClassifyDefault 按 SHOW FULL COLUMNS 判定已有列的默认值写法。Default 为 NULL 是「无」（可空列没写默认值
// 时 MySQL 同样当作 DEFAULT NULL，两者没有区别）；MySQL 8 的表达式默认值（Extra 里 DEFAULT_GENERATED，
// CURRENT_TIMESTAMP 也是）、5.7 时间类型上的时间函数、位串与十六进制写法都是表达式，原样写回；其余是值原文。
func (dialect) ClassifyDefault(column sqldb.AlterableColumn) (dbdriver.DefaultKind, string) {
	if column.Row["Default"] == nil {
		return dbdriver.DefaultKindNone, ""
	}
	value := column.DefaultValue
	switch {
	case defaultGenerated(&column),
		temporalTypePattern.MatchString(column.Type) && generatedTimestampPattern.MatchString(value),
		literalNotationPattern.MatchString(value):
		return dbdriver.DefaultKindExpression, value
	}
	return dbdriver.DefaultKindValue, value
}

// defaultGenerated 报告已有列的默认值是 MySQL 8 的表达式默认值（Extra 里 DEFAULT_GENERATED）。
func defaultGenerated(column *sqldb.AlterableColumn) bool {
	return strings.Contains(strings.ToLower(sqldb.PickString(column.Row, "Extra")), "default_generated")
}

// AutoIncrementSQL 是自增列的 AUTO_INCREMENT；只有整数类型能自增。
func (dialect) AutoIncrementSQL(column dbdriver.ColumnDefinition) (string, error) {
	if !integerTypePattern.MatchString(column.Type) {
		return "", fmt.Errorf("column %q: AUTO_INCREMENT needs an integer type, not %q", column.Name, strings.TrimSpace(column.Type))
	}
	return "AUTO_INCREMENT", nil
}

// explicitDefault 把按写法给出的默认值写成 DEFAULT 后面的内容；没有默认值时 ok 为假。值按列类型写字面量
// （数值类型的数字与 TRUE / FALSE 原样，其余加引号）；表达式要加括号（MySQL 8.0.13 起的表达式默认值），时间
// 函数不加；current 非 nil 表示已有列的默认值没改，原样写回：只有原本就是表达式默认值的才加括号。
func explicitDefault(column dbdriver.ColumnDefinition, current *sqldb.AlterableColumn) (literal string, ok bool, err error) {
	value := strings.TrimSpace(column.DefaultValue)
	switch column.DefaultKind {
	case dbdriver.DefaultKindValue:
		if numericTypePattern.MatchString(column.Type) && numericValuePattern.MatchString(value) {
			return strings.ToUpper(value), true, nil
		}
	case dbdriver.DefaultKindExpression:
		if generatedTimestampPattern.MatchString(value) || (current != nil && !defaultGenerated(current)) {
			return value, true, nil
		}
		return "(" + value + ")", true, nil
	}
	return sqldb.DefaultSQL(sqldb.DefinitionLiterals, column)
}

// readIndexes 用 SHOW INDEX 读出表的索引（按 SHOW INDEX 的次序，主键在前）。8.0.13 起表达式键的
// Column_name 为 NULL、表达式在 Expression 列；5.7 没有这一列（也没有表达式索引）。
func readIndexes(ctx context.Context, conn *sql.Conn, database, table string) ([]dbdriver.IndexDefinition, error) {
	records, err := sqldb.QueryRecords(ctx, conn, "SHOW INDEX FROM "+quoteTableRef(database, table))
	if err != nil {
		return nil, err
	}
	var indexes []dbdriver.IndexDefinition
	position := map[string]int{}
	for _, row := range records {
		name := sqldb.PickString(row, "Key_name")
		i, seen := position[name]
		if !seen {
			i = len(indexes)
			position[name] = i
			indexes = append(indexes, newIndexDefinition(row, name))
		}
		index := &indexes[i]
		column := dbdriver.IndexColumn{
			Name:       sqldb.PickString(row, "Column_name"),
			Descending: sqldb.PickString(row, "Collation") == "D",
		}
		if column.Name == "" {
			column.Name, column.Expression = sqldb.PickString(row, "Expression"), true
			markReadOnly(index, readOnlyExpression)
		}
		if subPart := sqldb.PickString(row, "Sub_part"); subPart != "" {
			if column.PrefixLength, err = strconv.Atoi(subPart); err != nil {
				return nil, fmt.Errorf("index %q: prefix length %q: %w", name, subPart, err)
			}
		}
		index.Columns = append(index.Columns, column)
	}
	return indexes, nil
}

// newIndexDefinition 是索引第一行给出的类型、方法、注释与只读原因。
func newIndexDefinition(row map[string]any, name string) dbdriver.IndexDefinition {
	index := dbdriver.IndexDefinition{Name: name, Type: dbdriver.IndexTypeNormal, Comment: sqldb.PickString(row, "Index_comment")}
	indexType := strings.ToUpper(sqldb.PickString(row, "Index_type"))
	switch {
	case name == primaryKeyName:
		index.Type = dbdriver.IndexTypePrimary
	case indexType == "FULLTEXT":
		index.Type = dbdriver.IndexTypeFullText
	case sqldb.PickString(row, "Non_unique") == "0":
		index.Type = dbdriver.IndexTypeUnique
	}
	switch indexType {
	case "BTREE", "HASH":
		index.Options = map[string]any{indexMethodOption: indexType}
	case "SPATIAL":
		markReadOnly(&index, readOnlySpatial)
	}
	if strings.EqualFold(sqldb.PickString(row, "Visible"), "NO") {
		markReadOnly(&index, readOnlyInvisible)
	}
	return index
}

// markReadOnly 标出索引只读的原因；已有原因时保留第一个。
func markReadOnly(index *dbdriver.IndexDefinition, reason string) {
	if index.ReadOnlyReason == "" {
		index.ReadOnlyReason = reason
	}
}

func (dialect) IndexEditing() *dbdriver.IndexEditing { return indexEditing }

// IndexSQL 是单独建一个索引的 ALTER TABLE … ADD。
func (dialect) IndexSQL(tableRef string, index dbdriver.IndexDefinition) (string, error) {
	definition, err := indexDefinition(index)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + tableRef + " ADD " + definition, nil
}

// DropIndexSQL 是单独删一个索引的 ALTER TABLE … DROP。
func (dialect) DropIndexSQL(tableRef, name string) (string, error) {
	return "ALTER TABLE " + tableRef + " " + dropIndexClause(name), nil
}

// dropIndexClause 是 ALTER TABLE 里删一个索引的子句；主键是 DROP PRIMARY KEY。
func dropIndexClause(name string) string {
	if name == primaryKeyName {
		return "DROP PRIMARY KEY"
	}
	return "DROP INDEX " + quoteIdent(name)
}

// indexDefinition 是建表语句里的索引定义，也是 ALTER TABLE ADD 后面的部分：PRIMARY KEY / [UNIQUE |
// FULLTEXT] INDEX 名称、按次序的列（前缀长度、降序）、USING 方法与 COMMENT。主键的名字固定，不写。
func indexDefinition(index dbdriver.IndexDefinition) (string, error) {
	var definition string
	switch index.Type {
	case dbdriver.IndexTypePrimary:
		definition = "PRIMARY KEY"
	case dbdriver.IndexTypeUnique:
		definition = "UNIQUE INDEX"
	case dbdriver.IndexTypeFullText:
		definition = "FULLTEXT INDEX"
	default:
		definition = "INDEX"
	}
	if name := strings.TrimSpace(index.Name); name != "" && index.Type != dbdriver.IndexTypePrimary {
		definition += " " + quoteIdent(name)
	}
	if len(index.Columns) == 0 {
		return "", fmt.Errorf("index %q has no columns", index.Name)
	}
	parts := make([]string, len(index.Columns))
	for i, column := range index.Columns {
		part := quoteIdent(strings.TrimSpace(column.Name))
		if column.PrefixLength > 0 {
			part += "(" + strconv.Itoa(column.PrefixLength) + ")"
		}
		if column.Descending {
			part += " DESC"
		}
		parts[i] = part
	}
	definition += " (" + strings.Join(parts, ", ") + ")"
	if method, ok := index.Options[indexMethodOption].(string); ok {
		definition += " USING " + method
	}
	if comment := strings.TrimSpace(index.Comment); comment != "" {
		literal, err := sqldb.DefinitionLiterals.Literal(comment, "")
		if err != nil {
			return "", err
		}
		definition += " COMMENT " + literal
	}
	return definition, nil
}

// indexClauses 把共用的索引改动（sqldb.IndexDropStatements / IndexCreateStatements：删哪些、建哪些）写成
// ALTER TABLE 的子句，而不是各自一条语句：MySQL 改表的全部改动合成一条 ALTER TABLE——自增列必须始终在键上，
// 单独的 DROP PRIMARY KEY 会被拒绝（1075），新增的自增列也要在同一条语句里加上它的键；单条 DDL 在 MySQL 8 里
// 是原子的，DDL 又不能回滚，合成一条就没有半途状态。
type indexClauses struct{ dialect }

func (indexClauses) IndexSQL(_ string, index dbdriver.IndexDefinition) (string, error) {
	definition, err := indexDefinition(index)
	if err != nil {
		return "", err
	}
	return "ADD " + definition, nil
}

func (indexClauses) DropIndexSQL(_, name string) (string, error) { return dropIndexClause(name), nil }

// CreateTableSQL 写整条建表语句：列定义（自增、按写法的默认值、注释）之后是索引（主键在内），表注释写在表
// 选项里。没有用到这些的请求与共用的写法逐字一致。
func (d dialect) CreateTableSQL(tables sqldb.Tables, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	columns := req.Definitions()
	defs := make([]string, 0, len(columns)+len(req.Indexes))
	for _, column := range columns {
		written := column
		if column.DefaultKind != "" {
			// 共用的列定义把表达式原样写入：先按 MySQL 的写法算好 DEFAULT 的内容，再作为表达式交给它。
			literal, ok, err := explicitDefault(column, nil)
			if err != nil {
				return nil, err
			}
			written.DefaultKind, written.DefaultValue = dbdriver.DefaultKindNone, ""
			if ok {
				written.DefaultKind, written.DefaultValue = dbdriver.DefaultKindExpression, literal
			}
		}
		def, err := sqldb.NewColumnSQL(d, sqldb.DefinitionLiterals, tables.QuoteIdent, written)
		if err != nil {
			return nil, err
		}
		if comment := strings.TrimSpace(column.Comment); comment != "" {
			literal, err := sqldb.DefinitionLiterals.Literal(comment, "")
			if err != nil {
				return nil, err
			}
			def += " COMMENT " + literal
		}
		defs = append(defs, def)
	}
	for _, index := range req.Indexes {
		def, err := indexDefinition(index)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	statement := fmt.Sprintf("CREATE TABLE %s (\n  %s\n)", tables.TableRef(database, req.Name), strings.Join(defs, ",\n  "))
	if comment := strings.TrimSpace(req.Comment); comment != "" {
		literal, err := sqldb.DefinitionLiterals.Literal(comment, "")
		if err != nil {
			return nil, err
		}
		statement += " COMMENT = " + literal
	}
	return []string{statement}, nil
}
