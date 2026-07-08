package connpool

import (
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMinioOptionsPlainHostSSL(t *testing.T) {
	ep, opts, err := buildMinioOptions(&asset_entity.OSSConfig{
		Endpoint: "s3.us-east-1.amazonaws.com", Region: "us-east-1", UseSSL: true, AccessKeyID: "AKIA",
	}, "sk")
	require.NoError(t, err)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", ep)
	assert.True(t, opts.Secure)
	assert.Equal(t, "us-east-1", opts.Region)
	assert.Equal(t, minio.BucketLookupAuto, opts.BucketLookup)
}

func TestBuildMinioOptionsSchemeStrippedPathStyle(t *testing.T) {
	ep, opts, err := buildMinioOptions(&asset_entity.OSSConfig{
		Endpoint: "http://127.0.0.1:9000", UsePathStyle: true,
	}, "sk")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9000", ep)
	assert.False(t, opts.Secure)
	assert.Equal(t, minio.BucketLookupPath, opts.BucketLookup)
}

func TestBuildMinioOptionsEmptyEndpoint(t *testing.T) {
	_, _, err := buildMinioOptions(&asset_entity.OSSConfig{}, "sk")
	require.Error(t, err)
}
