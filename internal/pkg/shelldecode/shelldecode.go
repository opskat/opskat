// Package shelldecode 把 shell 命令里「base64 解码后交给 shell 执行」的写法展开成解码出来的脚本，
// 让审核看到真正会执行的内容。纯算法，只依赖 shell 解析器（取词的值用 cmdline.ShellValue）。
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

// ErrUnparseable 表示命令解析不了，分不清哪些是解码执行、哪些是命令结构。
var ErrUnparseable = errors.New("command cannot be parsed")

const maxRevealDepth = 3

// Reveal 把 shell 里「解码后交给 shell 执行」的写法换成解码出来的脚本，其余部分原样保留；
// 没有这种写法时原样返回。解不开时返回 ErrUndecodable，命令解析不了时返回 ErrUnparseable。
func Reveal(command string) (string, error) {
	return revealDepth(command, 0)
}

func revealDepth(command string, depth int) (string, error) {
	if strings.TrimSpace(command) == "" {
		return command, nil
	}
	if depth > maxRevealDepth {
		return "", ErrUndecodable
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	writes := map[string]string{}
	var parts []string
	changed := false
	for _, stmt := range file.Stmts {
		text, c, err := rewriteStmt(command, stmt, writes, depth)
		if err != nil {
			return "", err
		}
		changed = changed || c
		if strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}
	if !changed {
		return command, nil
	}
	return revealDepth(strings.Join(parts, "\n"), depth+1)
}

func rewriteStmt(src string, stmt *syntax.Stmt, writes map[string]string, depth int) (string, bool, error) {
	if stmt == nil || stmt.Cmd == nil {
		return "", false, nil
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		if cmd.Op == syntax.Pipe || cmd.Op == syntax.PipeAll {
			return rewritePipeline(src, stmt, writes)
		}
		if cmd.Op == syntax.AndStmt {
			if paths := decodeWrites(cmd.X, writes); len(paths) > 0 {
				right, changed, err := rewriteStmt(src, cmd.Y, writes, depth)
				if err != nil {
					return "", false, err
				}
				if consumesWrite(cmd.Y, paths) {
					return right, true, nil
				}
				return joinNonEmpty(sliceStmt(src, cmd.X), right), changed, nil
			}
		}
		left, c1, err := rewriteStmt(src, cmd.X, writes, depth)
		if err != nil {
			return "", false, err
		}
		right, c2, err := rewriteStmt(src, cmd.Y, writes, depth)
		if err != nil {
			return "", false, err
		}
		return joinNonEmpty(left, right), c1 || c2, nil
	case *syntax.Subshell:
		return rewriteNested(src, stmt, cmd.Stmts, writes, depth)
	case *syntax.Block:
		return rewriteNested(src, stmt, cmd.Stmts, writes, depth)
	case *syntax.CallExpr:
		if base64Stage([]*syntax.Stmt{stmt}) == 0 {
			recordWrites(stmt, []*syntax.Stmt{stmt}, 0, writes)
		}
		return rewriteCall(src, stmt, cmd, writes, depth)
	default:
		if hasDecodedExecution(stmt) {
			return "", false, ErrUndecodable
		}
		return sliceStmt(src, stmt), false, nil
	}
}

func rewriteNested(src string, stmt *syntax.Stmt, stmts []*syntax.Stmt, writes map[string]string, depth int) (string, bool, error) {
	var parts []string
	changed := false
	for _, inner := range stmts {
		text, c, err := rewriteStmt(src, inner, writes, depth)
		if err != nil {
			return "", false, err
		}
		changed = changed || c
		if strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}
	if !changed {
		return sliceStmt(src, stmt), false, nil
	}
	return strings.Join(parts, "\n"), true, nil
}

func rewriteCall(src string, stmt *syntax.Stmt, call *syntax.CallExpr, writes map[string]string, depth int) (string, bool, error) {
	if script, ok, err := shellCommandArg(call, depth); err != nil || ok {
		return script, ok, err
	}
	if script, ok, err := procSubstExec(call); err != nil || ok {
		return script, ok, err
	}
	if script, ok := writtenScript(call, writes); ok {
		if script == "" {
			return "", false, ErrUndecodable
		}
		return script, true, nil
	}
	// 能执行代码的程序（shell、eval、解释器、代为执行命令的程序，或命令名本身是替换）
	// 带着上面展不开的解码：经重定向、进程替换或参数里的替换把解码结果交给它执行，看不清。
	if runsCode(call) && hasDecodedExecution(stmt) {
		return "", false, ErrUndecodable
	}
	return sliceStmt(src, stmt), false, nil
}

func rewritePipeline(src string, stmt *syntax.Stmt, writes map[string]string) (string, bool, error) {
	stages := pipelineStages(stmt)
	dec := base64Stage(stages)
	if dec < 0 {
		return sliceStmt(src, stmt), false, nil
	}
	reads, err := decodedSink(stages, dec)
	if err != nil {
		return "", false, err
	}
	if reads {
		script, err := decodeStage(stages, dec)
		if err != nil {
			return "", false, err
		}
		return script, true, nil
	}
	recordWrites(stmt, stages, dec, writes)
	return sliceStmt(src, stmt), false, nil
}

// decodedSink 判断第 dec 段解码出来的内容流向哪里：交给从标准输入读脚本的 shell（reads），
// 或者只经过只处理数据的程序（dataConsumers）。其余去向——不认识的程序、代为执行命令的
// 程序、子 shell 等复合命令——都可能执行它又看不清，返回 ErrUndecodable。
func decodedSink(stages []*syntax.Stmt, dec int) (reads bool, err error) {
	for _, later := range stages[dec+1:] {
		reads, unknown := readsDecodedStdin(later)
		switch {
		case reads:
			return true, nil
		case unknown || !consumesDataOnly(later):
			return false, ErrUndecodable
		}
	}
	return false, nil
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

// recordWrites 记下第 dec 段解码出来、写进了哪些文件，以及写进去的脚本；解不开的记为空，
// 之后执行这些文件时按看不清处理。
func recordWrites(stmt *syntax.Stmt, stages []*syntax.Stmt, dec int, writes map[string]string) []string {
	paths := decodedWritePaths(stmt, stages, dec)
	if len(paths) == 0 {
		return nil
	}
	script, err := decodeStage(stages, dec)
	if err != nil {
		script = ""
	}
	for _, p := range paths {
		writes[p] = script
	}
	return paths
}

// decodedWritePaths 是解码结果写进的文件：解码那一段及其后各段的输出重定向、base64 -o、
// tee 的文件参数。路径按 path.Clean 规范化，与执行时的写法对得上。
func decodedWritePaths(stmt *syntax.Stmt, stages []*syntax.Stmt, dec int) []string {
	var paths []string
	add := func(p string, ok bool) {
		if ok {
			paths = append(paths, path.Clean(p))
		}
	}
	add(base64OutputPath(stages[dec]))
	for _, stage := range stages[dec:] {
		add(outputPath(stage))
		paths = append(paths, teeFiles(stage)...)
	}
	if dec == len(stages)-1 {
		add(outputPath(stmt))
	}
	return paths
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

// teeFiles 是 tee 写入的文件。
func teeFiles(stmt *syntax.Stmt) []string {
	_, args, ok := callOf(stmt)
	if !ok || len(args) == 0 {
		return nil
	}
	if prog, lit := cmdline.ShellValue(args[0]); !lit || path.Base(prog) != "tee" {
		return nil
	}
	var files []string
	for _, arg := range args[1:] {
		if word, lit := cmdline.ShellValue(arg); lit && word != "" && !strings.HasPrefix(word, "-") {
			files = append(files, path.Clean(word))
		}
	}
	return files
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

// decodeWrites 在 stmt 是「解码后只写进文件」的管道时，记下写了哪些文件并返回它们。
func decodeWrites(stmt *syntax.Stmt, writes map[string]string) []string {
	stages := pipelineStages(stmt)
	dec := base64Stage(stages)
	if dec < 0 {
		return nil
	}
	if reads, err := decodedSink(stages, dec); reads || err != nil {
		return nil
	}
	return recordWrites(stmt, stages, dec, writes)
}

func outputPath(stmt *syntax.Stmt) (string, bool) {
	if stmt == nil {
		return "", false
	}
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.RdrOut, syntax.RdrClob, syntax.RdrAll:
			if r.Word == nil {
				return "", false
			}
			v, ok := cmdline.ShellValue(r.Word)
			if !ok || v == "" {
				return "", false
			}
			return v, true
		}
	}
	return "", false
}

// consumesWrite 判断 stmt 里有没有执行 paths 里的某个文件。
func consumesWrite(stmt *syntax.Stmt, paths []string) bool {
	if stmt == nil || stmt.Cmd == nil {
		return false
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		file, ok := executedFile(cmd)
		return ok && slices.Contains(paths, file)
	case *syntax.BinaryCmd:
		return consumesWrite(cmd.X, paths) || consumesWrite(cmd.Y, paths)
	case *syntax.Subshell:
		return slices.ContainsFunc(cmd.Stmts, func(inner *syntax.Stmt) bool { return consumesWrite(inner, paths) })
	case *syntax.Block:
		return slices.ContainsFunc(cmd.Stmts, func(inner *syntax.Stmt) bool { return consumesWrite(inner, paths) })
	}
	return false
}

// writtenScript 是 call 执行的、之前由解码写出来的文件里的脚本；解不开时脚本为空。
func writtenScript(call *syntax.CallExpr, writes map[string]string) (string, bool) {
	file, ok := executedFile(call)
	if !ok {
		return "", false
	}
	script, ok := writes[file]
	return script, ok
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

func shellCommandArg(call *syntax.CallExpr, depth int) (string, bool, error) {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return "", false, nil
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit {
		return "", false, nil
	}
	base := path.Base(prog)
	if wrappers[base] {
		rest, ok := skipWrapper(base, args[1:])
		if !ok || len(rest) == 0 {
			return "", false, nil
		}
		args = rest
		prog, lit = cmdline.ShellValue(args[0])
		if !lit {
			return "", false, nil
		}
		base = path.Base(prog)
	}
	switch {
	case base == "eval":
		return revealEval(args[1:], depth)
	case shells[base]:
		return revealShellC(args, depth)
	default:
		return "", false, nil
	}
}

// revealShellC 只展开 bash -c 里解码后执行的脚本。-c 的参数本身看不清、又不是这种写法时，
// 保持原命令，不把普通的 bash -c "$cmd" 当成审核失败。
func revealShellC(args []*syntax.Word, depth int) (string, bool, error) {
	kind, _, _ := shellInvocation(args[1:])
	if kind != invokeC {
		return "", false, nil
	}
	script, decoded, ok, err := cArgScript(args)
	if err != nil || !ok {
		return "", false, err
	}
	return finishReveal(script, decoded, depth)
}

func revealEval(args []*syntax.Word, depth int) (string, bool, error) {
	if len(args) == 1 {
		if text, ok := cmdline.ShellValue(args[0]); ok {
			return finishReveal(text, false, depth)
		}
		if inner, ok := pureSubst(args[0]); ok {
			script, err := decodedSubst(inner)
			if err != nil || script == "" {
				return "", false, err
			}
			return finishReveal(script, true, depth)
		}
		return "", false, nil
	}
	var b strings.Builder
	for i, arg := range args {
		text, ok := cmdline.ShellValue(arg)
		if !ok {
			return "", false, nil
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return finishReveal(b.String(), false, depth)
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

func finishReveal(script string, alreadyDecoded bool, depth int) (string, bool, error) {
	if !alreadyDecoded && !strings.Contains(script, "base64") {
		return "", false, nil
	}
	revealed, err := revealDepth(script, depth+1)
	if err != nil {
		if !alreadyDecoded && errors.Is(err, ErrUnparseable) {
			return "", false, nil
		}
		return "", false, err
	}
	if !alreadyDecoded && revealed == script {
		return "", false, nil
	}
	return revealed, true, nil
}

func isCommandFlag(word string) bool {
	return word == "-c" || (strings.HasPrefix(word, "-") && strings.Contains(word, "c") && !strings.HasPrefix(word, "--"))
}

func procSubstExec(call *syntax.CallExpr) (string, bool, error) {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return "", false, nil
	}
	prog, lit := cmdline.ShellValue(args[0])
	if !lit || !shells[path.Base(prog)] {
		return "", false, nil
	}
	for _, arg := range args[1:] {
		subst, ok := pureProcSubst(arg)
		if !ok {
			continue
		}
		script, err := decodeProducer(subst)
		if err != nil {
			return "", false, err
		}
		return script, true, nil
	}
	return "", false, nil
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

func sliceStmt(src string, stmt *syntax.Stmt) string {
	if stmt == nil {
		return ""
	}
	start, end := int(stmt.Pos().Offset()), int(stmt.End().Offset())
	if start < 0 || end > len(src) || start > end {
		return ""
	}
	return src[start:end]
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n")
}
