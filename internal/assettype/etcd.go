package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type etcdHandler struct{}

func init() {
	Register(&etcdHandler{})
	policy.RegisterDefaultPolicy("etcd", func() any { return asset_entity.DefaultEtcdPolicy() })
}

func (h *etcdHandler) Type() string     { return asset_entity.AssetTypeEtcd }
func (h *etcdHandler) DefaultPort() int { return 2379 }

// SafeView 仅返回不敏感字段；TLS 证书/私钥/CA 路径都不暴露给前端 list 视图。
func (h *etcdHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetEtcdConfig()
	if err != nil || cfg == nil {
		return nil
	}
	view := map[string]any{
		"endpoints": cfg.Endpoints,
		"username":  cfg.Username,
		"tls":       cfg.TLS,
	}
	if cfg.TLSServerName != "" {
		view["tls_server_name"] = cfg.TLSServerName
	}
	if cfg.DialTimeoutSeconds > 0 {
		view["dial_timeout_seconds"] = cfg.DialTimeoutSeconds
	}
	if cfg.CommandTimeoutSeconds > 0 {
		view["command_timeout_seconds"] = cfg.CommandTimeoutSeconds
	}
	return view
}

func (h *etcdHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetEtcdConfig()
	if err != nil {
		return "", fmt.Errorf("get etcd config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

func (h *etcdHandler) ValidateCreateArgs(args map[string]any) error {
	eps := ArgStringSlice(args, "endpoints")
	if len(eps) == 0 {
		return fmt.Errorf("missing required parameter: endpoints")
	}
	return nil
}

func (h *etcdHandler) DefaultPolicy() any { return asset_entity.DefaultEtcdPolicy() }

func (h *etcdHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:             ArgStringSlice(args, "endpoints"),
		Username:              ArgString(args, "username"),
		TLS:                   ArgBool(args, "tls"),
		TLSInsecure:           ArgBool(args, "tls_insecure"),
		TLSServerName:         ArgString(args, "tls_server_name"),
		TLSCAFile:             ArgString(args, "tls_ca_file"),
		TLSCertFile:           ArgString(args, "tls_cert_file"),
		TLSKeyFile:            ArgString(args, "tls_key_file"),
		DialTimeoutSeconds:    ArgInt(args, "dial_timeout_seconds"),
		CommandTimeoutSeconds: ArgInt(args, "command_timeout_seconds"),
	}
	a.SSHTunnelID = ArgInt64(args, "ssh_asset_id")
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt etcd password: %w", err)
		}
		cfg.Password = encrypted
	}
	return a.SetEtcdConfig(cfg)
}

func (h *etcdHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetEtcdConfig()
	if err != nil || cfg == nil {
		return err
	}
	if v := ArgStringSlice(args, "endpoints"); len(v) > 0 {
		cfg.Endpoints = v
	}
	if v := ArgString(args, "username"); v != "" {
		cfg.Username = v
	}
	if _, ok := args["tls"]; ok {
		cfg.TLS = ArgBool(args, "tls")
	}
	if _, ok := args["tls_insecure"]; ok {
		cfg.TLSInsecure = ArgBool(args, "tls_insecure")
	}
	if v := ArgString(args, "tls_server_name"); v != "" {
		cfg.TLSServerName = v
	}
	if v := ArgString(args, "tls_ca_file"); v != "" {
		cfg.TLSCAFile = v
	}
	if v := ArgString(args, "tls_cert_file"); v != "" {
		cfg.TLSCertFile = v
	}
	if v := ArgString(args, "tls_key_file"); v != "" {
		cfg.TLSKeyFile = v
	}
	if v := ArgInt(args, "dial_timeout_seconds"); v > 0 {
		cfg.DialTimeoutSeconds = v
	}
	if v := ArgInt(args, "command_timeout_seconds"); v > 0 {
		cfg.CommandTimeoutSeconds = v
	}
	if _, ok := args["ssh_asset_id"]; ok {
		a.SSHTunnelID = ArgInt64(args, "ssh_asset_id")
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt etcd password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0
	}
	if err := a.SetEtcdConfig(cfg); err != nil {
		return err
	}
	connpool.InvalidateEtcd(a.ID)
	return nil
}
