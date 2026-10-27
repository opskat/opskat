package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 表设计器：常用类型、默认值的四种写法、新列自增（IDENTITY(1,1)）与索引编辑。声明与实现同源——描述的
// ExplicitDefaults / AutoIncrement / IndexEditing 取自这里实现的 sqldb 方言接口。SQL Server 的 DDL 可以在
// 事务里执行，描述声明 TransactionalDDL：保存时整批语句原子执行，失败则表结构不变。
//
// 已有列只能改类型与可空性（见 alter.go）：默认值的写法只用在新列、新表与读出的现状上。建表由方言整条写
// （TableCreator）：主键写在表定义里，聚集与否要看同一请求里的其他索引。
var (
	_ sqldb.DefaultClassifier = dialect{}
	_ sqldb.AutoIncrementer   = dialect{}
	_ sqldb.TableIndexer      = dialect{}
	_ sqldb.TableCreator      = dialect{}
)

// columnTypes 是类型下拉里的常用类型；分组名是宿主多语言的键。SQL Server 2025 之前没有 JSON 类型（JSON 存在
// nvarchar(max) 里），不列 JSON 分组。
var columnTypes = &dbdriver.ColumnTypes{
	Groups: []dbdriver.ColumnTypeGroup{
		{Label: "query.designer.typeGroup.numeric", Types: []string{
			"tinyint", "smallint", "int", "bigint", "decimal(18,2)", "float", "real", "money",
		}},
		{Label: "query.designer.typeGroup.string", Types: []string{
			"nvarchar(255)", "nvarchar(max)", "varchar(255)", "varchar(max)", "nchar(10)", "char(10)", "uniqueidentifier",
		}},
		{Label: "query.designer.typeGroup.datetime", Types: []string{
			"date", "time", "datetime2", "datetimeoffset", "datetime", "smalldatetime",
		}},
		{Label: "query.designer.typeGroup.binary", Types: []string{"varbinary(max)", "varbinary(255)", "binary(16)"}},
		{Label: "query.designer.typeGroup.other", Types: []string{
			"bit", "xml", "sql_variant", "hierarchyid", "geography", "geometry", "rowversion",
		}},
	},
	NewColumn:  "nvarchar(255)",
	PrimaryKey: "bigint",
}

// clusteredOption 是索引的聚集开关。一张表至多一个聚集索引（Exclusive）。没给这个选项的索引照 SQL Server 的
// 缺省写（CREATE INDEX 与 UNIQUE 约束非聚集，主键在表上还没有聚集索引时聚集）；给了就写出 CLUSTERED /
// NONCLUSTERED。读出的索引总给出它。
const clusteredOption = "clustered"

// indexEditing 是 SQL Server 的索引编辑声明：普通 / 唯一与主键（可改），降序，没有前缀长度与索引注释；索引名在
// 表内唯一；聚集开关。
var indexEditing = &dbdriver.IndexEditing{
	Types: []dbdriver.IndexTypeDecl{
		{ID: dbdriver.IndexTypeNormal, Label: "query.designer.indexType.normal"},
		{ID: dbdriver.IndexTypeUnique, Label: "query.designer.indexType.unique"},
		{ID: dbdriver.IndexTypePrimary, Label: "query.designer.indexType.primary"},
	},
	AlterPrimaryKey: true,
	Descending:      true,
	NameScope:       dbdriver.IndexScopeTable,
	Options: []dbdriver.IndexOptionDecl{
		{Key: clusteredOption, Label: "query.designer.indexOption.clustered", Type: dbdriver.FieldTypeSwitch, Exclusive: true},
	},
}

// --- 列：默认值写法与自增 ---

var (
	// quotedDefaultPattern 匹配一个字符串常量（可带 N 前缀）；第一组是引号里的原文（'' 是转义的单引号）。
	quotedDefaultPattern = regexp.MustCompile(`(?s)^N?'((?:[^']|'')*)'$`)
	// numberDefaultPattern 匹配数字常量（负数在目录里是 ((-1))，去掉括号后带符号）。
	numberDefaultPattern = regexp.MustCompile(`^[-+]?(\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?$`)
	// integerTypePattern 匹配能作 IDENTITY 的类型：整数类型与小数位为 0 的 decimal / numeric。
	integerTypePattern = regexp.MustCompile(`(?i)^\s*(tinyint|smallint|int|bigint|(decimal|numeric)\s*\(\s*\d+\s*(,\s*0\s*)?\))\s*$`)
)

// ClassifyDefault 按目录里默认约束的定义（INFORMATION_SCHEMA.COLUMNS.COLUMN_DEFAULT，同 sys.default_constraints.
// definition）判定已有列的默认值写法。定义外面总有一层括号（数字常量两层），先去掉整段外面的括号：没有默认值
// （IDENTITY 列也没有）是「无」；NULL 是 NULL；数字与字符串常量（去掉 N 前缀，成对的单引号还原成一个）是值；
// 其余——函数（getdate()、newid()）、运算、转换、十六进制常量——是表达式，原样写回。
func (dialect) ClassifyDefault(column sqldb.AlterableColumn) (dbdriver.DefaultKind, string) {
	if column.Row["COLUMN_DEFAULT"] == nil {
		return dbdriver.DefaultKindNone, ""
	}
	value := unwrapParens(column.DefaultValue)
	switch {
	case strings.EqualFold(value, "NULL"):
		return dbdriver.DefaultKindNull, ""
	case numberDefaultPattern.MatchString(value):
		return dbdriver.DefaultKindValue, value
	}
	if match := quotedDefaultPattern.FindStringSubmatch(value); match != nil {
		return dbdriver.DefaultKindValue, strings.ReplaceAll(match[1], "''", "'")
	}
	return dbdriver.DefaultKindExpression, value
}

// unwrapParens 去掉把整段包住的括号（可以多层），(0)+(1) 这样首尾的括号不是一对的不去。
func unwrapParens(text string) string {
	for {
		text = strings.TrimSpace(text)
		if len(text) < 2 || text[0] != '(' || closingParen(text) != len(text)-1 {
			return text
		}
		text = text[1 : len(text)-1]
	}
}

// closingParen 返回与 text 开头的左括号配对的右括号的位置；单引号字符串与方括号标识符里的括号不算。没有配对时
// 为 -1。
func closingParen(text string) int {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\'', '[':
			closing := byte('\'')
			if text[i] == '[' {
				closing = ']'
			}
			// 跳到配对的引号 / 右方括号；成对的（'' / ]]）是转义。
			for i++; i < len(text); i++ {
				if text[i] != closing {
					continue
				}
				if i+1 < len(text) && text[i+1] == closing {
					i++
					continue
				}
				break
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// explicitDefaultLiterals 是按写法给出的「值」的字面量：nchar / nvarchar / ntext 列的文本带 N 前缀（同行语句，
// 没有它库排序规则不是 Unicode 时中文会被写成 ?）。没给写法的默认值照旧用 sqldb.DefinitionLiterals 猜，语句与
// 改动前一致。
var explicitDefaultLiterals = sqldb.Literals{UnicodeText: literals.UnicodeText}

// definitionLiterals 是写这一列默认值的字面量规则，见 explicitDefaultLiterals。
func definitionLiterals(column dbdriver.ColumnDefinition) sqldb.Literals {
	if column.DefaultKind == "" {
		return sqldb.DefinitionLiterals
	}
	return explicitDefaultLiterals
}

// AutoIncrementSQL 是自增列的 IDENTITY(1,1)。只有整数类型（与小数位为 0 的 decimal / numeric）能作 IDENTITY；
// IDENTITY 列不能再有默认值（SQL Server 拒绝两者同时出现）。
func (dialect) AutoIncrementSQL(column dbdriver.ColumnDefinition) (string, error) {
	if !integerTypePattern.MatchString(column.Type) {
		return "", fmt.Errorf("column %q: an identity column needs an integer type (tinyint, smallint, int, bigint, or decimal / numeric with scale 0), not %q",
			column.Name, strings.TrimSpace(column.Type))
	}
	_, hasDefault, err := sqldb.DefaultSQL(sqldb.DefinitionLiterals, column)
	if err != nil {
		return "", err
	}
	if hasDefault {
		return "", fmt.Errorf("column %q: an identity column cannot also have a default value", column.Name)
	}
	return "IDENTITY(1,1)", nil
}

// --- 读索引 ---

// indexesSQL 读一张表的索引：每个索引的每一列一行，主键在最前，其余按名字；索引里键列按键序在前，INCLUDE 列在后
// （INCLUDE 列的 key_ordinal 是 0，只按 key_ordinal 排会排在键列前面）。另带上设计器写不回原样的东西：索引的种类
// （type：1 聚集、2 非聚集是 rowstore，其余是 XML、空间、列存储、哈希）、筛选条件、禁用、假设索引、内存优化表、
// 非默认的索引选项（填充因子、PAD_INDEX、IGNORE_DUP_KEY、行锁 / 页锁、统计信息不自动重算、压缩）与存储位置
// （分区方案、非默认文件组）。堆（i.name 为 NULL）不是索引。
const indexesSQL = "SELECT i.name AS index_name, i.type AS index_type, i.type_desc AS type_desc, i.is_primary_key AS is_primary, " +
	"i.is_unique_constraint AS is_unique_constraint, i.is_unique AS is_unique, i.has_filter AS has_filter, " +
	"COALESCE(i.filter_definition, '') AS filter_definition, i.is_disabled AS is_disabled, i.is_hypothetical AS is_hypothetical, " +
	"i.ignore_dup_key AS ignore_dup_key, i.fill_factor AS fill_factor, i.is_padded AS is_padded, " +
	"i.allow_row_locks AS allow_row_locks, i.allow_page_locks AS allow_page_locks, COALESCE(st.no_recompute, 0) AS no_recompute, " +
	"COALESCE((SELECT MAX(p.data_compression) FROM sys.partitions p WHERE p.object_id = i.object_id AND p.index_id = i.index_id), 0) AS data_compression, " +
	"COALESCE(ds.type, '') AS data_space_type, COALESCE(ds.name, '') AS data_space_name, COALESCE(ds.is_default, 1) AS data_space_default, " +
	"COALESCE(OBJECTPROPERTY(i.object_id, 'TableIsMemoryOptimized'), 0) AS memory_optimized, " +
	"COALESCE(c.name, '') AS column_name, COALESCE(ic.key_ordinal, 0) AS key_ordinal, " +
	"COALESCE(ic.is_included_column, 0) AS is_included, COALESCE(ic.is_descending_key, 0) AS is_descending " +
	"FROM sys.indexes i " +
	"JOIN sys.objects o ON o.object_id = i.object_id " +
	"JOIN sys.schemas s ON s.schema_id = o.schema_id " +
	"LEFT JOIN sys.data_spaces ds ON ds.data_space_id = i.data_space_id " +
	"LEFT JOIN sys.stats st ON st.object_id = i.object_id AND st.stats_id = i.index_id " +
	"LEFT JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id " +
	"LEFT JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id " +
	"WHERE s.name = @p1 AND o.name = @p2 AND i.name IS NOT NULL " +
	"ORDER BY i.is_primary_key DESC, i.name, ic.is_included_column, ic.key_ordinal, ic.index_column_id"

// sys.indexes.type 里设计器能写的两种 rowstore 索引，与占着聚集位置的聚集列存储。
const (
	indexTypeClustered            = 1
	indexTypeNonclustered         = 2
	indexTypeClusteredColumnstore = 5
	indexTypeColumnstore          = 6
)

// 设计器无法原样重建的已有索引的原因（给用户看）：只读，只能删除。
const (
	readOnlyKind         = "It is a %s index, which the designer cannot recreate."
	readOnlyMemory       = "It belongs to a memory-optimized table, whose indexes the designer cannot recreate."
	readOnlyHypothetical = "It is a hypothetical index (left by the Database Engine Tuning Advisor), which the designer cannot recreate."
	readOnlyDisabled     = "It is disabled, which the designer cannot represent (recreating it would enable it)."
	readOnlyInclude      = "It has INCLUDE columns (%s), which the designer cannot recreate."
	readOnlyFilter       = "It is a filtered index (WHERE %s), which the designer cannot recreate."
	readOnlyPartitioned  = "It is partitioned (on %s), which the designer cannot recreate."
	readOnlyFilegroup    = "It is stored on the filegroup %s rather than the default one, which the designer cannot recreate."
	readOnlyOptions      = "It has non-default index options (%s), which the designer cannot recreate."
)

// msIndex 是 indexesSQL 读出的一个索引。
type msIndex struct {
	name, typeDesc, filter                  string
	indexType                               int
	primary, unique                         bool
	disabled, hypothetical, memoryOptimized bool
	dataSpaceType, dataSpaceName            string
	defaultDataSpace                        bool
	// options 是非默认的索引选项（写成 WITH 子句里的样子）。
	options []string
	keys    []dbdriver.IndexColumn
	include []string
}

// readIndexes 用 indexesSQL 读出表的索引，按查询的次序。
func readIndexes(ctx context.Context, conn *sql.Conn, schema, table string) ([]msIndex, error) {
	records, err := sqldb.QueryRecords(ctx, conn, indexesSQL, sql.Named("p1", schema), sql.Named("p2", table))
	if err != nil {
		return nil, err
	}
	var indexes []msIndex
	position := map[string]int{}
	for _, row := range records {
		name := sqldb.PickString(row, "index_name")
		i, seen := position[name]
		if !seen {
			index, err := newMSIndex(row)
			if err != nil {
				return nil, fmt.Errorf("index %q: %w", name, err)
			}
			i = len(indexes)
			position[name] = i
			indexes = append(indexes, index)
		}
		index := &indexes[i]
		column := sqldb.PickString(row, "column_name")
		if column == "" {
			continue
		}
		keyOrdinal, err := number(row, "key_ordinal")
		if err != nil {
			return nil, fmt.Errorf("index %q: %w", name, err)
		}
		switch {
		case keyOrdinal > 0:
			index.keys = append(index.keys, dbdriver.IndexColumn{Name: column, Descending: flag(row, "is_descending")})
		case index.columnstore():
			// 列存储没有键列：它的列都列成列（目录里记作 INCLUDE 列）。
			index.keys = append(index.keys, dbdriver.IndexColumn{Name: column})
		case flag(row, "is_included"):
			index.include = append(index.include, column)
		}
		// 其余是分区列（不在键里也不是 INCLUDE 列），不是索引定义的一部分。
	}
	return indexes, nil
}

// newMSIndex 取一个索引自己的属性（索引的每一行都一样）。
func newMSIndex(row map[string]any) (msIndex, error) {
	indexType, err := number(row, "index_type")
	if err != nil {
		return msIndex{}, err
	}
	fillFactor, err := number(row, "fill_factor")
	if err != nil {
		return msIndex{}, err
	}
	compression, err := number(row, "data_compression")
	if err != nil {
		return msIndex{}, err
	}
	index := msIndex{
		name:             sqldb.PickString(row, "index_name"),
		typeDesc:         sqldb.PickString(row, "type_desc"),
		indexType:        indexType,
		primary:          flag(row, "is_primary"),
		unique:           flag(row, "is_unique"),
		disabled:         flag(row, "is_disabled"),
		hypothetical:     flag(row, "is_hypothetical"),
		memoryOptimized:  flag(row, "memory_optimized"),
		dataSpaceType:    strings.TrimSpace(sqldb.PickString(row, "data_space_type")),
		dataSpaceName:    sqldb.PickString(row, "data_space_name"),
		defaultDataSpace: flag(row, "data_space_default"),
	}
	if flag(row, "has_filter") {
		index.filter = sqldb.PickString(row, "filter_definition")
	}
	// 填充因子 0 与 100 都是缺省（填满）。
	if fillFactor != 0 && fillFactor != 100 {
		index.options = append(index.options, fmt.Sprintf("FILLFACTOR = %d", fillFactor))
	}
	for _, option := range []struct {
		set  bool
		text string
	}{
		{flag(row, "is_padded"), "PAD_INDEX = ON"},
		{flag(row, "ignore_dup_key"), "IGNORE_DUP_KEY = ON"},
		{!flag(row, "allow_row_locks"), "ALLOW_ROW_LOCKS = OFF"},
		{!flag(row, "allow_page_locks"), "ALLOW_PAGE_LOCKS = OFF"},
		{flag(row, "no_recompute"), "STATISTICS_NORECOMPUTE = ON"},
	} {
		if option.set {
			index.options = append(index.options, option.text)
		}
	}
	// rowstore 的压缩：1 行压缩、2 页压缩（列存储的 3 / 4 是它本身的存储，列存储整个只读）。
	if !index.columnstore() {
		switch compression {
		case 1:
			index.options = append(index.options, "DATA_COMPRESSION = ROW")
		case 2:
			index.options = append(index.options, "DATA_COMPRESSION = PAGE")
		}
	}
	return index, nil
}

// flag 取一行里的 bit 列（驱动给 bool，COALESCE 过的给整数）。
func flag(row map[string]any, key string) bool {
	value := sqldb.PickString(row, key)
	return value == "1" || strings.EqualFold(value, "true")
}

// number 取一行里的整数列。
func number(row map[string]any, key string) (int, error) {
	value, err := strconv.Atoi(sqldb.PickString(row, key))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func (i msIndex) columnstore() bool {
	return i.indexType == indexTypeClusteredColumnstore || i.indexType == indexTypeColumnstore
}

// clustered 报告索引占着表上唯一的聚集位置（聚集 rowstore 与聚集列存储）。
func (i msIndex) clustered() bool {
	return i.indexType == indexTypeClustered || i.indexType == indexTypeClusteredColumnstore
}

// meta 是对象浏览里的索引：键列（按键序）与 INCLUDE 列分开。
func (i msIndex) meta() dbdriver.IndexMeta {
	columns := make([]string, len(i.keys))
	for k, key := range i.keys {
		columns[k] = key.Name
	}
	return dbdriver.IndexMeta{Name: i.name, Columns: columns, Unique: i.unique, Include: i.include}
}

// indexDefinition 是改表模型里的索引：类型、键列、INCLUDE 列与聚集选项；设计器写不回原样的带只读原因（有几处时
// 给第一处）。
func (i msIndex) indexDefinition() dbdriver.IndexDefinition {
	index := dbdriver.IndexDefinition{
		Name:    i.name,
		Type:    dbdriver.IndexTypeNormal,
		Columns: i.keys,
		Include: i.include,
		Options: map[string]any{clusteredOption: i.clustered()},
	}
	switch {
	case i.primary:
		index.Type = dbdriver.IndexTypePrimary
	case i.unique:
		index.Type = dbdriver.IndexTypeUnique
	}
	switch {
	case i.indexType != indexTypeClustered && i.indexType != indexTypeNonclustered:
		index.ReadOnlyReason = fmt.Sprintf(readOnlyKind, i.typeDesc)
	case i.memoryOptimized:
		index.ReadOnlyReason = readOnlyMemory
	case i.hypothetical:
		index.ReadOnlyReason = readOnlyHypothetical
	case i.disabled:
		index.ReadOnlyReason = readOnlyDisabled
	case len(i.include) > 0:
		index.ReadOnlyReason = fmt.Sprintf(readOnlyInclude, strings.Join(i.include, ", "))
	case i.filter != "":
		index.ReadOnlyReason = fmt.Sprintf(readOnlyFilter, i.filter)
	case i.dataSpaceType == "PS":
		index.ReadOnlyReason = fmt.Sprintf(readOnlyPartitioned, i.dataSpaceName)
	case i.dataSpaceType != "" && !i.defaultDataSpace:
		index.ReadOnlyReason = fmt.Sprintf(readOnlyFilegroup, i.dataSpaceName)
	case len(i.options) > 0:
		index.ReadOnlyReason = fmt.Sprintf(readOnlyOptions, strings.Join(i.options, ", "))
	}
	return index
}

// --- 写索引 ---

func (dialect) IndexEditing() *dbdriver.IndexEditing { return indexEditing }

// IndexSQL 是建一个索引的语句：普通 / 唯一索引是 CREATE [UNIQUE] [CLUSTERED | NONCLUSTERED] INDEX，主键是
// ALTER TABLE … ADD [CONSTRAINT 名] PRIMARY KEY。
func (dialect) IndexSQL(tableRef string, index dbdriver.IndexDefinition) (string, error) {
	return createIndexSQL(tableRef, index, false)
}

// DropIndexSQL 是删一个普通索引的 DROP INDEX 名 ON 表。支撑主键 / 唯一约束的索引要随约束删（ALTER TABLE …
// DROP CONSTRAINT）：只凭名字分不出，改表时由 AlterTableSQL 查过约束再选写法（alterIndexes）。
func (dialect) DropIndexSQL(tableRef, name string) (string, error) {
	return dropIndexSQL(tableRef, name, false), nil
}

// dropIndexSQL 删一个索引；constraint 表示它支撑一个约束，连约束一起删（约束与它的索引同名）。
func dropIndexSQL(tableRef, name string, constraint bool) string {
	if constraint {
		return "ALTER TABLE " + tableRef + " DROP CONSTRAINT " + quoteIdent(name)
	}
	return "DROP INDEX " + quoteIdent(name) + " ON " + tableRef
}

// clusteredKeyword 是索引的聚集选项写成的关键字（前面带空格）：给了就写 CLUSTERED / NONCLUSTERED，没给不写，
// 照 SQL Server 的缺省。
func clusteredKeyword(index dbdriver.IndexDefinition) string {
	on, given := index.Options[clusteredOption].(bool)
	switch {
	case !given:
		return ""
	case on:
		return " CLUSTERED"
	}
	return " NONCLUSTERED"
}

// clusteredOn 报告索引打开了聚集选项。
func clusteredOn(index dbdriver.IndexDefinition) bool {
	on, _ := index.Options[clusteredOption].(bool)
	return on
}

// createIndexSQL 建一个索引。主键写成 ALTER TABLE … ADD [CONSTRAINT 名] PRIMARY KEY；uniqueConstraint 表示把唯一
// 索引写成唯一约束（ADD CONSTRAINT … UNIQUE，重建原本支撑唯一约束的索引时用）；其余是 CREATE [UNIQUE]
// [CLUSTERED | NONCLUSTERED] INDEX 名 ON 表 (列 [DESC], …)——SQL Server 的 CREATE INDEX 必须有名字。
func createIndexSQL(tableRef string, index dbdriver.IndexDefinition, uniqueConstraint bool) (string, error) {
	switch {
	case index.Type == dbdriver.IndexTypePrimary:
		clause, err := constraintClause(index, "PRIMARY KEY", clusteredKeyword(index))
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + tableRef + " ADD " + clause, nil
	case uniqueConstraint:
		clause, err := constraintClause(index, "UNIQUE", clusteredKeyword(index))
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + tableRef + " ADD " + clause, nil
	}
	name := strings.TrimSpace(index.Name)
	if name == "" {
		return "", fmt.Errorf("an index on %s has no name; SQL Server needs one", tableRef)
	}
	columns, err := indexColumnList(index)
	if err != nil {
		return "", err
	}
	statement := "CREATE"
	if index.Type == dbdriver.IndexTypeUnique {
		statement += " UNIQUE"
	}
	return statement + clusteredKeyword(index) + " INDEX " + quoteIdent(name) + " ON " + tableRef + " (" + columns + ")", nil
}

// constraintClause 是主键 / 唯一约束的 [CONSTRAINT 名] 种类 [CLUSTERED | NONCLUSTERED] (列 [DESC], …)：建表时写在
// 表定义里，改表时跟在 ADD 后面。没有名字时由 SQL Server 取名。
func constraintClause(index dbdriver.IndexDefinition, kind, clustered string) (string, error) {
	columns, err := indexColumnList(index)
	if err != nil {
		return "", err
	}
	clause := kind + clustered + " (" + columns + ")"
	if name := strings.TrimSpace(index.Name); name != "" {
		clause = "CONSTRAINT " + quoteIdent(name) + " " + clause
	}
	return clause, nil
}

// indexColumnList 是索引的列：列名加方括号，降序带 DESC。
func indexColumnList(index dbdriver.IndexDefinition) (string, error) {
	if len(index.Columns) == 0 {
		return "", fmt.Errorf("index %q has no columns", index.Name)
	}
	parts := make([]string, len(index.Columns))
	for i, column := range index.Columns {
		parts[i] = quoteIdent(strings.TrimSpace(column.Name))
		if column.Descending {
			parts[i] += " DESC"
		}
	}
	return strings.Join(parts, ", "), nil
}

// indexConstraintsSQL 读表上的主键与唯一约束：名字（就是它的索引的名字）与种类（PK / UQ）。
const indexConstraintsSQL = "SELECT kc.name, kc.type FROM sys.key_constraints kc " +
	"JOIN sys.objects o ON o.object_id = kc.parent_object_id JOIN sys.schemas s ON s.schema_id = o.schema_id " +
	"WHERE s.name = @p1 AND o.name = @p2"

// sys.key_constraints.type 里的唯一约束（主键是 PK）。
const constraintUnique = "UQ"

// alterIndexes 写改表里索引的两半（见 sqldb.IndexDropStatements / IndexCreateStatements 的次序约定）：drops 写在
// 列的改动之前，creates 写在之后。与共用写法的不同：
//   - 现有索引支撑约束时随约束删（ALTER TABLE … DROP CONSTRAINT）——要先查这张表的约束；
//   - 重建原本支撑唯一约束、改完仍是唯一的，写回唯一约束（ADD CONSTRAINT … UNIQUE），不悄悄变成唯一索引；
//   - 打开聚集的索引先建：之后建的非聚集索引不必随它重建，没给聚集选项的新主键也不会抢先占了聚集位置。
//
// 其余按请求的次序。
func alterIndexes(ctx context.Context, conn *sql.Conn, change sqldb.TableChange) (drops, creates []string, err error) {
	if len(change.IndexDrops) == 0 && len(change.Indexes) == 0 {
		return nil, nil, nil
	}
	target := change.TargetRef
	constraints := map[string]string{}
	if len(change.IndexDrops) > 0 || slices.ContainsFunc(change.Indexes, func(c sqldb.IndexChange) bool { return !c.IsNew() }) {
		// 语句还没执行，约束按改名之前的表名查。
		schema, table := splitSchemaTable(change.Table)
		records, err := sqldb.QueryRecords(ctx, conn, indexConstraintsSQL, sql.Named("p1", schema), sql.Named("p2", table))
		if err != nil {
			return nil, nil, fmt.Errorf("index constraints: %w", err)
		}
		for _, record := range records {
			constraints[sqldb.PickString(record, "name")] = strings.TrimSpace(sqldb.PickString(record, "type"))
		}
	}

	for _, dropped := range change.IndexDrops {
		drops = append(drops, dropIndexSQL(target, dropped.Name, constraints[dropped.Name] != ""))
	}
	var clusteredBuilds, builds []string
	for _, changed := range change.Indexes {
		uniqueConstraint := false
		if !changed.IsNew() {
			drops = append(drops, dropIndexSQL(target, changed.Current.Name, constraints[changed.Current.Name] != ""))
			uniqueConstraint = constraints[changed.Current.Name] == constraintUnique && changed.Index.Type == dbdriver.IndexTypeUnique
		}
		statement, err := createIndexSQL(target, changed.Index, uniqueConstraint)
		if err != nil {
			return nil, nil, err
		}
		if clusteredOn(changed.Index) {
			clusteredBuilds = append(clusteredBuilds, statement)
		} else {
			builds = append(builds, statement)
		}
	}
	return drops, append(clusteredBuilds, builds...), nil
}

// --- 建表 ---

// CreateTableSQL 写整条建表：CREATE TABLE（列定义，IDENTITY 与按写法的默认值见 sqldb.NewColumnSQL；主键写在表定义
// 里），之后建其余索引（打开聚集的先建，其余按请求的次序）。一张表至多一个聚集索引由共用会话按声明的独占选项
// 先行拒绝。没给聚集选项的主键在另有聚集索引时写成 NONCLUSTERED——否则 SQL Server 把它建成聚集，之后的聚集
// 索引建不成。SQL Server 的注释在扩展属性里，建表时不写：带注释的请求以 ErrUnsupported 拒绝（同共用的写法）。
// 没有用到索引与新写法的请求与共用的写法逐字一致。
func (d dialect) CreateTableSQL(tables sqldb.Tables, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	if strings.TrimSpace(req.Comment) != "" {
		return nil, fmt.Errorf("%w: this driver does not write a table comment when creating a table", dbdriver.ErrUnsupported)
	}
	for _, column := range req.Columns {
		if strings.TrimSpace(column.Comment) != "" {
			return nil, fmt.Errorf("%w: this driver does not write column comments when creating a table (column %q)", dbdriver.ErrUnsupported, column.Name)
		}
	}
	tableRef := tables.TableRef(database, req.Name)
	anotherClustered := slices.ContainsFunc(req.Indexes, func(index dbdriver.IndexDefinition) bool {
		return index.Type != dbdriver.IndexTypePrimary && clusteredOn(index)
	})

	columns := req.Definitions()
	defs := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		def, err := sqldb.NewColumnSQL(d, definitionLiterals(column), tables.QuoteIdent, column)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	var clusteredBuilds, builds []string
	for _, index := range req.Indexes {
		if index.Type == dbdriver.IndexTypePrimary {
			keyword := clusteredKeyword(index)
			if keyword == "" && anotherClustered {
				keyword = " NONCLUSTERED"
			}
			clause, err := constraintClause(index, "PRIMARY KEY", keyword)
			if err != nil {
				return nil, err
			}
			defs = append(defs, clause)
			continue
		}
		statement, err := createIndexSQL(tableRef, index, false)
		if err != nil {
			return nil, err
		}
		if clusteredOn(index) {
			clusteredBuilds = append(clusteredBuilds, statement)
		} else {
			builds = append(builds, statement)
		}
	}
	statements := []string{fmt.Sprintf("CREATE TABLE %s (\n  %s\n)", tableRef, strings.Join(defs, ",\n  "))}
	statements = append(statements, clusteredBuilds...)
	return append(statements, builds...), nil
}
