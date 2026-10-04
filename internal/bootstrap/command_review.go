package bootstrap

import (
	"fmt"
	"net/http"

	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/pkg/typesafe"
	"github.com/opskat/opskat/internal/repository/command_review_repo"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// registerCommandReview 注册命令的模型审核服务。桌面端和 opsctl 都经 Init 走到这里，
// 两边用同一份设置和同一个数据库缓存。
func registerCommandReview() {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = netdial.Default().DialContext
	httpClient := &http.Client{Transport: transport}

	command_review_svc.Register(command_review_svc.New(
		CommandReviewConfig,
		command_review_svc.NewRepoCache(command_review_repo.CommandReview()),
		func(apiKey, baseURL string) command_review_svc.Evaluator {
			return typesafe.New(apiKey, typesafe.WithBaseURL(baseURL), typesafe.WithHTTPClient(httpClient))
		},
	))
}

// CommandReviewSettings 是 config.json 里的审核设置，不含 API key。
func CommandReviewSettings() command_review_svc.Settings {
	cfg := GetConfig()
	return command_review_svc.Settings{
		BaseURL:   cfg.CommandReviewBaseURL,
		Model:     cfg.CommandReviewModel,
		TimeoutMs: cfg.CommandReviewTimeoutMs,
		Threshold: cfg.CommandReviewThreshold,
	}
}

// CommandReviewConfig 是审核服务用的设置：config.json 里的设置加上解密后的 API key。
// 保存过的 API key 解不开时返回错误，审核按失败处理，不会因此放行。
func CommandReviewConfig() (command_review_svc.Config, error) {
	key, err := CommandReviewAPIKey()
	if err != nil {
		return command_review_svc.Config{}, err
	}
	s := CommandReviewSettings()
	s.APIKey = key
	return command_review_svc.NewConfig(s), nil
}

// CommandReviewAPIKey 返回解密后的审核 API key；没有配置时为空。
func CommandReviewAPIKey() (string, error) {
	encrypted := GetConfig().CommandReviewAPIKey
	if encrypted == "" {
		return "", nil
	}
	key, err := credential_svc.Default().Decrypt(encrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt command review api key: %w", err)
	}
	return key, nil
}
