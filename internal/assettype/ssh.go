package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
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
	view := map[string]any{
		"host": cfg.Host, "port": cfg.Port,
		"username": cfg.Username, "auth_type": cfg.AuthType,
	}
	// Agent 认证资产：AI 安全视图对称返回来源 ID 与规范指纹（规格允许）。
	// 端点/公钥/备注/签名/挑战答案不落在 SSHConfig 里，安全视图绝不包含它们。
	if cfg.AuthType == asset_entity.AuthTypeAgent {
		view["agent_source_id"] = cfg.AgentSourceID
		view["agent_key_fingerprint"] = cfg.AgentKeyFingerprint
	}
	return view
}

func (h *sshHandler) AuthenticationAssociation(a *asset_entity.Asset) (AuthenticationAssociation, bool, error) {
	cfg, err := a.GetSSHConfig()
	if err != nil || cfg == nil {
		return AuthenticationAssociation{}, false, err
	}
	if cfg.AuthType == asset_entity.AuthTypeAgent {
		return AuthenticationAssociation{
			Type: "ssh_agent", Ref: fmt.Sprintf("agent-source:%d", cfg.AgentSourceID), Fingerprint: cfg.AgentKeyFingerprint,
		}, true, nil
	}
	if cfg.CredentialID <= 0 {
		return AuthenticationAssociation{}, false, nil
	}
	authType := credential_entity.TypePassword
	if cfg.AuthType == asset_entity.AuthTypeKey {
		authType = credential_entity.TypeSSHKey
	}
	return AuthenticationAssociation{Type: authType, Ref: fmt.Sprintf("credential:%d", cfg.CredentialID)}, true, nil
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

func (h *sshHandler) ValidateAutomationConfig(args map[string]any) error {
	return validateSSHAgentArgs(args)
}

// NormalizeAutomationConfig infers Agent auth from its dedicated identity pair
// without exposing SSH rules to the shared dispatcher.
func (h *sshHandler) NormalizeAutomationConfig(args map[string]any) error {
	if ArgString(args, "auth_type") != "" {
		return nil
	}
	sourceID := ArgInt64(args, "agent_source_id")
	fingerprint := ArgString(args, "agent_key_fingerprint")
	if sourceID == 0 && fingerprint == "" {
		return nil
	}
	if sourceID <= 0 || fingerprint == "" {
		return fmt.Errorf("SSH Agent authentication requires both agent_source_id and agent_key_fingerprint")
	}
	args["auth_type"] = asset_entity.AuthTypeAgent
	return nil
}

// PrepareAutomationAuthentication validates persisted SSH Agent source references
// during the side-effect-free prepare phase and describes their safe association.
func (h *sshHandler) PrepareAutomationAuthentication(ctx context.Context, args map[string]any) (string, int64, bool, error) {
	if ArgString(args, "auth_type") != asset_entity.AuthTypeAgent {
		return "", 0, false, nil
	}
	sourceID := ArgInt64(args, "agent_source_id")
	if err := ssh_agent_svc.RequireSourceExists(ctx, sourceID); err != nil {
		return "", 0, false, err
	}
	return "ssh_agent", sourceID, true, nil
}

// validateSSHAgentArgs 校验 AI 工具创建/更新 SSH 资产时的 Agent 参数（不触库）：
// auth_type=agent 必须带正数来源 ID 与规范指纹，且与密码/私钥/口令互斥；非 Agent
// 认证方式不得携带 Agent 来源字段。来源存在性由共享 Prepare 边界校验，Apply*Args
// 仍兜底直接 handler 调用。
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
			return fmt.Errorf("SSH Agent authentication rejects password, private_key, passphrase, and credential_id inputs")
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
			authType = asset_entity.AuthTypeKey
		} else {
			authType = asset_entity.AuthTypePassword
		}
	}

	cfg := &asset_entity.SSHConfig{
		Host:         ArgString(args, "host"),
		Port:         ArgInt(args, "port"),
		Username:     ArgString(args, "username"),
		AuthType:     authType,
		CredentialID: ArgInt64(args, "credential_id"),
	}

	if authType == asset_entity.AuthTypeAgent {
		sourceID := ArgInt64(args, "agent_source_id")
		if err := ssh_agent_svc.RequireSourceExists(ctx, sourceID); err != nil {
			return err
		}
		cfg.AgentSourceID = sourceID
		cfg.AgentKeyFingerprint = ArgString(args, "agent_key_fingerprint")
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
	if _, ok := args["credential_id"]; ok {
		cfg.CredentialID = ArgInt64(args, "credential_id")
		cfg.Password = ""
		cfg.PrivateKeys = nil
		cfg.PrivateKeyPassphrase = ""
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt SSH password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0 // legacy direct handler writes remain inline for compatibility
		cfg.PrivateKeys = nil
		cfg.PrivateKeyPassphrase = ""
		cfg.AuthType = asset_entity.AuthTypePassword
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
		cfg.PrivateKeys = nil
		cfg.PrivateKeyPassphrase = ""
		cfg.AuthType = asset_entity.AuthTypeKey
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
