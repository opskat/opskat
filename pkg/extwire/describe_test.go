package extwire

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What one side Puts the other Gets back, keyed by contribution point.
func TestDescribeContributionsRoundTrip(t *testing.T) {
	sent := Describe{Icon: "cloud", I18n: I18n{DisplayName: "d", Description: "x"}}
	tools := []Tool{{Name: "list", PolicyAction: "list", Parameters: map[string]any{"type": "object"}}}
	require.NoError(t, sent.Put(KeyTools, tools))
	require.NoError(t, sent.Put(KeyPolicy, Policy{Type: "x", Default: []string{"ext:x:ro"}}))
	data, err := json.Marshal(sent)
	require.NoError(t, err)

	got, err := Parse(data)
	require.NoError(t, err)
	assert.Equal(t, "cloud", got.Icon)
	assert.Equal(t, "d", got.I18n.DisplayName)
	var gotTools []Tool
	require.NoError(t, got.Get(KeyTools, &gotTools))
	assert.Equal(t, tools, gotTools)
	var policy Policy
	require.NoError(t, got.Get(KeyPolicy, &policy))
	assert.Equal(t, Policy{Type: "x", Default: []string{"ext:x:ro"}}, policy)

	pages := Pages{Entry: "keep"}
	require.NoError(t, got.Get(KeyPages, &pages))
	assert.Equal(t, Pages{Entry: "keep"}, pages, "an absent key leaves the destination untouched")
}

func TestParseRefusesAnotherShape(t *testing.T) {
	_, err := Parse([]byte(`{"assetTypes":[],"policies":{"type":"x"}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"assetTypes"`)
}

func TestGetNamesTheContributionThatDoesNotDecode(t *testing.T) {
	d, err := Parse([]byte(`{"contributes":{"tools":{"name":"not a list"}}}`))
	require.NoError(t, err)
	var tools []Tool
	err = d.Get(KeyTools, &tools)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contributes.tools")
}

// Auth and connection are applied by the host; a key it does not read must not
// load as a setting that silently never takes effect.
func TestAuthAndConnectionRefuseUnknownKeys(t *testing.T) {
	var at AssetType
	err := json.Unmarshal([]byte(`{"type":"x","connection":{"sshTunnel":true,"vpn":true}}`), &at)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"vpn"`)

	err = json.Unmarshal([]byte(`{"type":"x","auth":{"groups":[{"bindings":[{"in":"basic","value":"v","label":"x"}]}]}}`), &at)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"label"`)

	require.NoError(t, json.Unmarshal([]byte(`{"type":"x","connection":{"tls":true},`+
		`"auth":{"selector":"s","groups":[{"when":"a","bindings":[{"in":"header","name":"A","value":"v"}]}]}}`), &at))
	assert.Equal(t, &Connection{TLS: true}, at.Connection)
	assert.Equal(t, "A", at.Auth.Groups[0].Bindings[0].Name)
}
