package netproxy_test

import (
	"errors"
	"net/url"
	"testing"

	"github.com/opskat/opskat/internal/pkg/netproxy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// proxyFor 返回 s 对 rawURL 选择的代理地址；直连返回空串。
func proxyFor(t *testing.T, s netproxy.Settings, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	p, err := s.ProxyFunc()(u)
	require.NoError(t, err)
	if p == nil {
		return ""
	}
	return p.String()
}

func customSettings(noProxy string) netproxy.Settings {
	return netproxy.Settings{
		Mode:    netproxy.ModeCustom,
		Scheme:  netproxy.SchemeHTTP,
		Host:    "proxy.lan",
		Port:    7890,
		NoProxy: noProxy,
	}
}

func TestDefault(t *testing.T) {
	d := netproxy.Default()
	assert.Equal(t, netproxy.ModeEnv, d.Mode)
	assert.Equal(t, netproxy.SchemeHTTP, d.Scheme)
	assert.Equal(t, netproxy.DefaultNoProxy, d.NoProxy)
	assert.NoError(t, d.Validate())
}

func TestDefaultNoProxyBypassesLocalAndPrivateNetworks(t *testing.T) {
	s := customSettings(netproxy.DefaultNoProxy)
	for _, target := range []string{
		"http://localhost/", "http://127.0.0.1/", "http://[::1]/",
		"http://10.20.30.40/", "http://172.16.0.1/", "http://172.31.255.254/", "http://192.168.1.10:5005/",
	} {
		assert.Empty(t, proxyFor(t, s, target), target)
	}
	for _, target := range []string{"https://api.openai.com/", "http://172.32.0.1/", "http://8.8.8.8/", "http://192.169.0.1/"} {
		assert.Equal(t, "http://proxy.lan:7890", proxyFor(t, s, target), target)
	}
}

func TestCustomProxyFunc(t *testing.T) {
	s := customSettings("example.com\n.corp.test\n*.internal.test\n10.0.0.0/8\n203.0.113.7\n\n  spaced.test  \r\nFD00::/8")
	const proxy = "http://proxy.lan:7890"
	cases := map[string]string{
		"https://api.openai.com/v1/chat": proxy,
		"http://api.openai.com/v1":       proxy,
		"https://example.com/":           "",
		"https://EXAMPLE.com/":           "",
		"https://api.example.com/":       "",
		"https://notexample.com/":        proxy,
		"https://corp.test/":             "",
		"https://a.b.corp.test/":         "",
		"https://internal.test/":         "",
		"https://x.internal.test/":       "",
		"http://10.2.3.4:8080/":          "",
		"http://11.0.0.1/":               proxy,
		"http://203.0.113.7/":            "",
		"http://203.0.113.8/":            proxy,
		"https://spaced.test/":           "",
		"http://[fd00::1]/":              "",
		"http://[fe80::1]/":              proxy,
	}
	for target, want := range cases {
		assert.Equal(t, want, proxyFor(t, s, target), target)
	}
}

func TestCustomProxyURL(t *testing.T) {
	t.Run("socks5", func(t *testing.T) {
		s := customSettings("")
		s.Scheme = netproxy.SchemeSOCKS5
		s.Port = 1080
		assert.Equal(t, "socks5://proxy.lan:1080", proxyFor(t, s, "https://api.openai.com/"))
	})
	t.Run("ipv6 host", func(t *testing.T) {
		s := customSettings("")
		s.Host = "fd00::7"
		assert.Equal(t, "http://[fd00::7]:7890", proxyFor(t, s, "https://api.openai.com/"))
	})
	t.Run("credentials", func(t *testing.T) {
		s := customSettings("")
		s.Username, s.Password = "user@corp", "p:ss/w@rd"
		u, err := url.Parse("https://api.openai.com/")
		require.NoError(t, err)
		p, err := s.ProxyFunc()(u)
		require.NoError(t, err)
		require.NotNil(t, p)
		require.NotNil(t, p.User)
		assert.Equal(t, "user@corp", p.User.Username())
		pass, ok := p.User.Password()
		assert.True(t, ok)
		assert.Equal(t, "p:ss/w@rd", pass)
		assert.Equal(t, "proxy.lan:7890", p.Host)
	})
	t.Run("password without username sends no auth", func(t *testing.T) {
		s := customSettings("")
		s.Password = "orphan"
		assert.Equal(t, "http://proxy.lan:7890", proxyFor(t, s, "https://api.openai.com/"))
	})
}

// setProxyEnv 清空并设置 Go 读取的代理环境变量（大小写两套），外加 Go 忽略的 ALL_PROXY。
func setProxyEnv(t *testing.T, env map[string]string) {
	for _, k := range []string{
		"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
		"ALL_PROXY", "all_proxy", "REQUEST_METHOD",
	} {
		t.Setenv(k, env[k])
	}
}

func TestEnvProxyFuncMatchesProxyFromEnvironment(t *testing.T) {
	env := netproxy.Settings{Mode: netproxy.ModeEnv, Scheme: netproxy.SchemeHTTP, Host: "proxy.lan", Port: 7890, NoProxy: "a.example"}

	t.Run("https/http/no_proxy, ALL_PROXY ignored", func(t *testing.T) {
		setProxyEnv(t, map[string]string{
			"HTTPS_PROXY": "http://envs:3128",
			"HTTP_PROXY":  "http://envh:8080",
			"NO_PROXY":    "skip.example",
			"ALL_PROXY":   "socks5://all:1080",
		})
		assert.Equal(t, "http://envs:3128", proxyFor(t, env, "https://a.example/"))
		assert.Equal(t, "http://envh:8080", proxyFor(t, env, "http://a.example/"))
		assert.Empty(t, proxyFor(t, env, "https://x.skip.example/"))
	})
	t.Run("lowercase variables", func(t *testing.T) {
		setProxyEnv(t, map[string]string{"https_proxy": "http://low:3128", "no_proxy": "skip.example"})
		assert.Equal(t, "http://low:3128", proxyFor(t, env, "https://a.example/"))
		assert.Empty(t, proxyFor(t, env, "http://a.example/"))
		assert.Empty(t, proxyFor(t, env, "https://skip.example/"))
	})
	t.Run("only ALL_PROXY means direct", func(t *testing.T) {
		setProxyEnv(t, map[string]string{"ALL_PROXY": "socks5://all:1080", "all_proxy": "socks5://all:1080"})
		assert.Empty(t, proxyFor(t, env, "https://a.example/"))
	})
}

func TestDirectIgnoresEnvironment(t *testing.T) {
	setProxyEnv(t, map[string]string{"HTTPS_PROXY": "http://envs:3128", "HTTP_PROXY": "http://envh:8080"})
	s := customSettings("")
	s.Mode = netproxy.ModeDirect
	assert.Empty(t, proxyFor(t, s, "https://api.openai.com/"))
	assert.Empty(t, proxyFor(t, s, "http://api.openai.com/"))
}

func TestValidate(t *testing.T) {
	custom := func(mut func(*netproxy.Settings)) netproxy.Settings {
		s := customSettings(netproxy.DefaultNoProxy)
		s.Host = "127.0.0.1"
		mut(&s)
		return s
	}
	valid := map[string]netproxy.Settings{
		"default":               netproxy.Default(),
		"env ignores custom":    {Mode: netproxy.ModeEnv, Host: "http://bad", Port: -1, NoProxy: "not valid"},
		"direct ignores custom": {Mode: netproxy.ModeDirect, Scheme: "ftp", NoProxy: "*"},
		"custom ipv4":           custom(func(*netproxy.Settings) {}),
		"custom socks5":         custom(func(s *netproxy.Settings) { s.Scheme = netproxy.SchemeSOCKS5 }),
		"custom hostname":       custom(func(s *netproxy.Settings) { s.Host = "proxy-1.corp_lan.example" }),
		"custom single label":   custom(func(s *netproxy.Settings) { s.Host = "localhost" }),
		"custom ipv6":           custom(func(s *netproxy.Settings) { s.Host = "fd00::7" }),
		"port 1":                custom(func(s *netproxy.Settings) { s.Port = 1 }),
		"port 65535":            custom(func(s *netproxy.Settings) { s.Port = 65535 }),
		"empty no proxy":        custom(func(s *netproxy.Settings) { s.NoProxy = "" }),
		"no proxy forms and blanks": custom(func(s *netproxy.Settings) {
			s.NoProxy = "example.com\n\n  .corp.test  \r\n*.x.test\n10.0.0.0/8\n::1\nfd00::/8\n203.0.113.7\n"
		}),
	}
	for name, s := range valid {
		assert.NoError(t, s.Validate(), name)
	}

	invalid := map[string]struct {
		s    netproxy.Settings
		want netproxy.FieldError
	}{
		"unknown mode":     {netproxy.Settings{Mode: "system"}, netproxy.FieldError{Field: netproxy.FieldMode, Code: netproxy.CodeInvalid}},
		"empty mode":       {netproxy.Settings{}, netproxy.FieldError{Field: netproxy.FieldMode, Code: netproxy.CodeInvalid}},
		"https scheme":     {custom(func(s *netproxy.Settings) { s.Scheme = "https" }), netproxy.FieldError{Field: netproxy.FieldScheme, Code: netproxy.CodeInvalid}},
		"empty scheme":     {custom(func(s *netproxy.Settings) { s.Scheme = "" }), netproxy.FieldError{Field: netproxy.FieldScheme, Code: netproxy.CodeInvalid}},
		"empty host":       {custom(func(s *netproxy.Settings) { s.Host = "" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeRequired}},
		"host with scheme": {custom(func(s *netproxy.Settings) { s.Host = "http://127.0.0.1" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"host with path":   {custom(func(s *netproxy.Settings) { s.Host = "127.0.0.1/proxy" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"host with port":   {custom(func(s *netproxy.Settings) { s.Host = "127.0.0.1:7890" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"host whitespace":  {custom(func(s *netproxy.Settings) { s.Host = " 127.0.0.1" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"host inner space": {custom(func(s *netproxy.Settings) { s.Host = "proxy lan" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"bracketed ipv6":   {custom(func(s *netproxy.Settings) { s.Host = "[::1]" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"bad ipv4":         {custom(func(s *netproxy.Settings) { s.Host = "999.1.1.1" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"empty label":      {custom(func(s *netproxy.Settings) { s.Host = "proxy..lan" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"leading hyphen":   {custom(func(s *netproxy.Settings) { s.Host = "-proxy.lan" }), netproxy.FieldError{Field: netproxy.FieldHost, Code: netproxy.CodeInvalid}},
		"port missing":     {custom(func(s *netproxy.Settings) { s.Port = 0 }), netproxy.FieldError{Field: netproxy.FieldPort, Code: netproxy.CodeRequired}},
		"port negative":    {custom(func(s *netproxy.Settings) { s.Port = -1 }), netproxy.FieldError{Field: netproxy.FieldPort, Code: netproxy.CodeOutOfRange}},
		"port too large":   {custom(func(s *netproxy.Settings) { s.Port = 65536 }), netproxy.FieldError{Field: netproxy.FieldPort, Code: netproxy.CodeOutOfRange}},
		"no proxy url": {custom(func(s *netproxy.Settings) { s.NoProxy = "ok.example\nhttp://bad.example" }),
			netproxy.FieldError{Field: netproxy.FieldNoProxy, Line: 2, Code: netproxy.CodeInvalid}},
		"no proxy line after blank": {custom(func(s *netproxy.Settings) { s.NoProxy = "a.example\n\nfoo bar" }),
			netproxy.FieldError{Field: netproxy.FieldNoProxy, Line: 3, Code: netproxy.CodeInvalid}},
		"no proxy bad cidr": {custom(func(s *netproxy.Settings) { s.NoProxy = "10.0.0.0/33" }),
			netproxy.FieldError{Field: netproxy.FieldNoProxy, Line: 1, Code: netproxy.CodeInvalid}},
		"no proxy with port": {custom(func(s *netproxy.Settings) { s.NoProxy = "a.example:443" }),
			netproxy.FieldError{Field: netproxy.FieldNoProxy, Line: 1, Code: netproxy.CodeInvalid}},
		"no proxy wildcard all": {custom(func(s *netproxy.Settings) { s.NoProxy = "a.example\n*" }),
			netproxy.FieldError{Field: netproxy.FieldNoProxy, Line: 2, Code: netproxy.CodeInvalid}},
		"no proxy bare dot": {custom(func(s *netproxy.Settings) { s.NoProxy = "." }),
			netproxy.FieldError{Field: netproxy.FieldNoProxy, Line: 1, Code: netproxy.CodeInvalid}},
		"no proxy comma list": {custom(func(s *netproxy.Settings) { s.NoProxy = "a.example,b.example" }),
			netproxy.FieldError{Field: netproxy.FieldNoProxy, Line: 1, Code: netproxy.CodeInvalid}},
	}
	for name, tc := range invalid {
		err := tc.s.Validate()
		var fe *netproxy.FieldError
		if assert.True(t, errors.As(err, &fe), "%s: got %v", name, err) {
			assert.Equal(t, tc.want, *fe, name)
			assert.NotEmpty(t, fe.Error(), name)
		}
	}
}
