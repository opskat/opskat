package oss_svc_test

import (
	"context"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListObjectsWithSplitsPrefixesAndObjects(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "images/").Return([]oss_svc.ObjectItem{
		{Key: "images/thumbnails/", IsPrefix: true},
		{Key: "images/hero.jpg", Size: 2516480},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "images/")
	require.NoError(t, err)
	assert.Equal(t, []string{"images/thumbnails/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "images/hero.jpg", res.Objects[0].Key)
}

// TestListObjectsWithEmptyBucketReturnsEmptySlicesNotNil 空 bucket/前缀下 ListObjects 应
// 序列化为 JSON "[]" 而非 "null" —— 前端对 prefixes/objects 做 .map(...) 遇到 null 会抛错。
func TestListObjectsWithEmptyBucketReturnsEmptySlicesNotNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "empty-bucket", "").Return([]oss_svc.ObjectItem{}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "empty-bucket", "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
	assert.NotNil(t, res.Prefixes)
	assert.NotNil(t, res.Objects)
}
