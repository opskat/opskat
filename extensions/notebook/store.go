package main

import (
	"encoding/json"
	"fmt"
	"regexp"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// The whole notebook lives under one KV key. The host KV is a get/set store with
// no prefix scan, so an extension that wants to enumerate anything keeps its own
// index — here the index and the data are the same document, which is what makes
// list, get, put and delete a single read-modify-write each.
const kvPrefix = "notebook/"

type note struct {
	Key       string   `json:"key"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	UpdatedAt string   `json:"updatedAt"`
}

type notebookDoc struct {
	Notes map[string]note `json:"notes"`
}

// open resolves the asset's configuration and loads its notebook. Every tool
// starts here, so the asset config is read at exactly one place.
func open(assetID int64) (notebookConfig, *notebookDoc, error) {
	cfg, err := loadConfig(assetID)
	if err != nil {
		return cfg, nil, err
	}
	doc, err := load(cfg)
	if err != nil {
		return cfg, nil, err
	}
	return cfg, doc, nil
}

// loadConfig reads the asset's configuration from the host and holds it to the
// same rules the configuration form is checked against. The config arrives across
// the WASM boundary, which is where a guest checks its inputs.
func loadConfig(assetID int64) (notebookConfig, error) {
	raw, err := opskat.GetAssetConfig(assetID)
	if err != nil {
		return notebookConfig{}, fmt.Errorf("read config of asset %d: %w", assetID, err)
	}
	var cfg notebookConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return notebookConfig{}, fmt.Errorf("parse config of asset %d: %w", assetID, err)
	}
	if errs := validateConfig(cfg); len(errs) > 0 {
		return notebookConfig{}, fmt.Errorf("asset %d has an invalid notebook config: %s %s",
			assetID, errs[0].Field, errs[0].Message)
	}
	return cfg, nil
}

func load(cfg notebookConfig) (*notebookDoc, error) {
	raw, err := opskat.KVGet(kvPrefix + cfg.Notebook)
	if err != nil {
		return nil, fmt.Errorf("read notebook %q: %w", cfg.Notebook, err)
	}
	// Unmarshalling into an initialized map leaves it in place when the key holds
	// nothing yet, so "first use" and "loaded" are the same value shape.
	doc := &notebookDoc{Notes: map[string]note{}}
	if len(raw) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(raw, doc); err != nil {
		return nil, fmt.Errorf("parse notebook %q: %w", cfg.Notebook, err)
	}
	return doc, nil
}

func save(cfg notebookConfig, doc *notebookDoc) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode notebook %q: %w", cfg.Notebook, err)
	}
	if err := opskat.KVSet(kvPrefix+cfg.Notebook, data); err != nil {
		return fmt.Errorf("write notebook %q: %w", cfg.Notebook, err)
	}
	return nil
}

// notebookNameRe keeps the configured name usable as a KV key segment.
var notebookNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// validateConfig is registered as the config validator and is also what the tools
// hold a loaded config to: one set of rules, applied at both ends.
func validateConfig(cfg notebookConfig) []opskat.ValidationError {
	var errs []opskat.ValidationError
	if !notebookNameRe.MatchString(cfg.Notebook) {
		errs = append(errs, opskat.ValidationError{
			Field:   "notebook",
			Message: "must match " + notebookNameRe.String(),
		})
	}
	if cfg.MaxNotes < 0 {
		errs = append(errs, opskat.ValidationError{
			Field:   "maxNotes",
			Message: "must not be negative (0 means unlimited)",
		})
	}
	return errs
}
