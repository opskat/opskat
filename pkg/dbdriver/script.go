package dbdriver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// ScriptRequest 是 Session.RunScript 要运行的一个 SQL 文件。
type ScriptRequest struct {
	// Database 是语句执行的库，含义同 Session 的其他操作。
	Database string
	// Script 是文件内容。它会被读两遍：先检查整个文件是 UTF-8，再回到开头边读边执行。
	Script io.ReadSeeker
	// Size 是文件的字节数，只用于进度。
	Size int64
	// ContinueOnError 为真时一条语句失败后继续执行后面的；为假时停在第一条失败的语句。
	ContinueOnError bool
	// Stop 被关闭后，正在执行的一批语句执行完就停下（结果标为 Stopped）。nil 表示不会被要求停下。
	Stop <-chan struct{}
	// Progress 在每批语句执行完后被调用（同一个 goroutine 上，按先后）；nil 表示不需要进度。
	Progress func(ScriptProgress)
	// ReadSize 是每次从文件读多少字节；0 表示 DefaultScriptReadSize。
	ReadSize int
}

// ScriptProgress 是运行到目前为止的计数。
type ScriptProgress struct {
	// Succeeded / Failed 是执行成功 / 失败的语句数。
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	// BytesRead 是已经从文件读出的字节数（含开头的 BOM），TotalBytes 是 ScriptRequest.Size。
	BytesRead  int64 `json:"bytesRead"`
	TotalBytes int64 `json:"totalBytes"`
}

// ScriptResult 是一次运行的结果。
type ScriptResult struct {
	ScriptProgress
	// Failure 是运行停在的那条失败语句（不继续时才有）。
	Failure *ScriptFailure `json:"failure,omitempty"`
	// Stopped 表示运行因为 ScriptRequest.Stop 在文件结束前停下。
	Stopped bool `json:"stopped"`
}

// ScriptFailure 是一条失败的语句。
type ScriptFailure struct {
	// Number 是它在文件里是第几条语句，从 1 起。
	Number int64 `json:"number"`
	// Line 是它在文件里开始的行，从 1 起。
	Line int `json:"line"`
	// Error 是数据库返回的错误原文。
	Error string `json:"error"`
	// Excerpt 是语句的开头，至多 ScriptExcerptRunes 个字符（截断时末尾加「…」）。
	Excerpt string `json:"excerpt"`
}

// ScriptEncodingError 表示文件不是 UTF-8：运行在执行任何语句之前就拒绝了它。
type ScriptEncodingError struct {
	// Line 是第一处非 UTF-8 内容所在的行，从 1 起。
	Line int
}

func (e *ScriptEncodingError) Error() string {
	return fmt.Sprintf("script is not UTF-8 encoded (first invalid content on line %d)", e.Line)
}

// ScriptExcerptRunes 是 ScriptFailure.Excerpt 至多保留的字符数。
const ScriptExcerptRunes = 200

// DefaultScriptReadSize 是 ScriptRequest.ReadSize 为 0 时每次读的字节数。
const DefaultScriptReadSize = 1 << 20

// scriptBatchStatements / scriptBatchBytes 是一批交给执行器的语句数与语句文本字节数的上限（到了其一就
// 交出去）：一批是一次执行器调用，扩展驱动里是一次跨 ABI 的调用。
const (
	scriptBatchStatements = 200
	scriptBatchBytes      = 1 << 20
)

// ScriptChunk 是交给 ScriptSplitter 切分的一段文件文本。
type ScriptChunk struct {
	// Text 是文件里上一次切分没有用掉的文本，接着新读到的内容；总是完整的 UTF-8 字符。
	Text string `json:"text"`
	// State 是上一次切分返回的状态（第一次为空），由切分自己定义、原样带回。
	State string `json:"state,omitempty"`
	// Final 表示 Text 一直到文件结尾：切分必须用掉全部文本，结尾没有结束符的语句也要给出。
	Final bool `json:"final"`
}

// ScriptSplit 是一次切分的结果。
type ScriptSplit struct {
	// Statements 是 Text 里完整的语句，按文件的次序。
	Statements []ScriptStatement `json:"statements"`
	// Consumed 是 Text 里用掉的字节数：其后的文本（不完整的语句）与下一次读到的内容一起再交给切分。
	Consumed int `json:"consumed"`
	// State 是切分要带到下一次的状态（如方言的当前分隔符），下一次原样放进 ScriptChunk.State。
	State string `json:"state,omitempty"`
}

// ScriptStatement 是切分出的一条语句。
type ScriptStatement struct {
	// Text 是要执行的语句文本（不含结束符）。
	Text string `json:"text"`
	// Offset 是语句在 ScriptChunk.Text 里开始的字节位置，编排据此算出它在文件里的行号。
	Offset int `json:"offset"`
}

// ScriptSplitter 是方言里「怎么把 SQL 文件切成语句」：编排每读到一段文本交给它一次，它给出其中
// 完整的语句、用掉了多少文本与要带到下一次的状态。它自己不保存状态，每次的输入输出都能序列化，
// 扩展里的驱动可以经 ABI 提供它。
type ScriptSplitter interface {
	SplitScript(ctx context.Context, chunk ScriptChunk) (*ScriptSplit, error)
}

// ScriptBatch 是交给 ScriptExecutor 的一批语句。
type ScriptBatch struct {
	// Statements 按次序执行，每条单独提交。
	Statements []string `json:"statements"`
	// ContinueOnError 为假时停在这批里第一条失败的语句。
	ContinueOnError bool `json:"continueOnError"`
}

// ScriptExecutor 在一条固定的连接上执行一个 SQL 文件的语句：文件前面设置的会话状态（如关闭外键
// 检查）对后面的语句有效。
type ScriptExecutor interface {
	// ExecuteScriptBatch 按次序执行一批语句，结果的 Executed 下标对应 batch.Statements；语句本身的
	// 失败记在结果里，不作为 error 返回。连接本身出错（或 ctx 被取消）时返回 error，此前执行过的
	// 语句同时记在结果里。
	ExecuteScriptBatch(ctx context.Context, batch ScriptBatch) (*BatchResult, error)
	// Close 释放执行器占着的连接。
	io.Closer
}

// ScriptRunner 是驱动给运行 SQL 文件的两个钩子：切分，与在一条固定连接上执行。
type ScriptRunner interface {
	ScriptSplitter
	// OpenScriptExecutor 在 database 上占用一条连接，直到执行器被关闭。
	OpenScriptExecutor(ctx context.Context, database string) (ScriptExecutor, error)
}

// utf8BOM 是 UTF-8 的字节序标记：文件开头的它不属于任何语句。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// RunScript 是运行 SQL 文件的编排，各驱动的 Session.RunScript 用它：先检查整个文件是 UTF-8（不是
// 就以 *ScriptEncodingError 拒绝，什么也不执行），再回到开头边读边经 runner 切分，按批在同一个
// 执行器（一条连接）上按文件的次序执行。不继续时停在第一条失败的语句（ScriptResult.Failure）。
//
// 运行本身出错（读文件、切分、连接出错，ctx 被取消）时返回 error；已经执行过语句的，同时返回
// 记着它们的结果。
func RunScript(ctx context.Context, req ScriptRequest, runner ScriptRunner) (*ScriptResult, error) {
	readSize := req.ReadSize
	if readSize <= 0 {
		readSize = DefaultScriptReadSize
	}
	if err := checkScriptUTF8(req.Script, readSize); err != nil {
		return nil, err
	}
	if _, err := req.Script.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind script: %w", err)
	}
	executor, err := runner.OpenScriptExecutor(ctx, req.Database)
	if err != nil {
		return nil, err
	}
	run := &scriptRun{ctx: ctx, req: req, runner: runner, executor: executor, readSize: readSize}
	run.result.TotalBytes = req.Size
	err = run.run()
	if closeErr := executor.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close script executor: %w", closeErr))
	}
	return &run.result, err
}

// checkScriptUTF8 读完整个文件，确认它是 UTF-8（开头的 BOM 不算内容）。
func checkScriptUTF8(script io.Reader, readSize int) error {
	buf := make([]byte, 0, readSize+utf8.UTFMax)
	chunk := make([]byte, readSize)
	line := 1
	for {
		n, readErr := io.ReadFull(script, chunk)
		eof := errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)
		if readErr != nil && !eof {
			return fmt.Errorf("read script: %w", readErr)
		}
		buf = append(buf, chunk[:n]...)
		// 末尾被读取切开的字符留到下一轮，与后面读到的字节拼成一个字符再检查。
		end := len(buf)
		if !eof {
			end = completeRunes(buf)
		}
		for i := 0; i < end; {
			r, size := utf8.DecodeRune(buf[i:end])
			if r == utf8.RuneError && size <= 1 {
				return &ScriptEncodingError{Line: line + bytes.Count(buf[:i], []byte{'\n'})}
			}
			i += size
		}
		if eof {
			return nil
		}
		line += bytes.Count(buf[:end], []byte{'\n'})
		buf = append(buf[:0], buf[end:]...)
	}
}

// completeRunes 返回 b 里完整字符的长度：末尾被切开的半个多字节字符不算在内。
func completeRunes(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return len(b)
			}
			return i
		}
	}
	return len(b)
}

// scriptRun 是一次运行的状态。
type scriptRun struct {
	ctx      context.Context
	req      ScriptRequest
	runner   ScriptRunner
	executor ScriptExecutor
	readSize int
	result   ScriptResult

	// pending 是攒着、还没交给执行器的语句，pendingBytes 是它们的文本字节数。
	pending      []pendingStatement
	pendingBytes int
	// number 是已经切分出的语句数（文件里的条号）。
	number int64
	// done 表示运行已经停下（失败或被要求停下），不再执行后面的语句。
	done bool
}

type pendingStatement struct {
	text   string
	number int64
	line   int
}

func (r *scriptRun) run() error {
	buf := make([]byte, 0, r.readSize)
	chunk := make([]byte, r.readSize)
	want := r.readSize
	state := ""
	line := 1
	bomChecked := false
	for {
		if len(chunk) < want {
			chunk = make([]byte, want)
		}
		n, readErr := io.ReadFull(r.req.Script, chunk[:want])
		eof := errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)
		if readErr != nil && !eof {
			return fmt.Errorf("read script: %w", readErr)
		}
		r.result.BytesRead += int64(n)
		buf = append(buf, chunk[:n]...)
		if !bomChecked && (len(buf) >= len(utf8BOM) || eof) {
			buf = bytes.TrimPrefix(buf, utf8BOM)
			bomChecked = true
		}
		end := len(buf)
		if !eof {
			end = completeRunes(buf)
		}
		text := string(buf[:end])
		split, err := r.runner.SplitScript(r.ctx, ScriptChunk{Text: text, State: state, Final: eof})
		if err != nil {
			return err
		}
		if split.Consumed < 0 || split.Consumed > len(text) || (eof && split.Consumed != len(text)) {
			return fmt.Errorf("script splitter consumed %d of the %d bytes it was given (final: %t)", split.Consumed, len(text), eof)
		}
		pos := 0
		for _, statement := range split.Statements {
			if statement.Offset < pos || statement.Offset > split.Consumed {
				return fmt.Errorf("script splitter returned a statement at offset %d outside %d..%d", statement.Offset, pos, split.Consumed)
			}
			line += strings.Count(text[pos:statement.Offset], "\n")
			pos = statement.Offset
			if strings.TrimSpace(statement.Text) == "" {
				continue
			}
			r.number++
			r.pending = append(r.pending, pendingStatement{text: statement.Text, number: r.number, line: line})
			r.pendingBytes += len(statement.Text)
			if len(r.pending) >= scriptBatchStatements || r.pendingBytes >= scriptBatchBytes {
				if err := r.flush(); err != nil || r.done {
					return err
				}
			}
		}
		line += strings.Count(text[pos:split.Consumed], "\n")
		state = split.State
		buf = append(buf[:0], buf[split.Consumed:]...)
		if eof {
			break
		}
		// 一段文本里没有一条完整的语句（语句比读取块长）：下一次多读一倍，避免反复切分同一段。
		if split.Consumed == 0 {
			want *= 2
		} else {
			want = r.readSize
		}
	}
	return r.flush()
}

// flush 把攒着的语句交给执行器，计数，并在失败（不继续时）或被要求停下时结束运行。
func (r *scriptRun) flush() error {
	if len(r.pending) == 0 {
		return nil
	}
	select {
	case <-r.req.Stop:
		r.result.Stopped = true
		r.done = true
		return nil
	default:
	}
	batch := ScriptBatch{Statements: make([]string, len(r.pending)), ContinueOnError: r.req.ContinueOnError}
	for i, statement := range r.pending {
		batch.Statements[i] = statement.text
	}
	result, err := r.executor.ExecuteScriptBatch(r.ctx, batch)
	if result != nil {
		for _, executed := range result.Executed {
			if executed.Index < 0 || executed.Index >= len(r.pending) {
				return fmt.Errorf("script executor reported statement %d of a batch of %d", executed.Index, len(r.pending))
			}
			if executed.Error == "" {
				r.result.Succeeded++
				continue
			}
			r.result.Failed++
			if !r.req.ContinueOnError && r.result.Failure == nil {
				statement := r.pending[executed.Index]
				r.result.Failure = &ScriptFailure{
					Number: statement.number, Line: statement.line, Error: executed.Error, Excerpt: scriptExcerpt(statement.text),
				}
				r.done = true
			}
		}
	}
	r.pending, r.pendingBytes = r.pending[:0], 0
	if err != nil {
		return err
	}
	if r.req.Progress != nil {
		r.req.Progress(r.result.ScriptProgress)
	}
	select {
	case <-r.req.Stop:
		if !r.done {
			r.result.Stopped = true
			r.done = true
		}
	default:
	}
	return nil
}

// scriptExcerpt 是语句的开头：至多 ScriptExcerptRunes 个字符，截断时末尾加「…」。
func scriptExcerpt(statement string) string {
	statement = strings.TrimSpace(statement)
	if utf8.RuneCountInString(statement) <= ScriptExcerptRunes {
		return statement
	}
	runes := []rune(statement)
	return string(runes[:ScriptExcerptRunes]) + "…"
}
