package remote_desktop

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestVerifyVNCAuthPassword(t *testing.T) {
	challenge := []byte("1234567890abcdef")
	tests := []struct {
		name      string
		clientPwd string
		wantErr   bool
	}{
		{name: "correct password", clientPwd: "secret", wantErr: false},
		{name: "wrong password", clientPwd: "bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer func() { _ = client.Close() }()
			defer func() { _ = server.Close() }()
			errCh := make(chan error, 1)
			go func() {
				errCh <- serveVNCAuth38(server, "secret", challenge)
			}()
			err := verifyVNCAuth(client, tt.clientPwd)
			if tt.wantErr && err == nil {
				t.Fatalf("expected auth error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected auth success, got %v", err)
			}
			if serverErr := <-errCh; serverErr != nil {
				t.Fatalf("server failed: %v", serverErr)
			}
		})
	}
}

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

func TestVerifyVNCAuthNegotiatesRealVNC5AsRFB38(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	errCh := make(chan error, 1)
	go func() {
		defer func() { _ = server.Close() }()
		errCh <- serveVNCAuth(server, "RFB 005.000\n", "RFB 003.008\n", "secret", []byte("1234567890abcdef"))
	}()

	if err := verifyVNCAuth(client, "secret"); err != nil {
		t.Fatalf("expected RealVNC 5 compatibility handshake success, got %v", err)
	}
	if serverErr := <-errCh; serverErr != nil {
		t.Fatalf("server failed: %v", serverErr)
	}
}

func TestVNCConnectionStopsWhenContextExpiresDuringHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte("RFB 003.008\n"))
		time.Sleep(500 * time.Millisecond)
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	configJSON, err := json.Marshal(asset_entity.VNCConfig{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = (&RemoteDesktop{}).testVNCConnection(ctx, string(configJSON), "secret")
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("VNC handshake ignored context deadline: elapsed=%s, error=%v", elapsed, err)
	}
	<-serverDone
}

func TestLiveVNCAuthAndServerInit(t *testing.T) {
	addr := os.Getenv("OPSKAT_TEST_VNC_ADDR")
	password := os.Getenv("OPSKAT_TEST_VNC_PASSWORD")
	if addr == "" || password == "" {
		t.Skip("set OPSKAT_TEST_VNC_ADDR and OPSKAT_TEST_VNC_PASSWORD to run")
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second) //nolint:gosec // This opt-in integration test connects to the caller-provided VNC fixture.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := verifyVNCAuth(conn, password); err != nil {
		t.Fatal(err)
	}

	header := make([]byte, 24)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read VNC ServerInit: %v", err)
	}
	width := binary.BigEndian.Uint16(header[0:2])
	height := binary.BigEndian.Uint16(header[2:4])
	nameLength := binary.BigEndian.Uint32(header[20:24])
	if width == 0 || height == 0 || nameLength > 1<<20 {
		t.Fatalf("invalid VNC ServerInit: width=%d height=%d nameLength=%d", width, height, nameLength)
	}
	name := make([]byte, nameLength)
	if _, err := io.ReadFull(conn, name); err != nil {
		t.Fatalf("read VNC desktop name: %v", err)
	}
	t.Logf("VNC ServerInit received: %dx%d, name=%q", width, height, string(name))
}

func serveVNCAuth38(conn net.Conn, password string, challenge []byte) error {
	return serveVNCAuth(conn, "RFB 003.008\n", "RFB 003.008\n", password, challenge)
}

func serveVNCAuth(conn net.Conn, serverVersion, expectedClientVersion, password string, challenge []byte) error {
	if _, err := conn.Write([]byte(serverVersion)); err != nil {
		return err
	}
	clientVersion := make([]byte, 12)
	if _, err := io.ReadFull(conn, clientVersion); err != nil {
		return err
	}
	if string(clientVersion) != expectedClientVersion {
		return &unexpectedVNCClientVersionError{got: string(clientVersion), want: expectedClientVersion}
	}
	if _, err := conn.Write([]byte{2, 1, 2}); err != nil {
		return err
	}
	selected := []byte{0}
	if _, err := io.ReadFull(conn, selected); err != nil {
		return err
	}
	if selected[0] != 2 {
		return nil
	}
	if _, err := conn.Write(challenge); err != nil {
		return err
	}
	got := make([]byte, 16)
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	want, err := vncPasswordResponse(password, challenge)
	if err != nil {
		return err
	}
	result := make([]byte, 4)
	if string(got) != string(want) {
		binary.BigEndian.PutUint32(result, 1)
		if _, err := conn.Write(result); err != nil {
			return err
		}
		reason := []byte("authentication failed")
		lenBuf := make([]byte, 0, 4)
		lenBuf = binary.BigEndian.AppendUint32(lenBuf, uint32(len(reason)))
		if _, err := conn.Write(append(lenBuf, reason...)); err != nil {
			return err
		}
		return nil
	}
	if _, err := conn.Write(result); err != nil {
		return err
	}
	_, err = io.CopyN(io.Discard, conn, 1)
	return err
}

type unexpectedVNCClientVersionError struct {
	got  string
	want string
}

func (e *unexpectedVNCClientVersionError) Error() string {
	return "unexpected VNC client version: got " + e.got + ", want " + e.want
}
