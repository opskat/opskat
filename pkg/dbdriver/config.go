package dbdriver

// DriverID 是驱动的 id，也是存进数据库资产配置 driver 字段的值；改名即数据迁移。
type DriverID string

// Config 是驱动看得到的数据库资产配置：资产配置里除口令、凭据与代理之外的全部字段。
// 口令由宿主解析后放进 OpenRequest.Password；隧道、代理与代理链由宿主解析成
// OpenRequest.Transport，配置校验时以 Route 告知。
//
// JSON 键与资产配置的同名字段一致（也是 FormField 的取值）。
type Config struct {
	Driver   DriverID `json:"driver"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	// Database 是默认数据库；调用方可能已经用本次操作指定的库名覆盖了它。
	Database string `json:"database,omitempty"`
	SSLMode  string `json:"ssl_mode,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
	// Params 是额外的连接参数。
	Params string `json:"params,omitempty"`
	// ReadOnly 要求连接级只读。
	ReadOnly bool `json:"read_only,omitempty"`
	// QueryTimeoutSeconds 是查询超时（秒），0 为默认值。
	QueryTimeoutSeconds int `json:"query_timeout_seconds,omitempty"`
	// SSHAssetID 是配置里的 SSH 资产：网络驱动的 SSH 隧道（宿主已解析进 OpenRequest.Transport），
	// 或数据库文件所在的主机（如远端 SQLite）。它归宿主，不交给扩展提供的驱动。
	SSHAssetID int64 `json:"ssh_asset_id,omitempty"`
	// SQLiteSource 是 SQLite 文件来源。
	SQLiteSource string `json:"sqlite_source,omitempty"`
	// Path 是数据库文件的路径。
	Path string `json:"path,omitempty"`
}

// Route 是资产到达数据库的途径配置的摘要，供 ConfigRules.ValidateConfig 判断驱动支不支持。
// 途径本身（隧道、代理的地址与凭据）只有宿主知道。
type Route struct {
	// SSHTunnelID 是资产级的 SSH 隧道，0 表示没有。
	SSHTunnelID int64 `json:"ssh_tunnel_id,omitempty"`
	// Proxy 报告配置了 SOCKS5 代理。
	Proxy bool `json:"proxy,omitempty"`
	// ProxyChain 报告配置了至少一层的代理链。
	ProxyChain bool `json:"proxy_chain,omitempty"`
}
