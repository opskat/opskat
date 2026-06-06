package ssh

import (
	"testing"

	"github.com/opskat/opskat/internal/pkg/dirsync"
	"github.com/opskat/opskat/internal/service/ssh_svc"
)

func TestChangeSSHDirectoryReturnsSessionNotFoundForUnknownID(t *testing.T) {
	s := &SSH{manager: ssh_svc.NewManager()}
	err := s.ChangeSSHDirectory("nonexistent", "/tmp")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != dirsync.CodeSessionNotFound {
		t.Fatalf("expected SessionNotFound code, got %q", err.Error())
	}
}

func TestEnableSSHSyncReturnsSessionNotFoundForUnknownID(t *testing.T) {
	s := &SSH{manager: ssh_svc.NewManager()}
	err := s.EnableSSHSync("nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != dirsync.CodeSessionNotFound {
		t.Fatalf("expected SessionNotFound code, got %q", err.Error())
	}
}

func TestGetSSHServerStatusReturnsSessionNotFoundForUnknownID(t *testing.T) {
	s := &SSH{manager: ssh_svc.NewManager()}
	_, err := s.GetSSHServerStatus("nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "会话不存在: nonexistent" {
		t.Fatalf("unexpected error: %v", err)
	}
}
