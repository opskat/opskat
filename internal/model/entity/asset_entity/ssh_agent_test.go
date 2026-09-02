package asset_entity

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validAgentFingerprint 返回一条符合规范格式的 SHA256 指纹：大写 SHA256: 前缀、
// 32 字节摘要、base64 无填充、重编码与输入一致。
func validAgentFingerprint() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(raw)
}

func agentAsset(t *testing.T, cfg *SSHConfig) *Asset {
	t.Helper()
	a := &Asset{Name: "box", Type: AssetTypeSSH}
	require.NoError(t, a.SetSSHConfig(cfg))
	return a
}

func TestValidateFingerprintSHA256(t *testing.T) {
	convey.Convey("规范 SHA256 指纹校验", t, func() {
		convey.Convey("规范指纹通过", func() {
			assert.NoError(t, ValidateFingerprintSHA256(validAgentFingerprint()))
		})

		convey.Convey("前缀必须精确为大写 SHA256:", func() {
			assert.Error(t, ValidateFingerprintSHA256("sha256:"+validAgentFingerprint()[7:]))
			assert.Error(t, ValidateFingerprintSHA256("SHA256x"+validAgentFingerprint()[6:]))
			assert.Error(t, ValidateFingerprintSHA256(validAgentFingerprint()[7:])) // 无前缀
		})

		convey.Convey("首尾空白被拒绝", func() {
			assert.Error(t, ValidateFingerprintSHA256(" "+validAgentFingerprint()))
			assert.Error(t, ValidateFingerprintSHA256(validAgentFingerprint()+"\n"))
		})

		convey.Convey("base64 带填充被拒绝", func() {
			assert.Error(t, ValidateFingerprintSHA256(validAgentFingerprint()+"="))
		})

		convey.Convey("解码后必须恰好 32 字节", func() {
			short := base64.RawStdEncoding.EncodeToString(make([]byte, 31))
			long := base64.RawStdEncoding.EncodeToString(make([]byte, 33))
			assert.Error(t, ValidateFingerprintSHA256("SHA256:"+short))
			assert.Error(t, ValidateFingerprintSHA256("SHA256:"+long))
		})

		convey.Convey("重编码必须与输入完全相同", func() {
			// 32 字节 → 43 个 base64 字符，最后一个字符低 2 位是填充位。把规范字符
			// 换成同高 4 位、低 2 位非 0 的变体：解码得到相同字节，但重编码回到规范
			// 字符 → 与输入不一致 → 被重编码检查拒绝。
			raw := make([]byte, 32)
			for i := range raw {
				raw[i] = byte(i)
			}
			const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
			enc := base64.RawStdEncoding.EncodeToString(raw)
			last := enc[len(enc)-1]
			variant := table[strings.IndexByte(table, last)+1]
			bad := "SHA256:" + enc[:len(enc)-1] + string(variant)
			assert.Error(t, ValidateFingerprintSHA256(bad))
		})

		convey.Convey("空字符串被拒绝", func() {
			assert.Error(t, ValidateFingerprintSHA256(""))
		})
	})
}

func TestAsset_ValidateSSHAgentContract(t *testing.T) {
	convey.Convey("SSH Agent 认证契约（权威校验）", t, func() {
		convey.Convey("合法 Agent 配置通过", func() {
			a := agentAsset(t, &SSHConfig{
				Host: "h", Port: 22, Username: "u",
				AuthType: AuthTypeAgent, AgentSourceID: 7, AgentKeyFingerprint: validAgentFingerprint(),
			})
			assert.NoError(t, a.Validate())
		})

		convey.Convey("agent_source_id 必须为正数", func() {
			a := agentAsset(t, &SSHConfig{
				Host: "h", Port: 22, Username: "u",
				AuthType: AuthTypeAgent, AgentSourceID: 0, AgentKeyFingerprint: validAgentFingerprint(),
			})
			err := a.Validate()
			assert.Error(t, err)

			a = agentAsset(t, &SSHConfig{
				Host: "h", Port: 22, Username: "u",
				AuthType: AuthTypeAgent, AgentSourceID: -1, AgentKeyFingerprint: validAgentFingerprint(),
			})
			assert.Error(t, a.Validate())
		})

		convey.Convey("agent_key_fingerprint 必须规范", func() {
			a := agentAsset(t, &SSHConfig{
				Host: "h", Port: 22, Username: "u",
				AuthType: AuthTypeAgent, AgentSourceID: 7, AgentKeyFingerprint: "sha256:bad",
			})
			assert.Error(t, a.Validate())
		})

		convey.Convey("Agent 字段与托管凭据互斥", func() {
			base := &SSHConfig{Host: "h", Port: 22, Username: "u", AuthType: AuthTypeAgent, AgentSourceID: 7, AgentKeyFingerprint: validAgentFingerprint()}
			withCredential := *base
			withCredential.CredentialID = 1
			assert.Error(t, agentAsset(t, &withCredential).Validate())

			withPassword := *base
			withPassword.Password = "encrypted"
			assert.Error(t, agentAsset(t, &withPassword).Validate())

			withKeys := *base
			withKeys.PrivateKeys = []string{"/tmp/key"}
			assert.Error(t, agentAsset(t, &withKeys).Validate())

			withPassphrase := *base
			withPassphrase.PrivateKeyPassphrase = "pp"
			assert.Error(t, agentAsset(t, &withPassphrase).Validate())
		})

		convey.Convey("非 Agent 认证方式不得携带 Agent 来源字段", func() {
			cfg := &SSHConfig{Host: "h", Port: 22, Username: "u", AuthType: AuthTypePassword,
				AgentSourceID: 7, AgentKeyFingerprint: validAgentFingerprint()}
			assert.Error(t, agentAsset(t, cfg).Validate())
		})

		convey.Convey("启用 Agent 转发必须指定来源", func() {
			a := agentAsset(t, &SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: AuthTypePassword,
				AgentForwarding: true,
			})
			assert.Error(t, a.Validate())
		})

		convey.Convey("关闭 Agent 转发不得残留来源", func() {
			a := agentAsset(t, &SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: AuthTypePassword,
				AgentForwardSourceID: 7,
			})
			assert.Error(t, a.Validate())
		})
	})
}

func TestCheckSSHConfigAgentWriteBoundary(t *testing.T) {
	convey.Convey("SSH Agent 字段 JSON 写入边界", t, func() {
		convey.Convey("重复 auth_type key 被拒绝", func() {
			raw := `{"host":"h","port":22,"username":"u","auth_type":"password","auth_type":"agent"}`
			err := CheckSSHConfigAgentWriteBoundary(raw)
			assert.Error(t, err)
			code, ok := AgentConfigCodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, CodeAgentConfigDuplicateKey, code)
		})

		convey.Convey("重复 agent_source_id key 被拒绝", func() {
			raw := `{"host":"h","auth_type":"agent","agent_source_id":1,"agent_source_id":2}`
			assert.Error(t, CheckSSHConfigAgentWriteBoundary(raw))
		})

		convey.Convey("非规范拼写被拒绝（Go 大小写不敏感读取、SQLite 大小写敏感）", func() {
			for _, raw := range []string{
				`{"host":"h","authType":"agent"}`,
				`{"host":"h","AUTH_TYPE":"agent"}`,
				`{"host":"h","agentSourceId":1}`,
				`{"host":"h","agent-source-id":1}`,
				`{"host":"h","AgentKeyFingerprint":"SHA256:abc"}`,
				`{"host":"h","agentForwarding":true}`,
				`{"host":"h","agentForwardSourceId":1}`,
			} {
				err := CheckSSHConfigAgentWriteBoundary(raw)
				assert.Error(t, err, "raw=%s", raw)
				code, ok := AgentConfigCodeOf(err)
				assert.True(t, ok)
				assert.Equal(t, CodeAgentConfigNoncanonicalKey, code)
			}
		})

		convey.Convey("规范拼写与无关字段通过", func() {
			raw := `{"host":"h","port":22,"username":"u","auth_type":"agent","agent_source_id":7,"agent_key_fingerprint":"SHA256:abc","password":"x"}`
			assert.NoError(t, CheckSSHConfigAgentWriteBoundary(raw))
		})

		convey.Convey("非 SSH 的无关 JSON 通过（不检查其它字段）", func() {
			assert.NoError(t, CheckSSHConfigAgentWriteBoundary(`{"host":"h","port":22}`))
			assert.NoError(t, CheckSSHConfigAgentWriteBoundary(`[]`))
			assert.NoError(t, CheckSSHConfigAgentWriteBoundary(""))
		})

		convey.Convey("嵌套同名 key 不误报（只查顶层）", func() {
			raw := `{"host":"h","proxy":{"auth_type":"agent","agent_source_id":3},"auth_type":"password"}`
			assert.NoError(t, CheckSSHConfigAgentWriteBoundary(raw))
		})
	})
}
