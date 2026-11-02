// Package shelldecode 判断一条 shell 命令是否既解码（base64 等）又执行代码。这样的命令把要执行
// 的内容藏在解码后面，审核器只看得到编码后的外壳，所以不送审（决定 22）。判断只看两者是否都
// 出现，不认两者之间怎样连接，也不解码。纯算法，只依赖 shell 解析器（取词的值用 cmdline.ShellValue）。
package shelldecode

import (
	"errors"
	"fmt"
	"path"
	"strings"

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
//     kubectl / docker exec，以及命令名是变量、路径（./x、/tmp/a.sh）、脚本文件名（a.sh）、
//     带 PATH= 赋值的命令；sudo、env、timeout 等包装后的命令名同样算。
//
// 命令替换、子 shell、if / for 等结构里的命令都会看到；引号、heredoc、here-string 里的
// 脚本文本（bash -c '…'、ssh host '…'、echo '…' | sh）再解析一遍，在里面认解码与按名字认
// 执行。
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
	level := []*syntax.File{file}
	for depth := 0; len(level) > 0 && !f.both(); depth++ {
		var next []*syntax.File
		for _, file := range level {
			for _, text := range f.scan(file, depth) {
				if depth+1 > maxScriptDepth {
					if mentionsDecoder(text) {
						f.decodes, f.runs = true, true
					}
					continue
				}
				// 解析不了的文本不是脚本（shell 执行它同样报语法错），只当作数据。
				if nested, err := parse(text); err == nil {
					next = append(next, nested)
				}
			}
		}
		level = next
	}
	return f.both(), nil
}

func parse(text string) (*syntax.File, error) {
	return syntax.NewParser().Parse(strings.NewReader(text), "")
}

// findings 是目前看到的解码与执行。
type findings struct {
	decodes, runs bool
}

func (f *findings) both() bool { return f.decodes && f.runs }

// scan 走一遍 file 的语法树（深度受 MaxCommandBytes 限制），记下解码与执行，返回其中可能是
// 脚本的词的文本。depth 为 0 是原命令本身：命令名是变量、路径或脚本文件名，或带 PATH= 赋值，
// 才算执行；引号里的文本多半是数据（sed 's/a b/c/'），只按名字认执行。
func (f *findings) scan(file *syntax.File, depth int) []string {
	var texts []string
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			f.call(n, depth == 0)
		case *syntax.Assign:
			if depth == 0 && n.Name != nil && pathVars[n.Name.Value] {
				f.runs = true
			}
		case *syntax.Word:
			if text := scriptText(n); strings.ContainsAny(text, " \t\n;|&<>()`") {
				texts = append(texts, text)
			}
		}
		return true
	})
	return texts
}

// call 认一条简单命令里的解码与执行。
func (f *findings) call(call *syntax.CallExpr, topLevel bool) {
	if len(call.Args) == 0 {
		return
	}
	words := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		if v, ok := cmdline.ShellValue(arg); ok {
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
	if topLevel && runsProgram(call.Args) {
		f.runs = true
	}
	if name, ok := cmdline.ShellValue(call.Args[0]); ok && commandOnlyRunners[name] {
		f.runs = true
	}
}

// runsProgram 判断命令名（跳过 sudo、env、timeout 等包装）是不是变量、路径或脚本文件名。
func runsProgram(args []*syntax.Word) bool {
	wrapper := ""
	positional := 0
	for i := 0; i < len(args); i++ {
		name, ok := cmdline.ShellValue(args[i])
		if !ok {
			return true
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
		if strings.Contains(name, "/") || hasScriptExt(name) {
			return true
		}
		if _, ok := wrappers[name]; !ok {
			return false
		}
		wrapper, positional = name, 0
	}
	return false
}

func isAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	return eq > 0 && syntax.ValidName(word[:eq])
}

func hasScriptExt(name string) bool {
	return scriptExts[path.Ext(name)]
}

// runnerName 判断一个词是不是执行代码的程序名（/bin/sh、python3.11 也算）或 find -exec。
func runnerName(word string) bool {
	base := path.Base(word)
	return runners[base] || runners[strings.TrimRight(base, "0123456789.")]
}

// decodes 判断一条命令的词里是否有解码器及其解码选项。
func decodes(words []string) bool {
	for _, w := range words {
		match, ok := decoders[path.Base(w)]
		if !ok {
			continue
		}
		for _, flag := range words {
			if match(flag) {
				return true
			}
		}
	}
	return false
}

func mentionsDecoder(text string) bool {
	for name := range decoders {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}

// scriptText 是一个词里可能是脚本的文本：字面部分原样拼起来，变量、命令替换等换成占位的 _
// （命令替换里的命令已在语法树里直接看到）。每段字面值只属于一个词，所以各个词的文本互不重叠。
func scriptText(w *syntax.Word) string {
	if v, ok := cmdline.ShellValue(w); ok {
		return v
	}
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				if lit, ok := q.(*syntax.Lit); ok {
					b.WriteString(lit.Value)
				} else {
					b.WriteString("_")
				}
			}
		default:
			b.WriteString("_")
		}
	}
	return b.String()
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
var runners = map[string]bool{
	"sh": true, "bash": true, "rbash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "pdksh": true,
	"ash": true, "yash": true, "fish": true, "csh": true, "tcsh": true,
	"python": true, "pypy": true, "perl": true, "ruby": true, "irb": true, "node": true, "nodejs": true,
	"deno": true, "bun": true, "php": true, "lua": true, "luajit": true, "tclsh": true, "wish": true,
	"pwsh": true, "powershell": true, "awk": true, "gawk": true, "mawk": true, "nawk": true,
	"osascript": true, "expect": true,
	"eval": true, "exec": true, "xargs": true, "parallel": true, "watch": true,
	"ssh": true, "su": true, "runuser": true, "nsenter": true, "chroot": true, "crontab": true, "systemd-run": true,
	"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
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
