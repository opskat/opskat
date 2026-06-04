package assettype

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
)

type localHandler struct{}

func init() {
	Register(&localHandler{})
}

func (h *localHandler) Type() string     { return asset_entity.AssetTypeLocal }
func (h *localHandler) DefaultPort() int { return 0 }

func (h *localHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetLocalConfig()
	if err != nil || cfg == nil {
		return nil
	}
	return map[string]any{
		"shell": cfg.Shell,
		"args":  cfg.Args,
		"cwd":   cfg.Cwd,
	}
}

// ResolvePassword 本地终端无密码，返回空。
func (h *localHandler) ResolvePassword(_ context.Context, _ *asset_entity.Asset) (string, error) {
	return "", nil
}

// DefaultPolicy 仅为满足接口；本次不接 AI，策略不参与拦截。
func (h *localHandler) DefaultPolicy() any { return asset_entity.DefaultCommandPolicy() }
func (h *localHandler) PolicyKind() string { return policy.PolicyKindCommand }

// ValidateCreateArgs 本地终端无必填字段（shell 可空，运行时按 OS 兜底）。
func (h *localHandler) ValidateCreateArgs(_ map[string]any) error { return nil }

func (h *localHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	return a.SetLocalConfig(&asset_entity.LocalConfig{
		Shell: ArgString(args, "shell"),
		Args:  ArgStringSlice(args, "args"),
		Cwd:   ArgString(args, "cwd"),
	})
}

func (h *localHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetLocalConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &asset_entity.LocalConfig{}
	}
	if v := ArgString(args, "shell"); v != "" {
		cfg.Shell = v
	}
	if v := ArgStringSlice(args, "args"); v != nil {
		cfg.Args = v
	}
	if v := ArgString(args, "cwd"); v != "" {
		cfg.Cwd = v
	}
	return a.SetLocalConfig(cfg)
}
