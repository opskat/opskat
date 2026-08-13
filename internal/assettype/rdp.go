package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type rdpHandler struct{}

func init() {
	Register(&rdpHandler{})
}

func (h *rdpHandler) Type() string     { return asset_entity.AssetTypeRDP }
func (h *rdpHandler) DefaultPort() int { return 3389 }

func (h *rdpHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetRDPConfig()
	if err != nil || cfg == nil {
		return nil
	}
	return map[string]any{
		"host": cfg.Host, "port": cfg.Port,
		"username": cfg.Username, "domain": cfg.Domain,
		"width": cfg.Width, "height": cfg.Height,
		"clipboard": cfg.Clipboard,
	}
}

func (h *rdpHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetRDPConfig()
	if err != nil {
		return "", fmt.Errorf("get RDP config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

func (h *rdpHandler) DefaultPolicy() any { return nil }
func (h *rdpHandler) PolicyKind() string { return "" }

func (h *rdpHandler) ValidateCreateArgs(args map[string]any) error {
	if ArgString(args, "host") == "" || ArgString(args, "username") == "" {
		return fmt.Errorf("missing required parameters: host, username")
	}
	return nil
}

func (h *rdpHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.RDPConfig{
		Host:         ArgString(args, "host"),
		Port:         ArgInt(args, "port"),
		Username:     ArgString(args, "username"),
		CredentialID: ArgInt64(args, "credential_id"),
		Domain:       ArgString(args, "domain"),
		Width:        ArgInt(args, "width"),
		Height:       ArgInt(args, "height"),
		Clipboard:    ArgBool(args, "clipboard"),
	}
	if cfg.Port == 0 {
		cfg.Port = h.DefaultPort()
	}
	if _, ok := args["clipboard"]; !ok {
		cfg.Clipboard = true
	}
	if cfg.Width == 0 {
		cfg.Width = 1280
	}
	if cfg.Height == 0 {
		cfg.Height = 720
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt RDP password: %w", err)
		}
		cfg.Password = encrypted
	}
	return a.SetRDPConfig(cfg)
}

func (h *rdpHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetRDPConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &asset_entity.RDPConfig{}
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
	if _, ok := args["domain"]; ok {
		cfg.Domain = ArgString(args, "domain")
	}
	if v := ArgInt(args, "width"); v > 0 {
		cfg.Width = v
	}
	if v := ArgInt(args, "height"); v > 0 {
		cfg.Height = v
	}
	if _, ok := args["clipboard"]; ok {
		cfg.Clipboard = ArgBool(args, "clipboard")
	}
	if _, ok := args["credential_id"]; ok {
		cfg.CredentialID = ArgInt64(args, "credential_id")
		cfg.Password = ""
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt RDP password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0
	}
	return a.SetRDPConfig(cfg)
}
