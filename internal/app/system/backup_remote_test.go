package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
)

func TestPreviewWebDAVBackupDecryptsSummaryWithoutImporting(t *testing.T) {
	initBootstrapForSystemTest(t)

	plain, err := json.Marshal(&backup_svc.BackupData{
		Version:    "1.0",
		ExportedAt: "2026-11-10T00:00:00Z",
		Forwards:   []*backup_svc.BackupForward{{}, {}},
		Shortcuts:  json.RawMessage(`{"page.home":"mod+shift+H"}`),
	})
	if err != nil {
		t.Fatalf("marshal backup: %v", err)
	}
	encrypted, err := backup_svc.EncryptBackup(plain, "right-password")
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}

	const name = "opskat-backup-20261110.encrypted.json"
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && r.URL.Path == "/dav/opskat/"+name {
			_, _ = w.Write(encrypted)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{
		WebDAVURL:      srv.URL + "/dav/",
		WebDAVAuthType: string(backup_svc.WebDAVAuthNone),
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	s := New(t.Context(), SkillContent{})

	summary, err := s.PreviewWebDAVBackup(name, "right-password")
	if err != nil {
		t.Fatalf("PreviewWebDAVBackup: %v", err)
	}
	if !summary.Encrypted || summary.ForwardCount != 2 || !summary.HasShortcuts || summary.ExportedAt != "2026-11-10T00:00:00Z" {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	if _, err := s.PreviewWebDAVBackup(name, "wrong-password"); err == nil {
		t.Fatalf("a wrong password must fail the preview")
	}

	for _, req := range requests {
		if req != http.MethodGet+" /dav/opskat/"+name {
			t.Fatalf("preview must only download the backup, got request %q", req)
		}
	}
}
