package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
)

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
