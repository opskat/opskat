package backup_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

const (
	githubClientID     = "Ov23liydPaSoHVMqecz9"
	gistBackupFilename = "opskat-backup.encrypted.json"
	// githubTokenInvalidMarker 跨 Wails IPC 传递明确的凭据失效状态。
	// 只有 GitHub API 返回 401 时才能使用；网络错误、5xx 和限流都不代表 token 失效。
	githubTokenInvalidMarker = "[GITHUB_TOKEN_INVALID]"
)

// 可在测试中覆盖的 base URL
var (
	githubBaseURL    = "https://github.com"
	githubAPIBaseURL = "https://api.github.com"
)

// DeviceFlowInfo Device Flow 初始化返回
type DeviceFlowInfo struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURI string `json:"verificationUri"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

// GitHubUser GitHub 用户信息
type GitHubUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

// GistInfo Gist 概要信息
type GistInfo struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	UpdatedAt   string `json:"updatedAt"`
	HTMLURL     string `json:"htmlUrl"`
}

// StartDeviceFlow 发起 GitHub Device Flow
func StartDeviceFlow() (*DeviceFlowInfo, error) {
	data := url.Values{
		"client_id": {githubClientID},
		"scope":     {"gist"},
	}
	req, err := http.NewRequest("POST", githubBaseURL+"/login/device/code", strings.NewReader(data.Encode()))
	if err != nil {
		logger.Default().Warn("create device code request", zap.Error(err))
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub Device Flow 失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	var result struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("GitHub 错误: %s", result.ErrorDesc)
	}

	interval := result.Interval
	if interval < 5 {
		interval = 5
	}

	return &DeviceFlowInfo{
		DeviceCode:      result.DeviceCode,
		UserCode:        result.UserCode,
		VerificationURI: result.VerificationURI,
		ExpiresIn:       result.ExpiresIn,
		Interval:        interval,
	}, nil
}

// PollDeviceAuth 轮询 Device Flow 授权结果，返回 access_token
func PollDeviceAuth(ctx context.Context, deviceCode string, interval int) (string, error) {
	currentInterval := time.Duration(interval) * time.Second
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("授权已取消")
		case <-ticker.C:
			token, slowDown, done, err := pollOnce(deviceCode)
			if err != nil {
				return "", err
			}
			if done {
				return token, nil
			}
			if slowDown {
				currentInterval += 5 * time.Second
				ticker.Reset(currentInterval)
			}
		}
	}
}

func pollOnce(deviceCode string) (token string, slowDown bool, done bool, err error) {
	data := url.Values{
		"client_id":   {githubClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	req, err := http.NewRequest("POST", githubBaseURL+"/login/oauth/access_token", strings.NewReader(data.Encode()))
	if err != nil {
		logger.Default().Warn("create access token request", zap.Error(err))
		return "", false, false, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, false, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, false, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != 200 {
		logger.Default().Warn("GitHub device auth poll non-200",
			zap.Int("status", resp.StatusCode), zap.String("body", string(body)))
		return "", false, false, fmt.Errorf("GitHub 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		logger.Default().Warn("GitHub device auth poll parse error",
			zap.Error(err), zap.String("body", string(body)))
		return "", false, false, fmt.Errorf("解析响应失败: %w", err)
	}

	logger.Default().Info("GitHub device auth poll",
		zap.String("error", result.Error), zap.Bool("hasToken", result.AccessToken != ""))

	switch result.Error {
	case "":
		if result.AccessToken == "" {
			logger.Default().Warn("GitHub returned empty token", zap.String("body", string(body)))
			return "", false, false, fmt.Errorf("GitHub 返回空 token")
		}
		return result.AccessToken, false, true, nil
	case "authorization_pending":
		return "", false, false, nil
	case "slow_down":
		return "", true, false, nil
	case "expired_token":
		return "", false, false, fmt.Errorf("授权码已过期，请重新发起")
	case "access_denied":
		return "", false, false, fmt.Errorf("用户拒绝了授权")
	default:
		return "", false, false, fmt.Errorf("GitHub 错误: %s", result.ErrorDesc)
	}
}

// GetGitHubUser 获取当前用户信息
func GetGitHubUser(token string) (*GitHubUser, error) {
	req, err := http.NewRequest("GET", githubAPIBaseURL+"/user", nil)
	if err != nil {
		logger.Default().Warn("create GitHub user request", zap.Error(err))
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s GitHub API 错误: %d", githubTokenInvalidMarker, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 错误: %d", resp.StatusCode)
	}

	var user struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("解析用户信息失败: %w", err)
	}

	return &GitHubUser{Login: user.Login, AvatarURL: user.AvatarURL}, nil
}

// CreateOrUpdateGist 创建或更新 Gist
// gistID 为空时创建新 Gist，否则更新已有 Gist
func CreateOrUpdateGist(token, gistID string, content []byte) (*GistInfo, error) {
	body := map[string]interface{}{
		"description": fmt.Sprintf("OpsKat Backup - %s", time.Now().Format("2006-01-02 15:04")),
		"public":      false,
		"files": map[string]interface{}{
			gistBackupFilename: map[string]string{
				"content": string(content),
			},
		},
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Gist 请求失败: %w", err)
	}

	var method, apiURL string
	if gistID == "" {
		method = "POST"
		apiURL = githubAPIBaseURL + "/gists"
	} else {
		method = "PATCH"
		apiURL = fmt.Sprintf("%s/gists/%s", githubAPIBaseURL, gistID)
	}

	req, err := http.NewRequest(method, apiURL, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			logger.Default().Warn("read GitHub error response body", zap.Error(readErr))
		}
		return nil, fmt.Errorf("GitHub API 错误 %d: %s", resp.StatusCode, string(respBody))
	}

	var gist struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		UpdatedAt   string `json:"updated_at"`
		HTMLURL     string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &GistInfo{
		ID:          gist.ID,
		Description: gist.Description,
		UpdatedAt:   gist.UpdatedAt,
		HTMLURL:     gist.HTMLURL,
	}, nil
}

// GetGistContent 读取 Gist 中的备份内容
func GetGistContent(token, gistID string) ([]byte, error) {
	apiURL := fmt.Sprintf("%s/gists/%s", githubAPIBaseURL, gistID)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		logger.Default().Warn("create gist content request", zap.Error(err))
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API 错误: %d", resp.StatusCode)
	}

	var gist struct {
		Files map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	file, ok := gist.Files[gistBackupFilename]
	if !ok {
		return nil, fmt.Errorf("gist 中未找到备份文件 %s", gistBackupFilename)
	}

	return []byte(file.Content), nil
}

// ListBackupGists 列出用户的 OpsKat 备份 Gist
func ListBackupGists(token string) ([]*GistInfo, error) {
	req, err := http.NewRequest("GET", githubAPIBaseURL+"/gists?per_page=100", nil)
	if err != nil {
		logger.Default().Warn("create list gists request", zap.Error(err))
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API 错误: %d", resp.StatusCode)
	}

	var gists []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		UpdatedAt   string `json:"updated_at"`
		HTMLURL     string `json:"html_url"`
		Files       map[string]struct {
			Filename string `json:"filename"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gists); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	var result []*GistInfo
	for _, g := range gists {
		if _, ok := g.Files[gistBackupFilename]; ok {
			result = append(result, &GistInfo{
				ID:          g.ID,
				Description: g.Description,
				UpdatedAt:   g.UpdatedAt,
				HTMLURL:     g.HTMLURL,
			})
		}
	}
	return result, nil
}
