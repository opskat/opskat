package command

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestResolveAssetMarkdownRef(t *testing.T) {
	Convey("resolveAsset accepts copied markdown / URI refs", t, func() {
		ctrl := gomock.NewController(t)
		Reset(ctrl.Finish)

		mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
		want := &asset_entity.Asset{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(want, nil).AnyTimes()

		orig := asset_repo.Asset()
		asset_repo.RegisterAsset(mockAsset)
		Reset(func() {
			if orig != nil {
				asset_repo.RegisterAsset(orig)
			}
		})

		Convey("opsctl://asset/1", func() {
			got, err := resolveAsset(context.Background(), "opsctl://asset/1")
			So(err, ShouldBeNil)
			So(got.ID, ShouldEqual, 1)
		})

		Convey("markdown [name](opsctl://asset/1)", func() {
			got, err := resolveAsset(context.Background(), "[web-01](opsctl://asset/1)")
			So(err, ShouldBeNil)
			So(got.ID, ShouldEqual, 1)
		})

		Convey("quoted markdown ref", func() {
			got, err := resolveAsset(context.Background(), `"[web-01](opsctl://asset/1)"`)
			So(err, ShouldBeNil)
			So(got.ID, ShouldEqual, 1)
		})

		Convey("escaped brackets in the display name", func() {
			got, err := resolveAsset(context.Background(), `[prod \[web\]](opsctl://asset/1)`)
			So(err, ShouldBeNil)
			So(got.ID, ShouldEqual, 1)
		})
	})
}
