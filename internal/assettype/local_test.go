package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalHandler_Registered(t *testing.T) {
	h, ok := Get("local")
	require.True(t, ok, "local handler should be registered in init()")
	assert.Equal(t, "local", h.Type())
	assert.Equal(t, 0, h.DefaultPort())
}

func TestLocalHandler_ValidateCreateArgsAllowsEmpty(t *testing.T) {
	h := &localHandler{}
	assert.NoError(t, h.ValidateCreateArgs(map[string]any{}), "shell 可空")
}

func TestLocalHandler_ApplyCreateArgs(t *testing.T) {
	h := &localHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeLocal}
	err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"shell": "/bin/zsh",
		"args":  []any{"-l"},
		"cwd":   "/tmp",
	})
	require.NoError(t, err)
	cfg, err := a.GetLocalConfig()
	require.NoError(t, err)
	assert.Equal(t, "/bin/zsh", cfg.Shell)
	assert.Equal(t, []string{"-l"}, cfg.Args)
	assert.Equal(t, "/tmp", cfg.Cwd)
}

func TestLocalHandler_ApplyUpdateArgs_PartialFields(t *testing.T) {
	h := &localHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&asset_entity.LocalConfig{Shell: "/bin/bash", Cwd: "/old"}))
	err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{"cwd": "/new"})
	require.NoError(t, err)
	cfg, _ := a.GetLocalConfig()
	assert.Equal(t, "/bin/bash", cfg.Shell, "未传字段应保留")
	assert.Equal(t, "/new", cfg.Cwd)
}

func TestLocalHandler_SafeViewNoSecrets(t *testing.T) {
	h := &localHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&asset_entity.LocalConfig{Shell: "/bin/zsh", Cwd: "/tmp"}))
	view := h.SafeView(a)
	require.NotNil(t, view)
	assert.Contains(t, view, "shell")
	assert.Contains(t, view, "cwd")
}
