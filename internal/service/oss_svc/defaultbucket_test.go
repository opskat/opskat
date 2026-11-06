package oss_svc_test

import (
	"context"
	"errors"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// 限定到桶的令牌(如 R2 的对象级令牌)没有 ListBuckets 权限:配了默认桶就不得再向存储端列桶。
func TestListBucketsWithDefaultBucketSkipsRemoteListing(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl) // 未声明 ListBuckets 期望:一旦被调用即失败

	got, err := oss_svc.ListBucketsWith(context.Background(), c, "ootd")
	require.NoError(t, err)
	assert.Equal(t, []oss_svc.BucketItem{{Name: "ootd"}}, got)
}

func TestListBucketsWithoutDefaultBucketListsRemote(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListBuckets(gomock.Any()).Return([]oss_svc.BucketItem{{Name: "a"}, {Name: "b"}}, nil)

	got, err := oss_svc.ListBucketsWith(context.Background(), c, "")
	require.NoError(t, err)
	assert.Equal(t, []oss_svc.BucketItem{{Name: "a"}, {Name: "b"}}, got)
}

func TestProbeWithDefaultBucketListsThatBucketOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "ootd", "", 1, "").Return(&oss_svc.ListObjectsPage{}, nil)

	require.NoError(t, oss_svc.ProbeWith(context.Background(), c, "ootd"))
}

// 默认桶不存在或无权限时原样报错,不回退去列桶——回退会把配置错误藏在另一个报错后面。
func TestProbeWithDefaultBucketSurfacesErrorWithoutFallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	denied := errors.New("NoSuchBucket")
	c.EXPECT().ListObjects(gomock.Any(), "ootd", "", 1, "").Return(nil, denied)

	require.ErrorIs(t, oss_svc.ProbeWith(context.Background(), c, "ootd"), denied)
}

func TestProbeWithoutDefaultBucketListsBuckets(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	denied := errors.New("Access Denied")
	c.EXPECT().ListBuckets(gomock.Any()).Return(nil, denied)

	require.ErrorIs(t, oss_svc.ProbeWith(context.Background(), c, ""), denied)
}
