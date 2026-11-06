package oss_svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opskat/opskat/internal/pkg/transfer"
)

// UploadFileRequest 描述一次单文件上传:把本地 LocalPath 写成对象 Bucket/Key。
type UploadFileRequest struct {
	AssetID     int64
	Bucket      string
	Key         string
	LocalPath   string
	ContentType string
	TransferID  string
	OnProgress  func(transfer.Progress)
}

// UploadFile 把一个本地文件流式上传为对象,整次传输一条进度。
func (s *Service) UploadFile(ctx context.Context, req *UploadFileRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return uploadFileWith(ctx, c, req)
}

func uploadFileWith(ctx context.Context, c Client, req *UploadFileRequest) error {
	f, err := os.Open(req.LocalPath)
	if err != nil {
		return fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("获取文件信息失败: %w", err)
	}

	// 交给 minio 的源保留 ReadAt:大于分片的文件因此走并行分片上传,而不是逐片串行读写。
	file := transfer.NewTracker(req.TransferID, 1, info.Size(), req.OnProgress).File(filepath.Base(req.LocalPath), info.Size())
	if err := c.PutObject(ctx, req.Bucket, req.Key, file.ReaderAt(ctx, f), info.Size(), req.ContentType); err != nil {
		return err
	}
	file.Done()
	return nil
}
