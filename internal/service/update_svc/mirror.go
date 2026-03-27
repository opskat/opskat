package update_svc

import "strings"

// MirrorInfo 下载镜像信息
type MirrorInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"` // URL 前缀，空表示直连 GitHub
}

// availableMirrors 内置镜像列表
var availableMirrors = []MirrorInfo{
	{ID: "github", Name: "GitHub", URL: ""},
	{ID: "ghfast", Name: "ghfast.top", URL: "https://ghfast.top/"},
	{ID: "gh-proxy", Name: "gh-proxy.com", URL: "https://gh-proxy.com/"},
}

// GetAvailableMirrors 返回可用的下载镜像列表
func GetAvailableMirrors() []MirrorInfo {
	result := make([]MirrorInfo, len(availableMirrors))
	copy(result, availableMirrors)
	return result
}

// applyMirror 将镜像前缀应用到原始 URL
// mirrorPrefix 为空时返回原始 URL
func applyMirror(originalURL, mirrorPrefix string) string {
	if mirrorPrefix == "" {
		return originalURL
	}
	if !strings.HasSuffix(mirrorPrefix, "/") {
		mirrorPrefix += "/"
	}
	return mirrorPrefix + originalURL
}
