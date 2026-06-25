package query_svc

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/xuri/excelize/v2"
)

const xlsxSheetName = "Sheet1"

// ExportRowsToXlsx runs selectSQL on conn and streams the result set into an
// XLSX workbook written to w. It uses excelize's StreamWriter so rows are
// flushed incrementally rather than held in memory, keeping large exports
// bounded. The first sheet row holds the column names.
func ExportRowsToXlsx(ctx context.Context, conn *sql.Conn, selectSQL string, w io.Writer) error {
	rows, err := conn.QueryContext(ctx, selectSQL)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("read columns: %w", err)
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	sw, err := f.NewStreamWriter(xlsxSheetName)
	if err != nil {
		return fmt.Errorf("create stream writer: %w", err)
	}

	header := make([]any, len(cols))
	for i, c := range cols {
		header[i] = c
	}
	headerCell, err := excelize.CoordinatesToCellName(1, 1)
	if err != nil {
		return fmt.Errorf("header cell: %w", err)
	}
	if err := sw.SetRow(headerCell, header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	rowIdx := 2
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		record := make([]any, len(cols))
		for i := range values {
			record[i] = xlsxCellValue(values[i])
		}
		cell, err := excelize.CoordinatesToCellName(1, rowIdx)
		if err != nil {
			return fmt.Errorf("row cell: %w", err)
		}
		if err := sw.SetRow(cell, record); err != nil {
			return fmt.Errorf("write row %d: %w", rowIdx, err)
		}
		rowIdx++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}

	if err := sw.Flush(); err != nil {
		return fmt.Errorf("flush stream: %w", err)
	}
	if _, err := f.WriteTo(w); err != nil {
		return fmt.Errorf("write workbook: %w", err)
	}
	return nil
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
