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
	"github.com/stretchr/testify/assert"
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
	info, err := toProviderInfo(p, key)
	require.NoError(t, err)

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

// IPC 是边界，自定义请求头在这里做校验：用户看得见的拒绝理由必须在保存之前给出，
// 而不是等到请求发出去被网关拒。
func TestNormalizeExtraHeaders(t *testing.T) {
	t.Run("保留顺序并原样透传", func(t *testing.T) {
		got, err := normalizeExtraHeaders([]ExtraHeaderInput{
			{Name: "x-opencode-session", Value: "{{session}}"},
			{Name: "X-Trace", Value: "t-9"},
		})
		assert.NoError(t, err)
		assert.Equal(t, []ai_provider_entity.ExtraHeader{
			{Name: "x-opencode-session", Value: "{{session}}"},
			{Name: "X-Trace", Value: "t-9"},
		}, got)
	})

	t.Run("名称为空的条目被丢弃，不报错", func(t *testing.T) {
		got, err := normalizeExtraHeaders([]ExtraHeaderInput{
			{Name: "  ", Value: "ignored"},
			{Name: "X-Keep", Value: "1"},
			{Name: "", Value: ""},
		})
		assert.NoError(t, err)
		assert.Equal(t, []ai_provider_entity.ExtraHeader{{Name: "X-Keep", Value: "1"}}, got)
	})

	t.Run("名称前后空白被裁掉", func(t *testing.T) {
		got, err := normalizeExtraHeaders([]ExtraHeaderInput{{Name: "  X-Pad  ", Value: " v "}})
		assert.NoError(t, err)
		assert.Equal(t, []ai_provider_entity.ExtraHeader{{Name: "X-Pad", Value: " v "}}, got,
			"值不裁：有网关要求带空格的字面量")
	})

	t.Run("大小写不同的重名被拒绝", func(t *testing.T) {
		_, err := normalizeExtraHeaders([]ExtraHeaderInput{
			{Name: "x-opencode-session", Value: "a"},
			{Name: "X-Opencode-Session", Value: "b"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "X-Opencode-Session")
	})

	t.Run("OpsKat 自己设置的头被拒绝", func(t *testing.T) {
		for _, name := range []string{"Authorization", "x-api-key", "ANTHROPIC-VERSION", "Content-Type"} {
			_, err := normalizeExtraHeaders([]ExtraHeaderInput{{Name: name, Value: "x"}})
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), name)
		}
	})

	t.Run("未配置时得到空集合", func(t *testing.T) {
		got, err := normalizeExtraHeaders(nil)
		assert.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestNormalizeExtraHeadersRejectsUnsendableHeaders(t *testing.T) {
	t.Run("名称不是合法 HTTP token 时在保存前拒绝", func(t *testing.T) {
		_, err := normalizeExtraHeaders([]ExtraHeaderInput{{Name: "X Opencode Session", Value: "v"}})
		require.Error(t, err, "存下去只会让之后每次对话都死在 net/http: invalid header field name")
		assert.Contains(t, err.Error(), "X Opencode Session")
	})

	t.Run("值里有换行时在保存前拒绝", func(t *testing.T) {
		_, err := normalizeExtraHeaders([]ExtraHeaderInput{{Name: "X-Trace", Value: "a\r\nX-Injected: 1"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "X-Trace")
	})

	t.Run("展开占位符后的值仍然合法", func(t *testing.T) {
		got, err := normalizeExtraHeaders([]ExtraHeaderInput{{Name: "x-opencode-session", Value: "{{session}}"}})
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})
}

func TestToProviderInfoSurfacesCorruptExtraHeaders(t *testing.T) {
	_, err := toProviderInfo(&ai_provider_entity.AIProvider{ID: 7, Type: "openai", ExtraHeaders: "{not json"}, "k")
	require.Error(t, err, "展示成\"没配过\"会诱导用户再保存一次，把库里的配置直接抹掉")
}
