package backup_svc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"

	. "github.com/smartystreets/goconvey/convey"
)

// testFingerprint 返回一个规范、合法的 SHA256 指纹（32 字节 base64 无填充）。
func testFingerprint() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(raw)
}

// setupBackupTest 提供内存 SQLite + 已注册仓库的上下文，供备份导出/导入测试使用。
func setupBackupTest(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(
		&asset_entity.Asset{},
		&group_entity.Group{},
		&ssh_agent_source_entity.SSHAgentSource{},
	))
	db.SetDefault(gdb)
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	group_repo.RegisterGroup(group_repo.NewGroup())
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	return context.Background()
}

func createAgentSource(t *testing.T, ctx context.Context, name, epType, endpoint string) int64 {
	t.Helper()
	src := &ssh_agent_source_entity.SSHAgentSource{
		Name:         name,
		EndpointType: epType,
		Endpoint:     endpoint,
		Createtime:   1,
		Updatetime:   1,
	}
	require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, src))
	return src.ID
}

// agentAssetConfig 构造一个 auth_type=agent 的 SSH 配置 JSON。
func agentAssetConfig(sourceID int64, fingerprint string) string {
	return fmt.Sprintf(`{"host":"h","port":22,"username":"u","auth_type":"agent","agent_source_id":%d,"agent_key_fingerprint":%q}`,
		sourceID, fingerprint)
}

func createAgentAsset(t *testing.T, ctx context.Context, name string, sourceID int64) int64 {
	t.Helper()
	asset := &asset_entity.Asset{
		Name:       name,
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Config:     agentAssetConfig(sourceID, testFingerprint()),
		Createtime: 1,
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))
	return asset.ID
}

// findAgentAssetConfig 按名称查找资产并解析 SSH 配置。
func findAgentAssetConfig(t *testing.T, ctx context.Context, name string) *asset_entity.SSHConfig {
	t.Helper()
	assets, err := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
	require.NoError(t, err)
	for _, a := range assets {
		if a.Name == name {
			cfg, err := a.GetSSHConfig()
			require.NoError(t, err)
			return cfg
		}
	}
	t.Fatalf("asset %s not found", name)
	return nil
}

func TestExport_AgentSources(t *testing.T) {
	Convey("Agent 来源定义导出", t, func() {
		ctx := setupBackupTest(t)

		srcA := createAgentSource(t, ctx, "A", "environment", "SSH_AUTH_SOCK")
		srcB := createAgentSource(t, ctx, "B", "unix_socket", "/tmp/b.sock")
		createAgentSource(t, ctx, "unused", "unix_socket", "/tmp/unused.sock")
		assetX := createAgentAsset(t, ctx, "boxX", srcA)
		createAgentAsset(t, ctx, "boxY", srcB)

		Convey("部分导出只包含被引用来源", func() {
			data, err := Export(ctx, &ExportOptions{AssetIDs: []int64{assetX}}, nil)
			So(err, ShouldBeNil)
			So(data.AgentSources, ShouldNotBeNil)
			got := make([]int64, 0, len(data.AgentSources))
			for _, s := range data.AgentSources {
				got = append(got, s.ID)
			}
			So(got, ShouldContain, srcA)
			So(got, ShouldNotContain, srcB)
			So(got, ShouldNotContain, 0)
			So(len(got), ShouldEqual, 1)
		})

		Convey("全量导出包含全部来源（含未使用）", func() {
			data, err := Export(ctx, &ExportOptions{}, nil)
			So(err, ShouldBeNil)
			So(len(data.AgentSources), ShouldEqual, 3)
		})

		Convey("概览包含来源计数", func() {
			data, err := Export(ctx, &ExportOptions{}, nil)
			So(err, ShouldBeNil)
			So(data.Summary().AgentSourceCount, ShouldEqual, 3)
		})

		Convey("导出内容只有端点定义，绝不含身份/公钥/签名/payload", func() {
			data, err := Export(ctx, &ExportOptions{}, nil)
			So(err, ShouldBeNil)
			So(len(data.AgentSources), ShouldEqual, 3)
			forbidden := []string{"public_key", "private_key", "signature", "payload", "identities", "blob", "comment"}
			for _, s := range data.AgentSources {
				raw, err := json.Marshal(s)
				So(err, ShouldBeNil)
				var m map[string]json.RawMessage
				So(json.Unmarshal(raw, &m), ShouldBeNil)
				for _, key := range forbidden {
					_, present := m[key]
					So(present, ShouldBeFalse)
				}
				So(m, ShouldContainKey, "endpoint_type")
				So(m, ShouldContainKey, "endpoint")
			}
		})
	})
}

func TestImport_AgentSources_RoundTrip(t *testing.T) {
	Convey("来源+Agent 资产导入往返", t, func() {
		ctx1 := setupBackupTest(t)
		srcA := createAgentSource(t, ctx1, "A", "environment", "SSH_AUTH_SOCK")
		createAgentAsset(t, ctx1, "boxX", srcA)

		data, err := Export(ctx1, &ExportOptions{}, nil)
		So(err, ShouldBeNil)
		So(len(data.AgentSources), ShouldEqual, 1)

		ctx2 := setupBackupTest(t)
		// 预置既有来源，迫使导入来源获得新 ID，以证明旧来源 ID 被重映射
		existingID := createAgentSource(t, ctx2, "preexisting", "unix_socket", "/tmp/pre.sock")

		result, err := Import(ctx2, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
		So(err, ShouldBeNil)
		So(result.AgentSourcesImported, ShouldEqual, 1)

		sources, err := ssh_agent_source_repo.SSHAgentSource().List(ctx2)
		So(err, ShouldBeNil)
		So(len(sources), ShouldEqual, 2)
		var imported *ssh_agent_source_entity.SSHAgentSource
		for _, s := range sources {
			if s.ID != existingID {
				imported = s
			}
		}
		So(imported, ShouldNotBeNil)
		So(imported.ID, ShouldNotEqual, srcA)
		So(imported.Name, ShouldEqual, "A")
		So(imported.EndpointType, ShouldEqual, "environment")
		So(imported.Endpoint, ShouldEqual, "SSH_AUTH_SOCK")

		cfg := findAgentAssetConfig(t, ctx2, "boxX")
		So(cfg.AuthType, ShouldEqual, asset_entity.AuthTypeAgent)
		So(cfg.AgentSourceID, ShouldEqual, imported.ID) // 旧来源 ID 映射为新 ID
	})
}

func TestImport_AgentSources_Precheck(t *testing.T) {
	Convey("导入预检在写入前拒绝", t, func() {
		Convey("重复来源 ID", func() {
			ctx := setupBackupTest(t)
			src := func(id int64) *ssh_agent_source_entity.SSHAgentSource {
				return &ssh_agent_source_entity.SSHAgentSource{ID: id, Name: "x", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"}
			}
			data := &BackupData{AgentSources: []*ssh_agent_source_entity.SSHAgentSource{src(7), src(7)}}
			_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "重复")
			sources, e := ssh_agent_source_repo.SSHAgentSource().List(ctx)
			So(e, ShouldBeNil)
			So(sources, ShouldBeEmpty)
		})

		Convey("缺失来源引用", func() {
			ctx := setupBackupTest(t)
			asset := &asset_entity.Asset{Name: "orphan", Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive,
				Config: agentAssetConfig(99, testFingerprint())}
			data := &BackupData{Assets: []*asset_entity.Asset{asset}}
			_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "引用")
			assets, e := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
			So(e, ShouldBeNil)
			So(assets, ShouldBeEmpty)
		})

		Convey("畸形 Agent 字段：指纹非法", func() {
			ctx := setupBackupTest(t)
			src := &ssh_agent_source_entity.SSHAgentSource{ID: 1, Name: "x", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"}
			asset := &asset_entity.Asset{Name: "bad", Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive,
				Config: agentAssetConfig(1, "SHA256:not-a-valid-base64!")}
			data := &BackupData{AgentSources: []*ssh_agent_source_entity.SSHAgentSource{src}, Assets: []*asset_entity.Asset{asset}}
			_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "畸形")
		})

		Convey("畸形 Agent 字段：非 Agent 认证携带来源字段", func() {
			ctx := setupBackupTest(t)
			asset := &asset_entity.Asset{Name: "leak", Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive,
				Config: fmt.Sprintf(`{"host":"h","port":22,"username":"u","auth_type":"key","agent_source_id":1,"agent_key_fingerprint":%q}`, testFingerprint())}
			data := &BackupData{Assets: []*asset_entity.Asset{asset}}
			_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "畸形")
		})

		Convey("畸形来源字段：未知端点类型", func() {
			ctx := setupBackupTest(t)
			src := &ssh_agent_source_entity.SSHAgentSource{ID: 1, Name: "x", EndpointType: "banana", Endpoint: "whatever"}
			data := &BackupData{AgentSources: []*ssh_agent_source_entity.SSHAgentSource{src}}
			_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
			So(err, ShouldNotBeNil)
		})
	})
}

func TestImport_AgentSources_NoMergeByName(t *testing.T) {
	Convey("导入不按名称猜测合并", t, func() {
		ctx := setupBackupTest(t)
		// 已存在同名来源，端点不同
		existingID := createAgentSource(t, ctx, "work", "unix_socket", "/tmp/existing.sock")

		// 备份里同名来源（不同端点）+ 引用它的 Agent 资产
		src := &ssh_agent_source_entity.SSHAgentSource{ID: 50, Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"}
		asset := &asset_entity.Asset{Name: "newbox", Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive,
			Config: agentAssetConfig(50, testFingerprint())}
		data := &BackupData{AgentSources: []*ssh_agent_source_entity.SSHAgentSource{src}, Assets: []*asset_entity.Asset{asset}}

		_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
		So(err, ShouldBeNil)

		sources, e := ssh_agent_source_repo.SSHAgentSource().List(ctx)
		So(e, ShouldBeNil)
		So(len(sources), ShouldEqual, 2)
		// 既有来源原样保留
		var existing *ssh_agent_source_entity.SSHAgentSource
		for _, s := range sources {
			if s.ID == existingID {
				existing = s
			}
		}
		So(existing, ShouldNotBeNil)
		So(existing.Endpoint, ShouldEqual, "/tmp/existing.sock")
		// 新来源被创建（新 ID），资产引用新来源而不是按名称并入旧来源
		var newSource *ssh_agent_source_entity.SSHAgentSource
		for _, s := range sources {
			if s.ID != existingID {
				newSource = s
			}
		}
		So(newSource, ShouldNotBeNil)
		So(newSource.Endpoint, ShouldEqual, "SSH_AUTH_SOCK")
		cfg := findAgentAssetConfig(t, ctx, "newbox")
		So(cfg.AgentSourceID, ShouldEqual, newSource.ID)
		So(cfg.AgentSourceID, ShouldNotEqual, existingID)
	})
}

func TestImport_AgentSources_PlatformUnsupportedPreserved(t *testing.T) {
	Convey("平台不兼容来源照常保留", t, func() {
		ctx1 := setupBackupTest(t)
		// windows_named_pipe 在当前（非 Windows）平台结构合法但不支持
		createAgentSource(t, ctx1, "winpipe", "windows_named_pipe", `\\.\pipe\openssh-ssh-agent`)
		data, err := Export(ctx1, &ExportOptions{}, nil)
		So(err, ShouldBeNil)
		So(len(data.AgentSources), ShouldEqual, 1)

		ctx2 := setupBackupTest(t)
		_, err = Import(ctx2, data, &ImportOptions{ImportAssets: true, Mode: "replace"}, nil)
		So(err, ShouldBeNil)

		sources, e := ssh_agent_source_repo.SSHAgentSource().List(ctx2)
		So(e, ShouldBeNil)
		So(len(sources), ShouldEqual, 1)
		So(sources[0].EndpointType, ShouldEqual, "windows_named_pipe")
		So(sources[0].Endpoint, ShouldEqual, `\\.\pipe\openssh-ssh-agent`)
	})
}

// failingCrypto 注入 Encrypt 失败的依赖，用于验证事务回滚。
type failingCrypto struct{}

func (failingCrypto) Encrypt(string) (string, error) { return "", errors.New("encrypt failed") }
func (failingCrypto) Decrypt(string) (string, error) { return "", nil }

func TestImport_AgentSources_Rollback(t *testing.T) {
	Convey("任一失败回滚来源与资产写入", t, func() {
		Convey("事务中途失败时已写来源回滚", func() {
			ctx := setupBackupTest(t)
			src := &ssh_agent_source_entity.SSHAgentSource{ID: 1, Name: "x", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"}
			data := &BackupData{
				AgentSources: []*ssh_agent_source_entity.SSHAgentSource{src},
				Credentials:  []*BackupCredential{{PlainPassword: "secret"}},
			}
			_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, ImportCredentials: true, Mode: "merge"}, failingCrypto{})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "encrypt failed") // 失败发生在写入阶段而非预检
			sources, e := ssh_agent_source_repo.SSHAgentSource().List(ctx)
			So(e, ShouldBeNil)
			So(sources, ShouldBeEmpty)
			assets, e := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
			So(e, ShouldBeNil)
			So(assets, ShouldBeEmpty)
		})

		Convey("replace 模式下预检失败不清空既有数据", func() {
			ctx := setupBackupTest(t)
			srcA := createAgentSource(t, ctx, "A", "environment", "SSH_AUTH_SOCK")
			assetX := createAgentAsset(t, ctx, "boxX", srcA)

			dup := &ssh_agent_source_entity.SSHAgentSource{ID: 7, Name: "x", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"}
			data := &BackupData{AgentSources: []*ssh_agent_source_entity.SSHAgentSource{dup, {ID: 7, Name: "y", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"}}}

			_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "replace"}, nil)
			So(err, ShouldNotBeNil)

			assets, e := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
			So(e, ShouldBeNil)
			So(len(assets), ShouldEqual, 1)
			So(assets[0].ID, ShouldEqual, assetX)
			sources, e := ssh_agent_source_repo.SSHAgentSource().List(ctx)
			So(e, ShouldBeNil)
			So(len(sources), ShouldEqual, 1)
			So(sources[0].ID, ShouldEqual, srcA)
		})
	})
}
