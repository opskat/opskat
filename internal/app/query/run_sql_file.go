package query

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/app/i18n"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// RunSQLFileEventName 是运行 SQL 文件的事件名：每次运行的进度与终态都发在它上面，以 runId 区分。
// 前端在 StartRunSQLFile 之前订阅，拿到 runId 后只看自己的那次运行。
const RunSQLFileEventName = "query:run-sql-file"

// RunSQLFileStatus 是一次运行的状态。除了 running，其余都是终态，每次运行恰好发一个终态事件。
type RunSQLFileStatus string

const (
	// RunSQLFileRunning：进行中的进度。
	RunSQLFileRunning RunSQLFileStatus = "running"
	// RunSQLFileDone：文件执行完（勾了「遇到错误时继续」时可能有失败的语句，见 Progress.Failed）。
	RunSQLFileDone RunSQLFileStatus = "done"
	// RunSQLFileFailed：停在第一条失败的语句（Failure）。
	RunSQLFileFailed RunSQLFileStatus = "failed"
	// RunSQLFileStopped：被 StopRunSQLFile 停下。
	RunSQLFileStopped RunSQLFileStatus = "stopped"
	// RunSQLFileError：运行本身出错（文件不是 UTF-8 时 InvalidUTF8Line 非零），Error 是原因。
	RunSQLFileError RunSQLFileStatus = "error"
)

// RunSQLFileEvent 是运行 SQL 文件发给前端的一个事件。
type RunSQLFileEvent struct {
	RunID     string                  `json:"runId"`
	Status    RunSQLFileStatus        `json:"status"`
	Progress  dbdriver.ScriptProgress `json:"progress"`
	ElapsedMs int64                   `json:"elapsedMs"`
	// Failure 是 failed 时停在的那条语句。
	Failure *dbdriver.ScriptFailure `json:"failure,omitempty"`
	// Failures 是自上一个事件以来新失败的语句（勾了「遇到错误时继续」时）：整次运行里最多逐条列出
	// RunSQLFileMaxListedFailures 条，之后的只计数（Progress.Failed），并在 FailuresOmitted 上提示一次。
	Failures []dbdriver.ScriptFailure `json:"failures,omitempty"`
	// FailuresOmitted 在失败超过上限、后面的不再逐条列出的那个事件上为真，之后不再重复。
	FailuresOmitted bool `json:"failuresOmitted,omitempty"`
	// InvalidUTF8Line 是文件不是 UTF-8 时第一处非 UTF-8 内容所在的行（此时一条语句也没有执行）。
	InvalidUTF8Line int `json:"invalidUtf8Line,omitempty"`
	// Error 是 error 时的原因。
	Error string `json:"error,omitempty"`
}

// RunSQLFileRequest 是开始运行的参数。
type RunSQLFileRequest struct {
	// Path 是本机上要运行的文件（SelectSQLFile 选出的）。
	Path string `json:"path"`
	// ContinueOnError 为真时一条语句失败后继续执行后面的。
	ContinueOnError bool `json:"continueOnError"`
}

// SQLFile 是选中的一个 SQL 文件。
type SQLFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// RunSQLFileMaxListedFailures 是一次运行最多逐条列出的失败语句数。
const RunSQLFileMaxListedFailures = 100

// runSQLFileTimeout 是一次运行的时限。运行由用户停下，不受资产的查询超时限制；这个时限只收回被
// 遗忘、一直没有结束的运行。
const runSQLFileTimeout = 24 * time.Hour

// runSQLFileProgressInterval 是进度事件的最小间隔：执行很快时不要让事件淹没前端。
const runSQLFileProgressInterval = 200 * time.Millisecond

// runSQLFileLogEvery 是每执行多少条语句往日志写一行进度。
const runSQLFileLogEvery = 10000

// emitRunSQLFileEvent 发出一个事件；测试里替换它来截下事件。
var emitRunSQLFileEvent = func(ctx context.Context, event RunSQLFileEvent) {
	wailsRuntime.EventsEmit(ctx, RunSQLFileEventName, event)
}

var (
	sqlFileRunsMu sync.Mutex
	// sqlFileRuns 是进行中的运行：runId → 要求它停下的函数。
	sqlFileRuns = map[string]func(){}
	// sqlFileRunsWG 在每次运行收尾（会话与文件都释放）后完成：退出应用时等它们停下。
	sqlFileRunsWG sync.WaitGroup
)

// sqlFileStopWait 是退出应用时等进行中的运行在当前语句之后停下的上限。
const sqlFileStopWait = 10 * time.Second

// stopAllSQLFileRuns 要求所有进行中的运行停下，并等它们收尾（至多 sqlFileStopWait）：退出应用等同于
// 点「停止」，会话要在连接缓存关闭之前释放。
func stopAllSQLFileRuns() {
	sqlFileRunsMu.Lock()
	for _, stop := range sqlFileRuns {
		stop()
	}
	sqlFileRunsMu.Unlock()
	finished := make(chan struct{})
	go func() {
		sqlFileRunsWG.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(sqlFileStopWait):
		logger.Default().Warn("run sql file did not stop before quitting", zap.Duration("wait", sqlFileStopWait))
	}
}

// SelectSQLFile 打开原生的打开文件对话框（筛选 *.sql，也允许所有文件），返回选中的文件与它的大小；
// 用户取消时返回 nil。
func (q *Query) SelectSQLFile() (*SQLFile, error) {
	path, err := wailsRuntime.OpenFileDialog(q.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Run SQL File",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "SQL (*.sql)", Pattern: "*.sql"},
			{DisplayName: "All Files", Pattern: "*"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open file dialog failed: %w", err)
	}
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &SQLFile{Path: path, Size: info.Size()}, nil
}

// StartRunSQLFile 在资产的 database 上开始运行一个 SQL 文件，返回这次运行的 id；进度与终态经
// RunSQLFileEventName 事件发出。资产设为只读连接、驱动没有声明运行 SQL 文件时在开始之前拒绝。
// 不写审计日志；日志只记开始、结束、条数、失败数与耗时，不记语句与数据。
func (q *Query) StartRunSQLFile(assetID int64, database string, request RunSQLFileRequest) (string, error) {
	ctx := i18n.Ctx(q.ctx, q.lang.Lang())
	log := logger.Ctx(ctx).With(zap.String("op", "run-sql-file"), zap.Int64("assetID", assetID), zap.String("database", database))
	if request.Path == "" {
		return "", errors.New("没有选择 SQL 文件")
	}
	if err := checkRunSQLFileAllowed(ctx, assetID); err != nil {
		log.Warn("run sql file refused", zap.Error(err))
		return "", err
	}
	file, err := os.Open(request.Path)
	if err != nil {
		log.Error("run sql file open failed", zap.Error(err))
		return "", err
	}
	info, err := file.Stat()
	if err != nil {
		closeRunFile(ctx, file)
		log.Error("run sql file open failed", zap.Error(err))
		return "", err
	}
	panel, err := q.acquirePanelSession(assetID, database, func(*asset_entity.DatabaseConfig) time.Duration { return runSQLFileTimeout })
	if err != nil {
		closeRunFile(ctx, file)
		log.Error("run sql file open failed", zap.Error(err))
		return "", err
	}

	runID := uuid.NewString()
	stop := make(chan struct{})
	sqlFileRunsMu.Lock()
	sqlFileRuns[runID] = sync.OnceFunc(func() { close(stop) })
	sqlFileRunsMu.Unlock()
	sqlFileRunsWG.Add(1)

	log = log.With(zap.String("runID", runID))
	log.Info("run sql file start", zap.Int64("bytes", info.Size()), zap.Bool("continueOnError", request.ContinueOnError))
	go q.runSQLFile(runID, log, panel, file, dbdriver.ScriptRequest{
		Database: panel.database, Script: file, Size: info.Size(), ContinueOnError: request.ContinueOnError, Stop: stop,
	})
	return runID, nil
}

// StopRunSQLFile 要求一次运行停下：当前语句执行完就停，终态为 stopped。运行已经结束时什么
// 也不做。
func (q *Query) StopRunSQLFile(runID string) error {
	sqlFileRunsMu.Lock()
	stop, ok := sqlFileRuns[runID]
	sqlFileRunsMu.Unlock()
	if ok {
		stop()
	}
	return nil
}

// checkRunSQLFileAllowed 检查资产能不能运行 SQL 文件：资产设为只读连接时不能（有的驱动的连接级只读
// 并不生效），驱动没有声明时不能。
func checkRunSQLFileAllowed(ctx context.Context, assetID int64) error {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return errors.New("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return fmt.Errorf("获取数据库配置失败: %w", err)
	}
	if cfg.ReadOnly {
		return errors.New("资产设为只读连接，不能运行 SQL 文件")
	}
	driver, err := dbregistry.Lookup(cfg.Driver)
	if err != nil {
		return err
	}
	if !driver.Descriptor().Supports(dbdriver.CapRunSQLFile) {
		return fmt.Errorf("%w: driver %q does not run SQL files", dbdriver.ErrUnsupported, cfg.Driver)
	}
	return nil
}

// runSQLFile 是一次运行的 goroutine：运行文件，发出进度与终态，释放会话与文件。recover 是这条
// goroutine 的边界：意外的 panic 记下来，并以 error 终态结束，前端不会一直停在进行中。
func (q *Query) runSQLFile(runID string, log *zap.Logger, panel *panelSession, file *os.File, req dbdriver.ScriptRequest) {
	start := time.Now()
	event := RunSQLFileEvent{RunID: runID, Status: RunSQLFileRunning}
	finished := false
	defer func() {
		defer sqlFileRunsWG.Done()
		sqlFileRunsMu.Lock()
		delete(sqlFileRuns, runID)
		sqlFileRunsMu.Unlock()
		closeRunFile(panel.ctx, file)
		r := recover()
		if r == nil {
			return
		}
		log.Error("run sql file panic recovered", zap.Any("panic", r), zap.Stack("stack"))
		if !finished {
			if err := panel.finish(nil); err != nil {
				log.Warn("release database session", zap.Error(err))
			}
		}
		event.Status, event.Error = RunSQLFileError, fmt.Sprintf("运行 SQL 文件时发生意外错误: %v", r)
		event.Failures, event.FailuresOmitted = nil, false
		event.ElapsedMs = time.Since(start).Milliseconds()
		emitRunSQLFileEvent(q.ctx, event)
	}()

	var lastEmit time.Time
	var logged int64
	// failures 是还没发出去的失败语句；listed 是已经列出的条数，omitted 表示「不再逐条列出」已经提示过。
	var failures []dbdriver.ScriptFailure
	listed, omitReported, omitPending := 0, false, false
	req.OnFailure = func(failure dbdriver.ScriptFailure) {
		if listed < RunSQLFileMaxListedFailures {
			listed++
			failures = append(failures, failure)
		} else if !omitReported {
			omitReported, omitPending = true, true
		}
	}
	// flushFailures 把攒着的失败放进事件，之后清空。
	flushFailures := func() {
		event.Failures, event.FailuresOmitted = failures, omitPending
		failures, omitPending = nil, false
	}
	req.Progress = func(progress dbdriver.ScriptProgress) {
		if executed := progress.Succeeded + progress.Failed; executed/runSQLFileLogEvery > logged {
			logged = executed / runSQLFileLogEvery
			log.Info("run sql file progress", zap.Int64("succeeded", progress.Succeeded), zap.Int64("failed", progress.Failed))
		}
		if time.Since(lastEmit) < runSQLFileProgressInterval {
			return
		}
		lastEmit = time.Now()
		flushFailures()
		event.Progress = progress
		event.ElapsedMs = time.Since(start).Milliseconds()
		emitRunSQLFileEvent(q.ctx, event)
	}

	result, err := panel.session.RunScript(panel.ctx, req)
	finished = true
	err = panel.finish(err)
	if result != nil {
		event.Progress = result.ScriptProgress
	}
	event.ElapsedMs = time.Since(start).Milliseconds()
	flushFailures()
	fields := []zap.Field{zap.Int64("succeeded", event.Progress.Succeeded), zap.Int64("failed", event.Progress.Failed),
		zap.Duration("elapsed", time.Since(start))}
	var encodingErr *dbdriver.ScriptEncodingError
	switch {
	case errors.As(err, &encodingErr):
		event.Status, event.Error, event.InvalidUTF8Line = RunSQLFileError, err.Error(), encodingErr.Line
		log.Warn("run sql file refused a non-UTF-8 file", zap.Int("line", encodingErr.Line))
	case err != nil:
		event.Status, event.Error = RunSQLFileError, err.Error()
		log.Error("run sql file failed", append(fields, zap.Error(err))...)
	case result.Failure != nil:
		event.Status, event.Failure = RunSQLFileFailed, result.Failure
		log.Warn("run sql file stopped at a failed statement", append(fields,
			zap.Int64("statement", result.Failure.Number), zap.Int("line", result.Failure.Line), zap.String("error", result.Failure.Error))...)
	case result.Stopped:
		event.Status = RunSQLFileStopped
		log.Info("run sql file stopped", fields...)
	default:
		event.Status = RunSQLFileDone
		log.Info("run sql file end", fields...)
	}
	emitRunSQLFileEvent(q.ctx, event)
}

func closeRunFile(ctx context.Context, file *os.File) {
	if err := file.Close(); err != nil {
		logger.Ctx(ctx).Warn("close sql file", zap.Error(err))
	}
}
