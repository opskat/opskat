package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
)

// ModelInfo 从 API 返回的模型信息
type ModelInfo struct {
	ID string `json:"id"`
}

// FetchModelsOptions 拉取模型列表的入参。
type FetchModelsOptions struct {
	ProviderType string
	APIBase      string
	APIKey       string
	// ExtraHeaders 该 Provider 配置的自定义请求头。拉列表不属于任何会话，
	// 值里的 {{session}} 展开为一次性随机标识：要求会话头的网关照样能返回列表，
	// 又不给对端留一个可以跨次跟踪的固定标识。
	ExtraHeaders []ai_provider_entity.ExtraHeader
}

// FetchModels 从 API 获取可用模型列表
func FetchModels(opts FetchModelsOptions) ([]ModelInfo, error) {
	headers := resolveExtraHeaders(opts.ExtraHeaders, oneOffSessionID())
	switch opts.ProviderType {
	case "anthropic":
		return fetchAnthropicModels(opts.APIBase, opts.APIKey, headers)
	default:
		return fetchOpenAIModels(opts.APIBase, opts.APIKey, headers)
	}
}

// applyExtraHeaders 在鉴权头之后写，Set 覆盖同名值；保留头已在 IPC 边界拒掉，
// 这里不会出现自定义头挤掉鉴权头的情况。
func applyExtraHeaders(req *http.Request, headers map[string]string) {
	for name, value := range headers {
		req.Header.Set(name, value)
	}
}

func fetchOpenAIModels(apiBase, apiKey string, headers map[string]string) ([]ModelInfo, error) {
	if apiBase == "" {
		apiBase = "https://api.openai.com/v1"
	}
	apiBase = strings.TrimRight(apiBase, "/")

	req, err := http.NewRequest("GET", apiBase+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	applyExtraHeaders(req, headers)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{ID: m.ID})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models, nil
}

func fetchAnthropicModels(apiBase, apiKey string, headers map[string]string) ([]ModelInfo, error) {
	if apiBase == "" {
		apiBase = "https://api.anthropic.com"
	}
	apiBase = strings.TrimRight(apiBase, "/")

	req, err := http.NewRequest("GET", apiBase+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	applyExtraHeaders(req, headers)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{ID: m.ID})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models, nil
}
