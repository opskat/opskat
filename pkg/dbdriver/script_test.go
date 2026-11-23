package dbdriver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// semicolonRunner 是测试用的脚本钩子：切分只认分号（不认引号与注释），执行器把语句记下来，
// failing 里的语句失败。它记下每次切分收到的文本，证明编排只把完整的 UTF-8 交给切分。
type semicolonRunner struct {
	failing  map[string]string
	opened   int
	closed   int
	batches  []ScriptBatch
	executed []string
	chunks   []ScriptChunk
	splitErr error
	// splitErrAfter 是切分返回 splitErr 之前正常切分的次数。
	splitErrAfter int
	leaveTail     bool
	// unsupported 是不支持的语句开头：切分到以它开头的语句时停下，给出它之前的语句与它的位置。
	unsupported string
	// onStatement 在一条语句开始执行时被调用，测试用它在一批的中途要求停下（这条语句仍会执行完）。
	onStatement func(statement string)
	// connErr 里的语句执行时连接出错：执行器返回这个 error，这条语句不算执行过。
	connErr map[string]error
}

func (r *semicolonRunner) SplitScript(_ context.Context, chunk ScriptChunk) (*ScriptSplit, error) {
	r.chunks = append(r.chunks, chunk)
	if r.splitErr != nil && len(r.chunks) > r.splitErrAfter {
		return nil, r.splitErr
	}
	split := &ScriptSplit{Statements: []ScriptStatement{}, State: chunk.State + "+"}
	start := 0
	emit := func(end int) {
		text := chunk.Text[start:end]
		trimmed := strings.TrimLeft(text, " \t\r\n")
		if strings.TrimSpace(trimmed) == "" {
			return
		}
		offset := start + len(text) - len(trimmed)
		if r.unsupported != "" && strings.HasPrefix(trimmed, r.unsupported) {
			split.Unsupported = &ScriptUnsupported{Construct: r.unsupported, Tool: "psql", Offset: offset}
			return
		}
		split.Statements = append(split.Statements, ScriptStatement{Text: strings.TrimSpace(trimmed), Offset: offset})
	}
	for i := 0; i < len(chunk.Text) && split.Unsupported == nil; i++ {
		if chunk.Text[i] == ';' {
			emit(i)
			start = i + 1
		}
	}
	if split.Unsupported != nil {
		return split, nil
	}
	split.Consumed = start
	if chunk.Final && !r.leaveTail {
		emit(len(chunk.Text))
		split.Consumed = len(chunk.Text)
	}
	return split, nil
}

func (r *semicolonRunner) OpenScriptExecutor(context.Context, string) (ScriptExecutor, error) {
	r.opened++
	return scriptExecutorFunc{run: r.run, close: func() error { r.closed++; return nil }}, nil
}

func (r *semicolonRunner) run(_ context.Context, batch ScriptBatch) (*BatchResult, error) {
	r.batches = append(r.batches, batch)
	result := &BatchResult{Executed: []BatchStatement{}}
	for i, statement := range batch.Statements {
		select {
		case <-batch.Stop:
			return result, nil
		default:
		}
		if err, ok := r.connErr[statement]; ok {
			return result, err
		}
		r.executed = append(r.executed, statement)
		if r.onStatement != nil {
			r.onStatement(statement)
		}
		if msg, ok := r.failing[statement]; ok {
			result.Executed = append(result.Executed, BatchStatement{Index: i, Error: msg})
			if !batch.ContinueOnError {
				break
			}
			continue
		}
		result.Executed = append(result.Executed, BatchStatement{Index: i, AffectedRows: 1})
	}
	return result, nil
}

type scriptExecutorFunc struct {
	run   func(context.Context, ScriptBatch) (*BatchResult, error)
	close func() error
}

func (e scriptExecutorFunc) ExecuteScriptBatch(ctx context.Context, batch ScriptBatch) (*BatchResult, error) {
	return e.run(ctx, batch)
}

func (e scriptExecutorFunc) Close() error { return e.close() }

func scriptRequest(script string) ScriptRequest {
	return ScriptRequest{Script: strings.NewReader(script), Size: int64(len(script))}
}

// 文件里有非 UTF-8 的内容：一条语句也不执行（连执行器都不打开），错误给出第一处所在的行。
func TestRunScriptRefusesANonUTF8FileBeforeExecutingAnything(t *testing.T) {
	runner := &semicolonRunner{}
	script := "CREATE TABLE a (x INT);\nINSERT INTO a VALUES (1);\nINSERT INTO a VALUES ('\xff\xfe');\nINSERT INTO a VALUES (\xc3);\n"

	_, err := RunScript(context.Background(), scriptRequest(script), runner)

	var encodingErr *ScriptEncodingError
	require.ErrorAs(t, err, &encodingErr)
	assert.Equal(t, 3, encodingErr.Line)
	assert.Zero(t, runner.opened)
	assert.Empty(t, runner.executed)
}

// 一个多字节字符被读取块切开不是编码错误；交给切分的文本也总是完整的字符。
func TestRunScriptKeepsMultiByteCharactersWholeAcrossReads(t *testing.T) {
	runner := &semicolonRunner{}
	script := "INSERT INTO t VALUES ('中文字符');\nINSERT INTO t VALUES ('é');"
	req := scriptRequest(script)
	req.ReadSize = 4

	result, err := RunScript(context.Background(), req, runner)
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO t VALUES ('中文字符')", "INSERT INTO t VALUES ('é')"}, runner.executed)
	assert.EqualValues(t, 2, result.Succeeded)
	for _, chunk := range runner.chunks {
		assert.True(t, utf8.ValidString(chunk.Text), "chunk %q", chunk.Text)
	}
}

// 开头的 BOM 不是语句的一部分，也不影响编码检查。
func TestRunScriptIgnoresALeadingBOM(t *testing.T) {
	runner := &semicolonRunner{}
	result, err := RunScript(context.Background(), scriptRequest("\ufeffSELECT 1;\nSELECT 2;"), runner)
	require.NoError(t, err)
	assert.Equal(t, []string{"SELECT 1", "SELECT 2"}, runner.executed)
	assert.EqualValues(t, 2, result.Succeeded)
}

// 文件按块读取：跨块的语句照样完整，按文件的次序在同一个执行器（同一条连接）上执行；切分的
// 状态从上一块带到下一块；读完时字节数等于文件大小。
func TestRunScriptStreamsStatementsInOrderAcrossReadChunks(t *testing.T) {
	statements := make([]string, 0, 50)
	var script strings.Builder
	for i := range 50 {
		statement := "INSERT INTO t VALUES (" + strings.Repeat("x", i) + ")"
		statements = append(statements, statement)
		script.WriteString(statement + ";\n")
	}
	runner := &semicolonRunner{}
	req := scriptRequest(script.String())
	req.ReadSize = 7
	var progress []ScriptProgress
	req.Progress = func(p ScriptProgress) { progress = append(progress, p) }

	result, err := RunScript(context.Background(), req, runner)
	require.NoError(t, err)
	assert.Equal(t, statements, runner.executed)
	assert.Equal(t, 1, runner.opened)
	assert.Equal(t, 1, runner.closed)
	assert.EqualValues(t, 50, result.Succeeded)
	assert.Zero(t, result.Failed)
	assert.Nil(t, result.Failure)
	assert.Greater(t, len(runner.chunks), 1)
	for i, chunk := range runner.chunks {
		assert.Equal(t, strings.Repeat("+", i), chunk.State, "the splitter's state is carried to the next chunk")
	}
	require.NotEmpty(t, progress)
	last := progress[len(progress)-1]
	assert.EqualValues(t, 50, last.Succeeded)
	assert.EqualValues(t, len(script.String()), last.BytesRead)
	assert.EqualValues(t, len(script.String()), last.TotalBytes)
}

// 不继续时停在第一条失败的语句：给出它是第几条、从文件第几行开始、数据库的错误与语句开头
// 约 200 个字符；之后的语句不再执行。
func TestRunScriptStopsAtTheFirstFailure(t *testing.T) {
	long := "INSERT INTO t VALUES ('" + strings.Repeat("长", 300) + "')"
	runner := &semicolonRunner{failing: map[string]string{long: "duplicate key"}}
	script := "-- header\n\nSELECT 1;\nSELECT 2;\n\n  " + long + ";\nSELECT 3;\n"
	req := scriptRequest(script)
	req.ReadSize = 16

	result, err := RunScript(context.Background(), req, runner)
	require.NoError(t, err)
	assert.Equal(t, []string{"-- header\n\nSELECT 1", "SELECT 2", long}, runner.executed)
	assert.EqualValues(t, 2, result.Succeeded)
	assert.EqualValues(t, 1, result.Failed)
	require.NotNil(t, result.Failure)
	assert.EqualValues(t, 3, result.Failure.Number)
	assert.Equal(t, 6, result.Failure.Line)
	assert.Equal(t, "duplicate key", result.Failure.Error)
	assert.True(t, strings.HasPrefix(long, strings.TrimSuffix(result.Failure.Excerpt, "…")))
	assert.Equal(t, ScriptExcerptRunes+1, utf8.RuneCountInString(result.Failure.Excerpt))
	assert.Equal(t, 1, runner.closed)
}

// 第一条语句的行号从 1 起；语句前的空行与缩进计入行号。
func TestRunScriptReportsTheLineAStatementStartsOn(t *testing.T) {
	runner := &semicolonRunner{failing: map[string]string{"BAD": "syntax error"}}
	result, err := RunScript(context.Background(), scriptRequest("\ufeff\n\n   BAD;"), runner)
	require.NoError(t, err)
	require.NotNil(t, result.Failure)
	assert.Equal(t, 3, result.Failure.Line)
	assert.EqualValues(t, 1, result.Failure.Number)
	assert.Equal(t, "BAD", result.Failure.Excerpt)
}

// 切分出错（驱动认出了不支持的写法）时什么也不再执行，错误原样返回。
func TestRunScriptReturnsTheSplittersError(t *testing.T) {
	splitErr := errors.New("unsupported construct")
	runner := &semicolonRunner{splitErr: splitErr}
	_, err := RunScript(context.Background(), scriptRequest("SELECT 1;"), runner)
	require.ErrorIs(t, err, splitErr)
	assert.Empty(t, runner.executed)
}

// 文件读完时切分必须用掉全部文本，否则编排会一直等不到结尾：当作切分的错误报出来。
func TestRunScriptRefusesASplitterThatLeavesTheEndOfTheFile(t *testing.T) {
	runner := &semicolonRunner{leaveTail: true}
	_, err := RunScript(context.Background(), scriptRequest("SELECT 1; SELECT 2"), runner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consumed")
}

// 关闭 Stop 后不再开始下一批：结果标为已停止，已执行的照实计数。
func TestRunScriptStopsBeforeTheNextBatchWhenAsked(t *testing.T) {
	var script strings.Builder
	for range scriptBatchStatements * 3 {
		script.WriteString("SELECT 1;\n")
	}
	stop := make(chan struct{})
	runner := &semicolonRunner{}
	req := scriptRequest(script.String())
	req.Stop = stop
	req.Progress = func(ScriptProgress) {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}

	result, err := RunScript(context.Background(), req, runner)
	require.NoError(t, err)
	assert.True(t, result.Stopped)
	assert.Len(t, runner.batches, 1)
	assert.EqualValues(t, scriptBatchStatements, result.Succeeded)
}

// 一批语句执行到一半时被要求停下：执行器收到批里的 Stop，在当前语句之后停下，没轮到的语句不执行，
// 结果标为已停止，已执行的照实计数。
func TestRunScriptStopsBetweenStatementsOfABatch(t *testing.T) {
	stop := make(chan struct{})
	runner := &semicolonRunner{}
	req := scriptRequest("SELECT 1;\nSELECT 2;\nSELECT 3;\nSELECT 4;\n")
	req.Stop = stop
	var once sync.Once
	runner.onStatement = func(statement string) {
		if statement == "SELECT 2" {
			once.Do(func() { close(stop) })
		}
	}

	result, err := RunScript(context.Background(), req, runner)
	require.NoError(t, err)
	assert.True(t, result.Stopped)
	assert.Equal(t, []string{"SELECT 1", "SELECT 2"}, runner.executed)
	assert.EqualValues(t, 2, result.Succeeded)
}

// 勾了「继续」时每条失败的语句都交给 OnFailure（条号、行号、错误与语句开头）；没勾时失败只记在结果里。
func TestRunScriptReportsEveryFailureWhenContinuing(t *testing.T) {
	runner := &semicolonRunner{failing: map[string]string{"BAD 1": "boom 1", "BAD 2": "boom 2"}}
	script := "SELECT 1;\nBAD 1;\nSELECT 2;\n\nBAD 2;\n"

	var failures []ScriptFailure
	req := scriptRequest(script)
	req.ContinueOnError = true
	req.OnFailure = func(f ScriptFailure) { failures = append(failures, f) }
	result, err := RunScript(context.Background(), req, runner)
	require.NoError(t, err)
	assert.EqualValues(t, 2, result.Succeeded)
	assert.EqualValues(t, 2, result.Failed)
	assert.Nil(t, result.Failure)
	assert.Equal(t, []ScriptFailure{
		{Number: 2, Line: 2, Error: "boom 1", Excerpt: "BAD 1"},
		{Number: 4, Line: 5, Error: "boom 2", Excerpt: "BAD 2"},
	}, failures)

	failures = nil
	req = scriptRequest(script)
	req.OnFailure = func(f ScriptFailure) { failures = append(failures, f) }
	result, err = RunScript(context.Background(), req, &semicolonRunner{failing: runner.failing})
	require.NoError(t, err)
	assert.Empty(t, failures)
	require.NotNil(t, result.Failure)
}

// 切分在文件后面出错时，前面已经切出、还没执行的语句先按次序执行完，再返回切分的错误。
func TestRunScriptExecutesThePendingStatementsBeforeASplitterError(t *testing.T) {
	splitErr := errors.New("splitter broke")
	runner := &semicolonRunner{splitErr: splitErr, splitErrAfter: 1}
	req := scriptRequest("SELECT 1;\nSELECT 2;\nSELECT 3")
	req.ReadSize = len("SELECT 1;\nSELECT 2;\n")

	result, err := RunScript(context.Background(), req, runner)
	require.ErrorIs(t, err, splitErr)
	assert.Equal(t, []string{"SELECT 1", "SELECT 2"}, runner.executed)
	require.NotNil(t, result)
	assert.EqualValues(t, 2, result.Succeeded)
}

// 切分认出不支持的写法：它之前的语句按次序执行完（勾了「继续」时失败的也照样继续），然后运行停下，
// 报出是什么写法、在文件第几行、该用什么工具；之后的语句不执行。与文件按多大的块读取无关。
func TestRunScriptStopsAtAnUnsupportedConstructAfterTheStatementsBeforeIt(t *testing.T) {
	script := "SELECT 1;\nBAD;\n\n  COPY t FROM stdin;\n1\t2\n\\.\nSELECT 3;\n"
	for readSize := 1; readSize <= len(script); readSize++ {
		runner := &semicolonRunner{failing: map[string]string{"BAD": "boom"}, unsupported: "COPY"}
		req := scriptRequest(script)
		req.ReadSize = readSize
		req.ContinueOnError = true

		result, err := RunScript(context.Background(), req, runner)
		var unsupported *ScriptUnsupportedError
		require.ErrorAs(t, err, &unsupported, "read size %d", readSize)
		assert.Equal(t, ScriptUnsupportedError{Construct: "COPY", Tool: "psql", Line: 4}, *unsupported, "read size %d", readSize)
		assert.Equal(t, []string{"SELECT 1", "BAD"}, runner.executed, "read size %d", readSize)
		require.NotNil(t, result)
		assert.EqualValues(t, 1, result.Succeeded, "read size %d", readSize)
		assert.EqualValues(t, 1, result.Failed, "read size %d", readSize)
	}
}

// 执行不支持的写法之前的语句时连接出错：运行停在那里，报连接的错误，不报还没到的不支持的写法
// （否则会被当成「之前的语句已执行」）。
func TestRunScriptReportsAConnectionErrorBeforeAnUnsupportedConstruct(t *testing.T) {
	connErr := errors.New("connection reset")
	runner := &semicolonRunner{connErr: map[string]error{"SELECT 2": connErr}, unsupported: "COPY"}
	result, err := RunScript(context.Background(), scriptRequest("SELECT 1;\nSELECT 2;\nCOPY t FROM stdin;\n"), runner)
	require.ErrorIs(t, err, connErr)
	var unsupported *ScriptUnsupportedError
	assert.False(t, errors.As(err, &unsupported), "the run never reached the construct: %v", err)
	assert.Equal(t, []string{"SELECT 1"}, runner.executed)
	require.NotNil(t, result)
	assert.EqualValues(t, 1, result.Succeeded)
}

// 不继续时，不支持的写法之前已有语句失败：运行停在那条失败的语句上（照常报出），不再报不支持的写法。
func TestRunScriptStopsAtAFailureBeforeAnUnsupportedConstruct(t *testing.T) {
	runner := &semicolonRunner{failing: map[string]string{"BAD": "boom"}, unsupported: "COPY"}
	result, err := RunScript(context.Background(), scriptRequest("BAD;\nSELECT 1;\nCOPY t FROM stdin;\n"), runner)
	require.NoError(t, err)
	assert.Equal(t, []string{"BAD"}, runner.executed)
	require.NotNil(t, result.Failure)
	assert.EqualValues(t, 1, result.Failure.Number)
}
