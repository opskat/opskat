// Package shelldecode 判断一条 shell 命令是否既解码（base64 等）又执行代码。这样的命令把要执行
// 的内容藏在解码后面，审核器只看得到编码后的外壳，所以不送审（决定 22）。判断只看两者是否都
// 出现，不认两者之间怎样连接，也不解码。纯算法，只依赖 shell 解析器（取词的值用 cmdline.ShellValue，另认 $'…'）。
package shelldecode

import (
	"errors"
	"fmt"
	"path"
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

// DecodesAndRuns 判断 command 里是否既有解码，又有执行代码，不论出现在哪里：
//   - 解码：base64 / base32 / basenc 的 -d / --decode / -D，openssl 的 -d，xxd -r，uudecode；
//     busybox base64、python -m base64 这类写法里同样认得出；
//   - 执行代码：shell 与脚本解释器、eval、source / .、exec、xargs、find -exec、ssh、su、
//     kubectl / docker exec，以及命令名是路径（./x、/tmp/a.sh）、脚本文件名（a.sh）、
//     带 PATH= 赋值的命令；sudo、env、timeout、exec、xargs 等包装后的命令名与 find -exec
//     之后的命令名同样算；
//   - 命令名无法静态确定（变量、命令替换、花括号展开、通配、同一命令里定义的函数或别名）：
//     可能是解码，也可能是执行，所以和解码或执行之一同时出现、或出现两次就算两者都有；只有
//     一个这样的命令名（$EDITOR file）不算（决定 24）。
//
// 命令替换、子 shell、if / for 等结构里的命令都会看到；引号、heredoc、here-string 里的
// 文本再解析一遍，在里面认解码与按名字认执行。交给 shell 执行的文本（bash -c '…'、
// ssh host '…'、eval 与 ssh 用空格连起来的参数、trap 的参数、sh <<EOF、echo '…' | sh）里
// 命令名的位置就是命令名；别的引号文本多半是数据（awk '$3 > 1 {print}'，送进 sh 的 awk 程序
// 也是），其中的动态命令名带着解码选项（-d 等）或是定义过的函数、别名才算。
func DecodesAndRuns(command string) (bool, error) {
	if len(command) > MaxCommandBytes {
		return false, ErrTooLong
	}
	file, err := parse(command)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	var f findings
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
				}
			}
		}
		if len(next) > 0 {
			levels = append(levels, next)
		}
	}
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
// ssh host '…'、eval $b -d、sh <<EOF、echo '…' | sh），其中命令名的位置就是命令名。
type layer struct {
	file   *syntax.File
	script bool
}

// text 是一个词里可能是脚本的文本，script 同 layer。
type text struct {
	text   string
	script bool
}

func parse(text string) (*syntax.File, error) {
	return syntax.NewParser().Parse(strings.NewReader(text), "")
}

// findings 是目前看到的解码、执行，以及无法静态确定的命令名个数。
type findings struct {
	decodes, runs bool
	dynamic       int
	// funcs 是各层脚本里定义的函数名与别名：调用它们时命令名指向的是函数体或别名的内容，同样
	// 无法静态确定。
	funcs map[string]bool
}

func (f *findings) define(name string) {
	if f.funcs == nil {
		f.funcs = map[string]bool{}
	}
	f.funcs[name] = true
}

func (f *findings) both() bool {
	return f.decodes && f.runs || f.dynamic > 0 && (f.decodes || f.runs) || f.dynamic > 1
}

// collect 走一遍 l 的语法树（深度受 MaxCommandBytes 限制），记下定义的函数与别名，返回其中
// 可能是脚本的词的文本，并标出哪些是交给 shell 执行的。
func (f *findings) collect(l layer) []text {
	scripts := map[*syntax.Word]bool{}
	joined := map[*syntax.Word]bool{}
	walked := map[*syntax.Stmt]bool{}
	var texts []text
	syntax.Walk(l.file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.FuncDecl:
			if n.Name != nil {
				f.define(n.Name.Value)
			}
		case *syntax.CallExpr:
			f.aliases(n)
			markScriptArgs(n, scripts)
			if args := joinedArgs(n.Args); len(args) > 0 {
				if t := joinScript(args, joined); scriptLike(t) {
					texts = append(texts, text{text: t, script: true})
				}
			}
		case *syntax.Stmt:
			if readsScript(n) {
				markRedirs(n, scripts)
			}
		case *syntax.BinaryCmd:
			if (n.Op == syntax.Pipe || n.Op == syntax.PipeAll) && readsScript(n.Y) {
				markOutput(n.X, scripts, walked)
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

// check 记下 l 里的解码与执行。topLevel 为真时 l 是原命令本身：命令名是路径或脚本文件名，
// 或带 PATH= 赋值，才算执行；引号里的文本多半是数据（sed 's/a b/c/'），只按名字认执行。
func (f *findings) check(l layer, topLevel bool) {
	syntax.Walk(l.file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			f.call(n, topLevel, l.script)
		case *syntax.Assign:
			if topLevel && n.Name != nil && pathVars[n.Name.Value] {
				f.runs = true
			}
		}
		return true
	})
}

// aliases 记下 alias name=value 定义的别名（command alias、builtin alias 也算）。
func (f *findings) aliases(call *syntax.CallExpr) {
	i := commandIndex(call.Args)
	if i < 0 {
		return
	}
	if name, ok := value(call.Args[i]); !ok || name != "alias" {
		return
	}
	for _, arg := range call.Args[i+1:] {
		if v, ok := value(arg); ok {
			if name, _, found := strings.Cut(v, "="); found && name != "" {
				f.define(name)
			}
		}
	}
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
func markScriptArgs(call *syntax.CallExpr, scripts map[*syntax.Word]bool) {
	i := commandIndex(call.Args)
	if i >= 0 {
		if name, ok := value(call.Args[i]); !ok || name != "trap" {
			i = -1
		}
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

// joinedArgs 是 eval、ssh 用空格连成一段脚本再执行的参数（eval $b -d、ssh host $b -d）。
func joinedArgs(args []*syntax.Word) []*syntax.Word {
	values := argValues(args)
	i, name := scriptRunnerAt(values)
	switch name {
	case "eval":
		return args[i+1:]
	case "ssh":
		return args[i+1+sshCommandAt(values[i+1:]):]
	}
	return nil
}

// joinScript 把 args 的脚本文本用空格连起来，并把它们记进 joined，不再各自作为文本。词之间
// 在原文里至少隔着一个空白，所以连起来的文本不比原文长。
func joinScript(args []*syntax.Word, joined map[*syntax.Word]bool) string {
	var b strings.Builder
	for i, arg := range args {
		joined[arg] = true
		if i > 0 {
			b.WriteByte(' ')
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

// markRedirs 把交给 shell 的 heredoc 与 here-string 记为脚本文本。
func markRedirs(stmt *syntax.Stmt, scripts map[*syntax.Word]bool) {
	for _, r := range stmt.Redirs {
		if r.Word != nil {
			scripts[r.Word] = true
		}
		if r.Hdoc != nil {
			scripts[r.Hdoc] = true
		}
	}
}

// markOutput 把管道左边输出会送进 shell 的文本记为脚本：echo、printf 的参数与 heredoc、
// here-string，在子 shell、花括号块与中间的过滤命令里同样算（{ echo '…'; } | sh、
// cat <<EOF | tee x | sh）。awk、jq 等的程序不是脚本，它们的输出才是。walked 记下已经
// 走过的语句，嵌套的管道不重复走，耗时随长度线性增长。
func markOutput(stmt *syntax.Stmt, scripts map[*syntax.Word]bool, walked map[*syntax.Stmt]bool) {
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Stmt:
			if walked[n] {
				return false
			}
			walked[n] = true
			markRedirs(n, scripts)
		case *syntax.CallExpr:
			if i := commandIndex(n.Args); i >= 0 {
				if v, ok := value(n.Args[i]); ok && printers[v] {
					for _, arg := range n.Args[i+1:] {
						scripts[arg] = true
					}
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
// 解码选项或是定义过的函数、别名才算。
func (f *findings) call(call *syntax.CallExpr, topLevel, script bool) {
	if len(call.Args) == 0 {
		return
	}
	words := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		if v, ok := value(arg); ok {
			words = append(words, v)
		}
	}
	if decodes(words) {
		f.decodes = true
	}
	for _, w := range words {
		if runnerName(w) {
			f.runs = true
		}
	}
	if name, ok := value(call.Args[0]); ok && commandOnlyRunners[name] {
		f.runs = true
	}
	decodeOption := hasDecodeOption(words)
	for _, word := range commandWords(call.Args) {
		name, static := value(word)
		switch {
		case !static || expands(word) || f.funcs[name]:
			if topLevel || script || f.funcs[name] || decodeOption {
				f.dynamic++
			}
		case topLevel && (strings.Contains(name, "/") || hasScriptExt(name)):
			f.runs = true
		}
	}
}

// commandWords 是 args 里会被当作命令执行的词：命令名，以及 find 的 -exec、-execdir、-ok、
// -okdir 之后的命令名。每个词只看一遍。
func commandWords(args []*syntax.Word) []*syntax.Word {
	var words []*syntax.Word
	for len(args) > 0 {
		i := commandIndex(args)
		if i < 0 {
			break
		}
		words = append(words, args[i])
		args = args[i+1:]
		for len(args) > 0 {
			v, ok := value(args[0])
			args = args[1:]
			if ok && findExecs[v] {
				break
			}
		}
	}
	return words
}

// commandIndex 是 args 里真正被执行的命令名的下标（跳过 sudo、env、timeout、exec、xargs 等
// 包装）；只有包装时为 -1。值在运行时才知道的词就是命令名：它可能是包装的选项，也可能就是命令。
func commandIndex(args []*syntax.Word) int {
	wrapper := ""
	positional := 0
	for i := 0; i < len(args); i++ {
		name, ok := value(args[i])
		if !ok {
			return i
		}
		if wrapper != "" {
			switch {
			case name == "--":
				continue
			case strings.HasPrefix(name, "-"):
				if wrappers[wrapper][name] {
					i++
				}
				continue
			case isAssignment(name):
				continue
			case positional < wrapperOperands[wrapper]:
				positional++ // timeout 的时长、chroot 的新根目录
				continue
			}
		}
		if _, ok := wrappers[name]; !ok {
			return i
		}
		wrapper, positional = name, 0
	}
	return -1
}

// expands 判断命令名在 shell 里会不会展开成别的名字：不在引号里的通配（bas?64、base*、
// ba[s]e64）或花括号展开（{ba,}se64）。花括号只看有没有一对 { } 里直接含 , 或 ..，比 shell
// 宽一点（{1..a} 不展开也算），但一遍扫完；syntax.SplitBraces 在层层嵌套的 { 上耗时随长度
// 平方增长。
func expands(w *syntax.Word) bool {
	var open []bool // 每层还没闭合的 { 里有没有 , 或 ..
	for _, part := range w.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			continue
		}
		if pattern.HasMeta(lit.Value, 0) {
			return true
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

// runnerName 判断一个词是不是执行代码的程序名（/bin/sh、python3.11 也算）或 find -exec。
func runnerName(word string) bool {
	return matchName(runners, word) != "" || matchName(scriptRunners, word) != "" || findExecs[word]
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
	var found map[string]bool
	for _, w := range words {
		if name := path.Base(w); decoders[name] != nil {
			if found == nil {
				found = map[string]bool{}
			}
			found[name] = true
		}
	}
	for name := range found {
		match := decoders[name]
		for _, flag := range words {
			if match(flag) {
				return true
			}
		}
	}
	return false
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

// scriptText 是一个词里可能是脚本的文本：字面部分取 shell 去掉引号、转义后的值（同 value），
// 变量、命令替换等换成占位的 $_（命令替换里的命令已在语法树里直接看到）：它的值运行时才知道，
// 落在命令名的位置时同样无法静态确定（bash -c "… $b -d …"）。每段字面值只属于一个词、取值
// 不会变长，被占位的部分至少两个字符，所以各个词的文本互不重叠，总长不超过原文。
func scriptText(w *syntax.Word) string {
	var b strings.Builder
	for _, part := range w.Parts {
		if v, ok := partValue(part); ok {
			b.WriteString(v)
			continue
		}
		dq, ok := part.(*syntax.DblQuoted)
		if !ok {
			b.WriteString(placeholder)
			continue
		}
		for _, q := range dq.Parts {
			if v, ok := partValue(&syntax.DblQuoted{Parts: []syntax.WordPart{q}}); ok {
				b.WriteString(v)
			} else {
				b.WriteString(placeholder)
			}
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

// runners 是执行代码的程序名：出现在命令的任何位置都算（sudo bash、docker exec、find -exec）。
// scriptRunners 与 findExecs 里的同样算。
var runners = map[string]bool{
	"python": true, "pypy": true, "perl": true, "ruby": true, "irb": true, "node": true, "nodejs": true,
	"deno": true, "bun": true, "php": true, "lua": true, "luajit": true, "tclsh": true, "wish": true,
	"pwsh": true, "powershell": true, "awk": true, "gawk": true, "mawk": true, "nawk": true,
	"osascript": true, "expect": true,
	"exec": true, "xargs": true,
	"nsenter": true, "chroot": true, "crontab": true, "systemd-run": true,
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

// commandOnlyRunners 只在命令名的位置才算执行：作参数时多半是别的意思（find . 的 .）。
var commandOnlyRunners = map[string]bool{".": true, "source": true, "at": true, "batch": true}

// wrappers 是代为执行后面命令的程序 → 它带值的选项（值不是被执行的命令名）。
var wrappers = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true, "-T": true},
	"doas":    {"-u": true, "-C": true},
	"env":     {"-u": true, "-C": true, "--unset": true, "--chdir": true},
	"timeout": {"-s": true, "-k": true, "--signal": true, "--kill-after": true},
	"nice":    {"-n": true, "--adjustment": true},
	"ionice":  {"-c": true, "-n": true, "--class": true, "--classdata": true},
	"stdbuf":  {"-i": true, "-o": true, "-e": true},
	"time":    {"-f": true, "-o": true},
	"nohup":   {},
	"setsid":  {},
	"command": {},
	"builtin": {},
	"exec":    {"-a": true},
	"xargs": {
		"-a": true, "-d": true, "-E": true, "-I": true, "-L": true, "-n": true, "-P": true, "-s": true,
		"--arg-file": true, "--delimiter": true, "--max-lines": true, "--max-args": true, "--max-procs": true,
		"--max-chars": true, "--process-slot-var": true,
	},
	"busybox": {},
	"chroot":  {"--userspec": true, "--groups": true},
	"nsenter": {"-t": true, "--target": true, "-S": true, "--setuid": true, "-G": true, "--setgid": true},
}

// wrapperOperands 是包装在命令名之前要跳过的参数个数：timeout 的时长、chroot 的新根目录。
var wrapperOperands = map[string]int{"timeout": 1, "chroot": 1}

// scriptExts 是脚本文件的扩展名：PATH=.:$PATH a.sh 这样不带路径执行的脚本。
var scriptExts = map[string]bool{".sh": true, ".bash": true, ".zsh": true, ".py": true, ".pl": true, ".rb": true, ".php": true, ".js": true, ".ps1": true}

// pathVars 是改变命令从哪里加载、启动时执行什么的变量。
var pathVars = map[string]bool{"PATH": true, "BASH_ENV": true, "ENV": true, "LD_PRELOAD": true}
