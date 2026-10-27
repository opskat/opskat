// Package extwire is the JSON an extension and its host exchange: the describe()
// answer (this file and contributes.go) and the database driver calls
// (driver.go). The guest SDK (pkg/extsdk) encodes it and the host
// (pkg/extension) decodes it with these same types, so there is no second copy
// to keep in step. It is pure data — it depends on nothing but pkg/dbdriver —
// so it compiles into the guest.
package extwire

import (
	"encoding/json"
	"fmt"
)

// Describe is the guest's answer to describe(): its presentation and, under
// Contributes, one entry per contribution point it contributes to. A key the
// extension contributes nothing to is absent.
//
// Contributes stays raw here because each contribution point decodes its own
// entry: the typed shapes are AssetType, Tool, Policy, Snippets, Pages and
// dbdriver.Descriptor, under the Key* constants.
type Describe struct {
	Icon        string                     `json:"icon,omitempty"`
	I18n        I18n                       `json:"i18n"`
	Contributes map[string]json.RawMessage `json:"contributes,omitempty"`
}

// Contribution point keys of Describe.Contributes.
const (
	// KeyAssetTypes holds []AssetType.
	KeyAssetTypes = "assetTypes"
	// KeyTools holds []Tool.
	KeyTools = "tools"
	// KeyPolicy holds Policy.
	KeyPolicy = "policy"
	// KeySnippets holds Snippets.
	KeySnippets = "snippets"
	// KeyPages holds Pages.
	KeyPages = "pages"
	// KeyDatabaseDrivers holds []dbdriver.Descriptor.
	KeyDatabaseDrivers = "databaseDrivers"
)

// I18n is the extension's display name and description, as i18n keys resolved
// against its locales/ directory.
type I18n struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// Parse decodes a describe() answer. A top-level field Describe does not define
// is refused: an answer in another shape must not read as an extension that
// contributes nothing.
func Parse(data []byte) (*Describe, error) {
	var d Describe
	if err := decodeStrict(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Put encodes v as the contribution under key.
func (d *Describe) Put(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("contributes.%s: %w", key, err)
	}
	if d.Contributes == nil {
		d.Contributes = make(map[string]json.RawMessage)
	}
	d.Contributes[key] = raw
	return nil
}

// Get decodes the contribution under key into v, leaving v untouched when the
// extension contributes nothing there. The error names the key.
func (d *Describe) Get(key string, v any) error {
	raw, ok := d.Contributes[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("contributes.%s: %w", key, err)
	}
	return nil
}
