package system

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/bootstrap"
)

var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// validRegistryHost 判断 s 是否为 host[:port]：主机名 / IPv4 / 方括号 IPv6，
// 可带 1-65535 的端口，不含协议、路径或空白。
func validRegistryHost(s string) bool {
	host := s
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return false
		}
		addr, err := netip.ParseAddr(s[1:end])
		if err != nil || !addr.Is6() {
			return false
		}
		rest := s[end+1:]
		if rest == "" {
			return true
		}
		port, ok := strings.CutPrefix(rest, ":")
		return ok && validPort(port)
	}
	if strings.Contains(s, ":") {
		h, port, err := net.SplitHostPort(s)
		if err != nil || !validPort(port) {
			return false
		}
		host = h
	}
	if len(host) > 253 {
		return false
	}
	return hostnamePattern.MatchString(host)
}

func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	if err != nil || strconv.Itoa(n) != p {
		return false
	}
	return n >= 1 && n <= 65535
}

// GetExtensionMirror 返回已保存的扩展下载镜像主机；空字符串表示直连 ghcr.io。
func (s *System) GetExtensionMirror() string {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return ""
	}
	return cfg.ExtensionMirror
}

// SetExtensionMirror 设置扩展下载镜像主机（host[:port]，不带协议与路径）。
// 空字符串表示直连 ghcr.io；格式非法时拒绝且不保存。
func (s *System) SetExtensionMirror(host string) error {
	if host != "" && !validRegistryHost(host) {
		return fmt.Errorf("%s", i18n.Pick(s.Lang(),
			"镜像主机格式无效：只填主机名或 IP，可带端口（如 ghcr.nju.edu.cn、registry.example.com:5000），不含 https:// 和路径",
			"Invalid mirror host: enter a hostname or IP with an optional port (e.g. ghcr.nju.edu.cn, registry.example.com:5000), without https:// or a path"))
	}
	cfg := bootstrap.GetConfig()
	cfg.ExtensionMirror = host
	return bootstrap.SaveConfig(cfg)
}
