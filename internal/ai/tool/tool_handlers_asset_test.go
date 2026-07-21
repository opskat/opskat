package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestResolveAssetForBatch_NumericStringRef 锁住 batch_command 唯一走得通的解析路径。
//
// batchCommandItem.Asset 是 string（tool_handler_batch.go:17），resolveAssetForBatch
// 把它包成 {"id": "<string>"} 交给 handleGetAsset，而 handleGetAsset 只有
// aictx.ArgInt64(args, "id") 这一条路。ArgInt64 补上 string 分支之前，这里**恒为 0**，
// 于是每一项都返回 "missing required parameter: id" —— batch_command 整个工具不可用；
// 补上之后它才真的会去连主机执行命令。这条从"完全不工作"变成"真的执行远程命令"的路径
// 此前没有任何测试，这里把它钉住。
func TestResolveAssetForBatch_NumericStringRef(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().Find(gomock.Any(), int64(7)).
		Return(&asset_entity.Asset{ID: 7, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil)

	id, name, err := resolveAssetForBatch(context.Background(), "7")
	require.NoError(t, err)
	assert.Equal(t, int64(7), id)
	assert.Equal(t, "web-1", name)
}

// TestResolveAssetForBatch_NameRefDoesNotResolve 把**当前**行为钉成契约，而不是把它
// 当成期望行为：batch_command 的参数描述允许按名字指定资产，但 handleGetAsset 里没有
// 任何 name 查询，所以按名字指定今天一定失败。这是一个已知的用户可见缺陷（见本任务报告），
// 修它要改 get_asset 工具的契约，超出本 plan 的范围。这条测试的作用是：哪天有人加了
// name 解析，它会失败，从而强制那个人同时更新 batch_command 的文档与 get_asset 的契约。
func TestResolveAssetForBatch_NameRefDoesNotResolve(t *testing.T) {
	setupUnified(t)

	_, _, err := resolveAssetForBatch(context.Background(), "web-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required parameter: id")
}

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
