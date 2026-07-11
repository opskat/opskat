package oss

import (
	"context"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testLangProvider struct{}

func (testLangProvider) Lang() string { return "zh-CN" }

func TestDeriveUploadKeyJoinsPrefixAndBase(t *testing.T) {
	assert.Equal(t, "images/hero.jpg", deriveUploadKey("images", "/Users/me/pics/hero.jpg"), "non-slash prefix should be normalized with a single separator")
	assert.Equal(t, "images/hero.jpg", deriveUploadKey("images/", "/Users/me/pics/hero.jpg"), "already-slash prefix should not gain a double slash")
	assert.Equal(t, "hero.jpg", deriveUploadKey("", "/Users/me/pics/hero.jpg"), "empty prefix should upload to bucket root as bare filename")
}

// TestOSSCancelTransferInvokesRegisteredCancelFunc 验证注册表命中语义:不起真实传输,
// 直接把一个哨兵 CancelFunc 存进 cancels,断言 OSSCancelTransfer 确实 Load 并调用了它。
func TestOSSCancelTransferInvokesRegisteredCancelFunc(t *testing.T) {
	o := &OSS{}
	called := false
	o.cancels.Store("oss-1", context.CancelFunc(func() { called = true }))

	err := o.OSSCancelTransfer("oss-1")

	require.NoError(t, err)
	assert.True(t, called, "expected registered CancelFunc to be invoked")
	// 命中后不做二次清理断言:downloadObject/uploadObject 自身的 defer 负责 Delete，
	// OSSCancelTransfer 只管调用 CancelFunc。
}

// TestOSSCancelTransferUnknownIDIsNoop 未命中的 transferID(已终结或从未存在)视为幂等 no-op，不报错。
func TestOSSCancelTransferUnknownIDIsNoop(t *testing.T) {
	o := &OSS{}
	err := o.OSSCancelTransfer("does-not-exist")
	assert.NoError(t, err)
}

func TestOSSCancelTransferValidatesEmptyID(t *testing.T) {
	o := &OSS{}
	err := o.OSSCancelTransfer("")
	assert.Error(t, err)
}

func TestOSSStartTransferRunsPendingWorkExactlyOnce(t *testing.T) {
	o := &OSS{ctx: context.Background(), lang: testLangProvider{}}
	started := make(chan struct{}, 1)
	o.pending.Store("oss-1", func() { started <- struct{}{} })
	require.NoError(t, o.OSSStartTransfer("oss-1"))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pending transfer was not started")
	}
	require.Error(t, o.OSSStartTransfer("oss-1"))
}

func TestOSSDownloadObjectValidatesInput(t *testing.T) {
	o := &OSS{service: oss_svc.New()}
	_, err := o.OSSDownloadObject(0, "b", "k")
	require.Error(t, err)
	_, err = o.OSSDownloadObject(1, "", "k")
	require.Error(t, err)
	_, err = o.OSSDownloadObject(1, "b", "")
	require.Error(t, err)
}
