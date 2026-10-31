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

// 交给 shell 从标准输入读的其他写法同样展开：/dev/stdin、- 和带值的 shell 选项；eval 里
// 转义过的管道、换个路径写法执行写出来的文件也一样。
func TestRevealExpandsOtherWaysOfRunningTheDecodedScript(t *testing.T) {
	script := "rm -rf /data"
	payload := base64.StdEncoding.EncodeToString([]byte(script))
	for _, shell := range []string{"bash /dev/stdin", "bash -", "bash -o errexit", "bash -O extglob -s", "sh --rcfile /etc/profile"} {
		t.Run(shell, func(t *testing.T) {
			got, err := Reveal(pipeDecodeExec(script, shell))
			require.NoError(t, err)
			assert.Equal(t, script, got)
		})
	}
	for _, cmd := range []string{
		`eval echo ` + payload + ` \| base64 -d \| bash`,
		"echo " + payload + " | base64 -d > x.sh && bash ./x.sh",
		"echo " + payload + " | base64 -d > /tmp/x.sh && chmod +x /tmp/x.sh && /tmp/x.sh",
		"echo " + payload + " | base64 -d | sudo tee /tmp/x.sh >/dev/null && bash /tmp/x.sh",
	} {
		t.Run(cmd, func(t *testing.T) {
			got, err := Reveal(cmd)
			require.NoError(t, err)
			assert.Contains(t, got, script)
		})
	}
}

// 解码出来的内容去向认不出（交给不认识的程序、经替换或重定向送进 shell、写进文件再被执行
// 却解不开）时宁可审核失败，不能把只看得到 base64 的命令当成已经看清送审。
func TestRevealFailsClosedOnDecodedExecutionItCannotExpand(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("rm -rf /"))
	decode := "echo " + payload + " | base64 -d"
	cases := []string{
		decode + " | source /dev/stdin",
		decode + " | timeout 60 bash",
		decode + " | xargs -0 sh -c",
		decode + " | python3",
		decode + " | (bash)",
		decode + " | bash script.sh",
		"bash < <(" + decode + ")",
		"source <(" + decode + ")",
		"$(" + decode + ")",
		`timeout 9 bash -c "echo $(` + decode + `)"`,
		"base64 -d -o /tmp/x.sh <<< " + payload + " && bash /tmp/x.sh",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			_, err := Reveal(cmd)
			assert.ErrorIs(t, err, ErrUndecodable)
		})
	}
}

// 解码出来只是被查看、比对或写进文件（之后没有执行）的命令照常送审，不当成解码执行。
func TestRevealLeavesDecodedDataAlone(t *testing.T) {
	for _, cmd := range []string{
		"kubectl get secret db -o jsonpath='{.data.password}' | base64 -d",
		"kubectl get secret tls -o jsonpath='{.data.tls\\.crt}' | base64 -d | openssl x509 -noout -dates",
		"echo eyJhIjoxfQ== | base64 -d | jq .",
		"echo aGVsbG8= | base64 -d | sudo tee /etc/motd >/dev/null",
		`mysql -p"$(echo cGFzcw== | base64 -d)" -e 'select 1'`,
	} {
		t.Run(cmd, func(t *testing.T) {
			got, err := Reveal(cmd)
			require.NoError(t, err)
			assert.Equal(t, cmd, got)
		})
	}
}
