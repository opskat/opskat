package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/service/credential_svc"
)

func useCommandReviewConfig(t *testing.T, cfg *AppConfig) *credential_svc.CredentialSvc {
	t.Helper()
	origCfg, origCred := appConfig, credential_svc.Default()
	t.Cleanup(func() {
		appConfig = origCfg
		credential_svc.SetDefault(origCred)
	})
	cred := credential_svc.New("master-key", []byte("0123456789abcdef"))
	credential_svc.SetDefault(cred)
	appConfig = cfg
	return cred
}

func TestCommandReviewConfigDecryptsAPIKey(t *testing.T) {
	cred := useCommandReviewConfig(t, &AppConfig{CommandReviewModel: "jev-x"})
	encrypted, err := cred.Encrypt("tsk-123")
	require.NoError(t, err)
	appConfig.CommandReviewAPIKey = encrypted

	cfg, err := CommandReviewConfig()
	require.NoError(t, err)
	assert.Equal(t, "tsk-123", cfg.APIKey)
	assert.Equal(t, "jev-x", cfg.Model)
}

// 保存过的 API key 解不开（比如换了主密钥）要报出来，不能当成没配置。
func TestCommandReviewConfigReportsUndecryptableAPIKey(t *testing.T) {
	useCommandReviewConfig(t, &AppConfig{CommandReviewAPIKey: "not-a-ciphertext", CommandReviewModel: "jev-x"})

	_, err := CommandReviewConfig()
	assert.Error(t, err)

	// 其余设置照样读得到，设置页据此显示
	assert.Equal(t, "jev-x", CommandReviewSettings().Model)
}
