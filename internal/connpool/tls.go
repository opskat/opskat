package connpool

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/opskat/opskat/internal/service/credential_svc"
)

// TLSFields 描述构建 *tls.Config 所需的最小字段集，
// 由各资产类型 *Config 通过适配方法返回。
//
// CA 证书、客户端证书、客户端私钥各有两种给法：本机文件路径（*File），或直接给出的
// PEM 内容（*PEM）。同一项只能给一种。
type TLSFields struct {
	ServerName string
	Insecure   bool
	CAFile     string
	CertFile   string
	KeyFile    string
	CAPEM      string
	CertPEM    string
	KeyPEM     string
}

// BuildAssetTLSConfig 根据资产配置里保存的 TLS 字段构建 *tls.Config：以内容形式保存的
// 私钥（KeyPEM）是密文，这里解密后交给 BuildTLSConfig。
func BuildAssetTLSConfig(name string, f TLSFields) (*tls.Config, error) {
	f, err := decryptStoredKey(name, f)
	if err != nil {
		return nil, err
	}
	return BuildTLSConfig(name, f)
}

// decryptStoredKey 返回私钥内容（KeyPEM）解密成明文后的字段：资产配置里以内容形式保存的私钥
// 是 credential_svc 密文。错误点名字段，不含内容。
func decryptStoredKey(name string, f TLSFields) (TLSFields, error) {
	if f.KeyPEM != "" {
		key, err := credential_svc.Default().Decrypt(f.KeyPEM)
		if err != nil {
			return f, fmt.Errorf("解密 %s TLS 客户端私钥失败: %w", name, err)
		}
		f.KeyPEM = key
	}
	return f, nil
}

// tlsContents 是 CA 证书、客户端证书、客户端私钥的原始内容，没给的项为 nil。
type tlsContents struct {
	ca, cert, key []byte
}

// readTLSContents 取出三项证书材料的内容：给出的内容原样，路径由本进程读取。KeyPEM 为明文。
// 不解析内容；错误点名出问题的项，不含内容。
func readTLSContents(name string, f TLSFields) (tlsContents, error) {
	ca, err := tlsMaterial(name, "CA 证书", f.CAFile, f.CAPEM)
	if err != nil {
		return tlsContents{}, err
	}
	cert, err := tlsMaterial(name, "客户端证书", f.CertFile, f.CertPEM)
	if err != nil {
		return tlsContents{}, err
	}
	key, err := tlsMaterial(name, "客户端私钥", f.KeyFile, f.KeyPEM)
	if err != nil {
		return tlsContents{}, err
	}
	return tlsContents{ca: ca, cert: cert, key: key}, nil
}

// BuildTLSConfig 根据通用 TLS 字段构建 *tls.Config，KeyPEM 为明文。
// name 用于错误消息（例如 "Kafka"、"Redis"），方便用户定位是哪种资产的 TLS 配置出错。
func BuildTLSConfig(name string, f TLSFields) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         f.ServerName,
		InsecureSkipVerify: f.Insecure,
	}
	contents, err := readTLSContents(name, f)
	if err != nil {
		return nil, err
	}
	if contents.ca != nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(contents.ca) {
			return nil, fmt.Errorf("解析 %s TLS CA 证书失败", name)
		}
		cfg.RootCAs = pool
	}
	cert, key := contents.cert, contents.key
	if cert != nil || key != nil {
		if cert == nil || key == nil {
			return nil, fmt.Errorf("%s TLS 客户端证书和私钥必须同时配置", name)
		}
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("加载 %s TLS 客户端证书失败: %w", name, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// tlsMaterial 取出一项证书材料的 PEM 字节：直接给出的内容，或从路径读出；都没给返回 nil。
func tlsMaterial(name, item, file, content string) ([]byte, error) {
	switch {
	case file != "" && content != "":
		return nil, fmt.Errorf("%s TLS %s同时配置了文件路径和内容，只能保留一种", name, item)
	case content != "":
		return []byte(content), nil
	case file != "":
		data, err := os.ReadFile(file) //nolint:gosec // 路径来自用户自己的资产配置
		if err != nil {
			return nil, fmt.Errorf("读取 %s TLS %s失败: %w", name, item, err)
		}
		return data, nil
	}
	return nil, nil
}
