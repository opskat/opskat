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
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "images/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "images/thumbnails/", IsPrefix: true},
		{Key: "images/hero.jpg", Size: 2516480},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "images/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"images/thumbnails/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "images/hero.jpg", res.Objects[0].Key)
	assert.False(t, res.IsTruncated)
	assert.Empty(t, res.NextContinuationToken)
}

func TestListObjectsWithOmitsCurrentPrefixFolderMarker(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "docs/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "docs/", IsPrefix: true},
		{Key: "docs/archive/", IsPrefix: true},
		{Key: "docs/readme.pdf", Size: 1024},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "docs/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/archive/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/readme.pdf", res.Objects[0].Key)
}

func TestListObjectsWithEmptyFolderMarkerReturnsEmptyListing(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "empty/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "empty/", IsPrefix: true},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "empty/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
}

// 空 bucket/前缀应序列化为 JSON "[]" 而非 "null"。
func TestListObjectsWithEmptyBucketReturnsEmptySlicesNotNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "empty-bucket", "", 200, "").Return([]oss_svc.ObjectItem{}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "empty-bucket", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
	assert.NotNil(t, res.Prefixes)
	assert.NotNil(t, res.Objects)
}

// maxKeys+1 项 → 截断:丢掉最后一项,next = 第 maxKeys 项的 Key。
func TestListObjectsWithTruncatesAndSetsNextCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	// maxKeys=2,adapter 会多读 1 项;这里 mock 直接返回 3 项模拟"还有下一页"。
	c.EXPECT().ListObjects(gomock.Any(), "b", "docs/", 2, "").Return([]oss_svc.ObjectItem{
		{Key: "docs/a.md", Size: 10},
		{Key: "docs/b.md", Size: 20},
		{Key: "docs/c.md", Size: 30},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "docs/", 2, "")
	require.NoError(t, err)
	assert.True(t, res.IsTruncated)
	assert.Equal(t, "docs/b.md", res.NextContinuationToken)
	require.Len(t, res.Objects, 2)
	assert.Equal(t, "docs/a.md", res.Objects[0].Key)
	assert.Equal(t, "docs/b.md", res.Objects[1].Key)
}

// 续读:startAfter 透传给 Client;不足一页则不截断。
func TestListObjectsWithResumeCursorNotTruncated(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "docs/", 2, "docs/b.md").Return([]oss_svc.ObjectItem{
		{Key: "docs/c.md", Size: 30},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "docs/", 2, "docs/b.md")
	require.NoError(t, err)
	assert.False(t, res.IsTruncated)
	assert.Empty(t, res.NextContinuationToken)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/c.md", res.Objects[0].Key)
}
