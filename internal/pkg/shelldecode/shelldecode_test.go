package shelldecode

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipeDecodeExec 是「把脚本 base64 之后交给 shell 执行」的写法。
func pipeDecodeExec(script, shell string) string {
	return "echo '" + base64.StdEncoding.EncodeToString([]byte(script)) + "' | base64 -d | " + shell
}

func TestRevealReplacesBase64WithTheDecodedScript(t *testing.T) {
	script := "cd /root/docker/vaalhub-web\ndocker compose up -d"
	got, err := Reveal(pipeDecodeExec(script, "bash"))
	require.NoError(t, err)
	assert.Equal(t, script, got)
}

func TestRevealDecodesCommonShellWrappers(t *testing.T) {
	script := "docker compose up -d"
	payload := base64.StdEncoding.EncodeToString([]byte(script))
	cases := []string{
		pipeDecodeExec(script, "/bin/bash"),
		"printf '%s' '" + payload + "' | base64 --decode | sh",
		"echo '" + payload + "' | base64 -d | sudo bash",
		`bash -c "echo '` + payload + `' | base64 -d | bash"`,
		"echo '" + payload + "' | base64 -d > /tmp/x.sh && bash /tmp/x.sh",
		"bash -c \"$(echo '" + payload + "' | base64 -d)\"",
		"eval \"$(echo '" + payload + "' | base64 -d)\"",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := Reveal(cmd)
			require.NoError(t, err)
			assert.Contains(t, got, script)
			assert.NotContains(t, got, payload)
		})
	}
}

func TestRevealKeepsSiblingCommands(t *testing.T) {
	script := "docker inspect vaalhub-web"
	got, err := Reveal(pipeDecodeExec(script, "bash") + " && rm -rf /var/lib/gone")
	require.NoError(t, err)
	assert.Contains(t, got, script)
	assert.Contains(t, got, "rm -rf /var/lib/gone")
	assert.NotContains(t, got, "base64")
}

func TestRevealRefusesWhatItCannotRead(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("docker compose up -d"))
	cases := []string{
		"echo '!!!' | base64 -d | bash",
		"base64 -d /tmp/payload | bash",
		"echo \"$payload\" | base64 -d | bash",
		// 藏在条件里的解码执行同样读不了
		"if true; then " + pipeDecodeExec("docker compose up -d", "bash") + "; fi",
		"if true; then echo '" + payload + "' | base64 -d > /tmp/x.sh && bash /tmp/x.sh; fi",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			_, err := Reveal(cmd)
			assert.ErrorIs(t, err, ErrUndecodable)
		})
	}
}

func TestRevealLeavesWritesWithoutExecutionAlone(t *testing.T) {
	cmd := "echo '" + base64.StdEncoding.EncodeToString([]byte("/root/docker/nginx/logs/*.log {\n}")) + "' | base64 -d > /etc/logrotate.d/nginx-docker"
	got, err := Reveal(cmd)
	require.NoError(t, err)
	assert.Equal(t, cmd, got)
}

func TestRevealLeavesOrdinaryEvalAlone(t *testing.T) {
	got, err := Reveal(`eval "$cmd"`)
	require.NoError(t, err)
	assert.Equal(t, `eval "$cmd"`, got)
}

func TestRevealRejectsUnparseableCommands(t *testing.T) {
	_, err := Reveal("echo 'unterminated")
	assert.ErrorIs(t, err, ErrUnparseable)
}
