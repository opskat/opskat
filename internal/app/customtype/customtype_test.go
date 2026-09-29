package customtype

import (
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// fakeLang 是最小的 LangProvider 实现，测试里固定用中文。
type fakeLang struct{}

func (fakeLang) Lang() string { return "zh-CN" }

// setup 用内存 SQLite 跑真实仓储 + 真实 custom_type_svc 单例（与 custom_type_svc 包自身
// 的测试同一策略）：绑定层的职责就是把领域层结果转成前端能直接渲染的结构，必须经过
// 真实校验/占用逻辑才能验证转换是否保真。
func setup(t *testing.T) *CustomType {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&custom_type_entity.CustomType{}, &asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.SetDefault(credential_svc.New("test-master-key", []byte("0123456789abcdef")))
	custom_type_svc.CustomType().SetReservedNames(func() []string { return []string{"ssh", "redis"} })

	b := New(fakeLang{})
	b.Startup(t.Context())
	return b
}

func grafanaType() *custom_type_entity.CustomType {
	return &custom_type_entity.CustomType{
		Name:     "Grafana",
		Slug:     "grafana",
		ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{
			{Name: "host", Required: true},
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

func TestSaveCustomType_ValidationIssuesInsteadOfError(t *testing.T) {
	b := setup(t)

	ct := &custom_type_entity.CustomType{Slug: "bad slug", ExecMode: custom_type_entity.ExecModeHTTP}
	res, err := b.SaveCustomType(ct)
	require.NoError(t, err, "validation failures must surface as Issues, not a bare error")
	require.NotNil(t, res)
	assert.NotEmpty(t, res.Issues)
	assert.Nil(t, res.Type)
}

func TestSaveCustomType_SuccessReturnsSavedTypeAndID(t *testing.T) {
	b := setup(t)

	res, err := b.SaveCustomType(grafanaType())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Empty(t, res.Issues)
	require.NotNil(t, res.Type)
	assert.NotZero(t, res.Type.ID)
	assert.Equal(t, "grafana", res.Type.Slug)
}

func TestSaveCustomType_ReservedNameConflict(t *testing.T) {
	b := setup(t)

	ct := grafanaType()
	ct.Slug = "ssh"
	res, err := b.SaveCustomType(ct)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.Issues)
	assert.Equal(t, "slug", res.Issues[0].Path)
}

func TestListCustomTypes_ReportsExecModeAndAssetCount(t *testing.T) {
	b := setup(t)

	saveRes, err := b.SaveCustomType(grafanaType())
	require.NoError(t, err)
	require.NotNil(t, saveRes.Type)

	a := &asset_entity.Asset{Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric, Status: asset_entity.StatusActive}
	require.NoError(t, a.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "grafana", Values: map[string]asset_entity.GenericValue{
		"host": {Value: "grafana.example.com"},
	}}))
	require.NoError(t, asset_repo.Asset().Create(t.Context(), a))

	list, err := b.ListCustomTypes()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "grafana", list[0].Slug)
	assert.Equal(t, custom_type_entity.ExecModeHTTP, list[0].ExecMode)
	assert.Equal(t, 1, list[0].AssetCount)
}

func TestGetCustomTypeUsage(t *testing.T) {
	b := setup(t)

	saveRes, err := b.SaveCustomType(grafanaType())
	require.NoError(t, err)

	names, err := b.GetCustomTypeUsage(saveRes.Type.ID)
	require.NoError(t, err)
	assert.Empty(t, names)

	a := &asset_entity.Asset{Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric, Status: asset_entity.StatusActive}
	require.NoError(t, a.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "grafana"}))
	require.NoError(t, asset_repo.Asset().Create(t.Context(), a))

	names, err = b.GetCustomTypeUsage(saveRes.Type.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"grafana-prod"}, names)
}

func TestDeleteCustomType_InUseListsAssetNames(t *testing.T) {
	b := setup(t)

	saveRes, err := b.SaveCustomType(grafanaType())
	require.NoError(t, err)

	a := &asset_entity.Asset{Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric, Status: asset_entity.StatusActive}
	require.NoError(t, a.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "grafana"}))
	require.NoError(t, asset_repo.Asset().Create(t.Context(), a))

	res, err := b.DeleteCustomType(saveRes.Type.ID)
	require.NoError(t, err, "in-use deletion must surface as a structured refusal, not a bare error")
	require.NotNil(t, res)
	assert.False(t, res.Deleted)
	assert.Equal(t, []string{"grafana-prod"}, res.Assets)
}

func TestDeleteCustomType_Success(t *testing.T) {
	b := setup(t)

	saveRes, err := b.SaveCustomType(grafanaType())
	require.NoError(t, err)

	res, err := b.DeleteCustomType(saveRes.Type.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Deleted)
	assert.Empty(t, res.Assets)

	_, err = b.GetCustomType(saveRes.Type.ID)
	assert.Error(t, err)
}
