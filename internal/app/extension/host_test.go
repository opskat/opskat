package extension

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

type fixedLang string

func (l fixedLang) Lang() string { return string(l) }

func registeredExt(name, assetType string) *extension.Extension {
	return &extension.Extension{
		Name: name,
		Manifest: &extension.Manifest{
			Name:       name,
			AssetTypes: []extension.AssetTypeDef{{Type: assetType}},
		},
	}
}

// newHostTestBinder wires a binder over a service whose asset repo is mocked
// and whose bridge has two extensions loaded: "acme" (type acme-store) and
// "other" (type other-store).
func newHostTestBinder(t *testing.T) (*Extension, *mock_asset_repo.MockAssetRepo) {
	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	svc := extension_svc.New(nil, nil, nil, assets, zap.NewNop(), nil, nil)
	svc.Bridge().Register(registeredExt("acme", "acme-store"))
	svc.Bridge().Register(registeredExt("other", "other-store"))
	e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
	e.SetService(svc)
	return e, assets
}

func TestFrontendCallsScopeAssetToExtension(t *testing.T) {
	Convey("frontend-initiated calls may only name the extension's own assets", t, func() {
		e, assets := newHostTestBinder(t)

		Convey("assetRef refuses an asset of another type", func() {
			assets.EXPECT().Find(gomock.Any(), int64(2)).
				Return(&asset_entity.Asset{ID: 2, Name: "prod", Type: "ssh"}, nil)
			ref, err := e.assetRef("acme", 2)
			So(err, ShouldNotBeNil)
			So(ref, ShouldBeNil)
		})

		Convey("assetRef refuses a database asset of a driver the extension provides", func() {
			svc := e.service
			svc.Bridge().Register(&extension.Extension{Name: "mydb", Manifest: &extension.Manifest{
				Name: "mydb", AssetTypes: []extension.AssetTypeDef{{Type: "mydb-console"}},
				Capabilities: extension.Capabilities{DatabaseDrivers: []string{"mydb"}},
			}})
			assets.EXPECT().Find(gomock.Any(), int64(3)).Return(&asset_entity.Asset{
				ID: 3, Name: "orders", Type: asset_entity.AssetTypeDatabase, Config: `{"driver":"mydb","host":"h","port":1}`,
			}, nil)
			// 驱动只经会话碰到它的资产；扩展页面发起的调用不能以它为作用域。
			ref, err := e.assetRef("mydb", 3)
			So(err, ShouldNotBeNil)
			So(ref, ShouldBeNil)
		})

		Convey("assetRef names an owned asset", func() {
			assets.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&asset_entity.Asset{ID: 1, Name: "store", Type: "acme-store"}, nil)
			ref, err := e.assetRef("acme", 1)
			So(err, ShouldBeNil)
			So(ref, ShouldResemble, &extension.AssetRef{ID: 1, Name: "store", Type: "acme-store"})
		})
	})
}
