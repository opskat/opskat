package oss_svc

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

const defaultPresignExpiry = time.Hour

type Service struct{}

func New() *Service { return &Service{} }

func (s *Service) lookup(ctx context.Context, assetID int64) (*asset_entity.Asset, *asset_entity.OSSConfig, string, error) {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsOSS() {
		return nil, nil, "", fmt.Errorf("资产不是 OSS 类型")
	}
	cfg, err := asset.GetOSSConfig()
	if err != nil {
		return nil, nil, "", fmt.Errorf("获取 OSS 配置失败: %w", err)
	}
	secret, err := credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("解析 OSS 凭据失败: %w", err)
	}
	return asset, cfg, secret, nil
}

func (s *Service) connect(ctx context.Context, assetID int64) (Client, error) {
	asset, cfg, secret, err := s.lookup(ctx, assetID)
	if err != nil {
		return nil, err
	}
	mc, err := connpool.GetOrDialOSS(ctx, asset.ID, cfg, secret)
	if err != nil {
		return nil, err
	}
	return newMinioAdapter(mc, cfg.PartSizeMB), nil
}

func (s *Service) ListBuckets(ctx context.Context, assetID int64) ([]BucketItem, error) {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return c.ListBuckets(ctx)
}

func (s *Service) ListObjects(ctx context.Context, req *ListObjectsRequest) (*ListObjectsResult, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return nil, err
	}
	return listObjectsWith(ctx, c, req.Bucket, req.Prefix, req.MaxKeys, req.ContinuationToken)
}

func (s *Service) StatObject(ctx context.Context, req *ObjectRequest) (*ObjectItem, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return nil, err
	}
	item, err := c.StatObject(ctx, req.Bucket, req.Key)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Service) RemoveObject(ctx context.Context, req *ObjectRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return c.RemoveObject(ctx, req.Bucket, req.Key)
}

func (s *Service) CopyObject(ctx context.Context, req *CopyRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return copyObjectWith(ctx, c, req.SrcBucket, req.SrcKey, req.DstBucket, req.DstKey)
}

func (s *Service) MoveObject(ctx context.Context, req *CopyRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return moveObjectWith(ctx, c, req.SrcBucket, req.SrcKey, req.DstBucket, req.DstKey)
}

func (s *Service) RemoveObjects(ctx context.Context, req *RemoveObjectsRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return removeObjectsWith(ctx, c, req.Bucket, req.Keys)
}

func (s *Service) CreateFolder(ctx context.Context, req *CreateFolderRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return createFolderWith(ctx, c, req.Bucket, req.Prefix)
}

// PutObject 是给 app 层流式上传用的原语：connect 后把（通常已包进度）reader 写入对象。
func (s *Service) PutObject(ctx context.Context, assetID int64, bucket, key string, r io.Reader, size int64, contentType string) error {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return err
	}
	return c.PutObject(ctx, bucket, key, r, size, contentType)
}

// GetObject 是给 app 层流式下载用的原语:connect 后返回对象流 + 总大小。
func (s *Service) GetObject(ctx context.Context, assetID int64, bucket, key string) (io.ReadCloser, int64, error) {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return nil, 0, err
	}
	return c.GetObject(ctx, bucket, key)
}

func (s *Service) PresignGet(ctx context.Context, req *PresignRequest) (string, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return "", err
	}
	return c.PresignGet(ctx, req.Bucket, req.Key, presignExpiry(req.ExpirySecs))
}

func (s *Service) PresignPut(ctx context.Context, req *PresignRequest) (string, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return "", err
	}
	return c.PresignPut(ctx, req.Bucket, req.Key, presignExpiry(req.ExpirySecs))
}

// TestConnection 用已保存的资产拨号并列 Bucket 以验证凭证。
func (s *Service) TestConnection(ctx context.Context, assetID int64) error {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return err
	}
	_, err = c.ListBuckets(ctx)
	return err
}

// TestConfig 验证一份未保存的配置(表单"测试连接")。
func (s *Service) TestConfig(ctx context.Context, cfg *asset_entity.OSSConfig, secret string) error {
	mc, err := connpool.DialOSS(cfg, secret)
	if err != nil {
		return err
	}
	_, err = newMinioAdapter(mc).ListBuckets(ctx)
	return err
}

func presignExpiry(secs int) time.Duration {
	if secs <= 0 {
		return defaultPresignExpiry
	}
	return time.Duration(secs) * time.Second
}
