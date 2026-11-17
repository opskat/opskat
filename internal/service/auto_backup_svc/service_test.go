package auto_backup_svc

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/service/backup_svc"
)

type fakeTarget struct {
	mu        sync.Mutex
	id        string
	available bool
	err       error
	writes    []WriteRequest
	ref       string
}

func (f *fakeTarget) ID() string              { return f.id }
func (f *fakeTarget) Available(Settings) bool { return f.available }
func (f *fakeTarget) Validate(Settings) error { return nil }
func (f *fakeTarget) count() int              { f.mu.Lock(); defer f.mu.Unlock(); return len(f.writes) }
func (f *fakeTarget) last() WriteRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[len(f.writes)-1]
}
func (f *fakeTarget) setErr(err error) { f.mu.Lock(); defer f.mu.Unlock(); f.err = err }
func (f *fakeTarget) Write(_ context.Context, req WriteRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	if f.err != nil {
		return req.Ref, f.err
	}
	if f.ref != "" {
		return f.ref, nil
	}
	return req.Ref, nil
}

type fixture struct {
	svc       *Service
	now       time.Time
	settings  Settings
	data      *backup_svc.BackupData
	exportErr error
	a, b      *fakeTarget
	statePath string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{
		now: time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC),
		settings: Settings{
			Enabled:  true,
			Password: "pw",
			Interval: 30 * time.Minute,
			Keep:     10,
			Targets:  []string{"a", "b"},
		},
		data:      &backup_svc.BackupData{Version: "1.0"},
		a:         &fakeTarget{id: "a", available: true},
		b:         &fakeTarget{id: "b", available: true},
		statePath: filepath.Join(t.TempDir(), "auto_backup_state.json"),
	}
	f.svc = New(Options{
		Now:          func() time.Time { return f.now },
		LoadSettings: func() (Settings, error) { return f.settings, nil },
		Export: func(context.Context, ClientSnapshot) (*backup_svc.BackupData, error) {
			if f.exportErr != nil {
				return nil, f.exportErr
			}
			clone := *f.data
			return &clone, nil
		},
		Targets:   []Target{f.a, f.b},
		StatePath: f.statePath,
	})
	f.svc.SetClientSnapshot(ClientSnapshot{})
	return f
}

func (f *fixture) check() { f.svc.RunCheck(context.Background(), false) }

func TestRunCheck(t *testing.T) {
	Convey("自动备份检查", t, func() {
		f := newFixture(t)

		Convey("首次检查写入所有勾选且可用的位置，内容用密码加密", func() {
			f.check()
			So(f.a.count(), ShouldEqual, 1)
			So(f.b.count(), ShouldEqual, 1)
			req := f.a.last()
			So(req.Keep, ShouldEqual, 10)
			So(req.At, ShouldEqual, f.now)
			plain, err := backup_svc.DecryptBackup(req.Content, "pw")
			So(err, ShouldBeNil)
			So(string(plain), ShouldContainSubstring, `"version":"1.0"`)
			st := f.svc.Status()
			So(st.Targets["a"].LastSuccessAt, ShouldEqual, f.now.Unix())
			So(st.LastCheckResult, ShouldEqual, CheckWritten)
		})

		Convey("数据未变化时跳过，导出时间不算变化", func() {
			f.check()
			f.now = f.now.Add(30 * time.Minute)
			f.check()
			So(f.a.count(), ShouldEqual, 1)
			So(f.svc.Status().LastCheckResult, ShouldEqual, CheckUnchanged)
		})

		Convey("数据变化后写入新版本", func() {
			f.check()
			f.data.Version = "1.1"
			f.check()
			So(f.a.count(), ShouldEqual, 2)
		})

		Convey("一个位置失败不影响另一个，失败的按退避重试", func() {
			f.a.setErr(errors.New("507 Insufficient Storage"))
			f.check()
			So(f.b.count(), ShouldEqual, 1)
			st := f.svc.Status()
			So(st.Targets["a"].LastError, ShouldContainSubstring, "507")
			So(st.Targets["a"].RetryAt, ShouldEqual, f.now.Add(time.Minute).Unix())
			So(st.LastCheckResult, ShouldEqual, CheckFailed)

			Convey("退避未到期时不重试", func() {
				f.now = f.now.Add(30 * time.Second)
				f.check()
				So(f.a.count(), ShouldEqual, 1)
			})

			Convey("连续失败退避翻倍，且不超过检查间隔", func() {
				for i := 0; i < 8; i++ {
					f.now = time.Unix(f.svc.Status().Targets["a"].RetryAt, 0).UTC()
					f.check()
				}
				st := f.svc.Status()
				So(st.Targets["a"].RetryAt-f.now.Unix(), ShouldEqual, int64((30 * time.Minute).Seconds()))
				So(f.b.count(), ShouldEqual, 1)
			})

			Convey("到期重试成功后清除错误并重置退避", func() {
				f.a.setErr(nil)
				f.now = f.now.Add(time.Minute)
				f.check()
				st := f.svc.Status()
				So(f.a.count(), ShouldEqual, 2)
				So(st.Targets["a"].LastError, ShouldBeEmpty)
				So(st.Targets["a"].RetryAt, ShouldEqual, 0)
				So(st.Targets["a"].LastSuccessAt, ShouldEqual, f.now.Unix())
			})
		})

		Convey("不可用的位置跳过且不记错误", func() {
			f.b.available = false
			f.check()
			So(f.b.count(), ShouldEqual, 0)
			So(f.svc.Status().Targets["b"].LastError, ShouldBeEmpty)
			So(f.svc.Status().LastCheckResult, ShouldEqual, CheckWritten)
		})

		Convey("未勾选的位置不写", func() {
			f.settings.Targets = []string{"a"}
			f.check()
			So(f.b.count(), ShouldEqual, 0)
		})

		Convey("修改密码后指纹失效，下一次检查用新密码写新版本", func() {
			f.check()
			f.svc.ResetFingerprints()
			f.settings.Password = "new-pw"
			f.check()
			So(f.a.count(), ShouldEqual, 2)
			_, err := backup_svc.DecryptBackup(f.a.last().Content, "new-pw")
			So(err, ShouldBeNil)
		})

		Convey("立即备份忽略「无变化」", func() {
			f.check()
			f.svc.RunCheck(context.Background(), true)
			So(f.a.count(), ShouldEqual, 2)
		})

		Convey("关闭后不写", func() {
			f.settings.Enabled = false
			f.check()
			So(f.a.count(), ShouldEqual, 0)
		})

		Convey("生成备份数据失败时所有位置都不写并记录原因", func() {
			f.exportErr = errors.New("decrypt credential failed")
			f.check()
			So(f.a.count(), ShouldEqual, 0)
			st := f.svc.Status()
			So(st.LastCheckResult, ShouldEqual, CheckFailed)
			So(st.LastCheckError, ShouldContainSubstring, "decrypt credential failed")
		})

		Convey("位置返回的引用（Gist ID）被记住并在下次传回", func() {
			f.a.ref = "gist-1"
			f.check()
			f.a.ref = ""
			f.data.Version = "2"
			f.check()
			So(f.a.last().Ref, ShouldEqual, "gist-1")
		})

		Convey("状态持久化：重启后不因重启重复上传", func() {
			f.check()
			g := &fixture{}
			*g = *f
			g.a = &fakeTarget{id: "a", available: true}
			g.b = &fakeTarget{id: "b", available: true}
			g.svc = New(Options{
				Now:          func() time.Time { return f.now },
				LoadSettings: func() (Settings, error) { return f.settings, nil },
				Export: func(context.Context, ClientSnapshot) (*backup_svc.BackupData, error) {
					clone := *f.data
					return &clone, nil
				},
				Targets:   []Target{g.a, g.b},
				StatePath: f.statePath,
			})
			g.svc.SetClientSnapshot(ClientSnapshot{})
			g.check()
			So(g.a.count(), ShouldEqual, 0)
			So(g.svc.Status().Targets["a"].LastSuccessAt, ShouldEqual, f.now.Unix())
		})
	})
}

func TestNextWake(t *testing.T) {
	Convey("下一次唤醒时间", t, func() {
		f := newFixture(t)

		Convey("未收到前端快照前不安排检查", func() {
			svc := New(Options{
				Now:          func() time.Time { return f.now },
				LoadSettings: func() (Settings, error) { return f.settings, nil },
				Targets:      []Target{f.a},
				StatePath:    filepath.Join(t.TempDir(), "s.json"),
			})
			_, ok := svc.nextWake()
			So(ok, ShouldBeFalse)
		})

		Convey("收到快照后立即检查一次，此后按间隔", func() {
			at, ok := f.svc.nextWake()
			So(ok, ShouldBeTrue)
			So(at, ShouldEqual, f.now)
			f.check()
			at, _ = f.svc.nextWake()
			So(at, ShouldEqual, f.now.Add(30*time.Minute))
		})

		Convey("重试时间早于下一次检查时提前唤醒", func() {
			f.a.setErr(errors.New("boom"))
			f.check()
			at, _ := f.svc.nextWake()
			So(at, ShouldEqual, f.now.Add(time.Minute))
		})

		Convey("请求检查在 2 秒防抖后唤醒", func() {
			f.check()
			f.svc.Nudge()
			at, _ := f.svc.nextWake()
			So(at, ShouldEqual, f.now.Add(2*time.Second))
		})

		Convey("关闭时不安排检查", func() {
			f.settings.Enabled = false
			_, ok := f.svc.nextWake()
			So(ok, ShouldBeFalse)
		})
	})
}

func TestLoopRunsAfterFirstSnapshot(t *testing.T) {
	Convey("后台调度：收到前端快照后执行第一次检查", t, func() {
		a := &fakeTarget{id: "a", available: true}
		svc := New(Options{
			Now: time.Now,
			LoadSettings: func() (Settings, error) {
				return Settings{Enabled: true, Password: "pw", Interval: time.Hour, Keep: 10, Targets: []string{"a"}}, nil
			},
			Export: func(context.Context, ClientSnapshot) (*backup_svc.BackupData, error) {
				return &backup_svc.BackupData{Version: "1.0"}, nil
			},
			Targets:   []Target{a},
			StatePath: filepath.Join(t.TempDir(), "s.json"),
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		svc.Start(ctx)

		time.Sleep(200 * time.Millisecond)
		So(a.count(), ShouldEqual, 0)

		svc.SetClientSnapshot(ClientSnapshot{Shortcuts: `{"k":1}`})
		deadline := time.Now().Add(20 * time.Second)
		for a.count() == 0 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		So(a.count(), ShouldEqual, 1)
	})
}
