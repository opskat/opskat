package import_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/group_repo/mock_group_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/xuri/excelize/v2"
	"go.uber.org/mock/gomock"
)

// buildRDPExcel 构造测试用 xlsx：headers 为表头，rows 为数据行（nil 单元格跳过）
func buildRDPExcel(t *testing.T, headers []string, rows [][]interface{}) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	const sheet = "Sheet1"
	for col, h := range headers {
		cell, err := excelize.CoordinatesToCellName(col+1, 1)
		So(err, ShouldBeNil)
		So(f.SetCellValue(sheet, cell, h), ShouldBeNil)
	}
	for r, row := range rows {
		for col, v := range row {
			if v == nil {
				continue
			}
			cell, err := excelize.CoordinatesToCellName(col+1, r+2)
			So(err, ShouldBeNil)
			So(f.SetCellValue(sheet, cell, v), ShouldBeNil)
		}
	}
	buf, err := f.WriteToBuffer()
	So(err, ShouldBeNil)
	return buf.Bytes()
}

func TestParseRDPExcel(t *testing.T) {
	Convey("parseRDPExcel", t, func() {
		Convey("中文表头完整解析", func() {
			data := buildRDPExcel(t,
				[]string{"名称", "分组", "地址", "端口", "用户名", "密码", "域", "宽度", "高度", "剪贴板"},
				[][]interface{}{
					{"web", "生产", "192.168.1.10", 3390, "admin", "p@ss", "CORP", 1920, 1080, "否"},
				})
			rows, err := parseRDPExcel(data)
			So(err, ShouldBeNil)
			So(rows, ShouldHaveLength, 1)
			r := rows[0]
			So(r.Name, ShouldEqual, "web")
			So(r.Group, ShouldEqual, "生产")
			So(r.Host, ShouldEqual, "192.168.1.10")
			So(r.Port, ShouldEqual, 3390)
			So(r.Username, ShouldEqual, "admin")
			So(r.Password, ShouldEqual, "p@ss")
			So(r.HasPassword, ShouldBeTrue)
			So(r.Domain, ShouldEqual, "CORP")
			So(r.Width, ShouldEqual, 1920)
			So(r.Height, ShouldEqual, 1080)
			So(r.Clipboard, ShouldBeFalse)
		})

		Convey("英文表头别名 + 缺省值", func() {
			data := buildRDPExcel(t,
				[]string{"Name", "Host", "Port", "Username", "Password"},
				[][]interface{}{
					{"db", "10.0.0.2", 3389, "ops", "x"},
				})
			rows, err := parseRDPExcel(data)
			So(err, ShouldBeNil)
			So(rows[0].Host, ShouldEqual, "10.0.0.2")
			So(rows[0].Clipboard, ShouldBeTrue) // 缺省开启
		})

		Convey("地址端口与 DOMAIN\\user 统一规范化，独立端口列优先", func() {
			data := buildRDPExcel(t,
				[]string{"名称", "地址", "端口", "用户名"},
				[][]interface{}{
					{"embedded", "server.example.com:3390", nil, `CORP\ops`},
					{"override", "[fe80::1]:3390", 3391, `OTHER\admin`},
				})
			rows, err := parseRDPExcel(data)
			So(err, ShouldBeNil)
			So(rows[0].Host, ShouldEqual, "server.example.com")
			So(rows[0].Port, ShouldEqual, 3390)
			So(rows[0].Domain, ShouldEqual, "CORP")
			So(rows[0].Username, ShouldEqual, "ops")
			So(rows[1].Host, ShouldEqual, "fe80::1")
			So(rows[1].Port, ShouldEqual, 3391)
			So(rows[1].Domain, ShouldEqual, "OTHER")
			So(rows[1].Username, ShouldEqual, "admin")
		})

		Convey("重复逻辑表头报错", func() {
			data := buildRDPExcel(t,
				[]string{"地址", "Host", "用户名"},
				[][]interface{}{{"10.0.0.1", "10.0.0.2", "ops"}})
			_, err := parseRDPExcel(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "重复")
		})

		Convey("非法字段保留为行错误而不静默默认或截断", func() {
			data := buildRDPExcel(t,
				[]string{"名称", "地址", "端口", "用户名", "宽度", "高度", "剪贴板"},
				[][]interface{}{
					{"bad-port", "10.0.0.1", "3390.9", "ops", nil, nil, nil},
					{"bad-width", "10.0.0.2", nil, "ops", 65536, nil, nil},
					{"bad-height", "10.0.0.3", nil, "ops", nil, 0, nil},
					{"bad-clipboard", "10.0.0.4", nil, "ops", nil, nil, "maybe"},
					{"missing-host", nil, nil, "ops", nil, nil, nil},
					{"missing-user", "10.0.0.6", nil, nil, nil, nil, nil},
				})
			rows, err := parseRDPExcel(data)
			So(err, ShouldBeNil)
			So(rows, ShouldHaveLength, 6)
			So(rows[0].Reason, ShouldContainSubstring, "端口")
			So(rows[1].Reason, ShouldContainSubstring, "宽度")
			So(rows[2].Reason, ShouldContainSubstring, "高度")
			So(rows[3].Reason, ShouldContainSubstring, "剪贴板")
			So(rows[4].Reason, ShouldContainSubstring, "地址")
			So(rows[5].Reason, ShouldContainSubstring, "用户名")
		})

		Convey("空行跳过，未识别列忽略", func() {
			data := buildRDPExcel(t,
				[]string{"名称", "地址", "备注"},
				[][]interface{}{
					{nil, nil, nil},
					{"a", "1.1.1.1", "自由备注列"},
				})
			rows, err := parseRDPExcel(data)
			So(err, ShouldBeNil)
			So(rows, ShouldHaveLength, 1)
			So(rows[0].Host, ShouldEqual, "1.1.1.1")
		})

		Convey("缺地址列报错", func() {
			data := buildRDPExcel(t,
				[]string{"名称", "用户名"},
				[][]interface{}{{"a", "u"}})
			_, err := parseRDPExcel(data)
			So(err, ShouldNotBeNil)
		})

		Convey("只有表头没有数据行报错", func() {
			data := buildRDPExcel(t, []string{"名称", "地址"}, nil)
			_, err := parseRDPExcel(data)
			So(err, ShouldNotBeNil)
		})
	})
}

func TestBuildRDPExcelTemplate(t *testing.T) {
	Convey("BuildRDPExcelTemplate 模板可被自身解析器 round-trip", t, func() {
		data, err := BuildRDPExcelTemplate()
		So(err, ShouldBeNil)
		So(len(data), ShouldBeGreaterThan, 0)

		rows, err := parseRDPExcel(data)
		So(err, ShouldBeNil)
		So(rows, ShouldHaveLength, 2)

		So(rows[0].Name, ShouldEqual, "财务服务器")
		So(rows[0].Group, ShouldEqual, "生产环境")
		So(rows[0].Host, ShouldEqual, "192.168.1.60")
		So(rows[0].Port, ShouldEqual, 3389)
		So(rows[0].Username, ShouldEqual, "administrator")
		So(rows[0].HasPassword, ShouldBeTrue)
		So(rows[0].Domain, ShouldEqual, "CORP")
		So(rows[0].Width, ShouldEqual, 1920)
		So(rows[0].Height, ShouldEqual, 1080)
		So(rows[0].Clipboard, ShouldBeTrue)

		// 第二行为留空示例：端口/密码等缺省
		So(rows[1].Port, ShouldEqual, 0)
		So(rows[1].HasPassword, ShouldBeFalse)
		So(rows[1].Clipboard, ShouldBeTrue)
	})
}

func setupRDPExcelRepos(t *testing.T) (*mock_asset_repo.MockAssetRepo, *mock_group_repo.MockGroupRepo) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mockAssetRepo := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockGroupRepo := mock_group_repo.NewMockGroupRepo(ctrl)
	asset_repo.RegisterAsset(mockAssetRepo)
	group_repo.RegisterGroup(mockGroupRepo)
	t.Cleanup(func() {
		asset_repo.RegisterAsset(asset_repo.NewAsset())
		group_repo.RegisterGroup(group_repo.NewGroup())
	})
	return mockAssetRepo, mockGroupRepo
}

func TestPreviewRDPExcel(t *testing.T) {
	Convey("PreviewRDPExcel", t, func() {
		mockAssetRepo, _ := setupRDPExcelRepos(t)

		existing := &asset_entity.Asset{ID: 3, Name: "dup", Type: asset_entity.AssetTypeRDP}
		So(existing.SetRDPConfig(&asset_entity.RDPConfig{
			Host: "10.0.0.2", Port: 3389, Username: "ops",
		}), ShouldBeNil)
		mockAssetRepo.EXPECT().
			List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
			Return([]*asset_entity.Asset{existing}, nil)

		data := buildRDPExcel(t,
			[]string{"名称", "分组", "地址", "端口", "用户名", "密码"},
			[][]interface{}{
				{"new", "生产", "10.0.0.1", nil, "ops", "secret"},
				{"dup", nil, "10.0.0.2", nil, "ops", nil},
				{"invalid", nil, "10.0.0.3", "bad", "ops", nil},
			})
		result, err := PreviewRDPExcel(context.Background(), data)
		So(err, ShouldBeNil)

		So(result.Preview.Groups, ShouldHaveLength, 1)
		So(result.Preview.Groups[0].Name, ShouldEqual, "生产")

		So(result.Preview.Items, ShouldHaveLength, 3)
		first := result.Preview.Items[0]
		So(first.Name, ShouldEqual, "new")
		So(first.GroupID, ShouldEqual, "生产")
		So(first.Port, ShouldEqual, 3389)
		So(first.HasPassword, ShouldBeTrue)
		So(first.Exists, ShouldBeFalse)
		So(first.Reason, ShouldBeEmpty)

		second := result.Preview.Items[1]
		So(second.Exists, ShouldBeTrue)

		invalid := result.Preview.Items[2]
		So(invalid.Reason, ShouldContainSubstring, "端口")

		Convey("密码不出现在预览条目中", func() {
			for _, item := range result.Preview.Items {
				// 序列化后断言不含密码原文，拦截未来误加的密码字段
				raw, err := json.Marshal(item)
				So(err, ShouldBeNil)
				So(string(raw), ShouldNotContainSubstring, "secret")
			}
		})

		Convey("会话缓存可按 sourceID 取回", func() {
			cached, ok := RDPExcelImportSessionData(result.SourceID)
			So(ok, ShouldBeTrue)
			So(cached, ShouldHaveLength, len(data))
			DeleteRDPExcelImportSession(result.SourceID)
			_, ok = RDPExcelImportSessionData(result.SourceID)
			So(ok, ShouldBeFalse)
		})
	})
}

func TestImportRDPExcelSelected(t *testing.T) {
	Convey("ImportRDPExcelSelected", t, func() {
		Convey("密码加密入库 + 分组自动创建", func() {
			mockAssetRepo, mockGroupRepo := setupRDPExcelRepos(t)
			credential_svc.SetDefault(credential_svc.New("rdp-excel-test", []byte("1234567890abcdef")))

			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return(nil, nil)
			mockGroupRepo.EXPECT().List(gomock.Any()).Return(nil, nil)
			mockGroupRepo.EXPECT().
				Create(gomock.Any(), gomock.AssignableToTypeOf(&group_entity.Group{})).
				DoAndReturn(func(_ context.Context, g *group_entity.Group) error {
					g.ID = 9
					return nil
				})
			var created *asset_entity.Asset
			mockAssetRepo.EXPECT().
				Create(gomock.Any(), gomock.AssignableToTypeOf(&asset_entity.Asset{})).
				DoAndReturn(func(_ context.Context, asset *asset_entity.Asset) error {
					asset.ID = 1
					created = asset
					return nil
				})

			data := buildRDPExcel(t,
				[]string{"名称", "分组", "地址", "端口", "用户名", "密码", "域"},
				[][]interface{}{
					{"srv", "生产", "10.0.0.8", 3390, "administrator", "Secret@1", "CORP"},
				})
			result, err := ImportRDPExcelSelected(context.Background(), data, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)

			So(created.Type, ShouldEqual, asset_entity.AssetTypeRDP)
			So(created.GroupID, ShouldEqual, 9)
			cfg, err := created.GetRDPConfig()
			So(err, ShouldBeNil)
			So(cfg.Host, ShouldEqual, "10.0.0.8")
			So(cfg.Port, ShouldEqual, 3390)
			So(cfg.Domain, ShouldEqual, "CORP")
			So(cfg.Clipboard, ShouldBeTrue)
			So(cfg.Width, ShouldEqual, 1280)
			So(cfg.Height, ShouldEqual, 720)
			// 密码以密文入库且可解密回原文
			So(cfg.Password, ShouldNotEqual, "Secret@1")
			So(cfg.Password, ShouldNotBeEmpty)
			plain, err := credential_svc.Default().Decrypt(cfg.Password)
			So(err, ShouldBeNil)
			So(plain, ShouldEqual, "Secret@1")
		})

		Convey("覆盖模式：密码留空保留旧认证，填新密码则替换", func() {
			mockAssetRepo, _ := setupRDPExcelRepos(t)
			credential_svc.SetDefault(credential_svc.New("rdp-excel-test", []byte("1234567890abcdef")))

			existing := &asset_entity.Asset{ID: 7, Name: "old", Type: asset_entity.AssetTypeRDP}
			So(existing.SetRDPConfig(&asset_entity.RDPConfig{
				Host: "10.0.0.8", Port: 3389, Username: "ops",
				Password: "old-cipher", CredentialID: 42,
				Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "proxy.example.com", Port: 1080},
				ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{
					Type: asset_entity.ProxyChainLayerSSH, SSHAssetID: 99,
				}}},
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

			data := buildRDPExcel(t,
				[]string{"名称", "地址", "用户名"},
				[][]interface{}{{"renamed", "10.0.0.8", "ops"}})
			result, err := ImportRDPExcelSelected(context.Background(), data, []int{0}, ImportOptions{Overwrite: true})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			cfg, _ := updated.GetRDPConfig()
			So(cfg.Password, ShouldEqual, "old-cipher")
			So(cfg.CredentialID, ShouldEqual, 42)
			So(cfg.Proxy, ShouldNotBeNil)
			So(cfg.Proxy.Host, ShouldEqual, "proxy.example.com")
			So(cfg.ProxyChain, ShouldNotBeNil)
			So(cfg.ProxyChain.Layers[0].SSHAssetID, ShouldEqual, 99)

			// 第二次覆盖且带新密码 → 旧密码与统一凭证被替换
			existing2 := &asset_entity.Asset{ID: 8, Name: "old2", Type: asset_entity.AssetTypeRDP}
			So(existing2.SetRDPConfig(&asset_entity.RDPConfig{
				Host: "10.0.0.9", Port: 3389, Username: "ops",
				Password: "old-cipher", CredentialID: 42,
			}), ShouldBeNil)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return([]*asset_entity.Asset{existing2}, nil)
			var updated2 *asset_entity.Asset
			mockAssetRepo.EXPECT().
				Update(gomock.Any(), gomock.AssignableToTypeOf(&asset_entity.Asset{})).
				DoAndReturn(func(_ context.Context, asset *asset_entity.Asset) error {
					updated2 = asset
					return nil
				})
			data2 := buildRDPExcel(t,
				[]string{"名称", "地址", "用户名", "密码"},
				[][]interface{}{{"again", "10.0.0.9", "ops", "NewPass@9"}})
			result2, err := ImportRDPExcelSelected(context.Background(), data2, []int{0}, ImportOptions{Overwrite: true})
			So(err, ShouldBeNil)
			So(result2.Success, ShouldEqual, 1)
			cfg2, _ := updated2.GetRDPConfig()
			So(cfg2.Password, ShouldNotEqual, "old-cipher")
			So(cfg2.CredentialID, ShouldEqual, 0)
			plain, err := credential_svc.Default().Decrypt(cfg2.Password)
			So(err, ShouldBeNil)
			So(plain, ShouldEqual, "NewPass@9")
		})

		Convey("缺用户名失败并带原因", func() {
			mockAssetRepo, _ := setupRDPExcelRepos(t)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return(nil, nil)

			data := buildRDPExcel(t,
				[]string{"名称", "地址"},
				[][]interface{}{{"no-user", "10.0.0.3"}})
			result, err := ImportRDPExcelSelected(context.Background(), data, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Failed, ShouldEqual, 1)
			So(result.Errors, ShouldHaveLength, 1)
			So(result.Errors[0].Name, ShouldEqual, "no-user")
			So(result.Errors[0].Status, ShouldEqual, "failed")
			So(result.Errors[0].Reason, ShouldContainSubstring, "用户名")
		})

		Convey("已存在且未开启覆盖时在创建分组前跳过", func() {
			mockAssetRepo, _ := setupRDPExcelRepos(t)

			existing := &asset_entity.Asset{ID: 5, Name: "old", Type: asset_entity.AssetTypeRDP}
			So(existing.SetRDPConfig(&asset_entity.RDPConfig{
				Host: "10.0.0.8", Port: 3389, Username: "ops",
			}), ShouldBeNil)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return([]*asset_entity.Asset{existing}, nil)

			data := buildRDPExcel(t,
				[]string{"名称", "分组", "地址", "用户名"},
				[][]interface{}{{"dup", "不应创建", "10.0.0.8", "ops"}})
			result, err := ImportRDPExcelSelected(context.Background(), data, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Skipped, ShouldEqual, 1)
			So(result.Errors, ShouldHaveLength, 1)
			So(result.Errors[0].Name, ShouldEqual, "dup")
			So(result.Errors[0].Status, ShouldEqual, "skipped")
			So(result.Errors[0].Reason, ShouldNotBeEmpty)
		})

		Convey("同主机同用户名但不同域不是重复资产", func() {
			mockAssetRepo, _ := setupRDPExcelRepos(t)
			existing := &asset_entity.Asset{ID: 6, Name: "corp", Type: asset_entity.AssetTypeRDP}
			So(existing.SetRDPConfig(&asset_entity.RDPConfig{
				Host: "10.0.0.8", Port: 3389, Domain: "CORP", Username: "ops",
			}), ShouldBeNil)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return([]*asset_entity.Asset{existing}, nil)
			mockAssetRepo.EXPECT().
				Create(gomock.Any(), gomock.AssignableToTypeOf(&asset_entity.Asset{})).
				Return(nil)

			data := buildRDPExcel(t,
				[]string{"名称", "地址", "用户名", "域"},
				[][]interface{}{{"other", "10.0.0.8", "ops", "OTHER"}})
			result, err := ImportRDPExcelSelected(context.Background(), data, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Success, ShouldEqual, 1)
			So(result.Skipped, ShouldEqual, 0)
		})

		Convey("非法行导入失败且不创建资产", func() {
			mockAssetRepo, _ := setupRDPExcelRepos(t)
			mockAssetRepo.EXPECT().
				List(gomock.Any(), asset_repo.ListOptions{Type: asset_entity.AssetTypeRDP, GroupID: 0}).
				Return(nil, nil)
			data := buildRDPExcel(t,
				[]string{"名称", "地址", "端口", "用户名"},
				[][]interface{}{{"bad", "10.0.0.9", "abc", "ops"}})
			result, err := ImportRDPExcelSelected(context.Background(), data, []int{0}, ImportOptions{})
			So(err, ShouldBeNil)
			So(result.Failed, ShouldEqual, 1)
			So(result.Errors[0].Reason, ShouldContainSubstring, "端口")
		})
	})
}
