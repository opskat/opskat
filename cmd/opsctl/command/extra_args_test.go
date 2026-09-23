package command

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// 子命令消费不了的参数必须报错：静默忽略会让写错位置的 flag（--delete-assets、
// --mfa-code 等）悄悄失效。每个用例都在任何仓储访问之前被拒绝，所以不需要 DB。
func TestSubcommandsRejectExtraArgs(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  func() int
	}{
		{"ssh", func() int { return cmdSSH(ctx, []string{"86", "--bogus"}) }},
		{"get asset", func() int { return cmdGet(ctx, nil, []string{"asset", "web", "extra"}) }},
		{"get credential", func() int { return cmdGet(ctx, nil, []string{"credential", "c", "extra"}) }},
		{"delete asset", func() int { return cmdDelete(ctx, nil, []string{"asset", "web", "extra"}, "") }},
		{"delete group", func() int { return cmdDelete(ctx, nil, []string{"group", "g", "extra", "--delete-assets"}, "") }},
		{"list groups", func() int { return cmdList(ctx, nil, []string{"groups", "extra"}) }},
		{"list assets", func() int { return cmdList(ctx, nil, []string{"assets", "--type", "ssh", "extra"}) }},
		{"list credentials", func() int { return cmdList(ctx, nil, []string{"credentials", "extra"}) }},
		{"list audit", func() int { return cmdList(ctx, nil, []string{"audit", "extra"}) }},
		{"create group", func() int { return cmdCreate(ctx, nil, []string{"group", "--name", "g", "extra"}, "") }},
		{"update asset", func() int { return cmdUpdate(ctx, nil, []string{"asset", "web", "--name", "n", "extra"}, "") }},
		{"update group", func() int { return cmdUpdate(ctx, nil, []string{"group", "g", "--name", "n", "extra"}, "") }},
		{"cp", func() int { return cmdCp(ctx, nil, []string{"--bogus", "86:/a", "./b"}, "") }},
		{"ext exec", func() int { return cmdExtExec([]string{"ext", "tool", "--bogus"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			stderr := captureStderr(t, func() {
				defer func() {
					if r := recover(); r != nil {
						code = -1
						fmt.Fprintf(os.Stderr, "panic: %v", r)
					}
				}()
				code = tc.run()
			})
			require.Equal(t, 1, code, stderr)
			require.Contains(t, stderr, "unexpected argument")
		})
	}
}

func TestParseBatchInputRejectsArgsWithPipedStdin(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(`{"commands":[{"asset":"86","command":"uptime"}]}`)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	_, err = parseBatchInput([]string{"86:whoami"})
	require.ErrorContains(t, err, "unexpected argument")
}
