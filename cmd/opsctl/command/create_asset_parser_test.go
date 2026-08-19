package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// parseAssetCreateForTest 以注入的密码读取器驱动解析器：promptSecret 为 nil 表示
// 当前环境不可交互（解析器应给出 NEEDS TTY 结构化拒绝而不是读取任何输入）。
func parseAssetCreateForTest(t *testing.T, args []string, promptSecret func() (string, error), files map[string][]byte) (*assetCreateRequest, string, error) {
	t.Helper()
	var stderr bytes.Buffer
	deps := assetCreateParserDeps{
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
	}
	// promptSecret 为 nil 时 deps 也保持 nil：解析器据此判定环境不可交互。
	if promptSecret != nil {
		deps.promptSecret = func(prompt string) (string, error) {
			if _, err := io.WriteString(&stderr, prompt); err != nil {
				t.Fatalf("write prompt: %v", err)
			}
			return promptSecret()
		}
	}
	request, err := parseAssetCreate(context.Background(), args, deps)
	return request, stderr.String(), err
}

func TestParseAssetCreateGenericConfigAndExplicitLegacyPrecedence(t *testing.T) {
	request, stderr, err := parseAssetCreateForTest(t, []string{
		"--type", "database", "--name", "analytics",
		"--config", `{"driver":"postgresql","host":"from-config","port":15432,"username":"reader","read_only":true}`,
		"--host", "from-flag", "--read-only=false", "--ssh-asset", "jump",
	}, nil, nil)
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
		request, _, err := parseAssetCreateForTest(t, []string{"--name", "box", "--config-file", "asset.json"}, nil, map[string][]byte{
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
		{name: "removed credential name", args: []string{"--name", "x", "--credential-name", "implicit"}, want: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseAssetCreateForTest(t, tt.args, nil, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestParseAssetCreateRetainsKubeconfigFileAndAgentFlags(t *testing.T) {
	request, _, err := parseAssetCreateForTest(t, []string{
		"--type", "k8s", "--name", "prod", "--config", `{"namespace":"from-config"}`,
		"--kubeconfig-file", "kube.yaml",
	}, nil, map[string][]byte{"kube.yaml": []byte("apiVersion: v1\n")})
	require.NoError(t, err)
	assert.Equal(t, "apiVersion: v1\n", request.config["kubeconfig"])
	assert.Equal(t, "from-config", request.config["namespace"])

	request, _, err = parseAssetCreateForTest(t, []string{
		"--name", "agent-box", "--config", `{"host":"ssh.internal","username":"root","agent_source_id":1}`,
		"--agent-source-id", "7", "--agent-key-fingerprint", "SHA256:abc",
	}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(7), request.config["agent_source_id"])
	assert.Equal(t, "SHA256:abc", request.config["agent_key_fingerprint"])
}

func TestParseAssetCreateKubeconfigFileOverridesOnlyWhenExplicit(t *testing.T) {
	request, _, err := parseAssetCreateForTest(t, []string{
		"--type", "k8s", "--name", "prod", "--config", `{"kubeconfig":"from-config"}`,
		"--kubeconfig-file", "kube.yaml",
	}, nil, map[string][]byte{"kube.yaml": []byte("from-file")})
	require.NoError(t, err)
	assert.Equal(t, "from-file", request.config["kubeconfig"])
}

// splitPasswordPrompt 是「裸写 --password 进交互 / 带值 --password 用值」的唯一判据，
// 且必须发生在 flag 解析之前——Go 的 flag 包对可选值 flag 只认 = 形式，会把
// `--password s3cret` 的值解析成 "true" 并把 s3cret 丢进位置参数。
func TestSplitPasswordPromptClassifiesBareAndValuedForms(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantRest   []string
		wantPrompt bool
	}{
		{name: "bare alone", args: []string{"--password"}, wantRest: []string{}, wantPrompt: true},
		{name: "bare before another flag", args: []string{"--password", "--username", "root"},
			wantRest: []string{"--username", "root"}, wantPrompt: true},
		{name: "bare at the end", args: []string{"--username", "root", "--password"},
			wantRest: []string{"--username", "root"}, wantPrompt: true},
		{name: "single dash bare", args: []string{"-password"}, wantRest: []string{}, wantPrompt: true},
		{name: "space form keeps the value", args: []string{"--password", "s3cret", "--username", "root"},
			wantRest: []string{"--password", "s3cret", "--username", "root"}},
		{name: "equal form untouched", args: []string{"--password=s3cret"},
			wantRest: []string{"--password=s3cret"}},
		{name: "value that reads like a bool is still a value", args: []string{"--password", "true"},
			wantRest: []string{"--password", "true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rest, prompt, _ := splitPasswordPrompt(tt.args)
			assert.Equal(t, tt.wantRest, rest)
			assert.Equal(t, tt.wantPrompt, prompt)
		})
	}
}

// 空格形式是 v1.13.0 已发布的写法，交互形态不得把它顺带破坏。
func TestParseAssetCreatePasswordCarriesValueInBothForms(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "space form", args: []string{"--password", "s3cret"}, want: "s3cret"},
		{name: "equal form", args: []string{"--password=s3cret"}, want: "s3cret"},
		{name: "value that reads like a bool", args: []string{"--password", "true"}, want: "true"},
		{name: "equal form value with spaces", args: []string{"--password= s3 cret "}, want: " s3 cret "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--name", "cache", "--type", "redis",
				"--config", `{"host":"redis.internal","username":"default"}`}, tt.args...)
			request, stderr, err := parseAssetCreateForTest(t, args, nil, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, request.config["password"])
			assert.Contains(t, stderr, "shell history", "the argv plaintext path keeps warning")
			assert.NotContains(t, stderr, tt.want, "the warning must not echo the secret")
		})
	}
}

// 裸写 --password：密码从交互读取器进来，落到该类型自己的明文字段，且不回显。
func TestParseAssetCreateBarePasswordReadsFromPromptIntoTypeOwnedField(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantField string
	}{
		{name: "ssh uses password", args: []string{"--name", "web", "--type", "ssh", "--host", "10.0.0.1", "--username", "root", "--password"},
			wantField: "password"},
		{name: "oss uses secret_access_key", args: []string{"--name", "backups", "--type", "oss",
			"--config", `{"provider":"s3","endpoint":"s3.internal","access_key_id":"AKIA"}`, "--password"},
			wantField: "secret_access_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, stderr, err := parseAssetCreateForTest(t, tt.args,
				func() (string, error) { return "prompted-secret", nil }, nil)
			require.NoError(t, err)
			assert.Equal(t, "prompted-secret", request.config[tt.wantField])
			assert.NotContains(t, stderr, "prompted-secret", "the prompt must not echo the secret")
			assert.NotContains(t, stderr, "shell history",
				"the argv warning is for the valued form only; the prompt never touches argv")
		})
	}
}

// 空输入沿用「密码不得为空」，不落一个空密码的资产。
func TestParseAssetCreateBarePasswordRejectsEmptySecret(t *testing.T) {
	_, _, err := parseAssetCreateForTest(t, []string{"--name", "x", "--password"},
		func() (string, error) { return "", nil }, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// 不可交互时是结构化拒绝（退出码 3 + NEEDS TTY），不是普通参数错误，也绝不读输入。
func TestParseAssetCreateBarePasswordRefusesWithoutTerminal(t *testing.T) {
	_, stderr, err := parseAssetCreateForTest(t, []string{"--name", "x", "--password"}, nil, nil)
	require.Error(t, err)
	var refusal *structuredRefusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, needsTTYMarker, refusal.marker)
	assert.Contains(t, err.Error(), "--password=")
	assert.Empty(t, stderr, "the refusal is written by the command boundary, not the parser")
}

// 交互提示排在其余校验之后：参数写错时不该先让用户白输一遍密码。
func TestParseAssetCreateBarePasswordPromptsOnlyAfterOtherValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unregistered type", args: []string{"--name", "x", "--type", "no-such-type", "--password"}, want: "no-such-type"},
		{name: "missing name", args: []string{"--type", "ssh", "--password"}, want: "--name"},
		{name: "invalid config JSON", args: []string{"--name", "x", "--config", "{", "--password"}, want: "JSON"},
		{name: "unresolvable ssh asset", args: []string{"--name", "x", "--ssh-asset", "nope", "--password"}, want: "--ssh-asset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseAssetCreateForTest(t, tt.args, func() (string, error) {
				t.Fatal("the prompt must not run before the other validation passes")
				return "", nil
			}, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// 以 "-" 开头的值无法与「下一个 flag」区分，必须给定向指引而不是 Go flag 的通用报错。
func TestParseAssetCreatePasswordDashValueGuidesToEqualForm(t *testing.T) {
	_, _, err := parseAssetCreateForTest(t, []string{"--name", "x", "--password", "-abc123"},
		func() (string, error) { return "should-not-be-read", nil }, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--password=")
	assert.NotContains(t, err.Error(), "abc123", "the guidance must not echo the value")
}

// --password 同时带值又裸写会得到两个互相矛盾的来源，必须拒绝而不是静默择一。
func TestParseAssetCreatePasswordRejectsValueAndPromptTogether(t *testing.T) {
	_, _, err := parseAssetCreateForTest(t, []string{"--name", "x", "--password", "argv-secret", "--password"},
		func() (string, error) { return "prompted", nil }, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--password")
	assert.NotContains(t, err.Error(), "argv-secret")
}

// --password-stdin 是 v1.13.0 发布并在技能文档里推荐过的写法，退役后要把沿用者
// 指回 --password，而不是丢一句 Go flag 的 "flag provided but not defined"。
func TestParseAssetCreateRetiredPasswordStdinPointsAtPassword(t *testing.T) {
	for _, args := range [][]string{
		{"--name", "x", "--password-stdin"},
		{"--name", "x", "-password-stdin"},
		{"--name", "x", "--password-stdin=ignored"},
	} {
		_, _, err := parseAssetCreateForTest(t, args, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--password-stdin")
		assert.Contains(t, err.Error(), "--password")
		assert.NotContains(t, err.Error(), "not defined")
	}
}

func TestParseAssetCreateRejectsEquivalentSecretSourceConflicts(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		prompt bool
		wantOK bool
	}{
		{name: "reference and prompt", args: []string{"--name", "x", "--credential-id", "4", "--password"}, prompt: true},
		{name: "reference and argv", args: []string{"--name", "x", "--credential-id", "4", "--password", "argv"}},
		{name: "config password and argv", args: []string{"--name", "x", "--config", `{"password":"config-secret"}`, "--password", "argv-secret"}},
		{name: "config OSS secret and prompt", args: []string{"--name", "x", "--type", "oss", "--config", `{"secret_access_key":"config-secret"}`, "--password"}, prompt: true},
		{name: "config private key and reference", args: []string{"--name", "x", "--config", `{"private_key":"private-secret"}`, "--credential-id", "9"}},
		{name: "config passphrase and reference", args: []string{"--name", "x", "--config", `{"passphrase":"passphrase-secret"}`, "--credential-id", "9"}},
		{name: "config reference and argv", args: []string{"--name", "x", "--config", `{"credential_id":3}`, "--password", "argv-secret"}},
		{name: "SSH private key with passphrase is one source", args: []string{"--name", "x", "--config", `{"host":"ssh.example.com","username":"root","private_key":"private-secret","passphrase":"passphrase-secret"}`}, wantOK: true},
		{name: "OSS config secret and config reference", args: []string{"--name", "x", "--type", "oss", "--config", `{"secret_access_key":"config-secret","credential_id":3}`}},
		{name: "SSH config private key and password", args: []string{"--name", "x", "--config", `{"private_key":"private-secret","password":"config-secret"}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var promptSecret func() (string, error)
			if tt.prompt {
				promptSecret = func() (string, error) { return "prompted-secret", nil }
			}
			_, stderr, err := parseAssetCreateForTest(t, tt.args, promptSecret, nil)
			if tt.wantOK {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "mutually exclusive")
			for _, secret := range []string{"argv-secret", "config-secret", "prompted-secret", "private-secret", "passphrase-secret"} {
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
	}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "object-secret", request.config["secret_access_key"])
	assert.NotContains(t, request.config, "password")
}

func TestParseAssetCreatePlaintextWarningWriteFailuresAreReturned(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		files map[string][]byte
		want  string
	}{
		{
			name: "argv warning",
			args: []string{"--name", "x", "--password", "argv-top-secret"},
			want: "write plaintext argv warning",
		},
		{
			name:  "config file warning",
			args:  []string{"--name", "x", "--config-file", "asset.json"},
			files: map[string][]byte{"asset.json": []byte(`{"password":"file-top-secret"}`)},
			want:  "write plaintext config file warning",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseAssetCreate(context.Background(), tt.args, assetCreateParserDeps{
				stderr: failingWriter{err: errors.New("writer closed")},
				readFile: func(path string) ([]byte, error) {
					data, ok := tt.files[path]
					if !ok {
						return nil, errors.New("read failed")
					}
					return data, nil
				},
				resolveAssetID: func(context.Context, string) (int64, error) {
					return 0, errors.New("unexpected resolve")
				},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Contains(t, err.Error(), "writer closed")
		})
	}
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
			contains: []string{"shell history", "process listings", "CI", "bare --password", "--credential-id"}, secrets: []string{"argv-top-secret"},
		},
		{
			name: "inline config", args: []string{"--name", "x", "--config", `{"password":"inline-top-secret"}`},
			contains: []string{"shell history", "process listings", "CI", "bare --password", "--credential-id"}, secrets: []string{"inline-top-secret"},
		},
		{
			name: "plaintext config file", args: []string{"--name", "x", "--config-file", "asset.json"},
			contains: []string{"restrictive permissions", "commit", "remove"}, secrets: []string{"file-top-secret"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string][]byte{"asset.json": []byte(`{"password":"file-top-secret"}`)}
			_, stderr, err := parseAssetCreateForTest(t, tt.args, nil, files)
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
