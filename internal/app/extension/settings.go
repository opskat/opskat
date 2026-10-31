package extension

import (
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/service/extension_svc"
)

// GetExtensionSettings returns the settings form of extension name in lang —
// the declaration with its i18n keys resolved, the stored non-secret values and
// which secrets are set — or nil when the extension declares no settings.
func (e *Extension) GetExtensionSettings(name, lang string) (*extension_svc.SettingsForm, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	return e.service.SettingsForm(e.ctx, name, lang)
}

// SaveExtensionSettings saves the settings form of extension name. valuesJSON is
// the form's values as a JSON object, secrets in plaintext — a blank secret
// keeps the stored one. When the values do not validate, nothing is saved and
// the field errors are returned for the form to show.
func (e *Extension) SaveExtensionSettings(name, valuesJSON string) ([]extension_svc.SettingsFieldError, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(valuesJSON), &values); err != nil {
		return nil, fmt.Errorf("parse extension %q settings: %w", name, err)
	}
	return e.service.SaveSettings(e.ctx, name, values)
}
