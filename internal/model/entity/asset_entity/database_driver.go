package asset_entity

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// AuthMethodKerberos 是 DatabaseConfig.AuthMethod 选 Kerberos 的取值；空表示口令。
const AuthMethodKerberos = dbdriver.AuthMethodKerberos

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
		AuthMethod:          c.AuthMethod,
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
	c.AuthMethod = d.AuthMethod
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

// ValidateKerberos 校验 Kerberos 设置：认证方式为 Kerberos 时，主体须带 @REALM、至少一个 KDC 地址、
// 口令与 keytab 至少填一个，固定的服务主体若带 realm 须与主体的一致；错误点名字段。认证方式为口令
// 时不看 krb_* 字段（保留着，切回 Kerberos 时还在）。
func (c *DatabaseConfig) ValidateKerberos() error {
	switch c.AuthMethod {
	case "":
		return nil
	case AuthMethodKerberos:
	default:
		return fmt.Errorf("auth_method 无效: %q", c.AuthMethod)
	}
	if _, err := kerberosRealm(c.KrbPrincipal); err != nil {
		return err
	}
	if len(c.KerberosKDCs()) == 0 {
		return errors.New("krb_kdc 不能为空：需要至少一个 KDC 地址")
	}
	if c.KrbPassword == "" && c.KrbKeytabFile == "" {
		return errors.New("krb_password 与 krb_keytab_file 至少填一个")
	}
	_, err := c.KerberosPinnedSPN()
	return err
}

// KerberosKDCs 是 krb_kdc 拆出的 KDC 地址：与桌面端表单一样按逗号、分号或空白分隔，丢弃空项。
func (c *DatabaseConfig) KerberosKDCs() []string {
	return strings.FieldsFunc(c.KrbKDC, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
}

// KerberosPinnedSPN 返回固定的服务主体，去掉与主体同 realm 的 @REALM（宿主按 service/host 原样比较）；
// 没固定返回空。不是 service/host[@REALM] 形式（dbdriver.CheckKerberosSPN），或 realm 与主体的不同，
// 报错点名 krb_spn。
func (c *DatabaseConfig) KerberosPinnedSPN() (string, error) {
	if c.KrbSPN == "" {
		return "", nil
	}
	spn, realm, hasRealm := strings.Cut(c.KrbSPN, "@")
	if strings.Contains(realm, "@") {
		return "", errors.New("krb_spn 无效：应为 service/host 或 service/host@REALM")
	}
	if err := dbdriver.CheckKerberosSPN(spn); err != nil {
		return "", fmt.Errorf("krb_spn 无效: %w", err)
	}
	if !hasRealm {
		return spn, nil
	}
	want, err := kerberosRealm(c.KrbPrincipal)
	if err != nil {
		return "", err
	}
	if realm != want {
		return "", fmt.Errorf("krb_spn 的 realm %q 与主体的 realm %q 不同", realm, want)
	}
	return spn, nil
}

func kerberosRealm(principal string) (string, error) {
	i := strings.LastIndex(principal, "@")
	if i <= 0 || i == len(principal)-1 {
		return "", errors.New("krb_principal 须为 user@REALM")
	}
	return principal[i+1:], nil
}
