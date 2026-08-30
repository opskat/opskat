package vnc_svc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/opskat/opskat/internal/service/host_key_svc"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 用 net.Pipe 注入假 conn(client 端给 Session,server 端扮演 VNC 服务器),
// 验证:server 写入 → onData 收到;Session.write → server 读到;close → onClose。
func TestSessionPumpForwardsAndWrites(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()

	s := &Session{ID: "t1", conn: client}
	got := make(chan []byte, 1)
	closed := make(chan struct{})
	s.start(func(b []byte) { got <- b }, func() { close(closed) }, nil)

	// server → client(session):onData 应收到相同字节
	go func() { _, _ = server.Write([]byte("RFB 003.008\n")) }()
	select {
	case b := <-got:
		if string(b) != "RFB 003.008\n" {
			t.Fatalf("onData = %q, want RFB greeting", b)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onData")
	}

	// session.write → server 应读到相同字节(net.Pipe 同步,需并发读)
	readBack := make(chan string, 1)
	go func() {
		buf := make([]byte, 5)
		n, _ := io.ReadFull(server, buf)
		readBack <- string(buf[:n])
	}()
	if err := s.write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v := <-readBack; v != "hello" {
		t.Fatalf("server read = %q, want hello", v)
	}

	// close 关闭 conn → 读 pump 退出 → onClose 触发一次
	s.close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onClose after close")
	}
}

func TestManagerWriteUnknownSession(t *testing.T) {
	m := &Manager{sessions: map[string]*Session{}}
	if err := m.Write("nope", []byte("x")); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestManagerRetiresSessionWhenRemoteCloses(t *testing.T) {
	client, server := net.Pipe()
	m := &Manager{sessions: map[string]*Session{
		"remote-close": {ID: "remote-close", conn: client},
	}}
	closed := make(chan struct{})
	if err := m.SetCallbacks("remote-close", func([]byte) {}, func() { close(closed) }); err != nil {
		t.Fatalf("SetCallbacks: %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("close remote: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for remote close callback")
	}

	if err := m.Write("remote-close", []byte("x")); err == nil || !strings.Contains(err.Error(), "会话不存在") {
		t.Fatalf("Write after remote close = %v, want session-not-found error", err)
	}
}

type vncHostKeyRepoFake struct {
	stored *host_key_entity.HostKey
}

func (r *vncHostKeyRepoFake) FindByHostPortKeyType(_ context.Context, host string, port int, keyType string) (*host_key_entity.HostKey, error) {
	if r.stored == nil || r.stored.Host != host || r.stored.Port != port || r.stored.KeyType != keyType {
		return nil, gorm.ErrRecordNotFound
	}
	keyCopy := *r.stored
	return &keyCopy, nil
}
func (r *vncHostKeyRepoFake) Upsert(_ context.Context, key *host_key_entity.HostKey) error {
	keyCopy := *key
	r.stored = &keyCopy
	return nil
}
func (r *vncHostKeyRepoFake) Delete(context.Context, int64) error { return nil }
func (r *vncHostKeyRepoFake) List(context.Context) ([]*host_key_entity.HostKey, error) {
	return nil, nil
}

func TestManagerVNCServerKeyUsesSessionOwnedEndpoint(t *testing.T) {
	repo := &vncHostKeyRepoFake{}
	manager := &Manager{
		hostKeys: host_key_svc.New(repo),
		sessions: map[string]*Session{
			"session": {ID: "session", host: "actual.example", port: 5912},
		},
	}
	publicKey := []byte("server-rsa-public-key")
	publicKeyB64 := base64.StdEncoding.EncodeToString(publicKey)
	digest := sha256.Sum256(publicKey)
	wantFingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])

	check, err := manager.CheckVNCServerKey(context.Background(), "session", publicKeyB64)
	require.NoError(t, err)
	require.Equal(t, VNCServerKeyFirstUse, check.State)
	require.Equal(t, "actual.example", check.Host)
	require.Equal(t, 5912, check.Port)
	require.Equal(t, wantFingerprint, check.NewFingerprint)

	require.NoError(t, manager.TrustVNCServerKey(context.Background(), "session", publicKeyB64, false))
	require.Equal(t, "actual.example", repo.stored.Host)
	require.Equal(t, 5912, repo.stored.Port)
	require.Equal(t, host_key_entity.KeyTypeVNCRSA, repo.stored.KeyType)
}

func TestManagerVNCServerKeyCancellationDisconnectsWithoutTrusting(t *testing.T) {
	repo := &vncHostKeyRepoFake{}
	manager := &Manager{
		hostKeys: host_key_svc.New(repo),
		sessions: map[string]*Session{
			"session": {ID: "session", host: "cancel.example", port: 5900},
		},
	}
	publicKeyB64 := base64.StdEncoding.EncodeToString([]byte("untrusted-key"))

	check, err := manager.CheckVNCServerKey(context.Background(), "session", publicKeyB64)
	require.NoError(t, err)
	require.Equal(t, VNCServerKeyFirstUse, check.State)

	manager.Disconnect("session")
	require.Nil(t, repo.stored)
	_, err = manager.CheckVNCServerKey(context.Background(), "session", publicKeyB64)
	require.ErrorContains(t, err, "会话不存在")
}

func TestManagerVNCServerKeyRejectsInvalidInputAndUnknownSession(t *testing.T) {
	manager := &Manager{hostKeys: host_key_svc.New(&vncHostKeyRepoFake{}), sessions: map[string]*Session{}}

	_, err := manager.CheckVNCServerKey(context.Background(), "missing", base64.StdEncoding.EncodeToString([]byte("key")))
	require.ErrorContains(t, err, "会话不存在")

	manager.sessions["session"] = &Session{ID: "session", host: "host", port: 5900}
	_, err = manager.CheckVNCServerKey(context.Background(), "session", "not@@base64")
	require.ErrorContains(t, err, "公钥")
}
