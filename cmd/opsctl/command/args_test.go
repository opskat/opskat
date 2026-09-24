package command

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

func TestHoistGlobalFlags(t *testing.T) {
	cases := []struct {
		name        string
		verb        string
		args        []string
		wantGlobals []string
		wantRest    []string
	}{
		{"exec 子命令后的 --mfa-code 生效", "exec",
			[]string{"86", "--mfa-code", "224716", "--", "whoami"},
			[]string{"--mfa-code", "224716"}, []string{"86", "--", "whoami"}},
		{"exec 无 -- 时 = 形式同样生效", "exec",
			[]string{"86", "--mfa-code=224716", "uptime"},
			[]string{"--mfa-code=224716"}, []string{"86", "uptime"}},
		{"exec 与 --type 混写", "exec",
			[]string{"86", "--type", "ssh", "--mfa-code", "1", "--", "ls"},
			[]string{"--mfa-code", "1"}, []string{"86", "--type", "ssh", "--", "ls"}},
		{"exec 远端命令里的同名 flag 不被抢走（无 --）", "exec",
			[]string{"86", "mysqld", "--data-dir", "/x"},
			nil, []string{"86", "mysqld", "--data-dir", "/x"}},
		{"-- 之后一律是负载", "exec",
			[]string{"86", "--", "tool", "--mfa-code", "1"},
			nil, []string{"86", "--", "tool", "--mfa-code", "1"}},
		{"delete asset 后的 --data-dir 生效，而不是静默删默认库", "delete",
			[]string{"asset", "web", "--data-dir", "/tmp/x"},
			[]string{"--data-dir", "/tmp/x"}, []string{"asset", "web"}},
		{"cp 路径之间的 --mfa-code", "cp",
			[]string{"86:/etc/hosts", "--mfa-code", "1", "./hosts"},
			[]string{"--mfa-code", "1"}, []string{"86:/etc/hosts", "./hosts"}},
		{"policy 的 -- 之后是模式", "policy",
			[]string{"allow", "web", "--", "--master-key"},
			nil, []string{"allow", "web", "--", "--master-key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			globals, rest, err := hoistGlobalFlags(tc.verb, tc.args)
			require.NoError(t, err)
			require.Equal(t, tc.wantGlobals, globals)
			require.Equal(t, tc.wantRest, rest)
		})
	}

	t.Run("缺值报错而不是吞掉", func(t *testing.T) {
		_, _, err := hoistGlobalFlags("exec", []string{"86", "--mfa-code"})
		require.ErrorContains(t, err, "--mfa-code")
	})
}

func TestParseExecArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantType string
		wantCmd  string
	}{
		{"-- 分隔", []string{"--", "echo", "hi"}, "", "echo hi"},
		{"无 --", []string{"uptime", "-a"}, "", "uptime -a"},
		{"--type 断言", []string{"--type", "ssh", "--", "ls"}, "ssh", "ls"},
		{"--type= 形式", []string{"--type=redis", "GET", "k"}, "redis", "GET k"},
		{"远端命令里的 --type 属于命令（无 --）", []string{"find", "/", "--type", "f"}, "", "find / --type f"},
		{"-- 之后的 -- 属于命令", []string{"--", "echo", "--", "x"}, "", "echo -- x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared, cmd, err := parseExecArgs(tc.args)
			require.NoError(t, err)
			require.Equal(t, tc.wantType, declared)
			require.Equal(t, tc.wantCmd, cmd)
		})
	}

	t.Run("命令前的未知 flag 报错，而不是静默丢弃或发往远端", func(t *testing.T) {
		_, _, err := parseExecArgs([]string{"--bogus", "1", "--", "ls"})
		require.ErrorContains(t, err, "--bogus")
		_, _, err = parseExecArgs([]string{"--bogus", "ls"})
		require.ErrorContains(t, err, "--bogus")
	})
	t.Run("--type 缺值报错", func(t *testing.T) {
		_, _, err := parseExecArgs([]string{"--type"})
		require.ErrorContains(t, err, "--type")
	})
	t.Run("本地 shell 吃掉的词边界在下游重新切分后仍在", func(t *testing.T) {
		// 下游（扩展 flag DSL、k8s/etcd/kafka canonicalizer、ssh 远端 shell）都会用真正
		// 的 shell 解析器重新切分这个串；裸空格拼接会让带空格的值变成两个词。
		for _, argv := range [][]string{
			{"grep", "foo bar", "file"},
			{"note_put", "--content=restart via systemctl"},
		} {
			_, cmd, err := parseExecArgs(append([]string{"--"}, argv...))
			require.NoError(t, err)
			words, err := cmdline.Words(cmd)
			require.NoError(t, err)
			require.Equal(t, argv, words)
		}
	})
	t.Run("不含空白的词原样保留，glob 仍交给远端 shell", func(t *testing.T) {
		_, cmd, err := parseExecArgs([]string{"--", "ls", "*.log"})
		require.NoError(t, err)
		require.Equal(t, "ls *.log", cmd)
		_, cmd, err = parseExecArgs([]string{"--", "grep", "foo bar", "*.log"})
		require.NoError(t, err)
		require.Equal(t, "grep 'foo bar' *.log", cmd)
	})
	t.Run("单个词就是命令串本身，不加引号", func(t *testing.T) {
		// `opsctl exec prod-db -- "SELECT * FROM t"` 是所有 DSL 的文档用法。
		_, cmd, err := parseExecArgs([]string{"--", "SELECT * FROM users"})
		require.NoError(t, err)
		require.Equal(t, "SELECT * FROM users", cmd)
		_, cmd, err = parseExecArgs([]string{"ls | wc -l"})
		require.NoError(t, err)
		require.Equal(t, "ls | wc -l", cmd)
	})
	t.Run("没有命令时报错", func(t *testing.T) {
		_, _, err := parseExecArgs([]string{"--"})
		require.Error(t, err)
		_, _, err = parseExecArgs(nil)
		require.Error(t, err)
	})
}
