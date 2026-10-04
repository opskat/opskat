package command_review_svc

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"
)

// ErrUndecodable 表示命令会执行解码后的内容，但这部分解不开，不能发给模型。
var ErrUndecodable = errors.New("decoded execution cannot be read")

const maxRevealDepth = 3

// commandForReview 返回真正交给替换敏感信息和模型的命令。shell 里「解码后交给 shell 执行」
// 的写法换成解码出来的脚本；没有这种写法时原样返回。解不开时返回 ErrUndecodable，
// 命令解析不了时返回 ErrUnparseable。
func commandForReview(in Input) (string, error) {
	if in.Syntax != SyntaxShell {
		return in.Command, nil
	}
	return revealDecodedExec(in.Command)
}

// cacheKeyCommand 让解码展开过的命令不命中展开前的缓存。没展开时键和原来一样。
func cacheKeyCommand(original, reviewed string) string {
	if reviewed == original {
		return original
	}
	return original + "\x00" + reviewed
}

func revealDecodedExec(command string) (string, error) {
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
			if path, script, ok := decodeWrite(cmd.X); ok {
				writes[path] = script
				right, changed, err := rewriteStmt(src, cmd.Y, writes, depth)
				if err != nil {
					return "", false, err
				}
				if consumesWrite(cmd.Y, path) {
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
		return script, true, nil
	}
	return sliceStmt(src, stmt), false, nil
}

func rewritePipeline(src string, stmt *syntax.Stmt, writes map[string]string) (string, bool, error) {
	stages := pipelineStages(stmt)
	dec := base64Stage(stages)
	if dec < 0 {
		return sliceStmt(src, stmt), false, nil
	}
	for _, later := range stages[dec+1:] {
		switch reads, unknown := readsDecodedStdin(later); {
		case unknown:
			return "", false, ErrUndecodable
		case reads:
			script, err := decodeStage(stages, dec)
			if err != nil {
				return "", false, err
			}
			return script, true, nil
		}
	}
	if path, script, ok := writeTarget(stmt, stages, dec); ok {
		writes[path] = script
	}
	return sliceStmt(src, stmt), false, nil
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
	prog, lit := literalValue(args[0])
	if !lit || path.Base(prog) != "base64" {
		return false, false, false
	}
	decode := false
	for i := 1; i < len(args); i++ {
		word, lit := literalValue(args[i])
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
	prog, lit := literalValue(args[0])
	if !lit {
		return false, false
	}
	base := path.Base(prog)
	if wrappers[base] {
		rest, ok := skipWrapper(base, args[1:])
		if !ok || len(rest) == 0 {
			return false, true
		}
		prog, lit = literalValue(rest[0])
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
	prog, lit := literalValue(args[0])
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
		word, ok := literalValue(arg)
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
	format, ok := literalValue(args[0])
	if !ok || (format != "%s" && format != "%s\\n" && format != "%s\n") {
		return "", false
	}
	return literalValue(args[1])
}

func redirectPayload(stmt *syntax.Stmt) (string, bool, bool) {
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.WordHdoc:
			if r.Word == nil {
				return "", true, true
			}
			v, ok := literalValue(r.Word)
			return v, true, !ok
		case syntax.Hdoc, syntax.DashHdoc:
			if r.Hdoc == nil {
				return "", true, true
			}
			v, ok := literalValue(r.Hdoc)
			return v, true, !ok
		}
	}
	return "", false, false
}

func decodeWrite(stmt *syntax.Stmt) (string, string, bool) {
	stages := pipelineStages(stmt)
	dec := base64Stage(stages)
	if dec < 0 {
		return "", "", false
	}
	for _, later := range stages[dec+1:] {
		if reads, unknown := readsDecodedStdin(later); reads || unknown {
			return "", "", false
		}
	}
	path, script, ok := writeTarget(stmt, stages, dec)
	return path, script, ok
}

func writeTarget(stmt *syntax.Stmt, stages []*syntax.Stmt, dec int) (string, string, bool) {
	path, ok := outputPath(stages[dec])
	if !ok && dec == len(stages)-1 {
		path, ok = outputPath(stmt)
	}
	if !ok {
		return "", "", false
	}
	script, err := decodeStage(stages, dec)
	if err != nil {
		return "", "", false
	}
	return path, script, true
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
			v, ok := literalValue(r.Word)
			if !ok || v == "" {
				return "", false
			}
			return v, true
		}
	}
	return "", false
}

func consumesWrite(stmt *syntax.Stmt, path string) bool {
	if stmt == nil || stmt.Cmd == nil {
		return false
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		got, ok := writtenScript(cmd, map[string]string{path: ""})
		return ok && got == ""
	case *syntax.BinaryCmd:
		return consumesWrite(cmd.X, path) || consumesWrite(cmd.Y, path)
	case *syntax.Subshell:
		for _, inner := range cmd.Stmts {
			if consumesWrite(inner, path) {
				return true
			}
		}
	case *syntax.Block:
		for _, inner := range cmd.Stmts {
			if consumesWrite(inner, path) {
				return true
			}
		}
	}
	return false
}

func writtenScript(call *syntax.CallExpr, writes map[string]string) (string, bool) {
	file, ok := executedFile(call)
	if !ok {
		return "", false
	}
	script, ok := writes[file]
	return script, ok
}

func executedFile(call *syntax.CallExpr) (string, bool) {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return "", false
	}
	prog, lit := literalValue(args[0])
	if !lit {
		return "", false
	}
	base := path.Base(prog)
	if wrappers[base] {
		rest, ok := skipWrapper(base, args[1:])
		if !ok || len(rest) == 0 {
			return "", false
		}
		args = rest
		prog, lit = literalValue(args[0])
		if !lit {
			return "", false
		}
		base = path.Base(prog)
	}
	if base == "source" || base == "." {
		if len(args) < 2 {
			return "", false
		}
		return literalValue(args[1])
	}
	if !shells[base] {
		return "", false
	}
	kind, file, _ := shellInvocation(args[1:])
	if kind != invokeFile {
		return "", false
	}
	return file, true
}

const (
	invokeStdin = "stdin"
	invokeFile  = "file"
	invokeC     = "c"
	invokeNone  = "none"
)

func shellInvocation(args []*syntax.Word) (kind, file string, unknown bool) {
	var positionals []*syntax.Word
	for i := 0; i < len(args); i++ {
		word, ok := literalValue(args[i])
		if !ok {
			if _, isSubst := pureSubst(args[i]); isSubst && i > 0 {
				prev, _ := literalValue(args[i-1])
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
		case word == "-s":
		case strings.HasPrefix(word, "-") && strings.Contains(word, "c") && !strings.HasPrefix(word, "--"):
			if i+1 >= len(args) {
				return invokeNone, "", true
			}
			return invokeC, "", false
		case strings.HasPrefix(word, "-"):
		default:
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) == 0 {
		return invokeStdin, "", false
	}
	file, ok := literalValue(positionals[0])
	if !ok {
		return invokeNone, "", true
	}
	return invokeFile, file, false
}

func shellCommandArg(call *syntax.CallExpr, depth int) (string, bool, error) {
	_, args, ok := callOf(&syntax.Stmt{Cmd: call})
	if !ok || len(args) == 0 {
		return "", false, nil
	}
	prog, lit := literalValue(args[0])
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
		prog, lit = literalValue(args[0])
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
		if text, ok := literalValue(args[0]); ok {
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
		text, ok := literalValue(arg)
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
		word, lit := literalValue(args[i])
		if !lit || !isCommandFlag(word) {
			continue
		}
		if i+1 >= len(args) {
			return "", false, false, nil
		}
		next := args[i+1]
		if text, ok := literalValue(next); ok {
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
	prog, lit := literalValue(args[0])
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
	prog, lit := literalValue(args[0])
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
		word, ok := literalValue(args[0])
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
