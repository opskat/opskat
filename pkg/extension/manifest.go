package extension

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/extmanifest"
)

// Manifest is the host's complete view of one extension as the runtime holds it:
// the security contract it reads from manifest.json, the presentation describe()
// answers, and describe()'s contributions, raw. Only the contract is written by
// hand — see extmanifest.Manifest.
//
// The runtime does not read the contributions: each is the business of the
// contribution point that owns its key (ContributionPoint), and the typed view of
// them — asset types, tools, policy, pages, snippets, drivers — is that host
// side's (internal/extreg).
type Manifest struct {
	// -- security contract, from manifest.json --
	extmanifest.Manifest

	// -- presentation, from describe() (see descriptor.go) --
	Icon string       `json:"icon"`
	I18n ManifestI18n `json:"i18n"`

	// contributes is describe()'s contributions, one raw entry per contribution
	// point key, for the points to judge (ExtensionView.Contribution).
	contributes map[string]json.RawMessage
}

// ManifestI18n is the extension's display name and description, as i18n keys
// resolved against its locales/ directory.
type ManifestI18n struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// Contribution is describe()'s raw entry under the contribution point key, nil
// when the extension contributes nothing there.
func (m *Manifest) Contribution(key string) json.RawMessage {
	return m.contributes[key]
}

// ContributionKeys is the contribution point keys describe() answered, sorted.
func (m *Manifest) ContributionKeys() []string {
	keys := make([]string, 0, len(m.contributes))
	for k := range m.contributes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ParseManifest reads the security contract from manifest.json (see
// extmanifest.Parse). The returned Manifest carries nothing from describe() yet —
// that arrives with (*Manifest).apply once describe() has been read, either from
// the guest or from the cache.
func ParseManifest(data []byte) (*Manifest, error) {
	c, err := extmanifest.Parse(data)
	if err != nil {
		return nil, err
	}
	return &Manifest{Manifest: *c}, nil
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
