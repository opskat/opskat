package system

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

func TestTestAssetConnectionReturnsDetail(t *testing.T) {
	defer conntest.Unregister("detailed")
	conntest.RegisterDetailed("detailed", func(context.Context, string, string) (string, error) {
		return "HTTP 200 OK（直连）", nil
	})
	detail, err := newTestSystem().TestAssetConnection("tid", "detailed", "{}", "")
	require.NoError(t, err)
	assert.Equal(t, "HTTP 200 OK（直连）", detail, "the form shows the tester's detail (status line / route)")
}

var errFakeDial = errors.New("fake ssh dial refused")

type fakePoolDialer struct{}

func (fakePoolDialer) DialAsset(context.Context, int64) (*ssh.Client, []io.Closer, error) {
	return nil, nil, errFakeDial
}

// TestTestAssetConnectionInjectsSSHPool 走真实的通用资产测试连接：表单选了 SSH 隧道时，
// 桌面端必须把 SSH 连接池交给 tester，否则拨号根本不会经过隧道（报"连接池不可用"）。
func TestTestAssetConnectionInjectsSSHPool(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&custom_type_entity.CustomType{}, &asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.SetDefault(credential_svc.New("test-master-key", []byte("0123456789abcdef")))
	custom_type_svc.CustomType().SetReservedNames(func() []string { return []string{"ssh"} })
	require.NoError(t, custom_type_svc.CustomType().Save(context.Background(), &custom_type_entity.CustomType{
		Name: "Grafana", Slug: "grafana", ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{{Name: "host", Required: true}},
		HTTP:   &custom_type_entity.HTTPConfig{BaseURL: "http://{{host}}"},
	}))
	defer conntest.Unregister(asset_entity.AssetTypeGeneric)
	conntest.RegisterDetailed(asset_entity.AssetTypeGeneric, helper.ProbeGenericConnection)

	cfg, err := json.Marshal(helper.GenericConnTestInput{
		CustomType:  "grafana",
		Values:      map[string]any{"host": "grafana.internal:3000"},
		SSHTunnelID: 42,
	})
	require.NoError(t, err)

	s := newTestSystem()
	s.SetSSHPool(sshpool.NewPool(fakePoolDialer{}, time.Minute))
	_, err = s.TestAssetConnection("tid", asset_entity.AssetTypeGeneric, string(cfg), "")
	require.Error(t, err)
	assert.ErrorContains(t, err, errFakeDial.Error(), "the probe must dial through the injected SSH pool")
}
