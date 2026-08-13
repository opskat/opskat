package asset_put_svc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

const failingAssetType = "asset-put-handler-failure-test"

type failingHandler struct{}

func (*failingHandler) Type() string                                { return failingAssetType }
func (*failingHandler) DefaultPort() int                            { return 0 }
func (*failingHandler) SafeView(*asset_entity.Asset) map[string]any { return nil }
func (*failingHandler) ResolvePassword(context.Context, *asset_entity.Asset) (string, error) {
	return "", nil
}
func (*failingHandler) DefaultPolicy() any { return nil }
func (*failingHandler) PolicyKind() string { return "" }
func (*failingHandler) AutomationContract() assettype.AutomationContract {
	return assettype.AutomationContract{
		ConfigFields:   []string{"username", "password", "private_key", "passphrase", "credential_id"},
		ApprovalFields: []string{"username"},
		CredentialPlan: func(args map[string]any) (assettype.CredentialPlan, error) {
			if id := assettype.ArgInt64(args, "credential_id"); id > 0 {
				return assettype.CredentialPlan{Kind: assettype.CredentialKindReference, ReferenceID: id, AcceptedTypes: []string{credential_entity.TypePassword}}, nil
			}
			if privateKey := assettype.ArgString(args, "private_key"); privateKey != "" {
				return assettype.CredentialPlan{
					Kind: assettype.CredentialKindSSHKey, PrivateKey: privateKey,
					Passphrase: assettype.ArgString(args, "passphrase"), Username: assettype.ArgString(args, "username"),
				}, nil
			}
			return assettype.CredentialPlan{Kind: assettype.CredentialKindPassword, Plaintext: assettype.ArgString(args, "password"), Username: assettype.ArgString(args, "username")}, nil
		},
		BindCredential: func(args map[string]any, binding assettype.CredentialBinding) (map[string]any, error) {
			out := make(map[string]any, len(args))
			for key, value := range args {
				out[key] = value
			}
			delete(out, "password")
			delete(out, "private_key")
			delete(out, "passphrase")
			out["credential_id"] = binding.ID
			return out, nil
		},
	}
}
func (*failingHandler) ValidateCreateArgs(map[string]any) error { return nil }
func (*failingHandler) ApplyCreateArgs(context.Context, *asset_entity.Asset, map[string]any) error {
	return errors.New("handler refused config")
}
func (*failingHandler) ApplyUpdateArgs(context.Context, *asset_entity.Asset, map[string]any) error {
	return errors.New("handler refused config")
}

func init() { assettype.Register(&failingHandler{}) }

type putTestEnv struct {
	ctx context.Context
	db  *gorm.DB
}

func setupPutTest(t *testing.T) *putTestEnv {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}, &credential_entity.Credential{}, &ssh_agent_source_entity.SSHAgentSource{}))

	oldAsset := asset_repo.Asset()
	oldCredential := credential_repo.Credential()
	oldAgentSource := ssh_agent_source_repo.SSHAgentSource()
	db.SetDefault(gdb)
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	credential_svc.SetDefault(credential_svc.New("asset-put-test-master-key", []byte("asset-put-salt16")))
	t.Cleanup(func() {
		asset_repo.RegisterAsset(oldAsset)
		credential_repo.RegisterCredential(oldCredential)
		ssh_agent_source_repo.RegisterSSHAgentSource(oldAgentSource)
		sqlDB, closeErr := gdb.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return &putTestEnv{ctx: context.Background(), db: gdb}
}

func (e *putTestEnv) counts(t *testing.T) (int64, int64) {
	t.Helper()
	var assets, credentials int64
	require.NoError(t, e.db.Model(&asset_entity.Asset{}).Count(&assets).Error)
	require.NoError(t, e.db.Model(&credential_entity.Credential{}).Count(&credentials).Error)
	return assets, credentials
}

func registerWriteFailure(t *testing.T, gdb *gorm.DB, operation, table string) {
	t.Helper()
	name := fmt.Sprintf("asset_put_%s_%s", operation, table)
	callback := func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == table {
			_ = tx.AddError(errors.New(table + " write failed"))
		}
	}
	switch operation {
	case "create":
		require.NoError(t, gdb.Callback().Create().Before("gorm:create").Register(name, callback))
	case "update":
		require.NoError(t, gdb.Callback().Update().Before("gorm:update").Register(name, callback))
	default:
		t.Fatalf("unknown callback operation %q", operation)
	}
}

func newRedisAsset(name string) *asset_entity.Asset {
	return &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeRedis}
}

func newSSHAsset(name string) *asset_entity.Asset {
	return &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeSSH}
}

func generatedSSHPrivateKey(t *testing.T, passphrase string) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var block *pem.Block
	if passphrase == "" {
		block, err = gossh.MarshalPrivateKey(privateKey, "automation-test")
	} else {
		block, err = gossh.MarshalPrivateKeyWithPassphrase(privateKey, "automation-test", []byte(passphrase))
	}
	require.NoError(t, err)
	return string(pem.EncodeToMemory(block))
}

func validAgentFingerprintForPut() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(raw)
}

func seedAgentSource(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	source := &ssh_agent_source_entity.SSHAgentSource{
		Name: "offline-agent", EndpointType: "unix", Endpoint: "/definitely/not/online.sock",
		Createtime: 1, Updatetime: 1,
	}
	require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, source))
	return source.ID
}

func TestPrepareIsSideEffectFreeAndBuildsOnlySafeViews(t *testing.T) {
	env := setupPutTest(t)
	config := map[string]any{"host": "redis.internal", "username": "default", "password": "plaintext-leak"}
	asset := newRedisAsset("cache-prod")

	prepared, err := Prepare(env.ctx, Request{Asset: asset, Config: config})
	require.NoError(t, err)
	assets, credentials := env.counts(t)
	assert.Zero(t, assets)
	assert.Zero(t, credentials)
	assert.Empty(t, asset.Config, "Prepare must not mutate the caller's asset")
	assert.Equal(t, "plaintext-leak", config["password"], "Prepare must not mutate caller config")

	approval := prepared.SafeApprovalDetail()
	approvalConfig, ok := approval["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 6379, approvalConfig["port"], "approval metadata uses the owner-normalized endpoint")
	approvalJSON, err := json.Marshal(approval)
	require.NoError(t, err)
	auditJSON, err := json.Marshal(prepared.SafeAuditArgs())
	require.NoError(t, err)
	for _, safe := range []string{string(approvalJSON), string(auditJSON)} {
		assert.NotContains(t, safe, "plaintext-leak")
		assert.NotContains(t, safe, "password")
	}
}

func TestPutMaterializesCanonicalPasswordCredentials(t *testing.T) {
	for _, tt := range []struct {
		name         string
		asset        *asset_entity.Asset
		config       map[string]any
		secret       string
		wantUsername string
		readConfig   func(*asset_entity.Asset) (int64, string, error)
	}{
		{
			name: "redis account metadata", asset: newRedisAsset("cache-prod"),
			config: map[string]any{"host": "redis.internal", "username": "default", "password": "redis-secret"},
			secret: "redis-secret", wantUsername: "default",
			readConfig: func(a *asset_entity.Asset) (int64, string, error) {
				cfg, err := a.GetRedisConfig()
				return cfg.CredentialID, cfg.Password, err
			},
		},
		{
			name: "oss access key metadata", asset: &asset_entity.Asset{Name: "backups", Type: asset_entity.AssetTypeOSS},
			config: map[string]any{"endpoint": "s3.internal", "access_key_id": "AKIAEXAMPLE", "secret_access_key": "oss-secret"},
			secret: "oss-secret", wantUsername: "AKIAEXAMPLE",
			readConfig: func(a *asset_entity.Asset) (int64, string, error) {
				cfg, err := a.GetOSSConfig()
				return cfg.CredentialID, cfg.SecretAccessKey, err
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPutTest(t)
			result, err := Put(env.ctx, Request{Asset: tt.asset, Config: tt.config})
			require.NoError(t, err)
			require.NotNil(t, result.Authentication)
			assert.Equal(t, credential_entity.TypePassword, result.Authentication.Type)
			assert.Positive(t, result.Authentication.Ref)

			cred, err := credential_repo.Credential().Find(env.ctx, result.Authentication.Ref)
			require.NoError(t, err)
			assert.Equal(t, tt.asset.Name, cred.Name)
			assert.Equal(t, tt.wantUsername, cred.Username)
			plaintext, err := credential_svc.Default().Decrypt(cred.Password)
			require.NoError(t, err)
			assert.Equal(t, tt.secret, plaintext)

			stored, err := asset_repo.Asset().Find(env.ctx, result.Asset.ID)
			require.NoError(t, err)
			credentialID, inlineCiphertext, err := tt.readConfig(stored)
			require.NoError(t, err)
			assert.Equal(t, cred.ID, credentialID)
			assert.Empty(t, inlineCiphertext, "managed secret must not be passed to or stored by the asset handler")

			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), tt.secret)
			assert.NotContains(t, string(encoded), cred.Password)
		})
	}
}

func TestPutValidatesReferencesAndCredentialSourceConflictsBeforeAssetWrite(t *testing.T) {
	tests := []struct {
		name   string
		seed   *credential_entity.Credential
		config map[string]any
	}{
		{name: "missing reference", config: map[string]any{"host": "redis.internal", "username": "default", "credential_id": float64(404)}},
		{name: "wrong reference type", seed: &credential_entity.Credential{Name: "key", Type: credential_entity.TypeSSHKey}, config: map[string]any{"host": "redis.internal", "username": "default", "credential_id": float64(1)}},
		{name: "plaintext and reference", seed: &credential_entity.Credential{Name: "password", Type: credential_entity.TypePassword, Password: "ciphertext"}, config: map[string]any{"host": "redis.internal", "username": "default", "credential_id": float64(1), "password": "must-not-win"}},
		{name: "zero reference is invalid", config: map[string]any{"host": "redis.internal", "username": "default", "credential_id": float64(0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPutTest(t)
			if tt.seed != nil {
				require.NoError(t, credential_repo.Credential().Create(env.ctx, tt.seed))
			}
			_, err := Put(env.ctx, Request{Asset: newRedisAsset("cache"), Config: tt.config})
			require.Error(t, err)
			assets, credentials := env.counts(t)
			assert.Zero(t, assets)
			if tt.seed == nil {
				assert.Zero(t, credentials)
			} else {
				assert.Equal(t, int64(1), credentials)
			}
			assert.NotContains(t, err.Error(), "must-not-win")
		})
	}
}

func TestPutRollsBackCredentialAndAssetWritesOnEveryFailureBoundary(t *testing.T) {
	t.Run("credential write", func(t *testing.T) {
		env := setupPutTest(t)
		registerWriteFailure(t, env.db, "create", "credentials")
		_, err := Put(env.ctx, Request{Asset: newRedisAsset("cache"), Config: map[string]any{"host": "redis.internal", "username": "default", "password": "secret"}})
		require.Error(t, err)
		assets, credentials := env.counts(t)
		assert.Zero(t, assets)
		assert.Zero(t, credentials)
	})

	t.Run("handler application after credential write", func(t *testing.T) {
		env := setupPutTest(t)
		_, err := Put(env.ctx, Request{Asset: &asset_entity.Asset{Name: "bad", Type: failingAssetType}, Config: map[string]any{"username": "alice", "password": "secret"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "handler refused")
		assets, credentials := env.counts(t)
		assert.Zero(t, assets)
		assert.Zero(t, credentials, "handler failure must roll back the credential created earlier in the transaction")
	})

	t.Run("asset create after credential write", func(t *testing.T) {
		env := setupPutTest(t)
		registerWriteFailure(t, env.db, "create", "assets")
		_, err := Put(env.ctx, Request{Asset: newRedisAsset("cache"), Config: map[string]any{"host": "redis.internal", "username": "default", "password": "secret"}})
		require.Error(t, err)
		assets, credentials := env.counts(t)
		assert.Zero(t, assets)
		assert.Zero(t, credentials, "asset failure must roll back the credential")
	})
}

func TestPutReusesValidatedExistingCredential(t *testing.T) {
	env := setupPutTest(t)
	cred := &credential_entity.Credential{Name: "shared", Type: credential_entity.TypePassword, Password: "existing-ciphertext", Username: "default"}
	require.NoError(t, credential_repo.Credential().Create(env.ctx, cred))

	result, err := Put(env.ctx, Request{Asset: newRedisAsset("cache"), Config: map[string]any{"host": "redis.internal", "username": "default", "credential_id": float64(cred.ID)}})
	require.NoError(t, err)
	require.NotNil(t, result.Authentication)
	assert.Equal(t, AuthenticationRef{Type: credential_entity.TypePassword, Ref: cred.ID}, *result.Authentication)
	_, credentialCount := env.counts(t)
	assert.Equal(t, int64(1), credentialCount, "reference reuse must not create or modify a credential")
	stored, err := asset_repo.Asset().Find(env.ctx, result.ID)
	require.NoError(t, err)
	cfg, err := stored.GetRedisConfig()
	require.NoError(t, err)
	assert.Equal(t, cred.ID, cfg.CredentialID)
	assert.Empty(t, cfg.Password)
}

func TestPutRejectsCredentialNameWithoutMaterialization(t *testing.T) {
	env := setupPutTest(t)
	cred := &credential_entity.Credential{Name: "shared", Type: credential_entity.TypePassword, Password: "existing-ciphertext"}
	require.NoError(t, credential_repo.Credential().Create(env.ctx, cred))
	_, err := Put(env.ctx, Request{Asset: newRedisAsset("cache"), CredentialName: "must-not-rename", Config: map[string]any{"host": "redis.internal", "username": "default", "credential_id": float64(cred.ID)}})
	require.Error(t, err)
	assets, credentialCount := env.counts(t)
	assert.Zero(t, assets)
	assert.Equal(t, int64(1), credentialCount)
}

func TestPutUpdatePlaintextDefaultsCredentialMetadataFromExistingAsset(t *testing.T) {
	env := setupPutTest(t)
	asset := newRedisAsset("cache")
	asset.Status = asset_entity.StatusActive
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{Host: "redis.internal", Port: 6379, Username: "existing-user"}))
	require.NoError(t, asset_repo.Asset().Create(env.ctx, asset))

	candidate := *asset
	result, err := Put(env.ctx, Request{Asset: &candidate, Config: map[string]any{"password": "replacement-secret"}})
	require.NoError(t, err)
	cred, err := credential_repo.Credential().Find(env.ctx, result.Authentication.Ref)
	require.NoError(t, err)
	assert.Equal(t, "existing-user", cred.Username)
}

func TestPutUpdateReplacesAssociationWithoutDeletingTheOldCredential(t *testing.T) {
	env := setupPutTest(t)
	oldCredential := &credential_entity.Credential{Name: "shared-old", Type: credential_entity.TypePassword, Password: "old-ciphertext"}
	require.NoError(t, credential_repo.Credential().Create(env.ctx, oldCredential))
	asset := newRedisAsset("cache")
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{Host: "redis.internal", Port: 6379, Username: "default", CredentialID: oldCredential.ID}))
	require.NoError(t, asset_repo.Asset().Create(env.ctx, asset))

	candidate := *asset
	result, err := Put(env.ctx, Request{Asset: &candidate, Config: map[string]any{"password": "replacement-secret"}})
	require.NoError(t, err)
	require.NotNil(t, result.Authentication)
	assert.NotEqual(t, oldCredential.ID, result.Authentication.Ref)

	_, err = credential_repo.Credential().Find(env.ctx, oldCredential.ID)
	assert.NoError(t, err, "replaced credentials may be shared and must not be deleted")
	_, credentialCount := env.counts(t)
	assert.Equal(t, int64(2), credentialCount)
	stored, err := asset_repo.Asset().Find(env.ctx, asset.ID)
	require.NoError(t, err)
	cfg, err := stored.GetRedisConfig()
	require.NoError(t, err)
	assert.Equal(t, result.Authentication.Ref, cfg.CredentialID)
	assert.Empty(t, cfg.Password)
}

func TestPutUpdateFailureRollsBackNewCredentialAndLeavesStoredAssociation(t *testing.T) {
	env := setupPutTest(t)
	oldCredential := &credential_entity.Credential{Name: "shared-old", Type: credential_entity.TypePassword, Password: "old-ciphertext"}
	require.NoError(t, credential_repo.Credential().Create(env.ctx, oldCredential))
	asset := newRedisAsset("cache")
	asset.Status = asset_entity.StatusActive
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{Host: "redis.internal", Port: 6379, Username: "default", CredentialID: oldCredential.ID}))
	require.NoError(t, asset_repo.Asset().Create(env.ctx, asset))
	registerWriteFailure(t, env.db, "update", "assets")

	candidate := *asset
	_, err := Put(env.ctx, Request{Asset: &candidate, Config: map[string]any{"password": "replacement-secret"}})
	require.Error(t, err)
	_, credentialCount := env.counts(t)
	assert.Equal(t, int64(1), credentialCount)
	stored, err := asset_repo.Asset().Find(env.ctx, asset.ID)
	require.NoError(t, err)
	cfg, err := stored.GetRedisConfig()
	require.NoError(t, err)
	assert.Equal(t, oldCredential.ID, cfg.CredentialID)
}

func TestPrepareSSHCredentialReferenceInfersAuthAndRejectsExplicitMismatchBeforeTransaction(t *testing.T) {
	for _, credentialType := range []string{credential_entity.TypePassword, credential_entity.TypeSSHKey} {
		t.Run(credentialType+" infers auth", func(t *testing.T) {
			env := setupPutTest(t)
			credential := &credential_entity.Credential{Name: "shared", Type: credentialType}
			require.NoError(t, credential_repo.Credential().Create(env.ctx, credential))

			prepared, err := Prepare(env.ctx, Request{Asset: newSSHAsset("box"), Config: map[string]any{
				"host": "ssh.internal", "username": "root", "credential_id": float64(credential.ID),
			}})
			require.NoError(t, err)
			result, err := Commit(env.ctx, prepared)
			require.NoError(t, err)
			cfg, err := result.Asset.GetSSHConfig()
			require.NoError(t, err)
			wantAuth := asset_entity.AuthTypePassword
			if credentialType == credential_entity.TypeSSHKey {
				wantAuth = asset_entity.AuthTypeKey
			}
			assert.Equal(t, wantAuth, cfg.AuthType)
			assert.Equal(t, AuthenticationRef{Type: credentialType, Ref: credential.ID}, *result.Authentication)
		})
	}

	env := setupPutTest(t)
	key := &credential_entity.Credential{Name: "shared-key", Type: credential_entity.TypeSSHKey}
	require.NoError(t, credential_repo.Credential().Create(env.ctx, key))
	enteredTransaction := false
	ctx := dbutil.WithTransactionRunner(env.ctx, func(ctx context.Context, fn func(context.Context) error) error {
		enteredTransaction = true
		return fn(ctx)
	})
	_, err := Put(ctx, Request{Asset: newSSHAsset("box"), Config: map[string]any{
		"host": "ssh.internal", "username": "root", "auth_type": "password", "credential_id": float64(key.ID),
	}})
	require.Error(t, err)
	assert.False(t, enteredTransaction, "explicit credential/auth mismatch must fail in Prepare before writes")
	assert.Contains(t, err.Error(), "accepted types")
}

func TestPutSSHPrivateKeyImportsManagedCredentialAndReturnsOnlySafeAssociation(t *testing.T) {
	for _, tt := range []struct {
		name           string
		assetName      string
		credentialName string
		wantName       string
	}{
		{name: "defaults to final asset name", assetName: "renamed-box", wantName: "renamed-box"},
		{name: "honors credential_name", assetName: "box", credentialName: "deployment-key", wantName: "deployment-key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPutTest(t)
			const passphrase = "write-only-passphrase"
			privateKey := generatedSSHPrivateKey(t, passphrase)
			prepared, err := Prepare(env.ctx, Request{Asset: newSSHAsset(tt.assetName), CredentialName: tt.credentialName, Config: map[string]any{
				"host": "ssh.internal", "username": "root", "private_key": privateKey, "passphrase": passphrase,
			}})
			require.NoError(t, err)
			for _, safeValue := range []any{prepared.SafeApprovalDetail(), prepared.SafeAuditArgs()} {
				encoded, marshalErr := json.Marshal(safeValue)
				require.NoError(t, marshalErr)
				assert.NotContains(t, string(encoded), privateKey)
				assert.NotContains(t, string(encoded), passphrase)
			}
			result, err := Commit(env.ctx, prepared)
			require.NoError(t, err)
			require.NotNil(t, result.Authentication)
			assert.Equal(t, credential_entity.TypeSSHKey, result.Authentication.Type)

			credential, err := credential_repo.Credential().Find(env.ctx, result.Authentication.Ref)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, credential.Name)
			assert.Equal(t, "root", credential.Username)
			storedPassphrase, err := credential_svc.Default().Decrypt(credential.Passphrase)
			require.NoError(t, err)
			assert.Equal(t, passphrase, storedPassphrase)

			cfg, err := result.Asset.GetSSHConfig()
			require.NoError(t, err)
			assert.Equal(t, asset_entity.AuthTypeKey, cfg.AuthType)
			assert.Equal(t, credential.ID, cfg.CredentialID)
			assert.Empty(t, cfg.Password)
			assert.Empty(t, cfg.PrivateKeys)
			assert.Empty(t, cfg.PrivateKeyPassphrase)

			for _, safeValue := range []any{result, map[string]any{"audit": result.Authentication}} {
				encoded, marshalErr := json.Marshal(safeValue)
				require.NoError(t, marshalErr)
				assert.NotContains(t, string(encoded), privateKey)
				assert.NotContains(t, string(encoded), passphrase)
				assert.NotContains(t, string(encoded), credential.PrivateKey)
				assert.NotContains(t, string(encoded), credential.Passphrase)
			}
		})
	}
}

func TestPutSSHAgentRequiresPersistedSourceCreatesNoCredentialAndReturnsTypedAuthentication(t *testing.T) {
	env := setupPutTest(t)
	sourceID := seedAgentSource(t, env.ctx)
	fingerprint := validAgentFingerprintForPut()

	result, err := Put(env.ctx, Request{Asset: newSSHAsset("agent-box"), Config: map[string]any{
		"host": "ssh.internal", "username": "root",
		"agent_source_id": float64(sourceID), "agent_key_fingerprint": fingerprint,
	}})
	require.NoError(t, err, "offline source existence is sufficient; save must not inspect online status")
	require.NotNil(t, result.Authentication)
	assert.Equal(t, AuthenticationRef{Type: "ssh_agent", Ref: sourceID}, *result.Authentication)
	_, credentialCount := env.counts(t)
	assert.Zero(t, credentialCount)
	cfg, err := result.Asset.GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AuthTypeAgent, cfg.AuthType)
	assert.Equal(t, sourceID, cfg.AgentSourceID)
	assert.Equal(t, fingerprint, cfg.AgentKeyFingerprint)

	for _, conflict := range []map[string]any{
		{"password": "must-not-leak"},
		{"private_key": "must-not-leak"},
		{"passphrase": "must-not-leak"},
		{"credential_id": float64(99)},
	} {
		config := map[string]any{
			"host": "ssh.internal", "username": "root", "auth_type": "agent",
			"agent_source_id": float64(sourceID), "agent_key_fingerprint": fingerprint,
		}
		for key, value := range conflict {
			config[key] = value
		}
		_, err := Put(env.ctx, Request{Asset: newSSHAsset("invalid-agent"), Config: config})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "must-not-leak")
	}

	_, err = Put(env.ctx, Request{Asset: newSSHAsset("missing-source"), Config: map[string]any{
		"host": "ssh.internal", "username": "root", "auth_type": "agent",
		"agent_source_id": float64(404), "agent_key_fingerprint": fingerprint,
	}})
	require.Error(t, err)

	enteredTransaction := false
	ctx := dbutil.WithTransactionRunner(env.ctx, func(ctx context.Context, fn func(context.Context) error) error {
		enteredTransaction = true
		return fn(ctx)
	})
	_, err = Put(ctx, Request{Asset: newSSHAsset("incomplete-agent"), Config: map[string]any{
		"host": "ssh.internal", "username": "root", "agent_source_id": float64(sourceID),
	}})
	require.Error(t, err)
	assert.False(t, enteredTransaction, "incomplete Agent identity must fail in Prepare")

	assets, credentials := env.counts(t)
	assert.Equal(t, int64(1), assets, "only the valid Agent asset should exist")
	assert.Zero(t, credentials)
}

func TestPutSSHUpdateSwitchesPasswordToKeyToAgentAndRetainsReplacedCredentials(t *testing.T) {
	env := setupPutTest(t)
	passwordResult, err := Put(env.ctx, Request{Asset: newSSHAsset("switch-box"), Config: map[string]any{
		"host": "ssh.internal", "username": "root", "password": "password-secret",
	}})
	require.NoError(t, err)
	oldPasswordID := passwordResult.Authentication.Ref

	candidate := *passwordResult.Asset
	keyResult, err := Put(env.ctx, Request{Asset: &candidate, Config: map[string]any{
		"private_key": generatedSSHPrivateKey(t, ""),
	}})
	require.NoError(t, err)
	require.NotNil(t, keyResult.Authentication)
	oldKeyID := keyResult.Authentication.Ref
	keyCfg, err := keyResult.Asset.GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AuthTypeKey, keyCfg.AuthType)
	assert.Equal(t, oldKeyID, keyCfg.CredentialID)
	assert.Empty(t, keyCfg.Password)
	assert.Zero(t, keyCfg.AgentSourceID)

	sourceID := seedAgentSource(t, env.ctx)
	candidate = *keyResult.Asset
	agentResult, err := Put(env.ctx, Request{Asset: &candidate, Config: map[string]any{
		"auth_type": "agent", "agent_source_id": float64(sourceID), "agent_key_fingerprint": validAgentFingerprintForPut(),
	}})
	require.NoError(t, err)
	agentCfg, err := agentResult.Asset.GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AuthTypeAgent, agentCfg.AuthType)
	assert.Zero(t, agentCfg.CredentialID)
	assert.Empty(t, agentCfg.Password)
	assert.Empty(t, agentCfg.PrivateKeys)
	assert.Empty(t, agentCfg.PrivateKeyPassphrase)
	require.NotNil(t, agentResult.Authentication)
	assert.Equal(t, AuthenticationRef{Type: "ssh_agent", Ref: sourceID}, *agentResult.Authentication)

	_, err = credential_repo.Credential().Find(env.ctx, oldPasswordID)
	assert.NoError(t, err, "replaced password credential may be shared and must remain")
	_, err = credential_repo.Credential().Find(env.ctx, oldKeyID)
	assert.NoError(t, err, "replaced key credential may be shared and must remain")
	_, credentialCount := env.counts(t)
	assert.Equal(t, int64(2), credentialCount)
}

func TestPutSSHKeyImportRollsBackOnHandlerAndAssetWriteFailure(t *testing.T) {
	privateKey := generatedSSHPrivateKey(t, "")
	for _, tt := range []struct {
		name    string
		asset   *asset_entity.Asset
		config  map[string]any
		prepare func(*testing.T, *putTestEnv)
	}{
		{
			name: "handler update", asset: &asset_entity.Asset{Name: "bad", Type: failingAssetType, ID: 77},
			config: map[string]any{"username": "root", "private_key": privateKey},
		},
		{
			name: "asset create", asset: newSSHAsset("box"),
			config:  map[string]any{"host": "ssh.internal", "username": "root", "private_key": privateKey},
			prepare: func(t *testing.T, env *putTestEnv) { registerWriteFailure(t, env.db, "create", "assets") },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPutTest(t)
			if tt.prepare != nil {
				tt.prepare(t, env)
			}
			_, err := Put(env.ctx, Request{Asset: tt.asset, Config: tt.config})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), privateKey)
			assets, credentials := env.counts(t)
			assert.Zero(t, assets)
			assert.Zero(t, credentials, "newly imported key must roll back with the failed asset operation")
		})
	}
}
