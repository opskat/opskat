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

func TestCopyObjectWithForwardsArgs(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().CopyObject(gomock.Any(), "src", "a/x.txt", "dst", "b/y.txt").Return(nil)

	err := oss_svc.CopyObjectWith(context.Background(), c, "src", "a/x.txt", "dst", "b/y.txt")
	require.NoError(t, err)
}

// Move = copy 成功后再删源;顺序必须 copy→remove。
func TestMoveObjectWithCopiesThenRemovesInOrder(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	copyCall := c.EXPECT().CopyObject(gomock.Any(), "b", "old.txt", "b", "new.txt").Return(nil)
	c.EXPECT().RemoveObject(gomock.Any(), "b", "old.txt").Return(nil).After(copyCall)

	err := oss_svc.MoveObjectWith(context.Background(), c, "b", "old.txt", "b", "new.txt")
	require.NoError(t, err)
}

// copy 失败绝不接着 delete —— 未对 RemoveObject 设期望,若被调用 gomock 会 fail。
func TestMoveObjectWithCopyFailsDoesNotRemove(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().CopyObject(gomock.Any(), "b", "old.txt", "b", "new.txt").Return(errors.New("access denied"))

	err := oss_svc.MoveObjectWith(context.Background(), c, "b", "old.txt", "b", "new.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}
