package oss

import (
	"testing"

	"github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/stretchr/testify/require"
)

func TestOSSListObjectsValidatesInput(t *testing.T) {
	o := &OSS{service: oss_svc.New()}
	_, err := o.OSSListObjects(oss_svc.ListObjectsRequest{AssetID: 0, Bucket: "b"})
	require.Error(t, err)
	_, err = o.OSSListObjects(oss_svc.ListObjectsRequest{AssetID: 1, Bucket: ""})
	require.Error(t, err)
}

func TestOSSListBucketsValidatesInput(t *testing.T) {
	o := &OSS{service: oss_svc.New()}
	_, err := o.OSSListBuckets(0)
	require.Error(t, err)
}
