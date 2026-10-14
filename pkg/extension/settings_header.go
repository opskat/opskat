package extension

import (
	"fmt"
	"net/url"
	"strings"
)

// SettingPlaceholder 是密钥字段在请求头里的占位。扩展拿不到明文，只拿这个占位写进请求头，
// 宿主发出请求前替换成密钥。
func SettingPlaceholder(field string) string {
	return "{{setting." + field + "}}"
}

func firstSettingPlaceholder(headers map[string]string) string {
	for _, v := range headers {
		start := strings.Index(v, "{{setting.")
		if start < 0 {
			continue
		}
		end := strings.Index(v[start:], "}}")
		if end < 0 {
			return "{{setting.*}}"
		}
		return v[start : start+end+2]
	}
	return "{{setting.*}}"
}

// SubstituteSettingHeaders 把请求头里的 {{setting.<field>}} 换成对应密钥。
// 只允许发往 settingsSchema 里 format:"endpoint" 字段所声明的地址；别的地址带着占位就拒绝，
// 避免密钥被发到扩展自己拼出来的主机。没有占位的请求原样通过。
func SubstituteSettingHeaders(rawURL string, headers, secrets map[string]string, endpoints []string) (map[string]string, error) {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	needs := false
	for _, v := range out {
		if strings.Contains(v, "{{setting.") {
			needs = true
			break
		}
	}
	if !needs {
		return out, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("substitute settings header: %w", err)
	}
	if !urlMatchesEndpoints(u, endpoints) {
		return nil, fmt.Errorf("substitute settings header: %s is only sent to a declared endpoint, not %s", firstSettingPlaceholder(out), u.Host)
	}
	for k, v := range out {
		replaced := v
		for field, secret := range secrets {
			replaced = strings.ReplaceAll(replaced, SettingPlaceholder(field), secret)
		}
		if strings.Contains(replaced, "{{setting.") {
			return nil, fmt.Errorf("substitute settings header: %s still contains an unknown {{setting.*}} placeholder", k)
		}
		out[k] = replaced
	}
	return out, nil
}

// urlMatchesEndpoints 判断目标是否落在某个已声明的 endpoint 上（scheme + host + port）。
func urlMatchesEndpoints(target *url.URL, endpoints []string) bool {
	for _, raw := range endpoints {
		ep, err := url.Parse(raw)
		if err != nil || ep.Host == "" {
			continue
		}
		if strings.EqualFold(ep.Scheme, target.Scheme) && strings.EqualFold(ep.Host, target.Host) {
			return true
		}
	}
	return false
}
