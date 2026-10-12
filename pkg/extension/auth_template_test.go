package extension

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveGroup renders a single-group auth declaration the way a request to the
// asset's endpoint does, against an unsaved asset whose config is config.
func resolveGroup(t *testing.T, bindings []AuthBinding, config map[string]any) ([]renderedBinding, error) {
	t.Helper()
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	host := NewDefaultHostProvider(DefaultHostConfig{})
	asset := &AssetRef{Name: "unsaved", Type: "fixture", AdHoc: &AdHocAssetConfig{Credentials: raw}}
	auth, err := host.resolveAuth(context.Background(), asset, &HTTPAuth{Def: &AuthDef{Groups: []AuthGroup{{Bindings: bindings}}}})
	if err != nil {
		return nil, err
	}
	return auth.bindings, nil
}

func renderValue(t *testing.T, value string, config map[string]any) string {
	t.Helper()
	rendered, err := resolveGroup(t, []AuthBinding{{In: authInHeader, Name: "Authorization", Value: value}}, config)
	require.NoError(t, err, "template %q", value)
	require.Len(t, rendered, 1)
	return rendered[0].value
}

// The binding grammar extensions were published against — {{field}} and
// {{base64(part, ...)}}, a part being a config field or a double-quoted
// literal — keeps rendering to the same bytes now that the shared template
// language reads it.
func TestAuthTemplateRendersThePublishedGrammarUnchanged(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	config := map[string]any{"username": "elastic", "password": "s3cret", "key-id": "k1", "api_key": "v1"}

	for value, want := range map[string]string{
		"Bearer {{password}}":                             "Bearer s3cret",
		"{{username}}:{{password}}":                       "elastic:s3cret",
		"{{ password }}":                                  "s3cret",
		`ApiKey {{base64(username, ":", password)}}`:      "ApiKey " + b64("elastic:s3cret"),
		`{{base64(username, password)}}`:                  b64("elastics3cret"),
		`{{key-id}} {{base64( key-id , ":" , api_key )}}`: "k1 " + b64("k1:v1"),
		`{{base64("a\"b\\c\n", password)}}`:               b64("a\"b\\c\ns3cret"),
		`{{base64("}}", password)}} }} {{username}}`:      b64("}}s3cret") + " }} elastic",
		"no placeholders":                                 "no placeholders",
		"[{{unset}}] {{base64(unset, \":\", unset)}}":     "[] " + b64(":"),
		"{{username}}{{username}} {{base64(username)}}":   "elasticelastic " + b64("elastic"),
	} {
		assert.Equal(t, want, renderValue(t, value, config), "template %q", value)
	}
}

// The shared language's functions and now.* are available to a binding.
func TestAuthTemplateRendersSharedFunctionsAndNow(t *testing.T) {
	config := map[string]any{"username": "abc", "password": "s3cret"}

	// sha256("abc")
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		renderValue(t, "{{hex(sha256(username))}}", config))
	assert.Equal(t, "a%2Fb+abc", renderValue(t, `{{urlencode("a/b " + username)}}`, config))

	before := time.Now().Unix()
	got, err := strconv.ParseInt(renderValue(t, "{{now.unix}}", config), 10, 64)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, got, before)
	assert.LessOrEqual(t, got, time.Now().Unix())
}

// One request renders all of its group's bindings at one instant: a signature
// in one binding and the timestamp it signed in another must agree.
func TestAuthGroupRendersItsBindingsAtOneInstant(t *testing.T) {
	// Hashing the payload between the two timestamps takes milliseconds, so
	// bindings rendered one clock read each would disagree.
	rendered, err := resolveGroup(t, []AuthBinding{
		{In: authInHeader, Name: "X-Timestamp", Value: "{{now.unix_ms}}"},
		{In: authInHeader, Name: "X-Digest", Value: "{{hex(sha256(payload))}}"},
		{In: authInQuery, Name: "ts", Value: "{{now.unix_ms}}"},
	}, map[string]any{"payload": strings.Repeat("x", 32<<20)})
	require.NoError(t, err)
	require.Len(t, rendered, 3)
	assert.NotEmpty(t, rendered[0].value)
	assert.Equal(t, rendered[0].value, rendered[2].value)
}

// The asset form shows a group's fields from the manifest: every config field a
// binding references, wherever it sits in an expression, once.
func TestAuthGroupJSONListsFieldsReferencedInsideFunctions(t *testing.T) {
	raw, err := json.Marshal(AuthGroup{Bindings: []AuthBinding{
		{In: authInHeader, Name: "X-Sign", Value: "{{hex(hmac_sha256(secret, key-id + now.unix))}}"},
		{In: authInQuery, Name: "key", Value: "{{key-id}}{{uuid()}}"},
	}})
	require.NoError(t, err)

	var out struct {
		Fields []string `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, []string{"secret", "key-id"}, out.Fields)
}

// A declaration that never went through describe() validation reports its
// malformed template instead of injecting or listing nothing.
func TestAuthGroupWithAMalformedTemplateIsAnError(t *testing.T) {
	bindings := []AuthBinding{{In: authInHeader, Name: "A", Value: "{{md5(password)}}"}}

	_, err := resolveGroup(t, bindings, map[string]any{"password": "s3cret"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cret")

	_, err = json.Marshal(AuthGroup{Bindings: bindings})
	require.Error(t, err)
}
