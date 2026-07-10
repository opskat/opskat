package rdp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

// testConnection tests an unsaved RDP config from the asset form. The common
// System.TestAssetConnection envelope supplies timeout/cancel/i18n context.
func (r *RDP) testConnection(ctx context.Context, configJSON string, plainPassword string) error {
	var cfg asset_entity.RDPConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolvePasswordGeneric(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	return r.service.TestConfig(ctx, &cfg, password)
}
