package opskat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// credentialHandleKey is the key of the opaque handle object the host serves in
// place of a password field's plaintext when the extension does not declare
// capabilities.credentials="read" (produced by the desktop's asset config reader,
// internal/app/extension/host.go).
const credentialHandleKey = "__credential_handle" //nolint:gosec // G101: a JSON key name, not a credential

// ErrCredentialWithheld is what Credential.Plaintext reports when the host
// served the field as an opaque handle: the extension does not declare
// capabilities.credentials="read".
var ErrCredentialWithheld = errors.New("credential withheld: the extension does not declare capabilities.credentials \"read\"")

// Credential is the type of a secret field in an asset config struct. AssetType
// reports it as format:"password" — a secret the host encrypts — with no tag
// needed, and it decodes whichever form the host serves the secret in:
//
//   - with capabilities.credentials="read", the plaintext, returned by Plaintext;
//   - without it, an opaque handle that carries no plaintext: IsSet reports
//     whether the asset has a value, Plaintext returns ErrCredentialWithheld, and
//     the host still injects the secret into requests to the asset's endpoint
//     through the asset type's declared Auth.
//
// A plain string field tagged format:"password" only decodes the plaintext form,
// so it suits an extension that declares credentials:read and nothing else.
type Credential struct {
	plaintext string
	handle    string
	// present is set when the host sent a plaintext string (possibly empty),
	// so re-encoding reproduces it rather than null.
	present bool
}

var credentialType = reflect.TypeFor[Credential]()

// Plaintext returns the secret's plaintext, or "" when the field is unset. It
// fails with ErrCredentialWithheld when the host served an opaque handle.
func (c Credential) Plaintext() (string, error) {
	if c.handle != "" {
		return "", ErrCredentialWithheld
	}
	return c.plaintext, nil
}

// IsSet reports whether the asset has a value for the field, whether or not the
// extension may read it.
func (c Credential) IsSet() bool {
	return c.handle != "" || c.plaintext != ""
}

// UnmarshalJSON accepts the plaintext string, the host's handle object, or null
// (unset). Anything else is an error rather than an empty secret.
func (c *Credential) UnmarshalJSON(data []byte) error {
	*c = Credential{}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	var plaintext string
	if err := json.Unmarshal(data, &plaintext); err == nil {
		*c = Credential{plaintext: plaintext, present: true}
		return nil
	}
	var handle map[string]string
	if err := json.Unmarshal(data, &handle); err != nil || len(handle) != 1 || handle[credentialHandleKey] == "" {
		// The value is not echoed: whatever it is, it sits where a secret goes.
		return fmt.Errorf("credential: want a string or a {%q: ...} handle object", credentialHandleKey)
	}
	c.handle = handle[credentialHandleKey]
	return nil
}

// MarshalJSON encodes the credential in the form it was decoded from.
func (c Credential) MarshalJSON() ([]byte, error) {
	switch {
	case c.handle != "":
		return json.Marshal(map[string]string{credentialHandleKey: c.handle})
	case c.present:
		return json.Marshal(c.plaintext)
	default:
		return []byte("null"), nil
	}
}
