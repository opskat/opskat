// Package shelldecode 找出 shell 命令里「base64 解码后交给 shell 执行」的写法，解出要执行的
// 脚本，让审核看到真正会执行的内容。命令本身不改写：调用方把解出的脚本追加在原命令之后。
// 纯算法，只依赖 shell 解析器（取词的值用 cmdline.ShellValue）。
package shelldecode

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// ErrUndecodable 表示命令会执行解码后的内容，但这部分解不开，不能当作已经看清。
var ErrUndecodable = errors.New("decoded execution cannot be read")

// ErrUnparseable 表示命令（或解码出的脚本）解析不了，分不清哪些是解码执行、哪些是命令结构。
var ErrUnparseable = errors.New("command cannot be parsed")

const maxDecodeDepth = 3

// Decode 返回 command 里解码后会交给 shell 执行的脚本，按出现顺序；解码出的脚本里还有这种
// 写法时，再解出的脚本跟在它后面。没有这种写法时返回 nil。
//
// 「写入文件再执行该文件」只在写入与执行相邻（中间至多隔着 chmod）时展开；中间切换了目录、
// 别的命令可能改写了这个文件，或执行处在另一个分支、后台、子 shell 里，都按解不开处理；
// 解码结果是追加进文件的，或写出的文件经重定向、管道、替换、代为执行的程序交给能执行代码
// 的命令，也按解不开处理。
// 解不开时返回 ErrUndecodable，命令或解码出的脚本解析不了时返回 ErrUnparseable。
func Decode(command string) ([]string, error) {
	return decodeText(command, 0)
}

func decodeText(command string, depth int) ([]string, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	return decodeFile(file, depth)
}

func decodeFile(file *syntax.File, depth int) ([]string, error) {
	if depth > maxDecodeDepth {
		return nil, ErrUndecodable
	}
	d := &decoder{depth: depth, writes: map[string]string{}}
	if err := d.list(file.Stmts); err != nil {
		return nil, err
	}
	return d.scripts, nil
}

// decoder 走一遍命令的语法树，收集解码后执行的脚本。
type decoder struct {
	depth int
	// writes 是解码结果写进的文件（path.Clean 后）→ 写进去的脚本；解不开的记为空。
	writes  map[string]string
	scripts []string
}

// decoded 记下一段解码出来、会被执行的脚本，再找它里面的解码执行。
func (d *decoder) decoded(script string) error {
	d.scripts = append(d.scripts, script)
	nested, err := decodeText(script, d.depth+1)
	if err != nil {
		return err
	}
	d.scripts = append(d.scripts, nested...)
	return nil
}

// literal 在原命令里字面写出的脚本（bash -c '…'、eval '…'）里找解码执行。脚本本身已经在
// 原命令里，不追加；解析不了的字面脚本也不是这种写法（shell 执行它同样报语法错），原样送审。
// 它里面把解码结果写进的文件记为解不开：之后在外面执行这些文件时看不清执行的是什么
// （它可能在里面切换过目录）。
func (d *decoder) literal(script string) error {
	if !strings.Contains(script, "base64") {
		return nil
	}
	file, parseErr := syntax.NewParser().Parse(strings.NewReader(script), "")
	if parseErr == nil {
		return d.literalFile(file)
	}
	return nil
}

// literalFile 是 literal 解析出的字面脚本里的解码执行与解码写出的文件。
func (d *decoder) literalFile(file *syntax.File) error {
	if d.depth+1 > maxDecodeDepth {
		return ErrUndecodable
	}
	nested := &decoder{depth: d.depth + 1, writes: map[string]string{}}
	if err := nested.list(file.Stmts); err != nil {
		return err
	}
	d.scripts = append(d.scripts, nested.scripts...)
	for p := range nested.writes {
		d.writes[p] = ""
	}
	return nil
}

// list 依次处理一串按顺序执行的语句。
func (d *decoder) list(stmts []*syntax.Stmt) error {
	var fresh []string
	for _, stmt := range stmts {
		var err error
		if fresh, err = d.stmt(stmt, fresh); err != nil {
			return err
		}
	}
	return nil
}

// isPlainChmod 判断 stmt 是不是一条参数都是字面值、没有重定向的 chmod。
func isPlainChmod(stmt *syntax.Stmt) bool {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 || len(call.Assigns) > 0 || len(stmt.Redirs) > 0 {
		return false
	}
	for _, arg := range call.Args {
		if _, lit := cmdline.ShellValue(arg); !lit {
			return false
		}
	}
	prog, _ := cmdline.ShellValue(call.Args[0])
	return prog == "chmod"
}

// stmt 处理一条语句。fresh 是紧挨着它之前、刚由解码写好的文件；返回的是紧挨着它之后的语句
// 能当作刚写好执行的文件：它自己把解码结果写进的文件，它是 chmod 时沿用 fresh（改权限不改
// 内容、不切换目录），其余都没有。放到后台或协程的语句不算写好：下一条开始时它可能还没写完。
func (d *decoder) stmt(stmt *syntax.Stmt, fresh []string) ([]string, error) {
	if stmt == nil || stmt.Cmd == nil {
		return nil, nil
	}
	after, err := d.cmd(stmt, fresh)
	if err != nil || stmt.Background || stmt.Coprocess {
		return nil, err
	}
	return after, nil
}

func (d *decoder) cmd(stmt *syntax.Stmt, fresh []string) ([]string, error) {
	switch cmd := stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		switch cmd.Op {
		case syntax.Pipe, syntax.PipeAll:
			return d.pipeline(stmt, fresh)
		case syntax.AndStmt:
			between, err := d.stmt(cmd.X, fresh)
			if err != nil {
				return nil, err
			}
			return d.stmt(cmd.Y, between)
		default:
			// a || b：b 只在 a 失败时执行，a 写没写好说不清，两边都不算紧挨着的写入。
			if _, err := d.stmt(cmd.X, nil); err != nil {
				return nil, err
			}
			_, err := d.stmt(cmd.Y, nil)
			return nil, err
		}
	case *syntax.Subshell:
		return nil, d.list(cmd.Stmts)
	case *syntax.Block:
		return nil, d.list(cmd.Stmts)
	case *syntax.CallExpr:
		var written []string
		if base64Stage([]*syntax.Stmt{stmt}) == 0 {
			written = d.recordWrites(stmt, []*syntax.Stmt{stmt}, 0)
		}
		if err := d.call(stmt, cmd, fresh); err != nil {
			return nil, err
		}
		switch {
		case len(written) > 0:
			return written, nil
		case isPlainChmod(stmt):
			return fresh, nil
		}
		return nil, nil
	default:
		// if / while / for 等没展开的语法：里面有解码，或执行了解码写出的文件，都看不清。
		if hasDecodedExecution(stmt) || d.runsWrittenFile(stmt) {
			return nil, ErrUndecodable
		}
		return nil, nil
	}
}

// call 处理一条简单命令：bash -c / eval 的脚本、进程替换、执行解码写出的文件。
func (d *decoder) call(stmt *syntax.Stmt, call *syntax.CallExpr, fresh []string) error {
	if handled, err := d.shellCommandArg(call); err != nil || handled {
		return err
	}
	if handled, err := d.procSubstExec(call); err != nil || handled {
		return err
	}
	if file, ok := executedFile(call); ok {
		if script, written := d.writes[file]; written {
			if script == "" || !slices.Contains(fresh, file) {
				return ErrUndecodable
			}
			return d.decoded(script)
		}
		if d.mayBeWritten(file) {
			return ErrUndecodable
		}
	}
	// 能执行代码的程序（shell、eval、解释器、代为执行命令的程序，或命令名本身是替换）
	// 带着上面展不开的解码，或提到解码写出的文件：经重定向、进程替换、参数里的替换或代为
	// 执行把解码结果交给它执行，看不清。
	if runsCode(call) && (hasDecodedExecution(stmt) || d.namesWritten(stmt)) {
		return ErrUndecodable
	}
	return nil
}

// namesWritten 判断 node 里有没有哪个词（参数、重定向目标、替换里的词）是解码写出的文件
// （或可能是它的文件）。
func (d *decoder) namesWritten(node syntax.Node) bool {
	if len(d.writes) == 0 {
		return false
	}
	found := false
	syntax.Walk(node, func(n syntax.Node) bool {
		if found {
			return false
		}
		if w, ok := n.(*syntax.Word); ok {
			if v, lit := cmdline.ShellValue(w); lit && v != "" {
				file := path.Clean(v)
				_, written := d.writes[file]
				found = written || d.mayBeWritten(file)
			}
		}
		return !found
	})
	return found
}

// mayRunCode 判断管道里的这一段会不会把读到的内容当代码执行：能执行代码的简单命令，
// 或任何复合命令（里面可能有）。
func mayRunCode(stmt *syntax.Stmt) bool {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	return !ok || runsCode(call)
}

// mayBeWritten 判断 file 会不会是解码写出的某个文件换了一种写法：文件名相同、又有一边是
// 相对路径（切换目录后可能是同一个文件）。
func (d *decoder) mayBeWritten(file string) bool {
	for written := range d.writes {
		if path.Base(written) == path.Base(file) && (!path.IsAbs(written) || !path.IsAbs(file)) {
			return true
		}
	}
	return false
}

// runsWrittenFile 判断 stmt 里有没有哪条命令执行了解码写出的文件（或可能是它的文件）：
// 直接执行它，或者提到它、里面又有能执行代码的命令。
func (d *decoder) runsWrittenFile(stmt *syntax.Stmt) bool {
	found, runner := false, false
	syntax.Walk(stmt, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || found {
			return !found
		}
		runner = runner || runsCode(call)
		if file, ok := executedFile(call); ok {
			_, written := d.writes[file]
			found = written || d.mayBeWritten(file)
		}
		return !found
	})
	return found || (runner && d.namesWritten(stmt))
}

// pipeline 处理管道：解码结果交给读标准输入的 shell 时追加脚本；只写进文件时记下这些文件
// 并返回它们。其余各段照常检查（解码那一段除外）。
func (d *decoder) pipeline(stmt *syntax.Stmt, fresh []string) ([]string, error) {
	stages := pipelineStages(stmt)
	dec := base64Stage(stages)
	var written []string
	if dec >= 0 {
		reads, err := decodedSink(stages, dec)
		if err != nil {
			return nil, err
		}
		if reads {
			script, err := decodeStage(stages, dec)
			if err != nil {
				return nil, err
			}
			if err := d.decoded(script); err != nil {
				return nil, err
			}
		} else {
			written = d.recordWrites(stmt, stages, dec)
		}
	}
	for i, stage := range stages {
		if i == dec {
			continue
		}
		// 读出解码写出的文件、再经管道交给能执行代码的一段（cat a.sh | sh）：看不清。
		if d.namesWritten(stage) && slices.ContainsFunc(stages[i+1:], mayRunCode) {
			return nil, ErrUndecodable
		}
		if _, err := d.stmt(stage, fresh); err != nil {
			return nil, err
		}
	}
	return written, nil
}

// decodedSink 判断第 dec 段解码出来的内容流向哪里：交给从标准输入读脚本的 shell（reads），
// 或者只经过只处理数据的程序（dataConsumers）。其余去向——不认识的程序、代为执行命令的
// 程序、子 shell 等复合命令——都可能执行它又看不清，返回 ErrUndecodable。交给 shell 之前
// 经过了会改写内容的程序（再解一次码、tr、解压等）时，shell 执行的不是解出来的脚本，同样看不清。
func decodedSink(stages []*syntax.Stmt, dec int) (reads bool, err error) {
	unchanged := true
	for _, later := range stages[dec+1:] {
		reads, unknown := readsDecodedStdin(later)
		switch {
		case reads && unchanged:
			return true, nil
		case reads, unknown || !consumesDataOnly(later):
			return false, ErrUndecodable
		}
		unchanged = unchanged && passesThrough(later)
	}
	return false, nil
}

// passesThrough 判断这一段把标准输入原样传下去：tee，或只读标准输入（不带参数或只有 - / -u）的 cat。
func passesThrough(stmt *syntax.Stmt) bool {
	_, args, ok := callOf(stmt)
	if !ok || len(args) == 0 {
		return false
	}
	prog, _ := cmdline.ShellValue(args[0])
	switch path.Base(prog) {
	case "tee":
		return true
	case "cat":
		for _, arg := range args[1:] {
			if word, lit := cmdline.ShellValue(arg); !lit || (word != "-" && word != "-u") {
				return false
			}
		}
		return true
	}
	return false
}

// dataConsumers 只读、转换或保存数据，不把输入当代码执行。
var dataConsumers = map[string]bool{
	"cat": true, "tee": true, "jq": true, "yq": true, "grep": true, "egrep": true, "fgrep": true, "rg": true,
	"head": true, "tail": true, "less": true, "more": true, "wc": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "column": true, "fold": true, "xxd": true, "hexdump": true, "od": true,
	"strings": true, "file": true, "openssl": true, "base64": true, "base32": true, "iconv": true,
	"gzip": true, "gunzip": true, "zcat": true, "xz": true, "unxz": true, "zstd": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true, "cksum": true,
}

func consumesDataOnly(stmt *syntax.Stmt) bool {
	_, args, ok := callOf(stmt)
	if !ok || len(args) == 0 {
		return false
	}
	prog, lit := cmdline.ShellValue(args[0])
	return lit && dataConsumers[path.Base(prog)]
}

// executors 是会把参数、输入或文件当命令执行的程序（shell 之外）。
var executors = map[string]bool{
	"eval": true, "source": true, ".": true, "exec": true, "xargs": true, "timeout": true, "stdbuf": true,
	"setsid": true, "doas": true, "su": true, "busybox": true, "watch": true, "flock": true, "chroot": true,
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true, "php": true,
}

// runsCode 判断这条命令会不会执行代码：命令名是替换、shell、executors 里的程序，或是没法
// 跳过的 wrapper。
func runsCode(call *syntax.CallExpr) bool {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return false
	}
	prog, lit := cmdline.ShellValue(args[0])
	base := path.Base(prog)
	return !lit || shells[base] || executors[base] || wrappers[base]
}

// decodedWritePaths 是解码结果写进的文件：解码那一段及其后各段的输出重定向、base64 -o、
// tee 的文件参数。路径按 path.Clean 规范化，与执行时的写法对得上。appends 表示其中有追加
// 写入（>>、tee -a）：文件里还有原来的内容，不只是解出来的脚本。
func decodedWritePaths(stmt *syntax.Stmt, stages []*syntax.Stmt, dec int) (paths []string, appends bool) {
	add := func(p string, appended, ok bool) {
		if ok {
			paths = append(paths, path.Clean(p))
			appends = appends || appended
		}
	}
	p, ok := base64OutputPath(stages[dec])
	add(p, false, ok)
	for _, stage := range stages[dec:] {
		add(outputPath(stage))
		files, appended := teeFiles(stage)
		paths = append(paths, files...)
		appends = appends || (appended && len(files) > 0)
	}
	if dec == len(stages)-1 {
		add(outputPath(stmt))
	}
	return paths, appends
}

// base64OutputPath 是 base64 -o / --output 写入的文件。
func base64OutputPath(stmt *syntax.Stmt) (string, bool) {
	_, args, ok := callOf(stmt)
	if !ok {
		return "", false
	}
	for i := 1; i < len(args); i++ {
		word, lit := cmdline.ShellValue(args[i])
		if !lit {
			continue
		}
		if (word == "-o" || word == "--output") && i+1 < len(args) {
			return cmdline.ShellValue(args[i+1])
		}
		if v, found := strings.CutPrefix(word, "--output="); found {
			return v, true
		}
	}
	return "", false
}

// teeFiles 是 tee 写入的文件，appends 表示 tee 是追加写入（-a / --append）。
func teeFiles(stmt *syntax.Stmt) (files []string, appends bool) {
	_, args, ok := callOf(stmt)
	if !ok || len(args) == 0 {
		return nil, false
	}
	if prog, lit := cmdline.ShellValue(args[0]); !lit || path.Base(prog) != "tee" {
		return nil, false
	}
	for _, arg := range args[1:] {
		word, lit := cmdline.ShellValue(arg)
		switch {
		case !lit || word == "":
		case word == "--append":
			appends = true
		case strings.HasPrefix(word, "--"):
		case strings.HasPrefix(word, "-"):
			appends = appends || strings.Contains(word, "a")
		default:
			files = append(files, path.Clean(word))
		}
	}
	return files, appends
}

func pipelineStages(stmt *syntax.Stmt) []*syntax.Stmt {
	var stages []*syntax.Stmt
	var walk func(*syntax.Stmt)
	walk = func(s *syntax.Stmt) {
		if s == nil {
			return
		}
		bin, ok := s.Cmd.(*syntax.BinaryCmd)
		if ok && (bin.Op == syntax.Pipe || bin.Op == syntax.PipeAll) {
			walk(bin.X)
			stages = append(stages, bin.Y)
			return
		}
		stages = append(stages, s)
	}
	walk(stmt)
	return stages
}

func base64Stage(stages []*syntax.Stmt) int {
	for i, stage := range stages {
		if _, _, ok := base64Decode(stage); ok {
			return i
		}
	}
	return -1
}

// base64Decode 报告这一段是不是 base64 解码，以及它是不是在读一个文件而不是管道。
func base64Decode(stmt *syntax.Stmt) (inputFile, ambiguous bool, ok bool) {
	_, args, ok := callOf(stmt)
	if !ok || len(args) == 0 {
		return false, false, false
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit || path.Base(prog) != "base64" {
		return false, false, false
	}
	decode := false
	for i := 1; i < len(args); i++ {
		word, lit := cmdline.ShellValue(args[i])
		if !lit {
			return false, true, true
		}
		switch {
		case word == "--":
			if i+1 < len(args) {
				inputFile = true
			}
			i = len(args)
		case word == "-d" || word == "--decode" || word == "-D":
			decode = true
		case word == "-i" || word == "--ignore-garbage":
		case word == "-w" || word == "--wrap" || word == "-b" || word == "--break" || word == "-o" || word == "--output":
			if word == "-o" || word == "--output" {
				ambiguous = true
			}
			if i+1 < len(args) {
				i++
			}
		case strings.HasPrefix(word, "--"):
			ambiguous = true
		case strings.HasPrefix(word, "-"):
			for _, r := range word[1:] {
				switch r {
				case 'd', 'D':
					decode = true
				case 'i':
				default:
					ambiguous = true
				}
			}
		default:
			inputFile = true
		}
	}
	return inputFile, ambiguous, decode
}

func readsDecodedStdin(stmt *syntax.Stmt) (reads, unknown bool) {
	_, args, ok := callOf(stmt)
	if !ok || len(args) == 0 {
		return false, false
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit {
		return false, false
	}
	base := path.Base(prog)
	if wrappers[base] {
		rest, ok := skipWrapper(base, args[1:])
		if !ok || len(rest) == 0 {
			return false, true
		}
		prog, lit = cmdline.ShellValue(rest[0])
		if !lit || !shells[path.Base(prog)] {
			return false, false
		}
		args = rest
		base = path.Base(prog)
	}
	if !shells[base] && base != "eval" {
		return false, false
	}
	kind, _, unknown := shellInvocation(args[1:])
	return kind == invokeStdin, unknown
}

func decodeStage(stages []*syntax.Stmt, dec int) (string, error) {
	inputFile, ambiguous, _ := base64Decode(stages[dec])
	if ambiguous || inputFile {
		return "", ErrUndecodable
	}
	if raw, found, bad := redirectPayload(stages[dec]); found {
		if bad {
			return "", ErrUndecodable
		}
		return decodePayload(raw)
	}
	if dec == 0 {
		return "", ErrUndecodable
	}
	raw, ok := producerPayload(stages[dec-1])
	if !ok {
		return "", ErrUndecodable
	}
	return decodePayload(raw)
}

func producerPayload(stmt *syntax.Stmt) (string, bool) {
	_, args, ok := callOf(stmt)
	if !ok || len(args) == 0 {
		return "", false
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit {
		return "", false
	}
	switch path.Base(prog) {
	case "echo":
		return echoPayload(args[1:])
	case "printf":
		return printfPayload(args[1:])
	default:
		return "", false
	}
}

func echoPayload(args []*syntax.Word) (string, bool) {
	var parts []string
	for _, arg := range args {
		word, ok := cmdline.ShellValue(arg)
		if !ok {
			return "", false
		}
		if word == "-n" || word == "-E" || word == "-nE" || word == "-En" {
			continue
		}
		if strings.HasPrefix(word, "-") {
			return "", false
		}
		parts = append(parts, word)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " "), true
}

func printfPayload(args []*syntax.Word) (string, bool) {
	if len(args) != 2 {
		return "", false
	}
	format, ok := cmdline.ShellValue(args[0])
	if !ok || (format != "%s" && format != "%s\\n" && format != "%s\n") {
		return "", false
	}
	return cmdline.ShellValue(args[1])
}

func redirectPayload(stmt *syntax.Stmt) (string, bool, bool) {
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.WordHdoc:
			if r.Word == nil {
				return "", true, true
			}
			v, ok := cmdline.ShellValue(r.Word)
			return v, true, !ok
		case syntax.Hdoc, syntax.DashHdoc:
			if r.Hdoc == nil {
				return "", true, true
			}
			v, ok := cmdline.ShellValue(r.Hdoc)
			return v, true, !ok
		}
	}
	return "", false, false
}

// outputPath 是 stmt 的输出重定向写入的文件，appends 表示是追加写入（>>、&>>）。
func outputPath(stmt *syntax.Stmt) (file string, appends, ok bool) {
	if stmt == nil {
		return "", false, false
	}
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.RdrOut, syntax.RdrClob, syntax.RdrAll, syntax.AppOut, syntax.AppAll:
			if r.Word == nil {
				return "", false, false
			}
			v, ok := cmdline.ShellValue(r.Word)
			if !ok || v == "" {
				return "", false, false
			}
			return v, r.Op == syntax.AppOut || r.Op == syntax.AppAll, true
		}
	}
	return "", false, false
}

// executedFile 是 call 当脚本执行的文件（source / . / shell 的脚本参数），或直接按路径执行的
// 程序本身；路径按 path.Clean 规范化。
func executedFile(call *syntax.CallExpr) (string, bool) {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return "", false
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit {
		return "", false
	}
	base := path.Base(prog)
	switch {
	case base == "source" || base == ".":
		if len(args) < 2 {
			return "", false
		}
		file, ok := cmdline.ShellValue(args[1])
		return path.Clean(file), ok
	case shells[base]:
		kind, file, _ := shellInvocation(args[1:])
		if kind != invokeFile {
			return "", false
		}
		return path.Clean(file), true
	case strings.Contains(prog, "/"):
		return path.Clean(prog), true
	}
	return "", false
}

const (
	invokeStdin = "stdin"
	invokeFile  = "file"
	invokeC     = "c"
	invokeNone  = "none"
)

// shellOptionsWithValue 是后面跟一个值的 shell 选项：值不是脚本文件。
var shellOptionsWithValue = map[string]bool{"-o": true, "+o": true, "-O": true, "+O": true, "--rcfile": true, "--init-file": true}

func shellInvocation(args []*syntax.Word) (kind, file string, unknown bool) {
	var positionals []*syntax.Word
	for i := 0; i < len(args); i++ {
		word, ok := cmdline.ShellValue(args[i])
		if !ok {
			if _, isSubst := pureSubst(args[i]); isSubst && i > 0 {
				prev, _ := cmdline.ShellValue(args[i-1])
				if prev == "-c" || strings.HasSuffix(prev, "c") {
					return invokeC, "", false
				}
			}
			return invokeNone, "", true
		}
		switch {
		case word == "--":
			positionals = append(positionals, args[i+1:]...)
			i = len(args)
		case word == "-c":
			if i+1 >= len(args) {
				return invokeNone, "", true
			}
			return invokeC, "", false
		case shellOptionsWithValue[word]:
			i++
		case word == "-s":
		case strings.HasPrefix(word, "-") && strings.Contains(word, "c") && !strings.HasPrefix(word, "--"):
			if i+1 >= len(args) {
				return invokeNone, "", true
			}
			return invokeC, "", false
		case strings.HasPrefix(word, "-"), strings.HasPrefix(word, "+"):
		default:
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) == 0 {
		return invokeStdin, "", false
	}
	file, ok := cmdline.ShellValue(positionals[0])
	if !ok {
		return invokeNone, "", true
	}
	if file == "/dev/stdin" || file == "-" {
		return invokeStdin, "", false
	}
	return invokeFile, file, false
}

// recordWrites 记下第 dec 段解码出来、写进了哪些文件，以及写进去的脚本；解不开或追加写入
// （文件里还有原来的内容）的记为空，之后执行这些文件时按看不清处理。返回这些文件。
func (d *decoder) recordWrites(stmt *syntax.Stmt, stages []*syntax.Stmt, dec int) []string {
	paths, appends := decodedWritePaths(stmt, stages, dec)
	if len(paths) == 0 {
		return nil
	}
	script, err := decodeStage(stages, dec)
	if err != nil || appends {
		script = ""
	}
	for _, p := range paths {
		d.writes[p] = script
	}
	return paths
}

// shellCommandArg 处理 eval 与 shell -c 的脚本（可以裹着 sudo / env 等）。handled 为真表示
// 脚本本身就是解码出来的，已经追加；字面写出的脚本只在里面找解码执行，留给 call 照常检查。
func (d *decoder) shellCommandArg(call *syntax.CallExpr) (handled bool, err error) {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return false, nil
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit {
		return false, nil
	}
	base := path.Base(prog)
	if wrappers[base] {
		rest, ok := skipWrapper(base, args[1:])
		if !ok || len(rest) == 0 {
			return false, nil
		}
		args = rest
		prog, lit = cmdline.ShellValue(args[0])
		if !lit {
			return false, nil
		}
		base = path.Base(prog)
	}
	switch {
	case base == "eval":
		return d.eval(args[1:])
	case shells[base]:
		return d.shellC(args)
	default:
		return false, nil
	}
}

// shellC 只处理 bash -c 里解码后执行的脚本。-c 的参数本身看不清、又不是这种写法时，
// 保持原命令，不把普通的 bash -c "$cmd" 当成审核失败。
func (d *decoder) shellC(args []*syntax.Word) (bool, error) {
	kind, _, _ := shellInvocation(args[1:])
	if kind != invokeC {
		return false, nil
	}
	script, decoded, ok, err := cArgScript(args)
	if err != nil || !ok {
		return false, err
	}
	if decoded {
		return true, d.decoded(script)
	}
	return false, d.literal(script)
}

func (d *decoder) eval(args []*syntax.Word) (bool, error) {
	if len(args) == 1 {
		if text, ok := cmdline.ShellValue(args[0]); ok {
			return false, d.literal(text)
		}
		if inner, ok := pureSubst(args[0]); ok {
			script, err := decodedSubst(inner)
			if err != nil || script == "" {
				return false, err
			}
			return true, d.decoded(script)
		}
		return false, nil
	}
	var b strings.Builder
	for i, arg := range args {
		text, ok := cmdline.ShellValue(arg)
		if !ok {
			return false, nil
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return false, d.literal(b.String())
}

// procSubstExec 处理 bash <(… | base64 -d)：shell 执行进程替换里解码出的脚本。
func (d *decoder) procSubstExec(call *syntax.CallExpr) (bool, error) {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return false, nil
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit || !shells[path.Base(prog)] {
		return false, nil
	}
	for _, arg := range args[1:] {
		subst, ok := pureProcSubst(arg)
		if !ok {
			continue
		}
		script, err := decodeProducer(subst)
		if err != nil {
			return false, err
		}
		return true, d.decoded(script)
	}
	return false, nil
}

func cArgScript(args []*syntax.Word) (script string, decoded, ok bool, err error) {
	for i := 0; i < len(args); i++ {
		word, lit := cmdline.ShellValue(args[i])
		if !lit || !isCommandFlag(word) {
			continue
		}
		if i+1 >= len(args) {
			return "", false, false, nil
		}
		next := args[i+1]
		if text, ok := cmdline.ShellValue(next); ok {
			return text, false, true, nil
		}
		if inner, ok := pureSubst(next); ok {
			script, err := decodedSubst(inner)
			return script, true, err == nil && script != "", err
		}
		return "", false, false, nil
	}
	return "", false, false, nil
}

// decodedSubst 在命令替换本身就是 base64 解码时返回脚本。不是这种写法时返回空字符串，不是错误。
func decodedSubst(stmt *syntax.Stmt) (string, error) {
	if base64Stage(pipelineStages(stmt)) < 0 {
		return "", nil
	}
	return decodeProducer(stmt)
}

func isCommandFlag(word string) bool {
	return word == "-c" || (strings.HasPrefix(word, "-") && strings.Contains(word, "c") && !strings.HasPrefix(word, "--"))
}

func decodeProducer(stmt *syntax.Stmt) (string, error) {
	stages := pipelineStages(stmt)
	dec := base64Stage(stages)
	if dec < 0 {
		return "", ErrUndecodable
	}
	return decodeStage(stages, dec)
}

func pureSubst(w *syntax.Word) (*syntax.Stmt, bool) {
	if w == nil || len(w.Parts) != 1 {
		return nil, false
	}
	switch p := w.Parts[0].(type) {
	case *syntax.CmdSubst:
		if len(p.Stmts) == 1 {
			return p.Stmts[0], true
		}
	case *syntax.DblQuoted:
		if len(p.Parts) == 1 {
			if subst, ok := p.Parts[0].(*syntax.CmdSubst); ok && len(subst.Stmts) == 1 {
				return subst.Stmts[0], true
			}
		}
	}
	return nil, false
}

func pureProcSubst(w *syntax.Word) (*syntax.Stmt, bool) {
	if w == nil || len(w.Parts) != 1 {
		return nil, false
	}
	subst, ok := w.Parts[0].(*syntax.ProcSubst)
	if !ok || len(subst.Stmts) != 1 {
		return nil, false
	}
	return subst.Stmts[0], true
}

// hasDecodedExecution 判断这段没展开的语法（if / while / for 等）里有没有 base64 解码。
// 解不开或展不开就失败，不能让它裹在条件里绕过上面的展开。
func hasDecodedExecution(stmt *syntax.Stmt) bool {
	found := false
	syntax.Walk(stmt, func(n syntax.Node) bool {
		s, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		if base64Stage(pipelineStages(s)) >= 0 {
			found = true
			return false
		}
		return true
	})
	return found
}

func callOf(stmt *syntax.Stmt) (*syntax.CallExpr, []*syntax.Word, bool) {
	if stmt == nil {
		return nil, nil, false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return nil, nil, false
	}
	args := call.Args
	prog, lit := cmdline.ShellValue(args[0])
	if lit && wrappers[path.Base(prog)] {
		rest, ok := skipWrapper(path.Base(prog), args[1:])
		if !ok || len(rest) == 0 {
			return call, call.Args, true
		}
		args = rest
	}
	return call, args, true
}

func skipWrapper(wrapper string, args []*syntax.Word) ([]*syntax.Word, bool) {
	for len(args) > 0 {
		word, ok := cmdline.ShellValue(args[0])
		if !ok {
			return nil, false
		}
		if word == "--" {
			return args[1:], true
		}
		if !strings.HasPrefix(word, "-") {
			return args, true
		}
		if wrapper == "command" && (word == "-v" || word == "-V") {
			return nil, false
		}
		if wrapperTakesValue(wrapper, word) {
			if len(args) < 2 {
				return nil, false
			}
			args = args[2:]
			continue
		}
		args = args[1:]
	}
	return args, true
}

func wrapperTakesValue(wrapper, flag string) bool {
	switch wrapper {
	case "sudo":
		switch flag {
		case "-u", "-g", "-h", "-p", "-C", "-T", "-U", "-D", "--user", "--group", "--host", "--prompt", "--close-from", "--type", "--other-user", "--chdir", "--role", "--command-timeout":
			return true
		}
	case "env":
		switch flag {
		case "-u", "-C", "-S", "--unset", "--chdir", "--split-string":
			return true
		}
	case "nice":
		return flag == "-n" || flag == "--adjustment"
	}
	return false
}

var shells = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "ash": true,
}

var wrappers = map[string]bool{
	"sudo": true, "env": true, "command": true, "nohup": true, "nice": true, "time": true,
}

func decodePayload(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, raw)
	if cleaned == "" {
		return "", ErrUndecodable
	}
	if m := len(cleaned) % 4; m != 0 {
		cleaned += strings.Repeat("=", 4-m)
	}
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil || len(decoded) == 0 || bytes.Contains(decoded, []byte{0}) || !utf8.Valid(decoded) {
		return "", ErrUndecodable
	}
	return strings.TrimRight(string(decoded), "\n"), nil
}
