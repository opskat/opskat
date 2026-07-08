package connpool

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// buildMinioOptions 从 OSS 配置 + 解密后的密钥推导 minio 端点与选项(纯函数,单测)。
func buildMinioOptions(cfg *asset_entity.OSSConfig, secret string) (string, *minio.Options, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return "", nil, fmt.Errorf("oss endpoint is empty")
	}
	secure := cfg.UseSSL
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return "", nil, fmt.Errorf("parse oss endpoint: %w", err)
		}
		secure = u.Scheme == "https"
		endpoint = u.Host
	}
	lookup := minio.BucketLookupAuto
	if cfg.UsePathStyle {
		lookup = minio.BucketLookupPath
	}
	return endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, secret, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	}, nil
}

// DialOSS 新建一个 minio 客户端(HTTP 客户端,无需显式 Close)。
func DialOSS(cfg *asset_entity.OSSConfig, secret string) (*minio.Client, error) {
	endpoint, opts, err := buildMinioOptions(cfg, secret)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("dial oss: %w", err)
	}
	return client, nil
}

type ossPool struct {
	mu      sync.Mutex
	clients map[int64]*minio.Client
}

var globalOSSPool = &ossPool{clients: map[int64]*minio.Client{}}

// GetOrDialOSS 返回缓存的 minio 客户端,没有则新建并缓存。
func GetOrDialOSS(_ context.Context, assetID int64, cfg *asset_entity.OSSConfig, secret string) (*minio.Client, error) {
	globalOSSPool.mu.Lock()
	defer globalOSSPool.mu.Unlock()
	if c, ok := globalOSSPool.clients[assetID]; ok {
		return c, nil
	}
	c, err := DialOSS(cfg, secret)
	if err != nil {
		return nil, err
	}
	if assetID > 0 {
		globalOSSPool.clients[assetID] = c
	}
	return c, nil
}

// InvalidateOSS 丢弃某资产的缓存客户端(配置更新/删除时调用)。
func InvalidateOSS(assetID int64) {
	globalOSSPool.mu.Lock()
	delete(globalOSSPool.clients, assetID)
	globalOSSPool.mu.Unlock()
}
