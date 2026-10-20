package dbdriver

// 表浏览：打开一张表、翻页、计数。请求里只有结构化的筛选条件与排序项，怎么把它们变成
// 自己方言的查询是驱动的事——共享代码不替驱动拼 WHERE / ORDER BY。

// FilterKind 区分筛选项是一个条件还是一组括起来的条件。
type FilterKind string

const (
	FilterCondition FilterKind = "condition"
	FilterGroup     FilterKind = "group"
)

// FilterJoin 是一个筛选项与同一层里它后面那一项的连接方式。连接不带优先级：同一层的各项
// 按先后平铺，AND 先于 OR 结合；要改变结合次序用分组。
type FilterJoin string

const (
	JoinAnd FilterJoin = "and"
	JoinOr  FilterJoin = "or"
)

// FilterOperator 是条件的运算符。
type FilterOperator string

const (
	OpEqual          FilterOperator = "="
	OpNotEqual       FilterOperator = "!="
	OpLess           FilterOperator = "<"
	OpLessOrEqual    FilterOperator = "<="
	OpGreater        FilterOperator = ">"
	OpGreaterOrEqual FilterOperator = ">="
	// 文本匹配：值按字面嵌进模式，其中的通配符不转义。like / not_like 与
	// contains / not_contains 同义。
	OpContains      FilterOperator = "contains"
	OpNotContains   FilterOperator = "not_contains"
	OpBeginsWith    FilterOperator = "begins_with"
	OpNotBeginsWith FilterOperator = "not_begins_with"
	OpEndsWith      FilterOperator = "ends_with"
	OpNotEndsWith   FilterOperator = "not_ends_with"
	OpLike          FilterOperator = "like"
	OpNotLike       FilterOperator = "not_like"
	// 以下四个不看值。「空」指 NULL 或空串。
	OpIsNull     FilterOperator = "is_null"
	OpIsNotNull  FilterOperator = "is_not_null"
	OpIsEmpty    FilterOperator = "is_empty"
	OpIsNotEmpty FilterOperator = "is_not_empty"
	// 区间：值是两个元素的列表（下界、上界）。
	OpBetween    FilterOperator = "between"
	OpNotBetween FilterOperator = "not_between"
	// 列表：值是一个列表，或一段以逗号分隔的文本。
	OpInList    FilterOperator = "in_list"
	OpNotInList FilterOperator = "not_in_list"
)

// FilterItem 是筛选条件树里的一项：一个条件，或一组条件。
//
// 不产生任何限制的项被整个略过，连同它的 Join：停用的项、还没给值的条件、区间缺了一端、
// 列表为空、里面没有任何有效条件的组。
type FilterItem struct {
	Kind FilterKind `json:"kind"`
	// Join 连接这一项与同层里它之后第一个没被略过的项。
	Join FilterJoin `json:"join"`
	// Disabled 表示这一项被停用；停用一个组即停用它里面的全部。
	Disabled bool `json:"disabled,omitempty"`

	// 以下三个字段属于条件（Kind == FilterCondition）。

	Column   string         `json:"column,omitempty"`
	Operator FilterOperator `json:"operator,omitempty"`
	// Value 是条件的值：标量，或区间 / 列表运算符用的列表。nil 表示 NULL：等于 NULL、不等于
	// NULL 分别是「为空」「不为空」，其余要值的运算符遇到 NULL 则略过这个条件。
	Value any `json:"value,omitempty"`
	// Unset 表示用户还没有给这个条件填值（不同于填了 NULL），此时不看 Value。
	Unset bool `json:"unset,omitempty"`

	// 以下两个字段属于组（Kind == FilterGroup）。

	// Negated 给整个组取反。
	Negated bool         `json:"negated,omitempty"`
	Items   []FilterItem `json:"items,omitempty"`
}

// SortDirection 是排序方向。
type SortDirection string

const (
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

// SortItem 是一个排序键；多个排序键按先后依次生效。
type SortItem struct {
	Column    string        `json:"column"`
	Direction SortDirection `json:"dir"`
}

// TableQuery 是取表数据时的结构化条件：筛选哪些行、按什么顺序。两者都可以为空。
type TableQuery struct {
	Filters []FilterItem `json:"filters"`
	Sorts   []SortItem   `json:"sorts"`
}

// TableColumnRule 是一列在新增行时要遵守的约束。
type TableColumnRule struct {
	Name          string `json:"name"`
	Nullable      bool   `json:"nullable"`
	HasDefault    bool   `json:"hasDefault"`
	AutoIncrement bool   `json:"autoIncrement"`
}

// ColumnCategory 是一列的值属于哪一类，由驱动按自己的类型系统判断。导入据此决定源文件里的值
// 怎么转换（日期格式、小数点、二进制编码）。
type ColumnCategory string

const (
	CategoryText     ColumnCategory = "text"
	CategoryNumber   ColumnCategory = "number"
	CategoryDate     ColumnCategory = "date"
	CategoryDateTime ColumnCategory = "datetime"
	CategoryBinary   ColumnCategory = "binary"
)

// ColumnCategories 是全部已知的列类别。
func ColumnCategories() []ColumnCategory {
	return []ColumnCategory{CategoryText, CategoryNumber, CategoryDate, CategoryDateTime, CategoryBinary}
}

// OpenedTable 是打开一张表时一次给出的首屏。
type OpenedTable struct {
	// ColumnTypes 按列名给出列类型；驱动说不出类型的列不在其中。
	ColumnTypes map[string]string
	// ColumnCategories 按列名给出列的类别（声明了导入的驱动给出）；不在其中的列按文本处理。
	ColumnCategories map[string]ColumnCategory
	ColumnRules      []TableColumnRule
	// PrimaryKeys 是主键列，按它们在主键里的次序；没有主键时为空。
	PrimaryKeys []string
	// TotalCount 是表的总行数。
	TotalCount int64
	// FirstPage 是不带筛选与排序的第一页；它的 Columns 就是表格的列序。
	FirstPage *RowSet
}
