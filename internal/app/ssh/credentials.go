package ssh

import (
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/ssh_svc"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// resolveSSHCredentialsFull 解析 SSH 凭据，返回密码、密钥、passphrase
func (s *SSH) resolveSSHCredentialsFull(sshCfg *asset_entity.SSHConfig) (password, key, passphrase string) {
	p, k, pp, err := credential_resolver.Default().ResolveSSHCredentials(i18n.Ctx(s.ctx, s.lang.Lang()), sshCfg)
	if err != nil {
		logger.Default().Warn("resolve SSH credentials", zap.Error(err))
	}
	return p, k, pp
}

// decryptProxyPassword 解密代理配置中的密码（委托给 credential_resolver）
func (s *SSH) decryptProxyPassword(proxy *asset_entity.ProxyConfig) *asset_entity.ProxyConfig {
	return credential_resolver.Default().DecryptProxyPassword(proxy)
}

// resolveJumpHosts 递归解析跳板机链（委托给 credential_resolver，含凭据解密）
func (s *SSH) resolveJumpHosts(jumpHostID int64, maxDepth int) ([]ssh_svc.JumpHostEntry, error) {
	return credential_resolver.Default().ResolveJumpHosts(i18n.Ctx(s.ctx, s.lang.Lang()), jumpHostID, maxDepth)
}
