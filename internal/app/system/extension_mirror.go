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

// repositoryComponent 是 OCI 仓库路径里的一段：小写字母数字，中间可用 . _ __ - 分隔。
var repositoryComponent = regexp.MustCompile(`^[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*$`)

// validRegistryMirror 判断 s 是否为 host[:port][/路径前缀]。路径前缀是镜像站放上游
// registry 的那一段仓库路径（mirror.example.com/ghcr.io），按仓库路径的写法校验；
// 它不能以 v2 开头——那是 registry 的接口根，照抄进来只会拼出 /v2/v2/...。
func validRegistryMirror(s string) bool {
	host, prefix, hasPrefix := strings.Cut(s, "/")
	if !validRegistryHost(host) {
		return false
	}
	if !hasPrefix {
		return true
	}
	segments := strings.Split(prefix, "/")
	if segments[0] == "v2" {
		return false
	}
	for _, seg := range segments {
		if !repositoryComponent.MatchString(seg) {
			return false
		}
	}
	return true
}

// GetExtensionMirror 返回已保存的扩展下载镜像；空字符串表示直连 ghcr.io。
func (s *System) GetExtensionMirror() string {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return ""
	}
	return cfg.ExtensionMirror
}

// SetExtensionMirror 设置扩展下载镜像（host[:port][/路径前缀]，不带协议）。
// 空字符串表示直连 ghcr.io；格式非法时拒绝且不保存。
func (s *System) SetExtensionMirror(host string) error {
	if host != "" && !validRegistryMirror(host) {
		return fmt.Errorf("%s", i18n.Pick(s.Lang(),
			"镜像地址格式无效：填主机名或 IP，可带端口；镜像站把 ghcr.io 放在路径下时再跟上那段路径（如 ghcr.nju.edu.cn、registry.example.com:5000、mirror.example.com/ghcr.io），不含 https:// 和 /v2",
			"Invalid mirror address: enter a hostname or IP with an optional port, followed by the path when the mirror keeps ghcr.io under one (e.g. ghcr.nju.edu.cn, registry.example.com:5000, mirror.example.com/ghcr.io), without https:// or /v2"))
	}
	cfg := bootstrap.GetConfig()
	cfg.ExtensionMirror = host
	return bootstrap.SaveConfig(cfg)
}
