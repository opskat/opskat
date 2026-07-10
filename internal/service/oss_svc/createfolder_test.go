package oss_svc_test

import (
	"context"
	"io"
	"reflect"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// CreateFolder 规范化前缀为以 "/" 结尾,并做零字节 PUT。
func TestCreateFolderWithNormalizesAndZeroBytePut(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	// reflect.TypeOf((*io.Reader)(nil)).Elem() 取得 io.Reader 接口的 reflect.Type;
	// 直接传 (io.Reader)(nil) 会因嵌套 nil 接口被拍平成 nil any,导致 gomock 在
	// AssignableTo(nil) 上 panic,故按 gomock 文档写法传 reflect.Type。
	c.EXPECT().PutObject(
		gomock.Any(), "b", "docs/",
		gomock.AssignableToTypeOf(reflect.TypeOf((*io.Reader)(nil)).Elem()), int64(0), gomock.Any(),
	).Return(nil)

	err := oss_svc.CreateFolderWith(context.Background(), c, "b", "docs")
	require.NoError(t, err)
}

func TestCreateFolderWithRejectsEmptyPrefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	// 空前缀应在 PutObject 之前失败,故不设 PutObject 期望。
	err := oss_svc.CreateFolderWith(context.Background(), c, "b", "  ")
	require.Error(t, err)
}
