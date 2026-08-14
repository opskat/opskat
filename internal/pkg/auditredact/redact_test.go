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
	for _, raw := range []string{
		`{"password":"unterminated`,
		`{"host":"db.internal"} trailing-garbage`,
		`{"host":"db.internal"} {"token":"second-document-secret"}`,
	} {
		if got := JSON(raw); got != RedactedValue {
			t.Fatalf("invalid JSON %q = %q, want %q", raw, got, RedactedValue)
		}
	}
}

func TestTextRedactsCommonCredentialForms(t *testing.T) {
	raw := "--password one --api-key=two Authorization: Bearer three postgres://user:four@db CREATE USER x IDENTIFIED BY 'five' --json='{\"token\":\"seven\",\"bucket\":\"production-target\"}'\n-----BEGIN PRIVATE KEY-----\nsix\n-----END PRIVATE KEY-----"
	got := Text(raw)
	for _, secret := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		if strings.Contains(got, secret) {
			t.Fatalf("text leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "production-target") {
		t.Fatalf("text redaction removed a non-sensitive resource target: %s", got)
	}
}

func TestJSONRedactsSignatureChallengeAgentEndpointVariants(t *testing.T) {
	raw := `{
		"host":"db.internal",
		"signature":"sig-secret",
		"signed_value":"signed-secret",
		"signature_value":"sigv-secret",
		"challenge":"chal-secret",
		"challenge_response":"chalresp-secret",
		"challengeAnswer":"chala-secret",
		"agent_endpoint":"/run/agent.sock",
		"agent_socket":"/tmp/agent.sock",
		"agent_named_pipe":"namedpipe-secret",
		"ssh_agent_endpoint":"SSH_AUTH_SOCK=/tmp/agent.sock",
		"ssh_auth_sock":"/tmp/ssh-agent.sock",
		"endpoint":"https://oss.example.com",
		"endpoint_type":"public",
		"agent_source_id":42,
		"agent_key_fingerprint":"SHA256:abc123",
		"credential_id":7
	}`
	got := JSON(raw)
	for _, secret := range []string{
		"sig-secret", "signed-secret", "sigv-secret",
		"chal-secret", "chalresp-secret", "chala-secret",
		"/run/agent.sock", "/tmp/agent.sock", "namedpipe-secret", "/tmp/ssh-agent.sock",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("JSON leaked %q: %s", secret, got)
		}
	}
	for _, safe := range []string{
		"db.internal", "https://oss.example.com", "public", "SHA256:abc123",
		`"agent_source_id":42`, `"credential_id":7`,
	} {
		if !strings.Contains(got, safe) {
			t.Fatalf("JSON removed safe value %q: %s", safe, got)
		}
	}
}

func TestTextRedactsSignatureChallengeAgentEndpointForms(t *testing.T) {
	raw := "--agent-endpoint /Users/x/.ssh/agent.sock --challenge chal-1 signature=sig-1 challenge_response=resp-1 SSH_AUTH_SOCK=/Users/x/.ssh/sock\nagent_endpoint=/run/agent.sock"
	got := Text(raw)
	for _, secret := range []string{
		"/Users/x/.ssh/agent.sock", "chal-1", "sig-1", "resp-1",
		"/Users/x/.ssh/sock", "/run/agent.sock",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("text leaked %q: %s", secret, got)
		}
	}
}

func TestTextRedactsAuthorizationCookieAndCredentialKeyVariants(t *testing.T) {
	raw := "Authorization: Basic auth-secret\nProxy-Authorization: Bearer proxy-secret\nCookie: session=cookie-secret; csrf=csrf-secret\n--client-key client-key-secret --client-secret credential-client-42 --cookie cli-cookie-secret kubeconfig=kube-secret access_token=token-secret"
	got := Text(raw)
	for _, secret := range []string{"auth-secret", "proxy-secret", "cookie-secret", "csrf-secret", "client-key-secret", "credential-client-42", "cli-cookie-secret", "kube-secret", "token-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("text leaked %q: %s", secret, got)
		}
	}
}

func TestTextRedactsParameterizedAuthorizationHeaderCompletely(t *testing.T) {
	raw := `request failed
Authorization: Digest username="admin", realm="prod", nonce="nonce-secret", response="digest-secret"`
	got := Text(raw)
	for _, secret := range []string{"admin", "prod", "nonce-secret", "digest-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("parameterized authorization header leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "request failed") || !strings.Contains(got, RedactedValue) {
		t.Fatalf("authorization redaction lost safe context or marker: %s", got)
	}
}

func TestTextRedactsQuotedAndBracketedCredentialHeaders(t *testing.T) {
	raw := `provider failed: headers={"Authorization":"Bearer quoted-auth-secret","Cookie":"session=quoted-cookie-secret"} opaque={"Proxy-Authorization":"quoted-opaque-auth-secret"} escaped={"Authorization":"Digest username=\"escaped-user\", response=\"escaped-digest-secret\""} map[Proxy-Authorization:[Basic bracket-auth-secret] Cookie:[session=bracket-cookie-secret]]`
	got := Text(raw)
	for _, secret := range []string{"quoted-auth-secret", "quoted-cookie-secret", "quoted-opaque-auth-secret", "escaped-user", "escaped-digest-secret", "bracket-auth-secret", "bracket-cookie-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("text leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "provider failed") {
		t.Fatalf("text redaction removed safe error context: %s", got)
	}
}

func TestKubeconfigClientKeyDataIsSensitiveButPublicMetadataSurvives(t *testing.T) {
	raw := "apiVersion: v1\nclusters:\n- name: prod\nusers:\n- user:\n    client-key-data: kube-private-key-data\n    client-certificate-data: public-client-certificate"
	got := Text(raw)
	if strings.Contains(got, "kube-private-key-data") {
		t.Fatalf("text leaked kubeconfig client key data: %s", got)
	}
	for _, safe := range []string{"prod", "public-client-certificate"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("text redaction removed safe kubeconfig metadata %q: %s", safe, got)
		}
	}

	structured := JSON(`{"clientKeyData":"kube-json-private-key","clientCertificateData":"public-json-certificate"}`)
	if strings.Contains(structured, "kube-json-private-key") {
		t.Fatalf("JSON leaked kubeconfig client key data: %s", structured)
	}
	if !strings.Contains(structured, "public-json-certificate") {
		t.Fatalf("JSON removed public client certificate metadata: %s", structured)
	}
}

func TestJSONRedactsStructuredAgentEndpointAndPresignedFields(t *testing.T) {
	raw := `{
		"agent_source":{"endpoint_type":"unix_socket","endpoint":"/tmp/private-agent.sock"},
		"object_store":{"endpoint_type":"public","endpoint":"https://oss.example.com"},
		"X-Amz-Credential":"credential-secret",
		"AWSAccessKeyId":"access-key-secret",
		"X-Amz-Security-Token":"security-token-secret"
	}`
	got := JSON(raw)
	for _, secret := range []string{"/tmp/private-agent.sock", "credential-secret", "access-key-secret", "security-token-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("JSON leaked %q: %s", secret, got)
		}
	}
	for _, safe := range []string{`"endpoint_type":"unix_socket"`, `"endpoint_type":"public"`, "https://oss.example.com"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("JSON removed safe metadata %q: %s", safe, got)
		}
	}
}

func TestJSONDoesNotTreatGenericEnvironmentObjectsAsAgentSources(t *testing.T) {
	raw := `{"type":"environment","value":"production","path":"/srv/app"}`
	got := JSON(raw)
	for _, safe := range []string{`"value":"production"`, `"path":"/srv/app"`} {
		if !strings.Contains(got, safe) {
			t.Fatalf("generic environment object lost %s: %s", safe, got)
		}
	}
}

func TestResultPreservesBracketPrefixedNonJSONText(t *testing.T) {
	for _, raw := range []string{
		"[INFO] service started",
		"[ -f /tmp/ready ] && echo ready",
	} {
		if got := Result(raw); got != raw {
			t.Fatalf("Result(%q) = %q, want original text", raw, got)
		}
	}
}

func TestResultMalformedJSONFailsClosed(t *testing.T) {
	if got := Result(`{"opaque":"credential-material" trailing`); got != RedactedValue {
		t.Fatalf("malformed JSON result = %q, want %q", got, RedactedValue)
	}
}

func TestTextRedactsAWSPresignedURLQueryParameters(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		shouldHide []string // sensitive values that should be redacted
		shouldShow []string // non-sensitive parts that should be preserved
	}{
		{
			name:       "SigV4 with X-Amz-Signature",
			input:      "https://s3.amazonaws.com/bucket/key?X-Amz-Signature=abc123def456&X-Amz-Expires=3600",
			shouldHide: []string{"abc123def456"},
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "X-Amz-Signature", "X-Amz-Expires=3600"},
		},
		{
			name:       "SigV4 with X-Amz-Credential",
			input:      "https://s3.amazonaws.com/bucket/key?X-Amz-Credential=AKIAEXAMPLE/20260731/us-east-1/s3/aws4_request&X-Amz-Date=20260731T120000Z",
			shouldHide: []string{"AKIAEXAMPLE/20260731/us-east-1/s3/aws4_request"},
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "X-Amz-Credential", "X-Amz-Date=20260731T120000Z"},
		},
		{
			name:       "SigV4 with X-Amz-Security-Token",
			input:      "https://s3.amazonaws.com/bucket/key?X-Amz-Security-Token=token123xyz&X-Amz-SignedHeaders=host",
			shouldHide: []string{"token123xyz"},
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "X-Amz-Security-Token", "X-Amz-SignedHeaders=host"},
		},
		{
			name:       "SigV4 lowercase parameters",
			input:      "https://s3.amazonaws.com/bucket/key?x-amz-signature=lowercasesig&x-amz-credential=lowercasecred",
			shouldHide: []string{"lowercasesig", "lowercasecred"},
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "x-amz-signature", "x-amz-credential"},
		},
		{
			name:       "Legacy V2 with Signature",
			input:      "https://s3.amazonaws.com/bucket/key?AWSAccessKeyId=AKIAEXAMPLE&Signature=v2sig123&Expires=1234567890",
			shouldHide: []string{"v2sig123", "AKIAEXAMPLE"},
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "AWSAccessKeyId", "Signature", "Expires=1234567890"},
		},
		{
			name:       "Legacy V2 with AWSAccessKeyId only",
			input:      "https://s3.amazonaws.com/bucket/key?AWSAccessKeyId=AKIAEXAMPLE&Expires=1234567890",
			shouldHide: []string{"AKIAEXAMPLE"}, // AWSAccessKeyId value itself should be redacted
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "AWSAccessKeyId", "Expires=1234567890"},
		},
		{
			name:       "Multiple sensitive params in middle of URL",
			input:      "https://bucket.s3.us-west-2.amazonaws.com/path/to/file.txt?X-Amz-Signature=sig&X-Amz-Expires=600&X-Amz-Credential=cred&X-Amz-Date=20260731T000000Z",
			shouldHide: []string{"sig", "cred"},
			shouldShow: []string{"https://bucket.s3.us-west-2.amazonaws.com/path/to/file.txt", "X-Amz-Signature", "X-Amz-Credential", "X-Amz-Expires=600", "X-Amz-Date=20260731T000000Z"},
		},
		{
			name:       "URL with signature param last",
			input:      "https://s3.amazonaws.com/bucket/key?X-Amz-Expires=3600&X-Amz-Signature=abc123",
			shouldHide: []string{"abc123"},
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "X-Amz-Expires=3600", "X-Amz-Signature"},
		},
		{
			name:       "Complex URL with bucket in path",
			input:      "The presigned URL is: https://s3.amazonaws.com/prod-bucket/logs/2026/file.log?X-Amz-Signature=f5d8e9c&X-Amz-SignedHeaders=host",
			shouldHide: []string{"f5d8e9c"},
			shouldShow: []string{"prod-bucket", "logs/2026/file.log", "X-Amz-Signature", "X-Amz-SignedHeaders=host", "is:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.input)
			for _, secret := range tt.shouldHide {
				if strings.Contains(got, secret) {
					t.Errorf("leaked secret %q in: %s", secret, got)
				}
			}
			for _, preserve := range tt.shouldShow {
				if !strings.Contains(got, preserve) {
					t.Errorf("removed preserved content %q from: %s", preserve, got)
				}
			}
			// Verify redacted value is in there for sensitive params
			if len(tt.shouldHide) > 0 && !strings.Contains(got, RedactedValue) {
				t.Errorf("redacted value marker not found in: %s", got)
			}
		})
	}
}
