package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/host_key_repo"
	"github.com/opskat/opskat/internal/service/ssh_svc"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	gossh "golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

type stubLang string

func (l stubLang) Lang() string { return string(l) }

type noopHostKeyRepo struct{}

func (noopHostKeyRepo) FindByHostPortKeyType(context.Context, string, int, string) (*host_key_entity.HostKey, error) {
	return nil, gorm.ErrRecordNotFound
}
func (noopHostKeyRepo) UpdateLastSeen(context.Context, int64, int64) error { return nil }
func (noopHostKeyRepo) Upsert(context.Context, *host_key_entity.HostKey) error {
	return nil
}
func (noopHostKeyRepo) Delete(context.Context, int64) error { return nil }
func (noopHostKeyRepo) List(context.Context) ([]*host_key_entity.HostKey, error) {
	return nil, nil
}

// startControllableSSHServer launches an in-process SSH server whose "shell" request
// pauses until allowShellReply is closed, creating a deterministic window after the
// SSH handshake completes (where handshakeUntilCanceled is no longer active) and before
// manager.Connect returns.
func startControllableSSHServer(t *testing.T) (host string, port int, shellReceived <-chan struct{}, allowShellReply chan struct{}) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	require.NoError(t, err)

	cfg := &gossh.ServerConfig{
		PasswordCallback: func(_ gossh.ConnMetadata, pass []byte) (*gossh.Permissions, error) {
			if string(pass) == "secret" {
				return nil, nil
			}
			return nil, gossh.ErrNoAuth
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	shellCh := make(chan struct{}, 1)
	allowCh := make(chan struct{})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				sc, chans, reqs, err := gossh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer func() { _ = sc.Close() }()
				go gossh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						_ = nc.Reject(gossh.UnknownChannelType, "unsupported")
						continue
					}
					ch, chReqs, err := nc.Accept()
					if err != nil {
						return
					}
					go func() {
						defer func() { _ = ch.Close() }()
						for req := range chReqs {
							switch req.Type {
							case "pty-req":
								_ = req.Reply(true, nil)
							case "shell":
								select {
								case shellCh <- struct{}{}:
								default:
								}
								<-allowCh
								_ = req.Reply(true, nil)
							default:
								if req.WantReply {
									_ = req.Reply(false, nil)
								}
							}
						}
					}()
				}
			}()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, shellCh, allowCh
}

func TestConnectSSHAsyncCancelAfterConnectDisconnectsOrphanSession(t *testing.T) {
	host, port, shellReceived, allowShellReply := startControllableSSHServer(t)

	origAssets := asset_repo.Asset()
	ctrl := gomock.NewController(t)
	mockAssets := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset_repo.RegisterAsset(mockAssets)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAssets) })

	origHostKeys := host_key_repo.HostKey()
	host_key_repo.RegisterHostKey(noopHostKeyRepo{})
	t.Cleanup(func() { host_key_repo.RegisterHostKey(origHostKeys) })

	cfgBytes, err := json.Marshal(asset_entity.SSHConfig{
		Host:     host,
		Port:     port,
		Username: "tester",
		AuthType: "password",
	})
	require.NoError(t, err)
	asset := &asset_entity.Asset{
		ID:     42,
		Name:   "test-ssh",
		Type:   asset_entity.AssetTypeSSH,
		Config: string(cfgBytes),
		Status: asset_entity.StatusActive,
	}
	mockAssets.EXPECT().Find(gomock.Any(), int64(42)).Return(asset, nil).AnyTimes()

	mgr := ssh_svc.NewManager()
	t.Cleanup(mgr.DisconnectAll)

	appCtx, appCancel := context.WithCancel(context.Background())
	t.Cleanup(appCancel)

	binder := New(appCtx, stubLang("en"), mgr, nil, nil)
	binder.Startup(appCtx)

	var (
		mu             sync.Mutex
		connectEvents  []SSHConnectEvent
		connectedCount int
	)
	binder.emit = func(event string, payload any) {
		ev, ok := payload.(SSHConnectEvent)
		if !ok {
			return
		}
		mu.Lock()
		connectEvents = append(connectEvents, ev)
		if ev.Type == "connected" {
			connectedCount++
		}
		mu.Unlock()
		if ev.Type == "host_key_verify" {
			go binder.RespondHostKeyVerify(ev.HostKeyVerifyID, int(ssh_svc.HostKeyAcceptOnce))
		}
	}

	connID, err := binder.ConnectSSHAsync(SSHConnectRequest{
		AssetID:  42,
		Password: "secret",
		Cols:     80,
		Rows:     24,
	})
	require.NoError(t, err)

	// Wait until SSH handshake has finished and session.Shell() is in flight.
	select {
	case <-shellReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SSH shell request")
	}

	// Cancel in the post-handshake window, then let session.Shell() finish.
	binder.CancelSSHConnect(connID)
	close(allowShellReply)

	// Wait for the ConnectSSHAsync goroutine to exit (pendingConnections entry removed).
	require.Eventually(t, func() bool {
		_, stillPending := binder.pendingConnections.Load(connID)
		return !stillPending
	}, 5*time.Second, 10*time.Millisecond)

	// Canceled connect must neither leak an active session nor emit "connected".
	require.Equal(t, 0, mgr.ActiveSessions(), "canceled ConnectSSHAsync leaked an active SSH session")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 0, connectedCount, "canceled ConnectSSHAsync should not emit connected event; events=%+v", connectEvents)
}
