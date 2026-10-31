package extreg

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// wireManifestJSON is a manifest.json contract and wireDescribeJSON a describe()
// answer contributing to every point: together, what one installed extension is.
const wireManifestJSON = `{"name":"wire","version":"1.2.3","hostABI":"1.0",` +
	`"backend":{"runtime":"wasm","binary":"main.wasm"},` +
	`"permissions":{"network":{"assetEndpoint":true}},"contributes":{"databaseDrivers":["mysql-ext"]}}`

const wireDriverJSON = `{"id":"mysql-ext","name":"MySQL Ext","icon":"x","order":50,"aliases":[],"defaultPort":3306,` +
	`"form":[{"tab":"connection","fields":["host","port"]}],"capabilities":["schemas"],` +
	`"createDatabaseOptions":[],"alterTableAttributes":[],"driverFields":[]}`

const wireDescribeJSON = `{"icon":"cloud","i18n":{"displayName":"d","description":"x"},"contributes":{` +
	`"assetTypes":[{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{` +
	`"endpoint":{"type":"string","format":"endpoint"},"authType":{"type":"string"},"password":{"type":"string","format":"password"}}},` +
	`"connection":{"sshTunnel":true,"tls":true},` +
	`"auth":{"selector":"authType","groups":[{"when":"token","bindings":[{"in":"header","name":"Authorization","value":"Bearer {{password}}"}]}]},` +
	`"testConnection":true}],` +
	`"tools":[{"name":"list","i18n":{"description":"td"},"policyAction":"list","parameters":{"type":"object","properties":{"body":{"type":"string"}}},"timeoutMs":3000,"large":["body"]},` +
	`{"name":"page","pageOnly":true,"noTimeout":true,"parameters":{"type":"object","properties":{}}}],` +
	`"policy":{"type":"x","groups":[{"id":"ext:x:ro","i18n":{"name":"g","description":"gd"},"policy":{"allow_list":["list"]}}],"default":["ext:x:ro"]},` +
	`"snippets":{"categories":[{"id":"xcat","assetType":"x","i18n":{"name":"c"}}],"seed":[{"key":"s","name":"s","category":"xcat","content":"ls","description":"sd"}]},` +
	`"pages":{"entry":"frontend/index.js","styles":"frontend/index.css","pages":[{"id":"p","slot":"asset.connect","i18n":{"name":"pn"},"component":"P"}]},` +
	`"databaseDrivers":[` + wireDriverJSON + `]}}`

// mergedManifestJSON is the merged manifest the frontend reads for that extension
// (the extension list, the page loader), byte for byte what the host handed it
// while the runtime still carried the typed face.
const mergedManifestJSON = `{"name":"wire","version":"1.2.3","minAppVersion":"","hostABI":"1.0","backend":{"runtime":"wasm","binary":"main.wasm"},` +
	`"permissions":{"fs":{"read":null,"write":null},"credentials":"","kerberos":false,"network":{"http":null,"allowPrivate":false,"assetEndpoint":true,"assetRoute":false}},` +
	`"contributes":{"databaseDrivers":["mysql-ext"]},"icon":"cloud","i18n":{"displayName":"d","description":"x"},` +
	`"assetTypes":[{"type":"x","i18n":{"name":"n"},"configSchema":{"properties":{"authType":{"type":"string"},"endpoint":{"format":"endpoint","type":"string"},"password":{"format":"password","type":"string"}},"type":"object"},` +
	`"connection":{"sshTunnel":true,"tls":true},"auth":{"selector":"authType","groups":[{"when":"token","bindings":[{"in":"header","name":"Authorization","value":"Bearer {{password}}"}],"fields":["password"]}]},"testConnection":true}],` +
	`"tools":[{"name":"list","i18n":{"description":"td"},"parameters":{"properties":{"body":{"type":"string"}},"type":"object"},"policyAction":"list","timeoutMs":3000,"large":["body"]},` +
	`{"name":"page","i18n":{"description":""},"parameters":{"properties":{},"type":"object"},"pageOnly":true,"noTimeout":true}],` +
	`"policies":{"type":"x","actions":["list"],"groups":[{"id":"ext:x:ro","i18n":{"name":"g","description":"gd"},"policy":{"allow_list":["list"]}}],"default":["ext:x:ro"]},` +
	`"frontend":{"entry":"frontend/index.js","styles":"frontend/index.css","pages":[{"id":"p","slot":"asset.connect","i18n":{"name":"pn"},"component":"P"}]},` +
	`"snippets":{"categories":[{"id":"xcat","assetType":"x","i18n":{"name":"c"}}],"seed":[{"key":"s","name":"s","category":"xcat","content":"ls","description":"sd"}]},` +
	`"databaseDrivers":[{"id":"mysql-ext","name":"MySQL Ext","icon":"cloud","order":50,"aliases":[],"defaultPort":3306,"form":[{"tab":"connection","fields":["host","port"]}],` +
	`"capabilities":["schemas"],"canDisableForeignKeyChecks":false,"locatesRowWithoutPrimaryKey":false,"createDatabaseOptions":[],"alterTableAttributes":[],"driverFields":[]}]}`

// described is the runtime's manifest of an extension: the contract merged with
// the describe() answer, as loading it produces.
func described(t *testing.T, manifestJSON, describeJSON string) *extension.Manifest {
	t.Helper()
	m, err := extension.ParseManifest([]byte(manifestJSON))
	require.NoError(t, err)
	d, err := extension.ParseDescriptor([]byte(describeJSON))
	require.NoError(t, err)
	return extension.Described(m.Manifest, d)
}

// describedWith is an extension contributing exactly the given describe() entries.
func describedWith(t *testing.T, contributes string) *extension.Manifest {
	t.Helper()
	return described(t, `{"name":"x","version":"1.0.0","hostABI":"1.0","backend":{"runtime":"wasm","binary":"main.wasm"}}`,
		`{"contributes":{`+contributes+`}}`)
}

// The merged manifest the frontend reads keeps its shape now that the typed face
// is decoded here from the runtime's raw contributions.
func TestManifestWireShape(t *testing.T) {
	view, err := ManifestOf(described(t, wireManifestJSON, wireDescribeJSON))
	require.NoError(t, err)

	raw, err := json.Marshal(view)
	require.NoError(t, err)
	assert.JSONEq(t, mergedManifestJSON, string(raw))

	t.Run("localized, every i18n key is resolved and nothing else changes", func(t *testing.T) {
		localized, err := json.Marshal(view.Localized(func(k string) string { return "T:" + k }))
		require.NoError(t, err)
		var got, want map[string]any
		require.NoError(t, json.Unmarshal(localized, &got))
		require.NoError(t, json.Unmarshal([]byte(mergedManifestJSON), &want))
		want["i18n"] = map[string]any{"displayName": "T:d", "description": "T:x"}
		want["assetTypes"].([]any)[0].(map[string]any)["i18n"] = map[string]any{"name": "T:n"}
		want["tools"].([]any)[0].(map[string]any)["i18n"] = map[string]any{"description": "T:td"}
		want["tools"].([]any)[1].(map[string]any)["i18n"] = map[string]any{"description": "T:"}
		want["policies"].(map[string]any)["groups"].([]any)[0].(map[string]any)["i18n"] = map[string]any{"name": "T:g", "description": "T:gd"}
		want["frontend"].(map[string]any)["pages"].([]any)[0].(map[string]any)["i18n"] = map[string]any{"name": "T:pn"}
		want["snippets"].(map[string]any)["categories"].([]any)[0].(map[string]any)["i18n"] = map[string]any{"name": "T:c"}
		assert.Equal(t, want, got)

		again, err := json.Marshal(view)
		require.NoError(t, err)
		assert.JSONEq(t, mergedManifestJSON, string(again), "localizing returns a copy")
	})

	t.Run("an answer the guest SDK encodes reads the same", func(t *testing.T) {
		w, err := extwire.Parse([]byte(wireDescribeJSON))
		require.NoError(t, err)
		encoded, err := json.Marshal(w)
		require.NoError(t, err)
		again, err := ManifestOf(described(t, wireManifestJSON, string(encoded)))
		require.NoError(t, err)
		assert.Equal(t, view, again)
	})
}

func TestManifestOf(t *testing.T) {
	t.Run("the functional face is read off the contributions", func(t *testing.T) {
		view, err := ManifestOf(described(t, wireManifestJSON, wireDescribeJSON))
		require.NoError(t, err)
		assert.Equal(t, "wire", view.Name, "the security contract is the runtime manifest's")
		assert.True(t, view.Permissions.Network.AssetEndpoint)
		assert.Equal(t, "cloud", view.Icon)
		assert.Equal(t, "d", view.I18n.DisplayName)
		require.Len(t, view.AssetTypes, 1)
		assert.Same(t, &view.AssetTypes[0], view.AssetType("x"))
		assert.Nil(t, view.AssetType("y"))
		assert.Equal(t, "frontend/index.js", view.Frontend.Entry)
		assert.Equal(t, "xcat", view.Snippets.Categories[0].ID)
		assert.Equal(t, []string{"ext:x:ro"}, view.Policies.Default)
	})

	t.Run("the action set is derived from the tools, not declared twice", func(t *testing.T) {
		view, err := ManifestOf(describedWith(t, `"tools":[`+
			`{"name":"search","policyActions":["search","index.read"],"parameters":{"type":"object","properties":{}}},`+
			`{"name":"list_objects","policyAction":"list","parameters":{"type":"object","properties":{}}},`+
			`{"name":"get_object","policyAction":"list","parameters":{"type":"object","properties":{}}}]`))
		require.NoError(t, err)
		assert.Equal(t, []string{"index.read", "list", "search"}, view.Policies.Actions)
	})

	t.Run("a tool's declared call timeout and large parameters are kept", func(t *testing.T) {
		view, err := ManifestOf(describedWith(t, `"tools":[`+
			`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"body":{"type":"string"}}},"timeoutMs":600000,"large":["body"]},`+
			`{"name":"u","policyAction":"read","parameters":{"type":"object","properties":{}}}]`))
		require.NoError(t, err)
		assert.Equal(t, 10*time.Minute, view.Tools[0].Timeout())
		assert.Equal(t, []string{"body"}, view.Tools[0].Large)
		assert.Equal(t, time.Duration(0), view.Tools[1].Timeout(), "undeclared means the host default")
	})

	assetType := func(extra string) string {
		return `"assetTypes":[{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{` +
			`"endpoint":{"type":"string","format":"endpoint"},"authType":{"type":"string"},` +
			`"username":{"type":"string"},"password":{"type":"string","format":"password"}}}` + extra + `}]`
	}

	// 连接配置区（隧道 / 代理链 / TLS）由宿主拥有，资产类型只声明支持哪几项；声明里出现宿主
	// 不认识的项要在解码时拒绝，而不是默默忽略成"不生效"。
	t.Run("an asset type declares which host-owned connection settings it supports", func(t *testing.T) {
		view, err := ManifestOf(describedWith(t, assetType(`,"connection":{"sshTunnel":true}`)))
		require.NoError(t, err)
		assert.Equal(t, &extwire.Connection{SSHTunnel: true}, view.AssetTypes[0].Connection)

		view, err = ManifestOf(describedWith(t, assetType("")))
		require.NoError(t, err)
		assert.Nil(t, view.AssetTypes[0].Connection, "no declaration means no connection settings")

		_, err = ManifestOf(describedWith(t, assetType(`,"connection":{"sshTunnel":true,"vpn":true}`)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"vpn"`)
	})

	// 凭据注入的 auth 绑定读进宿主的 AuthDef，由宿主在请求 endpoint 时渲染。
	t.Run("an asset type's auth bindings are read for the host to inject", func(t *testing.T) {
		view, err := ManifestOf(describedWith(t, assetType(`,"auth":{"selector":"authType","groups":[`+
			`{"when":"basic","bindings":[{"in":"basic","value":"{{username}}:{{password}}"}]},`+
			`{"when":"token","bindings":[{"in":"header","name":"Authorization","value":"ApiKey {{base64(username, \":\", password)}}"},`+
			`{"in":"query","name":"sig","value":"{{ password }}"}]}]}`)))
		require.NoError(t, err)
		auth := view.AssetTypes[0].Auth
		require.NotNil(t, auth)
		assert.Equal(t, "authType", auth.Selector)
		require.Len(t, auth.Groups, 2)
		assert.Equal(t, AuthBinding{In: "header", Name: "Authorization", Value: `ApiKey {{base64(username, ":", password)}}`}, auth.Groups[1].Bindings[0])

		view, err = ManifestOf(describedWith(t, assetType("")))
		require.NoError(t, err)
		assert.Nil(t, view.AssetTypes[0].Auth, "no declaration means no injection")

		_, err = ManifestOf(describedWith(t, assetType(`,"auth":{"groups":[{"bindings":[{"in":"header","header":"Authorization","value":"{{password}}"}]}]}`)))
		require.Error(t, err, "an unknown key is refused rather than ignored")
		assert.Contains(t, err.Error(), `"header"`)
	})

	t.Run("a described driver carries the extension's icon: it is the one icon the user sees on it", func(t *testing.T) {
		view, err := ManifestOf(described(t, wireManifestJSON, `{"icon":"ext-icon","contributes":{"databaseDrivers":[`+wireDriverJSON+`]}}`))
		require.NoError(t, err)
		require.Len(t, view.DatabaseDrivers, 1)
		assert.Equal(t, "ext-icon", view.DatabaseDrivers[0].Icon)
	})

	t.Run("an extension that contributes nothing has an empty face", func(t *testing.T) {
		view, err := ManifestOf(describedWith(t, ""))
		require.NoError(t, err)
		assert.Empty(t, view.AssetTypes)
		assert.Empty(t, view.Tools)
		assert.Empty(t, view.Policies.Type)
	})
}

func TestLocalizeConfigSchemaEnumLabels(t *testing.T) {
	out := localizeConfigSchema(map[string]any{"properties": map[string]any{
		"auth": map[string]any{"enum": []any{"a"}, "enumLabels": []any{"k.a"}, "default": "a"},
	}}, func(k string) string { return "T:" + k })
	auth := out["properties"].(map[string]any)["auth"].(map[string]any)
	assert.Equal(t, []any{"T:k.a"}, auth["enumLabels"])
	assert.Equal(t, []any{"a"}, auth["enum"])
}
