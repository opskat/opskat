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
//     带 PATH= 赋值的命令；sudo、env、timeout 等包装后的命令名同样算；
//   - 命令名无法静态确定（变量、命令替换、花括号展开、通配、同一命令里定义的函数或别名）：
//     可能是解码，也可能是执行，所以和解码或执行之一同时出现、或出现两次就算两者都有；只有
//     一个这样的命令名（$EDITOR file）不算（决定 24）。
//
// 命令替换、子 shell、if / for 等结构里的命令都会看到；引号、heredoc、here-string 里的
// 文本再解析一遍，在里面认解码与按名字认执行。交给 shell 执行的文本（bash -c '…'、
// ssh host '…'、sh <<EOF、echo '…' | sh）里命令名的位置就是命令名；别的引号文本多半是数据
// （awk '$3 > 1 {print}'），其中的动态命令名带着解码选项（-d 等）或是定义过的函数、别名才算。
func DecodesAndRuns(command string) (bool, error) {
	if len(command) > MaxCommandBytes {
		return false, ErrTooLong
	}
	file, err := parse(command)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	var f findings
	// 按层处理脚本文本，不递归：每一层的文本是上一层各个词里互不重叠的字面部分，总长不超过
	// 原命令，所以每层的工作量都和原命令长度成正比。
	level := []layer{{file: file}}
	for depth := 0; len(level) > 0 && !f.both(); depth++ {
		var next []layer
		for _, l := range level {
			for _, text := range f.scan(l, depth) {
				if depth+1 > maxScriptDepth {
					if mentionsDecoder(text.text) || strings.ContainsAny(text.text, dynamicMarks) {
						f.decodes, f.runs = true, true
					}
					continue
				}
				// 解析不了的文本不是脚本（shell 执行它同样报语法错），只当作数据。
				if nested, err := parse(text.text); err == nil {
					next = append(next, layer{file: nested, script: text.script})
				}
			}
		}
		level = next
	}
	return f.both(), nil
}

// layer 是一段要看的语法树；script 表示它来自交给 shell 执行的文本（bash -c '…'、
// ssh host '…'、sh <<EOF、echo '…' | sh），其中命令名的位置就是命令名。
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

// scan 走一遍 l 的语法树（深度受 MaxCommandBytes 限制），记下解码与执行，返回其中可能是
// 脚本的词的文本。depth 为 0 是原命令本身：命令名是路径或脚本文件名，或带 PATH= 赋值，才算
// 执行；引号里的文本多半是数据（sed 's/a b/c/'），只按名字认执行。先记下定义的函数与别名，
// 它们在调用之后才定义时同样认得出；同时记下哪些词是交给 shell 执行的脚本文本。
func (f *findings) scan(l layer, depth int) []text {
	scripts := map[*syntax.Word]bool{}
	syntax.Walk(l.file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.FuncDecl:
			if n.Name != nil {
				f.define(n.Name.Value)
			}
		case *syntax.CallExpr:
			f.aliases(n)
			markScriptArgs(n, scripts)
		case *syntax.Stmt:
			if runsScript(n) {
				markRedirs(n, scripts)
			}
		case *syntax.BinaryCmd:
			if (n.Op == syntax.Pipe || n.Op == syntax.PipeAll) && runsScript(n.Y) {
				markStage(lastStage(n.X), scripts)
			}
		}
		return true
	})
	var texts []text
	syntax.Walk(l.file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			f.call(n, depth == 0, l.script)
		case *syntax.Assign:
			if depth == 0 && n.Name != nil && pathVars[n.Name.Value] {
				f.runs = true
			}
		case *syntax.Word:
			if t := scriptText(n); strings.ContainsAny(t, " \t\n;|&<>()`") {
				texts = append(texts, text{text: t, script: scripts[n]})
			}
		}
		return true
	})
	return texts
}

// aliases 记下 alias name=value 定义的别名。
func (f *findings) aliases(call *syntax.CallExpr) {
	if len(call.Args) == 0 {
		return
	}
	if name, ok := value(call.Args[0]); !ok || name != "alias" {
		return
	}
	for _, arg := range call.Args[1:] {
		if v, ok := value(arg); ok {
			if name, _, found := strings.Cut(v, "="); found && name != "" {
				f.define(name)
			}
		}
	}
}

// runsScript 判断一条语句是不是把脚本文本交给 shell 执行（sh、sudo bash、ssh host、
// docker exec c sh 等）。
func runsScript(stmt *syntax.Stmt) bool {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	for _, arg := range call.Args {
		if v, ok := value(arg); ok && scriptRunnerName(v) {
			return true
		}
	}
	return false
}

// markScriptArgs 把 shell 之后的参数（bash -c '…'、ssh host '…'）记为脚本文本。
func markScriptArgs(call *syntax.CallExpr, scripts map[*syntax.Word]bool) {
	for i, arg := range call.Args {
		if v, ok := value(arg); ok && scriptRunnerName(v) {
			for _, script := range call.Args[i+1:] {
				scripts[script] = true
			}
			return
		}
	}
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

// markStage 把管道里送进 shell 的那一段的参数与 heredoc 记为脚本文本（echo '…' | sh）。
func markStage(stmt *syntax.Stmt, scripts map[*syntax.Word]bool) {
	if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
		for _, arg := range call.Args {
			scripts[arg] = true
		}
	}
	markRedirs(stmt, scripts)
}

// lastStage 是管道左边的最后一段：a | b | c 解析成 (a | b) | c。
func lastStage(stmt *syntax.Stmt) *syntax.Stmt {
	if b, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && (b.Op == syntax.Pipe || b.Op == syntax.PipeAll) {
		return b.Y
	}
	return stmt
}

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
	word := commandWord(call.Args)
	if word == nil {
		return
	}
	name, static := value(word)
	switch {
	case !static || expands(word) || f.funcs[name]:
		if topLevel || script || f.funcs[name] || hasDecodeOption(words) {
			f.dynamic++
		}
	case topLevel && (strings.Contains(name, "/") || hasScriptExt(name)):
		f.runs = true
	}
}

// commandWord 是 args 里真正被执行的命令名（跳过 sudo、env、timeout 等包装）；只有包装时为 nil。
// 值在运行时才知道的词就是命令名：它可能是包装的选项，也可能就是命令。
func commandWord(args []*syntax.Word) *syntax.Word {
	wrapper := ""
	positional := 0
	for i := 0; i < len(args); i++ {
		name, ok := value(args[i])
		if !ok {
			return args[i]
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
			case wrapper == "timeout" && positional == 0:
				positional++ // 时长
				continue
			}
		}
		if _, ok := wrappers[name]; !ok {
			return args[i]
		}
		wrapper, positional = name, 0
	}
	return nil
}

// expands 判断命令名在 shell 里会不会展开成别的名字：不在引号里的通配（bas?64、base*、
// ba[s]e64）或花括号展开（{ba,}se64）。
func expands(w *syntax.Word) bool {
	for _, part := range w.Parts {
		if lit, ok := part.(*syntax.Lit); ok && pattern.HasMeta(lit.Value, 0) {
			return true
		}
	}
	// SplitBraces 改写传入的词，所以给它一份拷贝；它对任何带 { 的词都返回真，要看结果里有没有展开。
	split := &syntax.Word{Parts: w.Parts}
	if !syntax.SplitBraces(split) {
		return false
	}
	for _, part := range split.Parts {
		if _, ok := part.(*syntax.BraceExp); ok {
			return true
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
	return inNames(runners, word) || scriptRunnerName(word)
}

// scriptRunnerName 判断一个词是不是把参数或标准输入当作 shell 脚本执行的程序名。
func scriptRunnerName(word string) bool {
	return inNames(scriptRunners, word)
}

func inNames(names map[string]bool, word string) bool {
	base := path.Base(word)
	return names[base] || names[strings.TrimRight(base, "0123456789.")]
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
// scriptRunners 里的同样算。
var runners = map[string]bool{
	"python": true, "pypy": true, "perl": true, "ruby": true, "irb": true, "node": true, "nodejs": true,
	"deno": true, "bun": true, "php": true, "lua": true, "luajit": true, "tclsh": true, "wish": true,
	"pwsh": true, "powershell": true, "awk": true, "gawk": true, "mawk": true, "nawk": true,
	"osascript": true, "expect": true,
	"exec": true, "xargs": true,
	"nsenter": true, "chroot": true, "crontab": true, "systemd-run": true,
	"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
}

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
}

// scriptExts 是脚本文件的扩展名：PATH=.:$PATH a.sh 这样不带路径执行的脚本。
var scriptExts = map[string]bool{".sh": true, ".bash": true, ".zsh": true, ".py": true, ".pl": true, ".rb": true, ".php": true, ".js": true, ".ps1": true}

// pathVars 是改变命令从哪里加载、启动时执行什么的变量。
var pathVars = map[string]bool{"PATH": true, "BASH_ENV": true, "ENV": true, "LD_PRELOAD": true}
