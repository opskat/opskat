package extension_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

// settingsView is extension "jev" whose manifest declares settingsEndpoint as
// given and whose describe() contributes settings (nil: none).
func settingsView(settingsEndpoint bool, settings any) extension.ExtensionView {
	contract := extmanifest.Manifest{Name: "jev", Version: "1.0.0"}
	contract.Permissions.Network.SettingsEndpoint = settingsEndpoint
	d := extensiontest.Describe{Contributes: map[string]any{}}
	if settings != nil {
		d.Contributes[extwire.KeySettings] = settings
	}
	m := extensiontest.Described(contract, d)
	return (&extension.ManifestInfo{Name: m.Name, Manifest: m}).View()
}

func apiSettings(auth *extwire.Auth) extwire.Settings {
	return extwire.Settings{
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"baseURL": map[string]any{"type": "string", "format": "endpoint"},
				"apiKey":  map[string]any{"type": "string", "format": "password"},
			},
			"required": []any{"baseURL"},
		},
		Auth: auth,
	}
}

var settingsBearerAuth = &extwire.Auth{Groups: []extwire.AuthGroup{{Bindings: []extwire.AuthBinding{
	{In: "header", Name: "Authorization", Value: "Bearer {{apiKey}}"},
}}}}

func TestSettingsPointCheck(t *testing.T) {
	point := SettingsPoint()
	assert.Equal(t, extwire.KeySettings, point.Key())

	plainSchema := extwire.Settings{ConfigSchema: map[string]any{
		"properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []any{"fast", "safe"}}},
	}}

	cases := []struct {
		name             string
		settingsEndpoint bool
		settings         any
		wantErr          string // "" = accepted
	}{
		{name: "no settings, no permission", settings: nil},
		{name: "settings without endpoint or auth need no permission", settings: plainSchema},
		{name: "endpoint and auth with the permission", settingsEndpoint: true, settings: apiSettings(settingsBearerAuth)},
		{name: "endpoint field without the permission", settings: apiSettings(nil),
			wantErr: "settings: declares a format:\"endpoint\" field or auth, which need permissions.network.settingsEndpoint, but the manifest does not declare it"},
		{name: "auth without the permission", settings: extwire.Settings{
			ConfigSchema: map[string]any{"properties": map[string]any{"apiKey": map[string]any{"type": "string", "format": "password"}}},
			Auth:         settingsBearerAuth,
		}, wantErr: "settings: declares a format:\"endpoint\" field or auth, which need permissions.network.settingsEndpoint, but the manifest does not declare it"},
		{name: "permission without settings", settingsEndpoint: true, settings: nil,
			wantErr: "settings: the manifest declares permissions.network.settingsEndpoint, but the settings mark no field format:\"endpoint\""},
		{name: "permission without an endpoint field", settingsEndpoint: true, settings: plainSchema,
			wantErr: "settings: the manifest declares permissions.network.settingsEndpoint, but the settings mark no field format:\"endpoint\""},
		{name: "schema without properties", settings: extwire.Settings{ConfigSchema: map[string]any{"type": "object"}},
			wantErr: "settings: configSchema must declare properties"},
		{name: "auth referencing an undeclared field", settingsEndpoint: true, settings: apiSettings(&extwire.Auth{Groups: []extwire.AuthGroup{{
			Bindings: []extwire.AuthBinding{{In: "header", Name: "X-Key", Value: "{{token}}"}},
		}}}), wantErr: `settings: auth.groups[0].bindings[0]: template references "token", which is not a configSchema property`},
		{name: "auth selected by a password field", settingsEndpoint: true, settings: apiSettings(&extwire.Auth{Selector: "apiKey", Groups: []extwire.AuthGroup{{
			When: "x", Bindings: []extwire.AuthBinding{{In: "header", Name: "X-Key", Value: "{{apiKey}}"}},
		}}}), wantErr: `settings: auth.selector "apiKey" is a password field; a secret cannot select an auth group`},
		{name: "password field that is not a string", settings: extwire.Settings{ConfigSchema: map[string]any{
			"properties": map[string]any{"pin": map[string]any{"type": "integer", "format": "password"}},
		}}, wantErr: `settings: configSchema.properties.pin: format "password" is only for a string field, not "integer"`},
		{name: "endpoint field that is not a string", settingsEndpoint: true, settings: extwire.Settings{ConfigSchema: map[string]any{
			"properties": map[string]any{"hosts": map[string]any{"type": "array", "format": "endpoint"}},
		}}, wantErr: `settings: configSchema.properties.hosts: format "endpoint" is only for a string field, not "array"`},
		{name: "settings in another shape", settings: []string{"x"}, wantErr: "settings: json: cannot unmarshal array"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := point.Check(settingsView(c.settingsEndpoint, c.settings))
			if c.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

// The registry names the extension on a refusal: the error the user sees on a
// failed load points at both the extension and the item.
func TestSettingsPointRefusalNamesTheExtension(t *testing.T) {
	reg := extension.NewContributionRegistry(SettingsPoint())
	err := reg.Check(settingsView(false, apiSettings(nil)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jev: settings: ")
}

func TestSettingsPointAttachAndRevoke(t *testing.T) {
	reg := extension.NewContributionRegistry(SettingsPoint())
	v := settingsView(true, apiSettings(settingsBearerAuth))
	require.NoError(t, reg.Attach(v))
	reg.Revoke(v.Name())
	// Revoked, the same extension attaches again: nothing of it was left behind.
	require.NoError(t, reg.Attach(v))
	reg.Revoke(v.Name())
}
