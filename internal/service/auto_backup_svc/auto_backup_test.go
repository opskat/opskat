package auto_backup_svc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
)

func withTestHooks(t *testing.T) {
	t.Helper()
	oldExportData := exportData
	oldUpload := uploadWebDAVAutoBackup
	oldDelay := delay
	oldReady := ready
	oldLoadConfig := loadAutoBackupConfig
	oldRecord := record
	t.Cleanup(func() {
		exportData = oldExportData
		uploadWebDAVAutoBackup = oldUpload
		delay = oldDelay
		ready = oldReady
		loadAutoBackupConfig = oldLoadConfig
		record = oldRecord
	})
	ready = func() bool { return true }
	loadAutoBackupConfig = func() (backup_svc.WebDAVConfig, string, error) {
		return backup_svc.WebDAVConfig{URL: "https://example.com/dav/"}, "backup-password", nil
	}
	exportData = func(ctx context.Context, opts *backup_svc.ExportOptions, crypto backup_svc.CredentialCrypto) (*backup_svc.BackupData, error) {
		if !opts.IncludeCredentials || !opts.IncludeForwards || !opts.IncludePolicyGroups {
			t.Fatalf("auto backup must include credentials, forwards, and policy groups")
		}
		if opts.Shortcuts != `{"tab.close":{"code":"KeyW"}}` {
			t.Fatalf("unexpected shortcuts snapshot %q", opts.Shortcuts)
		}
		if opts.CustomThemes != `[{"id":"theme-1"}]` {
			t.Fatalf("unexpected themes snapshot %q", opts.CustomThemes)
		}
		return &backup_svc.BackupData{Version: "1.0"}, nil
	}
	record = func(successAt int64, err error) {}
}

func TestSetClientSnapshotValidatesAndCaches(t *testing.T) {
	withTestHooks(t)
	svc := New()
	if err := svc.SetClientSnapshot(`{"a":1}`, `[{"id":"theme-1"}]`); err != nil {
		t.Fatalf("SetClientSnapshot: %v", err)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.shortcuts != `{"a":1}` || svc.customThemes != `[{"id":"theme-1"}]` {
		t.Fatalf("snapshot was not cached")
	}
	if err := svc.SetClientSnapshot("not-json", ""); err == nil {
		t.Fatalf("expected invalid shortcut json error")
	}
}

func TestScheduleDebouncesIntoSingleUpload(t *testing.T) {
	withTestHooks(t)
	delay = 10 * time.Millisecond
	svc := New()
	svc.Start(context.Background())
	defer svc.Stop()
	_ = svc.SetClientSnapshot(`{"tab.close":{"code":"KeyW"}}`, `[{"id":"theme-1"}]`)

	var mu sync.Mutex
	calls := 0
	done := make(chan struct{})
	uploadWebDAVAutoBackup = func(cfg backup_svc.WebDAVConfig, content []byte) (*backup_svc.WebDAVBackupInfo, error) {
		mu.Lock()
		calls++
		if calls == 1 {
			close(done)
		}
		mu.Unlock()
		return &backup_svc.WebDAVBackupInfo{Name: backup_svc.WebDAVAutoBackupFilename}, nil
	}

	svc.Schedule()
	svc.Schedule()
	svc.Schedule()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for upload")
	}
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected debounced single upload, got %d", calls)
	}
}

func TestSchedulePendingRerunsAfterCurrentUpload(t *testing.T) {
	withTestHooks(t)
	delay = 1 * time.Millisecond
	svc := New()
	svc.Start(context.Background())
	defer svc.Stop()
	_ = svc.SetClientSnapshot(`{"tab.close":{"code":"KeyW"}}`, `[{"id":"theme-1"}]`)

	started := make(chan struct{})
	finishFirst := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	uploadWebDAVAutoBackup = func(cfg backup_svc.WebDAVConfig, content []byte) (*backup_svc.WebDAVBackupInfo, error) {
		mu.Lock()
		calls++
		current := calls
		mu.Unlock()
		if current == 1 {
			close(started)
			<-finishFirst
		} else {
			close(done)
		}
		return &backup_svc.WebDAVBackupInfo{Name: backup_svc.WebDAVAutoBackupFilename}, nil
	}

	svc.Schedule()
	<-started
	svc.Schedule()
	close(finishFirst)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for pending rerun")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected pending rerun, got %d calls", calls)
	}
}

// recordResult must not re-persist a finished backup's status once the user has
// disabled/cleared auto backup mid-flight (#2): otherwise ClearWebDAVConfig leaves
// a stray LastAt/LastError behind in the cleared config.
func TestRecordResultSkipsWhenDisabled(t *testing.T) {
	if _, err := bootstrap.LoadConfig(t.TempDir()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Enabled: a completed backup records its success time.
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{WebDAVAutoBackupEnabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	recordResult(12345, nil)
	if got := bootstrap.GetConfig().WebDAVAutoBackupLastAt; got != 12345 {
		t.Fatalf("enabled: expected LastAt=12345, got %d", got)
	}

	// Disabled (ClearWebDAVConfig / toggle off ran while a backup was in flight):
	// the in-flight completion must not write stale status back into the cleared config.
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{WebDAVAutoBackupEnabled: false}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	recordResult(99999, nil)
	cfg := bootstrap.GetConfig()
	if cfg.WebDAVAutoBackupLastAt != 0 || cfg.WebDAVAutoBackupLastError != "" {
		t.Fatalf("disabled: stale status persisted: at=%d err=%q", cfg.WebDAVAutoBackupLastAt, cfg.WebDAVAutoBackupLastError)
	}
}

func TestRunRecordsFailure(t *testing.T) {
	withTestHooks(t)
	wantErr := errors.New("upload failed")
	loadAutoBackupConfig = func() (backup_svc.WebDAVConfig, string, error) {
		return backup_svc.WebDAVConfig{}, "", wantErr
	}
	recorded := make(chan error, 1)
	record = func(successAt int64, err error) { recorded <- err }
	svc := New()
	svc.Start(context.Background())
	svc.shortcuts = `{"tab.close":{"code":"KeyW"}}`
	svc.customThemes = `[{"id":"theme-1"}]`
	svc.run()
	select {
	case err := <-recorded:
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected recorded error %v, got %v", wantErr, err)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for record")
	}
}
