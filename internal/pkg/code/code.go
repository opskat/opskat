package code

// 业务错误码
const (
	OperationFailed  = iota + 10000 // 操作失败
	InvalidParameter                // 参数错误
	NotFound                        // 资源不存在
	AssetNotFound                   // 资产不存在
	GroupNotFound                   // 分组不存在
	InvalidAssetType                // 无效的资产类型
	AssetNotSSH                     // 资产不是SSH类型
	SSHConnectFailed                // SSH连接失败
)
