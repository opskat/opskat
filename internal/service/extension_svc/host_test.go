package extension_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/extension_data_repo/mock_extension_data_repo"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"
)

func TestServiceHostAccess(t *testing.T) {
	Convey("Extension host access delegates persistence through the service", t, func() {
		ctrl := gomock.NewController(t)
		ctx := context.Background()
		assets := mock_asset_repo.NewMockAssetRepo(ctrl)
		data := mock_extension_data_repo.NewMockExtensionDataRepo(ctrl)
		svc := &Service{assetRepo: assets, dataRepo: data}

		Convey("asset config is returned through a narrow value", func() {
			assets.EXPECT().Find(ctx, int64(42)).Return(&asset_entity.Asset{Type: "demo", Config: `{"host":"example"}`}, nil)

			cfg, err := svc.GetHostAssetConfig(ctx, 42)

			So(err, ShouldBeNil)
			So(cfg.Type, ShouldEqual, "demo")
			So(cfg.Config, ShouldEqual, `{"host":"example"}`)
		})

		Convey("a missing KV key is the only read error recovered as empty", func() {
			data.EXPECT().Get(ctx, "demo", "missing").Return(nil, gorm.ErrRecordNotFound)

			value, err := svc.GetHostKV(ctx, "demo", "missing")

			So(err, ShouldBeNil)
			So(value, ShouldBeNil)
		})

		Convey("repository failures are surfaced to the extension", func() {
			repoErr := errors.New("database unavailable")
			data.EXPECT().Get(ctx, "demo", "key").Return(nil, repoErr)

			value, err := svc.GetHostKV(ctx, "demo", "key")

			So(value, ShouldBeNil)
			So(errors.Is(err, repoErr), ShouldBeTrue)
		})

		Convey("KV writes delegate through the service", func() {
			data.EXPECT().Set(ctx, "demo", "key", []byte("value")).Return(nil)

			So(svc.SetHostKV(ctx, "demo", "key", []byte("value")), ShouldBeNil)
		})
	})
}
