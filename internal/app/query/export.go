package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/query_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// TableExportWriteOptions controls how exported table data is written to disk.
type TableExportWriteOptions struct {
	Encoding string `json:"encoding"`
	Append   bool   `json:"append"`
}

// SelectTableExportFile opens a native save dialog for table exports.
func (q *Query) SelectTableExportFile(defaultFilename, filterName, pattern string) (string, error) {
	if filterName == "" {
		filterName = "Export Files"
	}
	if pattern == "" {
		pattern = "*.*"
	}
	filePath, err := wailsRuntime.SaveFileDialog(q.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Export Data",
		DefaultFilename: defaultFilename,
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: filterName, Pattern: pattern},
		},
	})
	if err != nil {
		return "", fmt.Errorf("save file dialog failed: %w", err)
	}
	return filePath, nil
}

// WriteTableExportFile writes exported table content to the path chosen by the user.
func (q *Query) WriteTableExportFile(filePath, content string, options *TableExportWriteOptions) error {
	if filePath == "" {
		return fmt.Errorf("export file path is empty")
	}
	opts := TableExportWriteOptions{}
	if options != nil {
		opts = *options
	}
	return writeTableExportFile(filePath, content, opts)
}

func writeTableExportFile(filePath, content string, options TableExportWriteOptions) (err error) {
	suppressBOM := false
	if options.Append {
		if info, statErr := os.Stat(filePath); statErr == nil && info.Size() > 0 {
			suppressBOM = true
		}
	}
	data, err := encodeTableExportContent(content, options.Encoding, suppressBOM)
	if err != nil {
		return err
	}

	flag := os.O_CREATE | os.O_WRONLY
	if options.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(filePath, flag, 0644) //nolint:gosec // user-selected export path
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	_, err = f.Write(data)
	return err
}

func encodeTableExportContent(content, charset string, suppressBOM bool) ([]byte, error) {
	normalized := strings.ToLower(strings.TrimSpace(charset))
	if normalized == "" || normalized == "utf-8" || normalized == "utf8" || normalized == "65001" {
		return []byte(content), nil
	}
	if normalized == "utf-8-bom" || normalized == "utf8-bom" {
		if suppressBOM {
			return []byte(content), nil
		}
		return append([]byte{0xef, 0xbb, 0xbf}, []byte(content)...), nil
	}

	enc, bom := tableExportTextEncoding(normalized)
	if enc == nil {
		return nil, fmt.Errorf("unsupported export encoding: %s", charset)
	}
	data, _, err := transform.Bytes(enc.NewEncoder(), []byte(content))
	if err != nil {
		return nil, fmt.Errorf("encode export content as %s: %w", charset, err)
	}
	if len(bom) > 0 && !suppressBOM {
		data = append(bytes.Clone(bom), data...)
	}
	return data, nil
}

func tableExportTextEncoding(charset string) (encoding.Encoding, []byte) {
	switch strings.ReplaceAll(charset, "_", "-") {
	case "utf-16le", "utf16le":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), []byte{0xff, 0xfe}
	case "utf-16be", "utf16be":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), []byte{0xfe, 0xff}
	case "gb18030", "54936":
		return simplifiedchinese.GB18030, nil
	case "gbk", "cp936", "936":
		return simplifiedchinese.GBK, nil
	case "big5", "950":
		return traditionalchinese.Big5, nil
	case "shift-jis", "shiftjis", "sjis", "cp932", "932":
		return japanese.ShiftJIS, nil
	case "iso-8859-1", "latin1", "latin-1", "28591":
		return charmap.ISO8859_1, nil
	case "windows-1252", "cp1252", "1252":
		return charmap.Windows1252, nil
	default:
		return nil, nil
	}
}

// 整表导出 CSV / TSV / SQL：分隔文本的格式化（日期、编码、分隔符……）在前端，所以后端把对驱动
// 的那一次读取做成一个游标，分批交给前端。取数始终只有一次（StreamTableRows），不会因为分批而
// 变成多条带偏移的语句。导出为 SQL 时游标直接交出驱动生成的 INSERT。

// TableExportStreamRequest 是整表导出取数的请求：结构化的筛选条件与排序项，没有 SQL。
type TableExportStreamRequest struct {
	Filters []dbdriver.FilterItem `json:"filters"`
	Sorts   []dbdriver.SortItem   `json:"sorts"`
	// InsertColumns 非空表示导出为 SQL：每批交出把这些列写进表的 INSERT（驱动生成，每行一条，与
	// GetRowStatements 对同样的行给出的一字不差），不再交出行。生成要的一切在打开读取之前就向
	// 驱动备好：读取的游标一直占着会话的一条连接，远端 SQLite 的会话也只有这一条。
	InsertColumns []string `json:"insertColumns"`
}

// TableExportCursor 是打开的导出游标。
type TableExportCursor struct {
	ID      string   `json:"id"`
	Columns []string `json:"columns"`
}

// TableExportChunk 是游标读出的一批行；Done 表示游标已读完。
type TableExportChunk struct {
	Rows []map[string]any `json:"rows"`
	// Statements 是导出为 SQL（TableExportStreamRequest.InsertColumns）时这批行的 INSERT，每行一条；
	// 这时没有 Rows。
	Statements []string `json:"statements"`
	Done       bool     `json:"done"`
}

// tableExportTimeout 是一个导出游标的存活时限：到时即使前端没有关闭也会被收回。
const tableExportTimeout = 30 * time.Minute

type exportCursor struct {
	mu     sync.Mutex
	stream dbdriver.RowStream
	// writer 与 insertColumns 只在导出为 SQL 时有。
	writer        dbdriver.RowStatementWriter
	insertColumns []string
	panel         *panelSession
	stop          func() bool
	closed        bool
}

var (
	exportCursorsMu sync.Mutex
	exportCursors   = map[string]*exportCursor{}
)

func (c *exportCursor) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.stop()
	err := c.stream.Close()
	return c.panel.finish(err)
}

// OpenTableExport 经资产的驱动打开整张表的读取游标（一次查询，不分页）。用完必须
// CloseTableExport；前端不关闭时到时限自动收回。
func (q *Query) OpenTableExport(assetID int64, database, table string, request TableExportStreamRequest) (*TableExportCursor, error) {
	log := logger.Ctx(i18n.Ctx(q.ctx, q.lang.Lang())).With(
		zap.String("op", "export-stream"), zap.Int64("assetID", assetID), zap.String("database", database), zap.String("table", table))
	log.Debug("table export open", zap.Bool("insertStatements", len(request.InsertColumns) > 0))
	panel, err := q.acquirePanelSession(assetID, database,
		func(*asset_entity.DatabaseConfig) time.Duration { return tableExportTimeout })
	if err != nil {
		log.Error("table export open failed", zap.Error(err))
		return nil, err
	}
	cursor := &exportCursor{panel: panel, insertColumns: request.InsertColumns}
	if len(request.InsertColumns) > 0 {
		if cursor.writer, err = panel.session.PrepareRowStatements(panel.ctx, panel.database, table); err != nil {
			err = panel.finish(err)
			log.Error("table export open failed", zap.Error(err))
			return nil, err
		}
	}
	cursor.stream, err = panel.session.StreamTableRows(panel.ctx, panel.database, table,
		dbdriver.TableQuery{Filters: request.Filters, Sorts: request.Sorts})
	if err != nil {
		err = panel.finish(err)
		log.Error("table export open failed", zap.Error(err))
		return nil, err
	}

	id := uuid.NewString()
	cursor.stop = context.AfterFunc(panel.ctx, func() {
		exportCursorsMu.Lock()
		delete(exportCursors, id)
		exportCursorsMu.Unlock()
		if err := cursor.close(); err != nil {
			log.Warn("table export expired", zap.Error(err))
		}
	})
	exportCursorsMu.Lock()
	exportCursors[id] = cursor
	exportCursorsMu.Unlock()
	return &TableExportCursor{ID: id, Columns: cursor.stream.Columns()}, nil
}

func lookupExportCursor(id string) (*exportCursor, error) {
	exportCursorsMu.Lock()
	defer exportCursorsMu.Unlock()
	cursor, ok := exportCursors[id]
	if !ok {
		return nil, fmt.Errorf("导出游标不存在或已关闭")
	}
	return cursor, nil
}

// ReadTableExport 从游标读出至多 maxRows 行；游标读完时 Done 为 true。读取出错时错误原样返回，
// 游标仍须 CloseTableExport。
func (q *Query) ReadTableExport(id string, maxRows int) (*TableExportChunk, error) {
	if maxRows <= 0 {
		return nil, fmt.Errorf("每批行数必须为正数")
	}
	cursor, err := lookupExportCursor(id)
	if err != nil {
		return nil, err
	}
	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	if cursor.closed {
		return nil, fmt.Errorf("导出游标不存在或已关闭")
	}
	rowSet := &dbdriver.RowSet{Columns: cursor.stream.Columns()}
	done := false
	for len(rowSet.Rows) < maxRows {
		if !cursor.stream.Next() {
			if err := cursor.stream.Err(); err != nil {
				logger.Ctx(i18n.Ctx(q.ctx, q.lang.Lang())).Error("table export read failed", zap.Error(err))
				return nil, err
			}
			done = true
			break
		}
		rowSet.Rows = append(rowSet.Rows, cursor.stream.Row())
	}
	rows := query_svc.NewTableRows(rowSet).Rows
	if cursor.writer == nil {
		return &TableExportChunk{Rows: rows, Done: done}, nil
	}
	statements, err := cursor.insertStatements(rows)
	if err != nil {
		logger.Ctx(i18n.Ctx(q.ctx, q.lang.Lang())).Error("table export statements failed", zap.Error(err))
		return nil, err
	}
	return &TableExportChunk{Statements: statements, Done: done}, nil
}

// insertStatements 生成一批行的 INSERT。行先转成前端拿到再带回来的样子（经 JSON：nil、字符串、
// 数字、布尔），语句才与 GetRowStatements 对这些行给出的一字不差。
func (c *exportCursor) insertStatements(rows []map[string]any) ([]string, error) {
	data, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("encode export rows: %w", err)
	}
	var sent []map[string]any
	if err := json.Unmarshal(data, &sent); err != nil {
		return nil, fmt.Errorf("decode export rows: %w", err)
	}
	return c.writer.RowStatements(dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: c.insertColumns, Rows: sent})
}

// CloseTableExport 关闭导出游标并释放它占用的连接；已关闭的游标再关是空操作。
func (q *Query) CloseTableExport(id string) error {
	exportCursorsMu.Lock()
	cursor, ok := exportCursors[id]
	delete(exportCursors, id)
	exportCursorsMu.Unlock()
	if !ok {
		return nil
	}
	return cursor.close()
}
