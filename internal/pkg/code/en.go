package code

import "github.com/cago-frame/cago/pkg/i18n"

func init() {
	i18n.Register("en", enUS)
}

var enUS = map[int]string{
	OperationFailed:  "Operation failed",
	InvalidParameter: "Invalid parameter",
	NotFound:         "Resource not found",
	AssetNotFound:    "Asset not found",
	GroupNotFound:    "Group not found",
	InvalidAssetType: "Invalid asset type",
	AssetNotSSH:      "Asset is not SSH type",
	SSHConnectFailed: "SSH connection failed",
}
