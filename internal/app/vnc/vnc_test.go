package vnc

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
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
	return &VNC{ctx: context.Background(), manager: mgr}
}

func TestConnectVNCTemporaryParsesUnsavedConfigAndReturnsTransportSession(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{
		"host": "127.0.0.1", "port": port, "username": "tester", "encryption": "always_maximum",
	})
	if err != nil {
		t.Fatal(err)
	}

	v := newTestVNC(t)
	session, err := v.ConnectVNCTemporary(string(config), "plain-password")
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer func() { _ = server.Close() }()
	defer v.DisconnectVNC(session.ID)
	if session.Username != "tester" || session.Password != "plain-password" || session.Encryption != "always_maximum" {
		t.Fatalf("temporary session = %#v", session)
	}
}

func TestConnectVNCTemporaryRejectsMalformedConfig(t *testing.T) {
	v := newTestVNC(t)
	if _, err := v.ConnectVNCTemporary("{", ""); err == nil {
		t.Fatal("expected malformed config error")
	}
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

func TestVNCServerKeyIPCRejectsUnknownSession(t *testing.T) {
	v := newTestVNC(t)

	if _, err := v.CheckVNCServerKey("missing", "a2V5"); err == nil {
		t.Fatal("expected check unknown-session error")
	}
	if err := v.TrustVNCServerKey("missing", "a2V5", false); err == nil {
		t.Fatal("expected trust unknown-session error")
	}
}
