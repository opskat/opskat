package extension

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

var (
	nameRe           = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	policyIDRe       = regexp.MustCompile(`^ext:[a-z0-9][a-z0-9_-]{0,63}(:[a-z0-9][a-z0-9_-]{0,63})*$`)
	snippetCatIDRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	snippetSeedKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// builtinSnippetCategoryIDs is the immutable set of category ids owned by the app core.
// Extensions may not re-declare these — they are reserved.
// Kept unexported so callers can't mutate it; use IsBuiltinSnippetCategoryID to query.
var builtinSnippetCategoryIDs = map[string]struct{}{
	"shell":  {},
	"sql":    {},
	"redis":  {},
	"mongo":  {},
	"prompt": {},
}

// IsBuiltinSnippetCategoryID reports whether id is reserved by the app core.
func IsBuiltinSnippetCategoryID(id string) bool {
	_, ok := builtinSnippetCategoryIDs[id]
	return ok
}

// Manifest is the host's complete view of one extension: the security contract it
// reads from manifest.json plus the functional face the guest reports through
// describe(). Only the first half is written by hand — see extmanifest.Manifest.
type Manifest struct {
	// -- security contract, from manifest.json --
	extmanifest.Manifest

	// -- functional face, from describe() (see descriptor.go) --
	Icon       string         `json:"icon"`
	I18n       ManifestI18n   `json:"i18n"`
	AssetTypes []AssetTypeDef `json:"assetTypes"`
	Tools      []ToolDef      `json:"tools"`
	Policies   PoliciesDef    `json:"policies"`
	Frontend   FrontendDef    `json:"frontend"`
	Snippets   SnippetsDef    `json:"snippets"`
	// DatabaseDrivers are the driver descriptors, one per contributes.databaseDrivers id.
	DatabaseDrivers []dbdriver.Descriptor `json:"databaseDrivers"`
}

// The functional face is merged from describe() in the wire types the guest SDK
// encodes it with (pkg/extwire). These names are the host's view of them; the
// JSON the frontend reads is the wire shape.

// SnippetsDef declares snippet categories and seed snippets contributed by this extension.
// All fields are optional; an empty block is a no-op.
type SnippetsDef = extwire.Snippets

// SnippetCategoryDef declares a new snippet category id that this extension owns.
//
// Validation rules:
//   - ID must match ^[a-z][a-z0-9-]{0,31}$ (lowercase kebab, <=32 chars).
//   - ID must NOT collide with any builtin category (see IsBuiltinSnippetCategoryID).
//   - ID must be unique within this manifest.
//   - AssetType must be non-empty and must match a type declared in assetTypes[].
type SnippetCategoryDef = extwire.SnippetCategory

// SeedSnippetDef is a read-only snippet shipped with the extension.
//
// Validation rules:
//   - Key must match ^[a-z0-9][a-z0-9-]{0,63}$ and be unique within this manifest.
//   - Name and Content must be non-empty after trimming.
//   - Category must be either a builtin category id or a category declared in THIS manifest.
type SeedSnippetDef = extwire.SeedSnippet

type ManifestI18n = extwire.I18n

type AssetTypeDef struct {
	Type         string         `json:"type"`
	I18n         I18nName       `json:"i18n"`
	ConfigSchema map[string]any `json:"configSchema"`
	Connection   *ConnectionDef `json:"connection,omitempty"`
	Auth         *AuthDef       `json:"auth,omitempty"`
	// TestConnection reports whether the type registered a test-connection
	// handler (opskat.AssetTypeReg.TestConnection in the guest SDK). The
	// asset form shows its "Test connection" button only when this is true;
	// the host dispatches the call itself (Plugin.TestConnection), never
	// through policy — testing a connection is not an operation on the asset.
	TestConnection bool `json:"testConnection,omitempty"`
}

// assetTypeDef is the host's view of a wire asset type: the same declaration,
// with its auth read into AuthDef, which the host renders and injects.
func assetTypeDef(at extwire.AssetType) AssetTypeDef {
	return AssetTypeDef{
		Type:           at.Type,
		I18n:           at.I18n,
		ConfigSchema:   at.ConfigSchema,
		Connection:     at.Connection,
		Auth:           authDef(at.Auth),
		TestConnection: at.TestConnection,
	}
}

// ConnectionDef is the subset of the host-owned connection settings an asset
// type supports. The host renders a declared item in the asset form and detail
// card and applies it when dialing the asset's endpoint; an undeclared item is
// neither shown nor applied. The extension never reads these settings — the SSH
// tunnel is the asset's own SSHTunnelID column, outside its config.
type ConnectionDef = extwire.Connection

// bindsEndpoint reports whether the type declares anything the host applies only
// on a connection to the asset's endpoint: credential injection or a connection
// setting.
func (at AssetTypeDef) bindsEndpoint() bool {
	c := at.Connection
	return at.Auth != nil || (c != nil && (c.SSHTunnel || c.ProxyChain || c.TLS))
}

// validateEndpointBindings refuses an asset type whose auth or connection
// declaration could never take effect: both apply only to a request to one of the
// asset's endpoints, and an extension without network.assetEndpoint has none. It
// runs once describe() is merged in, since the declaration comes from the guest
// and the permission from manifest.json.
//
// It is also the one post-merge check of the guest's answer against the manifest,
// so the database driver ids the manifest declares are compared with the ones
// describe() provided here too (see checkDatabaseDrivers).
func (m *Manifest) validateEndpointBindings() error {
	if err := m.checkDatabaseDrivers(); err != nil {
		return err
	}
	if m.Permissions.Network.AssetEndpoint {
		return nil
	}
	for _, at := range m.AssetTypes {
		if at.bindsEndpoint() {
			return fmt.Errorf("asset type %q declares auth or connection, which apply only to the asset's endpoint, but the manifest does not declare permissions.network.assetEndpoint", at.Type)
		}
	}
	return nil
}

// AssetTypeDef returns the declaration of assetType, nil when the extension does
// not register it.
func (m *Manifest) AssetTypeDef(assetType string) *AssetTypeDef {
	for i := range m.AssetTypes {
		if m.AssetTypes[i].Type == assetType {
			return &m.AssetTypes[i]
		}
	}
	return nil
}

type I18nName = extwire.I18nName

type I18nNameDesc = extwire.I18nNameDesc

// ToolDef is one tool. Its policy action is declared at the tool's registration
// in the guest, which is what makes the guest's policy answer unable to drift
// from its tool table; its timeout is the tool's to declare because only the
// tool knows whether it answers in a second or scans an index for minutes.
type ToolDef = extwire.Tool

// MaxToolTimeout is the longest timeout a tool may declare. A tool call holds
// one of the extension's few instance slots for as long as it runs; work that
// needs longer is an action, which streams progress and can be canceled.
const MaxToolTimeout = 10 * time.Minute

type I18nDesc = extwire.I18nDesc

// PoliciesDef is the policy face merged onto the manifest: the guest's
// extwire.Policy plus Actions, derived from the tools.
type PoliciesDef struct {
	Type    string           `json:"type"`
	Actions []string         `json:"actions"`
	Groups  []PolicyGroupDef `json:"groups"`
	Default []string         `json:"default"`
}

type PolicyGroupDef = extwire.PolicyGroup

type FrontendDef = extwire.Pages

type PageDef = extwire.Page

// ParseManifest reads the security contract from manifest.json (see
// extmanifest.Parse). The returned Manifest carries no functional face yet — that
// arrives with (*Manifest).apply once describe() has been read, either from the
// guest or from the cache.
func ParseManifest(data []byte) (*Manifest, error) {
	c, err := extmanifest.Parse(data)
	if err != nil {
		return nil, err
	}
	return &Manifest{Manifest: *c}, nil
}

// Localized returns a shallow copy of the manifest with all i18n string fields
// resolved using the provided translate function.
func (m *Manifest) Localized(tr func(key string) string) *Manifest {
	out := *m
	out.I18n = ManifestI18n{
		DisplayName: tr(m.I18n.DisplayName),
		Description: tr(m.I18n.Description),
	}

	if len(m.AssetTypes) > 0 {
		out.AssetTypes = make([]AssetTypeDef, len(m.AssetTypes))
		for i, at := range m.AssetTypes {
			at.I18n = I18nName{Name: tr(at.I18n.Name)}
			at.ConfigSchema = localizeConfigSchema(at.ConfigSchema, tr)
			out.AssetTypes[i] = at
		}
	}

	if len(m.Tools) > 0 {
		out.Tools = make([]ToolDef, len(m.Tools))
		for i, t := range m.Tools {
			t.I18n = I18nDesc{Description: tr(t.I18n.Description)}
			out.Tools[i] = t
		}
	}

	if len(m.Policies.Groups) > 0 {
		out.Policies.Groups = make([]PolicyGroupDef, len(m.Policies.Groups))
		for i, pg := range m.Policies.Groups {
			pg.I18n = I18nNameDesc{
				Name:        tr(pg.I18n.Name),
				Description: tr(pg.I18n.Description),
			}
			out.Policies.Groups[i] = pg
		}
	}

	if len(m.Frontend.Pages) > 0 {
		out.Frontend.Pages = make([]PageDef, len(m.Frontend.Pages))
		for i, p := range m.Frontend.Pages {
			p.I18n = I18nName{Name: tr(p.I18n.Name)}
			out.Frontend.Pages[i] = p
		}
	}

	if len(m.Snippets.Categories) > 0 {
		out.Snippets.Categories = make([]SnippetCategoryDef, len(m.Snippets.Categories))
		for i, c := range m.Snippets.Categories {
			c.I18n = I18nName{Name: tr(c.I18n.Name)}
			out.Snippets.Categories[i] = c
		}
	}

	return &out
}

// localizeConfigSchema translates title, placeholder, description and enumLabels fields in a JSON Schema.
func localizeConfigSchema(schema map[string]any, tr func(string) string) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		out[k] = v
	}
	// Translate top-level title/placeholder/description
	for _, field := range []string{"title", "placeholder", "description"} {
		if s, ok := out[field].(string); ok && s != "" {
			out[field] = tr(s)
		}
	}
	// Enum option labels are i18n keys, like title.
	if labels, ok := out["enumLabels"].([]any); ok {
		translated := make([]any, len(labels))
		for i, l := range labels {
			if key, ok := l.(string); ok {
				translated[i] = tr(key)
			} else {
				translated[i] = l
			}
		}
		out["enumLabels"] = translated
	}
	// Recurse into properties
	if props, ok := out["properties"].(map[string]any); ok {
		newProps := make(map[string]any, len(props))
		for name, propVal := range props {
			if propMap, ok := propVal.(map[string]any); ok {
				newProps[name] = localizeConfigSchema(propMap, tr)
			} else {
				newProps[name] = propVal
			}
		}
		out["properties"] = newProps
	}
	return out
}

// -- Capability enforcement --------------------------------------------------

// CheckFSRead returns nil if reading the given absolute path is permitted
// by the extension's permissions.fs.read. extDir is the extension's own
// directory (used to resolve ${EXT_DIR} placeholders).
func (m *Manifest) CheckFSRead(path, extDir string) error {
	return checkFSPath(path, extDir, m.Permissions.FS.Read, "read")
}

// CheckFSWrite returns nil if writing to the given absolute path is permitted
// by the extension's permissions.fs.write.
func (m *Manifest) CheckFSWrite(path, extDir string) error {
	return checkFSPath(path, extDir, m.Permissions.FS.Write, "write")
}

func checkFSPath(path, extDir string, patterns []string, kind string) error {
	if path == "" {
		return fmt.Errorf("fs %s denied: empty path", kind)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("fs %s denied: resolve path: %w", kind, err)
	}
	// Reject parent-traversal leftovers after absolute resolution.
	if strings.Contains(abs, "..") {
		return fmt.Errorf("fs %s denied: path contains traversal (%q)", kind, path)
	}
	for _, pat := range patterns {
		resolved := resolvePattern(pat, extDir)
		if resolved == "" {
			continue
		}
		if matchPathPrefix(abs, resolved) {
			return nil
		}
	}
	return fmt.Errorf("fs %s denied: %q not in permissions.fs.%s", kind, abs, kind)
}

// resolvePattern substitutes ${EXT_DIR} and cleans the pattern.
// Returns empty string on failure.
func resolvePattern(pattern, extDir string) string {
	if pattern == "" {
		return ""
	}
	p := strings.ReplaceAll(pattern, "${EXT_DIR}", extDir)
	// Strip trailing "/**" if present — we match by prefix.
	p = strings.TrimSuffix(p, "/**")
	p = strings.TrimSuffix(p, "**")
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// matchPathPrefix returns true if path is within root (root is a prefix,
// separated by a path separator).
func matchPathPrefix(path, root string) bool {
	if path == root {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(path, root)
}

// CheckHTTPURL returns nil if the given URL is permitted by the extension's
// permissions.network.http. Enforces:
//  1. The URL prefix must match an allowlist entry.
//  2. Private/loopback/link-local destinations are rejected unless allowPrivate is true.
//  3. The scheme must be http or https.
//
// allowPrivate should be true ONLY when the extension declared
// permissions.network.allowPrivate.
func (m *Manifest) CheckHTTPURL(urlStr string, allowPrivate bool) error {
	if urlStr == "" {
		return fmt.Errorf("http denied: empty url")
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("http denied: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("http denied: unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("http denied: missing host")
	}

	// Enforce allowlist prefix match.
	matched := false
	for _, allowed := range m.Permissions.Network.HTTP {
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(urlStr, allowed) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("http denied: %q not in permissions.network.http", urlStr)
	}

	// Reject private-network destinations unless the extension may reach them.
	if !allowPrivate && isPrivateHost(u.Hostname()) {
		return fmt.Errorf("http denied: %q resolves to private/loopback/link-local address (allowPrivate=%v, permissions.network.allowPrivate=%v)",
			u.Hostname(), allowPrivate, m.Permissions.Network.AllowPrivate)
	}
	return nil
}

// isPrivateHost returns true if the host is a literal private IP (RFC1918,
// loopback, link-local, or IPv6 unique-local / link-local).
// For DNS hostnames it returns false — the HTTP stack will resolve at dial time,
// and the private-network check must also run at dial time (see DialGuard).
func isPrivateHost(host string) bool {
	if host == "" {
		return true
	}
	// Strip brackets from IPv6 literals.
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// DNS hostname — dial-time guard handles this.
		return false
	}
	return isPrivateIP(ip)
}

// IsPrivateIP returns true for RFC1918, loopback, link-local, unspecified,
// IPv6 ULA, IPv6 link-local, and AWS/GCP metadata endpoints.
// Exported so that the dial-time guard in io_http.go can reuse it.
func IsPrivateIP(ip net.IP) bool {
	return isPrivateIP(ip)
}

// isPrivateIP returns true for RFC1918, loopback, link-local, unspecified,
// IPv6 ULA, IPv6 link-local, and AWS/GCP metadata endpoints.
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// IPv6 unique-local is covered by IsPrivate() in Go 1.17+.
	// AWS metadata: 169.254.169.254 (IsLinkLocalUnicast covers 169.254/16).
	return false
}

// CheckCredentialRead returns nil if the extension is allowed to request
// plaintext credential values. Default is deny.
func (m *Manifest) CheckCredentialRead() error {
	if m.Permissions.Credentials != extmanifest.CredentialAccessRead {
		return fmt.Errorf("credentials denied: extension must declare permissions.credentials=\"read\" to access plaintext passwords")
	}
	return nil
}
