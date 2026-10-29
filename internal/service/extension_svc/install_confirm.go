package extension_svc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"
	"go.uber.org/zap"
)

// InstallSource says where a package comes from, for the confirm to show.
type InstallSource string

const (
	InstallSourceStore InstallSource = "store"
	InstallSourceFile  InstallSource = "file"
	InstallSourceDir   InstallSource = "dir"
)

// Grant kinds, one per independently granted thing in extmanifest.Permissions
// and extmanifest.Contributes. The kind strings are the confirm dialog's wire
// format and spell each permission the way manifest.json does.
const (
	CapFSRead        = "fs.read"
	CapFSWrite       = "fs.write"
	CapHTTP          = "network.http"
	CapCredentials   = "credentials"
	CapAllowPrivate  = "network.allowPrivate"
	CapAssetEndpoint = "network.assetEndpoint"
	// CapAssetRoute: a call scoped to an asset may dial any address along that
	// asset's connection path, not just the address the user typed.
	CapAssetRoute = "network.assetRoute" // #nosec G101 -- a permission name, not a credential
	// CapKerberos: a call whose scope carries a ticket source may ask the host for
	// Kerberos tickets fetched with that asset's Kerberos credentials.
	CapKerberos       = "kerberos"
	CapDatabaseDriver = "databaseDriver"
)

// CapabilityGrant is one grant an extension asks for. Value is the path / URL
// prefix / access level; it is empty for on/off grants. HighRisk marks the
// grants that hand over secrets or widen the network path past what the user
// typed into the asset; the confirm dialog and the opsctl approval both warn on it.
type CapabilityGrant struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	HighRisk bool   `json:"highRisk"`
}

// InstallConfirm is what the user is asked to approve before an install lands.
type InstallConfirm struct {
	Name         string            `json:"name"`
	DisplayName  string            `json:"displayName"`
	Icon         string            `json:"icon"`
	From         string            `json:"from"`
	To           string            `json:"to"`
	Downgrade    bool              `json:"downgrade"`
	Source       InstallSource     `json:"source"`
	Size         int64             `json:"size"`
	Capabilities []CapabilityGrant `json:"capabilities"`
	Added        []CapabilityGrant `json:"added"`
}

// ConfirmInstallFunc asks the user to approve c.
type ConfirmInstallFunc func(ctx context.Context, c InstallConfirm) error

// ErrInstallCanceled is returned when the user declines the install.
var ErrInstallCanceled = errors.New("extension install canceled")

// InstallCandidate is a package about to be installed.
type InstallCandidate struct {
	Name        string
	DisplayName string
	Icon        string
	Version     string
	Permissions extmanifest.Permissions
	Contributes extmanifest.Contributes
	Source      InstallSource
	Size        int64
}

// NewInstallConfirm describes installing c over whatever is installed under the
// same name now: the installed version becomes From, Downgrade is judged by
// version order, and Added lists the grants the installed version did not have.
// A caller without a local package — the store, from signed index data — builds
// its confirm here too, so every install is described by one rule.
func (s *Service) NewInstallConfirm(c InstallCandidate) InstallConfirm {
	grants := CapabilityGrants(c.Permissions, c.Contributes)
	out := InstallConfirm{
		Name:         c.Name,
		DisplayName:  c.DisplayName,
		Icon:         c.Icon,
		To:           c.Version,
		Source:       c.Source,
		Size:         c.Size,
		Capabilities: grants,
		Added:        []CapabilityGrant{},
	}
	installed := s.installedManifest(c.Name)
	if installed == nil {
		return out
	}
	out.From = installed.Version
	out.Downgrade = appversion.Compare(c.Version, installed.Version) < 0
	out.Added = AddedGrants(grants, CapabilityGrants(installed.Permissions, installed.Contributes))
	return out
}

// AddedGrants returns the grants in now that had does not have.
func AddedGrants(now, had []CapabilityGrant) []CapabilityGrant {
	hadSet := make(map[CapabilityGrant]struct{}, len(had))
	for _, g := range had {
		hadSet[g] = struct{}{}
	}
	added := []CapabilityGrant{}
	for _, g := range now {
		if _, ok := hadSet[g]; !ok {
			added = append(added, g)
		}
	}
	return added
}

// installedManifest returns the manifest of the installed extension named name —
// loaded, or disabled on disk — or nil when none is installed.
func (s *Service) installedManifest(name string) *extension.Manifest {
	if ext := s.manager.GetExtension(name); ext != nil {
		return ext.Manifest
	}
	mi, err := extension.LoadManifestInfo(s.manager.ExtDir(name), s.manager.Contributions())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// An unreadable installed copy is about to be replaced; the confirm
			// describes the install as a fresh one.
			s.logger.Warn("read installed extension manifest", zap.String("extension", name), zap.Error(err))
		}
		return nil
	}
	return mi.Manifest
}

// installedVersion is the installed version of name, "" when none is installed.
func (s *Service) installedVersion(name string) string {
	if m := s.installedManifest(name); m != nil {
		return m.Version
	}
	return ""
}

// CapabilityGrants flattens p and c into one entry per independently granted
// thing. credentials "read", assetRoute and kerberos are high risk.
func CapabilityGrants(p extmanifest.Permissions, c extmanifest.Contributes) []CapabilityGrant {
	out := []CapabilityGrant{}
	for _, path := range p.FS.Read {
		out = append(out, CapabilityGrant{Kind: CapFSRead, Value: path})
	}
	for _, path := range p.FS.Write {
		out = append(out, CapabilityGrant{Kind: CapFSWrite, Value: path})
	}
	for _, u := range p.Network.HTTP {
		out = append(out, CapabilityGrant{Kind: CapHTTP, Value: u})
	}
	if p.Credentials != "" {
		out = append(out, CapabilityGrant{Kind: CapCredentials, Value: p.Credentials, HighRisk: p.Credentials == extmanifest.CredentialAccessRead})
	}
	if p.Network.AllowPrivate {
		out = append(out, CapabilityGrant{Kind: CapAllowPrivate})
	}
	if p.Network.AssetEndpoint {
		out = append(out, CapabilityGrant{Kind: CapAssetEndpoint})
	}
	if p.Network.AssetRoute {
		out = append(out, CapabilityGrant{Kind: CapAssetRoute, HighRisk: true})
	}
	if p.Kerberos {
		out = append(out, CapabilityGrant{Kind: CapKerberos, HighRisk: true})
	}
	// Only the ids are known here (manifest / signed store index); describe() has
	// not run before an install is confirmed.
	for _, id := range c.DatabaseDrivers {
		out = append(out, CapabilityGrant{Kind: CapDatabaseDriver, Value: id})
	}
	return out
}

// localPackage reports how a local install source is shown: a directory by the
// size of its contents, a file (ZIP) by its own size.
func localPackage(sourcePath string) (InstallSource, int64, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", 0, err
	}
	if !info.IsDir() {
		return InstallSourceFile, info.Size(), nil
	}
	var total int64
	err = filepath.WalkDir(sourcePath, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return InstallSourceDir, total, nil
}
