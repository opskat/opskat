package backup_svc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
)

// grafanaCustomType 是一个 HTTP 执行方式、含一个必填明文字段与一个必填密钥字段的
// 自定义类型，供备份导出 / 导入测试复用。
func grafanaCustomType(slug string) *custom_type_entity.CustomType {
	return &custom_type_entity.CustomType{
		Slug:     slug,
		Name:     "Grafana",
		ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{
			{Name: "host", Required: true},
			{Name: "token", Secret: true, Required: true},
		},
		HTTP: &custom_type_entity.HTTPConfig{
			BaseURL: "https://{{host}}",
			Auth:    []custom_type_entity.AuthBinding{{Type: "header", Name: "Authorization", Values: []string{`{{"Bearer " + token}}`}}},
		},
		Createtime: 1,
		Updatetime: 1,
	}
}

func createGenericAsset(t *testing.T, name, slug string, values map[string]asset_entity.GenericValue) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeGeneric, Status: asset_entity.StatusActive}
	require.NoError(t, a.SetGenericConfig(&asset_entity.GenericConfig{CustomType: slug, Values: values}))
	return a
}

// findGenericConfig 按名称查找资产并解析通用资产配置。
func findGenericConfig(t *testing.T, assets []*asset_entity.Asset, name string) *asset_entity.GenericConfig {
	t.Helper()
	for _, a := range assets {
		if a.Name == name {
			cfg, err := a.GetGenericConfig()
			require.NoError(t, err)
			return cfg
		}
	}
	t.Fatalf("asset %s not found", name)
	return nil
}

func TestExport_CustomTypes_Partial(t *testing.T) {
	ctx := setupBackupTest(t)
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("grafana")))
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("unused")))

	a := createGenericAsset(t, "g1", "grafana", map[string]asset_entity.GenericValue{"host": {Value: "h1"}})
	require.NoError(t, asset_repo.Asset().Create(ctx, a))

	data, err := Export(ctx, &ExportOptions{AssetIDs: []int64{a.ID}}, nil)
	require.NoError(t, err)
	require.Len(t, data.CustomTypes, 1)
	assert.Equal(t, "grafana", data.CustomTypes[0].Slug)
}

func TestExport_CustomTypes_Full(t *testing.T) {
	ctx := setupBackupTest(t)
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("grafana")))
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("unused")))

	data, err := Export(ctx, &ExportOptions{}, nil)
	require.NoError(t, err)
	require.Len(t, data.CustomTypes, 2)
	assert.Equal(t, 2, data.Summary().CustomTypeCount)
}

func TestExport_CustomTypes_WithoutCredentials_StripsSecretFieldKeepsPlainField(t *testing.T) {
	ctx := setupBackupTest(t)
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("grafana")))

	a := createGenericAsset(t, "g1", "grafana", map[string]asset_entity.GenericValue{
		"host":  {Value: "h1"},
		"token": {Value: "ciphertext-of-secret"},
	})
	require.NoError(t, asset_repo.Asset().Create(ctx, a))

	data, err := Export(ctx, &ExportOptions{}, nil)
	require.NoError(t, err)
	cfg := findGenericConfig(t, data.Assets, "g1")
	assert.Equal(t, "h1", cfg.Values["host"].Value, "non-secret field must survive export without credentials")
	tokenVal := cfg.Values["token"]
	assert.Empty(t, tokenVal.Value, "secret field value must be stripped when credentials are not exported")
	assert.Zero(t, tokenVal.CredentialID)
}

func TestExport_CustomTypes_WithCredentials_DecryptsSecretFieldToPlain(t *testing.T) {
	ctx := setupBackupTest(t)
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("grafana")))

	crypto := taggedCredentialCrypto{tag: "src:"}
	a := createGenericAsset(t, "g1", "grafana", map[string]asset_entity.GenericValue{
		"host":  {Value: "h1"},
		"token": {Value: "src:my-secret-token"},
	})
	require.NoError(t, asset_repo.Asset().Create(ctx, a))

	data, err := Export(ctx, &ExportOptions{IncludeCredentials: true}, crypto)
	require.NoError(t, err)
	cfg := findGenericConfig(t, data.Assets, "g1")
	assert.Equal(t, "my-secret-token", cfg.Values["token"].Value, "secret field must be decrypted to plaintext for backup")
	assert.Equal(t, "h1", cfg.Values["host"].Value)
}

func TestImport_CustomTypes_Replace(t *testing.T) {
	ctx := setupBackupTest(t)
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("a")))
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("b")))

	backupType := grafanaCustomType("a")
	backupType.Name = "Grafana From Backup"
	data := &BackupData{CustomTypes: []*custom_type_entity.CustomType{backupType}}

	result, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "replace"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, result.CustomTypesImported)

	types, err := custom_type_repo.CustomType().List(ctx)
	require.NoError(t, err)
	require.Len(t, types, 1, "replace mode must remove local types not present in the backup")
	assert.Equal(t, "a", types[0].Slug)
	assert.Equal(t, "Grafana From Backup", types[0].Name)
}

func TestImport_CustomTypes_Merge_KeepsLocalOnSlugConflict(t *testing.T) {
	ctx := setupBackupTest(t)
	local := grafanaCustomType("grafana")
	local.Name = "Local Grafana"
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, local))

	backupType := grafanaCustomType("grafana")
	backupType.Name = "Backup Grafana"
	newType := grafanaCustomType("newtype")
	asset := createGenericAsset(t, "g1", "grafana", map[string]asset_entity.GenericValue{"host": {Value: "h1"}})
	data := &BackupData{
		CustomTypes: []*custom_type_entity.CustomType{backupType, newType},
		Assets:      []*asset_entity.Asset{asset},
	}

	result, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, result.CustomTypesImported, "conflicting slug is skipped, only the new type is imported")

	types, err := custom_type_repo.CustomType().List(ctx)
	require.NoError(t, err)
	require.Len(t, types, 2)
	var gotLocal, gotNew *custom_type_entity.CustomType
	for _, ct := range types {
		switch ct.Slug {
		case "grafana":
			gotLocal = ct
		case "newtype":
			gotNew = ct
		}
	}
	require.NotNil(t, gotLocal)
	require.NotNil(t, gotNew)
	assert.Equal(t, "Local Grafana", gotLocal.Name, "merge mode keeps the local type on slug conflict")

	assets, err := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	cfg, err := assets[0].GetGenericConfig()
	require.NoError(t, err)
	assert.Equal(t, "grafana", cfg.CustomType, "the imported asset binds to the local type by slug")
}

// 合并模式下备份资产绑定到本地版本：值按本地版本的字段结构存储——本地是密钥的字段加密、
// 本地是普通字段的保持明文、本地没有的字段随之删除（与修改类型结构时的规则一致）。
func TestImport_CustomTypes_Merge_ValuesStoredPerLocalVersion(t *testing.T) {
	ctx := setupBackupTest(t)
	local := grafanaCustomType("grafana")
	local.Fields = []custom_type_entity.Field{
		{Name: "host", Required: true},
		{Name: "token", Secret: true, Required: true},
		{Name: "org"},
	}
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, local))

	backupType := grafanaCustomType("grafana")
	backupType.Fields = []custom_type_entity.Field{
		{Name: "host", Secret: true},
		{Name: "token"},
		{Name: "org"},
		{Name: "legacy"},
	}
	asset := createGenericAsset(t, "g1", "grafana", map[string]asset_entity.GenericValue{
		"host":   {Value: "grafana.internal"},
		"token":  {Value: "tok"},
		"org":    {Value: "main"},
		"legacy": {Value: "old"},
	})
	data := &BackupData{
		IncludesCredentials: true,
		CustomTypes:         []*custom_type_entity.CustomType{backupType},
		Assets:              []*asset_entity.Asset{asset},
	}

	_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, taggedCredentialCrypto{tag: "dst:"})
	require.NoError(t, err)

	assets, err := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	cfg, err := assets[0].GetGenericConfig()
	require.NoError(t, err)
	assert.Equal(t, map[string]asset_entity.GenericValue{
		"host":  {Value: "grafana.internal"},
		"token": {Value: "dst:tok"},
		"org":   {Value: "main"},
	}, cfg.Values)
}

func TestImport_CustomTypes_NotImportedWhenImportAssetsFalse(t *testing.T) {
	ctx := setupBackupTest(t)
	data := &BackupData{CustomTypes: []*custom_type_entity.CustomType{grafanaCustomType("grafana")}}

	result, err := Import(ctx, data, &ImportOptions{ImportAssets: false, Mode: "merge"}, nil)
	require.NoError(t, err)
	assert.Zero(t, result.CustomTypesImported)

	types, err := custom_type_repo.CustomType().List(ctx)
	require.NoError(t, err)
	assert.Empty(t, types)
}

func TestImport_CustomTypes_CredentialRoundTrip(t *testing.T) {
	setupCredentialBackupTest(t)
	ctx := t.Context()
	require.NoError(t, custom_type_repo.CustomType().Create(ctx, grafanaCustomType("grafana")))

	cred := &credential_entity.Credential{Name: "grafana-token", Type: credential_entity.TypePassword}
	require.NoError(t, credential_repo.Credential().Create(ctx, cred))
	a := createGenericAsset(t, "g1", "grafana", map[string]asset_entity.GenericValue{
		"host":  {Value: "h1"},
		"token": {CredentialID: cred.ID},
	})
	require.NoError(t, asset_repo.Asset().Create(ctx, a))

	data, err := Export(ctx, &ExportOptions{IncludeCredentials: true}, taggedCredentialCrypto{tag: "src:"})
	require.NoError(t, err)
	serialized, err := json.Marshal(data)
	require.NoError(t, err)
	var transported BackupData
	require.NoError(t, json.Unmarshal(serialized, &transported))

	setupCredentialBackupTest(t)
	ctx = t.Context()
	result, err := Import(ctx, &transported, &ImportOptions{
		ImportAssets: true, ImportCredentials: true, Mode: "merge",
	}, taggedCredentialCrypto{tag: "dst:"})
	require.NoError(t, err)
	assert.Equal(t, 1, result.CredentialsImported)
	assert.Equal(t, 1, result.CustomTypesImported)

	restoredCreds, err := credential_repo.Credential().List(ctx)
	require.NoError(t, err)
	require.Len(t, restoredCreds, 1)

	assets, err := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	cfg, err := assets[0].GetGenericConfig()
	require.NoError(t, err)
	assert.Equal(t, restoredCreds[0].ID, cfg.Values["token"].CredentialID, "credential id must be remapped like SSH's cfg.CredentialID")
	assert.Empty(t, cfg.Values["token"].Value)
}
