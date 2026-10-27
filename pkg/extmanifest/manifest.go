// Package extmanifest is an extension's security contract: manifest.json, the
// permissions it grants and the rule that decides whether this app can run it.
// It is pure data — no WASM runtime, no database driver contract — so the store
// index and the publisher in the extensions repo can read a manifest without
// pulling in the extension runtime (pkg/extension).
package extmanifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

// HostABIVersion is the host ABI contract this app speaks. An extension declares
// the hostABI it was built for in manifest.json; this is the version the history
// restarts from.
const HostABIVersion = "1.0"

// SupportedHostABIs lists every hostABI this app loads. The nightly-era 2.x
// contracts are not among them: a 2.x extension is incompatible, not upgraded.
var SupportedHostABIs = []string{HostABIVersion}

// CredentialAccessNone means the extension receives only opaque credential handles (default).
// CredentialAccessRead means the extension may request plaintext credentials (discouraged).
const (
	CredentialAccessNone = ""
	CredentialAccessRead = "read"
)

var (
	semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	// nameRe is what an extension name and a database driver id must match.
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// Manifest is manifest.json: exactly what a user audits before the extension's
// code runs. Everything else an extension says about itself comes from its code
// (describe()), never from here.
type Manifest struct {
	Name          string      `json:"name"`
	Version       string      `json:"version"`
	MinAppVersion string      `json:"minAppVersion"`
	HostABI       string      `json:"hostABI"`
	Backend       Backend     `json:"backend"`
	Permissions   Permissions `json:"permissions"`
	Contributes   Contributes `json:"contributes"`
}

// Backend names the WASM module the host loads.
type Backend struct {
	Runtime string `json:"runtime"`
	Binary  string `json:"binary"`
}

// Contributes is the one contribution that must be audited before the code runs:
// the database drivers, whose ids the user approves at install.
type Contributes struct {
	// DatabaseDrivers lists the SQL database driver ids the extension provides;
	// describe() must answer with exactly this set of drivers.
	DatabaseDrivers []string `json:"databaseDrivers"`
}

// Permissions declares which host resources an extension may use. The zero value
// is deny-all except an implicit read-only ${EXT_DIR}/** for the extension's own
// directory; the host enforces each grant at its host call.
type Permissions struct {
	FS FSPermission `json:"fs"`
	// Credentials is "" (opaque handles only) or "read" (plaintext credentials).
	Credentials string `json:"credentials"`
	// Kerberos lets a database driver ask the host for Kerberos tickets fetched
	// with the asset's Kerberos credentials. Only meaningful with
	// contributes.databaseDrivers.
	Kerberos bool              `json:"kerberos"`
	Network  NetworkPermission `json:"network"`
}

// FSPermission lists filesystem path patterns an extension may access. Patterns
// support one ${EXT_DIR} placeholder (resolved at load time to the extension's
// directory) and a trailing /** for recursive match; all other entries are
// absolute path prefixes.
type FSPermission struct {
	Read  []string `json:"read"`
	Write []string `json:"write"`
}

// NetworkPermission grants network reach.
type NetworkPermission struct {
	// HTTP lists the URL prefixes an extension may open over HTTP; the request URL
	// must start with one of them.
	HTTP []string `json:"http"`
	// AllowPrivate lets an HTTP target admitted by HTTP resolve to a private
	// address (RFC1918, loopback, link-local). Routing through an asset's SSH
	// tunnel is an asset type's connection.sshTunnel declaration, not this.
	AllowPrivate bool `json:"allowPrivate"`
	// AssetEndpoint lets a call scoped to an asset reach the addresses the user
	// typed into that asset's format:"endpoint" config fields — private addresses
	// included, since the user configured them — over HTTP and TCP.
	AssetEndpoint bool `json:"assetEndpoint"`
	// AssetRoute lets a database driver follow a redirect the database answers
	// with (a cluster node, a read replica, a routed listener) by dialing that
	// other address through the asset's own route. Only meaningful with
	// contributes.databaseDrivers.
	AssetRoute bool `json:"assetRoute"`
}

// Parse reads manifest.json strictly. The hostABI is judged first, from a lenient
// read, so a manifest written for another contract reports an *IncompatibleError
// naming the declared and supported versions rather than tripping over a field
// of its shape; a manifest on a supported hostABI must then contain no field this
// contract does not define. Every error names the extension when the manifest
// gives its name.
func Parse(data []byte) (*Manifest, error) {
	var head struct {
		Name          string `json:"name"`
		HostABI       string `json:"hostABI"`
		MinAppVersion string `json:"minAppVersion"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	named := func(err error) error {
		if head.Name == "" {
			return fmt.Errorf("manifest: %w", err)
		}
		return fmt.Errorf("extension %q: manifest: %w", head.Name, err)
	}
	if head.HostABI == "" {
		return nil, named(fmt.Errorf("hostABI is required (supported: %v)", SupportedHostABIs))
	}
	if !slices.Contains(SupportedHostABIs, head.HostABI) {
		return nil, named(&IncompatibleError{
			Reason:            ReasonHostABI,
			HostABI:           head.HostABI,
			MinAppVersion:     head.MinAppVersion,
			SupportedHostABIs: SupportedHostABIs,
		})
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, named(err)
	}
	if err := m.validate(); err != nil {
		return nil, named(err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("name must match %s (got %q)", nameRe.String(), m.Name)
	}
	if m.Version == "" {
		return fmt.Errorf("version is required")
	}
	if !semverRe.MatchString(m.Version) {
		return fmt.Errorf("version must be semver (got %q)", m.Version)
	}
	if m.MinAppVersion != "" && !semverRe.MatchString(m.MinAppVersion) {
		return fmt.Errorf("minAppVersion must be semver (got %q)", m.MinAppVersion)
	}
	if m.Backend.Binary == "" {
		return fmt.Errorf("backend.binary is required — it names the WASM module the host loads")
	}
	if c := m.Permissions.Credentials; c != CredentialAccessNone && c != CredentialAccessRead {
		return fmt.Errorf("permissions.credentials must be \"\" or \"read\" (got %q)", c)
	}
	return m.validateDatabaseDrivers()
}

// validateDatabaseDrivers checks the driver ids the manifest declares —
// well-formed, unique — and the driver-only permissions that need them.
func (m *Manifest) validateDatabaseDrivers() error {
	ids := m.Contributes.DatabaseDrivers
	if m.Permissions.Network.AssetRoute && len(ids) == 0 {
		return fmt.Errorf("permissions.network.assetRoute only applies to an extension that declares contributes.databaseDrivers")
	}
	if m.Permissions.Kerberos && len(ids) == 0 {
		return fmt.Errorf("permissions.kerberos only applies to an extension that declares contributes.databaseDrivers")
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !nameRe.MatchString(id) {
			return fmt.Errorf("contributes.databaseDrivers id %q must match %s", id, nameRe.String())
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("contributes.databaseDrivers lists %q twice", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}
