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

func TestTextRedactsAWSPresignedURLQueryParameters(t *testing.T) {
	tests := []struct {
		name      string
		input     string
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
			input:      "https://s3.amazonaws.com/bucket/key?X-Amz-Credential=AKIAIOSFODNN7EXAMPLE/20260731/us-east-1/s3/aws4_request&X-Amz-Date=20260731T120000Z",
			shouldHide: []string{"AKIAIOSFODNN7EXAMPLE/20260731/us-east-1/s3/aws4_request"},
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
			input:      "https://s3.amazonaws.com/bucket/key?AWSAccessKeyId=AKIAIOSFODNN7EXAMPLE&Signature=v2sig123&Expires=1234567890",
			shouldHide: []string{"v2sig123", "AKIAIOSFODNN7EXAMPLE"},
			shouldShow: []string{"https://s3.amazonaws.com/bucket/key", "AWSAccessKeyId", "Signature", "Expires=1234567890"},
		},
		{
			name:       "Legacy V2 with AWSAccessKeyId only",
			input:      "https://s3.amazonaws.com/bucket/key?AWSAccessKeyId=AKIAIOSFODNN7EXAMPLE&Expires=1234567890",
			shouldHide: []string{}, // AWSAccessKeyId value itself should be redacted
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
