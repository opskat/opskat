package custom_type_svc

import (
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
)

// 类型导入 / 导出文件格式（docs/specs/2026-09-28-generic-asset.md「导入、导出与
// 备份」，Design decision 16）。ExportFormat 标识文件是自定义类型导出文件；
// ExportFormatVersion 是格式版本号，向前不兼容的改动才递增。
const (
	ExportFormat        = "opskat-custom-type"
	ExportFormatVersion = 1
)

// wireType 是导出文件的 JSON 结构：只含类型定义本身（名称、标识、图标、执行方式、
// 字段结构、HTTP / 命令绑定、使用说明、默认策略），不含数据库自增 ID、创建 / 更新
// 时间，也不含任何资产、字段值或凭据。
type wireType struct {
	Format        string                            `json:"format"`
	Version       int                               `json:"version"`
	Name          string                            `json:"name"`
	Slug          string                            `json:"slug"`
	Icon          string                            `json:"icon"`
	ExecMode      string                            `json:"execMode"`
	Fields        []custom_type_entity.Field        `json:"fields"`
	HTTP          *custom_type_entity.HTTPConfig    `json:"http,omitempty"`
	Command       *custom_type_entity.CommandConfig `json:"command,omitempty"`
	Usage         string                            `json:"usage"`
	DefaultPolicy *policy.CommandPolicy             `json:"defaultPolicy"`
}

// ExportFileName 返回该类型导出文件的建议文件名：`<标识>.opskat-type.json`。
func ExportFileName(slug string) string {
	return slug + ".opskat-type.json"
}

// ExportType 把一个自定义类型序列化为导出文件内容。不含任何资产、字段值或凭据——
// ct 本身就没有携带这些（值只存在资产上，见 asset_entity.GenericConfig）。
func ExportType(ct *custom_type_entity.CustomType) ([]byte, error) {
	wire := wireType{
		Format: ExportFormat, Version: ExportFormatVersion,
		Name: ct.Name, Slug: ct.Slug, Icon: ct.Icon, ExecMode: ct.ExecMode,
		Fields: ct.Fields, HTTP: ct.HTTP, Command: ct.Command,
		Usage: ct.Usage, DefaultPolicy: ct.DefaultPolicy,
	}
	data, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化自定义类型失败: %w", err)
	}
	return data, nil
}

// ParseImportFile 解析并校验一个类型导入文件，返回可直接传给 Save 的类型（ID 为
// 0）。格式标识不认识、格式版本不支持、或类型本身校验不通过（含未注册的认证
// 类型、未知函数——都经 CustomType.Validate 复用与保存类型时相同的规则）时返回
// *custom_type_entity.ValidationError，说明拒绝原因。标识是否与现有类型冲突不在
// 这里判断，由调用方在确认导入时通过 Save 检测（Design decision 16：冲突时必须
// 换标识，不能覆盖）。
func ParseImportFile(data []byte) (*custom_type_entity.CustomType, error) {
	var wire wireType
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("解析类型文件失败: %w", err)
	}
	if wire.Format != ExportFormat {
		return nil, &custom_type_entity.ValidationError{Issues: []custom_type_entity.Issue{
			{Path: "format", Message: fmt.Sprintf("不是自定义类型文件（format=%q）", wire.Format)},
		}}
	}
	if wire.Version != ExportFormatVersion {
		return nil, &custom_type_entity.ValidationError{Issues: []custom_type_entity.Issue{
			{Path: "format", Message: fmt.Sprintf("不支持的格式版本 %d（当前支持 %d）", wire.Version, ExportFormatVersion)},
		}}
	}
	ct := &custom_type_entity.CustomType{
		Name: wire.Name, Slug: wire.Slug, Icon: wire.Icon, ExecMode: wire.ExecMode,
		Fields: wire.Fields, HTTP: wire.HTTP, Command: wire.Command,
		Usage: wire.Usage, DefaultPolicy: wire.DefaultPolicy,
	}
	if err := ct.Validate(); err != nil {
		return nil, err
	}
	return ct, nil
}
