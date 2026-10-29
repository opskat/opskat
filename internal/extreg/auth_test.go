package extreg

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
)

// A binding's template names the config fields it injects.
func TestAuthTemplateFields(t *testing.T) {
	fields, err := authTemplateFields(`ApiKey {{base64(username, ":", password)}} {{ token }}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"username", "password", "token"}, fields)

	_, err = authTemplateFields(`{{password`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed")
}

// Inject is the scope's credential injection for an asset whose type declares
// auth groups: the selector field picks the group, its bindings render from the
// asset's credentials. The declaration is the fixture extension's (basic / bearer /
// signed by authType).
func TestAuthInject(t *testing.T) {
	auth := &AuthDef{Selector: "authType", Groups: []AuthGroup{
		{When: "basic", Bindings: []AuthBinding{{In: "basic", Value: "{{username}}:{{password}}"}}},
		{When: "bearer", Bindings: []AuthBinding{{In: "header", Name: "Authorization", Value: "Bearer {{password}}"}}},
		{When: "signed", Bindings: []AuthBinding{{In: "query", Name: "token", Value: `{{base64(username, ":", password)}}`}}},
	}}
	creds := map[string]string{"username": "elastic", "password": "s3cret"}
	var asked [][]string
	values := func(_ context.Context, fields []string) (map[string]string, error) {
		asked = append(asked, fields)
		out := map[string]string{}
		for _, f := range fields {
			if v, ok := creds[f]; ok {
				out[f] = v
			}
		}
		return out, nil
	}
	inject := auth.Inject(values)

	for _, tc := range []struct {
		authType string
		want     []extension.AuthValue
	}{
		{"basic", []extension.AuthValue{{In: extension.AuthInBasic, Value: "elastic:s3cret"}}},
		{"bearer", []extension.AuthValue{{In: extension.AuthInHeader, Name: "Authorization", Value: "Bearer s3cret"}}},
		{"signed", []extension.AuthValue{{In: extension.AuthInQuery, Name: "token", Value: base64.StdEncoding.EncodeToString([]byte("elastic:s3cret"))}}},
		{"none", nil},
	} {
		t.Run(tc.authType, func(t *testing.T) {
			creds["authType"] = tc.authType
			asked = nil
			got, err := inject(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			if tc.want != nil {
				assert.Equal(t, []string{"authType"}, asked[0], "the selector is read first")
				assert.NotContains(t, asked[1], "authType", "then only the active group's fields")
			}
		})
	}

	t.Run("credentials that cannot be read fail the request rather than send it without them", func(t *testing.T) {
		broken := auth.Inject(func(context.Context, []string) (map[string]string, error) {
			return nil, errors.New("decrypt password field")
		})
		_, err := broken(context.Background())
		assert.ErrorContains(t, err, "decrypt password field")
	})

	t.Run("without a selector the one group is always active", func(t *testing.T) {
		single := &AuthDef{Groups: []AuthGroup{{Bindings: []AuthBinding{{In: "header", Name: "X-Key", Value: "{{password}}"}}}}}
		got, err := single.Inject(values)(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []extension.AuthValue{{In: extension.AuthInHeader, Name: "X-Key", Value: "s3cret"}}, got)
	})
}
