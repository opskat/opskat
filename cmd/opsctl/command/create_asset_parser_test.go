package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseAssetCreateForTest(t *testing.T, args []string, stdin string, files map[string][]byte) (*assetCreateRequest, string, error) {
	t.Helper()
	var stderr bytes.Buffer
	request, err := parseAssetCreate(context.Background(), args, assetCreateParserDeps{
		stdin:  strings.NewReader(stdin),
		stderr: &stderr,
		readFile: func(path string) ([]byte, error) {
			data, ok := files[path]
			if !ok {
				return nil, errors.New("read failed")
			}
			return data, nil
		},
		resolveAssetID: func(_ context.Context, ref string) (int64, error) {
			if ref != "jump" {
				return 0, errors.New("asset not found")
			}
			return 19, nil
		},
	})
	return request, stderr.String(), err
}

func TestParseAssetCreateGenericConfigAndExplicitLegacyPrecedence(t *testing.T) {
	request, stderr, err := parseAssetCreateForTest(t, []string{
		"--type", "database", "--name", "analytics",
		"--config", `{"driver":"postgresql","host":"from-config","port":15432,"username":"reader","read_only":true}`,
		"--host", "from-flag", "--read-only=false", "--ssh-asset", "jump",
	}, "", nil)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, "analytics", request.asset.Name)
	assert.Equal(t, "database", request.asset.Type)
	assert.Equal(t, "from-flag", request.config["host"])
	assert.Equal(t, int64(15432), request.config["port"], "an absent --port default must not overwrite generic config")
	assert.Equal(t, "reader", request.config["username"], "an absent --username default must not overwrite generic config")
	assert.Equal(t, false, request.config["read_only"], "an explicitly visited false bool must override generic config")
	assert.Equal(t, int64(19), request.config["ssh_asset_id"])
	assert.NotContains(t, request.config, "auth_type", "the legacy auth default must not leak into non-SSH generic config")
}

func TestParseAssetCreateGenericConfigSourcesAndErrors(t *testing.T) {
	t.Run("config file object", func(t *testing.T) {
		request, _, err := parseAssetCreateForTest(t, []string{"--name", "box", "--config-file", "asset.json"}, "", map[string][]byte{
			"asset.json": []byte(`{"host":"ssh.internal","username":"root"}`),
		})
		require.NoError(t, err)
		assert.Equal(t, "ssh.internal", request.config["host"])
	})

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "mutually exclusive sources", args: []string{"--name", "x", "--config", `{}`, "--config-file", "x.json"}, want: "mutually exclusive"},
		{name: "invalid JSON", args: []string{"--name", "x", "--config", `{`}, want: "invalid --config JSON"},
		{name: "non object", args: []string{"--name", "x", "--config", `[]`}, want: "JSON object"},
		{name: "null", args: []string{"--name", "x", "--config", `null`}, want: "JSON object"},
		{name: "unreadable", args: []string{"--name", "x", "--config-file", "missing.json"}, want: "read --config-file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseAssetCreateForTest(t, tt.args, "", nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestParseAssetCreateRetainsKubeconfigFileAndAgentFlags(t *testing.T) {
	request, _, err := parseAssetCreateForTest(t, []string{
		"--type", "k8s", "--name", "prod", "--config", `{"namespace":"from-config"}`,
		"--kubeconfig-file", "kube.yaml",
	}, "", map[string][]byte{"kube.yaml": []byte("apiVersion: v1\n")})
	require.NoError(t, err)
	assert.Equal(t, "apiVersion: v1\n", request.config["kubeconfig"])
	assert.Equal(t, "from-config", request.config["namespace"])

	request, _, err = parseAssetCreateForTest(t, []string{
		"--name", "agent-box", "--config", `{"host":"ssh.internal","username":"root","agent_source_id":1}`,
		"--agent-source-id", "7", "--agent-key-fingerprint", "SHA256:abc",
	}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(7), request.config["agent_source_id"])
	assert.Equal(t, "SHA256:abc", request.config["agent_key_fingerprint"])
}

func TestParseAssetCreateKubeconfigFileOverridesOnlyWhenExplicit(t *testing.T) {
	request, _, err := parseAssetCreateForTest(t, []string{
		"--type", "k8s", "--name", "prod", "--config", `{"kubeconfig":"from-config"}`,
		"--kubeconfig-file", "kube.yaml",
	}, "", map[string][]byte{"kube.yaml": []byte("from-file")})
	require.NoError(t, err)
	assert.Equal(t, "from-file", request.config["kubeconfig"])
}

func TestParseAssetCreatePasswordStdinTrimsExactlyOneTerminalLineEnding(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		want  string
	}{
		{name: "LF", stdin: "secret\n", want: "secret"},
		{name: "CRLF", stdin: "secret\r\n", want: "secret"},
		{name: "one only", stdin: "secret\n\n", want: "secret\n"},
		{name: "preserve CR", stdin: "secret\r", want: "secret\r"},
		{name: "preserve spaces", stdin: " secret \n", want: " secret "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, stderr, err := parseAssetCreateForTest(t, []string{
				"--name", "cache", "--type", "redis", "--config", `{"host":"redis.internal","username":"default"}`, "--password-stdin",
			}, tt.stdin, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, request.config["password"])
			assert.Empty(t, stderr, "stdin is the recommended non-echo path")
		})
	}

	for _, input := range []string{"", "\n", "\r\n"} {
		_, _, err := parseAssetCreateForTest(t, []string{"--name", "x", "--password-stdin"}, input, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	}
	_, _, err := parseAssetCreateForTest(t, []string{"--name", "x", "--password", ""}, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestParseAssetCreateRejectsEquivalentSecretSourceConflicts(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "argv and stdin", args: []string{"--name", "x", "--password", "argv", "--password-stdin"}, stdin: "stdin"},
		{name: "reference and argv", args: []string{"--name", "x", "--credential-id", "4", "--password", "argv"}},
		{name: "config password and argv", args: []string{"--name", "x", "--config", `{"password":"config-secret"}`, "--password", "argv-secret"}},
		{name: "config OSS secret and stdin", args: []string{"--name", "x", "--type", "oss", "--config", `{"secret_access_key":"config-secret"}`, "--password-stdin"}, stdin: "stdin-secret"},
		{name: "config private key and reference", args: []string{"--name", "x", "--config", `{"private_key":"private-secret"}`, "--credential-id", "9"}},
		{name: "config reference and argv", args: []string{"--name", "x", "--config", `{"credential_id":3}`, "--password", "argv-secret"}},
		{name: "OSS config secret and config reference", args: []string{"--name", "x", "--type", "oss", "--config", `{"secret_access_key":"config-secret","credential_id":3}`}},
		{name: "SSH config private key and password", args: []string{"--name", "x", "--config", `{"private_key":"private-secret","password":"config-secret"}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := parseAssetCreateForTest(t, tt.args, tt.stdin, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "mutually exclusive")
			for _, secret := range []string{"argv-secret", "config-secret", "stdin-secret", "private-secret"} {
				assert.NotContains(t, err.Error(), secret)
				assert.NotContains(t, stderr, secret)
			}
		})
	}
}

func TestParseAssetCreatePasswordFlagUsesTypeOwnedPlaintextField(t *testing.T) {
	request, _, err := parseAssetCreateForTest(t, []string{
		"--name", "backups", "--type", "oss", "--config", `{"provider":"s3","endpoint":"s3.internal","access_key_id":"AKIA"}`,
		"--password", "object-secret",
	}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "object-secret", request.config["secret_access_key"])
	assert.NotContains(t, request.config, "password")
}

func TestParseAssetCreateCredentialNameIsForwardedOnlyWhenVisited(t *testing.T) {
	request, _, err := parseAssetCreateForTest(t, []string{
		"--name", "cache", "--type", "redis", "--config", `{"host":"redis.internal","username":"default"}`,
		"--password", "secret", "--credential-name", "shared-cache-password",
	}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "shared-cache-password", request.credentialName)
}

func TestParseAssetCreatePlaintextWarningsNeverEchoValues(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains []string
		secrets  []string
	}{
		{
			name: "password argv", args: []string{"--name", "x", "--password", "argv-top-secret"},
			contains: []string{"shell history", "process listings", "CI", "--password-stdin", "--credential-id"}, secrets: []string{"argv-top-secret"},
		},
		{
			name: "inline config", args: []string{"--name", "x", "--config", `{"password":"inline-top-secret"}`},
			contains: []string{"shell history", "process listings", "CI", "--password-stdin", "--credential-id"}, secrets: []string{"inline-top-secret"},
		},
		{
			name: "plaintext config file", args: []string{"--name", "x", "--config-file", "asset.json"},
			contains: []string{"restrictive permissions", "commit", "remove"}, secrets: []string{"file-top-secret"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string][]byte{"asset.json": []byte(`{"password":"file-top-secret"}`)}
			_, stderr, err := parseAssetCreateForTest(t, tt.args, "", files)
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, stderr, want)
			}
			for _, secret := range tt.secrets {
				assert.NotContains(t, stderr, secret)
			}
		})
	}
}
