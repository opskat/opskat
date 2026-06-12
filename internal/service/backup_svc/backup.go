package backup_svc

import (
	"encoding/json"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/forward_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
)

// CredentialCrypto 凭证加解密接口
type CredentialCrypto interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// BackupCredential 凭据备份条目（含明文敏感字段）
type BackupCredential struct {
	credential_entity.Credential
	PlainPassword   string `json:"plain_password,omitempty"`
	PlainPrivateKey string `json:"plain_private_key,omitempty"`
}

// BackupForward 端口转发备份条目（config + rules 打包）
type BackupForward struct {
	forward_entity.ForwardConfig
	Rules []*forward_entity.ForwardRule `json:"rules"`
}

// BackupData 备份数据结构
type BackupData struct {
	Version             string                             `json:"version"`
	ExportedAt          string                             `json:"exported_at"`
	IncludesCredentials bool                               `json:"includes_credentials,omitempty"`
	Groups              []*group_entity.Group              `json:"groups,omitempty"`
	Assets              []*asset_entity.Asset              `json:"assets,omitempty"`
	Credentials         []*BackupCredential                `json:"credentials,omitempty"`
	PolicyGroups        []*policy_group_entity.PolicyGroup `json:"policy_groups,omitempty"`
	Forwards            []*BackupForward                   `json:"forwards,omitempty"`
	Shortcuts           json.RawMessage                    `json:"shortcuts,omitempty"`
	CustomThemes        json.RawMessage                    `json:"custom_themes,omitempty"`
}

// BackupSummary 备份概览信息（用于导入前预览）
type BackupSummary struct {
	Version             string `json:"version"`
	ExportedAt          string `json:"exported_at"`
	Encrypted           bool   `json:"encrypted"`
	IncludesCredentials bool   `json:"includes_credentials"`
	AssetCount          int    `json:"asset_count"`
	GroupCount          int    `json:"group_count"`
	CredentialCount     int    `json:"credential_count"`
	PolicyGroupCount    int    `json:"policy_group_count"`
	ForwardCount        int    `json:"forward_count"`
	HasShortcuts        bool   `json:"has_shortcuts"`
	HasCustomThemes     bool   `json:"has_custom_themes"`
}

// Summary 返回备份概览
func (d *BackupData) Summary() *BackupSummary {
	return &BackupSummary{
		Version:             d.Version,
		ExportedAt:          d.ExportedAt,
		IncludesCredentials: d.IncludesCredentials,
		AssetCount:          len(d.Assets),
		GroupCount:          len(d.Groups),
		CredentialCount:     len(d.Credentials),
		PolicyGroupCount:    len(d.PolicyGroups),
		ForwardCount:        len(d.Forwards),
		HasShortcuts:        len(d.Shortcuts) > 0,
		HasCustomThemes:     len(d.CustomThemes) > 0,
	}
}

// ExportOptions 导出选项
type ExportOptions struct {
	AssetIDs            []int64 `json:"asset_ids"`             // 空=全部
	IncludeCredentials  bool    `json:"include_credentials"`   // 包含凭据（强制加密）
	IncludeForwards     bool    `json:"include_forwards"`      // 包含端口转发
	IncludePolicyGroups bool    `json:"include_policy_groups"` // 包含策略组
	IncludeShortcuts    bool    `json:"include_shortcuts"`     // 包含快捷键
	IncludeThemes       bool    `json:"include_themes"`        // 包含终端主题
	Shortcuts           string  `json:"shortcuts,omitempty"`   // JSON 字符串
	CustomThemes        string  `json:"custom_themes,omitempty"`
}

// ImportOptions 导入选项
type ImportOptions struct {
	ImportAssets       bool   `json:"import_assets"`
	ImportCredentials  bool   `json:"import_credentials"`
	ImportForwards     bool   `json:"import_forwards"`
	ImportPolicyGroups bool   `json:"import_policy_groups"`
	ImportShortcuts    bool   `json:"import_shortcuts"`
	ImportThemes       bool   `json:"import_themes"`
	Mode               string `json:"mode"` // "replace" | "merge"
}

// ImportResult 导入结果
type ImportResult struct {
	AssetsImported       int    `json:"assets_imported"`
	GroupsImported       int    `json:"groups_imported"`
	CredentialsImported  int    `json:"credentials_imported"`
	PolicyGroupsImported int    `json:"policy_groups_imported"`
	ForwardsImported     int    `json:"forwards_imported"`
	Shortcuts            string `json:"shortcuts,omitempty"` // JSON 字符串，前端处理
	CustomThemes         string `json:"custom_themes,omitempty"`
}
