package query

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// runEvents 截下运行 SQL 文件发出的事件。
type runEvents struct {
	mu     sync.Mutex
	events []RunSQLFileEvent
	done   chan RunSQLFileEvent
}

func captureRunEvents(t *testing.T) *runEvents {
	t.Helper()
	captured := &runEvents{done: make(chan RunSQLFileEvent, 1)}
	orig := emitRunSQLFileEvent
	emitRunSQLFileEvent = func(_ context.Context, event RunSQLFileEvent) {
		captured.mu.Lock()
		captured.events = append(captured.events, event)
		captured.mu.Unlock()
		if event.Status != RunSQLFileRunning {
			captured.done <- event
		}
	}
	t.Cleanup(func() { emitRunSQLFileEvent = orig })
	return captured
}

func (e *runEvents) wait(t *testing.T) RunSQLFileEvent {
	t.Helper()
	select {
	case event := <-e.done:
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish")
		return RunSQLFileEvent{}
	}
}

func writeScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.sql")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// 在 SQLite 上运行一个文件：语句在库里执行，终态事件带着成功条数、读完的字节数与运行的 id。
func TestRunSQLFileRunsTheFileOnTheAssetsDatabase(t *testing.T) {
	events := captureRunEvents(t)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	script := "CREATE TABLE t (id INTEGER PRIMARY KEY);\nINSERT INTO t VALUES (1);\nINSERT INTO t VALUES (2);\n"
	path := writeScript(t, script)

	runID, err := q.StartRunSQLFile(1, "main", RunSQLFileRequest{Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	assert.Equal(t, runID, final.RunID)
	assert.Equal(t, RunSQLFileDone, final.Status)
	assert.EqualValues(t, 3, final.Progress.Succeeded)
	assert.EqualValues(t, len(script), final.Progress.BytesRead)
	assert.EqualValues(t, len(script), final.Progress.TotalBytes)
	result, err := q.ExecuteSQL(1, "SELECT COUNT(*) AS n FROM t", "")
	require.NoError(t, err)
	assert.JSONEq(t, `{"columns":["n"],"rows":[{"n":2}],"count":1}`, result)
}

// 停在第一条失败的语句：终态是 failed，带着条号、行号、错误与语句开头。
func TestRunSQLFileReportsTheStatementItStoppedAt(t *testing.T) {
	events := captureRunEvents(t)
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapRunSQLFile)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})
	path := writeScript(t, "SELECT 1;\n"+dbdrivertest.ConformanceFailingStatement+";\nSELECT 2;\n")

	_, err := q.StartRunSQLFile(1, "app", RunSQLFileRequest{Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	assert.Equal(t, RunSQLFileFailed, final.Status)
	require.NotNil(t, final.Failure)
	assert.Equal(t, dbdriver.ScriptFailure{
		Number: 2, Line: 2, Error: "fakedb: syntax error", Excerpt: dbdrivertest.ConformanceFailingStatement,
	}, *final.Failure)
	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []string{"SELECT 1", dbdrivertest.ConformanceFailingStatement}, sessions[0].Statements())
	assert.Equal(t, "app", sessions[0].Request.Config.Database)
}

// 日志不记语句与数据：停在失败的语句上时，数据库的错误原文（常带着语句里的名字与值）只给对话框，日志只记
// 条号、行号与计数。
func TestRunSQLFileLogLeavesOutTheStatementAndItsError(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	oldLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(oldLogger) })
	events := captureRunEvents(t)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	const secret = "log_sentinel_value"
	path := writeScript(t, "CREATE TABLE t (id INTEGER);\nSELECT "+secret+" FROM t;\n")

	_, err := q.StartRunSQLFile(1, "main", RunSQLFileRequest{Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	require.Equal(t, RunSQLFileFailed, final.Status)
	require.Contains(t, final.Failure.Error, secret, "the dialog still gets the database's error")
	stopped := logs.FilterMessage("run sql file stopped at a failed statement").All()
	require.Len(t, stopped, 1)
	assert.EqualValues(t, 2, stopped[0].ContextMap()["statement"])
	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message, secret)
		for key, value := range entry.ContextMap() {
			assert.NotContains(t, fmt.Sprint(value), secret, "log %q field %q", entry.Message, key)
		}
	}
}

// 不是 UTF-8 的文件：一条也不执行，终态是 error，带着第一处非 UTF-8 内容所在的行。
func TestRunSQLFileRefusesANonUTF8File(t *testing.T) {
	events := captureRunEvents(t)
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapRunSQLFile)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})
	path := writeScript(t, "SELECT 1;\nSELECT '\xb2\xe2\xca\xd4';\n")

	_, err := q.StartRunSQLFile(1, "", RunSQLFileRequest{Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	assert.Equal(t, RunSQLFileError, final.Status)
	assert.Equal(t, 2, final.InvalidUTF8Line)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
	}
}

// scriptErrorDriver 的会话运行文件时不碰文件，直接返回 result 与 err（如停在不支持的写法上，假库的切分
// 只认分号，认不出 psql 的写法）。
type scriptErrorDriver struct {
	*dbdrivertest.Fake
	result *dbdriver.ScriptResult
	err    error
}

func (d scriptErrorDriver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	session, err := d.Fake.Open(ctx, req)
	return scriptErrorSession{Session: session, result: d.result, err: d.err}, err
}

type scriptErrorSession struct {
	dbdriver.Session
	result *dbdriver.ScriptResult
	err    error
}

func (s scriptErrorSession) RunScript(context.Context, dbdriver.ScriptRequest) (*dbdriver.ScriptResult, error) {
	return s.result, s.err
}

// startScriptError 用 scriptErrorDriver 运行一个文件，返回终态事件与这次运行的日志。
func startScriptError(t *testing.T, result *dbdriver.ScriptResult, runErr error) (RunSQLFileEvent, *observer.ObservedLogs) {
	t.Helper()
	events := captureRunEvents(t)
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapRunSQLFile)
	dbdrivertest.Register(t, scriptErrorDriver{Fake: fake, result: result, err: runErr})
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})
	path := writeScript(t, "SELECT 1;\nSELECT 2;\nCOPY t FROM stdin;\n")

	core, logs := observer.New(zap.InfoLevel)
	q.ctx = logger.WithContextLogger(context.Background(), zap.New(core))
	_, err := q.StartRunSQLFile(1, "", RunSQLFileRequest{Path: path})
	require.NoError(t, err)
	return events.wait(t), logs
}

// 运行停在不支持的写法上：终态事件带着写法、行号与该改用的工具这些结构化字段，由对话框按界面语言写
// 提示，Error 不再带一句固定语言的文字；一起返回的错误（关执行器失败）不丢，写进日志。
func TestRunSQLFileReportsAnUnsupportedConstructAsFields(t *testing.T) {
	result := &dbdriver.ScriptResult{ScriptProgress: dbdriver.ScriptProgress{Succeeded: 2}}
	// 编排停下后关执行器也失败了：两者一起返回（dbdriver.RunScript 就是这样合并的）。
	runErr := errors.Join(fmt.Errorf("run: %w", &dbdriver.ScriptUnsupportedError{Construct: "COPY ... FROM stdin", Tool: "psql", Line: 3}),
		errors.New("close script executor: connection reset"))
	final, logs := startScriptError(t, result, runErr)

	assert.Equal(t, RunSQLFileError, final.Status)
	require.NotNil(t, final.Unsupported)
	assert.Equal(t, dbdriver.ScriptUnsupportedError{Construct: "COPY ... FROM stdin", Tool: "psql", Line: 3}, *final.Unsupported)
	assert.Empty(t, final.Error, "the dialog words this case itself, in the UI language")
	assert.Equal(t, int64(2), final.Progress.Succeeded)
	stopped := logs.FilterMessage("run sql file stopped at an unsupported construct").All()
	require.Len(t, stopped, 1)
	assert.Contains(t, stopped[0].ContextMap()["error"], "connection reset", "an error joined to the stop is not swallowed")
}

// 拒绝不是 UTF-8 的文件时一起返回的错误（如释放连接失败）也不丢：对话框只按行号写提示，它进日志。
func TestRunSQLFileLogsAnErrorJoinedToANonUTF8Refusal(t *testing.T) {
	final, logs := startScriptError(t, nil, errors.Join(&dbdriver.ScriptEncodingError{Line: 2}, errors.New("release session: connection reset")))

	assert.Equal(t, RunSQLFileError, final.Status)
	assert.Equal(t, 2, final.InvalidUTF8Line)
	assert.Empty(t, final.Error, "the dialog words this case itself, in the UI language")
	refused := logs.FilterMessage("run sql file refused a non-UTF-8 file").All()
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].ContextMap()["error"], "connection reset", "an error joined to the refusal is not swallowed")
}

// 只读连接的资产与没有声明运行 SQL 文件的驱动：在开始之前就拒绝，不打开会话。
func TestRunSQLFileIsRefusedBeforeStarting(t *testing.T) {
	path := writeScript(t, "DELETE FROM t;")
	for name, tc := range map[string]struct {
		capable  bool
		readOnly bool
	}{
		"a read-only asset":            {capable: true, readOnly: true},
		"a driver without the feature": {capable: false},
	} {
		t.Run(name, func(t *testing.T) {
			fake := dbdrivertest.NewFake()
			if tc.capable {
				fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapRunSQLFile)
			}
			dbdrivertest.Register(t, fake)
			q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, ReadOnly: tc.readOnly})

			_, err := q.StartRunSQLFile(1, "", RunSQLFileRequest{Path: path})
			require.Error(t, err)
			assert.Empty(t, fake.Sessions())
		})
	}
}

// 勾了「遇到错误时继续」：失败的语句逐条经事件列出（条号、行号、错误），超过 100 条后不再逐条列出，
// 只在一个事件上提示一次并继续计数；终态带着成功与失败的总数。
func TestRunSQLFileListsFailuresUpToTheCapAndKeepsCounting(t *testing.T) {
	events := captureRunEvents(t)
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapRunSQLFile)
	fake.StatementErrors = map[string]error{"BAD": errors.New("boom")}
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})
	var script strings.Builder
	script.WriteString("SELECT 1;\n")
	for range RunSQLFileMaxListedFailures + 5 {
		script.WriteString("BAD;\n")
	}
	script.WriteString("SELECT 2;\n")
	path := writeScript(t, script.String())

	_, err := q.StartRunSQLFile(1, "", RunSQLFileRequest{Path: path, ContinueOnError: true})
	require.NoError(t, err)
	final := events.wait(t)

	assert.Equal(t, RunSQLFileDone, final.Status)
	assert.EqualValues(t, 2, final.Progress.Succeeded)
	assert.EqualValues(t, RunSQLFileMaxListedFailures+5, final.Progress.Failed)
	events.mu.Lock()
	defer events.mu.Unlock()
	var listed []dbdriver.ScriptFailure
	omitted := 0
	for _, event := range events.events {
		listed = append(listed, event.Failures...)
		if event.FailuresOmitted {
			omitted++
		}
	}
	require.Len(t, listed, RunSQLFileMaxListedFailures)
	assert.Equal(t, dbdriver.ScriptFailure{Number: 2, Line: 2, Error: "boom", Excerpt: "BAD"}, listed[0])
	assert.EqualValues(t, RunSQLFileMaxListedFailures+1, listed[RunSQLFileMaxListedFailures-1].Number)
	assert.Equal(t, 1, omitted)
}

// 退出应用（Cleanup）等同于点「停止」：进行中的运行在当前语句后停下，终态是 stopped，Cleanup 返回
// 时它已经收尾（会话在连接缓存关闭之前释放）。
func TestCleanupStopsARunningSQLFile(t *testing.T) {
	events := captureRunEvents(t)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	const rows = 20000
	var script strings.Builder
	script.WriteString("CREATE TABLE t (id INTEGER);\n")
	for i := range rows {
		fmt.Fprintf(&script, "INSERT INTO t VALUES (%d);\n", i)
	}
	_, err := q.StartRunSQLFile(1, "main", RunSQLFileRequest{Path: writeScript(t, script.String())})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		events.mu.Lock()
		defer events.mu.Unlock()
		return len(events.events) > 0
	}, 10*time.Second, 5*time.Millisecond)

	q.Cleanup()

	select {
	case final := <-events.done:
		assert.Equal(t, RunSQLFileStopped, final.Status)
		assert.Less(t, final.Progress.Succeeded, int64(rows+1))
	default:
		t.Fatal("Cleanup returned before the run ended")
	}
}
