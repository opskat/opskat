package custom_type_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// setup 用内存 SQLite 跑真实仓储：删除字段清理资产值必须和类型更新处于同一事务，
// mock 无法验证这一点。
func setup(t *testing.T) (context.Context, CustomTypeSvc) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&custom_type_entity.CustomType{}, &asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.SetDefault(credential_svc.New("test-master-key", []byte("0123456789abcdef")))

	svc := New()
	svc.SetReservedNames(func() []string { return []string{"ssh", "redis", "ext-es"} })
	return context.Background(), svc
}

func grafanaType() *custom_type_entity.CustomType {
	return &custom_type_entity.CustomType{
		Name:     "Grafana",
		Slug:     "grafana",
		ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{
			{Name: "host", Required: true},
			{Name: "org", Default: "main"},
			{Name: "token", Secret: true, Required: true},
		},
		HTTP: &custom_type_entity.HTTPConfig{
			BaseURL: "https://{{host}}",
			Auth: []custom_type_entity.AuthBinding{
				{Type: "header", Name: "Authorization", Values: []string{`{{"Bearer " + token}}`}},
			},
		},
	}
}

func encrypt(t *testing.T, plain string) string {
	t.Helper()
	c, err := credential_svc.Default().Encrypt(plain)
	require.NoError(t, err)
	return c
}

func createGeneric(t *testing.T, ctx context.Context, name, slug string, values map[string]asset_entity.GenericValue) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeGeneric, Status: asset_entity.StatusActive}
	require.NoError(t, a.SetGenericConfig(&asset_entity.GenericConfig{CustomType: slug, Values: values}))
	require.NoError(t, asset_repo.Asset().Create(ctx, a))
	return a
}

func reload(t *testing.T, ctx context.Context, id int64) *asset_entity.GenericConfig {
	t.Helper()
	a, err := asset_repo.Asset().Find(ctx, id)
	require.NoError(t, err)
	cfg, err := a.GetGenericConfig()
	require.NoError(t, err)
	return cfg
}

func issuePaths(t *testing.T, err error) []string {
	t.Helper()
	var verr *custom_type_entity.ValidationError
	require.True(t, errors.As(err, &verr), "expected *ValidationError, got %v", err)
	paths := make([]string, 0, len(verr.Issues))
	for _, is := range verr.Issues {
		paths = append(paths, is.Path)
	}
	return paths
}

func TestSave_CreatePrefillsDefaultPolicyByExecMode(t *testing.T) {
	ctx, svc := setup(t)

	h := grafanaType()
	require.NoError(t, svc.Save(ctx, h))
	require.NotZero(t, h.ID)
	got, err := svc.Get(ctx, h.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"GET *", "HEAD *", "OPTIONS *"}, got.DefaultPolicy.AllowList)
	assert.NotZero(t, got.Createtime)

	c := &custom_type_entity.CustomType{
		Name: "AWS", Slug: "aws", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "key", Secret: true}},
		Command: &custom_type_entity.CommandConfig{Template: "aws"},
	}
	require.NoError(t, svc.Save(ctx, c))
	got, err = svc.GetBySlug(ctx, "aws")
	require.NoError(t, err)
	require.NotNil(t, got.DefaultPolicy)
	assert.True(t, got.DefaultPolicy.IsEmpty(), "命令方式没有默认放行")

	// 用户显式给出的策略（包括清空）不被预填覆盖。
	explicit := grafanaType()
	explicit.Slug = "grafana-2"
	explicit.DefaultPolicy = &policy.CommandPolicy{}
	require.NoError(t, svc.Save(ctx, explicit))
	got, err = svc.GetBySlug(ctx, "grafana-2")
	require.NoError(t, err)
	assert.True(t, got.DefaultPolicy.IsEmpty())
}

func TestSave_DropsConfigOfOtherExecMode(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	ct.Command = &custom_type_entity.CommandConfig{Template: "curl"}
	require.NoError(t, svc.Save(ctx, ct))
	got, err := svc.Get(ctx, ct.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Command)
	assert.NotNil(t, got.HTTP)
}

func TestSave_RejectsInvalidType(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	ct.Fields[1].Name = "host"
	assert.Contains(t, issuePaths(t, svc.Save(ctx, ct)), "fields[1].name")
	list, err := svc.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestSave_SlugConflicts(t *testing.T) {
	ctx, svc := setup(t)
	require.NoError(t, svc.Save(ctx, grafanaType()))

	for _, slug := range []string{"ssh", "ext-es", asset_entity.AssetTypeGeneric, "grafana"} {
		t.Run(slug, func(t *testing.T) {
			ct := grafanaType()
			ct.Slug = slug
			err := svc.Save(ctx, ct)
			assert.Contains(t, issuePaths(t, err), "slug")
		})
	}

	// 冲突提示要指出冲突对象（界面按 Code 翻译、用 Params 填文案）：内置 / 扩展类型
	// 给出标识，自定义类型再给出其名称。
	reserved := grafanaType()
	reserved.Slug = "ext-es"
	assert.Contains(t, validationIssues(t, svc.Save(ctx, reserved)),
		custom_type_entity.Issue{Path: "slug", Code: "slug_reserved", Params: map[string]string{"slug": "ext-es"}})

	dup := grafanaType()
	dup.Name = "Another"
	assert.Contains(t, validationIssues(t, svc.Save(ctx, dup)),
		custom_type_entity.Issue{Path: "slug", Code: "slug_taken", Params: map[string]string{"slug": "grafana", "name": "Grafana"}})
}

func validationIssues(t *testing.T, err error) []custom_type_entity.Issue {
	t.Helper()
	var verr *custom_type_entity.ValidationError
	require.ErrorAs(t, err, &verr)
	return verr.Issues
}

func TestSave_RequiresReservedNames(t *testing.T) {
	ctx, _ := setup(t)
	svc := New()
	err := svc.Save(ctx, grafanaType())
	require.Error(t, err, "未注入保留名时不能保存，否则可能与内置类型重名")
}

func TestSave_SlugIsImmutable(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))

	renamed := grafanaType()
	renamed.ID = ct.ID
	renamed.Slug = "grafana-new"
	assert.Contains(t, validationIssues(t, svc.Save(ctx, renamed)),
		custom_type_entity.Issue{Path: "slug", Code: "slug_immutable", Params: map[string]string{"original": "grafana"}})

	// 更新自己不算与自己重名；未编辑默认策略时保留原策略。
	same := grafanaType()
	same.ID = ct.ID
	same.Name = "Grafana 2"
	require.NoError(t, svc.Save(ctx, same))
	got, err := svc.Get(ctx, ct.ID)
	require.NoError(t, err)
	assert.Equal(t, "Grafana 2", got.Name)
	assert.Equal(t, ct.Createtime, got.Createtime)
	assert.Equal(t, []string{"GET *", "HEAD *", "OPTIONS *"}, got.DefaultPolicy.AllowList)
}

func TestSave_UpdateMissingType(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	ct.ID = 99
	err := svc.Save(ctx, ct)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestSave_RemovingFieldDeletesValuesOnAllAssets(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))
	a := createGeneric(t, ctx, "a", "grafana", map[string]asset_entity.GenericValue{
		"host": {Value: "a.local"}, "org": {Value: "ops"}, "token": {Value: encrypt(t, "ta")},
	})
	b := createGeneric(t, ctx, "b", "grafana", map[string]asset_entity.GenericValue{
		"host": {Value: "b.local"}, "org": {Value: "dev"},
	})
	other := createGeneric(t, ctx, "other", "aws", map[string]asset_entity.GenericValue{"org": {Value: "keep"}})

	update := grafanaType()
	update.ID = ct.ID
	update.Fields = []custom_type_entity.Field{update.Fields[0], update.Fields[2]} // 删掉 org
	require.NoError(t, svc.Save(ctx, update))

	for _, id := range []int64{a.ID, b.ID} {
		cfg := reload(t, ctx, id)
		assert.NotContains(t, cfg.Values, "org")
		assert.Contains(t, cfg.Values, "host")
	}
	assert.Contains(t, reload(t, ctx, other.ID).Values, "org", "其他类型的资产不受影响")
}

func TestSave_FailedAssetRewriteRollsBackType(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))
	cipher := encrypt(t, "ta")
	good := createGeneric(t, ctx, "good", "grafana", map[string]asset_entity.GenericValue{"token": {Value: cipher}})
	// 密钥改为普通字段时要解密已有值；这台资产的密文损坏，改写失败。
	createGeneric(t, ctx, "broken", "grafana", map[string]asset_entity.GenericValue{"token": {Value: "not-cipher"}})

	update := grafanaType()
	update.ID = ct.ID
	update.Name = "should not persist"
	update.Fields[2].Secret = false
	require.Error(t, svc.Save(ctx, update))
	got, err := svc.Get(ctx, ct.ID)
	require.NoError(t, err)
	assert.Equal(t, "Grafana", got.Name, "资产改写失败时类型更新整体回滚")
	assert.True(t, got.Fields[2].Secret)
	assert.Equal(t, cipher, reload(t, ctx, good.ID).Values["token"].Value, "已改写的资产同样回滚")
}

func TestSave_SecretFlipKeepsValue(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))
	a := createGeneric(t, ctx, "a", "grafana", map[string]asset_entity.GenericValue{
		"host": {Value: "a.local"}, "org": {Value: "ops"}, "token": {Value: encrypt(t, "secret-token")},
	})

	update := grafanaType()
	update.ID = ct.ID
	update.Fields[1].Secret, update.Fields[1].Default = true, "" // org → 密钥
	update.Fields[2].Secret = false                              // token → 普通
	require.NoError(t, svc.Save(ctx, update))

	cfg := reload(t, ctx, a.ID)
	assert.NotEqual(t, "ops", cfg.Values["org"].Value, "改为密钥后按密文存储")
	assert.Equal(t, "secret-token", cfg.Values["token"].Value, "改为普通后按明文存储")

	resolved, err := svc.ResolveAsset(ctx, mustFind(t, ctx, a.ID))
	require.NoError(t, err)
	assert.Equal(t, "ops", resolved.Values["org"])
	assert.Equal(t, "secret-token", resolved.Values["token"])
}

func TestSave_SecretToPlainInlinesManagedCredential(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))
	cred := &credential_entity.Credential{Name: "tok", Type: credential_entity.TypePassword, Password: encrypt(t, "managed")}
	require.NoError(t, credential_repo.Credential().Create(ctx, cred))
	a := createGeneric(t, ctx, "a", "grafana", map[string]asset_entity.GenericValue{
		"host": {Value: "a.local"}, "token": {CredentialID: cred.ID},
	})

	update := grafanaType()
	update.ID = ct.ID
	update.Fields[2].Secret = false
	require.NoError(t, svc.Save(ctx, update))

	cfg := reload(t, ctx, a.ID)
	assert.Equal(t, asset_entity.GenericValue{Value: "managed"}, cfg.Values["token"])
}

func mustFind(t *testing.T, ctx context.Context, id int64) *asset_entity.Asset {
	t.Helper()
	a, err := asset_repo.Asset().Find(ctx, id)
	require.NoError(t, err)
	return a
}

func TestResolveAsset(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))
	cred := &credential_entity.Credential{Name: "tok", Type: credential_entity.TypePassword, Password: encrypt(t, "managed-token")}
	require.NoError(t, credential_repo.Credential().Create(ctx, cred))

	inline := createGeneric(t, ctx, "inline", "grafana", map[string]asset_entity.GenericValue{
		"host": {Value: "a.local"}, "token": {Value: encrypt(t, "inline-token")},
	})
	resolved, err := svc.ResolveAsset(ctx, inline)
	require.NoError(t, err)
	assert.Equal(t, ct.ID, resolved.Type.ID)
	assert.Equal(t, map[string]string{"host": "a.local", "org": "main", "token": "inline-token"}, resolved.Values,
		"缺省的字段取默认值，密钥解密为明文")
	assert.Empty(t, resolved.Missing)

	managed := createGeneric(t, ctx, "managed", "grafana", map[string]asset_entity.GenericValue{
		"host": {Value: "b.local"}, "org": {Value: ""}, "token": {CredentialID: cred.ID},
	})
	resolved, err = svc.ResolveAsset(ctx, managed)
	require.NoError(t, err)
	assert.Equal(t, "managed-token", resolved.Values["token"])
	assert.Equal(t, "", resolved.Values["org"], "显式填写的空值不被默认值覆盖")
}

func TestResolveAsset_NewRequiredFieldIsReportedMissing(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))
	a := createGeneric(t, ctx, "a", "grafana", map[string]asset_entity.GenericValue{"host": {Value: "a.local"}})

	update := grafanaType()
	update.ID = ct.ID
	update.Fields = append(update.Fields, custom_type_entity.Field{Name: "region", Required: true})
	require.NoError(t, svc.Save(ctx, update), "新增必填字段不阻止保存类型")

	resolved, err := svc.ResolveAsset(ctx, mustFind(t, ctx, a.ID))
	require.NoError(t, err)
	assert.Equal(t, []string{"token", "region"}, resolved.Missing, "按字段顺序列出缺值的必填字段")
	assert.Equal(t, "", resolved.Values["region"], "每个字段都有值，模板渲染上下文完整")
}

func TestResolveAsset_Errors(t *testing.T) {
	ctx, svc := setup(t)
	_, err := svc.ResolveAsset(ctx, &asset_entity.Asset{Type: asset_entity.AssetTypeSSH, Config: "{}"})
	assert.Error(t, err)

	orphan := createGeneric(t, ctx, "orphan", "missing", nil)
	_, err = svc.ResolveAsset(ctx, orphan)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.NoError(t, svc.Save(ctx, grafanaType()))
	bad := createGeneric(t, ctx, "bad", "grafana", map[string]asset_entity.GenericValue{"token": {Value: "not-cipher"}})
	_, err = svc.ResolveAsset(ctx, bad)
	require.Error(t, err, "解密失败必须报错，不能当作空值继续")
	assert.Contains(t, err.Error(), "token")
	assert.NotContains(t, err.Error(), "not-cipher")
}

func TestDeleteAndUsage(t *testing.T) {
	ctx, svc := setup(t)
	ct := grafanaType()
	require.NoError(t, svc.Save(ctx, ct))
	a := createGeneric(t, ctx, "prod-grafana", "grafana", nil)
	createGeneric(t, ctx, "dev-grafana", "grafana", nil)

	n, err := svc.UsageCount(ctx, ct.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	err = svc.Delete(ctx, ct.ID)
	var inUse *InUseError
	require.True(t, errors.As(err, &inUse), "got %v", err)
	assert.Equal(t, []string{"prod-grafana", "dev-grafana"}, inUse.Assets)
	_, err = svc.Get(ctx, ct.ID)
	require.NoError(t, err, "拒绝删除时类型仍在")

	require.NoError(t, asset_repo.Asset().Delete(ctx, a.ID))
	list, err := asset_repo.Asset().ListByCustomType(ctx, "grafana")
	require.NoError(t, err)
	for _, x := range list {
		require.NoError(t, asset_repo.Asset().Delete(ctx, x.ID))
	}
	require.NoError(t, svc.Delete(ctx, ct.ID))
	_, err = svc.Get(ctx, ct.ID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
