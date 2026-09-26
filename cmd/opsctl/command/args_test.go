package command

import (
	"testing"

	"github.com/stretchr/testify/require"
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
			declared, scope, cmd, err := parseExecArgs(tc.args)
			require.NoError(t, err)
			require.Equal(t, tc.wantType, declared)
			require.Equal(t, tc.wantScope, scope)
			require.Equal(t, tc.wantCmd, cmd)
		})
	}

	t.Run("命令前的未知 flag 报错，而不是静默丢弃或发往远端", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--bogus", "1", "--", "ls"})
		require.ErrorContains(t, err, "--bogus")
		_, _, _, err = parseExecArgs([]string{"--bogus", "ls"})
		require.ErrorContains(t, err, "--bogus")
	})
	t.Run("--type 缺值报错", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--type"})
		require.ErrorContains(t, err, "--type")
	})
	t.Run("--scope 缺值报错", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--scope"})
		require.ErrorContains(t, err, "--scope")
	})
	t.Run("没有命令时报错", func(t *testing.T) {
		_, _, _, err := parseExecArgs([]string{"--"})
		require.Error(t, err)
		_, _, _, err = parseExecArgs(nil)
		require.Error(t, err)
	})
}
