package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/configs"
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

// DumpSQLFileEventName 是转储 SQL 文件的事件名：每次转储的进度与终态都发在它上面，以 dumpId 区分。
// 前端在 StartDumpSQLFile 之前订阅，拿到 dumpId 后只看自己的那次转储。
const DumpSQLFileEventName = "query:dump-sql-file"

// DumpSQLFileStatus 是一次转储的状态。除了 running，其余都是终态，每次转储恰好发一个终态事件。
type DumpSQLFileStatus string

const (
	// DumpSQLFileRunning：进行中的进度（每个对象开始与写完时各一个，读行的中途按时间间隔）。
	DumpSQLFileRunning DumpSQLFileStatus = "running"
	// DumpSQLFileDone：写完，文件已在选的位置（Result）。
	DumpSQLFileDone DumpSQLFileStatus = "done"
	// DumpSQLFileFailed：出错停下（Object 是停在的对象，Error 是原因），目标位置保持开始前的样子。
	DumpSQLFileFailed DumpSQLFileStatus = "failed"
	// DumpSQLFileCanceled：被取消（点「取消」、关闭资产标签页、退出应用），目标位置保持开始前的样子。
	DumpSQLFileCanceled DumpSQLFileStatus = "canceled"
)

// DumpSQLFileEvent 是转储发给前端的一个事件。
type DumpSQLFileEvent struct {
	DumpID    string                `json:"dumpId"`
	Status    DumpSQLFileStatus     `json:"status"`
	Progress  dbdriver.DumpProgress `json:"progress"`
	ElapsedMs int64                 `json:"elapsedMs"`
	// Result 是 done 时写完的文件。
	Result *dbdriver.DumpResult `json:"result,omitempty"`
	// Object 是 failed / canceled 时停在的对象；停在对象之外（还没开始写任何对象）时为空。
	Object string `json:"object,omitempty"`
	// Error 是 failed 时的原因。
	Error string `json:"error,omitempty"`
}

// DumpSQLFileRequest 是开始转储的参数。
type DumpSQLFileRequest struct {
	Database string `json:"database"`
	// Schema 是转储的 schema（有 schema 分组的驱动）：Whole 时非空表示转储整个 schema；转储选中的表时为空。
	Schema string `json:"schema"`
	// Tables 是选中的表与视图，用 ListTables 给出的形式；Whole 时为空。
	Tables []string `json:"tables"`
	// Whole 为真时转储整个库（Schema 为空）或整个 schema：全部表、视图、函数、过程与触发器。
	Whole bool `json:"whole"`
	// WithData 为真时是「结构和数据」，否则是「仅结构」。
	WithData bool `json:"withData"`
	// Options 是对话框里其余的导出选项。视图、函数与过程、触发器只在 Whole 时可选；关闭外键检查要驱动声明了
	// DumpDisablesForeignKeyChecks，且包含外键。
	Options dbdriver.DumpOptions `json:"options"`
	// Path 是本机上要写的文件（SelectDumpFile 选出的）。
	Path string `json:"path"`
}

// dumpTimeout 是一次转储的时限。转储由用户取消，不受资产的查询超时限制；这个时限只收回被遗忘、一直
// 没有结束的转储。
const dumpTimeout = 24 * time.Hour

// dumpProgressInterval 是读行中途进度事件的最小间隔；每个对象开始与写完的事件不受它限制。
const dumpProgressInterval = 200 * time.Millisecond

// emitDumpSQLFileEvent 发出一个事件；测试里替换它来截下事件。
var emitDumpSQLFileEvent = func(ctx context.Context, event DumpSQLFileEvent) {
	wailsRuntime.EventsEmit(ctx, DumpSQLFileEventName, event)
}

// dumpRun 是一次进行中的转储。
type dumpRun struct {
	ctx    context.Context
	cancel context.CancelFunc
}

var (
	dumpRunsMu sync.Mutex
	// dumpRuns 是进行中的转储：dumpId → 它。
	dumpRuns = map[string]*dumpRun{}
	// dumpRunsWG 在每次转储收尾（临时文件删掉、会话释放）后完成：退出应用时等它们停下。
	dumpRunsWG sync.WaitGroup
)

// dumpStopWait 是退出应用时等进行中的转储停下的上限。
const dumpStopWait = 10 * time.Second

// cancelAllDumps 取消所有进行中的转储，并等它们收尾（至多 dumpStopWait）：退出应用等同于点「取消」，
// 未完成的文件要删掉，会话要在连接缓存关闭之前释放。
func cancelAllDumps() {
	dumpRunsMu.Lock()
	for _, run := range dumpRuns {
		run.cancel()
	}
	dumpRunsMu.Unlock()
	finished := make(chan struct{})
	go func() {
		dumpRunsWG.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(dumpStopWait):
		logger.Default().Warn("dump sql file did not stop before quitting", zap.Duration("wait", dumpStopWait))
	}
}

// dumpFileName 把默认文件名里的路径分隔符换成下划线：库名或表名里的 / 不能把文件放到别的目录。
func dumpFileName(name string) string {
	return strings.NewReplacer("/", "_", `\`, "_").Replace(name)
}

// SelectDumpFile 打开原生的保存文件对话框（默认文件名 defaultFilename，筛选 *.sql），返回选中的路径；
// 用户取消时返回空串。目标已存在时由系统对话框询问是否覆盖。
func (q *Query) SelectDumpFile(defaultFilename string) (string, error) {
	path, err := wailsRuntime.SaveFileDialog(q.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Dump SQL File",
		DefaultFilename: dumpFileName(defaultFilename),
		Filters:         []wailsRuntime.FileFilter{{DisplayName: "SQL (*.sql)", Pattern: "*.sql"}},
	})
	if err != nil {
		return "", fmt.Errorf("save file dialog failed: %w", err)
	}
	return path, nil
}

// StartDumpSQLFile 在资产的库上开始转储选中的表（或整个库、整个 schema），返回这次转储的 id；进度与终态
// 经 DumpSQLFileEventName 事件发出。驱动没有声明转储（或要结构和数据而驱动生成不了行语句）时在开始之前拒绝；转储不受资产只读
// 连接的影响。不写审计日志；日志只记开始、结束、对象数、行数与耗时，不记语句与数据。
func (q *Query) StartDumpSQLFile(assetID int64, request DumpSQLFileRequest) (string, error) {
	ctx := i18n.Ctx(q.ctx, q.lang.Lang())
	log := logger.Ctx(ctx).With(zap.String("op", "dump-sql-file"), zap.Int64("assetID", assetID), zap.String("database", request.Database))
	if request.Path == "" {
		return "", errors.New("没有选择保存位置")
	}
	if request.Whole && len(request.Tables) > 0 {
		return "", errors.New("转储整个库或 schema 时不能另选表")
	}
	if !request.Whole && len(request.Tables) == 0 {
		return "", errors.New("没有要转储的表")
	}
	assetName, err := checkDumpAllowed(ctx, assetID, request)
	if err != nil {
		log.Warn("dump sql file refused", zap.Error(err))
		return "", err
	}
	panel, err := q.acquirePanelSession(assetID, request.Database, func(*asset_entity.DatabaseConfig) time.Duration { return dumpTimeout })
	if err != nil {
		log.Error("dump sql file open failed", zap.Error(err))
		return "", err
	}

	dumpID := uuid.NewString()
	runCtx, cancel := context.WithCancel(panel.ctx)
	dumpRunsMu.Lock()
	dumpRuns[dumpID] = &dumpRun{ctx: runCtx, cancel: cancel}
	dumpRunsMu.Unlock()
	dumpRunsWG.Add(1)

	log = log.With(zap.String("dumpID", dumpID))
	log.Info("dump sql file start", zap.Int("tables", len(request.Tables)), zap.Bool("whole", request.Whole),
		zap.String("schema", request.Schema), zap.Bool("withData", request.WithData),
		zap.Bool("indexes", request.Options.Indexes), zap.Bool("foreignKeys", request.Options.ForeignKeys), zap.Bool("views", request.Options.Views),
		zap.Bool("routines", request.Options.Routines), zap.Bool("triggers", request.Options.Triggers), zap.Bool("drops", request.Options.Drops),
		zap.Bool("disableForeignKeyChecks", request.Options.DisableForeignKeyChecks))
	go q.dumpSQLFile(runCtx, dumpID, log, panel, request.Path, dbdriver.DumpRequest{
		Database: panel.database, Schema: request.Schema, Tables: request.Tables, WithData: request.WithData, Whole: request.Whole,
		Options: request.Options,
		Header:  dbdriver.DumpHeader{Version: configs.Version, Asset: assetName, Time: time.Now()},
	})
	return dumpID, nil
}

// CancelDumpSQLFile 取消一次转储：它立即停下，未完成的文件被删掉，终态为 canceled。转储已经结束时
// 什么也不做。
func (q *Query) CancelDumpSQLFile(dumpID string) error {
	dumpRunsMu.Lock()
	run, ok := dumpRuns[dumpID]
	dumpRunsMu.Unlock()
	if ok {
		run.cancel()
	}
	return nil
}

// checkDumpAllowed 检查资产能不能这样转储，返回资产名：驱动没有声明转储时不能，要结构和数据而驱动生成不了
// 行语句时不能，要关闭外键检查而驱动不能在会话里关闭时不能；选项彼此不合（见 checkDumpOptions）时也不能。
func checkDumpAllowed(ctx context.Context, assetID int64, request DumpSQLFileRequest) (string, error) {
	if err := checkDumpOptions(request); err != nil {
		return "", err
	}
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return "", errors.New("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return "", fmt.Errorf("获取数据库配置失败: %w", err)
	}
	driver, err := dbregistry.Lookup(cfg.Driver)
	if err != nil {
		return "", err
	}
	desc := driver.Descriptor()
	if !desc.Supports(dbdriver.CapDumpSQL) {
		return "", fmt.Errorf("%w: driver %q does not dump SQL files", dbdriver.ErrUnsupported, cfg.Driver)
	}
	if request.WithData && !desc.GeneratesRowStatements() {
		return "", fmt.Errorf("%w: driver %q cannot write rows as statements", dbdriver.ErrUnsupported, cfg.Driver)
	}
	if request.Options.DisableForeignKeyChecks && !desc.DumpDisablesForeignKeyChecks {
		return "", fmt.Errorf("%w: driver %q cannot turn foreign key checks off in a dump file", dbdriver.ErrUnsupported, cfg.Driver)
	}
	return asset.Name, nil
}

// checkDumpOptions 拒绝对话框给不出的选项组合：转储选中的表时没有视图、函数与过程、触发器这几项（选中的视图
// 照样写），不包含外键时没有关闭外键检查。
func checkDumpOptions(request DumpSQLFileRequest) error {
	options := request.Options
	if !request.Whole && (options.Views || options.Routines || options.Triggers) {
		return errors.New("只转储选中的表时不能选视图、函数与过程或触发器")
	}
	if options.DisableForeignKeyChecks && !options.ForeignKeys {
		return errors.New("不包含外键时不能选关闭外键检查")
	}
	return nil
}

// dumpSQLFile 是一次转储的 goroutine：写文件，发出进度与终态，释放会话。recover 是这条 goroutine 的
// 边界：意外的 panic 记下来，并以 failed 终态结束，前端不会一直停在进行中（DumpToFile 没有走到收尾时，
// 临时文件可能留在目标旁边，但目标本身不变）。
func (q *Query) dumpSQLFile(ctx context.Context, dumpID string, log *zap.Logger, panel *panelSession, path string, req dbdriver.DumpRequest) {
	start := time.Now()
	event := DumpSQLFileEvent{DumpID: dumpID, Status: DumpSQLFileRunning}
	finished := false
	defer func() {
		defer dumpRunsWG.Done()
		dumpRunsMu.Lock()
		if run, ok := dumpRuns[dumpID]; ok {
			run.cancel()
			delete(dumpRuns, dumpID)
		}
		dumpRunsMu.Unlock()
		r := recover()
		if r == nil {
			return
		}
		log.Error("dump sql file panic recovered", zap.Any("panic", r), zap.Stack("stack"))
		if !finished {
			if err := panel.finish(nil); err != nil {
				log.Warn("release database session", zap.Error(err))
			}
		}
		event.Status, event.Error = DumpSQLFileFailed, fmt.Sprintf("转储时发生意外错误: %v", r)
		event.ElapsedMs = time.Since(start).Milliseconds()
		emitDumpSQLFileEvent(q.ctx, event)
	}()

	var lastEmit time.Time
	req.Progress = func(progress dbdriver.DumpProgress) {
		// 对象开始与写完的事件都发（对话框按它们写日志），读行中途的按时间间隔发。
		objectChanged := progress.Finished != nil || progress.Object != event.Progress.Object
		if !objectChanged && time.Since(lastEmit) < dumpProgressInterval {
			return
		}
		lastEmit = time.Now()
		event.Progress = progress
		event.ElapsedMs = time.Since(start).Milliseconds()
		emitDumpSQLFileEvent(q.ctx, event)
	}

	result, err := dbdriver.DumpToFile(ctx, panel.session, req, path)
	// 取消要在释放会话之前看：释放会话也会撤销 ctx。驱动在被取消的读取上报的未必是 ctx 的错误。
	canceled := errors.Is(ctx.Err(), context.Canceled)
	finished = true
	err = panel.finish(err)
	event.Progress.Finished = nil
	event.ElapsedMs = time.Since(start).Milliseconds()
	fields := []zap.Field{zap.Duration("elapsed", time.Since(start))}
	if result != nil {
		event.Progress.Rows = result.Rows
		fields = append(fields, zap.Int("objects", result.Objects), zap.Int64("rows", result.Rows))
	}
	var objectErr *dbdriver.DumpObjectError
	if errors.As(err, &objectErr) {
		event.Object = objectErr.Object
	}
	switch {
	case err == nil:
		event.Status, event.Result = DumpSQLFileDone, result
		log.Info("dump sql file end", append(fields, zap.Int64("bytes", result.Bytes))...)
	case canceled:
		event.Status = DumpSQLFileCanceled
		log.Info("dump sql file canceled", append(fields, zap.String("object", event.Object))...)
	default:
		// 停在对象上时对话框另外点出对象，原因只写数据库给的错误。
		event.Status, event.Error = DumpSQLFileFailed, err.Error()
		if objectErr != nil {
			event.Error = objectErr.Err.Error()
		}
		log.Error("dump sql file failed", append(fields, zap.String("object", event.Object), zap.Error(err))...)
	}
	emitDumpSQLFileEvent(q.ctx, event)
}
