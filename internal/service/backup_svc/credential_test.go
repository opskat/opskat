package backup_svc

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/cago-frame/cago/database/db"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
)

type taggedCredentialCrypto struct {
	tag string
}

func (c taggedCredentialCrypto) Encrypt(plaintext string) (string, error) {
	return c.tag + plaintext, nil
}

func (c taggedCredentialCrypto) Decrypt(ciphertext string) (string, error) {
	plaintext, ok := strings.CutPrefix(ciphertext, c.tag)
	if !ok {
		return "", fmt.Errorf("unexpected ciphertext")
	}
	return plaintext, nil
}

func setupCredentialBackupTest(t *testing.T) {
	t.Helper()
	setupBackupTest(t)
	require.NoError(t, db.Ctx(t.Context()).AutoMigrate(&credential_entity.Credential{}))
	credential_repo.RegisterCredential(credential_repo.NewCredential())
}

func TestCredentialBackupRoundTripPreservesSSHKeyPassphrase(t *testing.T) {
	const passphrase = "correct horse battery staple"

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	privateKeyBlock, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "backup-test", []byte(passphrase))
	require.NoError(t, err)
	privateKeyPEM := string(pem.EncodeToMemory(privateKeyBlock))
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	require.NoError(t, err)

	sourceCrypto := taggedCredentialCrypto{tag: "source:"}
	setupCredentialBackupTest(t)
	ctx := t.Context()
	credential := &credential_entity.Credential{
		Name:       "protected-key",
		Type:       credential_entity.TypeSSHKey,
		PrivateKey: sourceCrypto.tag + privateKeyPEM,
		Passphrase: sourceCrypto.tag + passphrase,
		PublicKey:  string(ssh.MarshalAuthorizedKey(publicKey)),
		KeyType:    credential_entity.KeyTypeED25519,
	}
	require.NoError(t, credential_repo.Credential().Create(ctx, credential))
	asset := &asset_entity.Asset{
		Name:   "protected-key-host",
		Type:   asset_entity.AssetTypeSSH,
		Status: asset_entity.StatusActive,
		Config: fmt.Sprintf(`{"host":"host","port":22,"username":"user","auth_type":"key","credential_id":%d}`, credential.ID),
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))

	backup, err := Export(ctx, &ExportOptions{IncludeCredentials: true}, sourceCrypto)
	require.NoError(t, err)
	serialized, err := json.Marshal(backup)
	require.NoError(t, err)
	var transported BackupData
	require.NoError(t, json.Unmarshal(serialized, &transported))

	destinationCrypto := taggedCredentialCrypto{tag: "destination:"}
	setupCredentialBackupTest(t)
	ctx = t.Context()
	_, err = Import(ctx, &transported, &ImportOptions{
		ImportAssets:      true,
		ImportCredentials: true,
		Mode:              "merge",
	}, destinationCrypto)
	require.NoError(t, err)

	restoredCredentials, err := credential_repo.Credential().List(ctx)
	require.NoError(t, err)
	require.Len(t, restoredCredentials, 1)
	restored := restoredCredentials[0]
	restoredPrivateKey, err := destinationCrypto.Decrypt(restored.PrivateKey)
	require.NoError(t, err)
	restoredPassphrase, err := destinationCrypto.Decrypt(restored.Passphrase)
	require.NoError(t, err)
	require.Equal(t, passphrase, restoredPassphrase)
	_, err = ssh.ParsePrivateKeyWithPassphrase([]byte(restoredPrivateKey), []byte(restoredPassphrase))
	require.NoError(t, err)
}
