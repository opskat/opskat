package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
	"github.com/opskat/opskat/internal/repository/ai_provider_repo"
	"github.com/opskat/opskat/internal/repository/ai_provider_repo/mock_ai_provider_repo"
	"github.com/opskat/opskat/internal/repository/conversation_repo"
	"github.com/opskat/opskat/internal/repository/conversation_repo/mock_conversation_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type providerTestLang struct{}

func (providerTestLang) Lang() string { return "en" }

// spec Decision 7（AI Provider DTO/form 测试行）：AIProviderInfo 只保留完整 apiKey，
// 删除 maskedApiKey。序列化 JSON 必须携带原始 apiKey 且不得出现 maskedApiKey 键。
func TestAIProviderInfoJSONCarriesOriginalAPIKeyAndNoMasked(t *testing.T) {
	p := &ai_provider_entity.AIProvider{
		ID:               1,
		Name:             "test",
		Type:             "openai",
		APIBase:          "https://api.openai.com/v1",
		Model:            "gpt-4o",
		ReasoningEnabled: true,
		ReasoningEffort:  "medium",
	}
	const key = "sk-abc1234567890secretXYZ"
	info := toProviderInfo(p, key)

	if info.APIKey != key {
		t.Fatalf("APIKey must be the original value, got %q", info.APIKey)
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal AIProviderInfo: %v", err)
	}
	raw := string(data)
	if !strings.Contains(raw, `"apiKey":"`+key+`"`) {
		t.Fatalf("AIProviderInfo JSON must carry the original apiKey, got %s", raw)
	}
	if strings.Contains(raw, "maskedApiKey") {
		t.Fatalf("AIProviderInfo JSON must not contain maskedApiKey, got %s", raw)
	}
}

func TestAIProviderQueriesReturnAPIKeyDecryptionErrors(t *testing.T) {
	oldRepo := ai_provider_repo.AIProvider()
	oldCredentialSvc := credential_svc.Default()
	t.Cleanup(func() {
		ai_provider_repo.RegisterAIProvider(oldRepo)
		credential_svc.SetDefault(oldCredentialSvc)
	})
	credential_svc.SetDefault(credential_svc.New("provider-test", make([]byte, 16)))

	provider := &ai_provider_entity.AIProvider{ID: 7, Name: "broken", APIKey: "not-base64"}
	a := &AI{ctx: context.Background(), lang: providerTestLang{}}

	t.Run("list", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
		ai_provider_repo.RegisterAIProvider(repo)
		repo.EXPECT().List(gomock.Any()).Return([]*ai_provider_entity.AIProvider{provider}, nil)

		got, err := a.ListAIProviders()

		require.ErrorContains(t, err, "解密 Provider API Key")
		require.Nil(t, got)
	})

	t.Run("active", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
		ai_provider_repo.RegisterAIProvider(repo)
		repo.EXPECT().GetActive(gomock.Any()).Return(provider, nil)

		got, err := a.GetActiveAIProvider()

		require.ErrorContains(t, err, "解密 Provider API Key")
		require.Nil(t, got)
	})
}

func TestGetActiveAIProviderReturnsLookupErrors(t *testing.T) {
	oldRepo := ai_provider_repo.AIProvider()
	t.Cleanup(func() { ai_provider_repo.RegisterAIProvider(oldRepo) })

	ctrl := gomock.NewController(t)
	repo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
	ai_provider_repo.RegisterAIProvider(repo)
	repo.EXPECT().GetActive(gomock.Any()).Return(nil, errors.New("database offline"))

	got, err := (&AI{ctx: context.Background(), lang: providerTestLang{}}).GetActiveAIProvider()

	require.ErrorContains(t, err, "获取激活 Provider 失败")
	require.Nil(t, got)
}

func TestInitAIProviderHandlesNoActiveProvider(t *testing.T) {
	oldRepo := ai_provider_repo.AIProvider()
	t.Cleanup(func() { ai_provider_repo.RegisterAIProvider(oldRepo) })

	ctrl := gomock.NewController(t)
	repo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
	ai_provider_repo.RegisterAIProvider(repo)
	repo.EXPECT().GetActive(gomock.Any()).Return(nil, nil)

	a := &AI{ctx: context.Background(), lang: providerTestLang{}}
	require.NotPanics(t, a.InitAIProvider)
	require.Nil(t, a.systemCfg)
}

func TestCreateConversationReturnsActiveProviderLookupError(t *testing.T) {
	oldProviderRepo := ai_provider_repo.AIProvider()
	oldConversationRepo := conversation_repo.Conversation()
	t.Cleanup(func() {
		ai_provider_repo.RegisterAIProvider(oldProviderRepo)
		conversation_repo.RegisterConversation(oldConversationRepo)
	})

	ctrl := gomock.NewController(t)
	providerRepo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
	ai_provider_repo.RegisterAIProvider(providerRepo)
	providerRepo.EXPECT().GetActive(gomock.Any()).Return(nil, errors.New("database offline"))
	conversation_repo.RegisterConversation(mock_conversation_repo.NewMockConversationRepo(ctrl))

	a := &AI{ctx: context.Background(), lang: providerTestLang{}, systemCfg: &runner.SystemConfig{}}
	got, err := a.CreateConversation()

	require.ErrorContains(t, err, "获取激活 Provider 失败")
	require.Nil(t, got)
}
