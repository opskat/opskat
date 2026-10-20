package asset_entity

import "github.com/opskat/opskat/pkg/dbdriver"

// DatabaseDriverConfigValidator 校验数据库资产配置里由驱动决定的部分（必填字段、文件路径
// 规则等）。驱动无关的规则（查询超时范围、代理链）留在 validateDatabase。
type DatabaseDriverConfigValidator func(cfg *dbdriver.Config, route dbdriver.Route) error

// 各驱动的规则在驱动注册表（internal/dbdriver）里，而注册表依赖本包，只能反向接入；
// 与 RegisterConfigValidator 是同一个做法。
var databaseDriverConfigValidator DatabaseDriverConfigValidator

// SetDatabaseDriverConfigValidator 接入按驱动校验数据库配置的实现。只由驱动注册表在
// init() 里调用一次。
func SetDatabaseDriverConfigValidator(v DatabaseDriverConfigValidator) {
	databaseDriverConfigValidator = v
}

// 数据库资产配置与驱动契约（pkg/dbdriver）之间只在下面三处转换：驱动看到的配置、驱动改写后
// 写回、配置校验时的到达途径。

// DriverConfig 返回驱动看得到的那部分配置：口令、凭据与代理之外的全部字段。
func (c *DatabaseConfig) DriverConfig() *dbdriver.Config {
	return &dbdriver.Config{
		Driver:              c.Driver,
		Host:                c.Host,
		Port:                c.Port,
		Username:            c.Username,
		Database:            c.Database,
		SSLMode:             c.SSLMode,
		TLS:                 c.TLS,
		Params:              c.Params,
		ReadOnly:            c.ReadOnly,
		QueryTimeoutSeconds: c.QueryTimeoutSeconds,
		SSHAssetID:          c.SSHAssetID,
		SQLiteSource:        string(c.SQLiteSource),
		Path:                c.Path,
	}
}

// SetDriverConfig 把驱动改写过的配置写回。口令、凭据与代理不在驱动的配置里，保持原样。
func (c *DatabaseConfig) SetDriverConfig(d *dbdriver.Config) {
	c.Driver = d.Driver
	c.Host = d.Host
	c.Port = d.Port
	c.Username = d.Username
	c.Database = d.Database
	c.SSLMode = d.SSLMode
	c.TLS = d.TLS
	c.Params = d.Params
	c.ReadOnly = d.ReadOnly
	c.QueryTimeoutSeconds = d.QueryTimeoutSeconds
	c.SSHAssetID = d.SSHAssetID
	c.SQLiteSource = SQLiteSource(d.SQLiteSource)
	c.Path = d.Path
}

// databaseRoute 返回资产到达数据库的途径摘要，交给驱动的配置校验。
func (a *Asset) databaseRoute(cfg *DatabaseConfig) dbdriver.Route {
	return dbdriver.Route{
		SSHTunnelID: a.SSHTunnelID,
		Proxy:       cfg.Proxy != nil,
		ProxyChain:  cfg.ProxyChain != nil && len(cfg.ProxyChain.Layers) > 0,
	}
}
