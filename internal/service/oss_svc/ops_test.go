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
