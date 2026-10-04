package extreg

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/extension"
)

// 扩展的配置校验器（guest 的 RegisterConfigValidator）随扩展注册进资产写入口：
// 一份 guest 判为无效的配置在保存时就被拒，而不是存下来、到第一次调工具才炸。

func TestRegisterWiresTheGuestConfigValidatorIntoAssetSave(t *testing.T) {
	plugin := &fakePlugin{validationErrors: []extension.ValidationError{{Field: "endpoint", Message: "must be a URL"}}}
	registerFake(t, plugin)

	asset := &asset_entity.Asset{Name: "a", Type: "acme-store", Config: `{"endpoint":"nope"}`}
	err := asset_entity.ValidateRegisteredConfig(context.Background(), asset)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
	assert.Contains(t, err.Error(), "must be a URL")
	assert.JSONEq(t, `{"endpoint":"nope"}`, string(plugin.lastValidated),
		"the guest validates the config exactly as it will be stored and later read back by its tools")

	plugin.validationErrors = nil
	assert.NoError(t, asset_entity.ValidateRegisteredConfig(context.Background(), asset))
}

func TestGuestValidatorFailureRefusesTheSave(t *testing.T) {
	registerFake(t, &fakePlugin{validateErr: errors.New("guest trapped")})
	err := asset_entity.ValidateRegisteredConfig(context.Background(),
		&asset_entity.Asset{Name: "a", Type: "acme-store", Config: `{}`})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "guest trapped")
}

func TestUnregisterRemovesTheConfigValidator(t *testing.T) {
	registerFake(t, &fakePlugin{validationErrors: []extension.ValidationError{{Message: "always"}}})
	Unregister("acme")
	assert.NoError(t, asset_entity.ValidateRegisteredConfig(context.Background(),
		&asset_entity.Asset{Name: "a", Type: "acme-store", Config: `{}`}))
}

// opsctl 的仅描述注册没有 WASM 运行时，guest 校验器不可用；schema 层的必填/未知字段
// 校验（assettype.PrepareCreate）照常生效，guest 规则要到桌面端保存时才跑。
func TestDescribeOnlyRegistersNoGuestValidator(t *testing.T) {
	m := testManifest()
	require.NoError(t, RegisterDescribeOnly(&extension.ManifestInfo{Name: m.Name, Manifest: m}))
	t.Cleanup(func() { Unregister(m.Name) })
	assert.NoError(t, asset_entity.ValidateRegisteredConfig(context.Background(),
		&asset_entity.Asset{Name: "a", Type: "acme-store", Config: `{}`}))
}
