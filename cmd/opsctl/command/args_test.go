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
		name      string
		args      []string
		wantType  string
		wantScope string
		wantCmd   string
	}{
		{"-- 分隔", []string{"--", "echo", "hi"}, "", "", "echo hi"},
		{"无 --", []string{"uptime", "-a"}, "", "", "uptime -a"},
		{"--type 断言", []string{"--type", "ssh", "--", "ls"}, "ssh", "", "ls"},
		{"--type= 形式", []string{"--type=redis", "GET", "k"}, "redis", "", "GET k"},
		{"远端命令里的 --type 属于命令（无 --）", []string{"find", "/", "--type", "f"}, "", "", "find / --type f"},
		{"-- 之后的 -- 属于命令", []string{"--", "echo", "--", "x"}, "", "", "echo -- x"},
		{"--scope 断言", []string{"--scope", "10.0.0.1:6379", "--", "DBSIZE"}, "", "10.0.0.1:6379", "DBSIZE"},
		{"--scope= 形式", []string{"--scope=0", "GET", "k"}, "", "0", "GET k"},
		{"--type 与 --scope 混写", []string{"--type", "redis", "--scope", "1", "--", "GET", "k"}, "redis", "1", "GET k"},
		{"远端命令里的 --scope 属于命令（无 --）", []string{"find", "/", "--scope", "f"}, "", "", "find / --scope f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared, scope, cmd, err := parseExecArgs(tc.args, false)
			require.NoError(t, err)
			require.Equal(t, tc.wantType, declared)
			require.Equal(t, tc.wantScope, scope)
			require.Equal(t, tc.wantCmd, cmd)
		})
	}

	t.Run("命令前的未知 flag 报错，而不是静默丢弃或发往远端", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--bogus", "1", "--", "ls"}, false)
		require.ErrorContains(t, err, "--bogus")
		_, _, _, err = parseExecArgs([]string{"--bogus", "ls"}, false)
		require.ErrorContains(t, err, "--bogus")
	})
	t.Run("--type 缺值报错", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--type"}, false)
		require.ErrorContains(t, err, "--type")
	})
	t.Run("本地 shell 吃掉的词边界在下游重新切分后仍在", func(t *testing.T) {
		// 下游（扩展 flag DSL、k8s/etcd/kafka canonicalizer、ssh 远端 shell）都会用真正
		// 的 shell 解析器重新切分这个串；裸空格拼接会让带空格的值变成两个词。
		for _, argv := range [][]string{
			{"grep", "foo bar", "file"},
			{"note_put", "--content=restart via systemctl"},
		} {
			_, _, cmd, err := parseExecArgs(append([]string{"--"}, argv...), false)
			require.NoError(t, err)
			words, err := cmdline.Words(cmd)
			require.NoError(t, err)
			require.Equal(t, argv, words)
		}
	})
	t.Run("不含空白的词原样保留，glob 仍交给远端 shell", func(t *testing.T) {
		_, _, cmd, err := parseExecArgs([]string{"--", "ls", "*.log"}, false)
		require.NoError(t, err)
		require.Equal(t, "ls *.log", cmd)
		_, _, cmd, err = parseExecArgs([]string{"--", "grep", "foo bar", "*.log"}, false)
		require.NoError(t, err)
		require.Equal(t, "grep 'foo bar' *.log", cmd)
	})
	t.Run("单个词就是命令串本身，不加引号", func(t *testing.T) {
		// `opsctl exec prod-db -- "SELECT * FROM t"` 是所有 DSL 的文档用法。这对扩展
		// 资产同样成立——单词形式不做重新分词，所以 literalWords 对它没有影响。
		_, _, cmd, err := parseExecArgs([]string{"--", "SELECT * FROM users"}, false)
		require.NoError(t, err)
		require.Equal(t, "SELECT * FROM users", cmd)
		_, _, cmd, err = parseExecArgs([]string{"ls | wc -l"}, false)
		require.NoError(t, err)
		require.Equal(t, "ls | wc -l", cmd)
		_, _, cmd, err = parseExecArgs([]string{"--", "request --path='/x?a=1&b=2'"}, true)
		require.NoError(t, err)
		require.Equal(t, "request --path='/x?a=1&b=2'", cmd)
	})
	t.Run("--scope 缺值报错", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--scope"}, false)
		require.ErrorContains(t, err, "--scope")
	})
	t.Run("没有命令时报错", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--"}, false)
		require.Error(t, err)
		_, _, _, err = parseExecArgs(nil, false)
		require.Error(t, err)
	})

	// 扩展资产：本地 shell 已经交付的每个 argv 词，必须原样（含元字符）到达扩展 flag
	// DSL 的重新切分——包括 ES 查询串常见的 & 这类字符。这是本用例集要锁的回归：
	// `opsctl exec <ext-asset> -- request --path='/x?a=1&b=2'` 此前会在下游被
	// mvdan/sh 当成后台运算符拆成两条语句，报 "only a single command is supported"。
	t.Run("扩展资产：多词 argv 逐词保真，元字符不被当成 shell 操作符", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			argv []string
		}{
			{"& 出现在 flag 值里", []string{"request", "--path=/x?a=1&b=2"}},
			{"| 出现在 flag 值里", []string{"request", "--query=a|b"}},
			{"; 出现在 flag 值里", []string{"request", "--body=a;b"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, _, cmd, err := parseExecArgs(append([]string{"--"}, tc.argv...), true)
				require.NoError(t, err)
				words, err := cmdline.Words(cmd)
				require.NoError(t, err, "joined command %q must re-split cleanly, not error as multiple statements", cmd)
				require.Equal(t, tc.argv, words, "each argv word must survive the round trip verbatim")
			})
		}
	})

	t.Run("非扩展资产：ssh 式多词语义不变，元字符原样交给远端 shell", func(t *testing.T) {
		_, _, cmd, err := parseExecArgs([]string{"--", "ls", "*.log"}, false)
		require.NoError(t, err)
		require.Equal(t, "ls *.log", cmd, "a non-extension asset must still see the bare glob, not a quoted literal")
	})
}
