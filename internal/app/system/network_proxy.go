package system

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// NetworkProxyEnvVar 是 OpsKat 进程实际读到的一个代理环境变量（代理地址里的密码已遮为 ***）。
type NetworkProxyEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// NetworkProxyView 是返回给前端的网络代理设置；不含密码，只说明是否已设置。
type NetworkProxyView struct {
	Mode        string               `json:"mode"` // env | direct | custom
	Type        string               `json:"type"` // http | socks5
	Host        string               `json:"host"`
	Port        int                  `json:"port"`
	Username    string               `json:"username"`
	PasswordSet bool                 `json:"passwordSet"`
	NoProxy     string               `json:"noProxy"` // 每行一项的原始文本
	EnvVars     []NetworkProxyEnvVar `json:"envVars"`
}

// NetworkProxyInput 是前端提交的网络代理设置；Password 留空表示沿用已保存的密码。
type NetworkProxyInput struct {
	Mode     string `json:"mode"`
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	NoProxy  string `json:"noProxy"`
}

// GetNetworkProxySettings 返回已保存的网络代理设置（未保存过时为默认：使用环境变量）及进程读到的代理环境变量。
func (s *System) GetNetworkProxySettings() (*NetworkProxyView, error) {
	saved := networkProxyConfigOrDefault(bootstrap.GetConfig())
	return &NetworkProxyView{
		Mode:        saved.Mode,
		Type:        saved.Type,
		Host:        saved.Host,
		Port:        saved.Port,
		Username:    saved.Username,
		PasswordSet: saved.Password != "",
		NoProxy:     saved.NoProxy,
		EnvVars:     proxyEnvVars(),
	}, nil
}

// SaveNetworkProxySettings 校验、持久化并立即应用网络代理设置。校验失败时拒绝，配置与生效设置都不变。
func (s *System) SaveNetworkProxySettings(in NetworkProxyInput) error {
	cfg := bootstrap.GetConfig()
	settings, err := networkProxySettingsFromInput(in, cfg)
	if err != nil {
		return err
	}
	if err := s.validateNetworkProxy(settings); err != nil {
		return err
	}
	stored := bootstrap.NetworkProxyConfig{
		Mode:     string(settings.Mode),
		Type:     string(settings.Scheme),
		Host:     settings.Host,
		Port:     settings.Port,
		Username: settings.Username,
		NoProxy:  settings.NoProxy,
	}
	if settings.Password != "" {
		encrypted, err := credential_svc.Default().Encrypt(settings.Password)
		if err != nil {
			return fmt.Errorf("加密代理密码失败: %w", err)
		}
		stored.Password = encrypted
	}
	prev := cfg.NetworkProxy
	cfg.NetworkProxy = &stored
	if err := bootstrap.SaveConfig(cfg); err != nil {
		cfg.NetworkProxy = prev
		return err
	}
	netproxy.Set(settings)
	return nil
}

// ApplyNetworkProxy 在启动时由 main.go 调用，把已保存的网络代理设置注入 netproxy。
// 已保存的密码解密失败时返回错误，但仍按其余设置应用（不带密码），不静默退回直连或环境变量：
// 请求会因代理要求认证而明确失败，由调用方记录该错误。
func ApplyNetworkProxy(cfg *bootstrap.AppConfig) error {
	settings, err := savedNetworkProxySettings(cfg)
	netproxy.Set(settings)
	return err
}

// networkProxySettingsFromInput 把前端提交的 in 与已保存配置 cfg 合成完整的 netproxy.Settings（未校验）。
// 用户名非空且 in.Password 为空时沿用 cfg 中已保存并解密的密码；用户名为空则没有密码（认证已取消）。
// 保存与「测试连接」（用未保存的设置测试）共用，二者对密码的取舍因此一致。
func networkProxySettingsFromInput(in NetworkProxyInput, cfg *bootstrap.AppConfig) (netproxy.Settings, error) {
	settings := netproxy.Settings{
		Mode:     netproxy.Mode(in.Mode),
		Scheme:   netproxy.Scheme(in.Type),
		Host:     in.Host,
		Port:     in.Port,
		Username: in.Username,
		Password: in.Password,
		NoProxy:  in.NoProxy,
	}
	if settings.Mode != netproxy.ModeCustom && settings.Scheme == "" {
		settings.Scheme = netproxy.SchemeHTTP
	}
	if settings.Username == "" {
		settings.Password = ""
		return settings, nil
	}
	if settings.Password == "" {
		saved, err := savedNetworkProxySettings(cfg)
		if err != nil {
			return netproxy.Settings{}, err
		}
		settings.Password = saved.Password
	}
	return settings, nil
}

// savedNetworkProxySettings 把配置中的网络代理设置转成 netproxy.Settings（密码解密）。
// 解密失败时仍返回其余字段，并同时返回错误。
func savedNetworkProxySettings(cfg *bootstrap.AppConfig) (netproxy.Settings, error) {
	saved := networkProxyConfigOrDefault(cfg)
	settings := netproxy.Settings{
		Mode:     netproxy.Mode(saved.Mode),
		Scheme:   netproxy.Scheme(saved.Type),
		Host:     saved.Host,
		Port:     saved.Port,
		Username: saved.Username,
		NoProxy:  saved.NoProxy,
	}
	if saved.Password == "" {
		return settings, nil
	}
	plain, err := credential_svc.Default().Decrypt(saved.Password)
	if err != nil {
		return settings, fmt.Errorf("解密代理密码失败: %w", err)
	}
	settings.Password = plain
	return settings, nil
}

func networkProxyConfigOrDefault(cfg *bootstrap.AppConfig) bootstrap.NetworkProxyConfig {
	if cfg != nil && cfg.NetworkProxy != nil {
		return *cfg.NetworkProxy
	}
	d := netproxy.Default()
	return bootstrap.NetworkProxyConfig{Mode: string(d.Mode), Type: string(d.Scheme), NoProxy: d.NoProxy}
}

// validateNetworkProxy 校验设置，把 netproxy 的字段错误转成中英文用户文案。
func (s *System) validateNetworkProxy(settings netproxy.Settings) error {
	err := settings.Validate()
	if err == nil {
		return nil
	}
	var fe *netproxy.FieldError
	if !errors.As(err, &fe) {
		return err
	}
	lang := s.Lang()
	pick := func(zh, en string) error { return fmt.Errorf("%s", i18n.Pick(lang, zh, en)) }
	switch {
	case fe.Field == netproxy.FieldMode:
		return pick("代理模式无效", "Invalid proxy mode")
	case fe.Field == netproxy.FieldScheme:
		return pick("代理类型无效：只支持 HTTP 或 SOCKS5", "Invalid proxy type: only HTTP or SOCKS5 is supported")
	case fe.Field == netproxy.FieldHost && fe.Code == netproxy.CodeRequired:
		return pick("请填写代理地址", "Proxy host is required")
	case fe.Field == netproxy.FieldHost:
		return pick("代理地址格式无效：填主机名、IPv4 或 IPv6，不含协议、路径与空白", "Invalid proxy host: enter a hostname, IPv4 or IPv6 address without scheme, path or whitespace")
	case fe.Field == netproxy.FieldPort && fe.Code == netproxy.CodeRequired:
		return pick("请填写代理端口", "Proxy port is required")
	case fe.Field == netproxy.FieldPort:
		return pick("代理端口无效：须为 1–65535 的整数", "Invalid proxy port: must be an integer between 1 and 65535")
	case fe.Field == netproxy.FieldNoProxy:
		return pick(fmt.Sprintf("「不走代理的地址」第 %d 行无法识别：每行填域名、IP 地址或网段（CIDR）", fe.Line),
			fmt.Sprintf("Line %d of the no-proxy list is not recognized: each line must be a domain, IP address or CIDR range", fe.Line))
	}
	return err
}

// proxySchemePrefix 匹配代理地址开头的 "scheme://"（可省略，省略时进程按 http:// 解析）。
var proxySchemePrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// maskProxyPassword 把代理地址里的密码换成 ***。按 net/url 的规则切分：authority 截到第一个 "/"、"?" 或 "#"，
// userinfo 截到 authority 里最后一个 "@"，用户名与密码以第一个 ":" 分隔——与进程实际解析出的密码一致。
func maskProxyPassword(value string) string {
	start := len(proxySchemePrefix.FindString(value))
	authority := value[start:]
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return value
	}
	colon := strings.Index(authority[:at], ":")
	if colon < 0 {
		return value
	}
	return value[:start+colon+1] + "***" + value[start+at:]
}

// proxyEnvVars 返回 OpsKat 进程实际读到的 HTTPS_PROXY / HTTP_PROXY / NO_PROXY：
// 与 net/http 一致，每个变量大写优先、其次小写，取第一个非空的；不含 ALL_PROXY。
func proxyEnvVars() []NetworkProxyEnvVar {
	out := []NetworkProxyEnvVar{}
	for _, names := range [][2]string{{"HTTPS_PROXY", "https_proxy"}, {"HTTP_PROXY", "http_proxy"}, {"NO_PROXY", "no_proxy"}} {
		for _, name := range names {
			value := os.Getenv(name)
			if value == "" {
				continue
			}
			if names[0] != "NO_PROXY" {
				value = maskProxyPassword(value)
			}
			out = append(out, NetworkProxyEnvVar{Name: name, Value: value})
			break
		}
	}
	return out
}
