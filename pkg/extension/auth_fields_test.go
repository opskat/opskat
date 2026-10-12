package extension

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The asset form shows only the selected auth group's fields, so the manifest it
// receives has to say which fields each group uses.
func TestAuthGroupJSONListsTheFieldsItsBindingsUse(t *testing.T) {
	auth := AuthDef{
		Selector: "authType",
		Groups: []AuthGroup{
			{When: "basic", Bindings: []AuthBinding{{In: "basic", Value: "{{username}}:{{password}}"}}},
			{When: "apiKey", Bindings: []AuthBinding{{In: "header", Name: "Authorization", Value: `ApiKey {{base64(keyId, ":", apiKey)}}`}}},
		},
	}

	raw, err := json.Marshal(auth)
	require.NoError(t, err)

	var out struct {
		Selector string `json:"selector"`
		Groups   []struct {
			When   string   `json:"when"`
			Fields []string `json:"fields"`
		} `json:"groups"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "authType", out.Selector)
	require.Len(t, out.Groups, 2)
	assert.Equal(t, []string{"username", "password"}, out.Groups[0].Fields)
	assert.Equal(t, []string{"keyId", "apiKey"}, out.Groups[1].Fields)
}

// fields is derived by the host; a guest cannot declare it in describe().
func TestAuthDeclarationCannotCarryFields(t *testing.T) {
	var auth AuthDef
	err := json.Unmarshal([]byte(`{"groups":[{"bindings":[{"in":"basic","value":"{{u}}:{{p}}"}],"fields":["u"]}]}`), &auth)
	require.Error(t, err)
}
