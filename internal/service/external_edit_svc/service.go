package external_edit_svc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/repository/audit_repo"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

type Service struct {
	dataDir      string
	storageDir   string
	manifestPath string

	configProvider func() *bootstrap.AppConfig
	configSaver    func(cfg *bootstrap.AppConfig) error
	remote         RemoteFileService
	findSessions   func(assetID int64) []string
	assets         AssetFinder
	auditRepo      audit_repo.AuditRepo
	emit           func(Event)
	launch         Launcher
	now            func() time.Time

	mu               sync.RWMutex
	sessions         map[string]*Session
	watcher          *fsnotify.Watcher
	watchedDirs      map[string]int
	reconcileTimers  map[string]*time.Timer
	autoSaveTimers   map[string]*time.Timer
	autoSavePaused   map[string]bool
	autoSaveTried    map[string]string
	documentRunners  map[string]*sync.Mutex
	autoSaveCounters map[string]*autoSaveCounter
	cleanupTicker    *time.Ticker
	closeCh          chan struct{}
	closeOnce        sync.Once
	closed           bool
	bg               sync.WaitGroup
}

// goTracked 在后台 goroutine 中执行 fn，并登记到 s.bg，使 Close 能够等待其收尾。
// 调用方不得持有 s.mu。Close 之后不再派生新的后台 goroutine。
func (s *Service) goTracked(fn func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.bg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.bg.Done()
		fn()
	}()
}

// trackedAfterFunc 排程一个延时回调并登记到 s.bg，使 Close 能够等待其收尾。
// 调用方必须持有 s.mu。Close 之后返回 nil，调用方据此放弃排程。
func (s *Service) trackedAfterFunc(d time.Duration, fn func()) *time.Timer {
	if s.closed {
		return nil
	}
	s.bg.Add(1)
	return time.AfterFunc(d, func() {
		defer s.bg.Done()
		fn()
	})
}

// stopTrackedTimer 停止由 trackedAfterFunc 排程的定时器。
// 若回调尚未触发（Stop 返回 true），需要相应地补一次 Done 抵消登记。
// 调用方必须持有 s.mu，且每个定时器至多停止一次。
func (s *Service) stopTrackedTimer(t *time.Timer) {
	if t != nil && t.Stop() {
		s.bg.Done()
	}
}

type autoSaveCounter struct {
	count  int
	lastAt int64
}

func NewService(opts Options) (*Service, error) {
	if opts.DataDir == "" {
		opts.DataDir = bootstrap.AppDataDir()
	}
	if opts.ConfigProvider == nil {
		return nil, fmt.Errorf("missing config provider")
	}
	if opts.ConfigSaver == nil {
		return nil, fmt.Errorf("missing config saver")
	}
	if opts.Remote == nil {
		return nil, fmt.Errorf("missing remote file service")
	}
	if opts.Emit == nil {
		opts.Emit = func(Event) {}
	}
	if opts.Launch == nil {
		opts.Launch = launcherFunc(func(execPath string, args []string) error {
			cmd := exec.Command(execPath, args...) //nolint:gosec // path and args are validated before launch
			return cmd.Start()
		})
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	s := &Service{
		dataDir:          opts.DataDir,
		storageDir:       filepath.Join(opts.DataDir, "storage"),
		manifestPath:     filepath.Join(opts.DataDir, "storage", "manifest.json"),
		configProvider:   opts.ConfigProvider,
		configSaver:      opts.ConfigSaver,
		remote:           opts.Remote,
		findSessions:     opts.FindSessions,
		assets:           opts.Assets,
		auditRepo:        opts.Audit,
		emit:             opts.Emit,
		launch:           opts.Launch,
		now:              opts.Now,
		sessions:         make(map[string]*Session),
		watchedDirs:      make(map[string]int),
		reconcileTimers:  make(map[string]*time.Timer),
		autoSaveTimers:   make(map[string]*time.Timer),
		autoSavePaused:   make(map[string]bool),
		autoSaveTried:    make(map[string]string),
		documentRunners:  make(map[string]*sync.Mutex),
		autoSaveCounters: make(map[string]*autoSaveCounter),
		closeCh:          make(chan struct{}),
	}

	return s, nil
}

func (s *Service) Start(context.Context) error {
	if err := os.MkdirAll(s.storageDir, 0o700); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	s.watcher = watcher

	if err := s.loadManifest(); err != nil {
		logger.Default().Warn("load external edit manifest", zap.Error(err))
	}

	s.goTracked(s.watchLoop)
	if err := s.restoreSessions(); err != nil {
		return err
	}
	s.startCleanupLoop()
	return nil
}

func (s *Service) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.closeCh)
		for _, timer := range s.reconcileTimers {
			s.stopTrackedTimer(timer)
		}
		s.reconcileTimers = map[string]*time.Timer{}
		for _, timer := range s.autoSaveTimers {
			s.stopTrackedTimer(timer)
		}
		s.autoSaveTimers = map[string]*time.Timer{}
		if s.cleanupTicker != nil {
			s.cleanupTicker.Stop()
			s.cleanupTicker = nil
		}
		watcher := s.watcher
		s.mu.Unlock()

		// 先关掉 watcher 让 watchLoop 退出，再等待所有后台 goroutine / 定时器回调收尾，
		// 确保 Close 返回后不会再有写盘动作（否则会与上层的临时目录清理竞争）。
		if watcher != nil {
			closeErr = watcher.Close()
		}
		s.bg.Wait()
	})
	return closeErr
}

func (s *Service) GetSettings() (*Settings, error) {
	cfg := s.configProvider()
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}
	workspaceRoot, err := s.resolveWorkspaceRoot(cfg.ExternalEditWorkspaceRoot)
	if err != nil {
		return nil, err
	}

	editors := s.detectEditors(cfg.ExternalEditCustomEditors, cfg.ExternalEditDefaultEditorID)
	defaultID := cfg.ExternalEditDefaultEditorID
	if defaultID == "" {
		defaultID = firstAvailableEditorID(editors)
	}
	for i := range editors {
		editors[i].Default = editors[i].ID == defaultID
	}

	return &Settings{
		DefaultEditorID:      defaultID,
		WorkspaceRoot:        workspaceRoot,
		CleanupRetentionDays: normalizeCleanupRetentionDays(cfg.ExternalEditCleanupRetentionDays),
		MaxReadFileSizeMB:    normalizeMaxReadFileSizeMB(cfg.ExternalEditMaxReadFileSizeMB),
		Editors:              editors,
		CustomEditors:        cloneCustomEditors(cfg.ExternalEditCustomEditors),
	}, nil
}

func (s *Service) SaveSettings(input SettingsInput) (*Settings, error) {
	cfg := s.configProvider()
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}

	workspaceRoot, err := s.resolveWorkspaceRoot(input.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "workspaces"), 0o700); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}

	customEditors, err := s.normalizeCustomEditors(input.CustomEditors)
	if err != nil {
		return nil, err
	}

	editors := s.detectEditors(customEditors, input.DefaultEditorID)
	defaultID := strings.TrimSpace(input.DefaultEditorID)
	if defaultID == "" || !containsEditorID(editors, defaultID) {
		defaultID = firstAvailableEditorID(editors)
	}
	if defaultID != "" && !containsAvailableEditor(editors, defaultID) {
		return nil, fmt.Errorf("默认外部编辑器不可用")
	}

	cfg.ExternalEditDefaultEditorID = defaultID
	cfg.ExternalEditWorkspaceRoot = workspaceRoot
	cfg.ExternalEditCustomEditors = customEditors
	cfg.ExternalEditCleanupRetentionDays = normalizeCleanupRetentionDays(input.CleanupRetentionDays)
	cfg.ExternalEditMaxReadFileSizeMB = normalizeMaxReadFileSizeMB(input.MaxReadFileSizeMB)
	if err := s.configSaver(cfg); err != nil {
		return nil, fmt.Errorf("save external edit settings: %w", err)
	}

	s.runRetentionCleanup()
	return s.GetSettings()
}

func (s *Service) ListSessions() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		if isExternalEditClipboardResidueSession(session) {
			continue
		}
		sessions = append(sessions, cloneSession(session))
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions
}
