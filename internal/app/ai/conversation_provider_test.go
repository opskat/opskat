package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
	"github.com/opskat/opskat/internal/model/entity/conversation_entity"
	"github.com/opskat/opskat/internal/repository/ai_provider_repo"
	"github.com/opskat/opskat/internal/repository/ai_provider_repo/mock_ai_provider_repo"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// TestAI_buildSendConfig 覆盖「按会话切换模型」(#246) 的 Provider 解析：会话选定的
// ProviderID 优先，缺省 / 缺失时回退全局激活 Provider。DecryptAPIKey 对空 APIKey 直接
// 返回 ("", nil)，因此这些用例无需初始化 credential_svc。
func TestAI_buildSendConfig(t *testing.T) {
	active := &ai_provider_entity.AIProvider{ID: 1, Type: "openai", Model: "gpt-4o"}
	newAI := func() *AI {
		return &AI{systemCfg: &runner.SystemConfig{
			ProviderEntity: active,
			APIKey:         "active-key",
			Cwd:            "/tmp/opskat-test",
		}}
	}

	t.Run("会话未指定 Provider(ID=0) 时用全局激活 Provider", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		// 注册一个无 EXPECT 的 mock：一旦被调用即测试失败，证明这条分支不查库。
		ai_provider_repo.RegisterAIProvider(mock_ai_provider_repo.NewMockAIProviderRepo(ctrl))

		cfg := newAI().buildSendConfig(context.Background(), &conversation_entity.Conversation{ID: 10, ProviderID: 0})
		assert.Equal(t, int64(1), cfg.ProviderEntity.ID)
		assert.Equal(t, "gpt-4o", cfg.Model)
		assert.Equal(t, "active-key", cfg.APIKey)
	})

	t.Run("会话 Provider 与全局激活相同时不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ai_provider_repo.RegisterAIProvider(mock_ai_provider_repo.NewMockAIProviderRepo(ctrl))

		cfg := newAI().buildSendConfig(context.Background(), &conversation_entity.Conversation{ID: 10, ProviderID: 1})
		assert.Equal(t, int64(1), cfg.ProviderEntity.ID)
		assert.Equal(t, "gpt-4o", cfg.Model)
	})

	t.Run("会话选定了另一个 Provider 时按其模型/密钥组装", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockRepo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
		ai_provider_repo.RegisterAIProvider(mockRepo)
		mockRepo.EXPECT().Find(gomock.Any(), int64(2)).
			Return(&ai_provider_entity.AIProvider{ID: 2, Type: "anthropic", Model: "claude-sonnet-5", APIKey: ""}, nil)

		cfg := newAI().buildSendConfig(context.Background(), &conversation_entity.Conversation{ID: 10, ProviderID: 2})
		assert.Equal(t, int64(2), cfg.ProviderEntity.ID)
		assert.Equal(t, "claude-sonnet-5", cfg.Model)
		assert.Equal(t, "", cfg.APIKey)
	})

	t.Run("会话选定的 Provider 已删除时回退全局激活 Provider", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockRepo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
		ai_provider_repo.RegisterAIProvider(mockRepo)
		mockRepo.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, errors.New("record not found"))

		cfg := newAI().buildSendConfig(context.Background(), &conversation_entity.Conversation{ID: 10, ProviderID: 99})
		assert.Equal(t, int64(1), cfg.ProviderEntity.ID)
		assert.Equal(t, "gpt-4o", cfg.Model)
		assert.Equal(t, "active-key", cfg.APIKey)
	})
}
