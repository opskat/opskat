package code

import "github.com/cago-frame/cago/pkg/i18n"

func init() {
	i18n.Register("zh-cn", zhCN)
}

var zhCN = map[int]string{
	OperationFailed:  "操作失败",
	InvalidParameter: "参数错误",
	NotFound:         "资源不存在",
	AssetNotFound:    "资产不存在",
	GroupNotFound:    "分组不存在",
	InvalidAssetType: "无效的资产类型",
	AssetNotSSH:      "资产不是SSH类型",
	SSHConnectFailed: "SSH连接失败",
}
