package auditredact

import (
	"strings"
	"testing"
)

func TestJSONRedactsCredentialKeyVariantsWithoutDroppingSafeFields(t *testing.T) {
	raw := `{"host":"db.internal","password":"a","webdav_password":"b","privateKey":"c","passphrase":"d","api_key":"e","githubToken":"f","client_secret":"g","secretAccessKey":"h","authorization":"i","cookie":"j","kubeconfig":"k","tls_client_key":"l","credential_id":42,"tokenUsage":7}`
	got := JSON(raw)
	for _, secret := range []string{`"a"`, `"b"`, `"c"`, `"d"`, `"e"`, `"f"`, `"g"`, `"h"`, `"i"`, `"j"`, `"k"`, `"l"`} {
		if strings.Contains(got, secret) {
			t.Fatalf("JSON leaked %s: %s", secret, got)
		}
	}
	for _, safe := range []string{"db.internal", `"credential_id":42`, `"tokenUsage":7`} {
		if !strings.Contains(got, safe) {
			t.Fatalf("JSON removed safe value %s: %s", safe, got)
		}
	}
}

func TestJSONInvalidPayloadFailsClosed(t *testing.T) {
	if got := JSON(`{"password":"unterminated`); got != RedactedValue {
		t.Fatalf("invalid JSON = %q, want %q", got, RedactedValue)
	}
}

func TestTextRedactsCommonCredentialForms(t *testing.T) {
	raw := "--password one --api-key=two Authorization: Bearer three postgres://user:four@db CREATE USER x IDENTIFIED BY 'five'\n-----BEGIN PRIVATE KEY-----\nsix\n-----END PRIVATE KEY-----"
	got := Text(raw)
	for _, secret := range []string{"one", "two", "three", "four", "five", "six"} {
		if strings.Contains(got, secret) {
			t.Fatalf("text leaked %q: %s", secret, got)
		}
	}
}
