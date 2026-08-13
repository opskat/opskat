package assettype

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareCreateCoversRegisteredBuiltins(t *testing.T) {
	valid := map[string]map[string]any{
		asset_entity.AssetTypeSSH: {
			"host": "ssh.example.com", "username": "root", "password": "secret",
		},
		asset_entity.AssetTypeDatabase: {
			"driver": "postgresql", "host": "db.example.com", "username": "postgres", "password": "secret",
		},
		asset_entity.AssetTypeRedis: {
			"host": "redis.example.com", "username": "default", "password": "secret",
		},
		asset_entity.AssetTypeMongoDB: {
			"host": "mongo.example.com", "username": "admin", "password": "secret",
		},
		asset_entity.AssetTypeKafka: {
			"brokers": []any{"kafka.example.com:9092"}, "sasl_mechanism": "plain", "username": "alice", "password": "secret",
		},
		asset_entity.AssetTypeK8s: {
			"kubeconfig": "apiVersion: v1", "namespace": "default",
		},
		asset_entity.AssetTypeSerial: {
			"port_path": "/dev/ttyUSB0", "baud_rate": float64(115200),
		},
		asset_entity.AssetTypeEtcd: {
			"endpoints": []any{"etcd.example.com:2379"}, "username": "root", "password": "secret",
		},
		asset_entity.AssetTypeLocal: {
			"shell": "/bin/zsh",
		},
		asset_entity.AssetTypeVNC: {
			"host": "vnc.example.com", "password": "secret",
		},
		asset_entity.AssetTypeRDP: {
			"host": "rdp.example.com", "username": "operator", "password": "secret",
		},
		asset_entity.AssetTypeOSS: {
			"endpoint": "s3.example.com", "access_key_id": "AKIA", "secret_access_key": "secret",
		},
	}

	types := RegisteredTypes()
	assert.True(t, sort.StringsAreSorted(types), "registered types must be stable and sorted")

	for _, assetType := range types {
		t.Run(assetType, func(t *testing.T) {
			args, ok := valid[assetType]
			require.True(t, ok, "every registered built-in needs a contract fixture")
			prepared, err := PrepareCreate(assetType, args)
			require.NoError(t, err)
			assert.Equal(t, assetType, prepared.Handler.Type())
			assert.NotEmpty(t, prepared.Handler.AutomationContract().ConfigFields)
			for _, secretField := range []string{"password", "private_key", "passphrase", "secret_access_key", "kubeconfig"} {
				assert.NotContains(t, prepared.Approval, secretField)
			}
		})
	}
}

func TestPrepareCreateRejectsNamedUnknownFields(t *testing.T) {
	_, err := PrepareCreate(asset_entity.AssetTypeRedis, map[string]any{
		"host": "redis.example.com", "username": "default",
		"z_typo": true, "a_typo": true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a_typo")
	assert.Contains(t, err.Error(), "z_typo")
	assert.Less(t, strings.Index(err.Error(), "a_typo"), strings.Index(err.Error(), "z_typo"))
}

func TestPrepareCreateNormalizesOwnerDefaults(t *testing.T) {
	tests := []struct {
		name      string
		assetType string
		args      map[string]any
		want      map[string]any
	}{
		{
			name: "ssh port and auth", assetType: asset_entity.AssetTypeSSH,
			args: map[string]any{"host": "ssh.example.com", "username": "root", "password": "secret"},
			want: map[string]any{"port": 22, "auth_type": asset_entity.AuthTypePassword},
		},
		{
			name: "postgresql port", assetType: asset_entity.AssetTypeDatabase,
			args: map[string]any{"driver": "postgresql", "host": "db.example.com", "username": "postgres"},
			want: map[string]any{"port": 5432},
		},
		{
			name: "serial shape", assetType: asset_entity.AssetTypeSerial,
			args: map[string]any{"port_path": "/dev/ttyUSB0", "baud_rate": 9600},
			want: map[string]any{"data_bits": 8, "stop_bits": "1", "parity": "none"},
		},
		{
			name: "rdp shape", assetType: asset_entity.AssetTypeRDP,
			args: map[string]any{"host": "rdp.example.com", "username": "operator"},
			want: map[string]any{"port": 3389, "width": 1280, "height": 720, "clipboard": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared, err := PrepareCreate(tt.assetType, tt.args)
			require.NoError(t, err)
			for key, want := range tt.want {
				assert.Equal(t, want, prepared.Config[key], key)
				assert.Equal(t, want, prepared.Approval[key], key)
			}
		})
	}
}

func TestCredentialPlanAndBindingArePure(t *testing.T) {
	t.Run("password owner", func(t *testing.T) {
		args := map[string]any{
			"driver": "mysql", "host": "db.example.com", "username": "admin", "password": "secret",
		}
		prepared, err := PrepareCreate(asset_entity.AssetTypeDatabase, args)
		require.NoError(t, err)
		assert.Equal(t, CredentialKindPassword, prepared.Credential.Kind)
		assert.Equal(t, "secret", prepared.Credential.Plaintext)
		assert.Equal(t, "admin", prepared.Credential.Username)

		bound, err := prepared.BindCredential(CredentialBinding{ID: 42, Type: credential_entity.TypePassword})
		require.NoError(t, err)
		assert.Equal(t, int64(42), bound["credential_id"])
		assert.NotContains(t, bound, "password")
		assert.Equal(t, "secret", args["password"], "preparation and binding must not mutate caller input")
	})

	t.Run("ssh key owner", func(t *testing.T) {
		prepared, err := PrepareCreate(asset_entity.AssetTypeSSH, map[string]any{
			"host": "ssh.example.com", "username": "root", "private_key": "PEM", "passphrase": "key-pass",
		})
		require.NoError(t, err)
		assert.Equal(t, CredentialKindSSHKey, prepared.Credential.Kind)
		assert.Equal(t, "PEM", prepared.Credential.PrivateKey)
		assert.Equal(t, "key-pass", prepared.Credential.Passphrase)

		bound, err := prepared.BindCredential(CredentialBinding{ID: 7, Type: credential_entity.TypeSSHKey})
		require.NoError(t, err)
		assert.Equal(t, int64(7), bound["credential_id"])
		assert.Equal(t, asset_entity.AuthTypeKey, bound["auth_type"])
		assert.NotContains(t, bound, "private_key")
		assert.NotContains(t, bound, "passphrase")
	})

	t.Run("ssh reference type owns auth inference", func(t *testing.T) {
		prepared, err := PrepareCreate(asset_entity.AssetTypeSSH, map[string]any{
			"host": "ssh.example.com", "username": "root", "credential_id": float64(9),
		})
		require.NoError(t, err)
		assert.Equal(t, CredentialKindReference, prepared.Credential.Kind)
		assert.ElementsMatch(t, []string{credential_entity.TypePassword, credential_entity.TypeSSHKey}, prepared.Credential.AcceptedTypes)

		bound, err := prepared.BindCredential(CredentialBinding{ID: 9, Type: credential_entity.TypeSSHKey})
		require.NoError(t, err)
		assert.Equal(t, asset_entity.AuthTypeKey, bound["auth_type"])
	})

	t.Run("ssh passphrase requires imported private key", func(t *testing.T) {
		for _, args := range []map[string]any{
			{"host": "ssh.example.com", "username": "root", "password": "password-secret", "passphrase": "key-secret"},
			{"host": "ssh.example.com", "username": "root", "credential_id": int64(9), "passphrase": "key-secret"},
		} {
			_, err := PrepareCreate(asset_entity.AssetTypeSSH, args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "passphrase requires private_key")
			assert.NotContains(t, err.Error(), "key-secret")
		}
	})

	t.Run("managed credential IDs must be integral", func(t *testing.T) {
		for _, tt := range []struct {
			assetType string
			args      map[string]any
		}{
			{assetType: asset_entity.AssetTypeSSH, args: map[string]any{"host": "ssh.example.com", "username": "root", "credential_id": 9.5}},
			{assetType: asset_entity.AssetTypeRedis, args: map[string]any{"host": "redis.example.com", "username": "default", "credential_id": 9.5}},
		} {
			_, err := PrepareCreate(tt.assetType, tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "credential_id must be a positive integer")
		}
	})

	t.Run("oss owns secret field mapping", func(t *testing.T) {
		prepared, err := PrepareCreate(asset_entity.AssetTypeOSS, map[string]any{
			"endpoint": "s3.example.com", "access_key_id": "AKIA", "secret_access_key": "secret",
		})
		require.NoError(t, err)
		assert.Equal(t, CredentialKindPassword, prepared.Credential.Kind)
		assert.Equal(t, "AKIA", prepared.Credential.Username)
		bound, err := prepared.BindCredential(CredentialBinding{ID: 3, Type: credential_entity.TypePassword})
		require.NoError(t, err)
		assert.NotContains(t, bound, "secret_access_key")
		assert.Equal(t, int64(3), bound["credential_id"])
	})

	t.Run("password owners reject SSH key binding", func(t *testing.T) {
		prepared, err := PrepareCreate(asset_entity.AssetTypeRedis, map[string]any{
			"host": "redis.example.com", "username": "default", "credential_id": float64(8),
		})
		require.NoError(t, err)
		_, err = prepared.BindCredential(CredentialBinding{ID: 8, Type: credential_entity.TypeSSHKey})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "password")
	})
}

func TestMaterializedBindingFlowsThroughOwnerApply(t *testing.T) {
	passwordOwners := map[string]map[string]any{
		asset_entity.AssetTypeDatabase: {"driver": "mysql", "host": "db.example.com", "username": "admin", "credential_id": float64(41)},
		asset_entity.AssetTypeRedis:    {"host": "redis.example.com", "username": "default", "credential_id": float64(41)},
		asset_entity.AssetTypeMongoDB:  {"host": "mongo.example.com", "username": "admin", "credential_id": float64(41)},
		asset_entity.AssetTypeKafka: {
			"brokers": []any{"kafka.example.com:9092"}, "sasl_mechanism": "plain", "username": "alice", "credential_id": float64(41),
		},
		asset_entity.AssetTypeEtcd: {"endpoints": []any{"etcd.example.com:2379"}, "username": "root", "credential_id": float64(41)},
		asset_entity.AssetTypeRDP:  {"host": "rdp.example.com", "username": "operator", "credential_id": float64(41)},
		asset_entity.AssetTypeVNC:  {"host": "vnc.example.com", "credential_id": float64(41)},
		asset_entity.AssetTypeOSS:  {"endpoint": "s3.example.com", "access_key_id": "AKIA", "credential_id": float64(41)},
	}
	for assetType, args := range passwordOwners {
		t.Run(assetType, func(t *testing.T) {
			prepared, err := PrepareCreate(assetType, args)
			require.NoError(t, err)
			bound, err := prepared.BindCredential(CredentialBinding{ID: 41, Type: credential_entity.TypePassword})
			require.NoError(t, err)
			a := &asset_entity.Asset{Name: assetType, Type: assetType}
			require.NoError(t, prepared.Handler.ApplyCreateArgs(t.Context(), a, bound))
			var stored map[string]any
			require.NoError(t, json.Unmarshal([]byte(a.Config), &stored))
			assert.Equal(t, float64(41), stored["credential_id"])
		})
	}

	t.Run("ssh key", func(t *testing.T) {
		prepared, err := PrepareCreate(asset_entity.AssetTypeSSH, map[string]any{
			"host": "ssh.example.com", "username": "root", "credential_id": float64(42),
		})
		require.NoError(t, err)
		bound, err := prepared.BindCredential(CredentialBinding{ID: 42, Type: credential_entity.TypeSSHKey})
		require.NoError(t, err)
		a := &asset_entity.Asset{Name: "ssh", Type: asset_entity.AssetTypeSSH}
		require.NoError(t, prepared.Handler.ApplyCreateArgs(t.Context(), a, bound))
		cfg, err := a.GetSSHConfig()
		require.NoError(t, err)
		assert.Equal(t, int64(42), cfg.CredentialID)
		assert.Equal(t, asset_entity.AuthTypeKey, cfg.AuthType)
	})
}

func TestOwnersRejectInapplicableCredentialInputs(t *testing.T) {
	for _, tt := range []struct {
		name      string
		assetType string
		args      map[string]any
		field     string
	}{
		{
			name: "sqlite password", assetType: asset_entity.AssetTypeDatabase,
			args: map[string]any{"driver": "sqlite", "path": "/tmp/test.db", "password": "secret"}, field: "password",
		},
		{
			name: "sqlite reference", assetType: asset_entity.AssetTypeDatabase,
			args: map[string]any{"driver": "sqlite", "path": "/tmp/test.db", "credential_id": float64(4)}, field: "credential_id",
		},
		{
			name: "ssh agent password", assetType: asset_entity.AssetTypeSSH,
			args: map[string]any{
				"host": "ssh.example.com", "username": "root", "auth_type": "agent",
				"agent_source_id": float64(1), "agent_key_fingerprint": "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "password": "secret",
			}, field: "password",
		},
		{
			name: "k8s password", assetType: asset_entity.AssetTypeK8s,
			args: map[string]any{"kubeconfig": "apiVersion: v1", "password": "secret"}, field: "password",
		},
		{
			name: "serial credential", assetType: asset_entity.AssetTypeSerial,
			args: map[string]any{"port_path": "/dev/ttyUSB0", "baud_rate": 9600, "credential_id": float64(2)}, field: "credential_id",
		},
		{
			name: "local password", assetType: asset_entity.AssetTypeLocal,
			args: map[string]any{"password": "secret"}, field: "password",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PrepareCreate(tt.assetType, tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.field)
		})
	}
}

func TestDatabaseHandlerOwnsDriverDefaultPorts(t *testing.T) {
	for _, tt := range []struct {
		driver string
		port   int
	}{
		{driver: "mysql", port: 3306},
		{driver: "postgresql", port: 5432},
		{driver: "mssql", port: 1433},
	} {
		t.Run(tt.driver, func(t *testing.T) {
			a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
			err := (&databaseHandler{}).ApplyCreateArgs(t.Context(), a, map[string]any{
				"driver": tt.driver, "host": "db.example.com", "username": "admin",
			})
			require.NoError(t, err)
			cfg, err := a.GetDatabaseConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.port, cfg.Port)
		})
	}
}

func TestDatabasePreparationSupportsSQLiteShapes(t *testing.T) {
	for _, tt := range []struct {
		name string
		args map[string]any
		want map[string]any
	}{
		{
			name: "local",
			args: map[string]any{"driver": "sqlite", "path": "/tmp/local.db"},
			want: map[string]any{"sqlite_source": string(asset_entity.SQLiteSourceLocal)},
		},
		{
			name: "remote",
			args: map[string]any{
				"driver": "sqlite", "path": "/var/lib/app.db",
				"sqlite_source": string(asset_entity.SQLiteSourceRemoteSSHVFS), "ssh_asset_id": float64(12),
			},
			want: map[string]any{"sqlite_source": string(asset_entity.SQLiteSourceRemoteSSHVFS), "ssh_asset_id": float64(12)},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prepared, err := PrepareCreate(asset_entity.AssetTypeDatabase, tt.args)
			require.NoError(t, err)
			assert.Equal(t, CredentialKindNone, prepared.Credential.Kind)
			for key, want := range tt.want {
				assert.Equal(t, want, prepared.Config[key])
			}

			a := &asset_entity.Asset{Name: "sqlite", Type: asset_entity.AssetTypeDatabase}
			require.NoError(t, prepared.Handler.ApplyCreateArgs(t.Context(), a, prepared.Config))
			require.NoError(t, a.Validate())
		})
	}
}

func TestAllMatchesRegisteredTypesOrder(t *testing.T) {
	handlers := All()
	got := make([]string, 0, len(handlers))
	for _, handler := range handlers {
		got = append(got, handler.Type())
	}
	assert.Equal(t, RegisteredTypes(), got)
}

func TestPrepareCreateDoesNotAdvertiseUnappliedFields(t *testing.T) {
	for _, tt := range []struct {
		assetType string
		args      map[string]any
		field     string
	}{
		{
			assetType: asset_entity.AssetTypeDatabase,
			args:      map[string]any{"driver": "mysql", "host": "db.example.com", "username": "admin", "ssl_mode": "require"},
			field:     "ssl_mode",
		},
		{
			assetType: asset_entity.AssetTypeRedis,
			args:      map[string]any{"host": "redis.example.com", "username": "default", "tls": true},
			field:     "tls",
		},
		{
			assetType: asset_entity.AssetTypeMongoDB,
			args:      map[string]any{"connection_uri": "mongodb://mongo.example.com/app"},
			field:     "connection_uri",
		},
		{
			assetType: asset_entity.AssetTypeOSS,
			args:      map[string]any{"endpoint": "s3.example.com", "access_key_id": "AKIA", "skip_tls_verify": true},
			field:     "skip_tls_verify",
		},
	} {
		t.Run(tt.assetType+"_"+tt.field, func(t *testing.T) {
			_, err := PrepareCreate(tt.assetType, tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.field)
		})
	}
}
