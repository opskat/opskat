package shelldecode

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

// quoteLoopVars 给只会展开成一个词的循环变量加上双引号（issue #384）：for 循环体里不加引号的 $c、${c}
// 改成 "$c"，此后与写成双引号的一样判断（见 containerCommandAt、singleField）。这样的循环变量是：
//   - 列表里每个词都是写死的、非空、不含空白与通配的值（for img in nginx:1.27 nginx:1.28）；或
//   - 列表里有不加引号的变量、命令替换（for c in $(docker ps -q)）：shell 展开列表时已经按空白拆开，
//     每次循环的值里没有空白。值里的通配会在使用处再展开成已有的文件名——那是服务器运行时给出的数据，
//     和 curl … | sh 一样照常送审；同一命令里写了文件（重定向、tee、cp、touch 等）时不认，免得命令先造出
//     带空格的文件名再让通配拆开它。
//
// 变量的值可能在别处改掉时一律不认：命令里出现 IFS；同一名字别处有赋值、read、printf -v、${c:=…}、
// declare -n 之类出现这个名字的字面词，或另一个同名的 for 的列表不合格；有 eval、source，或名字、值动态的赋值类命令
// （declare "$n=…"、read "$n"）。只改循环体里的使用，循环之前的 $c 是外面传进来的值。
//
// levels 是已经按层解析出的全部语法树：改动在 collect 之后，各层的脚本文本已按原样取出。
func (f *findings) quoteLoopVars(command string, levels [][]layer) {
	if strings.Contains(command, "IFS") {
		return
	}
	var files []*syntax.File
	var loops []*syntax.ForClause
	for _, level := range levels {
		for _, l := range level {
			files = append(files, l.file)
			syntax.Walk(l.file, func(node syntax.Node) bool {
				if fc, ok := node.(*syntax.ForClause); ok && !fc.Select {
					if it, ok := fc.Loop.(*syntax.WordIter); ok && it.InPos.IsValid() && it.Name != nil {
						loops = append(loops, fc)
					}
				}
				return true
			})
		}
	}
	if len(loops) == 0 {
		return
	}
	names := map[string]bool{}
	bound := map[string]int{} // 各个名字由几个 for 赋值
	for _, fc := range loops {
		name := fc.Loop.(*syntax.WordIter).Name.Value
		names[name] = true
		bound[name]++
	}
	touched, unsafe, writes := f.loopVarHazards(files, names)
	if unsafe {
		return
	}
	// 同名的 for 可以有几个（先后两个循环都用 c），但要个个都只给出一个词的值：嵌套在里面的
	// for c in 'a b' 跑完后，外层循环体里的 $c 就是它留下的值
	single := map[string]bool{}
	for name := range names {
		single[name] = touched[name] == bound[name]
	}
	for _, fc := range loops {
		it := fc.Loop.(*syntax.WordIter)
		expands, ok := false, true
		for _, w := range it.Items {
			e, s := loopItemSingle(w)
			expands, ok = expands || e, ok && s
		}
		if !ok || expands && writes {
			single[it.Name.Value] = false
		}
	}
	for _, fc := range loops {
		name := fc.Loop.(*syntax.WordIter).Name.Value
		if !single[name] {
			continue
		}
		for _, stmt := range fc.Do {
			quoteUses(stmt, name)
		}
	}
}

// loopVarHazards 走一遍各层语法树：touched 是各个循环变量名作为字面词出现的次数（每个 for 自己的名字
// 算 1 次，赋值、read c、printf -v c、declare -n r=c、${c:=x} 等都会让它超过 1）；unsafe 表示有 eval、
// source，或名字、值动态的赋值类命令；writes 表示同一命令里写了文件。
func (f *findings) loopVarHazards(files []*syntax.File, names map[string]bool) (touched map[string]int, unsafe, writes bool) {
	touched = map[string]int{}
	writes = len(f.written) > 0
	params := map[*syntax.Lit]bool{}
	for _, file := range files {
		syntax.Walk(file, func(node syntax.Node) bool {
			switch n := node.(type) {
			case *syntax.ParamExp:
				if n.Param == nil {
					break
				}
				params[n.Param] = true // 先序：参数名的 Lit 随后才走到，不算出现
				if n.Exp != nil && (n.Exp.Op == syntax.AssignUnset || n.Exp.Op == syntax.AssignUnsetOrNull) {
					touched[n.Param.Value] += 2
				}
			case *syntax.Lit:
				if params[n] {
					break
				}
				for _, name := range litNames(n.Value) {
					if names[name] {
						touched[name]++
					}
				}
			case *syntax.CallExpr:
				launches := parseCommand(n.Args).launches
				// 第一个程序在当前 shell 里运行（包装、容器里运行的之后的程序改不了这里的变量）；
				// 命令名动态的可能就是 eval、read
				if len(launches) > 0 {
					l := launches[0]
					if l.dynamic || evaluators[l.name] || assigners[l.name] && anyDynamic(n.Args[l.at+1:]) {
						unsafe = true
					}
				}
				for _, l := range launches {
					if creators[path.Base(l.name)] {
						writes = true
					}
				}
				for _, arg := range n.Args {
					if v, ok := value(arg); ok && (outputOptions[v] || strings.HasPrefix(v, "--output")) {
						writes = true
					}
				}
			case *syntax.DeclClause:
				for _, a := range n.Args {
					if a.Naked && a.Value != nil {
						if _, ok := value(a.Value); !ok {
							unsafe = true
						}
					}
				}
			case *syntax.Stmt:
				for _, r := range n.Redirs {
					if writesFile(r) {
						writes = true
					}
				}
			}
			return true
		})
	}
	return touched, unsafe, writes
}

// litNames 是字面词 v 可能提到的变量名：就是它（read c、unset c、for c 的名字），给它赋值
// （c=…、c+=…、c[0]=…），或者引用它（declare -n r=c）。按 v 算出候选，不逐个比对循环变量名，
// 循环再多耗时也随长度线性增长。
func litNames(v string) []string {
	names := []string{v}
	if k := strings.IndexAny(v, "=+["); k > 0 {
		names = append(names, v[:k])
	}
	if k := strings.LastIndexByte(v, '='); k >= 0 {
		names = append(names, v[k+1:])
	}
	return names
}

// evaluators 把字符串当作当前 shell 的脚本运行，里面的赋值看不到。
var evaluators = set("eval", "source", ".")

// assigners 是按参数给变量赋值的内置命令：参数是动态的时，赋的是哪个变量看不出来。
var assigners = set("read", "mapfile", "readarray", "getopts", "let", "printf", "declare", "typeset", "local",
	"export", "readonly", "unset")

// creators 是会写文件的程序：recordWrites 只记得下写死的目标（tee a.sh），tee "$f" 这样目标动态的
// 记不下，这里只看程序，不看目标；带 -o、--output 等选项的同样算（见 loopVarHazards）。
var creators = set("tee", "cp", "mv", "install", "ln", "dd", "rsync", "touch", "mkdir", "mktemp", "mkfifo",
	"mknod", "tar", "unzip", "cpio")

func anyDynamic(words []*syntax.Word) bool {
	for _, w := range words {
		if _, ok := value(w); !ok {
			return true
		}
	}
	return false
}

// writesFile 判断重定向是否写文件：输出重定向的目标不是 /dev/null，也不是复制、关闭文件描述符
// （>&2、>&-）。目标动态的也算。
func writesFile(r *syntax.Redirect) bool {
	switch r.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut, syntax.DplOut:
		v, ok := value(r.Word)
		if !ok {
			return true
		}
		if r.Op == syntax.DplOut {
			return v != "-" && strings.Trim(v, "0123456789") != ""
		}
		return v != "/dev/null"
	}
	return false
}

// loopItemSingle 判断 for 列表里的一个词展开后每个值都没有空白：写死的值要非空、不含空白与通配；
// 不加引号的变量、命令替换、算术展开会按空白拆开（expands），引号里的部分要是写死的。
func loopItemSingle(w *syntax.Word) (expands, single bool) {
	if hasBraces(w) || hasGlob(w) {
		return false, false
	}
	if v, ok := value(w); ok {
		return false, v != "" && plainValue(v)
	}
	for _, part := range w.Parts {
		switch part.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp:
			expands = true
		default:
			v, ok := partValue(part)
			if !ok || !plainValue(v) {
				return false, false
			}
		}
	}
	return expands, true
}

func plainValue(v string) bool {
	return !strings.ContainsAny(v, " \t\n") && !pattern.HasMeta(v, 0)
}

// quoteUses 把 stmt 里不加引号的 $name、${name} 改成 "$name"。
func quoteUses(stmt *syntax.Stmt, name string) {
	syntax.Walk(stmt, func(node syntax.Node) bool {
		w, ok := node.(*syntax.Word)
		if !ok {
			return true
		}
		for i, part := range w.Parts {
			pe, ok := part.(*syntax.ParamExp)
			if !ok || !plainUse(pe, name) {
				continue
			}
			end := pe.End()
			w.Parts[i] = &syntax.DblQuoted{
				Left:  pe.Pos(),
				Right: syntax.NewPos(end.Offset()-1, end.Line(), end.Col()-1),
				Parts: []syntax.WordPart{pe},
			}
		}
		return true
	})
}

// plainUse 判断 pe 是不带任何操作的 $name / ${name}（${c/x/ y}、${c:-a b} 会改出空白，不算）。
func plainUse(pe *syntax.ParamExp, name string) bool {
	return pe.Param != nil && pe.Param.Value == name && !pe.Excl && !pe.Length && !pe.Width &&
		pe.Index == nil && pe.Slice == nil && pe.Repl == nil && pe.Names == 0 && pe.Exp == nil
}
