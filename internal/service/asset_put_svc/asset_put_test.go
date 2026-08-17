package asset_put_svc

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
		ConfigFields:   []string{"username", "password", "credential_id"},
		ApprovalFields: []string{"username"},
		CredentialPlan: func(args map[string]any) (assettype.CredentialPlan, error) {
			if id := assettype.ArgInt64(args, "credential_id"); id > 0 {
				return assettype.CredentialPlan{Kind: assettype.CredentialKindReference, ReferenceID: id, AcceptedTypes: []string{credential_entity.TypePassword}}, nil
			}
			return assettype.CredentialPlan{Kind: assettype.CredentialKindNone}, nil
		},
		BindCredential: func(args map[string]any, binding assettype.CredentialBinding) (map[string]any, error) {
			out := make(map[string]any, len(args))
			for key, value := range args {
				out[key] = value
			}
			delete(out, "password")
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

func TestSafeAuditArgsForResultAddsOnlyPersistedIdentityAndAuthentication(t *testing.T) {
	env := setupPutTest(t)
	prepared, err := Prepare(env.ctx, Request{Asset: newRedisAsset("cache"), Config: map[string]any{
		"host": "redis.internal", "username": "default", "password": "audit-secret",
	}})
	require.NoError(t, err)

	safe := prepared.SafeAuditArgsForResult(&Result{ID: 91, Authentication: &AuthenticationRef{Type: credential_entity.TypePassword, Ref: 12}})
	encoded, err := json.Marshal(safe)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"id":91`)
	assert.Contains(t, string(encoded), `"ref":12`)
	assert.NotContains(t, string(encoded), "audit-secret")
	assert.NotContains(t, string(encoded), `"password":`)
}

func TestPutStoresPlaintextSecretsInlineAcrossPasswordOwners(t *testing.T) {
	for _, tt := range []struct {
		name       string
		asset      *asset_entity.Asset
		config     map[string]any
		secret     string
		readConfig func(*asset_entity.Asset) (int64, string, error)
	}{
		{
			name: "redis account metadata", asset: newRedisAsset("cache-prod"),
			config: map[string]any{"host": "redis.internal", "username": "default", "password": "redis-secret"},
			secret: "redis-secret",
			readConfig: func(a *asset_entity.Asset) (int64, string, error) {
				cfg, err := a.GetRedisConfig()
				return cfg.CredentialID, cfg.Password, err
			},
		},
		{
			name: "oss access key metadata", asset: &asset_entity.Asset{Name: "backups", Type: asset_entity.AssetTypeOSS},
			config: map[string]any{"endpoint": "s3.internal", "access_key_id": "AKIAEXAMPLE", "secret_access_key": "oss-secret"},
			secret: "oss-secret",
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
			assert.Nil(t, result.Authentication)
			_, credentialCount := env.counts(t)
			assert.Zero(t, credentialCount)

			stored, err := asset_repo.Asset().Find(env.ctx, result.Asset.ID)
			require.NoError(t, err)
			credentialID, inlineCiphertext, err := tt.readConfig(stored)
			require.NoError(t, err)
			assert.Zero(t, credentialID)
			assert.NotEmpty(t, inlineCiphertext)
			plaintext, err := credential_svc.Default().Decrypt(inlineCiphertext)
			require.NoError(t, err)
			assert.Equal(t, tt.secret, plaintext)

			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), tt.secret)
		})
	}
}

func TestPutStoresPlaintextPasswordInlineWithoutCreatingCredential(t *testing.T) {
	env := setupPutTest(t)
	result, err := Put(env.ctx, Request{Asset: newSSHAsset("box"), Config: map[string]any{
		"host": "ssh.internal", "username": "root", "password": "inline-secret",
	}})
	require.NoError(t, err)
	assert.Nil(t, result.Authentication)

	assets, credentials := env.counts(t)
	assert.Equal(t, int64(1), assets)
	assert.Zero(t, credentials, "plain --password must not create a managed credential implicitly")

	stored, err := asset_repo.Asset().Find(env.ctx, result.Asset.ID)
	require.NoError(t, err)
	cfg, err := stored.GetSSHConfig()
	require.NoError(t, err)
	assert.Zero(t, cfg.CredentialID)
	assert.NotEmpty(t, cfg.Password)
	assert.NotEqual(t, "inline-secret", cfg.Password)
	plaintext, err := credential_svc.Default().Decrypt(cfg.Password)
	require.NoError(t, err)
	assert.Equal(t, "inline-secret", plaintext)
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

func TestPutLeavesNoAssetOrCredentialRowsOnEveryFailureBoundary(t *testing.T) {
	t.Run("handler application", func(t *testing.T) {
		env := setupPutTest(t)
		_, err := Put(env.ctx, Request{Asset: &asset_entity.Asset{Name: "bad", Type: failingAssetType}, Config: map[string]any{"username": "alice", "password": "secret"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "handler refused")
		assets, credentials := env.counts(t)
		assert.Zero(t, assets)
		assert.Zero(t, credentials)
	})

	t.Run("asset create", func(t *testing.T) {
		env := setupPutTest(t)
		registerWriteFailure(t, env.db, "create", "assets")
		_, err := Put(env.ctx, Request{Asset: newRedisAsset("cache"), Config: map[string]any{"host": "redis.internal", "username": "default", "password": "secret"}})
		require.Error(t, err)
		assets, credentials := env.counts(t)
		assert.Zero(t, assets)
		assert.Zero(t, credentials)
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

func TestPrepareSQLiteUpdateRejectsPasswordCredentialsWhenDriverIsOmitted(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config func(*testing.T, *putTestEnv) map[string]any
	}{
		{
			name: "plaintext",
			config: func(*testing.T, *putTestEnv) map[string]any {
				return map[string]any{"password": "not-applicable"}
			},
		},
		{
			name: "managed reference",
			config: func(t *testing.T, env *putTestEnv) map[string]any {
				credential := &credential_entity.Credential{Name: "shared", Type: credential_entity.TypePassword, Password: "ciphertext"}
				require.NoError(t, credential_repo.Credential().Create(env.ctx, credential))
				return map[string]any{"credential_id": float64(credential.ID)}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPutTest(t)
			asset := &asset_entity.Asset{Name: "local-db", Type: asset_entity.AssetTypeDatabase, Status: asset_entity.StatusActive}
			require.NoError(t, asset.SetDatabaseConfig(&asset_entity.DatabaseConfig{
				Driver: asset_entity.DriverSQLite, SQLiteSource: asset_entity.SQLiteSourceLocal, Path: "/tmp/local.db",
			}))
			require.NoError(t, asset_repo.Asset().Create(env.ctx, asset))

			enteredTransaction := false
			ctx := dbutil.WithTransactionRunner(env.ctx, func(ctx context.Context, fn func(context.Context) error) error {
				enteredTransaction = true
				return fn(ctx)
			})
			candidate := *asset
			_, err := Put(ctx, Request{Asset: &candidate, Config: tt.config(t, env)})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not applicable")
			assert.NotContains(t, err.Error(), "not-applicable")
			assert.False(t, enteredTransaction, "SQLite credential rejection must happen before the write transaction")
		})
	}
}

func TestPutDatabaseUpdatePasswordRetainsExistingDriverContext(t *testing.T) {
	env := setupPutTest(t)
	asset := &asset_entity.Asset{Name: "postgres", Type: asset_entity.AssetTypeDatabase, Status: asset_entity.StatusActive}
	require.NoError(t, asset.SetDatabaseConfig(&asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverPostgreSQL, Host: "db.internal", Port: 5432, Username: "existing-user",
	}))
	require.NoError(t, asset_repo.Asset().Create(env.ctx, asset))

	candidate := *asset
	result, err := Put(env.ctx, Request{Asset: &candidate, Config: map[string]any{"password": "replacement-secret"}})
	require.NoError(t, err)
	assert.Nil(t, result.Authentication)
	stored, err := asset_repo.Asset().Find(env.ctx, asset.ID)
	require.NoError(t, err)
	config, err := stored.GetDatabaseConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.DriverPostgreSQL, config.Driver)
	assert.Zero(t, config.CredentialID)
	plaintext, err := credential_svc.Default().Decrypt(config.Password)
	require.NoError(t, err)
	assert.Equal(t, "replacement-secret", plaintext)
}

func TestPutUpdatePlaintextStoresInlineWithExistingAssetContext(t *testing.T) {
	env := setupPutTest(t)
	asset := newRedisAsset("cache")
	asset.Status = asset_entity.StatusActive
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{Host: "redis.internal", Port: 6379, Username: "existing-user"}))
	require.NoError(t, asset_repo.Asset().Create(env.ctx, asset))

	candidate := *asset
	result, err := Put(env.ctx, Request{Asset: &candidate, Config: map[string]any{"password": "replacement-secret"}})
	require.NoError(t, err)
	assert.Nil(t, result.Authentication)
	stored, err := asset_repo.Asset().Find(env.ctx, asset.ID)
	require.NoError(t, err)
	cfg, err := stored.GetRedisConfig()
	require.NoError(t, err)
	assert.Equal(t, "existing-user", cfg.Username)
	plaintext, err := credential_svc.Default().Decrypt(cfg.Password)
	require.NoError(t, err)
	assert.Equal(t, "replacement-secret", plaintext)
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
	assert.Nil(t, result.Authentication)

	_, err = credential_repo.Credential().Find(env.ctx, oldCredential.ID)
	assert.NoError(t, err, "replaced credentials may be shared and must not be deleted")
	_, credentialCount := env.counts(t)
	assert.Equal(t, int64(1), credentialCount)
	stored, err := asset_repo.Asset().Find(env.ctx, asset.ID)
	require.NoError(t, err)
	cfg, err := stored.GetRedisConfig()
	require.NoError(t, err)
	assert.Zero(t, cfg.CredentialID)
	plaintext, err := credential_svc.Default().Decrypt(cfg.Password)
	require.NoError(t, err)
	assert.Equal(t, "replacement-secret", plaintext)
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

func TestPrepareSSHAgentIncludesInferredAuthTypeInSafeProjection(t *testing.T) {
	env := setupPutTest(t)
	sourceID := seedAgentSource(t, env.ctx)
	input := map[string]any{
		"host": "ssh.internal", "username": "root",
		"agent_source_id": float64(sourceID), "agent_key_fingerprint": validAgentFingerprintForPut(),
	}

	prepared, err := Prepare(env.ctx, Request{Asset: newSSHAsset("agent-box"), Config: input})
	require.NoError(t, err)

	approval := prepared.SafeApprovalDetail()
	config, ok := approval["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, asset_entity.AuthTypeAgent, config["auth_type"], "approval/audit projection must reflect type-owned normalization")
	_, supplied := input["auth_type"]
	assert.False(t, supplied, "Prepare must not mutate caller config while projecting the normalized auth type")
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

// TestSafeAuditArgsOmitsWriteOnlyFieldsAcrossTypes 锁定 Task 8 的 producer 投影
// 契约：password / secret_access_key / kubeconfig 这三类
// write-only 字段在 SafeAuditArgs 与 SafeAuditArgsForResult 中必须整体缺席（不是脱敏），
// 而类型允许的普通 config 与资产身份保留。
func TestSafeAuditArgsOmitsWriteOnlyFieldsAcrossTypes(t *testing.T) {
	tests := []struct {
		name       string
		asset      *asset_entity.Asset
		config     map[string]any
		writeOnly  []string
		secretText string
		allowedKey string
	}{
		{
			name:  "redis password",
			asset: newRedisAsset("cache"),
			config: map[string]any{
				"host": "redis.internal", "username": "default", "password": "pw-secret",
			},
			writeOnly:  []string{"password"},
			secretText: "pw-secret",
			allowedKey: "host",
		},
		{
			name:  "oss secret access key",
			asset: &asset_entity.Asset{Name: "bucket", Type: asset_entity.AssetTypeOSS},
			config: map[string]any{
				"endpoint": "s3.internal", "access_key_id": "AKIAEXAMPLE", "secret_access_key": "sak-secret",
			},
			writeOnly:  []string{"secret_access_key"},
			secretText: "sak-secret",
			allowedKey: "access_key_id",
		},
		{
			name:  "k8s kubeconfig",
			asset: &asset_entity.Asset{Name: "cluster", Type: asset_entity.AssetTypeK8s},
			config: map[string]any{
				"kubeconfig": "apiVersion: v1\nkind: Config\n", "namespace": "prod", "context": "prod-ctx",
			},
			writeOnly:  []string{"kubeconfig"},
			secretText: "apiVersion",
			allowedKey: "namespace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPutTest(t)
			prepared, err := Prepare(env.ctx, Request{Asset: tt.asset, Config: tt.config})
			require.NoError(t, err)
			projections := []map[string]any{
				prepared.SafeAuditArgs(),
				prepared.SafeAuditArgsForResult(&Result{ID: 7, Authentication: &AuthenticationRef{Type: "password", Ref: 3}}),
			}
			for _, proj := range projections {
				encoded, marshalErr := json.Marshal(proj)
				require.NoError(t, marshalErr)
				text := string(encoded)
				assert.NotContains(t, text, tt.secretText)
				for _, field := range tt.writeOnly {
					assert.NotContains(t, text, `"`+field+`":`, "write-only %q must be absent, not redacted", field)
				}
				config, ok := proj["config"].(map[string]any)
				require.True(t, ok)
				assert.Contains(t, config, tt.allowedKey)
			}
		})
	}
}

// TestPrepareSafeViewsOmitCompositeNestedSecretsAndKeepFlatArrays 钉住 asset_put_svc 的
// producer 投影边界：允许审批字段的复合值（嵌套 map 藏 secret）整体省略、绝不进入
// SafeApprovalDetail / SafeAuditArgs；合法扁平字符串数组（brokers/endpoints）归一化为
// 新的 []string 保留。嵌套 secret 不能借任何允许键从共享 Prepare 边界流向 AI/opsctl。
func TestPrepareSafeViewsOmitCompositeNestedSecretsAndKeepFlatArrays(t *testing.T) {
	env := setupPutTest(t)
	// #nosec G101 -- 嵌套 secret 是故意用于证明 allowlist 键下不能藏复合值的夹具。
	secret := "nested-secret-must-not-leak"

	t.Run("ssh optional approval field composite omitted", func(t *testing.T) {
		prepared, err := Prepare(env.ctx, Request{Asset: newSSHAsset("box"), Config: map[string]any{
			"host": "ssh.internal", "username": "root",
			"auth_type": map[string]any{"password": secret},
		}})
		require.NoError(t, err)
		approval := prepared.SafeApprovalDetail()
		config, ok := approval["config"].(map[string]any)
		require.True(t, ok)
		_, hasAuthType := config["auth_type"]
		assert.False(t, hasAuthType, "composite auth_type must be omitted from approval")
		for _, proj := range []map[string]any{approval, prepared.SafeAuditArgs(), prepared.SafeAuditArgsForResult(&Result{ID: 7})} {
			encoded, marshalErr := json.Marshal(proj)
			require.NoError(t, marshalErr)
			assert.NotContains(t, string(encoded), secret)
		}
	})

	t.Run("flat string arrays retained normalized", func(t *testing.T) {
		prepared, err := Prepare(env.ctx, Request{
			Asset: &asset_entity.Asset{Name: "kafka", Type: asset_entity.AssetTypeKafka},
			Config: map[string]any{
				"brokers": []any{"kafka-1:9092", "kafka-2:9092"},
			},
		})
		require.NoError(t, err)
		approval := prepared.SafeApprovalDetail()
		config, ok := approval["config"].(map[string]any)
		require.True(t, ok)
		brokers, ok := config["brokers"].([]string)
		require.True(t, ok, "approval brokers must be normalized to []string")
		assert.Equal(t, []string{"kafka-1:9092", "kafka-2:9092"}, brokers)
	})
}
