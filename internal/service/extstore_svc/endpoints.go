package extstore_svc

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/ociclient"
)

// OfficialIndexURL is the official index on the extensions repo's main branch;
// its signature is the same URL plus ".sig". Both are fetched through the app's
// download mirror.
const OfficialIndexURL = "https://raw.githubusercontent.com/opskat/extensions/main/index.json"

// Verification-run overrides. The official index is not published yet and a
// verification run must not depend on it, so a run marked OPSKAT_E2E=1 (e2e,
// the interactive sandbox) may point the store at a test index, trust test keys
// and pull from a test registry. Outside OPSKAT_E2E=1 they are ignored.
const (
	// EnvIndexURL replaces OfficialIndexURL (the .sig is still URL + ".sig",
	// and the download mirror is still applied).
	EnvIndexURL = "OPSKAT_E2E_EXT_INDEX_URL"
	// EnvPublicKeys replaces officialPublicKeys: comma-separated standard
	// base64 ed25519 public keys.
	EnvPublicKeys = "OPSKAT_E2E_EXT_INDEX_KEYS"
	// EnvRegistryHost replaces the "Extension downloads" registry host
	// (host[:port]) that PullRegistry returns. Written http://host[:port] it
	// selects a plain-http test registry — the only way a pull goes over plain
	// http; the user's mirror setting is always https.
	EnvRegistryHost = "OPSKAT_E2E_EXT_REGISTRY"
)

// e2eOverride returns the value of a verification-run override, or "" when the
// process is not a verification run or the override is unset.
func e2eOverride(name string) string {
	if os.Getenv("OPSKAT_E2E") != "1" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(name))
}

// indexURL is the index.json URL before the download mirror is applied.
func indexURL() string {
	if u := e2eOverride(EnvIndexURL); u != "" {
		return u
	}
	return OfficialIndexURL
}

// trustedKeyText is the trusted public keys as configured (base64 text).
func trustedKeyText() []string {
	v := e2eOverride(EnvPublicKeys)
	if v == "" {
		return officialPublicKeys
	}
	var out []string
	for _, k := range strings.Split(v, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// trustedKeys decodes trustedKeyText. An empty result is not an error: Verify
// then accepts no signature at all.
func trustedKeys() ([]ed25519.PublicKey, error) {
	text := trustedKeyText()
	keys := make([]ed25519.PublicKey, 0, len(text))
	for i, k := range text {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trusted extension index key %d is not a base64 ed25519 public key", i)
		}
		keys = append(keys, raw)
	}
	return keys, nil
}

// PullRegistry is the registry extension packages are pulled from: the
// "Extension downloads" setting over https — a host, optionally followed by the
// path a mirror keeps the upstream registry under (mirror.example.com/ghcr.io) —
// or EnvRegistryHost in a verification run. Package pulls must resolve their
// registry here, never from bootstrap directly, so the override covers them too.
func PullRegistry() ociclient.Registry {
	if h := e2eOverride(EnvRegistryHost); h != "" {
		if rest, ok := strings.CutPrefix(h, "http://"); ok {
			return ociclient.Registry{Host: rest, PlainHTTP: true}
		}
		return ociclient.Registry{Host: h}
	}
	host, prefix, _ := strings.Cut(bootstrap.ExtensionRegistry(), "/")
	return ociclient.Registry{Host: host, Prefix: prefix}
}
