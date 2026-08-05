package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"
)

type sshHandler struct{}

func init() {
	Register(&sshHandler{})
	policy.RegisterDefaultPolicy("ssh", func() any { return asset_entity.DefaultCommandPolicy() })
}

func (h *sshHandler) Type() string     { return asset_entity.AssetTypeSSH }
func (h *sshHandler) DefaultPort() int { return 22 }

func (h *sshHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetSSHConfig()
	if err != nil || cfg == nil {
		return nil
	}
	return map[string]any{
		"host": cfg.Host, "port": cfg.Port,
		"username": cfg.Username, "auth_type": cfg.AuthType,
	}
}

func (h *sshHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetSSHConfig()
	if err != nil {
		return "", fmt.Errorf("get SSH config failed: %w", err)
	}
	password, _, _, err := credential_resolver.Default().ResolveSSHCredentials(ctx, cfg)
	return password, err
}

func (h *sshHandler) DefaultPolicy() any { return asset_entity.DefaultCommandPolicy() }
func (h *sshHandler) PolicyKind() string { return policy.PolicyKindCommand }

func (h *sshHandler) ValidateCreateArgs(args map[string]any) error {
	if err := validateRemoteServerArgs(args); err != nil {
		return err
	}
	return validateSSHAgentArgs(args)
}

// validateSSHAgentArgs 校验 AI 工具创建/更新 SSH 资产时的 Agent 参数（不触库）：
// auth_type=agent 必须带正数来源 ID 与规范指纹，且与密码/私钥/口令互斥；非 Agent
// 认证方式不得携带 Agent 来源字段。来源存在性（引用完整性）在 Apply*Args 里查库。
func validateSSHAgentArgs(args map[string]any) error {
	authType := ArgString(args, "auth_type")
	if authType == "" {
		return nil
	}
	if authType == asset_entity.AuthTypeAgent {
		sourceID := ArgInt64(args, "agent_source_id")
		fp := ArgString(args, "agent_key_fingerprint")
		if sourceID <= 0 {
			return fmt.Errorf("SSH Agent 认证需要有效的来源 ID")
		}
		if err := asset_entity.ValidateFingerprintSHA256(fp); err != nil {
			return fmt.Errorf("SSH Agent 认证指纹无效: %w", err)
		}
		if ArgString(args, "password") != "" || ArgString(args, "private_key") != "" || ArgString(args, "passphrase") != "" || ArgInt64(args, "credential_id") != 0 {
			return fmt.Errorf("SSH Agent 认证与托管凭据/密码/私钥/口令互斥")
		}
		return nil
	}
	if ArgInt64(args, "agent_source_id") != 0 || ArgString(args, "agent_key_fingerprint") != "" {
		return fmt.Errorf("非 Agent 认证方式不得携带 Agent 来源字段")
	}
	return nil
}

func (h *sshHandler) ApplyCreateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error {
	authType := ArgString(args, "auth_type")
	password := ArgString(args, "password")
	privateKey := ArgString(args, "private_key")
	if authType == "" {
		if privateKey != "" {
			authType = "key"
		} else {
			authType = "password"
		}
	}

	cfg := &asset_entity.SSHConfig{
		Host:     ArgString(args, "host"),
		Port:     ArgInt(args, "port"),
		Username: ArgString(args, "username"),
		AuthType: authType,
	}

	if authType == asset_entity.AuthTypeAgent {
		// Agent 模式：仅保存来源 + 规范指纹，绝不落密码/密钥材料（序列化器权威）。
		if password != "" || privateKey != "" {
			return fmt.Errorf("SSH Agent 认证与密码/私钥互斥")
		}
		sourceID := ArgInt64(args, "agent_source_id")
		fp := ArgString(args, "agent_key_fingerprint")
		if sourceID <= 0 {
			return fmt.Errorf("SSH Agent 认证需要有效的来源 ID")
		}
		if err := asset_entity.ValidateFingerprintSHA256(fp); err != nil {
			return fmt.Errorf("SSH Agent 认证指纹无效: %w", err)
		}
		if err := ssh_agent_svc.RequireSourceExists(ctx, sourceID); err != nil {
			return err
		}
		cfg.AgentSourceID = sourceID
		cfg.AgentKeyFingerprint = fp
	} else if privateKey != "" {
		credName := a.Name
		if credName == "" {
			credName = "ai-imported-key"
		}
		cred, err := credential_mgr_svc.ImportSSHKeyFromPEM(ctx, credName, "", privateKey, ArgString(args, "passphrase"), ArgString(args, "username"))
		if err != nil {
			return fmt.Errorf("import SSH key: %w", err)
		}
		cfg.CredentialID = cred.ID
	} else if password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt SSH password: %w", err)
		}
		cfg.Password = encrypted
	}

	return a.SetSSHConfig(cfg)
}

func (h *sshHandler) ApplyUpdateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetSSHConfig()
	if err != nil || cfg == nil {
		return err
	}
	if v := ArgString(args, "host"); v != "" {
		cfg.Host = v
	}
	if v := ArgInt(args, "port"); v > 0 {
		cfg.Port = v
	}
	if v := ArgString(args, "username"); v != "" {
		cfg.Username = v
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt SSH password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0 // 切换为内联密码，与原先关联的统一凭证解绑
		cfg.AuthType = "password"
	}
	if privateKey := ArgString(args, "private_key"); privateKey != "" {
		credName := a.Name
		if credName == "" {
			credName = "ai-imported-key"
		}
		cred, err := credential_mgr_svc.ImportSSHKeyFromPEM(ctx, credName, "", privateKey, ArgString(args, "passphrase"), ArgString(args, "username"))
		if err != nil {
			return fmt.Errorf("import SSH key: %w", err)
		}
		cfg.CredentialID = cred.ID
		cfg.Password = ""
		cfg.AuthType = "key"
	}

	authType := ArgString(args, "auth_type")
	sourceID := ArgInt64(args, "agent_source_id")
	fp := ArgString(args, "agent_key_fingerprint")

	// 进入 Agent 模式：来源 + 指纹必须齐全且有效，引用完整性必须成立，并清除全部
	// 密码/密钥材料。只要带 Agent 字段即视为进入 Agent 模式（切到 Agent 删除密码
	// 与密钥材料）。
	if authType == asset_entity.AuthTypeAgent || sourceID > 0 || fp != "" {
		if sourceID <= 0 {
			return fmt.Errorf("SSH Agent 认证需要有效的来源 ID")
		}
		if err := asset_entity.ValidateFingerprintSHA256(fp); err != nil {
			return fmt.Errorf("SSH Agent 认证指纹无效: %w", err)
		}
		if err := ssh_agent_svc.RequireSourceExists(ctx, sourceID); err != nil {
			return err
		}
		cfg.AuthType = asset_entity.AuthTypeAgent
		cfg.AgentSourceID = sourceID
		cfg.AgentKeyFingerprint = fp
	} else if authType != "" {
		// 显式切换离开 Agent（或指定其它认证方式而不动凭据）。
		cfg.AuthType = authType
	}

	// 序列化器强制不变量（切换离开 Agent 删除 Agent 字段；Agent 模式清除密码与
	// 密钥材料），与实体层权威校验 validateSSHAgentContract 一致。
	if cfg.AuthType == asset_entity.AuthTypeAgent {
		cfg.CredentialID = 0
		cfg.Password = ""
		cfg.PrivateKeys = nil
		cfg.PrivateKeyPassphrase = ""
	} else {
		cfg.AgentSourceID = 0
		cfg.AgentKeyFingerprint = ""
	}
	return a.SetSSHConfig(cfg)
}
