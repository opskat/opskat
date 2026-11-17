package auto_backup_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/service/backup_svc"
)

const (
	nudgeDelay   = 2 * time.Second
	firstBackoff = time.Minute
)

// ClientSnapshot 是只存在前端本地存储里的数据（JSON 字符串，空表示没有自定义项）。
type ClientSnapshot struct {
	Shortcuts    string
	CustomThemes string
}

// Options 是 Service 的依赖；生产环境由 Default() 组装，测试注入假实现。
type Options struct {
	Now          func() time.Time
	LoadSettings func() (Settings, error)
	// Export 生成完整备份数据（含凭据与前端快照）。
	Export    func(ctx context.Context, snap ClientSnapshot) (*backup_svc.BackupData, error)
	Targets   []Target
	StatePath string
}

// Phase 是一个位置在当前这一轮里的进度。
const (
	PhaseIdle    = ""
	PhaseQueued  = "queued"
	PhaseRunning = "running"
)

// TargetStatus 是给界面的单个位置状态。
type TargetStatus struct {
	TargetState
	Phase string `json:"phase"`
}

// Status 是给界面的整体状态。
type Status struct {
	Running         bool                     `json:"running"`
	Targets         map[string]*TargetStatus `json:"targets"`
	LastCheckAt     int64                    `json:"lastCheckAt"`
	LastCheckResult string                   `json:"lastCheckResult"`
	LastCheckError  string                   `json:"lastCheckError"`
	NextCheckAt     int64                    `json:"nextCheckAt"`
}

// Service 调度并执行自动备份。
type Service struct {
	opts Options

	runMu sync.Mutex // 同一时刻只有一轮检查

	mu           sync.Mutex
	state        State
	snapshot     ClientSnapshot
	snapshotSet  bool
	firstPending bool // 启动 / 设置变更后尚未做的那一次立即检查
	nudgeAt      time.Time
	running      bool
	phases       map[string]string
	wake         chan struct{}
	cancel       context.CancelFunc
	notify       func()
}

// New 构造 Service 并读取持久化的运行状态。
func New(opts Options) *Service {
	st, err := loadState(opts.StatePath)
	if err != nil {
		logger.Default().Warn("load auto backup state", zap.Error(err))
	}
	return &Service{
		opts:         opts,
		state:        st,
		firstPending: true,
		phases:       map[string]string{},
		wake:         make(chan struct{}, 1),
	}
}

// SetNotifier 设置状态变化时的回调（app 层用它推送事件给前端）。
func (s *Service) SetNotifier(fn func()) {
	s.mu.Lock()
	s.notify = fn
	s.mu.Unlock()
}

// Start 启动后台调度；ctx 取消或 Stop 后退出。
func (s *Service) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = cancel
	s.mu.Unlock()
	go s.loop(ctx)
}

// Stop 停止后台调度。
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// SetClientSnapshot 更新前端快照；第一次到达后才开始检查，之后的变化触发一次检查请求。
func (s *Service) SetClientSnapshot(snap ClientSnapshot) {
	s.mu.Lock()
	changed := !s.snapshotSet || snap != s.snapshot
	s.snapshot = snap
	s.snapshotSet = true
	s.mu.Unlock()
	if changed {
		s.Nudge()
	}
}

// Nudge 请求在 2 秒防抖后检查一次。
func (s *Service) Nudge() {
	s.mu.Lock()
	s.nudgeAt = s.opts.Now().Add(nudgeDelay)
	s.mu.Unlock()
	s.signal()
}

// Reschedule 在设置保存后调用：立即检查一次，此后从这一次起按间隔计时。
func (s *Service) Reschedule() {
	s.mu.Lock()
	s.firstPending = true
	s.mu.Unlock()
	s.signal()
	s.emit()
}

// ResetFingerprints 让所有位置记住的指纹失效（修改密码后调用），下一次检查会写新版本。
func (s *Service) ResetFingerprints() {
	s.mu.Lock()
	for _, ts := range s.state.Targets {
		ts.Fingerprint = ""
	}
	st := s.state.clone()
	s.mu.Unlock()
	s.persist(st)
}

// Status 返回当前状态。
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Status{
		Running:         s.running,
		Targets:         map[string]*TargetStatus{},
		LastCheckAt:     s.state.LastCheckAt,
		LastCheckResult: s.state.LastCheckResult,
		LastCheckError:  s.state.LastCheckError,
	}
	for _, t := range s.opts.Targets {
		id := t.ID()
		ts := TargetStatus{Phase: s.phases[id]}
		if st, ok := s.state.Targets[id]; ok {
			ts.TargetState = *st
		}
		out.Targets[id] = &ts
	}
	if at, ok := s.nextWakeLocked(); ok {
		out.NextCheckAt = at.Unix()
	}
	return out
}

// RunNow 立即对每个勾选且可用的位置写一个新版本（忽略「无变化」），等这一轮结束后返回。
func (s *Service) RunNow(ctx context.Context) error {
	settings, err := s.opts.LoadSettings()
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return fmt.Errorf("自动备份未开启")
	}
	s.RunCheck(ctx, true)
	return nil
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) emit() {
	s.mu.Lock()
	fn := s.notify
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *Service) loop(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		at, ok := s.nextWake()
		var delay time.Duration
		if ok {
			delay = max(at.Sub(s.opts.Now()), 0)
		} else {
			delay = 24 * time.Hour
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
			if at, ok := s.nextWake(); ok && !at.After(s.opts.Now()) {
				s.RunCheck(ctx, false)
			}
		}
	}
}

// nextWake 返回下一次应当检查的时间；关闭、设置读取失败或尚未收到前端快照时返回 false。
func (s *Service) nextWake() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextWakeLocked()
}

func (s *Service) nextWakeLocked() (time.Time, bool) {
	if !s.snapshotSet {
		return time.Time{}, false
	}
	settings, err := s.opts.LoadSettings()
	if err != nil || !settings.Enabled {
		return time.Time{}, false
	}
	now := s.opts.Now()
	if s.firstPending {
		return now, true
	}
	next := time.Unix(s.state.LastCheckAt, 0).Add(settings.Interval)
	consider := func(unix int64) {
		if unix > 0 && time.Unix(unix, 0).Before(next) {
			next = time.Unix(unix, 0)
		}
	}
	consider(s.state.CheckRetryAt)
	for _, id := range settings.Targets {
		if ts, ok := s.state.Targets[id]; ok {
			consider(ts.RetryAt)
		}
	}
	if !s.nudgeAt.IsZero() && s.nudgeAt.Before(next) {
		next = s.nudgeAt
	}
	return next.In(now.Location()), true
}

func backoff(failures int, limit time.Duration) time.Duration {
	d := firstBackoff
	for i := 1; i < failures && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// fingerprint 是备份明文（不含导出时间）的 SHA-256。
func fingerprint(data *backup_svc.BackupData) (string, error) {
	clone := *data
	clone.ExportedAt = ""
	raw, err := json.Marshal(&clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// RunCheck 执行一轮检查；force 时忽略「无变化」与退避。
func (s *Service) RunCheck(ctx context.Context, force bool) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	settings, err := s.opts.LoadSettings()
	if err != nil {
		logger.Default().Warn("auto backup: load settings", zap.Error(err))
		return
	}
	if !settings.Enabled {
		return
	}
	started := s.opts.Now()

	s.mu.Lock()
	s.firstPending = false
	s.nudgeAt = time.Time{}
	snap := s.snapshot
	s.mu.Unlock()

	var due []Target
	for _, t := range s.opts.Targets {
		if settings.Selected(t.ID()) && t.Available(settings) {
			due = append(due, t)
		}
	}

	data, err := s.opts.Export(ctx, snap)
	var fp string
	if err == nil {
		fp, err = fingerprint(data)
	}
	if err != nil {
		s.finishCheck(started, settings, CheckFailed, fmt.Sprintf("生成备份失败: %v", err), true)
		logger.Default().Warn("auto backup: export failed", zap.Error(err))
		return
	}

	// 选出本轮要写的位置。
	s.mu.Lock()
	var toWrite []Target
	for _, t := range due {
		ts := s.state.target(t.ID())
		if !force {
			if ts.RetryAt > 0 && started.Unix() < ts.RetryAt {
				continue
			}
			if ts.Fingerprint == fp && ts.LastError == "" {
				continue
			}
		}
		toWrite = append(toWrite, t)
		s.phases[t.ID()] = PhaseQueued
	}
	if len(toWrite) > 0 {
		s.running = true
	}
	s.mu.Unlock()

	if len(toWrite) == 0 {
		s.finishCheck(started, settings, CheckUnchanged, "", false)
		logger.Default().Info("auto backup: unchanged, skipped", zap.Int("targets", len(due)))
		return
	}
	s.emit()

	data.ExportedAt = started.UTC().Format(time.RFC3339)
	plain, err := json.Marshal(data)
	var content []byte
	if err == nil {
		content, err = backup_svc.EncryptBackup(plain, settings.Password)
	}
	if err != nil {
		s.clearPhases()
		s.finishCheck(started, settings, CheckFailed, fmt.Sprintf("加密备份失败: %v", err), true)
		logger.Default().Warn("auto backup: encrypt failed", zap.Error(err))
		return
	}

	failed := false
	for _, t := range toWrite {
		id := t.ID()
		s.mu.Lock()
		s.phases[id] = PhaseRunning
		ref := s.state.target(id).Ref
		s.mu.Unlock()
		s.emit()

		begin := s.opts.Now()
		newRef, werr := t.Write(ctx, WriteRequest{Content: content, At: started, Keep: settings.Keep, Settings: settings, Ref: ref})
		now := s.opts.Now()

		s.mu.Lock()
		ts := s.state.target(id)
		ts.Ref = newRef
		if werr != nil {
			failed = true
			ts.Failures++
			ts.LastError = werr.Error()
			ts.LastErrorAt = now.Unix()
			ts.RetryAt = now.Add(backoff(ts.Failures, settings.Interval)).Unix()
		} else {
			ts.Fingerprint = fp
			ts.LastSuccessAt = now.Unix()
			ts.LastError, ts.LastErrorAt, ts.Failures, ts.RetryAt = "", 0, 0, 0
		}
		delete(s.phases, id)
		s.mu.Unlock()

		if werr != nil {
			logger.Default().Warn("auto backup: target failed", zap.String("target", id), zap.Duration("took", now.Sub(begin)), zap.Error(werr))
		} else {
			logger.Default().Info("auto backup: target written", zap.String("target", id), zap.Duration("took", now.Sub(begin)), zap.Int("bytes", len(content)))
		}
	}

	result := CheckWritten
	if failed {
		result = CheckFailed
	}
	s.finishCheck(started, settings, result, "", false)
}

func (s *Service) clearPhases() {
	s.mu.Lock()
	s.phases = map[string]string{}
	s.mu.Unlock()
}

// finishCheck 记录本轮结果并持久化；exportFailed 时按退避安排重试。
func (s *Service) finishCheck(at time.Time, settings Settings, result, checkErr string, exportFailed bool) {
	s.mu.Lock()
	s.running = false
	s.state.LastCheckAt = at.Unix()
	s.state.LastCheckResult = result
	s.state.LastCheckError = checkErr
	if exportFailed {
		s.state.CheckFailures++
		s.state.CheckRetryAt = at.Add(backoff(s.state.CheckFailures, settings.Interval)).Unix()
	} else {
		s.state.CheckFailures, s.state.CheckRetryAt = 0, 0
	}
	st := s.state.clone()
	s.mu.Unlock()
	s.persist(st)
	s.emit()
}

func (s *Service) persist(st State) {
	if err := saveState(s.opts.StatePath, st); err != nil {
		logger.Default().Warn("save auto backup state", zap.Error(err))
	}
}
