package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/smartystreets/goconvey/convey"
)

func TestSSHHandler(t *testing.T) {
	convey.Convey("SSH Handler", t, func() {
		h := &sshHandler{}
		convey.Convey("SafeView", func() {
			a := &asset_entity.Asset{Type: "ssh", Status: 1}
			_ = a.SetSSHConfig(&asset_entity.SSHConfig{
				Host: "10.0.0.1", Port: 22, Username: "root",
				AuthType: "password", Password: "secret",
			})
			view := h.SafeView(a)
			convey.So(view["host"], convey.ShouldEqual, "10.0.0.1")
			convey.So(view["port"], convey.ShouldEqual, 22)
			convey.So(view["username"], convey.ShouldEqual, "root")
			convey.So(view["auth_type"], convey.ShouldEqual, "password")
			_, hasPassword := view["password"]
			convey.So(hasPassword, convey.ShouldBeFalse)
		})

		convey.Convey("SafeView Agent 资产返回来源 ID 与指纹，且不含任何端点/公钥/备注键", func() {
			a := &asset_entity.Asset{Type: "ssh", Status: 1}
			_ = a.SetSSHConfig(&asset_entity.SSHConfig{
				Host: "10.0.0.1", Port: 22, Username: "root",
				AuthType: "agent", AgentSourceID: 7, AgentKeyFingerprint: "SHA256:abc",
			})
			view := h.SafeView(a)
			convey.So(view["host"], convey.ShouldEqual, "10.0.0.1")
			convey.So(view["port"], convey.ShouldEqual, 22)
			convey.So(view["username"], convey.ShouldEqual, "root")
			convey.So(view["auth_type"], convey.ShouldEqual, "agent")
			// AI 安全视图对 Agent 资产对称返回来源 ID 与指纹（规格允许），
			// 但绝不包含来源端点、身份公钥、身份备注、签名或挑战答案。
			convey.So(view["agent_source_id"], convey.ShouldEqual, int64(7))
			convey.So(view["agent_key_fingerprint"], convey.ShouldEqual, "SHA256:abc")
			for _, banned := range []string{"agent_source_endpoint", "agent_public_key", "agent_comment", "agent_signature", "agent_challenge_answers"} {
				_, has := view[banned]
				convey.So(has, convey.ShouldBeFalse)
			}
		})
		convey.Convey("ApplyCreateArgs", func() {
			a := &asset_entity.Asset{Type: "ssh"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"host": "10.0.0.1", "port": float64(22), "username": "root",
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetSSHConfig()
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.1")
			convey.So(cfg.Port, convey.ShouldEqual, 22)
			convey.So(cfg.AuthType, convey.ShouldEqual, "password") // default
		})
		convey.Convey("ApplyUpdateArgs", func() {
			a := &asset_entity.Asset{Type: "ssh"}
			_ = a.SetSSHConfig(&asset_entity.SSHConfig{
				Host: "10.0.0.1", Port: 22, Username: "root", AuthType: "password",
			})
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{"host": "10.0.0.2"})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetSSHConfig()
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.2")
			convey.So(cfg.Port, convey.ShouldEqual, 22)
			convey.So(cfg.Username, convey.ShouldEqual, "root")
		})
	})
}
