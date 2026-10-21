package query_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/opskat/opskat/pkg/dbdriver"
)

const xlsxSheetName = "Sheet1"

// 导出的范围：当前页，或整张表。
const (
	ExportScopePage = "page"
	ExportScopeAll  = "all"
)

// TableExportRequest 是导出一张表的请求：结构化的筛选条件与排序项，范围，以及要写哪些列。
// 里面没有 SQL：怎么取数由资产的驱动决定。
type TableExportRequest struct {
	Filters []dbdriver.FilterItem `json:"filters"`
	Sorts   []dbdriver.SortItem   `json:"sorts"`
	// Columns 是要写的列，按写出的顺序；为空表示驱动给出的全部列。
	Columns []string `json:"columns"`
	// Scope 是 ExportScopePage 或 ExportScopeAll。
	Scope string `json:"scope"`
	// Page / PageSize 只在 ExportScopePage 时用：页号从 0 起。
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// ExportTableRowsToXlsx 经驱动的会话按请求取出表里的行，流式写成一个 XLSX 工作簿到 w。
// 当前页取一页；整张表是对驱动的一次读取（一个游标），边读边写，内存不随行数增长，也不会因为
// 分成多条语句而在没有稳定顺序时重复或漏行。第一行是列名。
func ExportTableRowsToXlsx(ctx context.Context, session dbdriver.Session, database, table string, request TableExportRequest, w io.Writer) (err error) {
	query := dbdriver.TableQuery{Filters: request.Filters, Sorts: request.Sorts}
	var source dbdriver.RowStream
	switch request.Scope {
	case ExportScopeAll:
		source, err = session.StreamTableRows(ctx, database, table, query)
		if err != nil {
			return err
		}
	case ExportScopePage:
		rowSet, pageErr := session.TableRows(ctx, database, table, query, dbdriver.Page{Index: request.Page, Size: request.PageSize})
		if pageErr != nil {
			return pageErr
		}
		source = &rowSetStream{rowSet: rowSet, next: -1}
	default:
		return fmt.Errorf("unknown export scope %q", request.Scope)
	}
	defer func() {
		if closeErr := source.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close rows: %w", closeErr))
		}
	}()

	indexes, header, err := projectColumns(source.Columns(), request.Columns)
	if err != nil {
		return err
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sw, err := f.NewStreamWriter(xlsxSheetName)
	if err != nil {
		return fmt.Errorf("create stream writer: %w", err)
	}
	if err := setXlsxRow(sw, 1, header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	rowIdx := 2
	for source.Next() {
		values := source.Row()
		record := make([]any, len(indexes))
		for i, index := range indexes {
			record[i] = xlsxCellValue(values[index])
		}
		if err := setXlsxRow(sw, rowIdx, record); err != nil {
			return fmt.Errorf("write row %d: %w", rowIdx, err)
		}
		rowIdx++
	}
	if err := source.Err(); err != nil {
		return fmt.Errorf("read rows: %w", err)
	}

	if err := sw.Flush(); err != nil {
		return fmt.Errorf("flush stream: %w", err)
	}
	if _, err := f.WriteTo(w); err != nil {
		return fmt.Errorf("write workbook: %w", err)
	}
	return nil
}

// rowSetStream 把一页已取出的行当作游标读。
type rowSetStream struct {
	rowSet *dbdriver.RowSet
	next   int
}

func (r *rowSetStream) Columns() []string { return r.rowSet.Columns }
func (r *rowSetStream) Next() bool {
	r.next++
	return r.next < len(r.rowSet.Rows)
}
func (r *rowSetStream) Row() []any   { return r.rowSet.Rows[r.next] }
func (r *rowSetStream) Err() error   { return nil }
func (r *rowSetStream) Close() error { return nil }

// projectColumns 决定写哪些列：selected 为空是 available 的全部；否则按 selected 的顺序，
// 每一列都必须在 available 里。返回各列在结果集里的下标与表头。
func projectColumns(available, selected []string) ([]int, []any, error) {
	names := selected
	if len(names) == 0 {
		names = available
	}
	indexes := make([]int, len(names))
	header := make([]any, len(names))
	for i, name := range names {
		index := slices.Index(available, name)
		if index < 0 {
			return nil, nil, fmt.Errorf("export column %q is not in the table", name)
		}
		indexes[i], header[i] = index, name
	}
	return indexes, header, nil
}

func setXlsxRow(sw *excelize.StreamWriter, rowIdx int, record []any) error {
	cell, err := excelize.CoordinatesToCellName(1, rowIdx)
	if err != nil {
		return fmt.Errorf("row cell: %w", err)
	}
	return sw.SetRow(cell, record)
}

// xlsxCellValue normalizes a scanned SQL value into a type excelize accepts.
// []byte (BLOB/TEXT) becomes a string; NULL becomes an empty cell; time.Time
// and primitive types are written as-is so excelize formats them natively.
// json.Number (an extension driver's numbers, decoded keeping their digits)
// becomes a numeric cell too.
func xlsxCellValue(v any) any {
	switch val := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(val)
	case time.Time:
		return val
	case json.Number:
		return xlsxNumber(val)
	default:
		return val
	}
}

// xlsxNumber writes an integer as an integer and a fraction as a float. An
// integer past int64 stays its digits: as a float it would lose them.
func xlsxNumber(n json.Number) any {
	if i, err := n.Int64(); err == nil {
		return i
	}
	if strings.ContainsAny(string(n), ".eE") {
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return n.String()
}
