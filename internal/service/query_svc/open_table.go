package query_svc

import (
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 表浏览绑定收发的类型。打开表、翻页、计数由驱动在自己的会话上完成（dbdriver.Session 的
// 表浏览方法），这里只是请求的形状，以及把驱动的结构化结果交给前端的形状：行按列名成对。

// OpenTableResult 是打开一张表时一次返回的全部首屏数据。
type OpenTableResult struct {
	Columns     []string          `json:"columns"`
	ColumnTypes map[string]string `json:"columnTypes"`
	// ColumnCategories 是导入转换源文件里的值用的列类别（见 dbdriver.ColumnCategory）。
	ColumnCategories map[string]dbdriver.ColumnCategory `json:"columnCategories"`
	ColumnRules      []dbdriver.TableColumnRule         `json:"columnRules"`
	PrimaryKeys      []string                           `json:"primaryKeys"`
	TotalCount       int64                              `json:"totalCount"`
	FirstPage        []map[string]any                   `json:"firstPage"`
	PageSize         int                                `json:"pageSize"`
}

// NewOpenTableResult 把驱动给出的首屏转成绑定返回的形状。pageSize 是第一页请求的行数。
func NewOpenTableResult(opened *dbdriver.OpenedTable, pageSize int) *OpenTableResult {
	return &OpenTableResult{
		Columns:          opened.FirstPage.Columns,
		ColumnTypes:      opened.ColumnTypes,
		ColumnCategories: opened.ColumnCategories,
		ColumnRules:      opened.ColumnRules,
		PrimaryKeys:      opened.PrimaryKeys,
		TotalCount:       opened.TotalCount,
		FirstPage:        rowMaps(opened.FirstPage),
		PageSize:         pageSize,
	}
}

// TableRowsRequest 是取表里一页数据的请求：结构化的筛选条件与排序项，加上要哪一页。
type TableRowsRequest struct {
	Filters []dbdriver.FilterItem `json:"filters"`
	Sorts   []dbdriver.SortItem   `json:"sorts"`
	// Page 是页号，从 0 起。
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// TableRows 是表里的一页数据。
type TableRows struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

// NewTableRows 把驱动给出的一页转成绑定返回的形状。
func NewTableRows(rowSet *dbdriver.RowSet) *TableRows {
	return &TableRows{Columns: rowSet.Columns, Rows: rowMaps(rowSet)}
}

// rowMaps 把按列序给值的行转成按列名取值的行；没有行时是空切片（序列化成 []）。
func rowMaps(rowSet *dbdriver.RowSet) []map[string]any {
	rows := make([]map[string]any, 0, len(rowSet.Rows))
	for _, values := range rowSet.Rows {
		row := make(map[string]any, len(rowSet.Columns))
		for i, column := range rowSet.Columns {
			row[column] = values[i]
		}
		rows = append(rows, row)
	}
	return rows
}
