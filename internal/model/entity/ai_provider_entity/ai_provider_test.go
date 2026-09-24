package ai_provider_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAIProvider_ExtraHeadersRoundTrip(t *testing.T) {
	t.Run("保留用户填写的顺序", func(t *testing.T) {
		p := &AIProvider{}
		require.NoError(t, p.SetExtraHeaders([]ExtraHeader{
			{Name: "x-opencode-session", Value: "{{session}}"},
			{Name: "X-Trace", Value: "t-9"},
			{Name: "A-Last", Value: "3"},
		}))

		got, err := p.GetExtraHeaders()
		require.NoError(t, err)
		assert.Equal(t, []ExtraHeader{
			{Name: "x-opencode-session", Value: "{{session}}"},
			{Name: "X-Trace", Value: "t-9"},
			{Name: "A-Last", Value: "3"},
		}, got)
	})

	t.Run("未配置时取出空集合而不是报错", func(t *testing.T) {
		got, err := (&AIProvider{}).GetExtraHeaders()
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("写入空集合后列仍是空串，不留 null 字面量", func(t *testing.T) {
		p := &AIProvider{ExtraHeaders: `[{"name":"X","value":"1"}]`}
		require.NoError(t, p.SetExtraHeaders(nil))
		assert.Equal(t, "", p.ExtraHeaders)
	})

	t.Run("列里是坏数据时报错，不静默当成无配置", func(t *testing.T) {
		_, err := (&AIProvider{ExtraHeaders: "{not json"}).GetExtraHeaders()
		assert.Error(t, err)
	})
}
