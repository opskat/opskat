// Package shelldecode 判断一条 shell 命令是否既解码（base64 等）又执行代码。这样的命令把要执行
// 的内容藏在解码后面，审核器只看得到编码后的外壳，所以不送审（决定 22）。判断只看两者是否都
// 出现，不认两者之间怎样连接，也不解码。纯算法，只依赖 shell 解析器（取词的值用 cmdline.ShellValue，另认 $'…'）。
package shelldecode

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// MaxCommandBytes 是判断前的长度上限：更长的命令不解析，报 ErrTooLong。它同时限住了解析与
// 遍历语法树的递归深度，任何输入都不会让栈溢出，耗时随长度线性增长（决定 23）。
const MaxCommandBytes = 16 << 10

// maxScriptDepth 是引号、heredoc 里的脚本文本再解析的层数上限。更深一层的文本不再解析：
// 提到解码器就按既解码又执行处理，看不清的不当作看清了。
const maxScriptDepth = 8

// ErrTooLong 表示命令超过 MaxCommandBytes，没有判断。
var ErrTooLong = errors.New("command too long to check")

// ErrUnparseable 表示命令解析不了，分不清里面有没有解码又执行。
var ErrUnparseable = errors.New("command cannot be parsed")

// DecodesAndRuns 判断 command 里是否既有解码，又有执行代码，不论出现在哪里（决定 22、24）：
//   - 解码：base64 / base32 / basenc 的 -d / --decode / -D，openssl 的 -d，xxd -r，uudecode；
//     busybox base64、python -m base64 这类写法里同样认得出；交给脚本解释器的代码（参数、
//     heredoc、管道送入）里的 base64 解码（b64decode、Buffer.from(…, 'base64') 等）也算；
//   - 执行代码：运行 shell 与脚本解释器、eval、source / .、ssh、su，以及命令名是路径（./x、
//     /tmp/a.sh）、脚本文件名（a.sh）、带 PATH= 赋值的命令；
//   - 包装启动（sudo、env、timeout、exec、xargs、find -exec、watch、parallel、runuser、
//     systemd-run、chroot、busybox 等）按它实际运行的程序判断：它的选项与参数不是命令名，运行
//     无法静态确定的程序也算执行代码；作为参数出现的名字（systemctl restart ssh）不是在运行它；
//   - 命令名无法静态确定（变量、命令替换、花括号展开、通配）：可能是解码，也可能是执行，所以和
//     解码或执行之一同时出现、或出现两次就算两者都有；只有一个这样的命令名（$EDITOR file）
//     不算。路径命令名只有目录动态时（$HOME/bin/tool）按最后一段的字面名判断；同一命令里定义
//     的函数与别名按内容判断，调用时带上的解码选项交给内容里的解码器（f(){ base64 "$@"; }; f -d）。
//
// 命令替换、子 shell、if / for 等结构里的命令都会看到；引号、heredoc、here-string 里的
// 文本再解析一遍，在里面按同样的规则认。交给 shell 执行的文本（bash -c '…'、ssh host '…'、
// eval、ssh、watch、parallel 用空格连起来的参数、trap 的参数、sh <<EOF、echo '…' | sh）里
// 命令名的位置就是命令名；别的引号文本多半是数据（awk '$3 > 1 {print}'，送进 sh 的 awk 程序
// 也是），其中的动态命令名带着解码选项才算。
func DecodesAndRuns(command string) (bool, error) {
	if len(command) > MaxCommandBytes {
		return false, ErrTooLong
	}
	file, err := parse(command)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	f := findings{defs: map[string][]syntax.Node{}, code: map[*syntax.Word]bool{}}
	// 先按层解析出全部脚本文本，不递归：每一层的文本是上一层各个词里互不重叠的字面部分，总长
	// 不超过原命令，所以每层的工作量都和原命令长度成正比。同时记下各层定义的函数与别名：eval、
	// source 的文本里定义、在外层调用的同样认得出。
	levels := [][]layer{{{file: file}}}
	for depth := 0; depth < len(levels); depth++ {
		var next []layer
		for _, l := range levels[depth] {
			for _, t := range f.collect(l) {
				if depth+1 > maxScriptDepth {
					if mentionsDecoder(t.text) || strings.ContainsAny(t.text, dynamicMarks) {
						f.decodes, f.runs = true, true
					}
					continue
				}
				// 解析不了的文本不是脚本（shell 执行它同样报语法错），只当作数据。
				if nested, err := parse(t.text); err == nil {
					next = append(next, layer{file: nested, script: t.script})
					if t.alias != "" {
						f.defs[t.alias] = append(f.defs[t.alias], nested)
					}
				}
			}
		}
		if len(next) > 0 {
			levels = append(levels, next)
		}
	}
	f.profile()
	for depth, level := range levels {
		for _, l := range level {
			if f.both() {
				return true, nil
			}
			f.check(l, depth == 0)
		}
	}
	return f.both(), nil
}

// layer 是一段要看的语法树；script 表示它来自交给 shell 执行的文本（bash -c '…'、
// ssh host '…'、eval $b -d、sh <<EOF、echo '…' | sh、别名的内容），其中命令名的位置就是命令名。
type layer struct {
	file   *syntax.File
	script bool
}

// text 是一个词里可能是脚本的文本，script 同 layer；alias 不为空时它是这个别名的内容。
type text struct {
	text   string
	script bool
	alias  string
}

func parse(text string) (*syntax.File, error) {
	return syntax.NewParser().Parse(strings.NewReader(text), "")
}

// findings 是目前看到的解码、执行，以及无法静态确定的命令名个数。
type findings struct {
	decodes, runs bool
	dynamic       int
	// defs 是各层脚本里定义的函数名与别名 → 函数体或别名的内容（同名定义多次时都在）。
	defs map[string][]syntax.Node
	// masks 是各个函数、别名的内容里（直接或经别的函数、别名）出现的解码器，按 decoderNames 的位。
	masks map[string]uint16
	// code 是交给脚本解释器的代码：解释器的 heredoc、here-string 与管道送入的文本。
	code map[*syntax.Word]bool
}

func (f *findings) both() bool {
	return f.decodes && f.runs || f.dynamic > 0 && (f.decodes || f.runs) || f.dynamic > 1
}

// collect 走一遍 l 的语法树（深度受 MaxCommandBytes 限制），记下定义的函数与别名、交给解释器
// 的代码，返回其中可能是脚本的词的文本，并标出哪些是交给 shell 执行的。
func (f *findings) collect(l layer) []text {
	scripts := map[*syntax.Word]bool{}
	joined := map[*syntax.Word]bool{}
	walkedScripts := map[*syntax.Stmt]bool{}
	walkedCode := map[*syntax.Stmt]bool{}
	var texts []text
	syntax.Walk(l.file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.FuncDecl:
			if n.Name != nil && n.Body != nil {
				f.defs[n.Name.Value] = append(f.defs[n.Name.Value], n.Body)
			}
		case *syntax.CallExpr:
			cmd := parseCommand(n.Args)
			texts = aliases(n, cmd, joined, texts)
			markScriptArgs(n, cmd, scripts)
			for _, j := range joins(n.Args, cmd) {
				if t := joinScript(j, joined); scriptLike(t) {
					texts = append(texts, text{text: t, script: true})
				}
			}
		case *syntax.Stmt:
			if readsScript(n) {
				markRedirs(n, scripts)
			}
			if runsInterpreter(n) {
				markRedirs(n, f.code)
			}
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				if readsScript(n.Y) {
					markOutput(n.X, scripts, walkedScripts)
				}
				if runsInterpreter(n.Y) {
					markOutput(n.X, f.code, walkedCode)
				}
			}
		}
		return true
	})
	syntax.Walk(l.file, func(node syntax.Node) bool {
		if w, ok := node.(*syntax.Word); ok && !joined[w] {
			if t := scriptText(w); scriptLike(t) {
				texts = append(texts, text{text: t, script: scripts[w]})
			}
		}
		return true
	})
	return texts
}

// scriptLike 判断一段文本里有没有分隔命令或参数的字符，没有的不会是脚本。
func scriptLike(t string) bool {
	return strings.ContainsAny(t, " \t\n;|&<>()`")
}

// profile 算出每个函数、别名的内容里出现的解码器（masks）：内容里的解码器，加上内容里调用的
// 别的函数、别名的解码器。每段内容只走一遍（嵌套定义的函数另算），再沿调用关系传递：每个名字
// 的位至多变 len(decoderNames) 次，耗时随长度线性增长。
func (f *findings) profile() {
	if len(f.defs) == 0 {
		return
	}
	f.masks = map[string]uint16{}
	callers := map[string][]string{}
	for name, bodies := range f.defs {
		for _, body := range bodies {
			syntax.Walk(body, func(node syntax.Node) bool {
				switch n := node.(type) {
				case *syntax.FuncDecl:
					return false
				case *syntax.CallExpr:
					for _, arg := range n.Args {
						if v, ok := value(arg); ok {
							f.masks[name] |= decoderBit(v)
						}
					}
					for _, l := range parseCommand(n.Args).launches {
						if _, ok := f.defs[l.name]; ok && !l.dynamic {
							callers[l.name] = append(callers[l.name], name)
						}
					}
				}
				return true
			})
		}
	}
	var queue []string
	for name, mask := range f.masks {
		if mask != 0 {
			queue = append(queue, name)
		}
	}
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, caller := range callers[name] {
			if mask := f.masks[caller] | f.masks[name]; mask != f.masks[caller] {
				f.masks[caller] = mask
				queue = append(queue, caller)
			}
		}
	}
}

// check 记下 l 里的解码与执行。topLevel 为真时 l 是原命令本身：命令名是路径或脚本文件名，
// 或带 PATH= 赋值，才算执行。
func (f *findings) check(l layer, topLevel bool) {
	syntax.Walk(l.file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			f.call(n, topLevel, l.script)
		case *syntax.Assign:
			if topLevel && n.Name != nil && pathVars[n.Name.Value] {
				f.runs = true
			}
		case *syntax.Word:
			if f.code[n] && codeDecodes.MatchString(scriptText(n)) {
				f.decodes = true
			}
		}
		return true
	})
}

// aliases 记下 alias name=value 定义的别名（command alias、builtin alias 也算），把别名的内容
// 作为交给 shell 执行的文本返回，定义所在的词不再另作文本。
func aliases(call *syntax.CallExpr, cmd command, joined map[*syntax.Word]bool, texts []text) []text {
	if len(cmd.launches) == 0 || cmd.launches[0].name != "alias" {
		return texts
	}
	for _, arg := range call.Args[cmd.launches[0].at+1:] {
		if v, ok := value(arg); ok {
			if name, content, found := strings.Cut(v, "="); found && name != "" {
				joined[arg] = true
				texts = append(texts, text{text: content, script: true, alias: name})
			}
		}
	}
	return texts
}

// argValues 是各个词的值；值在运行时才知道的词是占位的 $_。
func argValues(args []*syntax.Word) []string {
	values := make([]string, len(args))
	for i, arg := range args {
		if v, ok := value(arg); ok {
			values[i] = v
		} else {
			values[i] = placeholder
		}
	}
	return values
}

// scriptRunnerAt 是 args 里第一个把参数或标准输入当作 shell 脚本执行的程序（sh、sudo bash、
// docker exec c sh、ssh host 等）的下标与名字；没有时为 -1 与空串。
func scriptRunnerAt(args []string) (int, string) {
	for i, arg := range args {
		if name := matchName(scriptRunners, arg); name != "" {
			return i, name
		}
	}
	return -1, ""
}

// readsScript 判断一条语句是不是从标准输入读脚本执行（sh、sudo bash -s、docker exec -i c sh、
// 不带命令的 ssh host、ssh host 'sudo bash -s'）：交给它的 heredoc、here-string 与管道输入
// 是脚本文本。带着命令时（sh -c '…'、ssh host 'cat > f'）标准输入是给命令的数据。
func readsScript(stmt *syntax.Stmt) bool {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	args := argValues(call.Args)
	split := false
	for {
		i, name := scriptRunnerAt(args)
		switch name {
		case "", "eval", "watch":
			return false
		case "ssh":
			rest := args[i+1:]
			cmd := rest[sshCommandAt(rest):]
			if len(cmd) == 0 {
				return true
			}
			// 远端命令是这些参数用空格连成、再由远端 shell 拆开的一段。拆开后的词里没有空白，
			// 只拆一次，耗时随长度线性增长。
			if !split {
				cmd, split = strings.Fields(strings.Join(cmd, " ")), true
			}
			args = cmd
			continue
		}
		return !hasCommandOption(args[i+1:])
	}
}

// runsInterpreter 判断一条语句是不是运行脚本解释器（python3、sudo node、docker exec c perl）：
// 交给它的 heredoc、here-string 与管道输入是解释器的代码。
func runsInterpreter(stmt *syntax.Stmt) bool {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	for _, l := range parseCommand(call.Args).launches {
		if !l.dynamic && matchName(interpreters, l.name) != "" {
			return true
		}
	}
	return false
}

// hasCommandOption 判断 shell、su 的参数里有没有 -c（-lc 等）或 --command。
func hasCommandOption(args []string) bool {
	for _, v := range args {
		if shortFlagWith(v, 'c') || v == "--command" || strings.HasPrefix(v, "--command=") {
			return true
		}
	}
	return false
}

// markScriptArgs 把 shell 之后的参数（bash -c '…'、ssh host '…'）与 trap 的参数记为脚本文本。
func markScriptArgs(call *syntax.CallExpr, cmd command, scripts map[*syntax.Word]bool) {
	i := -1
	if len(cmd.launches) > 0 && cmd.launches[0].name == "trap" {
		i = cmd.launches[0].at
	}
	if i < 0 {
		i, _ = scriptRunnerAt(argValues(call.Args))
	}
	if i < 0 {
		return
	}
	for _, script := range call.Args[i+1:] {
		scripts[script] = true
	}
}

// joined 是一段被用空格（lines 为真时用换行）连成脚本再交给 shell 执行的参数。
type joined struct {
	args  []*syntax.Word
	lines bool
}

// joins 是一条命令里连成脚本执行的参数：eval、ssh 用空格连起来的参数（eval $b -d、
// ssh host $b -d），watch、parallel 运行的命令（watch -n1 echo … '|' sh、parallel ::: '…'）。
func joins(args []*syntax.Word, cmd command) []joined {
	var out []joined
	values := argValues(args)
	i, name := scriptRunnerAt(values)
	switch name {
	case "eval":
		out = append(out, joined{args: args[i+1:]})
	case "ssh":
		out = append(out, joined{args: args[i+1+sshCommandAt(values[i+1:]):]})
	}
	if cmd.joinAt >= 0 {
		out = append(out, joined{args: args[cmd.joinAt:], lines: cmd.joinLines})
	}
	return out
}

// joinScript 把 j 的脚本文本连起来，并把它们记进 done，不再各自作为文本。词之间在原文里至少
// 隔着一个空白，所以连起来的文本不比原文长；parallel 的 ::: 等分隔不连进去。
func joinScript(j joined, done map[*syntax.Word]bool) string {
	sep := byte(' ')
	if j.lines {
		sep = '\n'
	}
	var b strings.Builder
	for _, arg := range j.args {
		done[arg] = true
		if v, ok := value(arg); ok && j.lines && parallelSeparators[v] {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(sep)
		}
		b.WriteString(scriptText(arg))
	}
	return b.String()
}

// sshValueOptions 是 ssh 带值的短选项。
const sshValueOptions = "BbcDEeFIiJLlmOoPpQRSWw"

// sshCommandAt 是 ssh 的参数里远端命令开始的下标（跳过选项与主机名；占位的 $_ 当作主机名）；
// 没有远端命令时为 len(args)。
func sshCommandAt(args []string) int {
	for i := 0; i < len(args); i++ {
		v := args[i]
		switch {
		case v == "--":
			return min(i+2, len(args))
		case len(v) > 1 && v[0] == '-':
			for j := 1; j < len(v); j++ {
				if strings.IndexByte(sshValueOptions, v[j]) >= 0 {
					if j == len(v)-1 {
						i++ // 值是下一个词
					}
					break
				}
			}
		default:
			return i + 1
		}
	}
	return len(args)
}

// markRedirs 把一条语句的 heredoc 与 here-string 记进 marks。
func markRedirs(stmt *syntax.Stmt, marks map[*syntax.Word]bool) {
	for _, r := range stmt.Redirs {
		if r.Word != nil {
			marks[r.Word] = true
		}
		if r.Hdoc != nil {
			marks[r.Hdoc] = true
		}
	}
}

// markOutput 把管道左边会原样输出的文本记进 marks：echo、printf 的参数与 heredoc、
// here-string，在子 shell、花括号块与中间的过滤命令里同样算（{ echo '…'; } | sh、
// cat <<EOF | tee x | sh）。awk、jq 等的程序不是脚本，它们的输出才是。walked 记下已经
// 走过的语句，嵌套的管道不重复走，耗时随长度线性增长。
func markOutput(stmt *syntax.Stmt, marks map[*syntax.Word]bool, walked map[*syntax.Stmt]bool) {
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Stmt:
			if walked[n] {
				return false
			}
			walked[n] = true
			markRedirs(n, marks)
		case *syntax.CallExpr:
			if ls := parseCommand(n.Args).launches; len(ls) > 0 && printers[ls[0].name] {
				for _, arg := range n.Args[ls[0].at+1:] {
					marks[arg] = true
				}
			}
		}
		return true
	})
}

// printers 是把参数原样输出的命令。
var printers = map[string]bool{"echo": true, "printf": true, "print": true}

// call 认一条简单命令里的解码与执行。script 为真时它在交给 shell 执行的文本里，命令名的
// 位置就是命令名；否则在别的引号文本（awk、jq 程序）里，动态的「命令名」多半是数据，带着
// 解码选项才算。
func (f *findings) call(call *syntax.CallExpr, topLevel, script bool) {
	if len(call.Args) == 0 {
		return
	}
	cmd := parseCommand(call.Args)
	words := make([]string, 0, len(call.Args)+len(cmd.launches))
	for _, arg := range call.Args {
		if v, ok := value(arg); ok {
			words = append(words, v)
		}
	}
	for _, l := range cmd.launches {
		if l.tail {
			words = append(words, l.name) // $DIR/base64 的 base64
		}
	}
	if decodes(words) {
		f.decodes = true
	}
	counted := topLevel || script || hasDecodeOption(words)
	for k, l := range cmd.launches {
		if l.dynamic {
			if counted {
				f.dynamic++
				if l.wrapped {
					f.runs = true
				}
			}
			continue
		}
		if executes(l.name) {
			f.runs = true
		}
		if topLevel && (l.path || hasScriptExt(l.name)) {
			f.runs = true
		}
		if f.decodesThrough(l.name, words) {
			f.decodes = true
		}
		if name := matchName(interpreters, l.name); name != "" {
			end := len(call.Args)
			if k+1 < len(cmd.launches) {
				end = cmd.launches[k+1].at
			}
			if interpreterDecodes(name, call.Args[min(l.at+1, end):end]) {
				f.decodes = true
			}
		}
	}
}

// decodesThrough 判断调用同一命令里定义的函数、别名 name 时，带上的词（words）里有没有它内容里
// 某个解码器的解码选项：f(){ base64 "$@"; }; f -d 就是 base64 -d。
func (f *findings) decodesThrough(name string, words []string) bool {
	mask := f.masks[name]
	for bit, decoder := range decoderNames {
		if mask&(1<<bit) == 0 {
			continue
		}
		for _, w := range words {
			if decoders[decoder](w) {
				return true
			}
		}
	}
	return false
}

// executes 判断运行 name 这个程序是不是执行代码：shell 与脚本解释器、eval、source、ssh、su 等。
func executes(name string) bool {
	return matchName(shells, name) != "" || matchName(interpreters, name) != "" || executors[path.Base(name)]
}

// interpreterDecodes 判断交给解释器 name 的参数里有没有 base64 解码：代码里的 b64decode 等，
// 或 pwsh 的 -EncodedCommand。
func interpreterDecodes(name string, args []*syntax.Word) bool {
	for _, arg := range args {
		t := scriptText(arg)
		if codeDecodes.MatchString(t) {
			return true
		}
		if name == "pwsh" || name == "powershell" {
			if lower := strings.ToLower(t); len(lower) > 1 && (strings.HasPrefix("-encodedcommand", lower) || lower == "-ec") {
				return true
			}
		}
	}
	return false
}

// codeDecodes 认解释器代码里的 base64（及 base32 / base85）解码：Python 的 b64decode、
// base64.decodebytes、codecs.decode(…, 'base64')，Node 的 Buffer.from(…, 'base64')、atob，
// Perl 的 decode_base64，Ruby 的 decode64、unpack1('m')，PHP 的 base64_decode，.NET 的
// FromBase64String，Go 的 base64.StdEncoding.DecodeString 等。RE2 匹配耗时随长度线性增长。
var codeDecodes = regexp.MustCompile(`(?i)` +
	`[ab](64|32|85)decode|base64(\.|::)decode|base64\.\w*encoding\.decode|decode_?base64|base64_?decode|decode64|` +
	`frombase64|\batob\s*\(|unpack1?\s*\(?\s*['"]m0?['"]|` +
	`(buffer(\.from)?|codecs\.decode)\s*\((?:[^()]|\([^()]*\))*base64`)

// launch 是一条简单命令实际运行的一个程序。
type launch struct {
	at      int    // 程序名在参数里的下标
	name    string // 程序名；只有目录动态的路径（$DIR/base64）是最后一段
	dynamic bool   // 程序名无法静态确定
	path    bool   // 程序名是路径
	tail    bool   // name 是只有目录动态的路径的最后一段
	wrapped bool   // 由包装启动或 find -exec 运行
}

// command 是一条简单命令运行的程序，以及 watch、parallel 连成脚本交给 shell 的参数。
type command struct {
	launches  []launch
	joinAt    int  // watch、parallel 运行的命令从这里起；没有时为 -1
	joinLines bool // parallel 不带命令模板：之后的每个参数各是一条命令
}

// parseCommand 找出 args 实际运行的程序：跳过包装（sudo、env、timeout、exec、xargs 等）的
// 选项与参数，看它运行的程序；find 的 -exec 等与 kubectl / docker exec 之后的程序同样算。
// 每个词至多看两遍（下标只增不减），耗时随词数线性增长。
func parseCommand(args []*syntax.Word) command {
	c := command{joinAt: -1}
	wrapped := false
	for i := 0; i < len(args); {
		name, dynamic, isPath := commandName(args[i])
		l := launch{at: i, name: name, dynamic: dynamic, path: isPath, wrapped: wrapped}
		if dynamic {
			c.launches = append(c.launches, l)
			i, wrapped = afterFindExec(args, i+1), true
			continue
		}
		_, static := value(args[i])
		l.tail = !static
		base := path.Base(name)
		if base == "runuser" && !hasUserOption(args[i+1:]) {
			// runuser -l user -c '…' 与 su 一样运行 shell；runuser -u user -- cmd 是包装
			l.name, base = "su", "su"
		}
		spec, isWrapper := wrappers[base]
		if !isWrapper {
			if c.joinAt == i && strings.ContainsAny(name, " \t\n;|&<>()") {
				break // watch 'a | b' 的命令是一段脚本，在连成的脚本里看
			}
			c.launches = append(c.launches, l)
			// 容器里运行的程序只在下一个 find -exec 之前找，每个词只扫一遍
			next := afterFindExec(args, i+1)
			if containerTools[base] {
				if j := containerCommandAt(args[:next], i+1); j > 0 {
					next = j
				}
			}
			i, wrapped = next, true
			continue
		}
		j, shell, stop := spec.programAt(args, i+1)
		if stop {
			break
		}
		if j == len(args) {
			if shell {
				c.launches = append(c.launches, launch{at: i, name: "sh", wrapped: true})
			}
			break
		}
		if spec.joins && c.joinAt < 0 {
			c.joinAt = j
			if base == "parallel" {
				v, _ := value(args[j])
				if c.joinLines = parallelSeparators[v] || strings.Contains(v, "{"); c.joinLines {
					break // 没有命令模板（或模板就是 {}）：参数各是一条命令，在连成的脚本里看
				}
			}
		}
		i, wrapped = j, true
	}
	return c
}

// afterFindExec 是 args[from:] 里下一个 find -exec、-execdir、-ok、-okdir 之后的下标；没有时为
// len(args)。
func afterFindExec(args []*syntax.Word, from int) int {
	for k := from; k < len(args); k++ {
		if v, ok := value(args[k]); ok && findExecs[v] {
			return k + 1
		}
	}
	return len(args)
}

// containerCommandAt 是 kubectl / docker exec、run 等之后在容器里运行的程序（shell、解释器或
// 包装）的下标；没有时为 -1。
func containerCommandAt(args []*syntax.Word, from int) int {
	sub := false
	for k := from; k < len(args); k++ {
		v, ok := value(args[k])
		if !ok {
			continue
		}
		if !sub {
			sub = containerSubcommands[v]
			continue
		}
		if base := path.Base(v); executes(v) || wrappers[base].known {
			return k
		}
	}
	return -1
}

// hasUserOption 判断 runuser 开头的选项里有没有 -u / --user：有时它像 sudo 一样直接运行后面的
// 程序。只看到第一个不是选项的词，这一段之后 programAt 还会再走一遍，每个词至多看两遍。
func hasUserOption(args []*syntax.Word) bool {
	for j := 0; j < len(args); j++ {
		v, ok := value(args[j])
		if !ok || !strings.HasPrefix(v, "-") || v == "--" {
			return false
		}
		if v == "-u" || v == "--user" || strings.HasPrefix(v, "--user=") {
			return true
		}
		if wrappers["runuser"].values[v] {
			j++
		}
	}
	return false
}

// wrapper 描述一个代为运行后面程序的包装。
type wrapper struct {
	known    bool            // 是包装（零值表示不是）
	values   map[string]bool // 带值的选项（值不是程序名）
	operands int             // 程序名之前的位置参数个数：timeout 的时长、chroot 的新根目录
	// shell 判断一个选项是否让它在没有程序时运行 shell（sudo -s、doas -s、systemd-run --shell）。
	shell func(option string) bool
	// shellIfNone 表示没有程序时运行 shell（chroot /、nsenter -t 1 -m）。
	shellIfNone bool
	stops       map[string]bool // 这些选项让它不运行程序（command -v）
	joins       bool            // 把程序与参数用空格连成脚本交给 shell（watch、parallel）
}

// programAt 是包装的参数 args[from:] 里程序名的下标：跳过选项（及带值选项的值）、赋值
// （FOO=$BAR）与位置参数（timeout $T）；值在运行时才知道的词按它的字面开头判断。没有程序时
// 为 len(args)，shell 表示它这时运行 shell；stop 表示它不运行程序。
func (w wrapper) programAt(args []*syntax.Word, from int) (at int, shell, stop bool) {
	shell = w.shellIfNone
	positional := 0
	optionsDone := false
	for j := from; j < len(args); j++ {
		v, static := value(args[j])
		if !static {
			v = literalPrefix(args[j])
		}
		switch {
		case !optionsDone && static && v == "--":
			optionsDone = true
		case !optionsDone && strings.HasPrefix(v, "-"):
			if static {
				if w.stops[v] {
					return len(args), false, true
				}
				if w.shell != nil && w.shell(v) {
					shell = true
				}
				if w.values[v] {
					j++
				}
			}
		case isAssignment(v):
		case positional < w.operands:
			positional++
		default:
			return j, shell, false
		}
	}
	return len(args), shell, false
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}

// wrappers 是代为运行后面程序的包装。
var wrappers = map[string]wrapper{
	"sudo": {known: true, values: set("-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-U", "-T",
		"--user", "--group", "--close-from", "--chdir", "--host", "--prompt", "--role", "--type", "--other-user", "--command-timeout"),
		shell: func(o string) bool {
			return shortFlagWith(o, 's') || shortFlagWith(o, 'i') || o == "--shell" || o == "--login"
		}},
	"doas":    {known: true, values: set("-u", "-C"), shell: func(o string) bool { return o == "-s" }},
	"pkexec":  {known: true, values: set("--user")},
	"env":     {known: true, values: set("-u", "-C", "-S", "--unset", "--chdir", "--split-string")},
	"timeout": {known: true, values: set("-s", "-k", "--signal", "--kill-after"), operands: 1},
	"nice":    {known: true, values: set("-n", "--adjustment")},
	"ionice":  {known: true, values: set("-c", "-n", "-p", "-P", "-u", "--class", "--classdata", "--pid", "--pgid", "--uid")},
	"stdbuf":  {known: true, values: set("-i", "-o", "-e")},
	"time":    {known: true, values: set("-f", "-o", "--format", "--output")},
	"taskset": {known: true, operands: 1},
	"chrt":    {known: true, operands: 1},
	"nohup":   {known: true},
	"setsid":  {known: true},
	"setpriv": {known: true},
	"command": {known: true, stops: set("-v", "-V")},
	"builtin": {known: true},
	"exec":    {known: true, values: set("-a")},
	"xargs": {known: true, values: set("-a", "-d", "-E", "-I", "-L", "-n", "-P", "-s",
		"--arg-file", "--delimiter", "--max-lines", "--max-args", "--max-procs", "--max-chars", "--process-slot-var")},
	"busybox": {known: true},
	"chroot":  {known: true, values: set("--userspec", "--groups"), operands: 1, shellIfNone: true},
	"nsenter": {known: true, values: set("-t", "--target", "-S", "--setuid", "-G", "--setgid"), shellIfNone: true},
	"unshare": {known: true, shellIfNone: true},
	"runuser": {known: true, values: set("-u", "--user", "-g", "--group", "-G", "--supp-group", "-w", "--whitelist-environment")},
	"systemd-run": {known: true, values: set("-p", "-u", "-E", "-M", "-H", "--property", "--unit", "--setenv", "--machine", "--host",
		"--description", "--slice", "--uid", "--gid", "--nice", "--working-directory", "--service-type",
		"--on-active", "--on-boot", "--on-startup", "--on-unit-active", "--on-unit-inactive", "--on-calendar",
		"--timer-property", "--path-property", "--socket-property"),
		shell: func(o string) bool { return o == "-S" || o == "--shell" }},
	"flock": {known: true, values: set("-w", "-E", "-c", "--timeout", "--conflict-exit-code", "--command"), operands: 1,
		shell: func(o string) bool { return o == "-c" || o == "--command" }},
	"strace":       {known: true, values: set("-o", "-e", "-p", "-s", "-u", "-a", "-E", "-I", "-b", "-O", "-P", "-S", "-X")},
	"ltrace":       {known: true, values: set("-o", "-e", "-p", "-s", "-u", "-a", "-n", "-l", "-A", "-D")},
	"valgrind":     {known: true},
	"xvfb-run":     {known: true, values: set("-n", "-s", "-e", "-f", "-p", "--server-num", "--server-args", "--error-file", "--auth-file", "--xauth-protocol")},
	"firejail":     {known: true},
	"fakeroot":     {known: true},
	"faketime":     {known: true, operands: 1},
	"eatmydata":    {known: true},
	"nocache":      {known: true},
	"torsocks":     {known: true},
	"caffeinate":   {known: true, values: set("-t", "-w")},
	"numactl":      {known: true},
	"cpulimit":     {known: true, values: set("-l", "-p", "-e", "--limit", "--pid", "--exe")},
	"proxychains":  {known: true, values: set("-f")},
	"proxychains4": {known: true, values: set("-f")},
	"watch":        {known: true, values: set("-n", "-q", "--interval", "--equexit"), joins: true},
	"parallel": {known: true, values: set("-j", "-S", "-a", "-n", "-N", "-L", "-I", "-P", "-C", "-d", "-E",
		"--jobs", "--sshlogin", "--arg-file", "--max-args", "--max-lines", "--max-procs", "--delay", "--timeout",
		"--joblog", "--results", "--tmpdir", "--workdir", "--wd", "--colsep", "--delimiter", "--env", "--halt",
		"--memfree", "--load", "--nice", "--retries", "--sshloginfile", "--slf", "--basefile", "--bf", "--return"),
		joins: true},
}

// parallelSeparators 是 parallel 分开命令与参数的记号。
var parallelSeparators = map[string]bool{":::": true, "::::": true, ":::+": true, "::::+": true}

// containerTools 与 containerSubcommands：kubectl exec、docker run 等之后在容器里运行程序。
var containerTools = map[string]bool{
	"kubectl": true, "oc": true, "docker": true, "podman": true, "nerdctl": true, "crictl": true,
	"lxc": true, "incus": true, "ctr": true, "ip": true,
}
var containerSubcommands = map[string]bool{"exec": true, "run": true, "debug": true}

// commandName 是命令名的词运行的程序名。花括号展开、通配、变量、命令替换让它无法静态确定
// （dynamic）；只有目录部分动态的路径（$HOME/bin/tool、"$DIR"/base64、/opt/*/bin/tool）按
// 最后一段的字面名判断。
func commandName(w *syntax.Word) (name string, dynamic, isPath bool) {
	if hasBraces(w) {
		return "", true, false
	}
	v, static := value(w)
	if static && !hasGlob(w) {
		return v, false, strings.Contains(v, "/")
	}
	tail := literalTail(w)
	k := strings.LastIndexByte(tail, '/')
	if k < 0 || k == len(tail)-1 || pattern.HasMeta(tail[k+1:], 0) {
		return "", true, false
	}
	return tail[k+1:], false, true
}

// hasGlob 判断词里有没有不在引号里的通配（bas?64、base*、ba[s]e64）。
func hasGlob(w *syntax.Word) bool {
	for _, part := range w.Parts {
		if lit, ok := part.(*syntax.Lit); ok && pattern.HasMeta(lit.Value, 0) {
			return true
		}
	}
	return false
}

// hasBraces 判断词在 shell 里会不会做花括号展开（{ba,}se64）。只看有没有一对 { } 里直接含
// , 或 ..，比 shell 宽一点（{1..a} 不展开也算），但一遍扫完；syntax.SplitBraces 在层层嵌套
// 的 { 上耗时随长度平方增长。
func hasBraces(w *syntax.Word) bool {
	var open []bool // 每层还没闭合的 { 里有没有 , 或 ..
	for _, part := range w.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			continue
		}
		s := lit.Value
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '{':
				open = append(open, false)
			case ',':
				if len(open) > 0 {
					open[len(open)-1] = true
				}
			case '.':
				if len(open) > 0 && i+1 < len(s) && s[i+1] == '.' {
					open[len(open)-1] = true
					i++
				}
			case '}':
				if len(open) > 0 {
					if open[len(open)-1] {
						return true
					}
					open = open[:len(open)-1]
				}
			}
		}
	}
	return false
}

// hasDecodeOption 判断词里有没有某个解码器的解码选项（-d、--decode、-r 等）。
func hasDecodeOption(words []string) bool {
	for _, w := range words {
		if baseDecodeFlag(w) || decoders["openssl"](w) || decoders["xxd"](w) {
			return true
		}
	}
	return false
}

// dynamicMarks 是不再解析的脚本文本里可能让命令名无法静态确定的字符。
const dynamicMarks = "$`{*?["

func isAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	return eq > 0 && syntax.ValidName(word[:eq])
}

func hasScriptExt(name string) bool {
	return scriptExts[path.Ext(name)]
}

// matchName 是 word 在 names 里对应的程序名（/bin/sh 是 sh，python3.11 是 python）；不在时为空串。
func matchName(names map[string]bool, word string) string {
	base := path.Base(word)
	if names[base] {
		return base
	}
	if trimmed := strings.TrimRight(base, "0123456789."); names[trimmed] {
		return trimmed
	}
	return ""
}

// decodes 判断一条命令的词里是否有解码器及其解码选项。先找出出现了哪几种解码器（至多
// len(decoders) 种），再各扫一遍词，耗时随词数线性增长。
func decodes(words []string) bool {
	var mask uint16
	for _, w := range words {
		mask |= decoderBit(w)
	}
	if mask == 0 {
		return false
	}
	for bit, name := range decoderNames {
		if mask&(1<<bit) == 0 {
			continue
		}
		for _, flag := range words {
			if decoders[name](flag) {
				return true
			}
		}
	}
	return false
}

// decoderBit 是 word 作为解码器名（/usr/bin/base64 也算）在 decoderNames 里的位；不是时为 0。
func decoderBit(word string) uint16 {
	return decoderBits[path.Base(word)]
}

// mentionsDecoder 判断不再解析的脚本文本里有没有解码器的名字。先去掉引号、反斜杠与 $：
// ba""se64、"base"64、ba\se64 这样拆开的名字在层数上限以内认得出，这里同样认得出。
func mentionsDecoder(text string) bool {
	text = quoting.Replace(text)
	for name := range decoders {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}

var quoting = strings.NewReplacer(`'`, "", `"`, "", `\`, "", "$", "")

// piece 是一个词里的一段：字面段带着它的值（ok），变量、命令替换等运行时才知道的段 ok 为假。
type piece struct {
	value string
	ok    bool
}

// pieces 把一个词依次拆成段，双引号里的各段分开看。
func pieces(w *syntax.Word) []piece {
	out := make([]piece, 0, len(w.Parts))
	for _, part := range w.Parts {
		if v, ok := partValue(part); ok {
			out = append(out, piece{v, true})
			continue
		}
		dq, ok := part.(*syntax.DblQuoted)
		if !ok {
			out = append(out, piece{})
			continue
		}
		for _, q := range dq.Parts {
			v, ok := partValue(&syntax.DblQuoted{Parts: []syntax.WordPart{q}})
			out = append(out, piece{v, ok})
		}
	}
	return out
}

// literalPrefix 是一个词开头的字面部分（-$X 的 -，FOO=$BAR 的 FOO=）。
func literalPrefix(w *syntax.Word) string {
	var b strings.Builder
	for _, p := range pieces(w) {
		if !p.ok {
			break
		}
		b.WriteString(p.value)
	}
	return b.String()
}

// literalTail 是一个词结尾的字面部分（$HOME/bin/tool 的 /bin/tool）。
func literalTail(w *syntax.Word) string {
	ps := pieces(w)
	k := len(ps)
	for k > 0 && ps[k-1].ok {
		k--
	}
	var b strings.Builder
	for _, p := range ps[k:] {
		b.WriteString(p.value)
	}
	return b.String()
}

// scriptText 是一个词里可能是脚本的文本：字面部分取 shell 去掉引号、转义后的值（同 value），
// 变量、命令替换等换成占位的 $_（命令替换里的命令已在语法树里直接看到）：它的值运行时才知道，
// 落在命令名的位置时同样无法静态确定（bash -c "… $b -d …"）。每段字面值只属于一个词、取值
// 不会变长，被占位的部分至少两个字符，所以各个词的文本互不重叠，总长不超过原文。
func scriptText(w *syntax.Word) string {
	var b strings.Builder
	for _, p := range pieces(w) {
		if p.ok {
			b.WriteString(p.value)
		} else {
			b.WriteString(placeholder)
		}
	}
	return b.String()
}

const placeholder = "$_"

// value 是一个词在 shell 里的值：去掉引号、解开转义，$'…' 按 bash 解开（\x62 是 b）。含变量、
// 命令替换等运行时才知道的部分时 ok 为假。
func value(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		v, ok := partValue(part)
		if !ok {
			return "", false
		}
		b.WriteString(v)
	}
	return b.String(), true
}

// partValue 是一个词里一段的值。cmdline.ShellValue 不认 $'…'，这里补上。
func partValue(part syntax.WordPart) (string, bool) {
	if q, ok := part.(*syntax.SglQuoted); ok && q.Dollar {
		// args 为 nil 时 Format 只解转义、不认 % 格式，不会出错。和 bash 一样截在 \x00。
		s, _, _ := expand.Format(nil, q.Value, nil)
		s, _, _ = strings.Cut(s, "\x00")
		return s, true
	}
	return cmdline.ShellValue(&syntax.Word{Parts: []syntax.WordPart{part}})
}

// shortFlagWith 判断 flag 是不是含字母 c 的一组短选项（-d、-di）。
func shortFlagWith(flag string, c byte) bool {
	if len(flag) < 2 || flag[0] != '-' || flag[1] == '-' {
		return false
	}
	for i := 1; i < len(flag); i++ {
		if c := flag[i] | 0x20; c < 'a' || c > 'z' {
			return false
		}
	}
	return strings.IndexByte(flag[1:], c) >= 0
}

// baseDecodeFlag 是 base64 / base32 / basenc 的解码选项：-d、-D（macOS）、--decode 及其缩写。
func baseDecodeFlag(flag string) bool {
	return shortFlagWith(flag, 'd') || shortFlagWith(flag, 'D') ||
		len(flag) >= 3 && strings.HasPrefix("--decode", flag)
}

// decoders 是解码器的程序名 → 判断一个词是不是它的解码选项。
var decoders = map[string]func(string) bool{
	"base64":   baseDecodeFlag,
	"base32":   baseDecodeFlag,
	"basenc":   baseDecodeFlag,
	"gbase64":  baseDecodeFlag,
	"gbase32":  baseDecodeFlag,
	"openssl":  func(f string) bool { return f == "-d" || f == "-decode" || f == "-decrypt" },
	"xxd":      func(f string) bool { return shortFlagWith(f, 'r') || f == "-revert" },
	"uudecode": func(string) bool { return true },
}

// decoderNames 是解码器名按字母排的次序，decoderBits 是各自的位（函数、别名内容里的解码器
// 记成位，传递时按位合并）。
var decoderNames, decoderBits = func() ([]string, map[string]uint16) {
	names := make([]string, 0, len(decoders))
	for name := range decoders {
		names = append(names, name)
	}
	sort.Strings(names)
	bits := make(map[string]uint16, len(names))
	for i, name := range names {
		bits[name] = 1 << i
	}
	return names, bits
}()

// shells 是 shell 程序名。
var shells = map[string]bool{
	"sh": true, "bash": true, "rbash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "pdksh": true,
	"ash": true, "yash": true, "fish": true, "csh": true, "tcsh": true,
}

// interpreters 是脚本解释器：运行它就是执行代码，交给它的代码里的 base64 解码也算解码。
var interpreters = map[string]bool{
	"python": true, "pypy": true, "perl": true, "ruby": true, "irb": true, "node": true, "nodejs": true,
	"deno": true, "bun": true, "php": true, "lua": true, "luajit": true, "tclsh": true, "wish": true,
	"pwsh": true, "powershell": true, "awk": true, "gawk": true, "mawk": true, "nawk": true,
	"osascript": true, "expect": true,
}

// executors 是 shell 与解释器以外运行代码的程序：在当前 shell 里执行（eval、source / .），
// 交给远端、另一个用户或组的 shell（ssh、su、sg），在记录的终端里运行 shell（script），或排期
// 执行（at、batch、crontab）。
var executors = map[string]bool{
	"eval": true, "source": true, ".": true, "ssh": true, "su": true, "sg": true, "script": true,
	"at": true, "batch": true, "crontab": true,
}

// findExecs 是 find 执行命令的选项：之后的词是命令名。
var findExecs = map[string]bool{"-exec": true, "-execdir": true, "-ok": true, "-okdir": true}

// scriptRunners 是把参数或标准输入当作 shell 脚本执行的程序名：它们之后的参数、交给它们的
// heredoc 与管道输入是脚本文本。
var scriptRunners = map[string]bool{
	"sh": true, "bash": true, "rbash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "pdksh": true,
	"ash": true, "yash": true, "fish": true, "csh": true, "tcsh": true,
	"eval": true, "parallel": true, "watch": true, "ssh": true, "su": true, "runuser": true,
}

// scriptExts 是脚本文件的扩展名：PATH=.:$PATH a.sh 这样不带路径执行的脚本。
var scriptExts = map[string]bool{".sh": true, ".bash": true, ".zsh": true, ".py": true, ".pl": true, ".rb": true, ".php": true, ".js": true, ".ps1": true}

// pathVars 是改变命令从哪里加载、启动时执行什么的变量。
var pathVars = map[string]bool{"PATH": true, "BASH_ENV": true, "ENV": true, "LD_PRELOAD": true}
