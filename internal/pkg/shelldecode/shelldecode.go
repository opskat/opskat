// Package shelldecode 判断一条 shell 命令是否既解码（base64 等）又执行代码。这样的命令把要执行
// 的内容藏在解码后面，审核器只看得到编码后的外壳，所以不送审（决定 22）。判断只看两者是否都
// 出现，不认两者之间怎样连接，也不解码。纯算法，只依赖 shell 解析器（取词的值用 cmdline.ShellValue，另认 $'…'）。
package shelldecode

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
//     运行 shell 的包装（sudo -s、flock -c、sg、script）的命令与标准输入是交给 shell 的脚本；
//   - 不运行参数的程序（决定 26）：表里的程序（echo、grep、ps、cat、chmod、apt、git commit 等）的
//     参数与其中的引号文本不算在运行什么（其中的解码照样算）；表外的程序带着 shell、解释器的名字
//     （tmux new sh）或同一命令里写入的文件（> a.sh、tee、-o 之后 tmux new ./a.sh）作参数，按在运行
//     它算；
//   - 命令名无法静态确定（变量、命令替换、花括号展开、通配）：可能是解码，也可能是执行，所以和
//     解码或执行之一同时出现、或出现两次就算两者都有；只有一个这样的命令名（$EDITOR file）
//     不算。路径命令名只有目录动态、且动态部分都在双引号里时（"$DIR"/base64）按最后一段的字面名
//     判断，没有引号的（$X/dev/stdin）会按空白拆开，无法静态确定（决定 25）；字面的路径（./ls、
//     /usr/bin/base64）是在执行那个文件，不按名字判断；容器 exec / run 没有 -- 时子命令之后的动态词
//     （docker exec -i c $b）也是。同一命令里定义的函数与别名按内容判断，
//     像在调用处展开：别名的内容接上调用时带上的词（alias s=sudo; s $b 是 sudo $b），函数体里的
//     位置参数换成带上的词（f(){ base64 "$@"; }; f -d 是 base64 -d），落在交给 shell 的位置上、或被
//     送进 shell、解释器标准输入的词按脚本、代码看（f(){ sh -c "$1"; }; f '…'、alias s=sudo; s -s '…'、
//     f(){ echo "$1" | sh; }、f(){ python3 <<< "$1"; }），只落在表里程序的参数上的词不算在运行什么
//     （log(){ echo "$@"; }; log "use bash now"）；看不清的（eval "x $1"、shift 之后的位置参数、
//     送进来又被当作脚本读的标准输入）按命令名无法静态确定处理。
//
// 解释器代码里自己实现或拼出的解码认不出（决定 27）：这个判断挡住常见写法，不是安全边界。
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
	f := findings{
		defs:       map[string][]def{},
		code:       map[*syntax.Word]bool{},
		scripts:    map[*syntax.Word]bool{},
		fed:        map[*syntax.CallExpr]bool{},
		written:    map[string]bool{},
		wordLayers: map[*syntax.Word]layer{},
		rechecked:  map[*syntax.Word]feed{},
		feeds:      map[*syntax.Word]feed{},
		landed:     map[*syntax.Word]landing{},
		budget:     maxScriptDepth * MaxCommandBytes, // 每层展开都能把全部的词复制一遍
	}
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
					if t.alias != "" {
						f.defs[t.alias] = append(f.defs[t.alias], def{alias: true}) // 看不清的内容
					}
					continue
				}
				// 解析不了的文本不是脚本（shell 执行它同样报语法错），只当作数据。别名不同：它的内容
				// 与调用时带上的词连在一起才是命令（alias p='echo x |'; p sh），所以记为看不清的内容。
				nested, err := parse(t.text)
				if err == nil {
					nl := layer{file: nested, script: t.script, inert: t.inert, word: t.word}
					next = append(next, nl)
					if t.word != nil {
						f.wordLayers[t.word] = nl
					}
				}
				if t.alias != "" {
					d := def{alias: true, fresh: endsCommand(t.text)}
					if err == nil {
						d.body = nested
					}
					f.defs[t.alias] = append(f.defs[t.alias], d)
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
// inert 表示它是「不运行参数的程序」的参数里的文本（echo "use bash now"、git commit -m "…"）：
// 其中的解码照样算，但不算在运行什么。word 是文本所在的词（一个词对应一段文本时）。
type layer struct {
	file          *syntax.File
	script, inert bool
	word          *syntax.Word
}

// text 是一个词里可能是脚本的文本，script、inert 同 layer；alias 不为空时它是这个别名的内容；
// word 是文本所在的词（一个词对应一段文本时）。
type text struct {
	text          string
	script, inert bool
	alias         string
	word          *syntax.Word
}

func parse(text string) (*syntax.File, error) {
	return syntax.NewParser().Parse(strings.NewReader(text), "")
}

// findings 是目前看到的解码、执行，以及无法静态确定的命令名个数。
type findings struct {
	decodes, runs bool
	dynamic       int
	// defs 是各层脚本里定义的函数名与别名 → 它们的定义（同名定义多次时都在）。
	defs map[string][]def
	// masks 是各个函数、别名的内容里（直接或经别的函数、别名）出现的解码器，按 decoderNames 的位。
	masks map[string]uint16
	// expansions 是调用各个函数、别名时，带上的词与标准输入流进内容的方式。
	expansions map[string]*expansion
	// budget 是展开函数、别名的调用还能复制的词数。用完后的调用按命令名无法静态确定处理，展开的
	// 总工作量因此随长度线性增长，层层调用两次的函数链（f2(){ f1; f1; }）也不会指数增长。
	budget int
	// code 是交给脚本解释器的代码：解释器的 heredoc、here-string 与管道送入的文本。
	code map[*syntax.Word]bool
	// scripts 是交给 shell 执行的词（bash -c 的参数、eval 连起来的参数、送进 sh 的 echo 参数等）。
	scripts map[*syntax.Word]bool
	// fed 是有标准输入送进来的简单命令：管道右边、带 heredoc、here-string 或 < 输入重定向的。
	fed map[*syntax.CallExpr]bool
	// written 是同一命令里写入的文件（重定向、tee、-o 等）的文件名（最后一段）。
	written map[string]bool
	// wordLayers 是一个词的文本解析出的那一段（不是交给 shell 的文本时，展开函数、别名后它落在交给
	// shell 的位置上要再按脚本看一遍）；rechecked 是已经这样看过的词，按脚本、按代码各只看一遍。
	wordLayers map[*syntax.Word]layer
	rechecked  map[*syntax.Word]feed
	// feeds 是函数体里整个是位置参数、又被送进 shell 或解释器标准输入的词（echo "$1" | sh、
	// echo "$1" | python3）：代入后调用处的词是脚本或代码。
	feeds map[*syntax.Word]feed
	// landed 是调用处的词在函数、别名展开后落在哪里：只落在「不运行参数的程序」的参数上的，它的
	// 文本不算在运行什么（log(){ echo "$@"; }; log "use bash now"）。
	landed map[*syntax.Word]landing
}

// feed 是一个词被送进的地方：shell 的脚本或解释器的代码。
type feed uint8

const (
	feedScript feed = 1 << iota
	feedCode
)

// landing 是调用处的词展开后落在的位置，按位合并。
type landing uint8

const (
	landedInert landing = 1 << iota
	landedActive
)

// def 是一个函数或别名的定义。
type def struct {
	// body 是函数体或别名的内容；别名的内容解析不了或超过层数上限时为 nil，看不清。
	body  syntax.Node
	alias bool
	// fresh 表示别名的内容以 ;、& 或换行结尾（或为空）：调用时带上的词是一条新的命令。
	fresh bool
}

// expansion 是调用一个函数或别名时，调用处的东西怎样流进它的内容（按内容展开）。
type expansion struct {
	// opaque：带上的词可能成为看不清的脚本文本（eval "x $1"、alias e=eval、alias r='ssh h'），或者
	// 函数体用 shift、set 改了位置参数。
	opaque bool
	// stdin：内容从标准输入读脚本或代码（f(){ sh; }、alias py=python3）。
	stdin bool
	// leaks：函数体在 forms 与 inert 之外也用到位置参数（for x in "$@"、tmux new "$1.sh"），调用处的
	// 词展开后不只落在 forms 里。
	leaks bool
	// inert：函数体里「不运行参数的程序」的参数用到位置参数（echo "[$(date)] $*"）：调用处的词落在那里。
	inert bool
	// here：函数体里整个是位置参数的 here-string 送进 shell、解释器（sh <<< "$1"、python3 <<< "$1"）。
	here []hereFeed
	// forms：调用展开成的命令。
	forms []form
}

// form 是调用展开成的一条命令：别名是内容最后一条命令的词，调用时带上的词接在它们之后（alias s=sudo）；
// 函数是函数体里用到位置参数的一条命令，位置参数换成调用时带上的词（f(){ sudo "$@"; }、sh -c "$1"）。
type form struct {
	words      []*syntax.Word
	positional bool
}

// hereFeed 是送进 shell、解释器的 here-string 里的位置参数（param 同 positional）。
type hereFeed struct {
	param string
	to    feed
}

// endsCommand 判断别名的内容是否以 ;、&、换行结尾或为空：这时调用时带上的词是一条新的命令。
func endsCommand(content string) bool {
	content = strings.TrimRight(content, " \t")
	return content == "" || strings.ContainsAny(content[len(content)-1:], ";&\n")
}

func (f *findings) both() bool {
	return f.decodes && f.runs || f.dynamic > 0 && (f.decodes || f.runs) || f.dynamic > 1
}

// collect 走一遍 l 的语法树（深度受 MaxCommandBytes 限制），记下定义的函数与别名、交给解释器
// 的代码，返回其中可能是脚本的词的文本，并标出哪些是交给 shell 执行的。
func (f *findings) collect(l layer) []text {
	joined := map[*syntax.Word]bool{}
	inert := map[*syntax.Word]bool{} // 「不运行参数的程序」的参数
	walkedScripts := map[*syntax.Stmt]bool{}
	walkedCode := map[*syntax.Stmt]bool{}
	walkedFed := map[*syntax.Stmt]bool{}
	var texts []text
	syntax.Walk(l.file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.FuncDecl:
			if n.Name != nil && n.Body != nil {
				f.defs[n.Name.Value] = append(f.defs[n.Name.Value], def{body: n.Body})
			}
		case *syntax.CallExpr:
			cmd := parseCommand(n.Args)
			texts = aliases(n, cmd, joined, texts)
			markScriptArgs(n, cmd, f.scripts)
			for k, run := range cmd.launches {
				if run.dynamic {
					continue
				}
				rest := cmd.rest(n.Args, k)
				values := argValues(rest)
				if nonRunner(run.name, values) {
					for _, arg := range rest {
						inert[arg] = true
					}
				}
				f.recordWrites(run.name, values)
			}
			for _, j := range joins(n.Args, cmd) {
				t := joinScript(j, joined)
				dynamicName := false
				for _, arg := range j.args {
					f.scripts[arg] = true
					dynamicName = dynamicName || literalDynamic(arg)
				}
				// env -S 的字符串哪怕只有一个词（-Ssh）也是要运行的命令，eval '$b' 的 $b 是命令名
				if j.split || dynamicName || scriptLike(t) {
					texts = append(texts, text{text: t, script: true, inert: l.inert})
				}
			}
		case *syntax.Stmt:
			f.recordRedirWrites(n)
			if readsScript(n) {
				markRedirs(n, f.scripts)
			}
			if runsInterpreter(n) {
				markRedirs(n, f.code)
			}
			if takesInput(n) {
				f.feed(n, walkedFed)
			}
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				if readsScript(n.Y) {
					markOutput(n.X, f.scripts, walkedScripts)
				}
				if runsInterpreter(n.Y) {
					markOutput(n.X, f.code, walkedCode)
				}
				f.feed(n.Y, walkedFed)
			}
		}
		return true
	})
	syntax.Walk(l.file, func(node syntax.Node) bool {
		if w, ok := node.(*syntax.Word); ok && !joined[w] {
			// 交给 shell 的单个词也是一条命令：sh -c '$b' 的 $b 是命令名
			if t := scriptText(w); scriptLike(t) || f.scripts[w] && literalDynamic(w) {
				texts = append(texts, text{
					text:   t,
					script: f.scripts[w],
					inert:  l.inert || inert[w] && !f.scripts[w] && !f.code[w],
					word:   w,
				})
			}
		}
		return true
	})
	return texts
}

// literalDynamic 判断词的值是字面的，但作为脚本再解析时含变量、通配等（'$b'、'bas?64'）。
func literalDynamic(w *syntax.Word) bool {
	v, ok := value(w)
	return ok && strings.ContainsAny(v, dynamicMarks)
}

// takesInput 判断一条语句有没有从 heredoc、here-string 或文件（<、<>）读标准输入。
func takesInput(stmt *syntax.Stmt) bool {
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.RdrIn, syntax.RdrInOut, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			return true
		}
	}
	return false
}

// feed 把 stmt 里的简单命令记为有标准输入送进来（在子 shell、花括号块里同样算）。walked 记下已经
// 走过的语句，嵌套的管道不重复走，耗时随长度线性增长。
func (f *findings) feed(stmt *syntax.Stmt, walked map[*syntax.Stmt]bool) {
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Stmt:
			if walked[n] {
				return false
			}
			walked[n] = true
		case *syntax.CallExpr:
			f.fed[n] = true
		}
		return true
	})
}

// scriptLike 判断一段文本里有没有分隔命令或参数的字符，没有的不会是脚本。
func scriptLike(t string) bool {
	return strings.ContainsAny(t, " \t\n;|&<>()`")
}

// profile 看一遍每个函数、别名的内容，算出：内容里出现的解码器（masks，加上内容里调用的别的
// 函数、别名的解码器），以及调用时带上的词与标准输入怎样流进内容（expansions）。每段内容只走
// 一遍（嵌套定义的函数另算），再沿调用关系传递解码器与「从标准输入读脚本」：每个名字的位至多变
// len(decoderNames)+1 次，耗时随长度线性增长。
func (f *findings) profile() {
	if len(f.defs) == 0 {
		return
	}
	f.masks = map[string]uint16{}
	f.expansions = map[string]*expansion{}
	callers := map[string][]string{}
	for name, defs := range f.defs {
		e := &expansion{}
		f.expansions[name] = e
		for _, d := range defs {
			if d.body == nil {
				e.opaque, e.stdin = true, true
				continue
			}
			args := map[*syntax.Word]bool{} // 函数体里简单命令的词：整个是位置参数时调用处代入
			uses, substituted := 0, 0       // 函数体里位置参数出现的次数，其中在调用处代入的
			here := map[*syntax.Word]bool{} // 送进 shell、解释器的 here-string 里整个是位置参数的词
			syntax.Walk(d.body, func(node syntax.Node) bool {
				switch n := node.(type) {
				case *syntax.FuncDecl:
					return false
				case *syntax.Stmt:
					if readsScript(n) || runsInterpreter(n) {
						e.stdin = true
					}
					for _, r := range n.Redirs {
						if p, _ := positional(r.Word); r.Op == syntax.WordHdoc && p != "" && !d.alias {
							to := feedCode
							if f.scripts[r.Word] {
								to = feedScript
							} else if !f.code[r.Word] {
								continue
							}
							here[r.Word] = true
							substituted++
							e.here = append(e.here, hereFeed{param: p, to: to})
						}
					}
				case *syntax.ParamExp:
					if positionalParam(n) {
						uses++
					}
				case *syntax.Word:
					if _, static := value(n); d.alias || static {
						break
					}
					switch {
					case here[n]: // 代入后再看（expand）
					case args[n] && isPositional(n) && f.scripts[n]:
						f.feeds[n] = feedScript // 代入后按脚本看（echo "$1" | sh；bash -c "$1" 另由 scriptsIn 认出）
					case args[n] && isPositional(n) && f.code[n]:
						f.feeds[n] = feedCode // 代入后按解释器的代码看（echo "$1" | python3）
					case f.scripts[n], f.code[n] && mentionsPositional(n):
						e.opaque = true // 交给 shell、解释器的文本里有调用时才知道的部分（eval "x $1"、python3 <<< "$1"）
					}
				case *syntax.CallExpr:
					for _, arg := range n.Args {
						if v, ok := value(arg); ok {
							f.masks[name] |= decoderBit(v)
						}
					}
					cmd := parseCommand(n.Args)
					for _, l := range cmd.launches {
						if _, ok := f.defs[l.name]; ok && !l.dynamic {
							callers[l.name] = append(callers[l.name], name)
						}
					}
					if d.alias {
						break
					}
					if len(cmd.launches) > 0 && !cmd.launches[0].dynamic && setsPositionals(cmd.launches[0].name, n.Args[cmd.launches[0].at+1:]) {
						e.opaque = true
					}
					// 函数的参数从 "$@"、$1 落进这条命令：调用时换成调用处的词
					if slices.ContainsFunc(n.Args, isPositional) {
						e.forms = append(e.forms, form{words: n.Args, positional: true})
					}
					for _, arg := range n.Args {
						args[arg] = true
						if isPositional(arg) {
							substituted++
						}
					}
					for k, l := range cmd.launches {
						if rest := cmd.rest(n.Args, k); !l.dynamic && nonRunner(l.name, argValues(rest)) {
							for _, arg := range rest {
								if c := countPositional(arg); c > 0 && !isPositional(arg) && !f.scripts[arg] && !f.code[arg] {
									substituted += c
									e.inert = true
								}
							}
						}
					}
				}
				return true
			})
			if d.alias {
				e.addAlias(d)
			} else if uses != substituted {
				e.leaks = true
			}
		}
	}
	var queue []string
	for name, mask := range f.masks {
		if mask != 0 || f.expansions[name].stdin {
			queue = append(queue, name)
		}
	}
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, caller := range callers[name] {
			mask := f.masks[caller] | f.masks[name]
			stdin := f.expansions[caller].stdin || f.expansions[name].stdin
			if mask != f.masks[caller] || stdin != f.expansions[caller].stdin {
				f.masks[caller], f.expansions[caller].stdin = mask, stdin
				queue = append(queue, caller)
			}
		}
	}
}

// addAlias 记下别名 d 的调用怎样展开：别名的内容与调用时带上的词连在一起，成为内容最后一条
// 简单命令的后续参数；内容以 ;、& 结尾时带上的词是一条新命令。最后一条命令运行 shell、eval、
// ssh 等时，带上的词是脚本文本，看不清。
func (e *expansion) addAlias(d def) {
	if d.fresh {
		e.forms = append(e.forms, form{})
		return
	}
	last := lastCall(d.body.(*syntax.File))
	if last == nil {
		return // 子 shell、花括号块之后再带词是语法错误
	}
	for _, l := range parseCommand(last.Args).launches {
		if l.shell || !l.dynamic && executes(l.name) && matchName(interpreters, l.name) == "" {
			e.opaque = true
		}
	}
	e.forms = append(e.forms, form{words: last.Args})
}

// lastCall 是 file 最后一条语句里最右边的简单命令（a && sudo、cat | sudo 的 sudo）；不是简单命令时为 nil。
func lastCall(file *syntax.File) *syntax.CallExpr {
	if len(file.Stmts) == 0 {
		return nil
	}
	stmt := file.Stmts[len(file.Stmts)-1]
	for {
		switch c := stmt.Cmd.(type) {
		case *syntax.BinaryCmd:
			stmt = c.Y
		case *syntax.CallExpr:
			return c
		default:
			return nil
		}
	}
}

// positional 是整个就是函数位置参数的词（$1、"${2}"、"$@"、$*）的参数名，quoted 表示在双引号里；
// 不是时（"$1.sh"、${1:-x}）为空串。
func positional(w *syntax.Word) (param string, quoted bool) {
	if len(w.Parts) != 1 {
		return "", false
	}
	part := w.Parts[0]
	if dq, ok := part.(*syntax.DblQuoted); ok {
		if len(dq.Parts) != 1 {
			return "", false
		}
		part, quoted = dq.Parts[0], true
	}
	p, ok := part.(*syntax.ParamExp)
	if !ok || p.Param == nil || p.Flags != nil || p.Excl || p.Length || p.Width || p.IsSet || p.NestedParam != nil ||
		p.Index != nil || len(p.Modifiers) > 0 || p.Slice != nil || p.Repl != nil || p.Names != 0 || p.Exp != nil {
		return "", false
	}
	if v := p.Param.Value; v == "@" || v == "*" || v != "0" && strings.Trim(v, "0123456789") == "" {
		return v, quoted
	}
	return "", false
}

func isPositional(w *syntax.Word) bool {
	p, _ := positional(w)
	return p != ""
}

// positionalParam 判断参数展开用的是不是函数的位置参数（$1、$@、${2:-x}、${#1}）。
func positionalParam(p *syntax.ParamExp) bool {
	if p.Param == nil {
		return false
	}
	v := p.Param.Value
	return v == "@" || v == "*" || v != "" && v != "0" && strings.Trim(v, "0123456789") == ""
}

// mentionsPositional 判断词里有没有用到位置参数（"x $1"、$(cat "$1")）。
func mentionsPositional(w *syntax.Word) bool {
	return countPositional(w) > 0
}

// countPositional 是词里用到位置参数的次数。
func countPositional(w *syntax.Word) int {
	n := 0
	syntax.Walk(w, func(node syntax.Node) bool {
		if p, ok := node.(*syntax.ParamExp); ok && positionalParam(p) {
			n++
		}
		return true
	})
	return n
}

// setsPositionals 判断函数体里的命令 name args 是否改了位置参数（shift、set -- a、set a），
// 这时位置参数不再是调用时带上的那些词。
func setsPositionals(name string, args []*syntax.Word) bool {
	switch name {
	case "shift":
		return true
	case "set":
		for _, v := range argValues(args) {
			if v == "--" || !strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "+") {
				return true
			}
		}
	}
	return false
}

// substitute 把函数体里一条命令的位置参数换成调用时带上的词 rest（caller 标出其中哪些是调用处的词）。
// 不在双引号里的位置参数按空白拆开（$1 是 'sudo -s' 时是两个词）；"$*" 与 "$@" 一样按各个词代入，
// 宁可多看。返回展开后的词、它们是不是调用处的词，以及代入的词被送进的地方（feeds 里函数体的词，
// 见 findings.feeds）；复制的词数超过 budget 时 ok 为假。
func substitute(words, rest []*syntax.Word, caller []bool, feeds map[*syntax.Word]feed, budget *int) (args []*syntax.Word, from []bool, fed []feed, ok bool) {
	add := func(w *syntax.Word, c, quoted bool, to feed) {
		if v, static := value(w); static && !quoted && strings.ContainsAny(v, " \t\n") {
			for _, field := range strings.Fields(v) {
				args, from, fed = append(args, &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: field}}}), append(from, c), append(fed, to)
				*budget--
			}
			return
		}
		args, from, fed = append(args, w), append(from, c), append(fed, to)
		*budget--
	}
	for _, w := range words {
		switch p, quoted := positional(w); p {
		case "":
			add(w, false, true, 0)
		case "@", "*":
			for i, r := range rest {
				add(r, counts(caller, i), quoted, feeds[w])
			}
		default:
			if n, _ := strconv.Atoi(p); n >= 1 && n <= len(rest) { // ${00}、过大的编号没有对应的词
				add(rest[n-1], counts(caller, n-1), quoted, feeds[w])
			}
		}
		if *budget < 0 {
			return nil, nil, nil, false
		}
	}
	return args, from, fed, true
}

// counts 判断 args[i] 是不是调用处的词（caller 为 nil 时都是）：只有调用处的词里无法静态确定的命令名
// 在这里算，内容里的已在内容所在的地方算过。
func counts(caller []bool, i int) bool {
	return caller == nil || caller[i]
}

// check 记下 l 里的解码与执行。topLevel 为真时 l 是原命令本身：命令名是路径或脚本文件名，
// 或带 PATH= 赋值，才算执行。函数体里整个是位置参数的词（"$@"、$1）在调用处代入后再判断，不在
// 定义处当作动态命令名。
func (f *findings) check(l layer, topLevel bool) {
	// 调用处的词展开后只落在「不运行参数的程序」的参数上时，它的文本同样不算在运行什么
	inert := l.inert || l.word != nil && !l.script && f.landed[l.word] == landedInert
	var visit func(inFunc bool) func(syntax.Node) bool
	visit = func(inFunc bool) func(syntax.Node) bool {
		return func(node syntax.Node) bool {
			switch n := node.(type) {
			case *syntax.FuncDecl:
				if !inFunc && n.Body != nil {
					syntax.Walk(n.Body, visit(true))
					return false
				}
			case *syntax.CallExpr:
				f.call(n, use{topLevel: topLevel, script: l.script, inert: inert, fed: f.fed[n]}, inFunc)
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
		}
	}
	syntax.Walk(l.file, visit(false))
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
		case "":
			if k := shellLaunchAt(parseCommand(call.Args)); !split && k >= 0 {
				return !hasCommandOption(args[k+1:])
			}
			return false
		case "eval", "watch":
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

// shellLaunchAt 是 cmd 里第一个运行 shell 的包装或程序（sudo -s、flock -c、sg、script）的下标；
// 没有时为 -1。
func shellLaunchAt(cmd command) int {
	for _, l := range cmd.launches {
		if l.shell {
			return l.at
		}
	}
	return -1
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
		i = shellLaunchAt(cmd)
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
	// split 表示这是 env -S 的字符串：第一个词去掉开头 strip 个字节（-S、--split-string=）。
	split bool
	strip int
}

// joins 是一条命令里连成脚本执行的参数：eval、ssh 用空格连起来的参数（eval $b -d、
// ssh host $b -d），watch、parallel 运行的命令（watch -n1 echo … '|' sh、parallel ::: '…'），
// env -S 拆开运行的字符串（env -S 'sh -s'、env -Ssh）。
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
		out = append(out, joined{args: args[cmd.joinAt:], lines: cmd.joinLines, split: cmd.joinSplit, strip: cmd.joinStrip})
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
	for i, arg := range j.args {
		done[arg] = true
		if v, ok := value(arg); ok && j.lines && parallelSeparators[v] {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(sep)
		}
		t := scriptText(arg)
		if i == 0 && j.split {
			t = t[min(j.strip, len(t)):]
		}
		b.WriteString(t)
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

// nonRunner 判断运行 name args 时，参数不会被当作程序或脚本运行（决定 26）：表里的程序，多用途的
// 按子命令与选项判断（git commit 是，git -c …、git rebase 不是）。表只会让漏列的程序多问一次。
func nonRunner(name string, args []string) bool {
	check, listed := nonRunners[path.Base(name)]
	return listed && (check == nil || check(args))
}

// nonRunners 是不运行参数的程序 → 判断这次调用是否如此（nil 表示总是）。
var nonRunners = map[string]func(args []string) bool{
	// 打印、查找名字
	"echo": nil, "printf": nil, "print": nil, "which": nil, "type": nil, "whereis": nil, "hash": nil, "alias": nil,
	// 查找内容、进程
	"grep": nil, "egrep": nil, "fgrep": nil, "zgrep": nil, "ps": nil, "pgrep": nil,
	// 读文件、改文件属性、搬文件
	"cat": nil, "head": nil, "tail": nil, "wc": nil, "ls": nil, "stat": nil, "file": nil, "du": nil, "diff": nil, "cmp": nil,
	"md5sum": nil, "sha1sum": nil, "sha256sum": nil, "sha512sum": nil,
	"chmod": nil, "chown": nil, "chgrp": nil, "cp": nil, "mv": nil, "rm": nil, "mkdir": nil, "rmdir": nil, "touch": nil, "ln": nil, "cd": nil,
	// 服务与日志
	"systemctl": nil, "journalctl": nil,
	// 包管理
	"yum": nil, "dnf": nil, "apk": nil, "pip": nil, "pip3": nil,
	"apt": aptNonRunner, "apt-get": aptNonRunner, "brew": brewNonRunner, "dpkg": dpkgNonRunner, "rpm": rpmNonRunner,
	"git": gitNonRunner,
}

// hasOptionPrefix 判断 args 里有没有以 prefixes 之一开头的选项。
func hasOptionPrefix(args []string, prefixes ...string) bool {
	for _, v := range args {
		if !strings.HasPrefix(v, "-") {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(v, p) {
				return true
			}
		}
	}
	return false
}

// aptNonRunner：-o Dpkg::Pre-Invoke=… 与 -c 配置文件能让 apt 运行命令。
func aptNonRunner(args []string) bool {
	return !hasOptionPrefix(args, "-o", "--option", "-c", "--config-file")
}

// brewNonRunner：brew sh、brew ruby 等运行 shell 或代码，只认常见的安装、查询子命令。
func brewNonRunner(args []string) bool {
	sub := ""
	for _, v := range args {
		if !strings.HasPrefix(v, "-") {
			sub = v
			break
		}
	}
	return brewSubcommands[sub]
}

var brewSubcommands = set("install", "uninstall", "reinstall", "upgrade", "update", "list", "ls", "info", "search",
	"outdated", "doctor", "cleanup", "deps", "uses", "config")

// dpkgNonRunner：只认查询（dpkg -l、-L、-s、-S）；安装包会运行包里的脚本，--pre-invoke 等运行命令。
func dpkgNonRunner(args []string) bool {
	return len(args) > 0 && dpkgQueries[args[0]] && !hasOptionPrefix(args, "--pre-invoke", "--post-invoke", "--status-logger")
}

var dpkgQueries = set("-l", "-L", "-s", "-S", "-p", "--list", "--listfiles", "--status", "--search", "--print-avail", "--get-selections")

// rpmNonRunner：只认查询（rpm -q、-qa、-qi）；宏（--eval、-D）与 --pipe 能运行 shell。
func rpmNonRunner(args []string) bool {
	return len(args) > 0 && strings.HasPrefix(args[0], "-q") &&
		!hasOptionPrefix(args, "--pipe", "--eval", "-E", "--define", "-D", "--macros", "--rcfile", "--load")
}

// gitNonRunner：git 的子命令在表里，且没有会运行命令的选项（全局的 -c、--config-env、--exec-path，
// 子命令的 --exec、--upload-pack、grep -O 等）。
func gitNonRunner(args []string) bool {
	for i := 0; i < len(args); i++ {
		v := args[i]
		switch {
		case v == "-C" || v == "--git-dir" || v == "--work-tree" || v == "--namespace":
			i++
		case hasOptionPrefix([]string{v}, "-c", "--config-env", "--exec-path"):
			return false
		case strings.HasPrefix(v, "-"):
		default:
			return gitSubcommands[v] && !hasOptionPrefix(args[i+1:], "--exec", "--upload-pack", "--receive-pack",
				"-O", "--open-files-in-pager", "--extcmd", "--ext-diff")
		}
	}
	return true
}

var gitSubcommands = set("status", "log", "diff", "show", "add", "commit", "branch", "tag", "checkout", "switch",
	"restore", "reset", "stash", "blame", "shortlog", "describe", "rev-parse", "rev-list", "ls-files", "ls-tree",
	"cat-file", "init", "rm", "mv", "grep", "reflog")

// recordRedirWrites 记下一条语句的输出重定向写入的文件（> a.sh、>> a.sh、&> a.sh）。
func (f *findings) recordRedirWrites(stmt *syntax.Stmt) {
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut, syntax.DplOut:
			// >&2、>&- 是复制、关闭文件描述符，不是文件
			if v, ok := value(r.Word); ok && (r.Op != syntax.DplOut || v != "-" && strings.Trim(v, "0123456789") != "") {
				f.addWritten(v)
			}
		}
	}
}

// recordWrites 记下程序 name 写入的文件：tee 的参数，cp、mv、install、ln 的目标，dd 的 of=，以及
// -o、-O、--output、-out 等选项的值。
func (f *findings) recordWrites(name string, args []string) {
	base := path.Base(name)
	var operands []string
	for i, v := range args {
		switch {
		case outputOptions[v] && i+1 < len(args):
			f.addWritten(args[i+1])
		case strings.HasPrefix(v, "--output=") || strings.HasPrefix(v, "--output-document="):
			_, file, _ := strings.Cut(v, "=")
			f.addWritten(file)
		case base == "dd" && strings.HasPrefix(v, "of="):
			f.addWritten(v[len("of="):])
		case !strings.HasPrefix(v, "-"):
			operands = append(operands, v)
		}
	}
	switch base {
	case "tee":
		for _, v := range operands {
			f.addWritten(v)
		}
	case "cp", "mv", "install", "ln":
		if len(operands) > 1 {
			f.addWritten(operands[len(operands)-1])
		}
	}
}

var outputOptions = set("-o", "-O", "--output", "--output-document", "-out")

// addWritten 记下写入的文件 file 的文件名；运行时才知道的、/dev 下的不算。
func (f *findings) addWritten(file string) {
	if file == placeholder || file == "" || strings.HasPrefix(file, "/dev/") {
		return
	}
	if base := path.Base(file); base != "." && base != "/" {
		f.written[base] = true
	}
}

// call 认一条简单命令里的解码与执行。u.script 为真时它在交给 shell 执行的文本里，命令名的
// 位置就是命令名；否则在别的引号文本（awk、jq 程序）里，动态的「命令名」多半是数据，带着
// 解码选项才算。inFunc 为真时它在函数体里，整个是位置参数的词在调用处代入后才算。
func (f *findings) call(call *syntax.CallExpr, u use, inFunc bool) {
	if len(call.Args) == 0 {
		return
	}
	var caller []bool
	if inFunc {
		caller = make([]bool, len(call.Args))
		for i, arg := range call.Args {
			caller[i] = !isPositional(arg)
		}
	}
	f.launches(call.Args, caller, nil, u, nil)
}

// use 是判断一条简单命令时的上下文：它在哪一层、有没有标准输入送进来，以及其中无法静态确定的
// 命令名算不算数（counted，见 call）。inert 同 layer：其中的命令不算在运行什么。
type use struct {
	topLevel, script, inert, fed, counted bool
}

// launches 认 args 运行的各个程序里的解码与执行。args 是一条简单命令的词，或者函数、别名展开后
// 的词；caller 标出其中哪些是调用处的词（见 counts），fed 是展开后各个词被送进的地方（见 substitute）。
// expanding 是正在展开的函数、别名名字，由外到内。
func (f *findings) launches(args []*syntax.Word, caller []bool, fed []feed, u use, expanding []string) {
	cmd := parseCommand(args)
	words := make([]string, 0, len(args)+len(cmd.launches))
	for _, arg := range args {
		if v, ok := value(arg); ok {
			words = append(words, v)
		}
	}
	for _, l := range cmd.launches {
		if l.tail {
			words = append(words, l.name) // $DIR/base64 的 base64
		}
	}
	if len(expanding) == 0 {
		u.counted = u.topLevel || u.script || hasDecodeOption(words)
	}
	// 调用的函数、别名内容里的解码器，配上调用时带上的解码选项（f(){ base64 "$@"; }; f -d）
	mask := wordsMask(words)
	for k, l := range cmd.launches {
		rest := cmd.rest(args, k)
		if l.dynamic {
			if u.counted && counts(caller, l.at) {
				f.dynamic++
				if l.wrapped && !u.inert {
					f.runs = true
				}
			}
			continue
		}
		values := argValues(rest)
		if !u.inert {
			if executes(l.name) {
				f.runs = true
			}
			// 执行路径形式的程序就是执行那个文件（./ls 可能是刚写入的脚本）；只有动态部分在双引号里
			// 或是 ~ 的路径按最后一段的名字判断（"$DIR"/base64、~/bin/ls，决定 25）
			named := (l.tail || strings.HasPrefix(l.name, "~")) && (decoderBit(l.name) != 0 || nonRunner(l.name, values))
			if u.topLevel && (l.path && !named || hasScriptExt(l.name)) {
				f.runs = true
			}
		}
		mask |= f.masks[l.name]
		if name := matchName(interpreters, l.name); name != "" && interpreterDecodes(name, rest) {
			f.decodes = true
		}
		if _, defined := f.defs[l.name]; defined && !l.path {
			var restCaller []bool
			if caller != nil {
				restCaller = caller[l.at+1 : l.at+1+len(rest)]
			}
			f.expand(l.name, rest, restCaller, u, expanding)
		} else if !u.inert && f.mayRunArgument(l.name, rest, values) {
			f.runs = true
		}
	}
	if decodesWith(mask, words) {
		f.decodes = true
	}
	if len(expanding) > 0 {
		f.land(args, caller, cmd, u, expanding)
		f.scriptsIn(args, caller, fed, cmd, u)
	}
}

// land 记下展开后的命令 args 里调用处的词落在哪里：「不运行参数的程序」的参数上（inert），还是别处
// （命令名、别的程序的参数）。展开链上的函数在 forms 之外也用到位置参数时（leaks），调用处的词还可能
// 落在别处，都记为别处。
func (f *findings) land(args []*syntax.Word, caller []bool, cmd command, u use, expanding []string) {
	inert := make([]bool, len(args))
	passed := make([]bool, len(args)) // 交给同一命令里的函数、别名的参数：在那次展开里记
	leaks := u.inert || slices.ContainsFunc(expanding, func(name string) bool { return f.expansions[name].leaks })
	for k, l := range cmd.launches {
		rest := cmd.rest(args, k)
		_, defined := f.defs[l.name]
		mark := passed
		switch {
		case l.dynamic:
			continue
		case defined && !l.path:
		case !leaks && nonRunner(l.name, argValues(rest)):
			mark = inert
		default:
			continue
		}
		for i := l.at + 1; i < l.at+1+len(rest); i++ {
			mark[i] = true
		}
	}
	for i, arg := range args {
		switch {
		case !counts(caller, i) || passed[i]:
		case inert[i]:
			f.landed[arg] |= landedInert
		default:
			f.landed[arg] |= landedActive
		}
	}
}

// landedActiveRest 把调用时带上的词 rest 记为落在别处（展开没做完、按命令名无法静态确定处理时）。
func (f *findings) landedActiveRest(rest []*syntax.Word) {
	for _, w := range rest {
		f.landed[w] |= landedActive
	}
}

// rest 是 c.launches[k] 这个程序的参数：到下一个运行的程序为止。
func (c command) rest(args []*syntax.Word, k int) []*syntax.Word {
	end := len(args)
	if k+1 < len(c.launches) {
		end = c.launches[k+1].at
	}
	return args[min(c.launches[k].at+1, end):end]
}

// expand 按内容展开对函数、别名 name 的调用（rest 是调用时带上的词，restCaller 同 launches 的
// caller）：别名的内容与带上的词连成一条命令，函数体里的位置参数换成带上的词，再认一遍。和 bash
// 展开别名一样，正在展开的名字不再展开（alias a=b; alias b=a、f(){ f; }）；流进内容后看不清的（带上的
// 词成为看不清的脚本文本，或送进来的标准输入被当作脚本读），以及展开层数或词数超过上限的，按命令名
// 无法静态确定处理。
func (f *findings) expand(name string, rest []*syntax.Word, restCaller []bool, u use, expanding []string) {
	if slices.Contains(expanding, name) {
		return
	}
	e := f.expansions[name]
	if e.opaque && len(rest) > 0 || e.stdin && u.fed {
		f.unknown(u)
		f.landedActiveRest(rest)
		return
	}
	if restCaller == nil {
		restCaller = make([]bool, len(rest))
		for i := range restCaller {
			restCaller[i] = true
		}
	}
	expanding = append(expanding[:len(expanding):len(expanding)], name)
	for _, form := range e.forms {
		args, caller, fed, ok := f.expandForm(form, rest, restCaller)
		if !ok || len(expanding) > maxScriptDepth {
			f.unknown(u)
			f.landedActiveRest(rest)
			return
		}
		if len(args) > 0 {
			f.launches(args, caller, fed, u, expanding)
		}
	}
	// here-string 里的位置参数换成调用处的词：每个代入的词扣一次预算，耗时随长度线性增长
	for _, h := range e.here {
		from, to := 0, len(rest)
		if h.param != "@" && h.param != "*" {
			n, _ := strconv.Atoi(h.param) // ${00}、过大的编号没有对应的词
			from, to = min(max(n-1, 0), len(rest)), min(max(n, 0), len(rest))
		}
		for i := from; i < to; i++ {
			if f.budget--; f.budget < 0 {
				f.unknown(u)
				f.landedActiveRest(rest)
				return
			}
			if restCaller[i] {
				f.feedWord(rest[i], h.to, u)
			}
		}
	}
	if e.inert {
		to := landedInert
		if u.inert || slices.ContainsFunc(expanding, func(name string) bool { return f.expansions[name].leaks }) {
			to = landedActive
		}
		for i, w := range rest {
			if restCaller[i] {
				f.landed[w] |= to
			}
		}
	}
}

// expandForm 是一次调用按 form 展开后的词、它们是不是调用处的词，以及被送进的地方（见 substitute）；
// 复制的词数超过预算时 ok 为假，先扣预算再复制。
func (f *findings) expandForm(form form, rest []*syntax.Word, restCaller []bool) (args []*syntax.Word, caller []bool, fed []feed, ok bool) {
	f.budget--
	if form.positional {
		return substitute(form.words, rest, restCaller, f.feeds, &f.budget)
	}
	if f.budget -= len(form.words) + len(rest); f.budget < 0 {
		return nil, nil, nil, false
	}
	args = append(form.words[:len(form.words):len(form.words)], rest...)
	caller = append(make([]bool, len(form.words), len(args)), restCaller...)
	return args, caller, nil, true
}

// scriptsIn 看展开后的命令 args 里落在交给 shell 的位置上的调用处的词（alias s=sudo 后 s -s '…' 的
// '…'，f(){ sh -c "$1"; } 后 f '…' 的 '…'）：它们在调用处只是参数，这里按脚本再看一遍。展开后的命令
// 从标准输入读脚本、代码，而调用处有输入送进来时（echo '…' | s -s），送进来的没当作脚本看过，按命令名
// 无法静态确定处理。函数体把位置参数送进 shell、解释器的标准输入时（echo "$1" | sh、echo "$1" | python3，
// 见 fed），代入的词同样是脚本、代码。
func (f *findings) scriptsIn(args []*syntax.Word, caller []bool, fed []feed, cmd command, u use) {
	if u.inert {
		return
	}
	call := &syntax.CallExpr{Args: args}
	if stmt := (&syntax.Stmt{Cmd: call}); u.fed && (readsScript(stmt) || runsInterpreter(stmt)) {
		f.unknown(u)
	}
	marks := map[*syntax.Word]bool{}
	markScriptArgs(call, cmd, marks)
	for _, j := range joins(args, cmd) {
		for _, arg := range j.args {
			marks[arg] = true
		}
	}
	for i, arg := range args {
		if !counts(caller, i) {
			continue
		}
		switch {
		case marks[arg]:
			f.feedWord(arg, feedScript, u)
		case i < len(fed) && fed[i] != 0:
			f.feedWord(arg, fed[i], u)
		}
	}
}

// feedWord 看调用处的词 w 展开后被送进 shell（to 为 feedScript）或解释器（feedCode）：按脚本再看一遍，
// 或认其中的 base64 解码。
func (f *findings) feedWord(w *syntax.Word, to feed, u use) {
	if u.inert {
		return
	}
	if f.rechecked[w]&to != 0 {
		return
	}
	f.rechecked[w] |= to
	if to == feedCode {
		if codeDecodes.MatchString(scriptText(w)) {
			f.decodes = true
		}
		return
	}
	if l, ok := f.wordLayers[w]; ok {
		if !l.script {
			f.check(layer{file: l.file, script: true}, false)
		}
	} else if !f.scripts[w] && literalDynamic(w) {
		f.dynamic++ // 单个词的脚本，命令名无法静态确定（f '$b'）
	}
}

// unknown 记下一个无法静态确定的命令名。
func (f *findings) unknown(u use) {
	if u.counted {
		f.dynamic++
	}
}

// mayRunArgument 判断程序 name 是不是可能在运行它的参数：包装启动的表不是封闭的（tmux new sh、
// screen -dm python3、tmux new ./a.sh），不在「不运行参数的程序」表里的程序，参数是 shell、解释器
// 的名字（--shell=bash、core.pager=sh 取 = 之后），或是同一命令里写入的文件时，按可能在运行它处理。
// 包装与容器工具已按实际运行的程序判断，解码器读写入的文件不是运行它。values 是 args 的值。
func (f *findings) mayRunArgument(name string, args []*syntax.Word, values []string) bool {
	base := path.Base(name)
	if wrappers[base].known || containerTools[base] || decoderBit(base) != 0 || nonRunner(base, values) {
		return false
	}
	for i, v := range values {
		if v == placeholder {
			if t := literalTail(args[i]); t != "" && f.written[path.Base(t)] {
				return true // "$PWD"/a.sh
			}
			continue
		}
		if namesProgram(v) || f.written[path.Base(v)] {
			return true
		}
	}
	return false
}

// namesProgram 判断参数的值是不是 shell、解释器的名字：sh、/bin/bash、--shell=bash、core.pager=sh、
// 'sh -c …' 的第一个词。
func namesProgram(v string) bool {
	if k := strings.IndexByte(v, '='); k >= 0 && !strings.ContainsAny(v[:k], " \t\n") {
		v = v[k+1:]
	}
	if fields := strings.Fields(v); len(fields) > 0 {
		v = fields[0]
	}
	return matchName(shells, v) != "" || matchName(interpreters, v) != ""
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
// base64.decodebytes（单独导入的 decodebytes 也算）、binascii.a2b_base64、codecs.decode(…, 'base64')，
// Node 的 Buffer.from(…, 'base64')、atob，Perl 的 decode_base64，Ruby 的 decode64、unpack1('m')，
// PHP 的 base64_decode，.NET 的 FromBase64String，Go 的 base64.StdEncoding.DecodeString 等。
// Buffer.from(…) 的括号里再套两层括号也认得出。RE2 匹配耗时随长度线性增长。
var codeDecodes = regexp.MustCompile(`(?i)` +
	`[ab](64|32|85)decode|base64(\.|::)decode|base64\.\w*encoding\.decode|decode_?base64|base64_?decode|decode64|` +
	`\bdecode(bytes|string)\b|a2b_base64|frombase64|\batob\s*\(|unpack1?\s*\(?\s*['"]m0?['"]|` +
	`(buffer(\.from)?|\.decode)\s*\((?:[^()]|\((?:[^()]|\([^()]*\))*\))*base64`)

// launch 是一条简单命令实际运行的一个程序。
type launch struct {
	at      int    // 程序名在参数里的下标
	name    string // 程序名；只有目录动态的路径（$DIR/base64）是最后一段
	dynamic bool   // 程序名无法静态确定
	path    bool   // 程序名是路径
	tail    bool   // name 是只有目录动态的路径的最后一段
	wrapped bool   // 由包装启动或 find -exec 运行
	shell   bool   // 运行 shell，之后的参数与标准输入是交给它的脚本（sudo -s、flock -c、sg、script）
}

// command 是一条简单命令运行的程序，以及 watch、parallel 连成脚本交给 shell 的参数。
type command struct {
	launches  []launch
	joinAt    int  // watch、parallel、env -S 运行的命令从这里起；没有时为 -1
	joinLines bool // parallel 不带命令模板：之后的每个参数各是一条命令
	joinSplit bool // env -S：args[joinAt] 去掉开头 joinStrip 个字节后是要运行的命令
	joinStrip int
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
			l.shell = shellLaunchers[base]
			c.launches = append(c.launches, l)
			// 容器里运行的程序只在下一个 find -exec 之前找，每个词至多扫两遍
			next := afterFindExec(args, i+1)
			if containerTools[base] {
				j, guess := containerCommandAt(base, args[:next], i+1)
				if guess >= 0 && (j < 0 || guess < j) {
					// 也可能是容器名：只算无法静态确定的命令名，不算在运行它
					c.launches = append(c.launches, launch{at: guess, dynamic: true})
				}
				if j > 0 {
					next = j
				}
			}
			i, wrapped = next, true
			continue
		}
		j, shell, stop, strip := spec.programAt(args, i+1)
		// 以下几种情况这个包装之后的词不再有命令名，但 find 的下一个 -exec 之后还有
		switch {
		case stop:
			i, wrapped = afterFindExec(args, i+1), true
			continue
		case strip >= 0 && j < len(args):
			// env -S：要运行的命令在 args[j] 去掉开头 strip 个字节之后的字符串里，连同之后的参数
			// 在连成的脚本里看。字符串开头是不带引号、变量的程序名时也记为运行的程序（它是 shell
			// 时送进来的标准输入是脚本，是解释器时是代码）。
			if c.joinAt < 0 {
				c.joinAt, c.joinStrip, c.joinSplit = j, strip, true
			}
			if v, ok := value(args[j]); ok {
				first, _, _ := strings.Cut(strings.TrimLeft(v[min(strip, len(v)):], " \t"), " ")
				if first != "" && !strings.ContainsAny(first, "\t\"'\\$`{}*?[") {
					c.launches = append(c.launches, launch{at: j, name: first, path: strings.Contains(first, "/"),
						wrapped: true, shell: matchName(shells, first) != ""})
				}
			}
			i, wrapped = afterFindExec(args, j+1), true
			continue
		case shell && (j == len(args) || !spec.shellIfNone):
			// 选项让它运行 shell 时（sudo -s cmd、flock f -c cmd），之后的词都是交给这个 shell 的
			c.launches = append(c.launches, launch{at: i, name: "sh", wrapped: true, shell: true})
			i, wrapped = afterFindExec(args, i+1), true
			continue
		}
		if j == len(args) {
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

// containerCommandAt 是 kubectl / docker exec、run 等之后在容器里运行的程序（-- 之后的词，或
// shell、解释器、包装）的下标 at；没有时为 -1。docker / podman / nerdctl 的 exec、run 与 kubectl 的
// exec、run、debug 没有用 -- 隔开程序时，子命令之后的第一个动态词（不算 -e FOO=$X、--image=$I 这类
// 选项与赋值）分不清是容器名还是程序，guess 是它的下标；没有时为 -1（修正轮 7）。
func containerCommandAt(tool string, args []*syntax.Word, from int) (at, guess int) {
	at, guess = -1, -1
	k := from
	for ; k < len(args); k++ {
		if v, ok := value(args[k]); ok && containerSubcommands[v] {
			break
		}
	}
	if k == len(args) {
		return at, guess
	}
	sub, _ := value(args[k])
	guessing := containerProgramSubcommands[tool][sub] && dashDashAt(args, k+1) < 0
	for k++; k < len(args); k++ {
		v, ok := value(args[k])
		if !ok {
			if prefix := literalPrefix(args[k]); guessing && guess < 0 && !strings.HasPrefix(prefix, "-") && !isAssignment(prefix) {
				guess = k
			}
			continue
		}
		if v == "--" && k+1 < len(args) {
			return k + 1, guess // kubectl exec p -- 之后的词是要运行的程序，无法静态确定时同样算
		}
		if base := path.Base(v); executes(v) || wrappers[base].known {
			return k, guess
		}
	}
	return at, guess
}

// dashDashAt 是 args[from:] 里第一个字面的 -- 的下标；没有时为 -1。
func dashDashAt(args []*syntax.Word, from int) int {
	for k := from; k < len(args); k++ {
		if v, ok := value(args[k]); ok && v == "--" {
			return k
		}
	}
	return -1
}

// containerProgramSubcommands 是容器工具里没有 -- 时程序与容器名、镜像名混在一起的子命令。
var containerProgramSubcommands = map[string]map[string]bool{
	"docker": set("exec", "run"), "podman": set("exec", "run"), "nerdctl": set("exec", "run"),
	"kubectl": set("exec", "run", "debug"), "oc": set("exec", "run", "debug"),
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
	splits      bool            // -S / --split-string 的值是拆开运行的命令（env -S）
}

// programAt 是包装的参数 args[from:] 里程序名的下标：跳过选项（及带值选项的值，合在一组短选项
// 里的也算：sudo -Eu x、xargs -n1）、赋值（FOO=$BAR）与位置参数（timeout $T）；值在运行时才知道
// 的词按它的字面开头判断。没有程序时为 len(args)，shell 表示它这时运行 shell；stop 表示它不运行
// 程序。strip 不小于 0 时要运行的命令在 env -S 的字符串里：args[at] 去掉开头 strip 个字节；
// 否则为 -1。
func (w wrapper) programAt(args []*syntax.Word, from int) (at int, shell, stop bool, strip int) {
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
					return len(args), false, true, -1
				}
				if w.shell != nil && w.shell(v) {
					shell = true
				}
				next, split := w.optionValue(v)
				if split >= 0 {
					if next {
						return min(j+1, len(args)), shell, false, 0
					}
					return j, shell, false, split
				}
				if next {
					j++
				}
			}
		case isAssignment(v):
		case positional < w.operands:
			positional++
		default:
			return j, shell, false, -1
		}
	}
	return len(args), shell, false, -1
}

// optionValue 判断选项 v 是否把下一个词当作它的值（next）。v 是 env -S 时 split 不小于 0：next 为真
// 时字符串是下一个词，否则是 v 去掉开头 split 个字节（-Ssh、--split-string=sh）；不是时为 -1。
// 一组短选项（-Eu、-nc、-iS）里，第一个带值的选项取这组剩下的字符，没有剩下的就取下一个词。
func (w wrapper) optionValue(v string) (next bool, split int) {
	if w.splits {
		switch {
		case v == "--split-string":
			return true, 0
		case strings.HasPrefix(v, "--split-string="):
			return false, len("--split-string=")
		}
	}
	if w.values[v] {
		return true, -1
	}
	if len(v) < 2 || v[1] == '-' {
		return false, -1
	}
	for i := 1; i < len(v); i++ {
		if w.splits && v[i] == 'S' {
			return i == len(v)-1, i + 1
		}
		if w.values[v[:1]+v[i:i+1]] {
			return i == len(v)-1, -1
		}
	}
	return false, -1
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
	"doas":    {known: true, values: set("-u", "-C"), shell: func(o string) bool { return shortFlagWith(o, 's') }},
	"pkexec":  {known: true, values: set("--user")},
	"env":     {known: true, values: set("-u", "-C", "--unset", "--chdir"), splits: true},
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
		shell: func(o string) bool { return shortFlagWith(o, 'S') || o == "--shell" }},
	"flock": {known: true, values: set("-w", "-E", "-c", "--timeout", "--conflict-exit-code", "--command"), operands: 1,
		shell: func(o string) bool { return shortFlagWith(o, 'c') || o == "--command" }},
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
// （dynamic）；只有目录部分动态的路径按最后一段的字面名判断，但动态部分要都在双引号里
// （"$DIR"/base64、"$HOME"/bin/tool；~/bin/tool 本是字面的）：没有引号的 $X/dev/stdin 会按空白
// 拆开，第一段才是程序（决定 25）。/opt/*/bin/tool 也按最后一段判断。
func commandName(w *syntax.Word) (name string, dynamic, isPath bool) {
	if hasBraces(w) {
		return "", true, false
	}
	v, static := value(w)
	if static && !hasGlob(w) {
		return v, false, strings.Contains(v, "/")
	}
	for _, part := range w.Parts {
		if _, ok := partValue(part); !ok {
			if _, quoted := part.(*syntax.DblQuoted); !quoted {
				return "", true, false
			}
		}
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

// wordsMask 是 words 里出现的解码器名的位。
func wordsMask(words []string) uint16 {
	var mask uint16
	for _, w := range words {
		mask |= decoderBit(w)
	}
	return mask
}

// decodesWith 判断 words 里有没有 mask 里某个解码器（至多 len(decoders) 种）的解码选项：每种
// 各扫一遍词，耗时随词数线性增长。一条命令里各个程序与它调用的函数、别名的解码器先并起来，
// 词只扫一遍（find . -exec f \; -exec f \; … 不随 -exec 个数平方增长）。
func decodesWith(mask uint16, words []string) bool {
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

// shellLaunchers 是运行 shell 的程序：sg 经 sh -c 运行它的命令，script 在记录的终端里运行 shell
// （-c 给出命令）。之后的参数与标准输入是脚本文本。
var shellLaunchers = map[string]bool{"sg": true, "script": true}

// findExecs 是 find 执行命令的选项：之后的词是命令名。
var findExecs = map[string]bool{"-exec": true, "-execdir": true, "-ok": true, "-okdir": true}

// scriptRunners 是把参数或标准输入当作 shell 脚本执行的程序名：它们之后的参数、交给它们的
// heredoc 与管道输入是脚本文本。
var scriptRunners = func() map[string]bool {
	m := set("eval", "parallel", "watch", "ssh", "su", "runuser")
	for name := range shells {
		m[name] = true
	}
	return m
}()

// scriptExts 是脚本文件的扩展名：PATH=.:$PATH a.sh 这样不带路径执行的脚本。
var scriptExts = map[string]bool{".sh": true, ".bash": true, ".zsh": true, ".py": true, ".pl": true, ".rb": true, ".php": true, ".js": true, ".ps1": true}

// pathVars 是改变命令从哪里加载、启动时执行什么的变量。
var pathVars = map[string]bool{"PATH": true, "BASH_ENV": true, "ENV": true, "LD_PRELOAD": true}
