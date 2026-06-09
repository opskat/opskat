package import_svc

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPreserveSSHSecretsOnOverwrite(t *testing.T) {
	Convey("覆盖导入时补齐缺失的敏感字段", t, func() {
		Convey("新导入不含任何认证材料时，整体保留旧凭证与认证方式", func() {
			oldCfg := &asset_entity.SSHConfig{
				AuthType: asset_entity.AuthTypeKey, CredentialID: 42,
				PrivateKeys: []string{"/home/u/.ssh/id_rsa"}, PrivateKeyPassphrase: "enc-pass",
			}
			newCfg := &asset_entity.SSHConfig{AuthType: asset_entity.AuthTypePassword}

			preserveSSHSecretsOnOverwrite(oldCfg, newCfg)

			So(newCfg.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(newCfg.CredentialID, ShouldEqual, 42)
			So(newCfg.PrivateKeys, ShouldResemble, []string{"/home/u/.ssh/id_rsa"})
			So(newCfg.PrivateKeyPassphrase, ShouldEqual, "enc-pass")
		})

		Convey("新导入自带密钥时，保留旧密码但不复活旧统一凭证", func() {
			oldCfg := &asset_entity.SSHConfig{
				AuthType: asset_entity.AuthTypePassword, Password: "old-pass", CredentialID: 99,
			}
			newCfg := &asset_entity.SSHConfig{AuthType: asset_entity.AuthTypeKey, PrivateKeys: []string{"/k/new"}}

			preserveSSHSecretsOnOverwrite(oldCfg, newCfg)

			So(newCfg.AuthType, ShouldEqual, asset_entity.AuthTypeKey)
			So(newCfg.PrivateKeys, ShouldResemble, []string{"/k/new"})
			So(newCfg.Password, ShouldEqual, "old-pass") // 旧密码补齐
			So(newCfg.CredentialID, ShouldEqual, 0)      // 不遮蔽新密钥
		})

		Convey("新导入自带密码时以新密码为准，不复活旧统一凭证", func() {
			oldCfg := &asset_entity.SSHConfig{AuthType: asset_entity.AuthTypePassword, Password: "old", CredentialID: 7}
			newCfg := &asset_entity.SSHConfig{AuthType: asset_entity.AuthTypePassword, Password: "new"}

			preserveSSHSecretsOnOverwrite(oldCfg, newCfg)

			So(newCfg.Password, ShouldEqual, "new")
			So(newCfg.CredentialID, ShouldEqual, 0)
		})

		Convey("新导入缺失 passphrase 时补齐", func() {
			oldCfg := &asset_entity.SSHConfig{PrivateKeyPassphrase: "old-pp"}
			newCfg := &asset_entity.SSHConfig{AuthType: asset_entity.AuthTypeKey, PrivateKeys: []string{"/k"}}

			preserveSSHSecretsOnOverwrite(oldCfg, newCfg)

			So(newCfg.PrivateKeyPassphrase, ShouldEqual, "old-pp")
		})
	})
}
