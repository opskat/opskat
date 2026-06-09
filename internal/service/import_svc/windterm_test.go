package import_svc

import (
	"context"
	"runtime"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	. "github.com/smartystreets/goconvey/convey"
)

func TestWindTermWindowsKeyPath(t *testing.T) {
	Convey("WindTerm 密钥路径按当前系统可用性处理", t, func() {
		Convey("非 Windows 跳过 Windows 绝对密钥路径，回退 password", func() {
			for _, p := range []string{`C:\Users\foo\.ssh\id_rsa`, `C:/Users/foo/.ssh/id_rsa`, `\\server\share\id_rsa`} {
				entry := normalizeWindTermSession(windTermSession{Protocol: "SSH", Target: "10.0.0.1", IdentityFileWindows: p})
				if runtime.GOOS == "windows" {
					So(entry.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
					So(len(entry.PrivateKeys), ShouldEqual, 1)
				} else {
					So(entry.AuthType, ShouldEqual, asset_entity.AuthTypePassword)
					So(len(entry.PrivateKeys), ShouldEqual, 0)
				}
			}
		})

		Convey("Unix 风格路径在任何系统都按 key 导入", func() {
			entry := normalizeWindTermSession(windTermSession{Protocol: "SSH", Target: "10.0.0.1", IdentityFileWindows: "~/.ssh/id_rsa"})
			So(entry.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(len(entry.PrivateKeys), ShouldEqual, 1)
		})
	})
}

func TestWindTermConfigParsing(t *testing.T) {
	Convey("WindTerm 配置解析", t, func() {
		data := []byte(`[
  {
    "session.group": "Dogyun",
    "session.label": "CVM-CLD",
    "session.port": 22,
    "session.protocol": "SSH",
    "session.target": "127.0.0.1",
    "session.uuid": "ssh-1"
  },
  {
    "session.group": "Shell sessions",
    "session.label": "PowerShell",
    "session.protocol": "Shell"
  },
  {
    "session.group": "Router",
    "session.protocol": "SSH",
    "session.target": "192.168.31.1",
    "ssh.identityFilePath.windows": "~/.ssh/id_rsa",
    "session.autoLogin": "must-not-be-read"
  },
  {
    "session.protocol": "Telnet",
    "session.target": "192.168.31.1"
  }
]`)

		sessions, err := parseWindTermSessions(data)
		So(err, ShouldBeNil)
		So(len(sessions), ShouldEqual, 2)

		first := normalizeWindTermSession(sessions[0])
		So(first.Name, ShouldEqual, "CVM-CLD")
		So(first.Host, ShouldEqual, "127.0.0.1")
		So(first.Port, ShouldEqual, 22)
		So(first.Username, ShouldEqual, "root")
		So(first.AuthType, ShouldEqual, asset_entity.AuthTypePassword)
		So(first.GroupID, ShouldEqual, "Dogyun")

		second := normalizeWindTermSession(sessions[1])
		So(second.Name, ShouldEqual, "root@192.168.31.1:22")
		So(second.Port, ShouldEqual, 22)
		So(second.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
		So(len(second.PrivateKeys), ShouldEqual, 1)
		So(second.PrivateKeys[0], ShouldNotEqual, "")
	})
}

func TestPreviewWindTermConfig(t *testing.T) {
	ctx := context.Background()
	assetRepo := &windTermAssetRepo{}
	groupRepo := &windTermGroupRepo{}
	asset_repo.RegisterAsset(assetRepo)
	group_repo.RegisterGroup(groupRepo)

	Convey("WindTerm 预览过滤 SSH 并保留嵌套分组", t, func() {
		data := []byte(`[
  {"session.group":"Dogyun>物理服务器","session.label":"K3S","session.port":10087,"session.protocol":"SSH","session.target":"127.0.0.1"},
  {"session.group":"Shell sessions","session.label":"Local","session.protocol":"Shell"}
]`)

		preview, err := PreviewWindTermConfig(ctx, data)
		So(err, ShouldBeNil)
		So(len(preview.Items), ShouldEqual, 1)
		So(preview.Items[0].Name, ShouldEqual, "K3S")
		So(preview.Items[0].Port, ShouldEqual, 10087)
		So(preview.Items[0].Username, ShouldEqual, "root")
		So(preview.Items[0].GroupID, ShouldEqual, "Dogyun>物理服务器")
		So(preview.Groups, ShouldResemble, []PreviewGroup{
			{ID: "Dogyun", Name: "Dogyun"},
			{ID: "Dogyun>物理服务器", Name: "物理服务器"},
		})
	})

	Convey("分组路径含空格时归一化，item.GroupID 必须能在 Groups 中命中", t, func() {
		data := []byte(`[{"session.group":"Dogyun > 物理服务器","session.label":"K3S","session.protocol":"SSH","session.target":"127.0.0.1"}]`)

		preview, err := PreviewWindTermConfig(ctx, data)
		So(err, ShouldBeNil)
		So(len(preview.Items), ShouldEqual, 1)
		So(preview.Items[0].GroupID, ShouldEqual, "Dogyun>物理服务器")
		So(preview.Groups, ShouldResemble, []PreviewGroup{
			{ID: "Dogyun", Name: "Dogyun"},
			{ID: "Dogyun>物理服务器", Name: "物理服务器"},
		})
		// 前端按 item.GroupID 归类到 Groups，未命中则该行不渲染、无法取消勾选
		groupIDs := make(map[string]bool)
		for _, g := range preview.Groups {
			groupIDs[g.ID] = true
		}
		So(groupIDs[preview.Items[0].GroupID], ShouldBeTrue)
	})
}

func TestImportWindTermSelected(t *testing.T) {
	ctx := context.Background()

	Convey("WindTerm 导入", t, func() {
		Convey("创建嵌套分组并导入 key 资产", func() {
			assetRepo := &windTermAssetRepo{}
			groupRepo := &windTermGroupRepo{}
			asset_repo.RegisterAsset(assetRepo)
			group_repo.RegisterGroup(groupRepo)

			data := []byte(`[
  {"session.group":"Dogyun>物理服务器","session.label":"K3S","session.port":10087,"session.protocol":"SSH","session.target":"127.0.0.1","ssh.identityFilePath.windows":"~/.ssh/id_rsa","session.autoLogin":"secret"},
  {"session.group":"Oracle","session.label":"Tokyo","session.protocol":"SSH","session.target":"tokyo.example.com"}
]`)

			result, err := ImportWindTermSelected(ctx, data, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			So(result.Failed, ShouldEqual, 0)
			So(len(groupRepo.groups), ShouldEqual, 2)
			So(groupRepo.groups[0].Name, ShouldEqual, "Dogyun")
			So(groupRepo.groups[0].ParentID, ShouldEqual, 0)
			So(groupRepo.groups[1].Name, ShouldEqual, "物理服务器")
			So(groupRepo.groups[1].ParentID, ShouldEqual, groupRepo.groups[0].ID)
			So(len(assetRepo.assets), ShouldEqual, 1)
			So(assetRepo.assets[0].Name, ShouldEqual, "K3S")
			So(assetRepo.assets[0].GroupID, ShouldEqual, groupRepo.groups[1].ID)
			cfg, err := assetRepo.assets[0].GetSSHConfig()
			So(err, ShouldBeNil)
			So(cfg.Host, ShouldEqual, "127.0.0.1")
			So(cfg.Port, ShouldEqual, 10087)
			So(cfg.Username, ShouldEqual, "root")
			So(cfg.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(len(cfg.PrivateKeys), ShouldEqual, 1)
			So(cfg.Password, ShouldEqual, "")
		})

		Convey("重复资产未覆盖时跳过", func() {
			asset := newWindTermAsset(1, "Existing", 0, &asset_entity.SSHConfig{Host: "dup.example.com", Port: 22, Username: "root", AuthType: asset_entity.AuthTypePassword})
			assetRepo := &windTermAssetRepo{assets: []*asset_entity.Asset{asset}, nextID: 2}
			groupRepo := &windTermGroupRepo{}
			asset_repo.RegisterAsset(assetRepo)
			group_repo.RegisterGroup(groupRepo)

			data := []byte(`[{"session.label":"Dup","session.protocol":"SSH","session.target":"dup.example.com"}]`)
			result, err := ImportWindTermSelected(ctx, data, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Skipped, ShouldEqual, 1)
			So(len(assetRepo.assets), ShouldEqual, 1)
		})

		Convey("覆盖资产保留敏感字段", func() {
			asset := newWindTermAsset(1, "Existing", 0, &asset_entity.SSHConfig{
				Host: "dup.example.com", Port: 22, Username: "root", AuthType: asset_entity.AuthTypePassword,
				Password: "encrypted-password", CredentialID: 99, PrivateKeyPassphrase: "encrypted-passphrase",
			})
			assetRepo := &windTermAssetRepo{assets: []*asset_entity.Asset{asset}, nextID: 2}
			groupRepo := &windTermGroupRepo{}
			asset_repo.RegisterAsset(assetRepo)
			group_repo.RegisterGroup(groupRepo)

			data := []byte(`[{"session.group":"Router","session.label":"Updated","session.protocol":"SSH","session.target":"dup.example.com","ssh.identityFilePath.windows":"~/.ssh/id_ed25519"}]`)
			result, err := ImportWindTermSelected(ctx, data, []int{0}, ImportOptions{Overwrite: true})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			So(assetRepo.assets[0].Name, ShouldEqual, "Updated")
			cfg, err := assetRepo.assets[0].GetSSHConfig()
			So(err, ShouldBeNil)
			So(cfg.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(cfg.Password, ShouldEqual, "encrypted-password")
			// 新导入带来了密钥，不再复活旧的统一凭证，否则 CredentialID 会按解析优先级遮蔽导入的密钥
			So(cfg.CredentialID, ShouldEqual, 0)
			So(cfg.PrivateKeyPassphrase, ShouldEqual, "encrypted-passphrase")
			So(len(cfg.PrivateKeys), ShouldEqual, 1)
		})

		Convey("覆盖内联密钥资产时不丢失密钥认证", func() {
			asset := newWindTermAsset(1, "Existing", 0, &asset_entity.SSHConfig{
				Host: "dup.example.com", Port: 22, Username: "root",
				AuthType: asset_entity.AuthTypeKey, PrivateKeys: []string{"/home/u/.ssh/id_rsa"},
			})
			assetRepo := &windTermAssetRepo{assets: []*asset_entity.Asset{asset}, nextID: 2}
			groupRepo := &windTermGroupRepo{}
			asset_repo.RegisterAsset(assetRepo)
			group_repo.RegisterGroup(groupRepo)

			// WindTerm 常态：session 只有 host/port/group，不携带任何密钥
			data := []byte(`[{"session.label":"Updated","session.protocol":"SSH","session.target":"dup.example.com"}]`)
			result, err := ImportWindTermSelected(ctx, data, []int{0}, ImportOptions{Overwrite: true})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			cfg, err := assetRepo.assets[0].GetSSHConfig()
			So(err, ShouldBeNil)
			So(cfg.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(len(cfg.PrivateKeys), ShouldEqual, 1)
			So(cfg.PrivateKeys[0], ShouldEqual, "/home/u/.ssh/id_rsa")
		})

		Convey("覆盖无分组的 session 时保留已有分组", func() {
			asset := newWindTermAsset(1, "Existing", 7, &asset_entity.SSHConfig{
				Host: "dup.example.com", Port: 22, Username: "root", AuthType: asset_entity.AuthTypePassword,
			})
			assetRepo := &windTermAssetRepo{assets: []*asset_entity.Asset{asset}, nextID: 2}
			groupRepo := &windTermGroupRepo{}
			asset_repo.RegisterAsset(assetRepo)
			group_repo.RegisterGroup(groupRepo)

			data := []byte(`[{"session.label":"Updated","session.protocol":"SSH","session.target":"dup.example.com"}]`)
			result, err := ImportWindTermSelected(ctx, data, []int{0}, ImportOptions{Overwrite: true})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			So(assetRepo.assets[0].GroupID, ShouldEqual, 7)
		})
	})
}

type windTermAssetRepo struct {
	assets []*asset_entity.Asset
	nextID int64
}

func (r *windTermAssetRepo) Find(_ context.Context, id int64) (*asset_entity.Asset, error) {
	for _, asset := range r.assets {
		if asset.ID == id {
			return asset, nil
		}
	}
	return nil, nil
}

func (r *windTermAssetRepo) List(_ context.Context, opts asset_repo.ListOptions) ([]*asset_entity.Asset, error) {
	var assets []*asset_entity.Asset
	for _, asset := range r.assets {
		if opts.Type != "" && asset.Type != opts.Type {
			continue
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func (r *windTermAssetRepo) Create(_ context.Context, asset *asset_entity.Asset) error {
	if r.nextID == 0 {
		r.nextID = 1
	}
	asset.ID = r.nextID
	r.nextID++
	r.assets = append(r.assets, asset)
	return nil
}

func (r *windTermAssetRepo) Update(_ context.Context, asset *asset_entity.Asset) error {
	for i, existing := range r.assets {
		if existing.ID == asset.ID {
			r.assets[i] = asset
			return nil
		}
	}
	r.assets = append(r.assets, asset)
	return nil
}

func (r *windTermAssetRepo) Delete(context.Context, int64) error             { return nil }
func (r *windTermAssetRepo) MoveToGroup(context.Context, int64, int64) error { return nil }
func (r *windTermAssetRepo) DeleteByGroupID(context.Context, int64) error    { return nil }
func (r *windTermAssetRepo) FindByCredentialID(context.Context, int64) ([]*asset_entity.Asset, error) {
	return nil, nil
}
func (r *windTermAssetRepo) UpdateSortOrder(context.Context, int64, int) error     { return nil }
func (r *windTermAssetRepo) UpdateGroupID(context.Context, int64, int64) error     { return nil }
func (r *windTermAssetRepo) CountByTypes(context.Context, []string) (int64, error) { return 0, nil }

type windTermGroupRepo struct {
	groups []*group_entity.Group
	nextID int64
}

func (r *windTermGroupRepo) Find(_ context.Context, id int64) (*group_entity.Group, error) {
	for _, group := range r.groups {
		if group.ID == id {
			return group, nil
		}
	}
	return nil, nil
}

func (r *windTermGroupRepo) List(context.Context) ([]*group_entity.Group, error) {
	return r.groups, nil
}

func (r *windTermGroupRepo) Create(_ context.Context, group *group_entity.Group) error {
	if r.nextID == 0 {
		r.nextID = 1
	}
	group.ID = r.nextID
	r.nextID++
	r.groups = append(r.groups, group)
	return nil
}

func (r *windTermGroupRepo) Update(context.Context, *group_entity.Group) error    { return nil }
func (r *windTermGroupRepo) Delete(context.Context, int64) error                  { return nil }
func (r *windTermGroupRepo) UpdateName(context.Context, int64, string) error      { return nil }
func (r *windTermGroupRepo) ReparentChildren(context.Context, int64, int64) error { return nil }
func (r *windTermGroupRepo) UpdateSortOrder(context.Context, int64, int) error    { return nil }
func (r *windTermGroupRepo) UpdateParentID(context.Context, int64, int64) error   { return nil }

func newWindTermAsset(id int64, name string, groupID int64, cfg *asset_entity.SSHConfig) *asset_entity.Asset {
	asset := &asset_entity.Asset{ID: id, Name: name, Type: asset_entity.AssetTypeSSH, GroupID: groupID, Icon: "server", Status: asset_entity.StatusActive}
	_ = asset.SetSSHConfig(cfg)
	return asset
}
