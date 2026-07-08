package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSSConfigRoundTrip(t *testing.T) {
	a := &Asset{Type: AssetTypeOSS}
	cfg := &OSSConfig{
		Provider: "s3", Endpoint: "s3.us-east-1.amazonaws.com", Region: "us-east-1",
		AccessKeyID: "AKIA", SecretAccessKey: "cipher", UseSSL: true,
	}
	require.NoError(t, a.SetOSSConfig(cfg))

	got, err := a.GetOSSConfig()
	require.NoError(t, err)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", got.Endpoint)
	assert.Equal(t, "us-east-1", got.Region)
	assert.True(t, got.UseSSL)
}

func TestOSSConfigPasswordSource(t *testing.T) {
	cfg := &OSSConfig{CredentialID: 7, SecretAccessKey: "cipher"}
	assert.Equal(t, int64(7), cfg.GetCredentialID())
	assert.Equal(t, "cipher", cfg.GetPassword())
}

func TestGetOSSConfigWrongType(t *testing.T) {
	a := &Asset{Type: AssetTypeSSH}
	_, err := a.GetOSSConfig()
	require.Error(t, err)
}
