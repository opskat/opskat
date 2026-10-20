package extension_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/pkg/extension"
)

type memDataRepo struct {
	rows map[string][]byte
}

func (m *memDataRepo) key(ext, k string) string { return ext + "\x00" + k }

func (m *memDataRepo) Get(_ context.Context, extName, key string) ([]byte, error) {
	v, ok := m.rows[m.key(extName, key)]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return append([]byte(nil), v...), nil
}

func (m *memDataRepo) Set(_ context.Context, extName, key string, value []byte) error {
	m.rows[m.key(extName, key)] = append([]byte(nil), value...)
	return nil
}

func (m *memDataRepo) Delete(context.Context, string, string) error { return nil }
func (m *memDataRepo) DeleteAll(context.Context, string) error      { return nil }

func settingsService(t *testing.T) *Service {
	t.Helper()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"endpoint": map[string]any{"type": "string", "format": "endpoint"},
			"token":    map[string]any{"type": "string", "format": "password"},
			"model":    map[string]any{"type": "string"},
		},
		"required": []any{"endpoint"},
	}
	mgr := extension.NewManager(t.TempDir(), func(string) extension.HostProvider { return nil }, nopLogger())
	mgr.PutForTest(&extension.Extension{
		Name: "rev",
		Manifest: &extension.Manifest{
			Name:           "rev",
			SettingsSchema: schema,
		},
	})
	return &Service{
		manager:  mgr,
		dataRepo: &memDataRepo{rows: map[string][]byte{}},
		logger:   nopLogger(),
	}
}

func TestSaveExtensionSettingsEncryptsPasswords(t *testing.T) {
	svc := settingsService(t)
	err := svc.SaveExtensionSettings(context.Background(), "rev", map[string]string{
		"endpoint": "https://review.example.com",
		"token":    "s3cret",
		"model":    "m",
	}, func(plain string) (string, error) { return "enc:" + plain, nil })
	require.NoError(t, err)

	pub, err := svc.PublicSettings(context.Background(), "rev")
	require.NoError(t, err)
	assert.Equal(t, "https://review.example.com", pub["endpoint"])
	assert.Equal(t, "m", pub["model"])
	assert.Equal(t, "{{setting.token}}", pub["token"], "已设置的密钥只给占位符，扩展拿它写请求头，拿不到明文")

	secrets, err := svc.SettingSecrets(context.Background(), "rev", []string{"token"}, func(v string) (string, error) {
		return v[len("enc:"):], nil
	})
	require.NoError(t, err)
	assert.Equal(t, "s3cret", secrets["token"])

	err = svc.SaveExtensionSettings(context.Background(), "rev", map[string]string{"model": "m"}, func(plain string) (string, error) { return "enc:" + plain, nil })
	assert.Error(t, err, "缺必填字段要失败")
}

// 设置表单读回时密钥只显示"已设置"，用户不改密钥再保存时提交的是空值，不能因此清掉已存的密钥。
func TestSaveExtensionSettingsKeepsSecretWhenLeftBlank(t *testing.T) {
	svc := settingsService(t)
	ctx := context.Background()
	encrypt := func(plain string) (string, error) { return "enc:" + plain, nil }
	decrypt := func(v string) (string, error) { return v[len("enc:"):], nil }

	require.NoError(t, svc.SaveExtensionSettings(ctx, "rev", map[string]string{
		"endpoint": "https://review.example.com", "token": "s3cret",
	}, encrypt))
	require.NoError(t, svc.SaveExtensionSettings(ctx, "rev", map[string]string{
		"endpoint": "https://review2.example.com", "token": "",
	}, encrypt))

	secrets, err := svc.SettingSecrets(ctx, "rev", []string{"token"}, decrypt)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", secrets["token"])
	pub, err := svc.PublicSettings(ctx, "rev")
	require.NoError(t, err)
	assert.Equal(t, "https://review2.example.com", pub["endpoint"])

	require.NoError(t, svc.SaveExtensionSettings(ctx, "rev", map[string]string{
		"endpoint": "https://review2.example.com", "token": "n3w",
	}, encrypt))
	secrets, err = svc.SettingSecrets(ctx, "rev", []string{"token"}, decrypt)
	require.NoError(t, err)
	assert.Equal(t, "n3w", secrets["token"])
}

// 设置表单读回时只知道密钥"已设置"，拿不到密文或明文。
func TestSettingsViewHidesSecrets(t *testing.T) {
	svc := settingsService(t)
	ctx := context.Background()
	require.NoError(t, svc.SaveExtensionSettings(ctx, "rev", map[string]string{
		"endpoint": "https://review.example.com", "token": "s3cret",
	}, func(plain string) (string, error) { return "enc:" + plain, nil }))

	view, err := svc.SettingsView(ctx, "rev", "en")
	require.NoError(t, err)
	assert.Equal(t, "https://review.example.com", view.Values["endpoint"])
	_, has := view.Values["token"]
	assert.False(t, has)
	assert.Equal(t, []string{"token"}, view.SecretsSet)
	assert.NotNil(t, view.Schema)
}

// 没设置的密钥字段不出现在 settings_get 里，扩展据此知道"还没配置"。
func TestPublicSettingsOmitsUnsetSecrets(t *testing.T) {
	svc := settingsService(t)
	require.NoError(t, svc.SaveExtensionSettings(context.Background(), "rev", map[string]string{
		"endpoint": "https://review.example.com",
	}, func(plain string) (string, error) { return "enc:" + plain, nil }))

	pub, err := svc.PublicSettings(context.Background(), "rev")
	require.NoError(t, err)
	_, has := pub["token"]
	assert.False(t, has)
}

// 已存过密钥后再保存，表单可以不带密钥字段（留空表示不修改）：必填检查认已存的密钥，
// 不能因此报"必填"。从没存过的必填密钥照样要求填写。
func TestSaveExtensionSettingsRequiredSecretCanBeKept(t *testing.T) {
	svc := settingsService(t)
	schema := svc.manager.GetExtension("rev").Manifest.SettingsSchema
	schema["required"] = []any{"endpoint", "token"}
	ctx := context.Background()
	encrypt := func(plain string) (string, error) { return "enc:" + plain, nil }

	err := svc.SaveExtensionSettings(ctx, "rev", map[string]string{"endpoint": "https://review.example.com"}, encrypt)
	require.Error(t, err, "第一次保存没填必填的密钥要报错")

	require.NoError(t, svc.SaveExtensionSettings(ctx, "rev", map[string]string{
		"endpoint": "https://review.example.com", "token": "s3cret",
	}, encrypt))
	require.NoError(t, svc.SaveExtensionSettings(ctx, "rev", map[string]string{
		"endpoint": "https://review2.example.com",
	}, encrypt), "已存过密钥，不带密钥字段也能保存")

	secrets, err := svc.SettingSecrets(ctx, "rev", []string{"token"}, func(v string) (string, error) { return v[len("enc:"):], nil })
	require.NoError(t, err)
	assert.Equal(t, "s3cret", secrets["token"])
}
