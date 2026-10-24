package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 结构化的筛选条件与排序项怎么变成 SQL：这一部分在各方言里相同，只有标识符的引号、空串是否
// 就是 NULL、值是否按列类型写不同，由调用它的驱动给出。值默认写成字符串字面量（数字也是），与
// 数据库比较时由数据库转换。

// EmptyStringNull 是方言里「空串就是 NULL」（Oracle）：EmptyStringIsNull 为真时，筛选的「为空」写成
// col IS NULL、「不为空」写成 col IS NOT NULL，不再与空串字面量比较——在这样的数据库里「不等于空串」
// 永远不成立，照常的写法让「不为空」一行也匹配不到。没实现的方言照常写成「是 NULL 或等于空串」与
// 「不是 NULL 且不等于空串」。
type EmptyStringNull interface {
	// EmptyStringIsNull 为真表示数据库把空串当作 NULL 存放与比较。
	EmptyStringIsNull() bool
}

// TypedFilterValues 是筛选的值也要按列的类型写字面量的方言（Oracle：带本地时区的时间戳列按会话的
// 时间戳格式转换字符串，不认表格显示的时区）。TypedFilterValues 为真时，表数据的筛选、计数与整表
// 读取在有要写值的条件时先查表的列与 RowWriter 的字面量规则，比较、区间与列表的值按列的类型写
// （Literals.Literal）；文本匹配的值仍是字符串。实现它的方言必须同时实现 RowWriter。
type TypedFilterValues interface {
	TypedFilterValues() bool
}

// FilterSyntax 是筛选条件写成 SQL 时随方言变化的部分。
type FilterSyntax struct {
	// Quote 给一个标识符加上方言的引号。
	Quote func(string) string
	// EmptyStringIsNull 为真表示方言里空串就是 NULL（见 EmptyStringNull）。
	EmptyStringIsNull bool
	// Literal 把要与 column 列比较的值写成字面量（见 TypedFilterValues）；nil 表示一律写成字符串
	// 字面量。
	Literal func(column string, value any) (string, error)
}

// filterSyntax 是会话方言在 database.table 上写 filters 的筛选写法：引号取自 Tables，空串是否就是
// NULL 取自 EmptyStringNull，值按列类型写时（TypedFilterValues）在一条连接上查表的列与字面量规则。
func (s *Session) filterSyntax(ctx context.Context, tables Tables, database, table string, filters []dbdriver.FilterItem) (FilterSyntax, error) {
	syntax := FilterSyntax{Quote: tables.QuoteIdent}
	if empty, ok := s.dialect.(EmptyStringNull); ok {
		syntax.EmptyStringIsNull = empty.EmptyStringIsNull()
	}
	typed, ok := s.dialect.(TypedFilterValues)
	if !ok || !typed.TypedFilterValues() || !hasFilterValues(filters) {
		return syntax, nil
	}
	writer, ok := s.dialect.(RowWriter)
	if !ok {
		return FilterSyntax{}, fmt.Errorf("dialect %T writes typed filter values but is not a RowWriter", s.dialect)
	}
	return onConn(ctx, s, func(conn *sql.Conn) (FilterSyntax, error) {
		columns, err := tables.TableColumns(ctx, conn, database, table)
		if err != nil {
			return FilterSyntax{}, fmt.Errorf("query columns: %w", err)
		}
		literals, err := writer.Literals(ctx, conn)
		if err != nil {
			return FilterSyntax{}, err
		}
		types := make(map[string]string, len(columns))
		for _, column := range columns {
			types[column.Name] = column.Type
		}
		syntax.Literal = func(column string, value any) (string, error) {
			return literals.Literal(value, types[column])
		}
		return syntax, nil
	})
}

// hasFilterValues 报告筛选条件里有没有要写成字面量的值：启用的、填了非 NULL 值的比较、区间或
// 列表条件。
func hasFilterValues(items []dbdriver.FilterItem) bool {
	for _, item := range items {
		if item.Disabled {
			continue
		}
		switch item.Kind {
		case dbdriver.FilterGroup:
			if hasFilterValues(item.Items) {
				return true
			}
		case dbdriver.FilterCondition:
			if _, isLike := likeSQL[item.Operator]; !item.Unset && item.Value != nil && !isLike {
				return true
			}
		}
	}
	return false
}

// FilterWhere 把筛选条件树变成 WHERE 后面的表达式（不含 WHERE）。没有任何有效条件时返回
// 空串。
func FilterWhere(items []dbdriver.FilterItem, syntax FilterSyntax) (string, error) {
	var b strings.Builder
	var join dbdriver.FilterJoin
	for _, item := range items {
		expr, err := filterItemSQL(item, syntax)
		if err != nil {
			return "", err
		}
		if expr == "" {
			continue
		}
		if b.Len() > 0 {
			keyword, err := joinKeyword(join)
			if err != nil {
				return "", err
			}
			b.WriteString(" " + keyword + " ")
		}
		b.WriteString(expr)
		join = item.Join
	}
	return b.String(), nil
}

// SortOrderBy 把排序项变成 ORDER BY 后面的表达式（不含 ORDER BY）；没有排序项时返回空串。
func SortOrderBy(sorts []dbdriver.SortItem, quote func(string) string) (string, error) {
	parts := make([]string, 0, len(sorts))
	for _, sort := range sorts {
		switch sort.Direction {
		case dbdriver.SortAscending:
			parts = append(parts, quote(sort.Column)+" ASC")
		case dbdriver.SortDescending:
			parts = append(parts, quote(sort.Column)+" DESC")
		default:
			return "", fmt.Errorf("unknown sort direction %q for column %q", sort.Direction, sort.Column)
		}
	}
	return strings.Join(parts, ", "), nil
}

func joinKeyword(join dbdriver.FilterJoin) (string, error) {
	switch join {
	case dbdriver.JoinAnd:
		return "AND", nil
	case dbdriver.JoinOr:
		return "OR", nil
	default:
		return "", fmt.Errorf("unknown filter join %q", join)
	}
}

func filterItemSQL(item dbdriver.FilterItem, syntax FilterSyntax) (string, error) {
	switch item.Kind {
	case dbdriver.FilterCondition:
		if item.Disabled {
			return "", nil
		}
		return conditionSQL(item, syntax)
	case dbdriver.FilterGroup:
		if item.Disabled {
			return "", nil
		}
		inner, err := FilterWhere(item.Items, syntax)
		if err != nil || inner == "" {
			return "", err
		}
		if item.Negated {
			return "NOT (" + inner + ")", nil
		}
		return "(" + inner + ")", nil
	default:
		return "", fmt.Errorf("unknown filter item kind %q", item.Kind)
	}
}

// comparisonSQL 是比较运算符在 SQL 里的写法。
var comparisonSQL = map[dbdriver.FilterOperator]string{
	dbdriver.OpEqual:          "=",
	dbdriver.OpNotEqual:       "<>",
	dbdriver.OpLess:           "<",
	dbdriver.OpLessOrEqual:    "<=",
	dbdriver.OpGreater:        ">",
	dbdriver.OpGreaterOrEqual: ">=",
}

// likeSQL 是文本匹配运算符的写法：关键字，以及值的前后是否接通配符。
var likeSQL = map[dbdriver.FilterOperator]struct {
	keyword        string
	prefix, suffix string
}{
	dbdriver.OpContains:      {"LIKE", "%", "%"},
	dbdriver.OpLike:          {"LIKE", "%", "%"},
	dbdriver.OpNotContains:   {"NOT LIKE", "%", "%"},
	dbdriver.OpNotLike:       {"NOT LIKE", "%", "%"},
	dbdriver.OpBeginsWith:    {"LIKE", "", "%"},
	dbdriver.OpNotBeginsWith: {"NOT LIKE", "", "%"},
	dbdriver.OpEndsWith:      {"LIKE", "%", ""},
	dbdriver.OpNotEndsWith:   {"NOT LIKE", "%", ""},
}

// conditionSQL 按 syntax 生成一个条件。条件不产生限制时返回空串。
func conditionSQL(item dbdriver.FilterItem, syntax FilterSyntax) (string, error) {
	column, emptyStringIsNull := syntax.Quote(item.Column), syntax.EmptyStringIsNull
	valueLiteral := literal
	if syntax.Literal != nil {
		valueLiteral = func(value any) (string, error) { return syntax.Literal(item.Column, value) }
	}
	switch item.Operator {
	case dbdriver.OpIsNull:
		return column + " IS NULL", nil
	case dbdriver.OpIsNotNull:
		return column + " IS NOT NULL", nil
	case dbdriver.OpIsEmpty:
		if emptyStringIsNull {
			return column + " IS NULL", nil
		}
		return "(" + column + " IS NULL OR " + column + " = '')", nil
	case dbdriver.OpIsNotEmpty:
		if emptyStringIsNull {
			return column + " IS NOT NULL", nil
		}
		return "(" + column + " IS NOT NULL AND " + column + " <> '')", nil
	}

	comparison, isComparison := comparisonSQL[item.Operator]
	like, isLike := likeSQL[item.Operator]
	isRange := item.Operator == dbdriver.OpBetween || item.Operator == dbdriver.OpNotBetween
	isList := item.Operator == dbdriver.OpInList || item.Operator == dbdriver.OpNotInList
	if !isComparison && !isLike && !isRange && !isList {
		return "", fmt.Errorf("unknown filter operator %q for column %q", item.Operator, item.Column)
	}

	if item.Unset {
		return "", nil
	}
	if item.Value == nil {
		switch item.Operator {
		case dbdriver.OpEqual:
			return column + " IS NULL", nil
		case dbdriver.OpNotEqual:
			return column + " IS NOT NULL", nil
		default:
			return "", nil
		}
	}

	negation := ""
	if item.Operator == dbdriver.OpNotBetween || item.Operator == dbdriver.OpNotInList {
		negation = "NOT "
	}
	switch {
	case isLike:
		text, err := valueText(item.Value)
		if err != nil {
			return "", err
		}
		return column + " " + like.keyword + " " + quoteString(like.prefix+text+like.suffix), nil
	case isRange:
		bounds, ok := item.Value.([]any)
		if !ok || len(bounds) < 2 {
			return "", nil
		}
		literals, err := literalList(bounds[:2], valueLiteral)
		if err != nil {
			return "", err
		}
		return column + " " + negation + "BETWEEN " + literals[0] + " AND " + literals[1], nil
	case isList:
		literals, err := literalList(listValues(item.Value), valueLiteral)
		if err != nil || len(literals) == 0 {
			return "", err
		}
		return column + " " + negation + "IN (" + strings.Join(literals, ", ") + ")", nil
	default:
		value, err := valueLiteral(item.Value)
		if err != nil {
			return "", err
		}
		return column + " " + comparison + " " + value, nil
	}
}

// listValues 把列表运算符的值变成元素：列表原样，文本按逗号拆开（去掉首尾空白与空段），
// 其余标量是只有它自己的列表。
func listValues(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case string:
		var parts []any
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				parts = append(parts, part)
			}
		}
		return parts
	default:
		return []any{value}
	}
}

func literalList(values []any, literal func(any) (string, error)) ([]string, error) {
	literals := make([]string, 0, len(values))
	for _, value := range values {
		lit, err := literal(value)
		if err != nil {
			return nil, err
		}
		literals = append(literals, lit)
	}
	return literals, nil
}

// literal 把一个值写成 SQL 字面量：nil 是 NULL，其余都是字符串字面量。
func literal(value any) (string, error) {
	if value == nil {
		return "NULL", nil
	}
	text, err := valueText(value)
	if err != nil {
		return "", err
	}
	return quoteString(text), nil
}

// quoteString 把文本包成字符串字面量，只把单引号成对转义。
func quoteString(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "''") + "'"
}

// valueText 是一个值写进字面量里的文本。值来自表格的单元格或筛选器的输入，经 JSON 到达
// 这里，所以数字照前端显示它的样子写（1e21 以下、1e-6 以上不用指数），列表是各元素以逗号
// 相接（其中的 NULL 为空）。
func valueText(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return numberText(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, element := range v {
			part, err := valueText(element)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, ","), nil
	default:
		return "", fmt.Errorf("unsupported value of type %T", value)
	}
}

func numberText(f float64) string {
	if f == 0 {
		return "0"
	}
	if abs := math.Abs(f); abs >= 1e21 || abs < 1e-6 {
		// 指数形式照 JavaScript 的写法：指数不补零（1e-7，不是 1e-07）。
		mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
		return mantissa + "e" + exponent[:1] + strings.TrimLeft(exponent[1:], "0")
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
