package sqldb

import (
	"fmt"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 表设计器里的索引编辑。方言实现 TableIndexer 即开启，驱动描述的 IndexEditing 用 IndexEditing(dialect)
// 从同一个方言得出，声明与实现只有这一个来源。没开启的方言，Session 以 dbdriver.ErrUnsupported 拒绝带
// 索引的建表 / 改表请求，读出的改表模型也不带索引。
//
// 现有索引与请求里的索引怎么比较、改动是否在声明范围内、只读与不可删除的索引怎么处理，由 Session 统一
// 完成；怎么把一条索引写成语句由方言给出。

// TableIndexer 是方言里「索引怎么写」：实现它即开启索引编辑。方言读表时（AlterableTable）同时给出
// 现有的索引与范围内其他表已占用的索引名。
type TableIndexer interface {
	// IndexEditing 是方言开启的索引编辑声明：可用的类型、主键可改、降序、前缀长度、注释、名称唯一范围与
	// 额外选项。驱动描述的 IndexEditing 就是它。
	IndexEditing() *dbdriver.IndexEditing
	// IndexSQL 是建一条索引的语句（不含分号）。tableRef 已按方言加好引号；索引的列名是改动之后的列名，
	// 不合法时返回 error（IndexEditing.Check 已检查过声明了的能力）。
	IndexSQL(tableRef string, index dbdriver.IndexDefinition) (string, error)
	// DropIndexSQL 是删除一条索引的语句（不含分号）。
	DropIndexSQL(tableRef, name string) (string, error)
}

// TableCreator 是方言里「整条建表语句怎么写」：实现它的方言自己把列、索引与注释（表注释与列注释）写成
// 语句，共用代码不再拼 CREATE TABLE——注释只能写进表定义里的方言（如 MySQL）必须实现它。没实现它的方言用
// 共用的写法（CREATE TABLE 加建索引语句）；表注释与列注释再交给 SchemaCommenter，方言写不了时请求带
// 注释就以 dbdriver.ErrUnsupported 拒绝。
type TableCreator interface {
	CreateTableSQL(tables Tables, database string, req dbdriver.CreateTableRequest) ([]string, error)
}

// SchemaCommenter 是方言里「建表之后怎么写表注释与列注释」：CREATE TABLE（与建索引）之后每条注释一条语句，
// 按表注释在前、列注释按列的次序。没实现它、也没实现 TableCreator 的方言写不了建表时的注释。
type SchemaCommenter interface {
	// TableCommentSQL 是给新表写注释的语句（不含分号），comment 已去掉首尾空白、不为空。
	TableCommentSQL(tableRef, comment string) (string, error)
	// ColumnCommentSQL 是给新表的一列写注释的语句（不含分号），column.Comment 已去掉首尾空白、不为空。
	ColumnCommentSQL(tableRef string, column dbdriver.ColumnDefinition) (string, error)
}

// IndexesTables 报告方言开启了索引编辑（实现了 TableIndexer），即驱动描述的 IndexEditing。参数是方言
// 本身（Dialect、TableAlterer 都是）。
func IndexesTables(dialect any) bool {
	_, ok := dialect.(TableIndexer)
	return ok
}

// IndexEditing 是方言开启的索引编辑声明，没开启时为 nil。
func IndexEditing(dialect any) *dbdriver.IndexEditing {
	indexer, ok := dialect.(TableIndexer)
	if !ok {
		return nil
	}
	return indexer.IndexEditing()
}

// IndexChange 是一处索引的改动：现有的索引（Current）与它改完之后的样子（Index）。新增的索引 Current 为
// nil。
type IndexChange struct {
	// Current 是这一索引现在的定义；新增的索引为 nil。
	Current *dbdriver.IndexDefinition
	// Index 是改完之后的样子（新增的索引就是它的定义）。
	Index dbdriver.IndexDefinition
}

// IsNew 报告这是新增的索引。
func (c IndexChange) IsNew() bool { return c.Current == nil }

// 改表里索引的语句分两半，方言把 IndexDropStatements 写在列的改动之前、IndexCreateStatements 写在列的
// 改动之后：引用被删列的索引先于删列删掉；建索引时新增的列已经加上、改名的列已经是新名字（索引的列名
// 是改动之后的列名）。先删完再建，改名的索引也不会与另一个还没删的索引重名。tableRef 是改名之后的表引用
// （TableChange.TargetRef），方言要先改表名再写这两半。

// IndexDropStatements 是要删的索引的语句：请求删掉的，以及改动了的现有索引（随后重建）。
func IndexDropStatements(dialect TableIndexer, tableRef string, change TableChange) ([]string, error) {
	statements := make([]string, 0, len(change.IndexDrops)+len(change.Indexes))
	for _, dropped := range change.IndexDrops {
		statement, err := dialect.DropIndexSQL(tableRef, dropped.Name)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	for _, changed := range change.Indexes {
		if changed.Current == nil {
			continue
		}
		statement, err := dialect.DropIndexSQL(tableRef, changed.Current.Name)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}

// IndexCreateStatements 是要建的索引的语句：改动了的（已由 IndexDropStatements 删掉）与新增的。
func IndexCreateStatements(dialect TableIndexer, tableRef string, change TableChange) ([]string, error) {
	statements := make([]string, 0, len(change.Indexes))
	for _, changed := range change.Indexes {
		statement, err := dialect.IndexSQL(tableRef, changed.Index)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}

// tableColumnsAfter 是改动之后的表的列：现在的名字到改名之后的名字（只有改了名的），改动之后的全部列名，
// 以及删掉的列。
type tableColumnsAfter struct {
	renames map[string]string
	present map[string]bool
	dropped map[string]bool
}

// resolve 是索引里一列在改动之后的表里的名字：现在的名字换成改名之后的名字；这一列不在改动之后的表里时
// ok 为假。
func (c tableColumnsAfter) resolve(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if renamed, ok := c.renames[name]; ok {
		name = renamed
	}
	return name, c.present[name]
}

// planIndexes 比较请求里的索引与表现在的索引，得出要删与要建的索引（见 dbdriver.AlterTableRequest）：
// 原样交回的不产生语句；改动的是删除后重建（方言可以写成等价语句）；只读的只能删除、不可删除的删不得；
// 主键的任何改动要声明了主键可改；索引的列按改动之后的表解析（改名的列换成新名字，不在表里的列报错）；
// 引用被删列的索引随列一起删（请求没有列出的话补上），请求却要保留它时失败。
//
// editing 非 nil：没有声明索引编辑的方言的请求在读表之前已被拒绝。change 已经填好改名与删除的列。
func planIndexes(editing *dbdriver.IndexEditing, current *AlterableTable, change *TableChange, req dbdriver.AlterTableRequest, columns tableColumnsAfter) error {
	if editing == nil {
		// 方言没声明索引编辑：会话已在读表之前拒绝任何索引改动，这里只有一点可做——但索引也读不出来，
		// 引用被删列的索引留给数据库随列一起丢掉。
		return nil
	}
	table := change.Table
	existing := make(map[string]*dbdriver.IndexDefinition, len(current.Indexes))
	for i := range current.Indexes {
		existing[current.Indexes[i].Name] = &current.Indexes[i]
	}
	// normalize 把一条索引的列名解析到改动之后的表上：名字不合法的列报错。
	normalize := func(index dbdriver.IndexDefinition) (dbdriver.IndexDefinition, error) {
		normalized := index
		normalized.Name = strings.TrimSpace(index.Name)
		normalized.Comment = strings.TrimSpace(index.Comment)
		normalized.Columns = make([]dbdriver.IndexColumn, len(index.Columns))
		for i, column := range index.Columns {
			column.Name = strings.TrimSpace(column.Name)
			if !column.Expression {
				name, ok := columns.resolve(column.Name)
				if !ok {
					return dbdriver.IndexDefinition{}, fmt.Errorf("index %q names column %q, which is not in table %s after the change", normalized.Name, column.Name, table)
				}
				column.Name = name
			}
			normalized.Columns[i] = column
		}
		if len(index.Include) > 0 {
			normalized.Include = make([]string, len(index.Include))
			for i, included := range index.Include {
				name, ok := columns.resolve(strings.TrimSpace(included))
				if !ok {
					return dbdriver.IndexDefinition{}, fmt.Errorf("index %q includes column %q, which is not in table %s after the change", normalized.Name, included, table)
				}
				normalized.Include[i] = name
			}
		}
		return normalized, nil
	}
	// renamedColumns 是现有索引在改动之后的列名：索引跟着列改名，不必重建。
	renamedColumns := func(index dbdriver.IndexDefinition) dbdriver.IndexDefinition {
		out := index
		out.Columns = make([]dbdriver.IndexColumn, len(index.Columns))
		for i, column := range index.Columns {
			if !column.Expression {
				if renamed, ok := columns.renames[column.Name]; ok {
					column.Name = renamed
				}
			}
			out.Columns[i] = column
		}
		if len(index.Include) > 0 {
			out.Include = make([]string, len(index.Include))
			for i, included := range index.Include {
				if renamed, ok := columns.renames[included]; ok {
					included = renamed
				}
				out.Include[i] = included
			}
		}
		return out
	}

	mentioned := make(map[string]bool, len(req.Indexes)+len(req.IndexDrops))
	primaryKeys := 0
	for _, requested := range req.Indexes {
		index, err := normalize(requested.Definition())
		if err != nil {
			return err
		}
		if index.Type == dbdriver.IndexTypePrimary {
			primaryKeys++
		}
		if requested.OriginalName == "" {
			// 新索引只用声明了的能力（新主键还要声明了主键可改）。这一部分在读表之前已检查过，这里再按解析
			// 之后的定义检查一次：列名解析不改别的。
			if err := editing.Check(index); err != nil {
				return err
			}
			change.Indexes = append(change.Indexes, IndexChange{Index: index})
			continue
		}

		current, ok := existing[requested.OriginalName]
		if !ok {
			return fmt.Errorf("index %q is not in table %s", requested.OriginalName, table)
		}
		if mentioned[current.Name] {
			return fmt.Errorf("index %q of table %s is given more than once", current.Name, table)
		}
		mentioned[current.Name] = true
		if index.Equal(renamedColumns(*current)) {
			continue
		}
		switch {
		case current.ReadOnlyReason != "":
			return fmt.Errorf("%w: index %q cannot be changed (%s); it can only be dropped",
				dbdriver.ErrUnsupported, current.Name, current.ReadOnlyReason)
		case current.Undroppable:
			return fmt.Errorf("%w: index %q cannot be dropped, so it cannot be rebuilt either", dbdriver.ErrUnsupported, current.Name)
		case (current.Type == dbdriver.IndexTypePrimary || index.Type == dbdriver.IndexTypePrimary) && !editing.AlterPrimaryKey:
			return fmt.Errorf("%w: this driver does not declare altering the primary key, but index %q changes it",
				dbdriver.ErrUnsupported, current.Name)
		}
		if err := editing.Check(index); err != nil {
			return err
		}
		change.Indexes = append(change.Indexes, IndexChange{Current: current, Index: index})
	}

	drops := make(map[string]bool, len(req.IndexDrops))
	for _, name := range req.IndexDrops {
		current, ok := existing[name]
		if !ok {
			return fmt.Errorf("index %q is not in table %s", name, table)
		}
		if mentioned[name] || drops[name] {
			return fmt.Errorf("index %q of table %s is given more than once", name, table)
		}
		if current.Undroppable {
			return fmt.Errorf("%w: index %q cannot be dropped", dbdriver.ErrUnsupported, name)
		}
		if current.Type == dbdriver.IndexTypePrimary && !editing.AlterPrimaryKey {
			return fmt.Errorf("%w: this driver does not declare altering the primary key, but index %q is one",
				dbdriver.ErrUnsupported, name)
		}
		drops[name] = true
		change.IndexDrops = append(change.IndexDrops, *current)
	}

	// 引用被删列（键列或 INCLUDE 列）的索引随列一起删：不删它就删不掉那一列。请求没有列出的在这里补上；不可删除的留给数据库
	// 随列一起丢掉。请求要保留一个（改完之后仍然）引用被删列的索引是矛盾的，报错而不是写一条数据库会拒的语句。
	for _, changed := range change.Indexes {
		for _, column := range changed.Index.Columns {
			if !column.Expression && columns.dropped[column.Name] {
				return fmt.Errorf("index %q of table %s still names column %q, which the request drops",
					changed.Index.Name, table, column.Name)
			}
		}
	}
	for i := range current.Indexes {
		index := &current.Indexes[i]
		if mentioned[index.Name] || drops[index.Name] || index.Undroppable {
			continue
		}
		if slicesContainsColumn(index.Columns, columns.dropped) || slices.ContainsFunc(index.Include, func(name string) bool { return columns.dropped[name] }) {
			drops[index.Name] = true
			change.IndexDrops = append(change.IndexDrops, *index)
		}
	}

	if primaryKeys > 1 {
		return fmt.Errorf("table %s cannot have more than one primary key", table)
	}
	return checkExclusiveOptions(editing, indexesAfter(current.Indexes, change), table)
}

// indexesAfter 是改完之后表上的全部索引：没删也没改的现有索引按现状，改动的与新增的按请求。
func indexesAfter(current []dbdriver.IndexDefinition, change *TableChange) []dbdriver.IndexDefinition {
	gone := make(map[string]bool, len(change.IndexDrops)+len(change.Indexes))
	for _, dropped := range change.IndexDrops {
		gone[dropped.Name] = true
	}
	after := make([]dbdriver.IndexDefinition, 0, len(current)+len(change.Indexes))
	for _, changed := range change.Indexes {
		if changed.Current != nil {
			gone[changed.Current.Name] = true
		}
		after = append(after, changed.Index)
	}
	for _, index := range current {
		if !gone[index.Name] {
			after = append(after, index)
		}
	}
	return after
}

// checkExclusiveOptions 拒绝一张表上有两个索引打开同一个独占开关（IndexOptionDecl.Exclusive）；indexes 是
// 这张表（改完之后 / 新建）的全部索引。
func checkExclusiveOptions(editing *dbdriver.IndexEditing, indexes []dbdriver.IndexDefinition, table string) error {
	for _, option := range editing.Options {
		if !option.Exclusive {
			continue
		}
		var on []string
		for _, index := range indexes {
			if enabled, _ := index.Options[option.Key].(bool); enabled {
				on = append(on, index.Name)
			}
		}
		if len(on) > 1 {
			return fmt.Errorf("table %s can have only one index with option %q, but %s have it", table, option.Key, strings.Join(on, ", "))
		}
	}
	return nil
}

// slicesContainsColumn 报告索引的列里有哪一列的名字在 dropped 里。
func slicesContainsColumn(columns []dbdriver.IndexColumn, dropped map[string]bool) bool {
	for _, column := range columns {
		if !column.Expression && dropped[column.Name] {
			return true
		}
	}
	return false
}
