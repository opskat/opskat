//go:build integration

package oss_svc

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/require"
)

// 需本地 MinIO(S3 兼容):MINIO_ENDPOINT / MINIO_ACCESS_KEY / MINIO_SECRET_KEY / MINIO_BUCKET。
func newIntegrationAdapter(t *testing.T) (Client, string) {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_ENDPOINT 未设置,跳过集成测")
	}
	cfg := &asset_entity.OSSConfig{
		Endpoint:     endpoint,
		AccessKeyID:  os.Getenv("MINIO_ACCESS_KEY"),
		UsePathStyle: true,
	}
	mc, err := connpool.DialOSS(cfg, os.Getenv("MINIO_SECRET_KEY"))
	require.NoError(t, err)
	return newMinioAdapter(mc), os.Getenv("MINIO_BUCKET")
}

func TestIntegrationPutGetRoundTripKeepsBytes(t *testing.T) {
	c, bucket := newIntegrationAdapter(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("opskat-oss-"), 5000)

	require.NoError(t, c.PutObject(ctx, bucket, "p3a/roundtrip.bin", bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"))

	rc, size, err := c.GetObject(ctx, bucket, "p3a/roundtrip.bin")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	require.Equal(t, int64(len(payload)), size)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.True(t, bytes.Equal(payload, got))

	require.NoError(t, c.RemoveObjects(ctx, bucket, []string{"p3a/roundtrip.bin"}))
}
