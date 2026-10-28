package extmanifest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// manifest builds a manifest.json for extension "x" on hostABI 1.0 whose
// permissions and contributes blocks are the given raw JSON ("" leaves it out).
func manifest(permissions, contributes string) []byte {
	s := `{"name":"x","version":"1.0.0","hostABI":"1.0","backend":{"runtime":"wasm","binary":"main.wasm"}`
	if permissions != "" {
		s += `,"permissions":` + permissions
	}
	if contributes != "" {
		s += `,"contributes":` + contributes
	}
	return []byte(s + "}")
}

func TestParseNewShape(t *testing.T) {
	m, err := Parse([]byte(`{
		"name": "oss",
		"version": "1.2.3",
		"minAppVersion": "1.15.0",
		"hostABI": "1.0",
		"backend": {"runtime": "wasm", "binary": "main.wasm"},
		"permissions": {
			"fs": {"read": ["${EXT_DIR}/**"], "write": ["/tmp/oss"]},
			"credentials": "read",
			"kerberos": true,
			"network": {
				"http": ["https://oss.example.com/"],
				"allowPrivate": true,
				"assetEndpoint": true,
				"assetRoute": true
			}
		},
		"contributes": {"databaseDrivers": ["ossdb"]}
	}`))
	require.NoError(t, err)
	assert.Equal(t, &Manifest{
		Name:          "oss",
		Version:       "1.2.3",
		MinAppVersion: "1.15.0",
		HostABI:       "1.0",
		Backend:       Backend{Runtime: "wasm", Binary: "main.wasm"},
		Permissions: Permissions{
			FS:          FSPermission{Read: []string{"${EXT_DIR}/**"}, Write: []string{"/tmp/oss"}},
			Credentials: CredentialAccessRead,
			Kerberos:    true,
			Network: NetworkPermission{
				HTTP:          []string{"https://oss.example.com/"},
				AllowPrivate:  true,
				AssetEndpoint: true,
				AssetRoute:    true,
			},
		},
		Contributes: Contributes{DatabaseDrivers: []string{"ossdb"}},
	}, m)
}

func TestParseRejectsOldHostABIsAsIncompatible(t *testing.T) {
	for _, abi := range []string{"2.0", "2.1", "2.2", "2.3", "2.4", "2.5"} {
		t.Run(abi, func(t *testing.T) {
			// A nightly-era manifest: capabilities, which the new shape no longer
			// knows. The version decides first, so it is reported as incompatible
			// rather than as an unknown field.
			_, err := Parse([]byte(`{"name":"oracle","version":"0.1.0","hostABI":"` + abi + `",` +
				`"backend":{"runtime":"wasm","binary":"main.wasm"},"capabilities":{"databaseDrivers":["oracle"]}}`))
			var inc *IncompatibleError
			require.True(t, errors.As(err, &inc), "want IncompatibleError, got %v", err)
			assert.Equal(t, ReasonHostABI, inc.Reason)
			assert.Equal(t, abi, inc.HostABI)
			assert.Equal(t, []string{"1.0"}, inc.SupportedHostABIs)
			assert.Contains(t, err.Error(), "hostABI "+abi)
			assert.Contains(t, err.Error(), "1.0")
			assert.Contains(t, err.Error(), `"oracle"`)
		})
	}
}

func TestParseRejectsOldShapeOnHostABI1(t *testing.T) {
	// A v1.14.0-era manifest declared hostABI 1.0 too, with its contributions
	// written into manifest.json.
	for field, body := range map[string]string{
		"capabilities": `"capabilities":{"http":{"allowlist":["https://example.com/"]}}`,
		"assetTypes":   `"assetTypes":[{"type":"kafka"}]`,
		"tools":        `"tools":[{"name":"list"}]`,
	} {
		t.Run(field, func(t *testing.T) {
			_, err := Parse([]byte(`{"name":"kafka","version":"1.0.0","hostABI":"1.0",` +
				`"backend":{"runtime":"wasm","binary":"main.wasm"},` + body + `}`))
			require.Error(t, err)
			var inc *IncompatibleError
			assert.False(t, errors.As(err, &inc), "an old-shape 1.0 manifest passes the version check: %v", err)
			assert.Contains(t, err.Error(), `"kafka"`)
			assert.Contains(t, err.Error(), `unknown field "`+field+`"`)
		})
	}
}

func TestParseRejectsUnknownFieldsInsideBlocks(t *testing.T) {
	cases := map[string][]byte{
		"tunnel":     manifest(`{"tunnel":true}`, ""),
		"allowlist":  manifest(`{"network":{"allowlist":["https://a/"]}}`, ""),
		"tools":      manifest("", `{"tools":[]}`),
		"assetTypes": manifest("", `{"databaseDrivers":["d"],"assetTypes":[]}`),
	}
	for field, data := range cases {
		t.Run(field, func(t *testing.T) {
			_, err := Parse(data)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `"x"`)
			assert.Contains(t, err.Error(), `unknown field "`+field+`"`)
		})
	}
}

func TestParseValidatesFields(t *testing.T) {
	cases := []struct {
		name, json, want string
	}{
		{"missing name", `{"version":"1.0.0","hostABI":"1.0","backend":{"binary":"main.wasm"}}`, "name is required"},
		{"name with path", `{"name":"../../etc","version":"1.0.0","hostABI":"1.0","backend":{"binary":"main.wasm"}}`, "name must match"},
		{"uppercase name", `{"name":"MyExt","version":"1.0.0","hostABI":"1.0","backend":{"binary":"main.wasm"}}`, "name must match"},
		{"missing version", `{"name":"x","hostABI":"1.0","backend":{"binary":"main.wasm"}}`, "version is required"},
		{"non-semver version", `{"name":"x","version":"1.0","hostABI":"1.0","backend":{"binary":"main.wasm"}}`, "version must be semver"},
		{"bad minAppVersion", `{"name":"x","version":"1.0.0","minAppVersion":"latest","hostABI":"1.0","backend":{"binary":"main.wasm"}}`, "minAppVersion"},
		{"missing hostABI", `{"name":"x","version":"1.0.0","backend":{"binary":"main.wasm"}}`, "hostABI is required"},
		{"missing binary", `{"name":"x","version":"1.0.0","hostABI":"1.0"}`, "backend.binary"},
		{"credentials write", string(manifest(`{"credentials":"write"}`, "")), "permissions.credentials"},
		{"credentials wrong type", string(manifest(`{"credentials":true}`, "")), "credentials"},
		{"driver id", string(manifest("", `{"databaseDrivers":["Bad Id"]}`)), "Bad Id"},
		{"driver twice", string(manifest("", `{"databaseDrivers":["a","a"]}`)), "twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// network.assetRoute and kerberos are basic permissions any extension may
// declare: they apply to whatever call carries a scope with a route or a ticket
// source, a tool call included, not only to a database driver's.
func TestParseAcceptsAssetRouteAndKerberosWithoutDrivers(t *testing.T) {
	m, err := Parse(manifest(`{"kerberos":true,"network":{"assetRoute":true}}`, ""))
	require.NoError(t, err)
	assert.True(t, m.Permissions.Kerberos)
	assert.True(t, m.Permissions.Network.AssetRoute)
	assert.Empty(t, m.Contributes.DatabaseDrivers)
}

func TestParseAcceptsMinimalManifest(t *testing.T) {
	m, err := Parse(manifest("", ""))
	require.NoError(t, err)
	assert.Equal(t, Permissions{}, m.Permissions)
	assert.Empty(t, m.Contributes.DatabaseDrivers)
}

func TestParseRejectsNewerHostABI(t *testing.T) {
	for _, abi := range []string{"1.1", "3.0"} {
		_, err := Parse([]byte(`{"name":"x","version":"1.0.0","hostABI":"` + abi + `","backend":{"binary":"main.wasm"}}`))
		var inc *IncompatibleError
		require.True(t, errors.As(err, &inc), "%s: %v", abi, err)
		assert.Equal(t, abi, inc.HostABI)
	}
}
