package import_svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	. "github.com/smartystreets/goconvey/convey"
)

type failingListGroupRepo struct {
	group_repo.GroupRepo
}

func (f *failingListGroupRepo) List(context.Context) ([]*group_entity.Group, error) {
	return nil, errors.New("group repository unavailable")
}

func TestImportSSHConfigDoesNotDependOnGroups(t *testing.T) {
	assetRepo := &windTermAssetRepo{}
	originalAssetRepo := asset_repo.Asset()
	originalGroupRepo := group_repo.Group()
	asset_repo.RegisterAsset(assetRepo)
	group_repo.RegisterGroup(&failingListGroupRepo{})
	t.Cleanup(func() {
		asset_repo.RegisterAsset(originalAssetRepo)
		group_repo.RegisterGroup(originalGroupRepo)
	})

	data := []byte("Host prod\n  HostName prod.example.com\n  User deploy\n")
	result, err := ImportSSHConfigSelected(context.Background(), data, []int{0}, ImportOptions{})

	if err != nil {
		t.Fatalf("SSH Config import should not depend on groups: %v", err)
	}
	if result.Success != 1 {
		t.Fatalf("expected one imported asset, got %+v", result)
	}
	if len(assetRepo.assets) != 1 || assetRepo.assets[0].GroupID != 0 {
		t.Fatalf("expected imported asset in root group, got %+v", assetRepo.assets)
	}
}

func TestParseSSHConfig(t *testing.T) {
	Convey("parseSSHConfig", t, func() {
		Convey("基本解析", func() {
			config := `
Host myserver
    HostName 192.168.1.100
    Port 2222
    User admin
    IdentityFile ~/.ssh/id_rsa

Host webserver
    HostName 10.0.0.1
    User root
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 2)

			So(hosts[0].alias, ShouldEqual, "myserver")
			So(hosts[0].hostName, ShouldEqual, "192.168.1.100")
			So(hosts[0].port, ShouldEqual, 2222)
			So(hosts[0].user, ShouldEqual, "admin")
			So(hosts[0].identityFiles, ShouldResemble, []string{"~/.ssh/id_rsa"})

			So(hosts[1].alias, ShouldEqual, "webserver")
			So(hosts[1].hostName, ShouldEqual, "10.0.0.1")
			So(hosts[1].user, ShouldEqual, "root")
			So(hosts[1].port, ShouldEqual, 0)
		})

		Convey("跳过通配符 Host", func() {
			config := `
Host *
    ServerAliveInterval 60

Host prod
    HostName prod.example.com
    User deploy
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].alias, ShouldEqual, "prod")
		})

		Convey("跳过没有 HostName 的条目", func() {
			config := `
Host alias-only
    User test

Host real
    HostName 1.2.3.4
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].alias, ShouldEqual, "real")
		})

		Convey("ProxyJump 解析", func() {
			config := `
Host jump
    HostName 10.0.0.1
    User admin

Host target
    HostName 10.0.0.2
    User root
    ProxyJump jump
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 2)
			So(hosts[1].proxyJump, ShouldEqual, "jump")
		})

		Convey("等号分隔格式", func() {
			config := `
Host eqserver
    HostName=192.168.1.1
    Port=22
    User=root
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].hostName, ShouldEqual, "192.168.1.1")
			So(hosts[0].port, ShouldEqual, 22)
			So(hosts[0].user, ShouldEqual, "root")
		})

		Convey("注释和空行", func() {
			config := `
# This is a comment
Host server1
    HostName 1.1.1.1
    # inline comment
    User test

`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].hostName, ShouldEqual, "1.1.1.1")
		})

		Convey("多个 IdentityFile", func() {
			config := `
Host multi-key
    HostName 10.0.0.1
    User deploy
    IdentityFile ~/.ssh/id_ed25519
    IdentityFile ~/.ssh/id_rsa
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].identityFiles, ShouldResemble, []string{"~/.ssh/id_ed25519", "~/.ssh/id_rsa"})
		})

		Convey("ProxyJump 多跳只取第一跳", func() {
			config := `
Host target
    HostName 10.0.0.3
    ProxyJump jump1,jump2,jump3
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].proxyJump, ShouldEqual, "jump1")
		})

		Convey("Tab 缩进", func() {
			config := "Host tabserver\n\tHostName 172.16.0.1\n\tPort 2222\n\tUser ops\n"
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].hostName, ShouldEqual, "172.16.0.1")
			So(hosts[0].port, ShouldEqual, 2222)
			So(hosts[0].user, ShouldEqual, "ops")
		})

		Convey("指令大小写不敏感", func() {
			config := `
Host caseless
    HOSTNAME 10.0.0.1
    PORT 22
    USER admin
    IDENTITYFILE ~/.ssh/id_rsa
    PROXYJUMP bastion
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].hostName, ShouldEqual, "10.0.0.1")
			So(hosts[0].port, ShouldEqual, 22)
			So(hosts[0].user, ShouldEqual, "admin")
			So(hosts[0].identityFiles, ShouldResemble, []string{"~/.ssh/id_rsa"})
			So(hosts[0].proxyJump, ShouldEqual, "bastion")
		})

		Convey("Host 多别名取第一个", func() {
			config := `
Host alias1 alias2 alias3
    HostName 10.0.0.1
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].alias, ShouldEqual, "alias1")
		})

		Convey("空配置", func() {
			hosts := parseSSHConfig("")
			So(hosts, ShouldBeEmpty)
		})

		Convey("仅注释", func() {
			config := `
# comment 1
# comment 2
`
			hosts := parseSSHConfig(config)
			So(hosts, ShouldBeEmpty)
		})

		Convey("等号带空格格式", func() {
			config := `
Host eqspace
    HostName = 192.168.1.1
    Port = 2222
    User = admin
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].hostName, ShouldEqual, "192.168.1.1")
			So(hosts[0].port, ShouldEqual, 2222)
			So(hosts[0].user, ShouldEqual, "admin")
		})

		Convey("跳过问号通配符", func() {
			config := `
Host server?
    HostName 10.0.0.1

Host real
    HostName 10.0.0.2
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].alias, ShouldEqual, "real")
		})

		Convey("没有 IdentityFile 时列表为 nil", func() {
			config := `
Host nokey
    HostName 10.0.0.1
    User root
`
			hosts := parseSSHConfig(config)
			So(len(hosts), ShouldEqual, 1)
			So(hosts[0].identityFiles, ShouldBeNil)
		})
	})
}

func TestSplitDirective(t *testing.T) {
	Convey("splitDirective", t, func() {
		Convey("空格分隔", func() {
			key, value := splitDirective("HostName 192.168.1.1")
			So(key, ShouldEqual, "HostName")
			So(value, ShouldEqual, "192.168.1.1")
		})

		Convey("等号分隔", func() {
			key, value := splitDirective("HostName=192.168.1.1")
			So(key, ShouldEqual, "HostName")
			So(value, ShouldEqual, "192.168.1.1")
		})

		Convey("等号带空格", func() {
			key, value := splitDirective("HostName = 192.168.1.1")
			So(key, ShouldEqual, "HostName")
			So(value, ShouldEqual, "192.168.1.1")
		})

		Convey("Tab 分隔", func() {
			key, value := splitDirective("HostName\t192.168.1.1")
			So(key, ShouldEqual, "HostName")
			So(value, ShouldEqual, "192.168.1.1")
		})

		Convey("值中包含等号的路径", func() {
			key, value := splitDirective("IdentityFile /path/with=equals/id_rsa")
			So(key, ShouldEqual, "IdentityFile")
			So(value, ShouldEqual, "/path/with=equals/id_rsa")
		})

		Convey("多个空格分隔", func() {
			key, value := splitDirective("Host    myserver")
			So(key, ShouldEqual, "Host")
			So(value, ShouldEqual, "myserver")
		})

		Convey("仅关键字无值", func() {
			key, value := splitDirective("OnlyKey")
			So(key, ShouldEqual, "")
			So(value, ShouldEqual, "")
		})

		Convey("空字符串", func() {
			key, value := splitDirective("")
			So(key, ShouldEqual, "")
			So(value, ShouldEqual, "")
		})

		Convey("值包含空格", func() {
			key, value := splitDirective("Host my server alias")
			So(key, ShouldEqual, "Host")
			So(value, ShouldEqual, "my server alias")
		})
	})
}

func TestExpandPath(t *testing.T) {
	Convey("expandPath", t, func() {
		Convey("波浪号展开", func() {
			result := expandPath("~/test/path")
			So(result, ShouldNotEqual, "~/test/path")
			home, err := os.UserHomeDir()
			So(err, ShouldBeNil)
			So(result, ShouldEqual, filepath.Join(home, "test", "path"))
		})

		Convey("绝对路径不变", func() {
			result := expandPath("/etc/ssh/id_rsa")
			So(result, ShouldEqual, "/etc/ssh/id_rsa")
		})

		Convey("相对路径不变", func() {
			result := expandPath("relative/path")
			So(result, ShouldEqual, "relative/path")
		})

		Convey("仅波浪号", func() {
			result := expandPath("~")
			home, err := os.UserHomeDir()
			So(err, ShouldBeNil)
			So(result, ShouldEqual, home)
		})
	})
}

func TestMapAuthType(t *testing.T) {
	Convey("mapAuthType", t, func() {
		Convey("publickey → key", func() {
			So(mapAuthType("publickey"), ShouldEqual, "key")
		})

		Convey("大写 PUBLICKEY → key", func() {
			So(mapAuthType("PUBLICKEY"), ShouldEqual, "key")
		})

		Convey("password → password", func() {
			So(mapAuthType("password"), ShouldEqual, "password")
		})

		Convey("未知类型默认 password", func() {
			So(mapAuthType(""), ShouldEqual, "password")
			So(mapAuthType("unknown"), ShouldEqual, "password")
		})
	})
}
