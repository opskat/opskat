package extension_svc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
	"go.uber.org/zap"
)

// InstallSource says where a package comes from, for the confirm to show.
type InstallSource string

const (
	InstallSourceStore InstallSource = "store"
	InstallSourceFile  InstallSource = "file"
	InstallSourceDir   InstallSource = "dir"
)

// Capability grant kinds, one per independently granted thing in
// extension.Capabilities.
const (
	CapFSRead        = "fs.read"
	CapFSWrite       = "fs.write"
	CapHTTP          = "http"
	CapCredentials   = "credentials"
	CapTunnel        = "tunnel"
	CapAssetEndpoint = "network.assetEndpoint"
)

// CapabilityGrant is one grant an extension asks for. Value is the path / URL
// prefix / access level; it is empty for on/off grants.
type CapabilityGrant struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
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
	Name         string
	DisplayName  string
	Icon         string
	Version      string
	Capabilities extension.Capabilities
	Source       InstallSource
	Size         int64
}

// NewInstallConfirm describes installing c over whatever is installed under the
// same name now: the installed version becomes From, Downgrade is judged by
// version order, and Added lists the grants the installed version did not have.
// A caller without a local package — the store, from signed index data — builds
// its confirm here too, so every install is described by one rule.
func (s *Service) NewInstallConfirm(c InstallCandidate) InstallConfirm {
	grants := capabilityGrants(c.Capabilities)
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
	had := make(map[CapabilityGrant]struct{})
	for _, g := range capabilityGrants(installed.Capabilities) {
		had[g] = struct{}{}
	}
	for _, g := range grants {
		if _, ok := had[g]; !ok {
			out.Added = append(out.Added, g)
		}
	}
	return out
}

// installedManifest returns the manifest of the installed extension named name —
// loaded, or disabled on disk — or nil when none is installed.
func (s *Service) installedManifest(name string) *extension.Manifest {
	if ext := s.manager.GetExtension(name); ext != nil {
		return ext.Manifest
	}
	mi, err := extension.LoadManifestInfo(s.manager.ExtDir(name))
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

// capabilityGrants flattens c into one entry per independently granted thing.
func capabilityGrants(c extension.Capabilities) []CapabilityGrant {
	out := []CapabilityGrant{}
	for _, p := range c.FS.Read {
		out = append(out, CapabilityGrant{Kind: CapFSRead, Value: p})
	}
	for _, p := range c.FS.Write {
		out = append(out, CapabilityGrant{Kind: CapFSWrite, Value: p})
	}
	for _, u := range c.HTTP.Allowlist {
		out = append(out, CapabilityGrant{Kind: CapHTTP, Value: u})
	}
	if c.Credentials != "" {
		out = append(out, CapabilityGrant{Kind: CapCredentials, Value: c.Credentials})
	}
	if c.Tunnel {
		out = append(out, CapabilityGrant{Kind: CapTunnel})
	}
	if c.Network.AssetEndpoint {
		out = append(out, CapabilityGrant{Kind: CapAssetEndpoint})
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
