// Package extstore is the official extension store index: the wire model shared by
// the app (which reads it) and the publisher in the extensions repo (which writes
// and signs it), plus the rule that picks which version of an extension the store
// offers. It does no I/O: callers fetch the bytes and hand them in.
//
// Wire format. index.json is a JSON object {"format": 1, "extensions": [...]}; see
// Index for the field names. index.json.sig is the ed25519 signature over the raw
// bytes of index.json, exactly as served, encoded as standard base64 text (padded,
// RFC 4648 §4). Surrounding whitespace in the .sig file — a trailing newline from an
// editor or git — is ignored; Sign emits the bare base64 text. The app trusts a set
// of public keys so the signing key can be rotated: a signature from any one of them
// is accepted.
package extstore

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/opskat/opskat/pkg/extension"
)

// FormatVersion is the only index format this build understands. A publisher
// bumps it only for a change older apps cannot read; adding fields does not.
const FormatVersion = 1

// SourceOCI is a package stored as an OCI artifact. It is the default when an
// index entry names no source type.
const SourceOCI = "oci"

// Index is the whole index.json.
type Index struct {
	Format     int         `json:"format"`
	Extensions []Extension `json:"extensions"`
}

// Extension is one extension with every version ever published.
type Extension struct {
	Name string `json:"name"`
	// Display is keyed by language tag ("en", "zh-CN").
	Display  map[string]Display `json:"display"`
	Icon     string             `json:"icon"`
	Versions []Version          `json:"versions"`
}

// Display is the localized name and description of an extension.
type Display struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Version is one published version of an extension.
type Version struct {
	Version       string `json:"version"`
	HostABI       string `json:"hostABI"`
	MinAppVersion string `json:"minAppVersion,omitempty"`
	// Capabilities are the grants the version's manifest.json asks for, in the
	// host's own shape, so the store shows exactly what an install will ask.
	Capabilities extension.Capabilities `json:"capabilities"`
	// Size is the package size in bytes.
	Size        int64     `json:"size"`
	PublishedAt time.Time `json:"publishedAt"`
	Source      Source    `json:"source"`
}

// Source says where a version's package lives. Type selects how Ref is read; for
// SourceOCI, Ref is the image reference and SHA256 the lowercase hex digest of the
// package bytes.
type Source struct {
	Type   string `json:"type"`
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

// UnsupportedFormatError means the index was written in a format this build cannot
// read: the user's remedy is to update OpsKat. The store is not shown.
type UnsupportedFormatError struct {
	Format int
}

func (e *UnsupportedFormatError) Error() string {
	return fmt.Sprintf("extension index format %d is not supported (this OpsKat reads format %d): update OpsKat",
		e.Format, FormatVersion)
}

// ParseIndex decodes index.json. It checks the format version before anything
// else, so an index from a newer publisher reports UnsupportedFormatError rather
// than whatever decode error its new shape would trip. A version naming no source
// type gets SourceOCI here, once, so no consumer has to default it again.
//
// ParseIndex does not check the signature; call Verify on the same bytes first.
func ParseIndex(raw []byte) (Index, error) {
	var head struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Index{}, fmt.Errorf("parse extension index: %w", err)
	}
	if head.Format != FormatVersion {
		return Index{}, &UnsupportedFormatError{Format: head.Format}
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return Index{}, fmt.Errorf("parse extension index: %w", err)
	}
	for i := range idx.Extensions {
		ext := &idx.Extensions[i]
		if ext.Name == "" {
			return Index{}, fmt.Errorf("parse extension index: extension %d has no name", i)
		}
		if len(ext.Versions) == 0 {
			return Index{}, fmt.Errorf("parse extension index: extension %s has no versions", ext.Name)
		}
		for j := range ext.Versions {
			v := &ext.Versions[j]
			if v.Version == "" {
				return Index{}, fmt.Errorf("parse extension index: extension %s version %d has no version number", ext.Name, j)
			}
			if v.Source.Type == "" {
				v.Source.Type = SourceOCI
			}
		}
	}
	return idx, nil
}
