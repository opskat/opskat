package system

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/app/i18n"
)

func TestSetLanguage_NormalizesToLowerCase(t *testing.T) {
	s := New(context.Background(), SkillContent{})
	s.SetLanguage("zh-CN")
	assert.Equal(t, "zh-cn", s.Lang())
	assert.Equal(t, "zh-cn", s.GetLanguage())
}

func TestSetLanguage_SelectsLocalizedText(t *testing.T) {
	s := New(context.Background(), SkillContent{})

	s.SetLanguage("en")
	assert.Equal(t, "English", i18n.Pick(s.Lang(), "中文", "English"))
	assert.False(t, policy.IsZh(i18n.Ctx(context.Background(), s.Lang())))

	s.SetLanguage("zh-CN")
	assert.Equal(t, "中文", i18n.Pick(s.Lang(), "中文", "English"))
	assert.True(t, policy.IsZh(i18n.Ctx(context.Background(), s.Lang())))
}
