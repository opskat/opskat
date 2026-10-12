package asset_entity

// ExtensionConnectionConfig is what an extension asset stores under the host-reserved
// key of its Config JSON (pkg/extension.HostConnectionConfigKey): the host-owned
// settings for the connection items its asset type opts into via connection.proxyChain /
// connection.tls. The asset form, the automation boundary (opsctl / put_asset) and the
// extension host's dialer all read and write this one shape. A single SSH tunnel is not
// here — it lives on the asset's own SSHTunnelID column.
type ExtensionConnectionConfig struct {
	ProxyChain *ProxyChainConfig   `json:"proxyChain,omitempty"`
	TLS        *ExtensionTLSConfig `json:"tls,omitempty"`
}

// ExtensionTLSConfig mirrors the TLS fields of a built-in asset type: each of the CA
// certificate, client certificate and client key is either a path on this machine
// (*File) or its PEM content (CACert / ClientCert / ClientKey). A client key given as
// content is the one secret here and is always ciphertext — on a stored asset and in an
// ad-hoc test-connection call alike; connpool.BuildAssetTLSConfig decrypts it for the dial.
type ExtensionTLSConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`
	ServerName string `json:"serverName,omitempty"`
	CAFile     string `json:"caFile,omitempty"`
	CertFile   string `json:"certFile,omitempty"`
	KeyFile    string `json:"keyFile,omitempty"`
	CACert     string `json:"caCert,omitempty"`
	ClientCert string `json:"clientCert,omitempty"`
	ClientKey  string `json:"clientKey,omitempty"`
}
