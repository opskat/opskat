package remote_desktop

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/remote_desktop_svc"
)

func TestEncodeVNCClipboardTextUsesWindowsChineseCodePage(t *testing.T) {
	got, err := (&RemoteDesktop{}).EncodeVNCClipboardText("abc中文XYZ")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a}
	if len(got) != len(want) {
		t.Fatalf("encoded length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("encoded byte %d = %#x, want %#x", i, got[i], want[i])
		}
	}
}

func newTestRemoteDesktop(t *testing.T) *RemoteDesktop {
	ctrl := gomock.NewController(t)
	mgr := remote_desktop_svc.NewManager(mock_asset_repo.NewMockAssetRepo(ctrl))
	return &RemoteDesktop{manager: mgr}
}

func TestWriteRemoteDesktopRejectsInvalidBase64(t *testing.T) {
	rd := newTestRemoteDesktop(t)
	if err := rd.WriteRemoteDesktop("s", "not@@base64"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestWriteRemoteDesktopUnknownSession(t *testing.T) {
	rd := newTestRemoteDesktop(t)
	if err := rd.WriteRemoteDesktop("missing", "aGVsbG8="); err == nil {
		t.Fatal("expected unknown-session error")
	}
}
