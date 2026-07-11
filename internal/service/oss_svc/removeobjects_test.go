package oss_svc_test

import (
	"context"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestRemoveObjectsWithForwardsKeys(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	keys := []string{"logs/1.txt", "logs/2.txt", "logs/3.txt"}
	c.EXPECT().RemoveObjects(gomock.Any(), "b", keys).Return(nil)

	err := oss_svc.RemoveObjectsWith(context.Background(), c, "b", keys)
	require.NoError(t, err)
}
