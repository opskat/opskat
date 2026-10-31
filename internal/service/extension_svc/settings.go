package extension_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/extension"
)

// Extension settings: the one form an extension declares (SettingsPoint), filled
// in under Settings › Extensions and stored in their own table
// (extension_settings_repo) — not in the extension's KV store, which the guest
// reads and writes, so no KV key reaches them. format:"password" fields are
// stored encrypted (credential_svc) and never leave the host as plaintext except
// through DecryptedSettings, for host-side injection.

// SettingsFieldError is one field the settings form must correct before a save.
// Code is one of the SettingsErr* constants; the form words it.
type SettingsFieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// Codes of SettingsFieldError.
const (
	// SettingsErrRequired: a field configSchema.required lists has no value.
	SettingsErrRequired = "required"
	// SettingsErrType: the value is not of the field's declared type.
	SettingsErrType = "type"
	// SettingsErrEnum: the value is not one of the field's enum options.
	SettingsErrEnum = "enum"
	// SettingsErrUnknown: configSchema declares no such field.
	SettingsErrUnknown = "unknown"
)

// SettingsView is the stored settings as the form may see them: every
// non-secret value, and for each format:"password" field only whether it is set.
type SettingsView struct {
	Values  map[string]any  `json:"values"`
	Secrets map[string]bool `json:"secrets"`
}

// SettingsForm is what the settings form renders: the declaration, its i18n keys
// resolved, and the read view of the stored values.
type SettingsForm struct {
	ConfigSchema map[string]any  `json:"configSchema"`
	Auth         *extreg.AuthDef `json:"auth,omitempty"`
	Values       map[string]any  `json:"values"`
	Secrets      map[string]bool `json:"secrets"`
}

// SettingsDefinition returns the settings extName declares; nil when it declares
// none. A loaded extension answers from its running manifest, any other
// installed one from its cached describe().
func (s *Service) SettingsDefinition(extName string) (*SettingsDef, error) {
	def, _, err := s.settingsOf(extName)
	return def, err
}

// settingsOf returns the settings extName declares and its translator.
func (s *Service) settingsOf(extName string) (*SettingsDef, func(lang, key string) string, error) {
	var (
		manifest *extension.Manifest
		tr       func(lang, key string) string
	)
	if ext := s.manager.GetExtension(extName); ext != nil {
		manifest, tr = ext.Manifest, ext.Translate
	} else {
		mi, err := extension.LoadManifestInfo(s.manager.ExtDir(extName), s.manager.Contributions())
		if err != nil {
			return nil, nil, fmt.Errorf("extension %q: %w", extName, err)
		}
		manifest, tr = mi.Manifest, mi.Translate
	}
	def, err := settingsDefOf(manifest)
	if err != nil {
		return nil, nil, fmt.Errorf("extension %q: %w", extName, err)
	}
	return def, tr, nil
}

// declaredSettings is settingsOf for an operation that needs a declaration.
func (s *Service) declaredSettings(extName string) (*SettingsDef, error) {
	def, err := s.SettingsDefinition(extName)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, fmt.Errorf("extension %q declares no settings", extName)
	}
	return def, nil
}

// storedSettings returns extName's stored settings JSON; "{}" when nothing was saved.
func (s *Service) storedSettings(ctx context.Context, extName string) (string, error) {
	row, err := s.settingsRepo.Find(ctx, extName)
	if err != nil {
		return "", fmt.Errorf("read extension %q settings: %w", extName, err)
	}
	if row == nil {
		return "{}", nil
	}
	return row.Value, nil
}

func (s *Service) storedSettingsMap(ctx context.Context, extName string) (map[string]any, error) {
	raw, err := s.storedSettings(ctx, extName)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse extension %q settings: %w", extName, err)
	}
	return out, nil
}

// SettingsView returns the read view of extName's stored settings.
func (s *Service) SettingsView(ctx context.Context, extName string) (*SettingsView, error) {
	def, err := s.declaredSettings(extName)
	if err != nil {
		return nil, err
	}
	return s.settingsView(ctx, extName, def)
}

func (s *Service) settingsView(ctx context.Context, extName string, def *SettingsDef) (*SettingsView, error) {
	stored, err := s.storedSettingsMap(ctx, extName)
	if err != nil {
		return nil, err
	}
	secrets := def.SecretFields()
	view := &SettingsView{Values: map[string]any{}, Secrets: make(map[string]bool, len(secrets))}
	for _, f := range secrets {
		_, set := stored[f]
		view.Secrets[f] = set
	}
	for _, f := range extreg.ConfigSchemaProperties(def.ConfigSchema) {
		if v, ok := stored[f]; ok && !slices.Contains(secrets, f) {
			view.Values[f] = v
		}
	}
	return view, nil
}

// SettingsForm returns the settings form of extName in lang; nil when it
// declares no settings.
func (s *Service) SettingsForm(ctx context.Context, extName, lang string) (*SettingsForm, error) {
	def, tr, err := s.settingsOf(extName)
	if err != nil || def == nil {
		return nil, err
	}
	view, err := s.settingsView(ctx, extName, def)
	if err != nil {
		return nil, err
	}
	return &SettingsForm{
		ConfigSchema: extreg.LocalizeConfigSchema(def.ConfigSchema, func(key string) string { return tr(lang, key) }),
		Auth:         def.Auth,
		Values:       view.Values,
		Secrets:      view.Secrets,
	}, nil
}

// SaveSettings validates values — the whole form, in plaintext — against
// extName's configSchema and stores them in place of the stored settings,
// format:"password" fields encrypted. A value left blank (absent, null or "")
// is not set, except that a blank secret keeps the stored one. Values that do
// not validate are returned as field errors and nothing is saved.
func (s *Service) SaveSettings(ctx context.Context, extName string, values map[string]any) ([]SettingsFieldError, error) {
	def, err := s.declaredSettings(extName)
	if err != nil {
		return nil, err
	}
	stored, err := s.storedSettingsMap(ctx, extName)
	if err != nil {
		return nil, err
	}
	next := make(map[string]any, len(values))
	for k, v := range values {
		if v != nil && v != "" {
			next[k] = v
		}
	}
	var typed []string // secrets given a new value, encrypted once they validate
	for _, f := range def.SecretFields() {
		if _, given := next[f]; given {
			typed = append(typed, f)
		} else if old, ok := stored[f]; ok {
			next[f] = old // already ciphertext
		}
	}
	if errs := validateSettings(def.ConfigSchema, next); len(errs) > 0 {
		return errs, nil
	}
	for _, f := range typed {
		encrypted, err := credential_svc.Default().Encrypt(next[f].(string)) // validated as a string
		if err != nil {
			return nil, fmt.Errorf("encrypt extension %q setting %q: %w", extName, f, err)
		}
		next[f] = encrypted
	}
	data, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("encode extension %q settings: %w", extName, err)
	}
	if err := s.settingsRepo.Save(ctx, extName, string(data)); err != nil {
		return nil, fmt.Errorf("save extension %q settings: %w", extName, err)
	}
	return nil, nil
}

// validateSettings checks values against the subset of JSON Schema the settings
// form renders: declared fields only, required ones present, each of its type
// (string, boolean, integer, number) and, with an enum, one of its options.
// Errors are sorted by field.
func validateSettings(schema map[string]any, values map[string]any) []SettingsFieldError {
	props, _ := schema["properties"].(map[string]any)
	var errs []SettingsFieldError
	for _, f := range extreg.ConfigSchemaRequired(schema) {
		if _, ok := values[f]; !ok {
			errs = append(errs, SettingsFieldError{Field: f, Code: SettingsErrRequired})
		}
	}
	for f, v := range values {
		prop, declared := props[f].(map[string]any)
		if !declared {
			errs = append(errs, SettingsFieldError{Field: f, Code: SettingsErrUnknown})
			continue
		}
		if code := checkSettingValue(prop, v); code != "" {
			errs = append(errs, SettingsFieldError{Field: f, Code: code})
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}

// checkSettingValue returns the error code of v against one property; "" when it fits.
func checkSettingValue(prop map[string]any, v any) string {
	typ, _ := prop["type"].(string)
	switch typ {
	case "boolean":
		if _, ok := v.(bool); !ok {
			return SettingsErrType
		}
	case "integer", "number":
		n, ok := v.(float64)
		if !ok || (typ == "integer" && n != math.Trunc(n)) {
			return SettingsErrType
		}
	default: // "string", or no type: the form renders a text input
		if _, ok := v.(string); !ok {
			return SettingsErrType
		}
	}
	if enum, ok := prop["enum"].([]any); ok && !slices.Contains(enum, v) {
		return SettingsErrEnum
	}
	return ""
}

// DecryptedSettings returns extName's stored settings with format:"password"
// fields decrypted: host-side only, for injecting them into the extension's
// requests (auth, endpoint fields). "{}" when nothing was saved.
func (s *Service) DecryptedSettings(ctx context.Context, extName string) (json.RawMessage, error) {
	def, err := s.declaredSettings(extName)
	if err != nil {
		return nil, err
	}
	stored, err := s.storedSettingsMap(ctx, extName)
	if err != nil {
		return nil, err
	}
	for _, f := range def.SecretFields() {
		v, ok := stored[f]
		if !ok {
			continue
		}
		ciphertext, isString := v.(string)
		if !isString {
			return nil, fmt.Errorf("extension %q setting %q: stored secret is not a string", extName, f)
		}
		plain, err := credential_svc.Default().Decrypt(ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt extension %q setting %q: %w", extName, f, err)
		}
		stored[f] = plain
	}
	return json.Marshal(stored)
}

// SettingsHash is a digest of extName's stored settings, ciphertext included:
// it changes whenever a save changes any value, a secret too, and stays the
// same otherwise. Nothing saved hashes as "{}".
func (s *Service) SettingsHash(ctx context.Context, extName string) (string, error) {
	raw, err := s.storedSettings(ctx, extName)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:]), nil
}
