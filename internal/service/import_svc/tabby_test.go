package import_svc

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/group_repo/mock_group_repo"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"gopkg.in/yaml.v3"
)

func TestTabbyConfigParsing(t *testing.T) {
	Convey("Tabby 配置 YAML 解析", t, func() {
		Convey("基本 SSH profile 解析", func() {
			data := `
profiles:
  - type: ssh
    name: my-server
    icon: server
    color: "#ff0000"
    id: uuid-1234
    weight: 10
    options:
      host: 192.168.1.100
      port: 2222
      user: admin
      auth: publickey
      privateKeys:
        - "file:///home/user/.ssh/id_rsa"
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)
			So(len(cfg.Profiles), ShouldEqual, 1)

			p := cfg.Profiles[0]
			So(p.Type, ShouldEqual, "ssh")
			So(p.Name, ShouldEqual, "my-server")
			So(p.Options.Host, ShouldEqual, "192.168.1.100")
			So(p.Options.Port, ShouldEqual, 2222)
			So(p.Options.User, ShouldEqual, "admin")
			So(p.Options.Auth, ShouldEqual, "publickey")
			So(p.Options.PrivateKeys, ShouldResemble, []string{"file:///home/user/.ssh/id_rsa"})
		})

		Convey("分组关联", func() {
			data := `
groups:
  - id: group-uuid-1
    name: Production
  - id: group-uuid-2
    name: Staging
profiles:
  - type: ssh
    name: prod-web
    group: group-uuid-1
    options:
      host: 10.0.0.1
  - type: ssh
    name: staging-web
    group: group-uuid-2
    options:
      host: 10.0.1.1
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)
			So(len(cfg.Groups), ShouldEqual, 2)
			So(cfg.Groups[0].Name, ShouldEqual, "Production")
			So(cfg.Profiles[0].Group, ShouldEqual, "group-uuid-1")
			So(cfg.Profiles[1].Group, ShouldEqual, "group-uuid-2")
		})

		Convey("端口转发配置", func() {
			data := `
profiles:
  - type: ssh
    name: tunnel-server
    options:
      host: 10.0.0.1
      forwardedPorts:
        - type: local
          host: 127.0.0.1
          port: 8080
          targetAddress: 10.0.0.2
          targetPort: 80
        - type: remote
          host: 0.0.0.0
          port: 9090
          targetAddress: 127.0.0.1
          targetPort: 3000
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)

			fps := cfg.Profiles[0].Options.ForwardedPorts
			So(len(fps), ShouldEqual, 2)
			So(fps[0].Type, ShouldEqual, "local")
			So(fps[0].Host, ShouldEqual, "127.0.0.1")
			So(fps[0].Port, ShouldEqual, 8080)
			So(fps[0].TargetHost, ShouldEqual, "10.0.0.2")
			So(fps[0].TargetPort, ShouldEqual, 80)
			So(fps[1].Type, ShouldEqual, "remote")
		})

		Convey("SOCKS 代理配置", func() {
			data := `
profiles:
  - type: ssh
    name: proxy-server
    options:
      host: 10.0.0.1
      socksProxyHost: socks.proxy.com
      socksProxyPort: 1080
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)

			opts := cfg.Profiles[0].Options
			So(opts.SocksProxyHost, ShouldEqual, "socks.proxy.com")
			So(opts.SocksProxyPort, ShouldEqual, 1080)
		})

		Convey("跳板机配置", func() {
			data := `
profiles:
  - type: ssh
    name: bastion
    options:
      host: bastion.example.com
  - type: ssh
    name: internal
    options:
      host: 10.0.0.1
      jumpHost: bastion
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)

			So(cfg.Profiles[1].Options.JumpHost, ShouldEqual, "bastion")
		})

		Convey("非 SSH 类型被过滤", func() {
			data := `
profiles:
  - type: serial
    name: serial-port
    options:
      host: /dev/ttyUSB0
  - type: ssh
    name: ssh-server
    options:
      host: 10.0.0.1
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)
			So(len(cfg.Profiles), ShouldEqual, 2)

			// PreviewTabbyConfig 会过滤非 SSH 类型
			sshCount := 0
			for _, p := range cfg.Profiles {
				if p.Type == "ssh" {
					sshCount++
				}
			}
			So(sshCount, ShouldEqual, 1)
		})

		Convey("默认值处理", func() {
			data := `
profiles:
  - type: ssh
    name: defaults
    options:
      host: 10.0.0.1
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)

			opts := cfg.Profiles[0].Options
			So(opts.Port, ShouldEqual, 0)  // 导入时会默认为 22
			So(opts.User, ShouldEqual, "") // 导入时会默认为 root
			So(opts.Auth, ShouldEqual, "") // 导入时会默认为 password
		})

		Convey("多私钥文件", func() {
			data := `
profiles:
  - type: ssh
    name: multi-key
    options:
      host: 10.0.0.1
      auth: publickey
      privateKeys:
        - "file:///home/user/.ssh/id_ed25519"
        - "file:///home/user/.ssh/id_rsa"
        - "/direct/path/key"
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)

			keys := cfg.Profiles[0].Options.PrivateKeys
			So(len(keys), ShouldEqual, 3)
			So(keys[0], ShouldEqual, "file:///home/user/.ssh/id_ed25519")
			So(keys[1], ShouldEqual, "file:///home/user/.ssh/id_rsa")
			So(keys[2], ShouldEqual, "/direct/path/key")
		})

		Convey("空配置", func() {
			data := `
profiles: []
groups: []
`
			var cfg tabbyConfig
			err := yaml.Unmarshal([]byte(data), &cfg)
			So(err, ShouldBeNil)
			So(cfg.Profiles, ShouldBeEmpty)
			So(cfg.Groups, ShouldBeEmpty)
		})
	})
}

func TestImportTabbySelected(t *testing.T) {
	Convey("ImportTabbySelected", t, func() {
		Convey("key 文件认证导入为 SSH key 认证", func() {
			data := []byte(`
profiles:
  - type: ssh
    name: key-server
    options:
      host: 10.0.0.1
      port: 2222
      user: admin
      auth: privateKey
      privateKey: "file:///C:/Users/me/.ssh/id_rsa"
`)

			sshCfg := importSingleTabbySSHProfile(t, data)
			So(sshCfg.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(sshCfg.PrivateKeys, ShouldResemble, []string{"C:/Users/me/.ssh/id_rsa"})
			So(sshCfg.Password, ShouldEqual, "")
		})

		Convey("Tabby publicKey + privateKeys 数组导入为 SSH key 认证", func() {
			data := []byte(`
profiles:
  - type: ssh
    name: tabby-key-profile
    icon: fas fa-desktop
    options:
      host: example.internal
      algorithms: {}
      input: {}
      privateKeys:
        - file:///home/user/.ssh/id_rsa
      auth: publicKey
    weight: -1
    id: ssh:custom:key-profile:00000000-0000-0000-0000-000000000000
`)

			sshCfg := importSingleTabbySSHProfile(t, data)
			So(sshCfg.Host, ShouldEqual, "example.internal")
			So(sshCfg.Username, ShouldEqual, "root")
			So(sshCfg.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(sshCfg.PrivateKeys, ShouldResemble, []string{"/home/user/.ssh/id_rsa"})
			So(sshCfg.Password, ShouldEqual, "")
		})
	})
}

func importSingleTabbySSHProfile(t *testing.T, data []byte) *asset_entity.SSHConfig {
	t.Helper()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockAssetRepo := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockGroupRepo := mock_group_repo.NewMockGroupRepo(ctrl)
	asset_repo.RegisterAsset(mockAssetRepo)
	group_repo.RegisterGroup(mockGroupRepo)
	t.Cleanup(func() {
		asset_repo.RegisterAsset(asset_repo.NewAsset())
		group_repo.RegisterGroup(group_repo.NewGroup())
	})

	var created *asset_entity.Asset
	mockAssetRepo.EXPECT().
		List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeSSH, GroupID: 0}).
		Return(nil, nil)
	mockGroupRepo.EXPECT().List(gomock.Any()).Return(nil, nil)
	mockAssetRepo.EXPECT().
		Create(gomock.Any(), gomock.AssignableToTypeOf(&asset_entity.Asset{})).
		DoAndReturn(func(_ context.Context, asset *asset_entity.Asset) error {
			created = asset
			asset.ID = 1
			return nil
		})

	result, err := ImportTabbySelected(context.Background(), data, []int{0}, ImportOptions{})
	So(err, ShouldBeNil)
	So(result.Success, ShouldEqual, 1)
	So(created, ShouldNotBeNil)

	sshCfg, err := created.GetSSHConfig()
	So(err, ShouldBeNil)
	return sshCfg
}
