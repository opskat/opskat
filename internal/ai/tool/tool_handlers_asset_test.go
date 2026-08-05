package tool

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestToSafeViewSerial 防止 SerialHandler.SafeView 返回的字段被丢掉。
// 之前 toSafeView 只接 host/port/username/database/redis/k8s 等几个 key，
// 串口资产对 AI 暴露时拿不到 port_path / baud_rate 等关键参数。
func TestToSafeViewSerial(t *testing.T) {
	asset := &asset_entity.Asset{
		ID:   42,
		Name: "console-1",
		Type: asset_entity.AssetTypeSerial,
	}
	require.NoError(t, asset.SetSerialConfig(&asset_entity.SerialConfig{
		PortPath:    "/dev/ttyUSB0",
		BaudRate:    115200,
		DataBits:    8,
		StopBits:    "1",
		Parity:      "none",
		FlowControl: "hardware",
	}))

	v := toSafeView(asset)

	assert.Equal(t, "/dev/ttyUSB0", v.PortPath)
	assert.Equal(t, 115200, v.BaudRate)
	assert.Equal(t, 8, v.DataBits)
	assert.Equal(t, "1", v.StopBits)
	assert.Equal(t, "none", v.Parity)
	assert.Equal(t, "hardware", v.FlowControl)
	// 串口没有 host/port/username 概念，确认没有被错误映射。
	assert.Empty(t, v.Host)
	assert.Equal(t, 0, v.Port)
	assert.Empty(t, v.Username)
}

// TestToSafeViewAgentSSH 覆盖 AI 资产安全视图对 Agent 认证 SSH 资产的对称返回：
// agent_source_id 与 agent_key_fingerprint 出现在安全视图里（规格允许，与桌面保存
// 校验一致），但端点/公钥/备注等敏感字段绝不出现——SSHConfig 本就不携带它们，
// SafeView 也不能把它们加进来。
func TestToSafeViewAgentSSH(t *testing.T) {
	asset := &asset_entity.Asset{
		ID:   1,
		Name: "box",
		Type: asset_entity.AssetTypeSSH,
	}
	require.NoError(t, asset.SetSSHConfig(&asset_entity.SSHConfig{
		Host: "10.0.0.1", Port: 22, Username: "root",
		AuthType:            "agent",
		AgentSourceID:       7,
		AgentKeyFingerprint: "SHA256:abc",
	}))

	v := toSafeView(asset)

	assert.Equal(t, "agent", v.AuthType)
	assert.Equal(t, int64(7), v.AgentSourceID)
	assert.Equal(t, "SHA256:abc", v.AgentKeyFingerprint)

	// 安全视图绝不包含端点/公钥/备注/签名/挑战答案：连空字段都不该出现，
	// 避免未来有人把敏感值填进来。
	data, err := json.Marshal(v)
	require.NoError(t, err)
	for _, banned := range []string{"agent_source_endpoint", "agent_public_key", "agent_comment", "agent_signature", "agent_challenge_answers"} {
		assert.NotContains(t, string(data), banned)
	}
}
