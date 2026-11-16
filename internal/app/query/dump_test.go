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

	"github.com/cago-frame/cago/configs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// dumpEvents 截下转储发出的事件。
type dumpEvents struct {
	mu     sync.Mutex
	events []DumpSQLFileEvent
	done   chan DumpSQLFileEvent
	// onRunning 在每个进行中的事件上调用（在转储的 goroutine 上），测试用它在中途取消。
	onRunning func(DumpSQLFileEvent)
}

func captureDumpEvents(t *testing.T) *dumpEvents {
	t.Helper()
	captured := &dumpEvents{done: make(chan DumpSQLFileEvent, 1)}
	orig := emitDumpSQLFileEvent
	emitDumpSQLFileEvent = func(_ context.Context, event DumpSQLFileEvent) {
		captured.mu.Lock()
		captured.events = append(captured.events, event)
		onRunning := captured.onRunning
		captured.mu.Unlock()
		if event.Status != DumpSQLFileRunning {
			captured.done <- event
		} else if onRunning != nil {
			onRunning(event)
		}
	}
	t.Cleanup(func() { emitDumpSQLFileEvent = orig })
	return captured
}

func (e *dumpEvents) wait(t *testing.T) DumpSQLFileEvent {
	t.Helper()
	select {
	case event := <-e.done:
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("the dump did not finish")
		return DumpSQLFileEvent{}
	}
}

// newNamedPanelQuery 同 newPanelQuery，资产带上名字（注释头里写它）。
func newNamedPanelQuery(t *testing.T, name string, cfg *asset_entity.DatabaseConfig) *Query {
	t.Helper()
	asset := &asset_entity.Asset{ID: 1, Name: name, Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, asset.SetDatabaseConfig(cfg))
	repo := mock_asset_repo.NewMockAssetRepo(gomock.NewController(t))
	repo.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(repo)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
	q := &Query{ctx: context.Background(), lang: stubLang{}, dbPanelCache: connpool.NewAssetConnCache[dbdriver.Session]("database", time.Minute)}
	t.Cleanup(func() { _ = q.dbPanelCache.Close() })
	return q
}

func dumpingFake() *dbdrivertest.Fake {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapViewDDL, dbdriver.CapExportSQL, dbdriver.CapDumpSQL)
	return fake
}

// 在 SQLite 上转储选中的表：文件写在选的位置，注释头写着 OpsKat 版本、资产名与库名；每个对象写完都有
// 一个事件（日志据此写一行），终态带着对象数、行数与文件大小。
func TestDumpSQLFileWritesTheSelectedTables(t *testing.T) {
	events := captureDumpEvents(t)
	q := newNamedPanelQuery(t, "local-sqlite", &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	for _, statement := range []string{
		"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)",
		"INSERT INTO t (name) VALUES ('a'), ('b')",
		"CREATE TABLE other (x INT)",
	} {
		_, err := q.ExecuteSQL(1, statement, "main")
		require.NoError(t, err)
	}
	path := filepath.Join(t.TempDir(), "t.sql")

	dumpID, err := q.StartDumpSQLFile(1, DumpSQLFileRequest{Database: "main", Tables: []string{"t"}, WithData: true, Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	assert.Equal(t, dumpID, final.DumpID)
	assert.Equal(t, DumpSQLFileDone, final.Status)
	content, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	require.NotNil(t, final.Result)
	assert.Equal(t, dbdriver.DumpResult{Objects: 1, Rows: 2, Bytes: int64(len(content))}, *final.Result)
	text := string(content)
	assert.Contains(t, text, "-- OpsKat version: "+configs.Version+"\n-- Asset: local-sqlite\n-- Database: main\n-- Content: structure and data\n")
	assert.Contains(t, text, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT);")
	assert.NotContains(t, text, "other")
	events.mu.Lock()
	defer events.mu.Unlock()
	var finished []dbdriver.DumpedObject
	for _, event := range events.events {
		if event.Progress.Finished != nil {
			finished = append(finished, *event.Progress.Finished)
		}
	}
	assert.Equal(t, []dbdriver.DumpedObject{{Name: "t", Kind: dbdriver.DumpTable, Data: true, Rows: 2}}, finished)
}

// 转储整个库：不用选表，库里的表、视图与触发器都写进文件，触发器在数据之后；每个对象写完都有事件。
func TestDumpSQLFileWritesAWholeDatabase(t *testing.T) {
	events := captureDumpEvents(t)
	q := newNamedPanelQuery(t, "local-sqlite", &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	for _, statement := range []string{
		"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)",
		"CREATE TABLE log (t_id INT)",
		"CREATE TRIGGER t_log AFTER INSERT ON t BEGIN INSERT INTO log VALUES (new.id); END",
		"CREATE VIEW v AS SELECT name FROM t",
		"INSERT INTO t (name) VALUES ('a')",
	} {
		_, err := q.ExecuteSQL(1, statement, "main")
		require.NoError(t, err)
	}
	path := filepath.Join(t.TempDir(), "main.sql")

	_, err := q.StartDumpSQLFile(1, DumpSQLFileRequest{Database: "main", Whole: true, WithData: true, Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	require.Equal(t, DumpSQLFileDone, final.Status, final.Error)
	require.NotNil(t, final.Result)
	assert.Equal(t, 4, final.Result.Objects)
	content, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	text := string(content)
	assert.Contains(t, text, "CREATE VIEW v AS SELECT name FROM t;")
	require.Contains(t, text, "-- Records of t\n")
	assert.Greater(t, strings.Index(text, "CREATE TRIGGER t_log"), strings.Index(text, "-- Records of t\n"))
	events.mu.Lock()
	defer events.mu.Unlock()
	var kinds []string
	for _, event := range events.events {
		if event.Progress.Finished != nil {
			kinds = append(kinds, event.Progress.Finished.Kind+" "+event.Progress.Finished.Name)
		}
	}
	assert.Equal(t, []string{"table log", "table t", "view v", "trigger t_log"}, kinds)
}

// 注释头不写主机、端口与账号。
func TestDumpSQLFileKeepsConnectionDetailsOutOfTheFile(t *testing.T) {
	events := captureDumpEvents(t)
	dbdrivertest.Register(t, dumpingFake())
	q := newNamedPanelQuery(t, "prod", &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "db.internal.example", Port: 7777, Username: "dbadmin"})
	path := filepath.Join(t.TempDir(), "users.sql")

	_, err := q.StartDumpSQLFile(1, DumpSQLFileRequest{Database: "app", Tables: []string{"users"}, WithData: true, Path: path})
	require.NoError(t, err)
	require.Equal(t, DumpSQLFileDone, events.wait(t).Status)

	content, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	for _, secret := range []string{"db.internal.example", "7777", "dbadmin"} {
		assert.NotContains(t, string(content), secret)
	}
}

// 读对象出错：终态是 failed，说出停在哪个对象与错误原文；目标位置保持开始前的样子，临时文件不留下。
func TestDumpSQLFileFailureLeavesTheTargetAsItWas(t *testing.T) {
	events := captureDumpEvents(t)
	fake := dumpingFake()
	fake.TableErr = errors.New("invalid connection")
	dbdrivertest.Register(t, fake)
	q := newNamedPanelQuery(t, "db", &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})
	dir := t.TempDir()
	path := filepath.Join(dir, "users.sql")
	require.NoError(t, os.WriteFile(path, []byte("previous dump"), 0o600))

	_, err := q.StartDumpSQLFile(1, DumpSQLFileRequest{Database: "app", Tables: []string{"users", "orders"}, WithData: true, Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	assert.Equal(t, DumpSQLFileFailed, final.Status)
	assert.Equal(t, "users", final.Object)
	assert.Contains(t, final.Error, "invalid connection")
	content, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	assert.Equal(t, "previous dump", string(content))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// 驱动没有声明转储、要结构和数据而驱动生成不了行语句、没有选位置或没有表：在开始之前就拒绝，不打开会话。
func TestDumpSQLFileIsRefusedBeforeStarting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.sql")
	for name, tc := range map[string]struct {
		capabilities []dbdriver.Capability
		request      DumpSQLFileRequest
	}{
		"a driver without the feature": {
			capabilities: []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapExportSQL},
			request:      DumpSQLFileRequest{Tables: []string{"users"}, Path: path},
		},
		"data from a driver without row statements": {
			capabilities: []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapDumpSQL},
			request:      DumpSQLFileRequest{Tables: []string{"users"}, WithData: true, Path: path},
		},
		"no location": {
			capabilities: []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapExportSQL, dbdriver.CapDumpSQL},
			request:      DumpSQLFileRequest{Tables: []string{"users"}},
		},
		"no tables": {
			capabilities: []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapExportSQL, dbdriver.CapDumpSQL},
			request:      DumpSQLFileRequest{Path: path},
		},
		"tables together with a whole database": {
			capabilities: []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapExportSQL, dbdriver.CapDumpSQL},
			request:      DumpSQLFileRequest{Whole: true, Tables: []string{"users"}, Path: path},
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := dbdrivertest.NewFake()
			fake.Desc.Capabilities = append(fake.Desc.Capabilities, tc.capabilities...)
			dbdrivertest.Register(t, fake)
			q := newNamedPanelQuery(t, "db", &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

			_, err := q.StartDumpSQLFile(1, tc.request)
			require.Error(t, err)
			assert.Empty(t, fake.Sessions())
			assert.NoFileExists(t, path)
		})
	}
}

// sqliteDumpQuery 是一个有很多行的 SQLite 资产：转储要读好几块数据才写完。
func sqliteDumpQuery(t *testing.T) *Query {
	t.Helper()
	q := newNamedPanelQuery(t, "db", &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	_, err := q.ExecuteSQL(1, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)", "main")
	require.NoError(t, err)
	for i := range 20 {
		_, err := q.ExecuteSQL(1, fmt.Sprintf(
			"INSERT INTO t (name) SELECT 'row-%d' FROM (WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n WHERE x < 500) SELECT x FROM n)", i), "main")
		require.NoError(t, err)
	}
	return q
}

// 取消（对话框里点「取消」、关闭资产标签页）：转储停下，终态是 canceled 并说出停在哪个对象，目标位置
// 没有留下文件。
func TestCancelDumpSQLFileStopsTheDump(t *testing.T) {
	events := captureDumpEvents(t)
	q := sqliteDumpQuery(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "t.sql")
	var once sync.Once
	events.onRunning = func(event DumpSQLFileEvent) {
		once.Do(func() { require.NoError(t, q.CancelDumpSQLFile(event.DumpID)) })
	}

	_, err := q.StartDumpSQLFile(1, DumpSQLFileRequest{Database: "main", Tables: []string{"t"}, WithData: true, Path: path})
	require.NoError(t, err)
	final := events.wait(t)

	assert.Equal(t, DumpSQLFileCanceled, final.Status)
	assert.Equal(t, "t", final.Object)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// 退出应用（Cleanup）等同于取消：进行中的转储停下、不留文件，Cleanup 返回时它已经收尾（会话在连接
// 缓存关闭之前释放）。
func TestCleanupCancelsARunningDump(t *testing.T) {
	events := captureDumpEvents(t)
	q := sqliteDumpQuery(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "t.sql")
	cleaned := make(chan struct{})
	var once sync.Once
	events.onRunning = func(event DumpSQLFileEvent) {
		once.Do(func() {
			go func() {
				q.Cleanup()
				close(cleaned)
			}()
			// 等到 Cleanup 真的要求它停下，再让转储接着写。
			require.Eventually(t, func() bool { return dumpCanceled(event.DumpID) }, 10*time.Second, time.Millisecond)
		})
	}

	_, err := q.StartDumpSQLFile(1, DumpSQLFileRequest{Database: "main", Tables: []string{"t"}, WithData: true, Path: path})
	require.NoError(t, err)

	select {
	case <-cleaned:
	case <-time.After(15 * time.Second):
		t.Fatal("Cleanup did not return")
	}
	select {
	case final := <-events.done:
		assert.Equal(t, DumpSQLFileCanceled, final.Status)
	default:
		t.Fatal("Cleanup returned before the dump ended")
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// dumpCanceled 报告进行中的转储是否已被要求停下。
func dumpCanceled(id string) bool {
	dumpRunsMu.Lock()
	defer dumpRunsMu.Unlock()
	run, ok := dumpRuns[id]
	return ok && run.ctx.Err() != nil
}

// 选位置的默认文件名不能带路径分隔符（库名或表名里的 / 不能把文件放到别处）。
func TestDumpFileNameStaysAFileName(t *testing.T) {
	assert.Equal(t, "public.users.sql", dumpFileName("public.users.sql"))
	assert.Equal(t, "a_b_c.sql", dumpFileName("a/b\\c.sql"))
	assert.False(t, strings.ContainsAny(dumpFileName("../../etc/passwd"), `/\`))
}
