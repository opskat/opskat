package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 设置页每次都带上超时和阈值；和它的校验一致（frontend/src/components/settings/CommandReviewSection.tsx）。
func TestCommandReviewSaveInputValidate(t *testing.T) {
	filled := func(in CommandReviewSaveInput) CommandReviewSaveInput {
		if in.TimeoutMs == 0 {
			in.TimeoutMs = 5000
		}
		if in.Threshold == 0 {
			in.Threshold = 0.2
		}
		return in
	}
	valid := []CommandReviewSaveInput{
		{Model: "jev-1.13.0", TimeoutMs: 1000, Threshold: 0.3},
		{TimeoutMs: 60000, Threshold: 0.99},
		// 地址、模型没填时用默认值；模型名由用户自己填，不做限制
		filled(CommandReviewSaveInput{}),
		filled(CommandReviewSaveInput{Model: "jev-latest"}),
		filled(CommandReviewSaveInput{Model: "my-self-hosted-jev"}),
		filled(CommandReviewSaveInput{BaseURL: "https://api.typesafe.ai"}),
		filled(CommandReviewSaveInput{BaseURL: " http://10.0.0.5:8080/ "}),
	}
	for _, in := range valid {
		assert.NoError(t, in.validate(), "%+v", in)
	}

	invalid := []CommandReviewSaveInput{
		filled(CommandReviewSaveInput{BaseURL: "api.typesafe.ai"}),
		filled(CommandReviewSaveInput{BaseURL: "ftp://10.0.0.5"}),
		filled(CommandReviewSaveInput{BaseURL: "https://"}),
		{TimeoutMs: 999, Threshold: 0.2},
		{TimeoutMs: 60001, Threshold: 0.2},
		{TimeoutMs: 0, Threshold: 0.2},
		{TimeoutMs: 5000, Threshold: -0.1},
		{TimeoutMs: 5000, Threshold: 0}, // 阈值 0 会拦下所有命令，不会悄悄变成默认值
		{TimeoutMs: 5000, Threshold: 1},
	}
	for _, in := range invalid {
		assert.Error(t, in.validate(), "%+v", in)
	}
}

// 地址只在这里规范化一次：去掉首尾空白和末尾的 /，客户端再拼上 /v1/systemone。
func TestCommandReviewBaseURLIsNormalized(t *testing.T) {
	assert.Equal(t, "http://10.0.0.5:8080", CommandReviewSaveInput{BaseURL: " http://10.0.0.5:8080/ "}.baseURL())
	assert.Equal(t, "", CommandReviewSaveInput{}.baseURL())
}
