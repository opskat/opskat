package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 表设计器：常用类型、默认值的四种写法、新表的自增（INTEGER PRIMARY KEY）与索引编辑。声明与实现同源——描述的
// ExplicitDefaults / AutoIncrement / AutoIncrementNewTablesOnly / IndexEditing 取自这里实现的 sqldb 方言接口。
// SQLite 的 DDL 可以在事务里执行，描述声明 TransactionalDDL：保存时整批语句原子执行，失败则表结构不变。
//
// SQLite 没有注释；主键只能在建表时定（ALTER TABLE 改不了主键），所以主键不可改。建表由方言整条写
// （TableCreator）：自增列就是写在列上的 INTEGER PRIMARY KEY，此时主键不能再写成表约束。
var (
	_ sqldb.DefaultClassifier       = dialect{}
	_ sqldb.NewTableAutoIncrementer = dialect{}
	_ sqldb.TableIndexer            = dialect{}
	_ sqldb.TableCreator            = dialect{}
)

// columnTypes 是类型下拉里的常用类型；分组名是宿主多语言的键。SQLite 的类型只决定列的亲和性（INTEGER、REAL、
// NUMERIC、TEXT、BLOB），其余是大家常写的声明类型，按亲和性规则落到这五种上。
var columnTypes = &dbdriver.ColumnTypes{
	Groups: []dbdriver.ColumnTypeGroup{
		{Label: "query.designer.typeGroup.numeric", Types: []string{"INTEGER", "REAL", "NUMERIC"}},
		{Label: "query.designer.typeGroup.string", Types: []string{"TEXT", "VARCHAR(255)"}},
		{Label: "query.designer.typeGroup.datetime", Types: []string{"DATETIME", "DATE"}},
		{Label: "query.designer.typeGroup.binary", Types: []string{"BLOB"}},
		{Label: "query.designer.typeGroup.json", Types: []string{"JSON"}},
		{Label: "query.designer.typeGroup.other", Types: []string{"BOOLEAN"}},
	},
	NewColumn: "TEXT",
	// 只有声明类型正好是 INTEGER 的主键是 rowid 的别名、能自增（INT、BIGINT 都不行）。
	PrimaryKey: "INTEGER",
}

// indexEditing 是 SQLite 的索引编辑声明：普通 / 唯一，主键只读；降序，没有前缀长度、注释与选项；索引名与表、
// 视图、触发器同在库（main / 附加库）的名字空间里，范围是库。
var indexEditing = &dbdriver.IndexEditing{
	Types: []dbdriver.IndexTypeDecl{
		{ID: dbdriver.IndexTypeNormal, Label: "query.designer.indexType.normal"},
		{ID: dbdriver.IndexTypeUnique, Label: "query.designer.indexType.unique"},
		{ID: dbdriver.IndexTypePrimary, Label: "query.designer.indexType.primary"},
	},
	Descending: true,
	NameScope:  dbdriver.IndexScopeDatabase,
}

// rowidPrimaryKeyName 是 rowid 主键（INTEGER PRIMARY KEY，没有索引）在改表模型里的名字。用 sqlite_ 开头的保留名，
// 不会与表上真的索引重名（用户建不了 sqlite_ 开头的对象）。
const rowidPrimaryKeyName = "sqlite_rowid"

// --- 列：默认值写法与自增 ---

var (
	// quotedDefaultPattern 匹配字符串常量；第一组是引号里的原文（'' 是转义的单引号）。
	quotedDefaultPattern = regexp.MustCompile(`(?s)^'((?:[^']|'')*)'$`)
	// numberDefaultPattern 匹配数字常量（可带符号）。
	numberDefaultPattern = regexp.MustCompile(`^[-+]?(\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?$`)
	// constantPattern 匹配 ADD COLUMN 接受的常量默认值（去掉外层括号之后）：数字（含十六进制）、字符串、blob、
	// NULL、TRUE / FALSE。其余（CURRENT_TIMESTAMP、函数、运算）SQLite 只在空表上接受。
	constantPattern = regexp.MustCompile(`(?is)^([-+]?\s*((\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?|0x[0-9a-f]+)|'(?:[^']|'')*'|x'[0-9a-f]*'|null|true|false)$`)
)

// ClassifyDefault 按 pragma_table_info 的 dflt_value（DEFAULT 后面写的原文）判定已有列的默认值写法：没有默认值
// 是「无」；NULL 是 NULL；字符串常量（成对的单引号还原成一个）与数字是值；其余——CURRENT_TIMESTAMP、括号里的
// 表达式、TRUE / FALSE、blob——是表达式，原样写回（TRUE 写成值会变成字符串 'TRUE'）。值写回时写成加引号的
// 字符串（sqldb.DefinitionLiterals），SQLite 按列的亲和性转换。
func (dialect) ClassifyDefault(column sqldb.AlterableColumn) (dbdriver.DefaultKind, string) {
	if column.Row["dflt_value"] == nil {
		return dbdriver.DefaultKindNone, ""
	}
	value := strings.TrimSpace(column.DefaultValue)
	switch {
	case strings.EqualFold(value, "NULL"):
		return dbdriver.DefaultKindNull, ""
	case numberDefaultPattern.MatchString(value):
		return dbdriver.DefaultKindValue, value
	}
	if match := quotedDefaultPattern.FindStringSubmatch(value); match != nil {
		return dbdriver.DefaultKindValue, strings.ReplaceAll(match[1], "''", "'")
	}
	return dbdriver.DefaultKindExpression, column.DefaultValue
}

// AutoIncrementSQL 是自增列写在类型之后的 PRIMARY KEY：SQLite 的自增就是 rowid——声明类型正好是 INTEGER 的单列
// 主键是 rowid 的别名，插入时不给值就取最大值加一。不写 AUTOINCREMENT：它只多保证删掉的最大 id 不再复用，代价
// 是每次插入都要维护 sqlite_sequence（SQLite 文档不建议），而且读结构时任何 rowid 主键都显示为自增，写出的与
// 读回的才一致。rowid 列不能有默认值。这一列是否就是表的主键由 CreateTableSQL 检查；改表时新增的列不能自增
// （AutoIncrementsNewTablesOnly）。
func (dialect) AutoIncrementSQL(column dbdriver.ColumnDefinition) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(column.Type), "INTEGER") {
		return "", fmt.Errorf("column %q: SQLite only auto-increments an INTEGER PRIMARY KEY column; the type must be exactly INTEGER, not %q",
			column.Name, strings.TrimSpace(column.Type))
	}
	_, hasDefault, err := sqldb.DefaultSQL(sqldb.DefinitionLiterals, column)
	if err != nil {
		return "", err
	}
	if hasDefault {
		return "", fmt.Errorf("column %q: an auto-increment column cannot also have a default value", column.Name)
	}
	return "PRIMARY KEY", nil
}

// AutoIncrementsNewTablesOnly：ADD COLUMN 加不出主键列，自增只能在新表上设。
func (dialect) AutoIncrementsNewTablesOnly() bool { return true }

// currentTimePattern 匹配 DEFAULT 后面可以不加括号的时间关键字。
var currentTimePattern = regexp.MustCompile(`(?i)^current_(time|date|timestamp)$`)

// withDefaultExpression 给按写法给出的表达式默认值加上括号：SQLite 的 DEFAULT 后面只接受常量、CURRENT_TIME /
// CURRENT_DATE / CURRENT_TIMESTAMP 与括号括起来的表达式（DEFAULT lower('a') 是语法错误）。已经整段加了括号的、
// 常量与时间关键字原样。读结构时 SQLite 给的是括号里面的部分，所以这里加的括号不会在比较时算作改动。
func withDefaultExpression(column dbdriver.ColumnDefinition) dbdriver.ColumnDefinition {
	if column.DefaultKind != dbdriver.DefaultKindExpression {
		return column
	}
	value := strings.TrimSpace(column.DefaultValue)
	if unwrapParens(value) != value || constantPattern.MatchString(value) || currentTimePattern.MatchString(value) {
		return column
	}
	column.DefaultValue = "(" + value + ")"
	return column
}

// checkAddColumnDefault 拒绝 ADD COLUMN 写不进去的默认值：表里有行时 SQLite 只接受常量默认值（报 Cannot add a
// column with non-constant default），空表不限。只查按写法给的表达式——值与 NULL 都是常量；没给写法的照旧按
// 文本猜，语句与改动前一致。tableRef 是改表之前的表引用（语句还没执行）。
func checkAddColumnDefault(ctx context.Context, conn *sql.Conn, tableRef string, column dbdriver.ColumnDefinition) error {
	if column.DefaultKind != dbdriver.DefaultKindExpression || constantPattern.MatchString(unwrapParens(column.DefaultValue)) {
		return nil
	}
	var one int
	err := conn.QueryRowContext(ctx, "SELECT 1 FROM "+tableRef+" LIMIT 1").Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("check whether table %s has rows: %w", tableRef, err)
	}
	return fmt.Errorf("column %q: SQLite cannot add a column whose default (%s) is not a constant to a table that has rows; use a value default instead",
		column.Name, strings.TrimSpace(column.DefaultValue))
}

// unwrapParens 去掉把整段包住的括号（可以多层）；(1)+(2) 这样首尾的括号不是一对的不去。
func unwrapParens(text string) string {
	for {
		text = strings.TrimSpace(text)
		if len(text) < 2 || text[0] != '(' {
			return text
		}
		items, rest, ok := parenthesized(text)
		if !ok || strings.TrimSpace(rest) != "" || len(items) != 1 {
			return text
		}
		text = items[0]
	}
}

// --- 读索引 ---

// 设计器无法原样重建的已有索引的原因（给用户看）：只读，只能删除。
const (
	readOnlyExpression = "It has an expression key, which the designer cannot recreate."
	readOnlyPartial    = "It is a partial index (WHERE %s), which the designer cannot recreate."
	readOnlyCollation  = "A key column names a collation (COLLATE), which the designer cannot recreate."
	readOnlyUnreadable = "Its CREATE INDEX statement could not be read completely, so the designer cannot recreate it."
)

// pragma_index_list.origin：c 是 CREATE INDEX 建的，u 是 UNIQUE 约束、pk 是 PRIMARY KEY 约束自动建的
// （sqlite_autoindex_*，不能单独删除）。
const (
	originUnique     = "u"
	originPrimaryKey = "pk"
)

// sqliteIndex 是读出的一个索引。
type sqliteIndex struct {
	name, origin string
	unique       bool
	// predicate 是部分索引的 WHERE 条件，collation 报告有键列写了 COLLATE：都只有 CREATE INDEX 建的索引才有。
	predicate string
	collation bool
	// unreadable 报告建它的语句读不全（解析失败或键数与 pragma 对不上）：键列只按 pragma 给出，只读。
	unreadable bool
	keys       []dbdriver.IndexColumn
}

// indexListSQL 读表上的索引与建它的语句（自动建的索引没有语句）。
func indexListSQL(database string) string {
	return `SELECT il.name, il."unique", il.origin, il.partial, COALESCE(m.sql, '') AS sql FROM ` +
		qualify(database, "pragma_index_list(?)") + ` il LEFT JOIN ` + qualify(database, "sqlite_master") +
		` m ON m.type = 'index' AND m.name = il.name`
}

// indexKeysSQL 读一个索引的键列：cid 为 -2 是表达式键（没有列名），desc 为 1 是降序；key 为 0 的是索引附带的
// rowid 等辅助列，不是键。
func indexKeysSQL(database string) string {
	return `SELECT cid, name, "desc" FROM ` + qualify(database, "pragma_index_xinfo(?)") + ` WHERE key = 1 ORDER BY seqno`
}

// readIndexes 读出表的索引，按 pragma_index_list 的次序。表达式键的原文与部分索引的条件 pragma 里没有，取自
// sqlite_master 里建索引的语句。
func readIndexes(ctx context.Context, conn *sql.Conn, database, table string) ([]sqliteIndex, error) {
	records, err := sqldb.QueryRecords(ctx, conn, indexListSQL(database), table)
	if err != nil {
		return nil, err
	}
	indexes := make([]sqliteIndex, 0, len(records))
	for _, row := range records {
		index := sqliteIndex{
			name:   sqldb.PickString(row, "name"),
			origin: sqldb.PickString(row, "origin"),
			unique: sqldb.PickString(row, "unique") == "1",
		}
		keys, err := sqldb.QueryRecords(ctx, conn, indexKeysSQL(database), index.name)
		if err != nil {
			return nil, fmt.Errorf("index %q: %w", index.name, err)
		}
		var definition indexStatement
		if text := sqldb.PickString(row, "sql"); text != "" {
			// 语句读不全不让整张表读不出来：按 pragma 给出键列，标为只读（设计器无法原样重建它）。
			if parsed, err := parseIndexStatement(text); err == nil && len(parsed.keys) == len(keys) {
				definition = parsed
			} else {
				index.unreadable = true
			}
		}
		if sqldb.PickString(row, "partial") == "1" {
			index.predicate = definition.predicate
		}
		for i, key := range keys {
			column := dbdriver.IndexColumn{Name: sqldb.PickString(key, "name"), Descending: sqldb.PickString(key, "desc") == "1"}
			// 表达式键（cid -2）的原文只在语句里；语句读不全时只知道它是表达式。
			column.Expression = sqldb.PickString(key, "cid") == "-2"
			if definition.keys != nil {
				if column.Expression {
					column.Name = definition.keys[i].expression
				}
				index.collation = index.collation || definition.keys[i].collation
			}
			index.keys = append(index.keys, column)
		}
		indexes = append(indexes, index)
	}
	return indexes, nil
}

// meta 是对象浏览里的索引：键列（表达式键是表达式原文）。
func (i sqliteIndex) meta() dbdriver.IndexMeta {
	columns := make([]string, len(i.keys))
	for k, key := range i.keys {
		columns[k] = key.Name
	}
	return dbdriver.IndexMeta{Name: i.name, Columns: columns, Unique: i.unique}
}

// indexDefinition 是改表模型里的索引：约束自动建的不能删；设计器写不回原样的带只读原因（有几处时给第一处）。
func (i sqliteIndex) indexDefinition() dbdriver.IndexDefinition {
	index := dbdriver.IndexDefinition{Name: i.name, Type: dbdriver.IndexTypeNormal, Columns: i.keys}
	switch {
	case i.origin == originPrimaryKey:
		index.Type = dbdriver.IndexTypePrimary
	case i.unique:
		index.Type = dbdriver.IndexTypeUnique
	}
	index.Undroppable = i.origin == originPrimaryKey || i.origin == originUnique
	switch {
	case i.unreadable:
		index.ReadOnlyReason = readOnlyUnreadable
	case slices.ContainsFunc(i.keys, func(c dbdriver.IndexColumn) bool { return c.Expression }):
		index.ReadOnlyReason = readOnlyExpression
	case i.predicate != "":
		index.ReadOnlyReason = fmt.Sprintf(readOnlyPartial, i.predicate)
	case i.collation:
		index.ReadOnlyReason = readOnlyCollation
	}
	return index
}

// tableIndexes 是改表模型里表的索引：主键在最前，其余按名字。主键是 PRIMARY KEY 约束自动建的索引；rowid 主键
// （INTEGER PRIMARY KEY）没有索引，按主键列给出一项（rowidPrimaryKeyName）。主键都不能删、不能改。primaryKeys
// 是按次序的主键列。
func tableIndexes(indexes []sqliteIndex, primaryKeys []string) []dbdriver.IndexDefinition {
	out := make([]dbdriver.IndexDefinition, 0, len(indexes)+1)
	if len(primaryKeys) > 0 && !slices.ContainsFunc(indexes, func(i sqliteIndex) bool { return i.origin == originPrimaryKey }) {
		columns := make([]dbdriver.IndexColumn, len(primaryKeys))
		for i, name := range primaryKeys {
			columns[i] = dbdriver.IndexColumn{Name: name}
		}
		out = append(out, dbdriver.IndexDefinition{Name: rowidPrimaryKeyName, Type: dbdriver.IndexTypePrimary, Columns: columns, Undroppable: true})
	}
	for _, index := range indexes {
		out = append(out, index.indexDefinition())
	}
	slices.SortStableFunc(out, func(a, b dbdriver.IndexDefinition) int {
		switch {
		case a.Type == dbdriver.IndexTypePrimary && b.Type != dbdriver.IndexTypePrimary:
			return -1
		case b.Type == dbdriver.IndexTypePrimary && a.Type != dbdriver.IndexTypePrimary:
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// rowidAlias 报告表的主键是 rowid 的别名（自增）：单列主键，且没有为它自动建的索引——rowid 表上不是 rowid 别名的
// 主键（类型不是 INTEGER、INTEGER PRIMARY KEY DESC）与 WITHOUT ROWID 表的主键都有。
func rowidAlias(indexes []sqliteIndex, primaryKeys []string) bool {
	return len(primaryKeys) == 1 && !slices.ContainsFunc(indexes, func(i sqliteIndex) bool { return i.origin == originPrimaryKey })
}

// takenIndexNamesSQL 读库里这张表的索引之外的全部对象名：索引名与表、视图、触发器、其他表的索引同在库的名字
// 空间里，与其中任何一个重名都建不成。
func takenIndexNamesSQL(database string) string {
	return "SELECT name FROM " + qualify(database, "sqlite_master") + " WHERE NOT (type = 'index' AND tbl_name = ?) ORDER BY name"
}

// --- 解析建索引的语句 ---

// indexStatement 是从 CREATE INDEX 语句里取出的、pragma 里没有的东西。
type indexStatement struct {
	keys      []indexKeyText
	predicate string
}

// indexKeyText 是一个键列的原文：表达式（去掉排序规则与升降序），以及它写没写 COLLATE。
type indexKeyText struct {
	expression string
	collation  bool
}

// parseIndexStatement 解析 sqlite_master 里的 CREATE INDEX 语句：第一个不在引号里的左括号起是键列的列表（索引名、
// 表名里的括号都在引号里），右括号之后的 WHERE 是部分索引的条件。
func parseIndexStatement(statement string) (indexStatement, error) {
	start := -1
	for i := 0; i < len(statement); i++ {
		if end, quoted := skipQuoted(statement, i); quoted {
			i = end
			continue
		}
		if statement[i] == '(' {
			start = i
			break
		}
	}
	if start < 0 {
		return indexStatement{}, fmt.Errorf("no column list in %q", statement)
	}
	items, rest, ok := parenthesized(statement[start:])
	if !ok {
		return indexStatement{}, fmt.Errorf("unbalanced column list in %q", statement)
	}
	out := indexStatement{keys: make([]indexKeyText, len(items))}
	for i, item := range items {
		out.keys[i] = keyText(item)
	}
	if words := tokens(rest); len(words) > 0 && strings.EqualFold(words[0].text, "WHERE") {
		out.predicate = strings.TrimSpace(rest[words[0].end:])
	}
	return out, nil
}

// keyText 去掉键列末尾的 ASC / DESC 与 COLLATE 名字，报告写没写 COLLATE。
func keyText(item string) indexKeyText {
	words := tokens(item)
	end, collation := len(item), false
	if n := len(words); n > 0 && (strings.EqualFold(words[n-1].text, "ASC") || strings.EqualFold(words[n-1].text, "DESC")) {
		end = words[n-1].start
		words = words[:n-1]
	}
	if n := len(words); n > 1 && strings.EqualFold(words[n-2].text, "COLLATE") {
		end, collation = words[n-2].start, true
	}
	return indexKeyText{expression: strings.TrimSpace(item[:end]), collation: collation}
}

// token 是一段文本里括号外的一个词（标识符、关键字或引号括起来的一整段）的位置。
type token struct {
	text       string
	start, end int
}

// tokens 把 text 里括号外的部分按空白与标点切成词；引号里的与括号里的整段是一个词。
func tokens(text string) []token {
	var out []token
	for i := 0; i < len(text); {
		switch text[i] {
		case ' ', '\t', '\n', '\r':
			i++
			continue
		case '(':
			_, rest, ok := parenthesized(text[i:])
			end := len(text)
			if ok {
				end = len(text) - len(rest)
			}
			out = append(out, token{text: text[i:end], start: i, end: end})
			i = end
			continue
		}
		if end, quoted := skipQuoted(text, i); quoted {
			out = append(out, token{text: text[i : end+1], start: i, end: end + 1})
			i = end + 1
			continue
		}
		start := i
		for i < len(text) && !strings.ContainsRune(" \t\n\r()'\"`[", rune(text[i])) {
			i++
		}
		if i == start {
			i++
		}
		out = append(out, token{text: text[start:i], start: start, end: i})
	}
	return out
}

// parenthesized 把以左括号开头的 text 里配对的那对括号里的内容按顶层的逗号切开，并给出右括号之后的部分；
// 引号里的括号与逗号不算。没有配对的右括号时 ok 为假。
func parenthesized(text string) (items []string, rest string, ok bool) {
	depth, from := 0, 1
	for i := 0; i < len(text); i++ {
		if end, quoted := skipQuoted(text, i); quoted {
			i = end
			continue
		}
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return append(items, strings.TrimSpace(text[from:i])), text[i+1:], true
			}
		case ',':
			if depth == 1 {
				items = append(items, strings.TrimSpace(text[from:i]))
				from = i + 1
			}
		}
	}
	return nil, "", false
}

// skipQuoted 在 text[i] 是引号（'…'、"…"、`…`、[…]）时给出配对的结束引号的位置；成对的（” "" “）是转义。
func skipQuoted(text string, i int) (int, bool) {
	closing := map[byte]byte{'\'': '\'', '"': '"', '`': '`', '[': ']'}[text[i]]
	if closing == 0 {
		return i, false
	}
	for j := i + 1; j < len(text); j++ {
		if text[j] != closing {
			continue
		}
		if closing != ']' && j+1 < len(text) && text[j+1] == closing {
			j++
			continue
		}
		return j, true
	}
	return len(text) - 1, true
}

// --- 写索引 ---

func (dialect) IndexEditing() *dbdriver.IndexEditing { return indexEditing }

// indexTarget 是写索引语句的那张表：SQLite 的 CREATE INDEX 把库名写在索引名前（"库"."索引" ON "表"），表名不能带
// 库名；DROP INDEX 同样按库名限定索引名。
type indexTarget struct {
	schemaPrefix string
	table        string
}

// targetOf 拆开 TableRef 给出的表引用（"库"."表" 或 "表"，标识符里的引号成对）：第一个标识符到不成对的引号为止，
// 其后是点就是库名。
func targetOf(tableRef string) indexTarget {
	end, _ := skipQuoted(tableRef, 0)
	if end+1 < len(tableRef) && tableRef[end+1] == '.' {
		return indexTarget{schemaPrefix: tableRef[:end+2], table: tableRef[end+2:]}
	}
	return indexTarget{table: tableRef}
}

// IndexSQL 是建一个普通 / 唯一索引的 CREATE [UNIQUE] INDEX。主键只能在建表时定（写在表定义里），这里拒绝。
func (dialect) IndexSQL(tableRef string, index dbdriver.IndexDefinition) (string, error) {
	return targetOf(tableRef).createIndexSQL(index)
}

// DropIndexSQL 是 DROP INDEX。
func (dialect) DropIndexSQL(tableRef, name string) (string, error) {
	return "DROP INDEX " + targetOf(tableRef).schemaPrefix + quoteIdent(name), nil
}

// createIndexSQL 写 CREATE [UNIQUE] INDEX "库"."名" ON "表" ("列" [DESC], …)；SQLite 的索引必须有名字。
func (t indexTarget) createIndexSQL(index dbdriver.IndexDefinition) (string, error) {
	if index.Type == dbdriver.IndexTypePrimary {
		return "", fmt.Errorf("%w: SQLite sets the primary key only when creating the table (index %q)", dbdriver.ErrUnsupported, index.Name)
	}
	name := strings.TrimSpace(index.Name)
	if name == "" {
		return "", fmt.Errorf("an index on %s has no name; SQLite needs one", t.table)
	}
	columns, err := indexColumnList(index)
	if err != nil {
		return "", err
	}
	statement := "CREATE INDEX "
	if index.Type == dbdriver.IndexTypeUnique {
		statement = "CREATE UNIQUE INDEX "
	}
	return statement + t.schemaPrefix + quoteIdent(name) + " ON " + t.table + " (" + columns + ")", nil
}

// indexColumnList 是索引的列：列名加引号，降序带 DESC。
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

// --- 建表 ---

// CreateTableSQL 写整条建表：CREATE TABLE（列定义，按写法的默认值见 sqldb.NewColumnSQL 与 withDefaultExpression），之后按请求的次序建其余
// 索引。主键：
//   - 有自增列时它就是主键，写在列上（"id" INTEGER PRIMARY KEY）：请求另声明的主键只能正是这一列（升序），
//     不再写成表约束；只能有一个自增列；
//   - 没有自增列时，请求声明的主键写成表约束 PRIMARY KEY (列 [DESC], …)。SQLite 不保存主键约束的名字，不写它。
//
// SQLite 没有注释：带注释的请求以 ErrUnsupported 拒绝（同共用的写法）。没有用到索引与新写法的请求与共用的写法
// 逐字一致。
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
	target := targetOf(tableRef)
	columns := req.Definitions()

	var primary *dbdriver.IndexDefinition
	var others []dbdriver.IndexDefinition
	for i, index := range req.Indexes {
		if index.Type != dbdriver.IndexTypePrimary {
			others = append(others, index)
			continue
		}
		if primary != nil {
			return nil, fmt.Errorf("table %s cannot have more than one primary key", req.Name)
		}
		primary = &req.Indexes[i]
	}
	var autoIncrement []string
	for _, column := range columns {
		if column.AutoIncrement {
			autoIncrement = append(autoIncrement, strings.TrimSpace(column.Name))
		}
	}
	if len(autoIncrement) > 1 {
		return nil, fmt.Errorf("table %s can have only one auto-increment column (the INTEGER PRIMARY KEY), not %s", req.Name, strings.Join(autoIncrement, ", "))
	}
	if len(autoIncrement) == 1 && primary != nil {
		if len(primary.Columns) != 1 || strings.TrimSpace(primary.Columns[0].Name) != autoIncrement[0] || primary.Columns[0].Descending {
			return nil, fmt.Errorf("column %q auto-increments, so it is the primary key of table %s by itself (ascending); the primary key cannot be on other columns",
				autoIncrement[0], req.Name)
		}
	}

	defs := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		def, err := sqldb.NewColumnSQL(d, sqldb.DefinitionLiterals, tables.QuoteIdent, withDefaultExpression(column))
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	if primary != nil && len(autoIncrement) == 0 {
		columns, err := indexColumnList(*primary)
		if err != nil {
			return nil, err
		}
		defs = append(defs, "PRIMARY KEY ("+columns+")")
	}
	statements := []string{fmt.Sprintf("CREATE TABLE %s (\n  %s\n)", tableRef, strings.Join(defs, ",\n  "))}
	for _, index := range others {
		statement, err := target.createIndexSQL(index)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}
