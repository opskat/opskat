package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKafkaValidateCreateArgsRequiresSASLCredentials(t *testing.T) {
	handler := &kafkaHandler{}
	base := map[string]any{
		"brokers":        "kafka.example.com:9092",
		"sasl_mechanism": "plain",
	}

	err := handler.ValidateCreateArgs(base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username")

	base["username"] = "alice"
	err = handler.ValidateCreateArgs(base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password or credential_id")

	base["credential_id"] = float64(42)
	assert.NoError(t, handler.ValidateCreateArgs(base))
}

func TestKafkaApplyCreateArgsStoresManagedCredential(t *testing.T) {
	handler := &kafkaHandler{}
	asset := &asset_entity.Asset{Type: asset_entity.AssetTypeKafka}
	args := map[string]any{
		"brokers":        "kafka.example.com:9092",
		"sasl_mechanism": "plain",
		"username":       "alice",
		"credential_id":  float64(42),
	}

	require.NoError(t, handler.ApplyCreateArgs(context.Background(), asset, args))
	cfg, err := asset.GetKafkaConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(42), cfg.CredentialID)
}

func TestKafkaApplyUpdateArgsSwitchesToManagedCredential(t *testing.T) {
	handler := &kafkaHandler{}
	asset := &asset_entity.Asset{Type: asset_entity.AssetTypeKafka}
	require.NoError(t, asset.SetKafkaConfig(&asset_entity.KafkaConfig{
		Brokers:      []string{"kafka.example.com:9092"},
		CredentialID: 7,
	}))

	require.NoError(t, handler.ApplyUpdateArgs(context.Background(), asset, map[string]any{
		"credential_id": float64(42),
	}))
	cfg, err := asset.GetKafkaConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(42), cfg.CredentialID)
}
