package extension_svc

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// SettingsPoint is the settings contribution point (describe()'s settings): the
// rules for the one settings form an extension may declare. Extension management
// owns it — settings are stored, edited and read back here — so it is not among
// internal/extreg's Points; every process that runs extensions appends it where it
// builds its contribution registry (main.go, cmd/opsctl/command/ext.go).
//
// Attaching is a no-op: the declaration is read back from the extension's
// manifest whenever it is needed (SettingsDefinition), so nothing outlives the
// extension's load.
func SettingsPoint() extension.ContributionPoint { return settingsPoint{} }

type settingsPoint struct{}

func (settingsPoint) Key() string { return extwire.KeySettings }

// Check judges the declaration by the asset-type rules for a configSchema and its
// auth (extreg.CheckConfigSchema), plus the permission that makes it reachable:
// a format:"endpoint" field or an auth — which is injected only into requests to
// those fields' addresses — needs permissions.network.settingsEndpoint, and that
// permission needs a format:"endpoint" field to grant anything. Check runs for
// every extension, so the permission is refused without settings too.
func (settingsPoint) Check(v extension.ExtensionView) error {
	permitted := v.Manifest().Permissions.Network.SettingsEndpoint
	raw := v.Contribution(extwire.KeySettings)
	if raw == nil {
		if permitted {
			return errSettingsEndpointUnused
		}
		return nil
	}
	var w extwire.Settings
	if err := json.Unmarshal(raw, &w); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if err := extreg.CheckConfigSchema(w.ConfigSchema, w.Auth); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	hasEndpoint := len(extreg.EndpointFieldsFromSchema(w.ConfigSchema)) > 0
	if (hasEndpoint || w.Auth != nil) && !permitted {
		return fmt.Errorf(`settings: declares a format:"endpoint" field or auth, which need permissions.network.settingsEndpoint, but the manifest does not declare it`)
	}
	if permitted && !hasEndpoint {
		return errSettingsEndpointUnused
	}
	return nil
}

var errSettingsEndpointUnused = errors.New(`settings: the manifest declares permissions.network.settingsEndpoint, but the settings mark no field format:"endpoint"`)

func (settingsPoint) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }

// SettingsDef is the settings an extension declares (describe()'s settings): the
// form's configSchema and the credentials the host injects from it.
type SettingsDef struct {
	ConfigSchema map[string]any  `json:"configSchema"`
	Auth         *extreg.AuthDef `json:"auth,omitempty"`
}

// SecretFields are the format:"password" fields: stored encrypted, read back
// only as set or unset.
func (d *SettingsDef) SecretFields() []string { return extreg.PasswordFieldsFromSchema(d.ConfigSchema) }

// EndpointFields are the format:"endpoint" fields: the addresses the extension
// may reach under permissions.network.settingsEndpoint, and the only ones Auth
// is injected into.
func (d *SettingsDef) EndpointFields() []string {
	return extreg.EndpointFieldsFromSchema(d.ConfigSchema)
}

// contributionSource is what a settings declaration is read from: a point's
// ExtensionView, or a loaded or cached extension's manifest.
type contributionSource interface {
	Contribution(key string) json.RawMessage
}

// settingsDefOf decodes the settings c contributes; nil when it declares none.
func settingsDefOf(c contributionSource) (*SettingsDef, error) {
	raw := c.Contribution(extwire.KeySettings)
	if raw == nil {
		return nil, nil
	}
	var w extwire.Settings
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	return &SettingsDef{ConfigSchema: w.ConfigSchema, Auth: extreg.AuthDefOf(w.Auth)}, nil
}
