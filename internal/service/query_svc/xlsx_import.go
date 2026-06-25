package query_svc

import (
	"fmt"
	"io"

	"github.com/xuri/excelize/v2"
)

// ParsedXlsxTable mirrors the frontend ParsedDelimitedTable shape
// ({ headers, rows }) so a parsed sheet can flow straight into the existing
// import preview/build pipeline without a separate frontend parser.
type ParsedXlsxTable struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// ParseXlsx reads an XLSX workbook from r and returns the named sheet (or the
// first sheet when sheet is empty) as a header row plus string rows. Data rows
// are padded/truncated to the header width so ragged sheets stay rectangular.
func ParseXlsx(r io.Reader, sheet string) (*ParsedXlsxTable, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	if sheet == "" {
		sheet = f.GetSheetName(0)
		if sheet == "" {
			return nil, fmt.Errorf("workbook has no sheets")
		}
	}

	raw, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	if len(raw) == 0 {
		return &ParsedXlsxTable{Headers: []string{}, Rows: [][]string{}}, nil
	}

	headers := raw[0]
	width := len(headers)
	dataRows := make([][]string, 0, len(raw)-1)
	for _, row := range raw[1:] {
		dataRows = append(dataRows, fitRow(row, width))
	}
	return &ParsedXlsxTable{Headers: headers, Rows: dataRows}, nil
}

// fitRow pads with empty strings or truncates a row to exactly width columns.
func fitRow(row []string, width int) []string {
	out := make([]string, width)
	copy(out, row)
	return out
}
