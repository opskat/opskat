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

func page(items ...oss_svc.ObjectItem) *oss_svc.ListObjectsPage {
	return &oss_svc.ListObjectsPage{Items: items}
}

func TestListObjectsWithSplitsPrefixesAndObjects(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "images/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "images/thumbnails/", IsPrefix: true},
		oss_svc.ObjectItem{Key: "images/hero.jpg", Size: 2516480},
	), nil)

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
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "docs/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "docs/", IsPrefix: true},
		oss_svc.ObjectItem{Key: "docs/archive/", IsPrefix: true},
		oss_svc.ObjectItem{Key: "docs/readme.pdf", Size: 1024},
	), nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "docs/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/archive/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/readme.pdf", res.Objects[0].Key)
}

func TestListObjectsWithEmptyFolderMarkerReturnsEmptyListing(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "empty/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "empty/", IsPrefix: true},
	), nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "empty/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
}

func TestListObjectsWithOnlyOmitsExactCurrentPrefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "parent/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "parent/", IsPrefix: true},
		oss_svc.ObjectItem{Key: "parent-empty/", IsPrefix: true},
		oss_svc.ObjectItem{Key: "parent/empty/", IsPrefix: true},
	), nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "parent/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"parent-empty/", "parent/empty/"}, res.Prefixes)
}

// 空 bucket/前缀应序列化为 JSON "[]" 而非 "null"。
func TestListObjectsWithEmptyBucketReturnsEmptySlicesNotNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "empty-bucket", "", 200, "").Return(page(), nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "empty-bucket", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
	assert.NotNil(t, res.Prefixes)
	assert.NotNil(t, res.Objects)
}

func TestListObjectsWithPreservesOpaqueContinuationToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "docs/", 2, "page-1").Return(&oss_svc.ListObjectsPage{
		Items: []oss_svc.ObjectItem{
			{Key: "docs/archive/", IsPrefix: true},
			{Key: "docs/b.md", Size: 20},
		},
		IsTruncated: true, NextContinuationToken: "page-2",
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "docs/", 2, "page-1")
	require.NoError(t, err)
	assert.True(t, res.IsTruncated)
	assert.Equal(t, "page-2", res.NextContinuationToken)
	assert.Equal(t, []string{"docs/archive/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/b.md", res.Objects[0].Key)
}

func TestListObjectsWithSortsContentsAndPrefixesForStableOutput(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "", 3, "").Return(page(
		oss_svc.ObjectItem{Key: "z.txt", Size: 1},
		oss_svc.ObjectItem{Key: "p2/", IsPrefix: true},
		oss_svc.ObjectItem{Key: "p1/", IsPrefix: true},
	), nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "", 3, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"p1/", "p2/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "z.txt", res.Objects[0].Key)
}
