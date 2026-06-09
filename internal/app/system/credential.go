package system

import (
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"go.uber.org/zap"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// --- 凭证操作 ---

// EncryptPassword 加密密码，返回加密后的字符串（用于前端保存资产配置）
func (s *System) EncryptPassword(plaintext string) (string, error) {
	return credential_svc.Default().Encrypt(plaintext)
}

// GetAssetPassword 获取指定资产的解密密码（用于编辑时回看密码）
func (s *System) GetAssetPassword(assetID int64) (string, error) {
	ctx := i18n.Ctx(s.ctx, s.Lang())
	asset, err := asset_repo.Asset().Find(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("asset not found: %w", err)
	}

	h, ok := assettype.Get(asset.Type)
	if !ok {
		return "", fmt.Errorf("unsupported asset type: %s", asset.Type)
	}
	return h.ResolvePassword(ctx, asset)
}

// --- 密钥管理 ---

// ListCredentials 列出所有凭证
func (s *System) ListCredentials() ([]*credential_entity.Credential, error) {
	return credential_mgr_svc.List(i18n.Ctx(s.ctx, s.Lang()))
}

// ListCredentialsByType 按类型列出凭证
func (s *System) ListCredentialsByType(credType string) ([]*credential_entity.Credential, error) {
	return credential_mgr_svc.ListByType(i18n.Ctx(s.ctx, s.Lang()), credType)
}

// CreatePasswordCredential 创建密码凭证
func (s *System) CreatePasswordCredential(name, username, password, description string) (*credential_entity.Credential, error) {
	return credential_mgr_svc.CreatePassword(i18n.Ctx(s.ctx, s.Lang()), credential_mgr_svc.CreatePasswordRequest{
		Name:        name,
		Username:    username,
		Password:    password,
		Description: description,
	})
}

// GenerateSSHKey 生成新的 SSH 密钥对
func (s *System) GenerateSSHKey(name, comment, keyType string, keySize int, passphrase, username string) (*credential_entity.Credential, error) {
	return credential_mgr_svc.GenerateSSHKey(i18n.Ctx(s.ctx, s.Lang()), credential_mgr_svc.GenerateKeyRequest{
		Name:       name,
		Comment:    comment,
		Username:   username,
		KeyType:    keyType,
		KeySize:    keySize,
		Passphrase: passphrase,
	})
}

// ImportSSHKeyFile 通过文件选择框导入 SSH 密钥
func (s *System) ImportSSHKeyFile(name, comment, passphrase, username string) (*credential_entity.Credential, error) {
	filePath, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 SSH 私钥文件",
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if filePath == "" {
		return nil, nil
	}
	return credential_mgr_svc.ImportSSHKeyFromFile(i18n.Ctx(s.ctx, s.Lang()), name, comment, filePath, passphrase, username)
}

// ImportSSHKeyPath imports an SSH private key from a user-selected or dropped local file path.
func (s *System) ImportSSHKeyPath(name, comment, filePath, passphrase, username string) (*credential_entity.Credential, error) {
	ctx := i18n.Ctx(s.ctx, s.Lang())
	logger.Ctx(ctx).Info("import ssh key from path start")
	cred, err := credential_mgr_svc.ImportSSHKeyFromFile(ctx, name, comment, filePath, passphrase, username)
	if err != nil {
		logger.Ctx(ctx).Error("import ssh key from path failed", zap.Error(err))
		return nil, err
	}
	logger.Ctx(ctx).Info("import ssh key from path end", zap.Int64("credentialID", cred.ID))
	return cred, nil
}

// ImportSSHKeyPEM 通过粘贴 PEM 内容导入 SSH 密钥
func (s *System) ImportSSHKeyPEM(name, comment, pemData, passphrase, username string) (*credential_entity.Credential, error) {
	return credential_mgr_svc.ImportSSHKeyFromPEM(i18n.Ctx(s.ctx, s.Lang()), name, comment, pemData, passphrase, username)
}

// UpdateCredential 更新凭证
func (s *System) UpdateCredential(id int64, name, comment, description, username string) (*credential_entity.Credential, error) {
	return credential_mgr_svc.Update(i18n.Ctx(s.ctx, s.Lang()), credential_mgr_svc.UpdateRequest{
		ID:          id,
		Name:        name,
		Comment:     comment,
		Description: description,
		Username:    username,
	})
}

// UpdateCredentialPassword 更新密码凭证的密码
func (s *System) UpdateCredentialPassword(id int64, password string) error {
	return credential_mgr_svc.UpdatePassword(i18n.Ctx(s.ctx, s.Lang()), id, password)
}

// UpdateCredentialPassphrase 更新 SSH 密钥的 passphrase
// 需要提供旧的 passphrase 用于解密 PEM
func (s *System) UpdateCredentialPassphrase(id int64, oldPassphrase, newPassphrase string) error {
	return credential_mgr_svc.UpdatePassphrase(i18n.Ctx(s.ctx, s.Lang()), id, oldPassphrase, newPassphrase)
}

// ExportSSHPrivateKey exports an SSH key credential's decrypted private key to a user-selected file.
func (s *System) ExportSSHPrivateKey(id int64) (bool, error) {
	ctx := i18n.Ctx(s.ctx, s.Lang())
	cred, err := credential_mgr_svc.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if !cred.IsSSHKey() {
		return false, fmt.Errorf("凭证类型不是 SSH 密钥")
	}

	filePath, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "导出 SSH 私钥",
		DefaultFilename: privateKeyExportFilename(cred.Name),
	})
	if err != nil {
		return false, fmt.Errorf("保存文件对话框失败: %w", err)
	}
	if filePath == "" {
		return false, nil
	}
	if err := credential_mgr_svc.ExportSSHPrivateKey(ctx, id, filePath); err != nil {
		return false, err
	}
	return true, nil
}

// GetCredentialUsage 获取引用此凭证的资产名称列表
func (s *System) GetCredentialUsage(id int64) ([]string, error) {
	assets, err := asset_repo.Asset().FindByCredentialID(i18n.Ctx(s.ctx, s.Lang()), id)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(assets))
	for i, asset := range assets {
		names[i] = asset.Name
	}
	return names, nil
}

// DeleteCredential 删除凭证
func (s *System) DeleteCredential(id int64) error {
	return credential_mgr_svc.Delete(i18n.Ctx(s.ctx, s.Lang()), id)
}

// GetCredentialPublicKey 获取 SSH 密钥凭证的公钥（用于复制）
func (s *System) GetCredentialPublicKey(id int64) (string, error) {
	cred, err := credential_mgr_svc.Get(i18n.Ctx(s.ctx, s.Lang()), id)
	if err != nil {
		return "", err
	}
	return cred.PublicKey, nil
}

func privateKeyExportFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "id_opskat"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\x00", "_")
	return replacer.Replace(name)
}
