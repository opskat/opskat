package dbdriver

import "errors"

// NetworkRules 是「主机 + 端口 + 用户名」这类经网络连接的驱动共用的 ConfigRules，
// 驱动嵌入它即可；内置的 MySQL / PostgreSQL / SQL Server 都是这样。
type NetworkRules struct {
	// DefaultPort 与驱动描述里的 DefaultPort 一致。
	DefaultPort int
}

func (r NetworkRules) ValidateConfig(cfg *Config, _ Route) error {
	if cfg.Host == "" {
		return errors.New("数据库主机地址不能为空")
	}
	if cfg.Port <= 0 {
		return errors.New("数据库端口无效")
	}
	// Kerberos 资产凭主体的票据登录，用户名可以不填。
	if cfg.Username == "" && cfg.AuthMethod != AuthMethodKerberos {
		return errors.New("数据库用户名不能为空")
	}
	return nil
}

func (r NetworkRules) CreateDefaults(args Args) map[string]any {
	if args.Int("port") != 0 {
		return nil
	}
	return map[string]any{"port": r.DefaultPort}
}

func (r NetworkRules) ValidateCreateArgs(args Args) error {
	if args.String("auth_method") == AuthMethodKerberos {
		if args.String("host") == "" || args.Int("port") == 0 {
			return errors.New("missing required parameters: host, port")
		}
		return nil
	}
	if args.String("host") == "" || args.Int("port") == 0 || args.String("username") == "" {
		return errors.New("missing required parameters: host, port, username")
	}
	return nil
}

func (r NetworkRules) ApplyCreateArgs(cfg *Config, args Args) {
	cfg.Host = args.String("host")
	cfg.Port = args.Int("port")
	if cfg.Port == 0 {
		cfg.Port = r.DefaultPort
	}
	cfg.Username = args.String("username")
}

func (r NetworkRules) ApplyUpdateArgs(cfg *Config, args Args) {
	if v := args.String("host"); v != "" {
		cfg.Host = v
	}
	if v := args.Int("port"); v > 0 {
		cfg.Port = v
	}
	if v := args.String("username"); v != "" {
		cfg.Username = v
	}
}

func (r NetworkRules) ConnectionView(cfg *Config) map[string]any {
	return map[string]any{
		"host":     cfg.Host,
		"port":     cfg.Port,
		"username": cfg.Username,
	}
}
