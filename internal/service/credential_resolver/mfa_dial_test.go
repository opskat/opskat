package credential_resolver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/sshagent"
)

type countingMFACaller struct {
	mu     sync.Mutex
	calls  int
	answer string
}

func (c *countingMFACaller) SubmitChallenge(_ context.Context, _ sshagent.MFAChallenge) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return []string{c.answer}, nil
}

func (c *countingMFACaller) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func otpAfterPublicKey(want string) *ssh.ServerConfig {
	return &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
				KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
					answers, err := ch("Verification", "Enter code", []string{"OTP: "}, []bool{false})
					if err != nil {
						return nil, err
					}
					if len(answers) != 1 || answers[0] != want {
						return nil, fmt.Errorf("bad otp")
					}
					return nil, nil
				},
			}}
		},
	}
}

// createKeyFileAsset 插入一个 auth_type=key、私钥来自文件路径的 SSH 资产（无需解密凭据）。
func createKeyFileAsset(t *testing.T, ctx context.Context, host string, port int) int64 {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))
	asset := &asset_entity.Asset{
		Name:       "bastion",
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Createtime: 1,
		Config: fmt.Sprintf(`{"host":%q,"port":%d,"username":"alice","auth_type":"key","private_keys":[%q]}`,
			host, port, keyPath),
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))
	return asset.ID
}

func TestDialAssetSSHWithMFA(t *testing.T) {
	convey.Convey("ctx 携带 MFA 应答方时 DialAssetSSH 把 keyboard-interactive 挑战交给它", t, func() {
		convey.Convey("私钥资产：公钥部分成功后 OTP 由应答方作答", func() {
			ctx := setupAgentDialTest(t)
			srv := newDialTestSSHServer(t, otpAfterPublicKey("123456"))
			host, port := srv.hostPort()
			assetID := createKeyFileAsset(t, ctx, host, port)

			caller := &countingMFACaller{answer: "123456"}
			client, closers, err := Default().DialAssetSSH(WithMFA(ctx, func(int64) sshagent.InteractiveCaller { return caller }), assetID)
			require.NoError(t, err)
			_ = client.Close()
			for _, c := range closers {
				_ = c.Close()
			}
			assert.Equal(t, 1, caller.count())
		})

		convey.Convey("Agent 资产：同一应答方承接结构化挑战", func() {
			ctx := setupAgentDialTest(t)
			priv, pub := testKeyPair(t)
			agentPath, _ := newDialTestAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			src := &ssh_agent_source_entity.SSHAgentSource{Name: "work", EndpointType: "unix_socket", Endpoint: agentPath}
			require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, src))
			srv := newDialTestSSHServer(t, otpAfterPublicKey("654321"))
			host, port := srv.hostPort()
			assetID := createAgentAsset(t, ctx, host, port, src.ID, sshagent.FingerprintSHA256(pub))

			caller := &countingMFACaller{answer: "654321"}
			client, closers, err := Default().DialAssetSSH(WithMFA(ctx, func(int64) sshagent.InteractiveCaller { return caller }), assetID)
			require.NoError(t, err)
			_ = client.Close()
			for _, c := range closers {
				_ = c.Close()
			}
			assert.Equal(t, 1, caller.count())
		})
	})
}
