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
		"host":              cfg.Host,
		"port":              cfg.Port,
		"username":          cfg.Username,
		"domain":            cfg.Domain,
		"screen_width":      cfg.ScreenWidth,
		"screen_height":     cfg.ScreenHeight,
		"color_depth":       cfg.ColorDepth,
		"ignore_cert":       cfg.IgnoreCert,
		"file_ssh_asset_id": cfg.FileSSHAssetID,
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
	return validateRemoteServerArgs(args)
}

func (h *rdpHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.RDPConfig{
		Host:           ArgString(args, "host"),
		Port:           ArgInt(args, "port"),
		Username:       ArgString(args, "username"),
		Domain:         ArgString(args, "domain"),
		ScreenWidth:    ArgInt(args, "screen_width"),
		ScreenHeight:   ArgInt(args, "screen_height"),
		ColorDepth:     ArgInt(args, "color_depth"),
		IgnoreCert:     ArgBool(args, "ignore_cert"),
		FileSSHAssetID: ArgInt64(args, "file_ssh_asset_id"),
	}
	if cfg.Port == 0 {
		cfg.Port = h.DefaultPort()
	}
	if cfg.ScreenWidth == 0 {
		cfg.ScreenWidth = 1280
	}
	if cfg.ScreenHeight == 0 {
		cfg.ScreenHeight = 720
	}
	if cfg.ColorDepth == 0 {
		cfg.ColorDepth = 24
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
	if _, ok := args["domain"]; ok {
		cfg.Domain = ArgString(args, "domain")
	}
	if v := ArgInt(args, "screen_width"); v > 0 {
		cfg.ScreenWidth = v
	}
	if v := ArgInt(args, "screen_height"); v > 0 {
		cfg.ScreenHeight = v
	}
	if v := ArgInt(args, "color_depth"); v > 0 {
		cfg.ColorDepth = v
	}
	if _, ok := args["ignore_cert"]; ok {
		cfg.IgnoreCert = ArgBool(args, "ignore_cert")
	}
	if _, ok := args["file_ssh_asset_id"]; ok {
		cfg.FileSSHAssetID = ArgInt64(args, "file_ssh_asset_id")
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
