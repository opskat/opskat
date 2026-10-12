package extension

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/pkg/appversion"
)

// IncompatibleReason names which half of the compatibility rule an extension broke.
type IncompatibleReason string

const (
	// ReasonHostABI: the extension needs a host ABI this app does not support.
	ReasonHostABI IncompatibleReason = "hostABI"
	// ReasonMinAppVersion: the extension needs a newer OpsKat release.
	ReasonMinAppVersion IncompatibleReason = "minAppVersion"
)

// IncompatibleError is returned when an extension cannot run on this app. It
// carries the facts a caller needs to say so in the user's language: the user's
// remedy is always "update OpsKat", plus what to update to.
type IncompatibleError struct {
	Reason            IncompatibleReason
	HostABI           string
	MinAppVersion     string
	AppVersion        string
	SupportedHostABIs []string
}

func (e *IncompatibleError) Error() string {
	switch e.Reason {
	case ReasonHostABI:
		return fmt.Sprintf("extension needs hostABI %s, this OpsKat supports %s: update OpsKat",
			e.HostABI, strings.Join(e.SupportedHostABIs, ", "))
	default:
		return fmt.Sprintf("extension needs OpsKat %s or newer, this is %s: update OpsKat",
			e.MinAppVersion, e.AppVersion)
	}
}

// CheckCompatible applies the compatibility rule: the hostABI must be one this app
// supports; and on an official release build the minAppVersion (when set) must not
// exceed the app version. Nightly and dev builds have no trustworthy version
// number, so they are judged by hostABI alone.
func CheckCompatible(hostABI, minAppVersion string, app appversion.Info) error {
	incompatible := func(reason IncompatibleReason) error {
		return &IncompatibleError{
			Reason:            reason,
			HostABI:           hostABI,
			MinAppVersion:     minAppVersion,
			AppVersion:        app.Version,
			SupportedHostABIs: SupportedHostABIs,
		}
	}
	if !slices.Contains(SupportedHostABIs, hostABI) {
		return incompatible(ReasonHostABI)
	}
	if app.Kind == appversion.KindRelease && minAppVersion != "" && appversion.Compare(minAppVersion, app.Version) > 0 {
		return incompatible(ReasonMinAppVersion)
	}
	return nil
}

// CheckManifestJSON runs CheckCompatible on raw manifest.json bytes. It reads only
// the two compatibility fields, so a manifest written for a newer host is judged
// by the rule instead of tripping over a field this app's validation rejects.
func CheckManifestJSON(data []byte, app appversion.Info) error {
	var f struct {
		HostABI       string `json:"hostABI"`
		MinAppVersion string `json:"minAppVersion"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	return CheckCompatible(f.HostABI, f.MinAppVersion, app)
}

// CheckSourceCompatible runs CheckManifestJSON on the manifest.json of an install
// source, a directory or a .zip, without unpacking it.
func CheckSourceCompatible(sourcePath string, app appversion.Info) error {
	data, err := readSourceManifest(sourcePath)
	if err != nil {
		return err
	}
	return CheckManifestJSON(data, app)
}

func readSourceManifest(sourcePath string) ([]byte, error) {
	if !strings.HasSuffix(strings.ToLower(sourcePath), ".zip") {
		data, err := os.ReadFile(filepath.Join(sourcePath, "manifest.json")) //nolint:gosec // user-chosen install source
		if err != nil {
			return nil, fmt.Errorf("read manifest: %w", err)
		}
		return data, nil
	}
	r, err := zip.OpenReader(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("read manifest: %w", err)
		}
		defer func() { _ = rc.Close() }()
		data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("read manifest: %w", err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("read manifest: manifest.json not found in %s", filepath.Base(sourcePath))
}
