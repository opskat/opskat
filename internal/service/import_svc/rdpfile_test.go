package import_svc

import (
	"context"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

const sampleRDP = `screen mode id:i:2
use multimon:i:0
desktopwidth:i:1920
desktopheight:i:1080
session bpp:i:32
full address:s:192.168.1.50
domain:s:CORP
username:s:CORP\administrator
server port:i:3389
redirectclipboard:i:1
`

func TestParseRDPFile(t *testing.T) {
	Convey("parseRDPFile .rdp 解析", t, func() {
		Convey("完整字段", func() {
			entry, err := parseRDPFile([]byte(sampleRDP))
			So(err, ShouldBeNil)
			So(entry.Host, ShouldEqual, "192.168.1.50")
			So(entry.Port, ShouldEqual, 3389)
			So(entry.Username, ShouldEqual, "administrator")
			So(entry.Domain, ShouldEqual, "CORP")
			So(entry.Width, ShouldEqual, 1920)
			So(entry.Height, ShouldEqual, 1080)
			So(entry.Clipboard, ShouldBeTrue)
		})

		Convey("full address 携带端口，username 为 DOMAIN\\user", func() {
			entry, err := parseRDPFile([]byte(
				"full address:s:example.com:3390\r\nusername:s:MYDOMAIN\\ops\r\n"))
			So(err, ShouldBeNil)
			So(entry.Host, ShouldEqual, "example.com")
			So(entry.Port, ShouldEqual, 3390)
			So(entry.Domain, ShouldEqual, "MYDOMAIN")
			So(entry.Username, ShouldEqual, "ops")
		})

		Convey("IPv6 裸地址不误拆端口", func() {
			entry, err := parseRDPFile([]byte("full address:s:fe80::1\nusername:s:admin\n"))
			So(err, ShouldBeNil)
			So(entry.Host, ShouldEqual, "fe80::1")
			So(entry.Port, ShouldEqual, 0)
		})

		Convey("IPv6 带方括号端口可拆分", func() {
			entry, err := parseRDPFile([]byte("full address:s:[fe80::1]:3390\n"))
			So(err, ShouldBeNil)
			So(entry.Host, ShouldEqual, "fe80::1")
			So(entry.Port, ShouldEqual, 3390)
		})

		Convey("UPN 用户名保持原样", func() {
			entry, err := parseRDPFile([]byte("full address:s:1.2.3.4\nusername:s:ops@example.com\n"))
			So(err, ShouldBeNil)
			So(entry.Username, ShouldEqual, "ops@example.com")
			So(entry.Domain, ShouldEqual, "")
		})

		Convey("redirectclipboard 显式为 0 时关闭，缺省开启", func() {
			entry, err := parseRDPFile([]byte("full address:s:1.2.3.4\nredirectclipboard:i:0\n"))
			So(err, ShouldBeNil)
			So(entry.Clipboard, ShouldBeFalse)

			entry, err = parseRDPFile([]byte("full address:s:1.2.3.4\n"))
			So(err, ShouldBeNil)
			So(entry.Clipboard, ShouldBeTrue)
		})

		Convey("缺 full address 报错", func() {
			_, err := parseRDPFile([]byte("username:s:admin\n"))
			So(err, ShouldNotBeNil)
		})

		Convey("忽略未知行与注释行", func() {
			entry, err := parseRDPFile([]byte(
				"# comment\nnot a kv line\ngatewayhostname:s:gw.corp\nfull address:s:10.0.0.8\nunknown type:b:x\n"))
			So(err, ShouldBeNil)
			So(entry.Host, ShouldEqual, "10.0.0.8")
		})

		Convey("UTF-16LE（旧版 mstsc）编码", func() {
			u16 := utf16.Encode([]rune(sampleRDP))
			buf := []byte{0xFF, 0xFE}
			for _, v := range u16 {
				buf = append(buf, byte(v), byte(v>>8))
			}
			entry, err := parseRDPFile(buf)
			So(err, ShouldBeNil)
			So(entry.Host, ShouldEqual, "192.168.1.50")
			So(entry.Username, ShouldEqual, "administrator")
			So(entry.Domain, ShouldEqual, "CORP")
		})
	})
}

func TestPreviewRDPFiles(t *testing.T) {
	Convey("PreviewRDPFiles", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockAssetRepo := mock_asset_repo.NewMockAssetRepo(ctrl)
		asset_repo.RegisterAsset(mockAssetRepo)
		t.Cleanup(func() { asset_repo.RegisterAsset(asset_repo.NewAsset()) })

		existing := &asset_entity.Asset{ID: 7, Name: "old", Type: asset_entity.AssetTypeRDP}
		So(existing.SetRDPConfig(&asset_entity.RDPConfig{
			Host: "192.168.1.50", Port: 3389, Username: "administrator",
		}), ShouldBeNil)

		mockAssetRepo.EXPECT().
			List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
			Return([]*asset_entity.Asset{existing}, nil)

		result, err := PreviewRDPFiles(context.Background(), []RDPFileData{
			{Filename: "/path/web-server.rdp", Content: []byte(sampleRDP)},
			{Filename: "/path/broken.rdp", Content: []byte("username:s:admin\n")},
		})
		So(err, ShouldBeNil)
		So(result.Preview.Items, ShouldHaveLength, 2)

		item := result.Preview.Items[0]
		So(item.Name, ShouldEqual, "web-server")
		So(item.Host, ShouldEqual, "192.168.1.50")
		So(item.Port, ShouldEqual, 3389)
		So(item.Exists, ShouldBeTrue)

		broken := result.Preview.Items[1]
		So(broken.Name, ShouldEqual, "broken")
		So(broken.Reason, ShouldNotBeEmpty)

		Convey("会话缓存可按 sourceID 取回", func() {
			files, ok := RDPImportSessionData(result.SourceID)
			So(ok, ShouldBeTrue)
			So(files, ShouldHaveLength, 2)
			DeleteRDPImportSession(result.SourceID)
			_, ok = RDPImportSessionData(result.SourceID)
			So(ok, ShouldBeFalse)
		})
	})
}

func TestImportRDPSelected(t *testing.T) {
	setup := func(t *testing.T) (*gomock.Controller, *mock_asset_repo.MockAssetRepo) {
		t.Helper()
		ctrl := gomock.NewController(t)
		mockAssetRepo := mock_asset_repo.NewMockAssetRepo(ctrl)
		asset_repo.RegisterAsset(mockAssetRepo)
		t.Cleanup(func() { asset_repo.RegisterAsset(asset_repo.NewAsset()) })
		return ctrl, mockAssetRepo
	}

	Convey("ImportRDPSelected", t, func() {
		Convey("新建 RDP 资产，缺省分辨率补默认值", func() {
			_, mockAssetRepo := setup(t)
			var created *asset_entity.Asset
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return(nil, nil)
			mockAssetRepo.EXPECT().
				Create(gomock.Any(), gomock.AssignableToTypeOf(&asset_entity.Asset{})).
				DoAndReturn(func(_ context.Context, asset *asset_entity.Asset) error {
					asset.ID = 1
					created = asset
					return nil
				})

			result, err := ImportRDPSelected(context.Background(), []RDPFileData{
				{Filename: "/tmp/jump.rdp", Content: []byte(
					"full address:s:10.1.1.1\nusername:s:ops\ndesktopwidth:i:800\ndesktopheight:i:600\n")},
			}, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			So(created.Type, ShouldEqual, asset_entity.AssetTypeRDP)
			cfg, err := created.GetRDPConfig()
			So(err, ShouldBeNil)
			So(cfg.Host, ShouldEqual, "10.1.1.1")
			So(cfg.Port, ShouldEqual, 3389)
			So(cfg.Username, ShouldEqual, "ops")
			So(cfg.Width, ShouldEqual, 800)
			So(cfg.Height, ShouldEqual, 600)
			So(cfg.Clipboard, ShouldBeTrue)
		})

		Convey("未指定分辨率时默认 1280x720", func() {
			_, mockAssetRepo := setup(t)
			var created *asset_entity.Asset
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return(nil, nil)
			mockAssetRepo.EXPECT().
				Create(gomock.Any(), gomock.AssignableToTypeOf(&asset_entity.Asset{})).
				DoAndReturn(func(_ context.Context, asset *asset_entity.Asset) error {
					created = asset
					return nil
				})

			_, err := ImportRDPSelected(context.Background(), []RDPFileData{
				{Filename: "a.rdp", Content: []byte("full address:s:10.1.1.2\nusername:s:ops\n")},
			}, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			cfg, _ := created.GetRDPConfig()
			So(cfg.Width, ShouldEqual, 1280)
			So(cfg.Height, ShouldEqual, 720)
		})

		Convey("已存在且不覆盖时跳过", func() {
			_, mockAssetRepo := setup(t)
			existing := &asset_entity.Asset{ID: 7, Name: "old", Type: asset_entity.AssetTypeRDP}
			So(existing.SetRDPConfig(&asset_entity.RDPConfig{
				Host: "10.1.1.1", Port: 3389, Username: "ops",
			}), ShouldBeNil)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return([]*asset_entity.Asset{existing}, nil)

			result, err := ImportRDPSelected(context.Background(), []RDPFileData{
				{Filename: "a.rdp", Content: []byte("full address:s:10.1.1.1\nusername:s:ops\n")},
			}, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Skipped, ShouldEqual, 1)
			So(result.Success, ShouldEqual, 0)
		})

		Convey("覆盖导入保留已有密码与统一凭证", func() {
			_, mockAssetRepo := setup(t)
			existing := &asset_entity.Asset{ID: 7, Name: "old-name", Type: asset_entity.AssetTypeRDP}
			So(existing.SetRDPConfig(&asset_entity.RDPConfig{
				Host: "10.1.1.1", Port: 3389, Username: "ops",
				Password: "cipher-blob", CredentialID: 42,
			}), ShouldBeNil)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return([]*asset_entity.Asset{existing}, nil)
			var updated *asset_entity.Asset
			mockAssetRepo.EXPECT().
				Update(gomock.Any(), gomock.AssignableToTypeOf(&asset_entity.Asset{})).
				DoAndReturn(func(_ context.Context, asset *asset_entity.Asset) error {
					updated = asset
					return nil
				})

			result, err := ImportRDPSelected(context.Background(), []RDPFileData{
				{Filename: "renamed.rdp", Content: []byte(
					"full address:s:10.1.1.1\nusername:s:ops\ndomain:s:NEW\nredirectclipboard:i:0\n")},
			}, []int{0}, ImportOptions{Overwrite: true})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			So(updated.Name, ShouldEqual, "renamed")
			cfg, _ := updated.GetRDPConfig()
			So(cfg.Domain, ShouldEqual, "NEW")
			So(cfg.Clipboard, ShouldBeFalse)
			// .rdp 无可用密码，覆盖不能清掉已有认证
			So(cfg.Password, ShouldEqual, "cipher-blob")
			So(cfg.CredentialID, ShouldEqual, 42)
		})

		Convey("缺用户名的条目失败并带原因", func() {
			_, mockAssetRepo := setup(t)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return(nil, nil)

			result, err := ImportRDPSelected(context.Background(), []RDPFileData{
				{Filename: "no-user.rdp", Content: []byte("full address:s:10.1.1.3\n")},
			}, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Failed, ShouldEqual, 1)
			So(result.Errors, ShouldHaveLength, 1)
			So(strings.Contains(result.Errors[0].Reason, "用户名"), ShouldBeTrue)
		})

		Convey("空选择返回空结果", func() {
			_, _ = setup(t)
			result, err := ImportRDPSelected(context.Background(), []RDPFileData{
				{Filename: "a.rdp", Content: []byte("full address:s:10.1.1.1\nusername:s:ops\n")},
			}, nil, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Total, ShouldEqual, 0)
		})
	})
}
