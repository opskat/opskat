package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type ossHandler struct{}

func init() { Register(&ossHandler{}) }

func (h *ossHandler) Type() string     { return asset_entity.AssetTypeOSS }
func (h *ossHandler) DefaultPort() int { return 0 }

func (h *ossHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetOSSConfig()
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{
		"provider":       cfg.Provider,
		"endpoint":       cfg.Endpoint,
		"region":         cfg.Region,
		"access_key_id":  cfg.AccessKeyID,
		"use_path_style": cfg.UsePathStyle,
		"use_ssl":        cfg.UseSSL,
		// SecretAccessKey / CredentialID 故意不返回 —— 绝不泄密
	}
}

func (h *ossHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetOSSConfig()
	if err != nil {
		return "", fmt.Errorf("get oss config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

func (h *ossHandler) DefaultPolicy() any { return nil }
func (h *ossHandler) PolicyKind() string { return "" }

func (h *ossHandler) ValidateCreateArgs(args map[string]any) error {
	if ArgString(args, "endpoint") == "" {
		return fmt.Errorf("missing required parameter: endpoint")
	}
	if ArgString(args, "access_key_id") == "" {
		return fmt.Errorf("missing required parameter: access_key_id")
	}
	return nil
}

func (h *ossHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.OSSConfig{
		Provider:       ArgString(args, "provider"),
		Endpoint:       ArgString(args, "endpoint"),
		Region:         ArgString(args, "region"),
		AccessKeyID:    ArgString(args, "access_key_id"),
		CredentialID:   ArgInt64(args, "credential_id"),
		UsePathStyle:   ArgBool(args, "use_path_style"),
		UseSSL:         ArgBool(args, "use_ssl"),
		ConnectTimeout: ArgInt(args, "connect_timeout"),
	}
	if secret := ArgString(args, "secret_access_key"); secret != "" {
		encrypted, err := credential_svc.Default().Encrypt(secret)
		if err != nil {
			return fmt.Errorf("encrypt oss secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
		cfg.CredentialID = 0
	}
	return a.SetOSSConfig(cfg)
}

func (h *ossHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetOSSConfig()
	if err != nil {
		return err
	}
	if _, ok := args["provider"]; ok {
		cfg.Provider = ArgString(args, "provider")
	}
	if _, ok := args["endpoint"]; ok {
		cfg.Endpoint = ArgString(args, "endpoint")
	}
	if _, ok := args["region"]; ok {
		cfg.Region = ArgString(args, "region")
	}
	if _, ok := args["access_key_id"]; ok {
		cfg.AccessKeyID = ArgString(args, "access_key_id")
	}
	if _, ok := args["use_path_style"]; ok {
		cfg.UsePathStyle = ArgBool(args, "use_path_style")
	}
	if _, ok := args["use_ssl"]; ok {
		cfg.UseSSL = ArgBool(args, "use_ssl")
	}
	if _, ok := args["connect_timeout"]; ok {
		cfg.ConnectTimeout = ArgInt(args, "connect_timeout")
	}
	if _, ok := args["credential_id"]; ok {
		cfg.CredentialID = ArgInt64(args, "credential_id")
	}
	if secret := ArgString(args, "secret_access_key"); secret != "" {
		encrypted, err := credential_svc.Default().Encrypt(secret)
		if err != nil {
			return fmt.Errorf("encrypt oss secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
		cfg.CredentialID = 0
	}
	if err := a.SetOSSConfig(cfg); err != nil {
		return err
	}
	connpool.InvalidateOSS(a.ID)
	return nil
}
