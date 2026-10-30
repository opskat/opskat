// Package netproxy 管理桌面端外网请求（AI、检查更新、扩展商店、备份）的网络代理设置：
// 设置值与校验、按请求地址选择代理，以及进程内唯一一个随设置实时切换的出站传输（见 Transport）。
//
// 资产连接不经过这里：它们从 http.DefaultTransport 克隆或自带拨号，本包从不修改 http.DefaultTransport。
package netproxy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/http/httpproxy"
)

// Mode 是代理模式。
type Mode string

const (
	// ModeEnv 按进程的 HTTPS_PROXY / HTTP_PROXY / NO_PROXY（大小写均可）选择代理，与 http.ProxyFromEnvironment 相同，不读 ALL_PROXY。
	ModeEnv Mode = "env"
	// ModeDirect 一律直连，忽略环境变量。
	ModeDirect Mode = "direct"
	// ModeCustom 经用户配置的代理连接，命中 NoProxy 的目标直连。
	ModeCustom Mode = "custom"
)

// Scheme 是自定义代理的协议；两种协议下目标主机名都交给代理解析。
type Scheme string

const (
	SchemeHTTP   Scheme = "http"
	SchemeSOCKS5 Scheme = "socks5"
)

// DefaultNoProxy 是「不走代理的地址」的默认值：本机与私有网段，避免内网 WebDAV 等在设了代理后断开。
const DefaultNoProxy = "localhost\n127.0.0.1\n::1\n10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16"

// Settings 是网络代理设置。Scheme / Host / Port / Username / Password / NoProxy 只在 ModeCustom 下生效，
// 其他模式下保留原值以便切回「自定义」。
type Settings struct {
	Mode     Mode
	Scheme   Scheme
	Host     string
	Port     int
	Username string
	// Password 为明文；只在 Username 非空时发给代理。
	Password string
	// NoProxy 每行一项：域名（同时匹配其所有子域名，example.com / .example.com / *.example.com 等价）、
	// IP 地址或 CIDR 网段；空行忽略。
	NoProxy string
}

// Default 返回默认设置：使用环境变量，自定义部分预填 HTTP 与默认的不走代理地址。
func Default() Settings {
	return Settings{Mode: ModeEnv, Scheme: SchemeHTTP, NoProxy: DefaultNoProxy}
}

// FieldError 的 Field 取值，对应界面上的字段。
const (
	FieldMode    = "mode"
	FieldScheme  = "scheme"
	FieldHost    = "host"
	FieldPort    = "port"
	FieldNoProxy = "noProxy"
)

// FieldError 的 Code 取值。
const (
	CodeRequired   = "required"
	CodeInvalid    = "invalid"
	CodeOutOfRange = "out_of_range"
)

// FieldError 指出哪个字段（NoProxy 还指出第几行，从 1 开始）因何不合法，供调用方映射到界面字段与文案。
type FieldError struct {
	Field string
	// Line 为 NoProxy 中出错的行号（从 1 开始，空行也计数）；其他字段为 0。
	Line int
	Code string
}

func (e *FieldError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("netproxy: %s line %d: %s", e.Field, e.Line, e.Code)
	}
	return fmt.Sprintf("netproxy: %s: %s", e.Field, e.Code)
}

// Validate 校验设置，返回第一个不合法字段的 *FieldError。非 ModeCustom 时不校验自定义部分。
func (s Settings) Validate() error {
	switch s.Mode {
	case ModeEnv, ModeDirect:
		return nil
	case ModeCustom:
	default:
		return &FieldError{Field: FieldMode, Code: CodeInvalid}
	}
	if s.Scheme != SchemeHTTP && s.Scheme != SchemeSOCKS5 {
		return &FieldError{Field: FieldScheme, Code: CodeInvalid}
	}
	if s.Host == "" {
		return &FieldError{Field: FieldHost, Code: CodeRequired}
	}
	if net.ParseIP(s.Host) == nil && !isHostname(s.Host) {
		return &FieldError{Field: FieldHost, Code: CodeInvalid}
	}
	if s.Port == 0 {
		return &FieldError{Field: FieldPort, Code: CodeRequired}
	}
	if s.Port < 1 || s.Port > 65535 {
		return &FieldError{Field: FieldPort, Code: CodeOutOfRange}
	}
	for i, line := range strings.Split(s.NoProxy, "\n") {
		if _, ok := noProxyEntry(line); !ok {
			return &FieldError{Field: FieldNoProxy, Line: i + 1, Code: CodeInvalid}
		}
	}
	return nil
}

// ProxyFunc 返回按本设置为请求 URL 选择代理的函数（nil 表示直连），签名与 httpproxy.Config.ProxyFunc 一致。
// ModeEnv 在调用 ProxyFunc 时读取环境变量。localhost 与回环地址在任何模式下都直连。
func (s Settings) ProxyFunc() func(*url.URL) (*url.URL, error) {
	switch s.Mode {
	case ModeEnv:
		return httpproxy.FromEnvironment().ProxyFunc()
	case ModeCustom:
		proxy := s.proxyURL().String()
		return (&httpproxy.Config{HTTPProxy: proxy, HTTPSProxy: proxy, NoProxy: s.noProxyList()}).ProxyFunc()
	default:
		return func(*url.URL) (*url.URL, error) { return nil, nil }
	}
}

func (s Settings) proxyURL() *url.URL {
	u := &url.URL{Scheme: string(s.Scheme), Host: net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}
	if s.Username != "" {
		u.User = url.UserPassword(s.Username, s.Password)
	}
	return u
}

// noProxyList 把 NoProxy 的各行转成 httpproxy 的逗号分隔列表。
func (s Settings) noProxyList() string {
	var entries []string
	for _, line := range strings.Split(s.NoProxy, "\n") {
		if entry, ok := noProxyEntry(line); ok && entry != "" {
			entries = append(entries, entry)
		}
	}
	return strings.Join(entries, ",")
}

// noProxyEntry 把 NoProxy 的一行归一成 httpproxy 的匹配项：空行返回 ("", true)；域名去掉前导 "." / "*."，
// 因为 httpproxy 中 ".a.com" 只匹配子域名而裸域名同时匹配自身与子域名。无法识别的行返回 ok=false。
func noProxyEntry(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", true
	}
	if _, _, err := net.ParseCIDR(line); err == nil {
		return line, true
	}
	if net.ParseIP(line) != nil {
		return line, true
	}
	domain := line
	switch {
	case strings.HasPrefix(line, "*."):
		domain = line[2:]
	case strings.HasPrefix(line, "."):
		domain = line[1:]
	}
	if !isHostname(domain) {
		return "", false
	}
	return domain, true
}

// isHostname 判断 host 是否为主机名：以 "." 分隔的非空标签，每段由字母、数字、"_"、"-" 组成且不以 "-" 开头或结尾；
// 末段不能全是数字（那是写错的 IPv4）。
func isHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	return strings.TrimFunc(last, unicode.IsDigit) != ""
}
