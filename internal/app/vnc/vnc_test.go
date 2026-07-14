package vnc

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/vnc_svc"
)

func TestEncodeVNCClipboardTextUsesWindowsChineseCodePage(t *testing.T) {
	got, err := (&VNC{}).EncodeVNCClipboardText("abc中文XYZ")
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

func newTestVNC(t *testing.T) *VNC {
	ctrl := gomock.NewController(t)
	mgr := vnc_svc.NewManager(mock_asset_repo.NewMockAssetRepo(ctrl))
	return &VNC{manager: mgr}
}

func TestWriteVNCRejectsInvalidBase64(t *testing.T) {
	v := newTestVNC(t)
	if err := v.WriteVNC("s", "not@@base64"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestWriteVNCUnknownSession(t *testing.T) {
	v := newTestVNC(t)
	if err := v.WriteVNC("missing", "aGVsbG8="); err == nil {
		t.Fatal("expected unknown-session error")
	}
}
