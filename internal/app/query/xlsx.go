package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/query_svc"
)

// ExportTableToXlsx runs selectSQL against a database asset and streams the
// result set into an XLSX workbook at filePath. The query (with filters /
// ordering / scope) is built by the frontend via buildTableExportSelectSql;
// rows are streamed server-side so large exports never enter the frontend.
func (q *Query) ExportTableToXlsx(assetID int64, database, selectSQL, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("导出文件路径为空")
	}
	if selectSQL == "" {
		return fmt.Errorf("导出查询为空")
	}

	ctx0 := i18n.Ctx(q.ctx, q.lang.Lang())
	asset, err := asset_svc.Asset().Get(ctx0, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return fmt.Errorf("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return fmt.Errorf("获取数据库配置失败: %w", err)
	}
	if database != "" {
		cfg.Database = database
	}
	password, err := credential_resolver.Default().ResolveDatabasePassword(ctx0, cfg)
	if err != nil {
		return fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx0, 30*time.Minute)
	defer cancel()

	db, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return finishPanelDBOperation(fmt.Errorf("打开数据库会话失败: %w", err), cleanup)
	}

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644) //nolint:gosec // user-selected export path
	if err != nil {
		opErr := fmt.Errorf("创建导出文件失败: %w", err)
		if closeErr := conn.Close(); closeErr != nil && !isExpectedPanelCloseErr(closeErr) {
			opErr = errors.Join(opErr, fmt.Errorf("关闭数据库会话失败: %w", closeErr))
		}
		return finishPanelDBOperation(opErr, cleanup)
	}

	opErr := query_svc.ExportRowsToXlsx(ctx, conn, selectSQL, f)
	if closeErr := f.Close(); closeErr != nil && opErr == nil {
		opErr = fmt.Errorf("关闭导出文件失败: %w", closeErr)
	}
	if closeErr := conn.Close(); closeErr != nil && !isExpectedPanelCloseErr(closeErr) {
		opErr = errors.Join(opErr, fmt.Errorf("关闭数据库会话失败: %w", closeErr))
	}
	return finishPanelDBOperation(opErr, cleanup)
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
