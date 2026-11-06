package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/query_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// xlsxExportTimeout 是一次 XLSX 导出的时限：整张表的导出可能很长，不受资产的查询超时限制。
const xlsxExportTimeout = 30 * time.Minute

// ExportTableToXlsx 按结构化的请求（筛选条件、排序项、范围、列）经资产的驱动取出表里的行，
// 在服务端流式写成 XLSX 工作簿到 filePath，大表导出不经过前端。
func (q *Query) ExportTableToXlsx(assetID int64, database, table string, request query_svc.TableExportRequest, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("导出文件路径为空")
	}
	return q.withPanelSession(assetID, database, func(*asset_entity.DatabaseConfig) time.Duration { return xlsxExportTimeout },
		func(ctx context.Context, session dbdriver.Session, database string) error {
			f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644) //nolint:gosec // user-selected export path
			if err != nil {
				return fmt.Errorf("创建导出文件失败: %w", err)
			}
			opErr := query_svc.ExportTableRowsToXlsx(ctx, session, database, table, request, f)
			if closeErr := f.Close(); closeErr != nil && opErr == nil {
				opErr = fmt.Errorf("关闭导出文件失败: %w", closeErr)
			}
			return opErr
		})
}

// ParseXlsx decodes a base64-encoded XLSX file (read in the frontend from a
// browser File) and returns the named sheet — or the first sheet when sheet is
// empty — as a ParsedXlsxTable JSON ({ headers, rows }) matching the frontend
// ParsedDelimitedTable so it flows straight into the import preview pipeline.
func (q *Query) ParseXlsx(base64Content, sheet string) (*query_svc.ParsedXlsxTable, error) {
	if base64Content == "" {
		return nil, fmt.Errorf("导入的 Excel 内容为空")
	}
	data, err := base64.StdEncoding.DecodeString(base64Content)
	if err != nil {
		return nil, fmt.Errorf("解码 Excel 内容失败: %w", err)
	}
	return query_svc.ParseXlsx(bytes.NewReader(data), sheet)
}
