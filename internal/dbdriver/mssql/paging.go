package mssql

import (
	"fmt"
	"strings"
)

// dialect 是 SQL Server 的分页方言：没有 LIMIT / OFFSET，用 OFFSET ... ROWS FETCH NEXT ...
// ROWS ONLY，而这个语法要求 ORDER BY。
type dialect struct{}

// CountSQL：派生表里不允许不带 TOP / OFFSET 的 ORDER BY，计数前要把顶层的 ORDER BY 去掉。
func (dialect) CountSQL(query string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS _t", stripTopLevelOrderBy(query))
}

// PagedSQL：查询自带顶层 ORDER BY 时直接接 OFFSET / FETCH；没有时包成子查询，用
// ORDER BY (SELECT NULL) 占位（不强加排序键）。
func (dialect) PagedSQL(query string, offset, limit int) string {
	if hasTopLevelOrderBy(query) {
		return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", query, offset, limit)
	}
	return fmt.Sprintf(
		"SELECT * FROM (%s) AS _t ORDER BY (SELECT NULL) OFFSET %d ROWS FETCH NEXT %d ROWS ONLY",
		query, offset, limit)
}

func stripTopLevelOrderBy(sqlText string) string {
	if idx := topLevelOrderByIndex(sqlText); idx >= 0 {
		return strings.TrimSpace(sqlText[:idx])
	}
	return sqlText
}

func hasTopLevelOrderBy(sqlText string) bool {
	return topLevelOrderByIndex(sqlText) >= 0
}

func topLevelOrderByIndex(sqlText string) int {
	upper := strings.ToUpper(sqlText)
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracketQuote := false

	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < len(sqlText) && sqlText[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracketQuote {
			if ch == ']' {
				if i+1 < len(sqlText) && sqlText[i+1] == ']' {
					i++
					continue
				}
				inBracketQuote = false
			}
			continue
		}

		switch ch {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '[':
			inBracketQuote = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && strings.HasPrefix(upper[i:], "ORDER") && isSQLWordBoundary(sqlText, i-1) {
				afterOrder := i + len("ORDER")
				if afterOrder < len(sqlText) && isSQLSpace(sqlText[afterOrder]) {
					j := afterOrder
					for j < len(sqlText) && isSQLSpace(sqlText[j]) {
						j++
					}
					if strings.HasPrefix(upper[j:], "BY") && isSQLWordBoundary(sqlText, j+len("BY")) {
						return i
					}
				}
			}
		}
	}
	return -1
}

func isSQLWordBoundary(sqlText string, idx int) bool {
	if idx < 0 || idx >= len(sqlText) {
		return true
	}
	ch := sqlText[idx]
	return ch != '_' && (ch < '0' || ch > '9') && (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z')
}

func isSQLSpace(ch byte) bool {
	return ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t' || ch == '\f'
}
