package extension_svc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/extension_data_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_settings_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/repository/extension_data_repo"
	"github.com/opskat/opskat/internal/repository/extension_settings_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

// settingsFixture is an extension service on a real database, with extension
// "jev" installed: it declares settings (an endpoint, an API key, a mode and a
// timeout) and permissions.network.settingsEndpoint.
type settingsFixture struct {
	ctx  context.Context
	svc  *Service
	repo extension_settings_repo.ExtensionSettingsRepo
	stub *describeCacheStub
	dir  string
}

func newSettingsFixture(t *testing.T) *settingsFixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(
		&extension_data_entity.ExtensionData{},
		&extension_settings_entity.ExtensionSettings{},
		&extension_state_entity.ExtensionState{},
	))
	db.SetDefault(gdb) // the guest's KV store runs on the default database
	prevCred := credential_svc.Default()
	credential_svc.SetDefault(credential_svc.New("settings-test", []byte("0123456789abcdef")))
	t.Cleanup(func() { credential_svc.SetDefault(prevCred) })

	stub := installDescribeStub(t)
	dir := t.TempDir()
	writeSettingsExtension(t, stub, dir, "jev", map[string]any{
		"configSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"baseURL": map[string]any{"type": "string", "format": "endpoint", "title": "settings.baseURL"},
				"apiKey":  map[string]any{"type": "string", "format": "password"},
				"mode":    map[string]any{"type": "string", "enum": []any{"fast", "safe"}},
				"timeout": map[string]any{"type": "integer"},
				"verbose": map[string]any{"type": "boolean"},
			},
			"required": []any{"baseURL", "apiKey"},
		},
		"auth": map[string]any{"groups": []any{map[string]any{"bindings": []any{
			map[string]any{"in": "header", "name": "Authorization", "value": "Bearer {{apiKey}}"},
		}}}},
	})

	mgr := extension.NewManager(dir, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{Logger: zap.NewNop()})
	}, extension.NewContributionRegistry(append(extreg.Points(), SettingsPoint())...), zap.NewNop())
	repo := extension_settings_repo.NewExtensionSettings()
	svc := New(mgr, extension_state_repo.NewExtensionState(), extension_data_repo.NewExtensionData(), repo,
		nil, zap.NewNop(), nil, nil)
	return &settingsFixture{ctx: context.Background(), svc: svc, repo: repo, stub: stub, dir: dir}
}

func writeSettingsExtension(t *testing.T, stub *describeCacheStub, dir, name string, settings map[string]any) {
	t.Helper()
	extDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(extDir, 0o755))
	manifest := map[string]any{
		"name":        name,
		"version":     "1.0.0",
		"hostABI":     extmanifest.HostABIVersion,
		"backend":     map[string]any{"runtime": "wasm", "binary": "main.wasm"},
		"permissions": map[string]any{"network": map[string]any{"settingsEndpoint": true}},
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "main.wasm"), minimalWASM, 0o600))
	stub.put(name, map[string]any{
		"i18n":        map[string]any{"displayName": name, "description": name},
		"contributes": map[string]any{extwire.KeySettings: settings},
	})
}

// stored is the settings row as written to the database, plain values and
// secrets' ciphertext in one map.
func (f *settingsFixture) stored(t *testing.T) map[string]any {
	t.Helper()
	row, err := f.repo.Find(f.ctx, "jev")
	require.NoError(t, err)
	require.NotNil(t, row)
	var parsed storedSettingsRow
	require.NoError(t, json.Unmarshal([]byte(row.Value), &parsed))
	out := map[string]any{}
	for k, v := range parsed.Values {
		out[k] = v
	}
	for k, v := range parsed.Secrets {
		out[k] = v
	}
	return out
}

func (f *settingsFixture) save(t *testing.T, values map[string]any) []SettingsFieldError {
	t.Helper()
	errs, err := f.svc.SaveSettings(f.ctx, "jev", values)
	require.NoError(t, err)
	return errs
}

func (f *settingsFixture) decrypted(t *testing.T) map[string]any {
	t.Helper()
	raw, err := f.svc.DecryptedSettings(f.ctx, "jev")
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestSettingsDefinitionIsTheDeclaration(t *testing.T) {
	f := newSettingsFixture(t)
	def, err := f.svc.SettingsDefinition("jev")
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, []string{"apiKey"}, def.SecretFields())
	assert.Equal(t, []string{"baseURL"}, def.EndpointFields())
	require.NotNil(t, def.Auth)
	assert.Equal(t, "Bearer {{apiKey}}", def.Auth.Groups[0].Bindings[0].Value)
}

func TestSaveSettingsEncryptsSecretsAndReadsThemBackAsSet(t *testing.T) {
	f := newSettingsFixture(t)

	view, err := f.svc.SettingsView(f.ctx, "jev")
	require.NoError(t, err)
	assert.Empty(t, view.Values)
	assert.Equal(t, map[string]bool{"apiKey": false}, view.Secrets)

	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://jev.example", "apiKey": "s3cret", "timeout": float64(30)}))

	stored := f.stored(t)
	assert.Equal(t, "https://jev.example", stored["baseURL"])
	require.IsType(t, "", stored["apiKey"])
	assert.NotEqual(t, "s3cret", stored["apiKey"], "the secret is stored encrypted")
	plain, err := credential_svc.Default().Decrypt(stored["apiKey"].(string))
	require.NoError(t, err)
	assert.Equal(t, "s3cret", plain)

	view, err = f.svc.SettingsView(f.ctx, "jev")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"baseURL": "https://jev.example", "timeout": float64(30)}, view.Values,
		"the read view carries no secret, not even its ciphertext")
	assert.Equal(t, map[string]bool{"apiKey": true}, view.Secrets)

	assert.Equal(t, map[string]any{"baseURL": "https://jev.example", "apiKey": "s3cret", "timeout": float64(30)}, f.decrypted(t))
}

func TestSaveSettingsBlankSecretKeepsTheStoredOne(t *testing.T) {
	f := newSettingsFixture(t)
	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "s3cret"}))
	before := f.stored(t)["apiKey"]

	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://b.example", "apiKey": ""}))
	assert.Equal(t, before, f.stored(t)["apiKey"], "blank keeps the stored ciphertext as it is")
	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://c.example"}))
	assert.Equal(t, map[string]any{"baseURL": "https://c.example", "apiKey": "s3cret"}, f.decrypted(t))

	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://c.example", "apiKey": "rotated"}))
	assert.Equal(t, "rotated", f.decrypted(t)["apiKey"])
}

// An upgrade can change whether a field is secret. The value stored as the other
// kind is never read as this one — ciphertext never shows (to the form, to the
// guest's config.get) as a plain value, a plaintext value is never "decrypted" —
// the field reads as not set until the user saves it again.
func TestSettingsFieldChangingSecretnessReadsAsUnset(t *testing.T) {
	f := newSettingsFixture(t)
	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "s3cret", "mode": "fast"}))

	writeSettingsExtension(t, f.stub, f.dir, "jev", map[string]any{"configSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"baseURL": map[string]any{"type": "string", "format": "endpoint"},
			"apiKey":  map[string]any{"type": "string"},
			"mode":    map[string]any{"type": "string", "format": "password"},
		},
		"required": []any{"baseURL"},
	}})

	view, err := f.svc.SettingsView(f.ctx, "jev")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"baseURL": "https://a.example"}, view.Values, "the old ciphertext is no plain value")
	assert.Equal(t, map[string]bool{"mode": false}, view.Secrets, "a plaintext value is no stored secret")
	assert.Equal(t, map[string]any{"baseURL": "https://a.example"}, f.decrypted(t))

	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "plain-now"}))
	assert.Equal(t, map[string]any{"baseURL": "https://a.example", "apiKey": "plain-now"}, f.decrypted(t),
		"a blank secret keeps only a value stored as a secret")
}

func TestSaveSettingsRefusesInvalidValuesAndSavesNothing(t *testing.T) {
	f := newSettingsFixture(t)
	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "s3cret", "mode": "fast"}))
	before := f.stored(t)
	hashBefore, err := f.svc.SettingsHash(f.ctx, "jev")
	require.NoError(t, err)

	errs := f.save(t, map[string]any{
		"baseURL": "",
		"apiKey":  "new",
		"mode":    "turbo",
		"timeout": "5",
		"verbose": "yes",
		"extra":   1,
	})
	assert.Equal(t, []SettingsFieldError{
		{Field: "baseURL", Code: SettingsErrRequired},
		{Field: "extra", Code: SettingsErrUnknown},
		{Field: "mode", Code: SettingsErrEnum},
		{Field: "timeout", Code: SettingsErrType},
		{Field: "verbose", Code: SettingsErrType},
	}, errs)
	assert.Equal(t, before, f.stored(t), "a refused save leaves the stored settings as they were")
	hashAfter, err := f.svc.SettingsHash(f.ctx, "jev")
	require.NoError(t, err)
	assert.Equal(t, hashBefore, hashAfter)

	assert.Equal(t, []SettingsFieldError{{Field: "timeout", Code: SettingsErrType}},
		f.save(t, map[string]any{"baseURL": "https://a.example", "timeout": 1.5}), "an integer field takes whole numbers only")
}

func TestSaveSettingsRequiresASecretNeverStored(t *testing.T) {
	f := newSettingsFixture(t)
	assert.Equal(t, []SettingsFieldError{{Field: "apiKey", Code: SettingsErrRequired}},
		f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": ""}))
	row, err := f.repo.Find(f.ctx, "jev")
	require.NoError(t, err)
	assert.Nil(t, row)
}

func TestSettingsHashFollowsTheStoredValues(t *testing.T) {
	f := newSettingsFixture(t)
	empty, err := f.svc.SettingsHash(f.ctx, "jev")
	require.NoError(t, err)
	assert.Len(t, empty, 64)

	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "one"}))
	first, err := f.svc.SettingsHash(f.ctx, "jev")
	require.NoError(t, err)
	again, err := f.svc.SettingsHash(f.ctx, "jev")
	require.NoError(t, err)
	assert.Equal(t, first, again, "stable while nothing is saved")
	assert.NotEqual(t, empty, first)

	require.Empty(t, f.save(t, map[string]any{"apiKey": "two", "baseURL": "https://a.example"}))
	rotated, err := f.svc.SettingsHash(f.ctx, "jev")
	require.NoError(t, err)
	assert.NotEqual(t, first, rotated, "changing only the secret changes the hash")
}

func TestUninstallWithCleanDataDeletesTheSettings(t *testing.T) {
	for _, cleanData := range []bool{false, true} {
		f := newSettingsFixture(t)
		require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "s3cret"}))
		require.NoError(t, f.svc.Uninstall(f.ctx, "jev", cleanData, true))
		row, err := f.repo.Find(f.ctx, "jev")
		require.NoError(t, err)
		if cleanData {
			assert.Nil(t, row, "clean data removes the settings")
		} else {
			assert.NotNil(t, row, "uninstalling without clean data keeps them")
		}
	}
}

// The guest's KV store is another table: no key reaches the settings, and
// writing one does not change them.
func TestGuestKVCannotReachTheSettings(t *testing.T) {
	f := newSettingsFixture(t)
	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "s3cret"}))

	kv := f.svc.HostConfig("jev").KV
	for _, key := range []string{"settings", "ext.settings", "jev", "apiKey"} {
		v, err := kv.Get(key)
		require.NoError(t, err)
		assert.Nil(t, v, key)
	}
	require.NoError(t, kv.Set("ext.settings", []byte(`{"baseURL":"https://evil.example"}`)))
	require.NoError(t, kv.Set("settings", []byte(`{"baseURL":"https://evil.example"}`)))
	assert.Equal(t, "https://a.example", f.decrypted(t)["baseURL"])
}

func TestSettingsOfAnExtensionDeclaringNone(t *testing.T) {
	f := newSettingsFixture(t)
	writeSettingsExtensionWithout(t, f, "plain")
	def, err := f.svc.SettingsDefinition("plain")
	require.NoError(t, err)
	assert.Nil(t, def)
	_, err = f.svc.SaveSettings(f.ctx, "plain", map[string]any{"a": "b"})
	assert.ErrorContains(t, err, `extension "plain" declares no settings`)
}

func writeSettingsExtensionWithout(t *testing.T, f *settingsFixture, name string) {
	t.Helper()
	writeSlimManifest(filepath.Dir(f.svc.Manager().ExtDir(name)), name)
}

func TestSettingsFormIsLocalized(t *testing.T) {
	f := newSettingsFixture(t)
	require.Empty(t, f.save(t, map[string]any{"baseURL": "https://a.example", "apiKey": "s3cret"}))
	form, err := f.svc.SettingsForm(f.ctx, "jev", "en")
	require.NoError(t, err)
	require.NotNil(t, form)
	props := form.ConfigSchema["properties"].(map[string]any)
	// No locale file: the key renders as itself, through the same translator the
	// asset form's schema goes through.
	assert.Equal(t, "settings.baseURL", props["baseURL"].(map[string]any)["title"])
	assert.Equal(t, map[string]bool{"apiKey": true}, form.Secrets)
	assert.Equal(t, map[string]any{"baseURL": "https://a.example"}, form.Values)
	require.NotNil(t, form.Auth)
}
