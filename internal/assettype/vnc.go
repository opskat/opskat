package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type vncHandler struct{}

func init() {
	Register(&vncHandler{})
}

func (h *vncHandler) Type() string     { return asset_entity.AssetTypeVNC }
func (h *vncHandler) DefaultPort() int { return 5900 }

func (h *vncHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetVNCConfig()
	if err != nil || cfg == nil {
		return nil
	}
	return map[string]any{
		"host":              cfg.Host,
		"port":              cfg.Port,
		"username":          cfg.Username,
		"security_type":     cfg.SecurityType,
		"file_ssh_asset_id": cfg.FileSSHAssetID,
	}
}

func (h *vncHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetVNCConfig()
	if err != nil {
		return "", fmt.Errorf("get VNC config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

func (h *vncHandler) DefaultPolicy() any { return nil }
func (h *vncHandler) PolicyKind() string { return "" }

func (h *vncHandler) ValidateCreateArgs(args map[string]any) error {
	if ArgString(args, "host") == "" {
		return fmt.Errorf("missing required parameters: host")
	}
	return nil
}

func (h *vncHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.VNCConfig{
		Host:           ArgString(args, "host"),
		Port:           ArgInt(args, "port"),
		Username:       ArgString(args, "username"),
		SecurityType:   ArgString(args, "security_type"),
		FileSSHAssetID: ArgInt64(args, "file_ssh_asset_id"),
	}
	if cfg.Port == 0 {
		cfg.Port = h.DefaultPort()
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt VNC password: %w", err)
		}
		cfg.Password = encrypted
	}
	return a.SetVNCConfig(cfg)
}

func (h *vncHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetVNCConfig()
	if err != nil || cfg == nil {
		return err
	}
	if v := ArgString(args, "host"); v != "" {
		cfg.Host = v
	}
	if v := ArgInt(args, "port"); v > 0 {
		cfg.Port = v
	}
	if _, ok := args["username"]; ok {
		cfg.Username = ArgString(args, "username")
	}
	if _, ok := args["security_type"]; ok {
		cfg.SecurityType = ArgString(args, "security_type")
	}
	if _, ok := args["file_ssh_asset_id"]; ok {
		cfg.FileSSHAssetID = ArgInt64(args, "file_ssh_asset_id")
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt VNC password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0
	}
	return a.SetVNCConfig(cfg)
}
