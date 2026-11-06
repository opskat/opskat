package custom_type_entity

// 宿主连接字段：一个资产"怎么连过去"的那部分自动化参数（opsctl --config / put_asset
// config 的键）——SSH 隧道、代理链、TLS。它们在所有资产类型里同名同义，解析、校验与
// 加密在 internal/assettype/connection_args.go。
//
// 名字定义在实体层，是因为自定义类型的字段名校验（Validate）与那份解析必须认同一份
// 清单（Design decision 21），而 assettype 依赖本包、反过来不行。
const (
	ConnFieldSSHAsset      = "ssh_asset_id"
	ConnFieldProxyChain    = "proxy_chain"
	ConnFieldTLS           = "tls"
	ConnFieldTLSInsecure   = "tls_insecure"
	ConnFieldTLSServerName = "tls_server_name"
	ConnFieldTLSKeyPEM     = "tls_key_pem"
)

// TLSCertField 是一份证书材料的两种给法：本机路径，或 PEM 内容。
type TLSCertField struct{ File, PEM string }

// TLSCertFields 依次是 CA 证书、客户端证书、客户端私钥。
func TLSCertFields() []TLSCertField {
	return []TLSCertField{
		{"tls_ca_file", "tls_ca_pem"},
		{"tls_cert_file", "tls_cert_pem"},
		{"tls_key_file", ConnFieldTLSKeyPEM},
	}
}

// TLSConnectionFields 返回全部 TLS 连接字段：开关、跳过校验、ServerName 与三份证书材料。
func TLSConnectionFields() []string {
	certs := TLSCertFields()
	out := make([]string, 0, 3+2*len(certs))
	out = append(out, ConnFieldTLS, ConnFieldTLSInsecure, ConnFieldTLSServerName)
	for _, cert := range certs {
		out = append(out, cert.File, cert.PEM)
	}
	return out
}

// HostConnectionFields 返回全部宿主连接字段名，即不能用作自定义类型字段名的那些。
func HostConnectionFields() []string {
	return append([]string{ConnFieldSSHAsset, ConnFieldProxyChain}, TLSConnectionFields()...)
}
