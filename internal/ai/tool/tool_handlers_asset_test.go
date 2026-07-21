package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestResolveAssetForBatch_NumericStringRef 锁住数字 id 这条解析路径。
//
// batchCommandItem.Asset 是 string（tool_handler_batch.go:17），所以数字 id 到这里
// 也是字符串形态。assetref.Resolve 对数字 ref 会**同时**按名称和按 id 查（名称列允许
// 纯数字且无唯一索引），因此 FindByName 必须一并 mock，否则 gomock 报 unexpected call。
func TestResolveAssetForBatch_NumericStringRef(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(7)).
		Return(&asset_entity.Asset{ID: 7, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil)

	id, name, err := resolveAssetForBatch(context.Background(), "7")
	require.NoError(t, err)
	assert.Equal(t, int64(7), id)
	assert.Equal(t, "web-1", name)
}

// TestResolveAssetForBatch_NameRef 钉住按名字寻址：batch_command 的参数描述写的是
// {"asset": "name-or-id"}，示例里直接给了 {"asset":"web-1"}，所以名字必须真的能解析。
// 此前它经 handleGetAsset 走，而 handleGetAsset 只认数字 id，名字必然报
// "missing required parameter: id" —— 文档承诺的形态整个不可用。
func TestResolveAssetForBatch_NameRef(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "web-1").
		Return([]*asset_entity.Asset{{ID: 7, Name: "web-1", Type: asset_entity.AssetTypeSSH}}, nil)

	id, name, err := resolveAssetForBatch(context.Background(), "web-1")
	require.NoError(t, err)
	assert.Equal(t, int64(7), id)
	assert.Equal(t, "web-1", name)
}

// TestResolveAssetForBatch_AmbiguousNameRef 钉住歧义必须报错：同名资产多于一个时
// 静默取第一个会让批量命令打到错误的机器上，而批量正是最不该猜的场景。
func TestResolveAssetForBatch_AmbiguousNameRef(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "web").
		Return([]*asset_entity.Asset{
			{ID: 7, Name: "web", Type: asset_entity.AssetTypeSSH},
			{ID: 9, Name: "web", Type: asset_entity.AssetTypeSSH},
		}, nil)

	_, _, err := resolveAssetForBatch(context.Background(), "web")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
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
