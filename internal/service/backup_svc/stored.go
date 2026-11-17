package backup_svc

import (
	"errors"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/bootstrap"
)

// ErrWebDAVNotConfigured 表示配置里没有 WebDAV 地址。
var ErrWebDAVNotConfigured = errors.New("WebDAV 未配置")

// StoredWebDAVConfig 读取 config.json 中已保存的 WebDAV 连接，并解密密码 / token。
func StoredWebDAVConfig(crypto CredentialCrypto) (WebDAVConfig, error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil || strings.TrimSpace(cfg.WebDAVURL) == "" {
		return WebDAVConfig{}, ErrWebDAVNotConfigured
	}
	authType := WebDAVAuthType(cfg.WebDAVAuthType)
	if authType == "" {
		authType = WebDAVAuthNone
	}
	out := WebDAVConfig{
		URL:      cfg.WebDAVURL,
		AuthType: authType,
		Username: cfg.WebDAVUsername,
	}
	if cfg.WebDAVPassword != "" {
		decrypted, err := crypto.Decrypt(cfg.WebDAVPassword)
		if err != nil {
			return WebDAVConfig{}, fmt.Errorf("解密 WebDAV 密码失败: %w", err)
		}
		out.Password = decrypted
	}
	if cfg.WebDAVToken != "" {
		decrypted, err := crypto.Decrypt(cfg.WebDAVToken)
		if err != nil {
			return WebDAVConfig{}, fmt.Errorf("解密 WebDAV token 失败: %w", err)
		}
		out.Token = decrypted
	}
	return out, nil
}

// StoredGitHubToken 返回已保存的 GitHub token（解密后）与用户名；未登录时 token 为空。
func StoredGitHubToken(crypto CredentialCrypto) (token, user string, err error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil || cfg.GitHubToken == "" {
		return "", "", nil
	}
	token, err = crypto.Decrypt(cfg.GitHubToken)
	if err != nil {
		return "", "", fmt.Errorf("解密 GitHub Token 失败: %w", err)
	}
	return token, cfg.GitHubUser, nil
}
