package system

import (
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// setupNetworkProxyTest 重置配置与进程级 netproxy 状态，并在结束时还原。
func setupNetworkProxyTest(t *testing.T, cfg *bootstrap.AppConfig) *System {
	t.Helper()
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	prev := netproxy.Get()
	netproxy.Set(netproxy.Default())
	t.Cleanup(func() { netproxy.Set(prev) })
	return New(t.Context(), SkillContent{})
}

func customInput() NetworkProxyInput {
	return NetworkProxyInput{Mode: "custom", Type: "socks5", Host: "127.0.0.1", Port: 7890, Username: "u", Password: "secret", NoProxy: "example.com\n10.0.0.0/8"}
}

func TestNetworkProxyDefaultsToEnv(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	v, err := s.GetNetworkProxySettings()
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != "env" || v.Type != "http" || v.NoProxy != netproxy.DefaultNoProxy || v.PasswordSet {
		t.Fatalf("default view = %+v", v)
	}
}

func TestNetworkProxySaveRoundTripAppliesAndEncrypts(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	if err := s.SaveNetworkProxySettings(customInput()); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetNetworkProxySettings()
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != "custom" || v.Type != "socks5" || v.Host != "127.0.0.1" || v.Port != 7890 || v.Username != "u" || !v.PasswordSet || v.NoProxy != "example.com\n10.0.0.0/8" {
		t.Fatalf("view = %+v", v)
	}
	got := netproxy.Get()
	if got.Mode != netproxy.ModeCustom || got.Scheme != netproxy.SchemeSOCKS5 || got.Port != 7890 || got.Password != "secret" || got.Username != "u" {
		t.Fatalf("applied = %+v", got)
	}
	stored := bootstrap.GetConfig().NetworkProxy
	if stored == nil || stored.Password == "" || strings.Contains(stored.Password, "secret") {
		t.Fatalf("stored password not encrypted: %+v", stored)
	}
	if plain, err := credential_svc.Default().Decrypt(stored.Password); err != nil || plain != "secret" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
}

func TestNetworkProxyEmptyPasswordKeepsSaved(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	if err := s.SaveNetworkProxySettings(customInput()); err != nil {
		t.Fatal(err)
	}
	in := customInput()
	in.Password = ""
	in.Port = 8080
	if err := s.SaveNetworkProxySettings(in); err != nil {
		t.Fatal(err)
	}
	if got := netproxy.Get(); got.Password != "secret" || got.Port != 8080 {
		t.Fatalf("applied = %+v", got)
	}
}

func TestNetworkProxyClearingUsernameDeletesPassword(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	if err := s.SaveNetworkProxySettings(customInput()); err != nil {
		t.Fatal(err)
	}
	in := customInput()
	in.Username, in.Password = "", ""
	if err := s.SaveNetworkProxySettings(in); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetNetworkProxySettings()
	if v.PasswordSet || v.Username != "" {
		t.Fatalf("view = %+v", v)
	}
	if stored := bootstrap.GetConfig().NetworkProxy; stored.Password != "" {
		t.Fatalf("password kept: %q", stored.Password)
	}
	if got := netproxy.Get(); got.Username != "" || got.Password != "" {
		t.Fatalf("applied = %+v", got)
	}
	// 再次填用户名但不填密码：旧密码已删除，不会复活。
	in.Username = "u2"
	if err := s.SaveNetworkProxySettings(in); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetNetworkProxySettings(); v.PasswordSet {
		t.Fatalf("password resurrected: %+v", v)
	}
}

func TestNetworkProxyRejectsInvalidAndLeavesConfigUnchanged(t *testing.T) {
	cases := map[string]func(*NetworkProxyInput){
		"empty host":  func(in *NetworkProxyInput) { in.Host = "" },
		"bad host":    func(in *NetworkProxyInput) { in.Host = "http://a b" },
		"no port":     func(in *NetworkProxyInput) { in.Port = 0 },
		"port range":  func(in *NetworkProxyInput) { in.Port = 70000 },
		"bad noproxy": func(in *NetworkProxyInput) { in.NoProxy = "ok.com\n10.0.0.0/99" },
		"bad type":    func(in *NetworkProxyInput) { in.Type = "ftp" },
		"bad mode":    func(in *NetworkProxyInput) { in.Mode = "weird" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
			if err := s.SaveNetworkProxySettings(customInput()); err != nil {
				t.Fatal(err)
			}
			before := netproxy.Get()
			in := customInput()
			in.Password = "other"
			mutate(&in)
			err := s.SaveNetworkProxySettings(in)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if strings.TrimSpace(err.Error()) == "" || strings.HasPrefix(err.Error(), "netproxy:") {
				t.Fatalf("message not user-facing: %q", err)
			}
			if netproxy.Get() != before {
				t.Fatalf("netproxy changed: %+v", netproxy.Get())
			}
			v, _ := s.GetNetworkProxySettings()
			if v.Host != "127.0.0.1" || v.Port != 7890 || v.Type != "socks5" {
				t.Fatalf("config changed: %+v", v)
			}
			if got, _ := credential_svc.Default().Decrypt(bootstrap.GetConfig().NetworkProxy.Password); got != "secret" {
				t.Fatalf("saved password changed to %q", got)
			}
		})
	}
}

func TestNetworkProxyRejectionMessageFollowsLanguage(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	in := customInput()
	in.Port = 0
	s.SetLanguage("en")
	en := s.SaveNetworkProxySettings(in)
	s.SetLanguage("zh-CN")
	zh := s.SaveNetworkProxySettings(in)
	if en == nil || zh == nil || en.Error() == zh.Error() {
		t.Fatalf("en=%v zh=%v", en, zh)
	}
}

func TestNetworkProxyNonCustomModeKeepsCustomFields(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	if err := s.SaveNetworkProxySettings(customInput()); err != nil {
		t.Fatal(err)
	}
	in := customInput()
	in.Mode, in.Password = "direct", ""
	if err := s.SaveNetworkProxySettings(in); err != nil {
		t.Fatal(err)
	}
	if netproxy.Get().Mode != netproxy.ModeDirect {
		t.Fatalf("applied = %+v", netproxy.Get())
	}
	v, _ := s.GetNetworkProxySettings()
	if v.Mode != "direct" || v.Host != "127.0.0.1" || !v.PasswordSet {
		t.Fatalf("view = %+v", v)
	}
}

func TestNetworkProxyEnvVarsMaskPasswordAndMatchWhatProcessReads(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, "")
	}
	t.Setenv("https_proxy", "http://bob:p%40ss@proxy.local:3128")
	t.Setenv("HTTP_PROXY", "proxy.local:3128")
	t.Setenv("ALL_PROXY", "socks5://x:y@z:1")
	v, err := s.GetNetworkProxySettings()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range v.EnvVars {
		got[e.Name] = e.Value
	}
	want := map[string]string{"https_proxy": "http://bob:***@proxy.local:3128", "HTTP_PROXY": "proxy.local:3128"} //nolint:gosec // masked placeholder, not a credential
	if len(got) != len(want) {
		t.Fatalf("envVars = %v", v.EnvVars)
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("envVars[%s] = %q, want %q (all %v)", k, got[k], w, v.EnvVars)
		}
	}
}

func TestApplyNetworkProxyAtStartup(t *testing.T) {
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	if err := s.SaveNetworkProxySettings(customInput()); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.GetConfig()
	netproxy.Set(netproxy.Default())
	if err := ApplyNetworkProxy(cfg); err != nil {
		t.Fatal(err)
	}
	if got := netproxy.Get(); got.Mode != netproxy.ModeCustom || got.Password != "secret" {
		t.Fatalf("applied = %+v", got)
	}
	// 无保存配置：默认 env。
	netproxy.Set(netproxy.Settings{Mode: netproxy.ModeDirect})
	if err := ApplyNetworkProxy(&bootstrap.AppConfig{}); err != nil {
		t.Fatal(err)
	}
	if netproxy.Get().Mode != netproxy.ModeEnv {
		t.Fatalf("applied = %+v", netproxy.Get())
	}
}

func TestApplyNetworkProxyUndecryptablePasswordReportsAndStaysOnProxy(t *testing.T) {
	setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	cfg := &bootstrap.AppConfig{NetworkProxy: &bootstrap.NetworkProxyConfig{
		Mode: "custom", Type: "http", Host: "127.0.0.1", Port: 7890, Username: "u", Password: "not-ciphertext", NoProxy: "x.com",
	}}
	if err := ApplyNetworkProxy(cfg); err == nil {
		t.Fatal("expected decrypt error")
	}
	got := netproxy.Get()
	if got.Mode != netproxy.ModeCustom || got.Host != "127.0.0.1" || got.Password != "" {
		t.Fatalf("applied = %+v", got)
	}
}
