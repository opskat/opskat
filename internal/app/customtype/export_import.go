package customtype

import (
	"errors"
	"fmt"
	"os"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// ImportPreview 是导入类型文件的预览结果（docs/specs/2026-09-28-generic-asset.md
// 「导入、导出与备份」）。Type 非 nil 且 Issues 为空时可以确认导入——前端直接把 Type
// 传给已有的 SaveCustomType（复用创建校验：标识不可用时返回同样的 issues，逼用户
// 换一个标识，不能覆盖）。Issues 非空时说明文件被拒绝（格式版本不认识、引用了未
// 注册的认证类型、未知函数，或其他与保存类型时相同的校验问题），Type 为 nil，前
// 端只展示原因，没有确认按钮。SlugTaken 只是提前提示：标识是否真的可用，仍由确认
// 时的 SaveCustomType 判定。
type ImportPreview struct {
	Type      *custom_type_entity.CustomType `json:"type,omitempty"`
	Issues    []custom_type_entity.Issue     `json:"issues,omitempty"`
	SlugTaken bool                           `json:"slugTaken"`
}

// ExportCustomType 把一个类型导出为 `<标识>.opskat-type.json`，经原生保存对话框
// 选择位置。用户取消对话框时返回 nil（不是错误）。
func (c *CustomType) ExportCustomType(id int64) error {
	ct, err := custom_type_svc.CustomType().Get(c.ctxWithLang(), id)
	if err != nil {
		return err
	}
	data, err := custom_type_svc.ExportType(ct)
	if err != nil {
		return err
	}
	filePath, err := wailsRuntime.SaveFileDialog(c.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: custom_type_svc.ExportFileName(ct.Slug),
		Filters:         []wailsRuntime.FileFilter{{DisplayName: "OpsKat Custom Type", Pattern: "*.opskat-type.json"}},
	})
	if err != nil {
		return fmt.Errorf("保存文件对话框失败: %w", err)
	}
	if filePath == "" {
		return nil
	}
	return os.WriteFile(filePath, data, 0o644)
}

// SelectImportTypeFile 选择一个类型导入文件并返回预览。用户取消对话框时返回
// nil, nil（不是错误）。
func (c *CustomType) SelectImportTypeFile() (*ImportPreview, error) {
	filePath, err := wailsRuntime.OpenFileDialog(c.ctx, wailsRuntime.OpenDialogOptions{
		Title:   "导入自定义类型",
		Filters: []wailsRuntime.FileFilter{{DisplayName: "OpsKat Custom Type", Pattern: "*.opskat-type.json"}},
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if filePath == "" {
		return nil, nil
	}

	data, err := os.ReadFile(filePath) //nolint:gosec // filePath 来自文件对话框
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	ctx := c.ctxWithLang()
	ct, err := custom_type_svc.ParseImportFile(data)
	if err != nil {
		var verr *custom_type_entity.ValidationError
		if errors.As(err, &verr) {
			return &ImportPreview{Issues: verr.Issues}, nil
		}
		return nil, err
	}

	preview := &ImportPreview{Type: ct}
	if _, err := custom_type_svc.CustomType().GetBySlug(ctx, ct.Slug); err == nil {
		preview.SlugTaken = true
	}
	return preview, nil
}
