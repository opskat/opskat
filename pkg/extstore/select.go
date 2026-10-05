package extstore

import (
	"fmt"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
)

// Action is what the store offers for one extension.
type Action string

const (
	// ActionInstall: not installed; Choice.Version is the version to install.
	ActionInstall Action = "install"
	// ActionUpdate: an older version is installed; Choice.Version is the update.
	ActionUpdate Action = "update"
	// ActionInstalled: installed, and nothing newer that this app can run is
	// published. The store never offers a version below the installed one.
	ActionInstalled Action = "installed"
	// ActionUnavailable: not installed and no published version runs on this
	// app; the reason returned alongside says what OpsKat update is needed.
	ActionUnavailable Action = "unavailable"
)

// Choice is the store's offer for one extension. Version is set for
// ActionInstall and ActionUpdate only.
type Choice struct {
	Action  Action
	Version *Version
}

// UnsupportedSourceError means a version's package lives in a kind of source this
// build cannot fetch: the user's remedy is to update OpsKat.
type UnsupportedSourceError struct {
	Type string
}

func (e *UnsupportedSourceError) Error() string {
	return fmt.Sprintf("extension package source type %q is not supported: update OpsKat", e.Type)
}

// Installable reports whether this app can install the version: its source type
// must be one this build fetches, and it must pass extension.CheckCompatible. The
// error is an *UnsupportedSourceError or an *extension.IncompatibleError.
func (v Version) Installable(app appversion.Info) error {
	if v.Source.Type != SourceOCI {
		return &UnsupportedSourceError{Type: v.Source.Type}
	}
	return extension.CheckCompatible(v.HostABI, v.MinAppVersion, app)
}

// SelectInstallable picks what the store offers for ext: its newest installable
// version, compared against installedVersion ("" when not installed).
//
// The reason is non-nil only for ActionUnavailable, and is the newest version's
// Installable error — the OpsKat update that would make the latest release
// installable.
func SelectInstallable(ext Extension, app appversion.Info, installedVersion string) (Choice, error) {
	var best, newest *Version
	for i := range ext.Versions {
		v := &ext.Versions[i]
		if newest == nil || appversion.Compare(v.Version, newest.Version) > 0 {
			newest = v
		}
		if v.Installable(app) != nil {
			continue
		}
		if best == nil || appversion.Compare(v.Version, best.Version) > 0 {
			best = v
		}
	}

	if installedVersion != "" {
		if best == nil || appversion.Compare(installedVersion, best.Version) >= 0 {
			return Choice{Action: ActionInstalled}, nil
		}
		offer := *best
		return Choice{Action: ActionUpdate, Version: &offer}, nil
	}
	if best == nil {
		return Choice{Action: ActionUnavailable}, newest.Installable(app)
	}
	offer := *best
	return Choice{Action: ActionInstall, Version: &offer}, nil
}
