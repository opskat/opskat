package query_svc

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/opskat/opskat/internal/dbdriver"
)

const xlsxSheetName = "Sheet1"

// 导出的范围：当前页，或整张表。
const (
	ExportScopePage = "page"
	ExportScopeAll  = "all"
)

// exportChunkRows 是导出整张表时每次向驱动取的行数。
const exportChunkRows = 1000

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
// 用 excelize 的 StreamWriter 边取边写，大表导出时内存不随行数增长。第一行是列名。
func ExportTableRowsToXlsx(ctx context.Context, session dbdriver.Session, database, table string, request TableExportRequest, w io.Writer) error {
	if request.Scope != ExportScopePage && request.Scope != ExportScopeAll {
		return fmt.Errorf("unknown export scope %q", request.Scope)
	}
	query := dbdriver.TableQuery{Filters: request.Filters, Sorts: request.Sorts}
	fetch := func(page dbdriver.Page) (*dbdriver.RowSet, error) {
		return session.TableRows(ctx, database, table, query, page)
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sw, err := f.NewStreamWriter(xlsxSheetName)
	if err != nil {
		return fmt.Errorf("create stream writer: %w", err)
	}

	pageSize := exportChunkRows
	pageIndex := 0
	if request.Scope == ExportScopePage {
		pageSize, pageIndex = request.PageSize, request.Page
	}

	var indexes []int
	rowIdx := 1
	for {
		rowSet, err := fetch(dbdriver.Page{Index: pageIndex, Size: pageSize})
		if err != nil {
			return err
		}
		if rowIdx == 1 {
			var header []any
			if indexes, header, err = projectColumns(rowSet.Columns, request.Columns); err != nil {
				return err
			}
			if err := setXlsxRow(sw, rowIdx, header); err != nil {
				return fmt.Errorf("write header: %w", err)
			}
			rowIdx++
		}
		for _, values := range rowSet.Rows {
			record := make([]any, len(indexes))
			for i, index := range indexes {
				record[i] = xlsxCellValue(values[index])
			}
			if err := setXlsxRow(sw, rowIdx, record); err != nil {
				return fmt.Errorf("write row %d: %w", rowIdx, err)
			}
			rowIdx++
		}
		if request.Scope == ExportScopePage || len(rowSet.Rows) < pageSize {
			break
		}
		pageIndex++
	}

	if err := sw.Flush(); err != nil {
		return fmt.Errorf("flush stream: %w", err)
	}
	if _, err := f.WriteTo(w); err != nil {
		return fmt.Errorf("write workbook: %w", err)
	}
	return nil
}

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
func xlsxCellValue(v any) any {
	switch val := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(val)
	case time.Time:
		return val
	default:
		return val
	}
}
