package connpool

import (
	"crypto/tls"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	defaultEtcdDialTimeout    = 5 * time.Second
	defaultEtcdCommandTimeout = 10 * time.Second
)

// buildEtcdClientConfig 把 EtcdConfig + 解密后的明文密码组装为 clientv3.Config。
// 仅处理参数映射,不负责 dialer/SSH 隧道（在 DialEtcd 中处理）。
func buildEtcdClientConfig(cfg *asset_entity.EtcdConfig, password string) (clientv3.Config, error) {
	dialTimeout := defaultEtcdDialTimeout
	if cfg.DialTimeoutSeconds > 0 {
		dialTimeout = time.Duration(cfg.DialTimeoutSeconds) * time.Second
	}

	c := clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: dialTimeout,
		Username:    cfg.Username,
		Password:    password,
	}

	if cfg.TLS {
		tlsCfg, err := buildEtcdTLSConfig(cfg)
		if err != nil {
			return clientv3.Config{}, err
		}
		c.TLS = tlsCfg
	}
	return c, nil
}

func buildEtcdTLSConfig(cfg *asset_entity.EtcdConfig) (*tls.Config, error) {
	return BuildTLSConfig("etcd", TLSFields{
		ServerName: cfg.TLSServerName,
		Insecure:   cfg.TLSInsecure,
		CAFile:     cfg.TLSCAFile,
		CertFile:   cfg.TLSCertFile,
		KeyFile:    cfg.TLSKeyFile,
	})
}
