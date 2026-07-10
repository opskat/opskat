package connpool

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"go.uber.org/zap"
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
	// use_path_style is an explicit addressing-mode choice. BucketLookupAuto
	// falls back to path-style for providers it does not recognize (including
	// Tencent COS), so it would silently ignore the user's virtual-hosted choice.
	lookup := minio.BucketLookupDNS
	if cfg.UsePathStyle {
		lookup = minio.BucketLookupPath
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.ConnectTimeout > 0 {
		timeout := time.Duration(cfg.ConnectTimeout) * time.Second
		transport.DialContext = (&net.Dialer{Timeout: timeout}).DialContext
		transport.TLSHandshakeTimeout = timeout
	}
	if cfg.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit user setting for private S3-compatible endpoints
	}
	return endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, secret, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
		Transport:    transport,
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
func GetOrDialOSS(ctx context.Context, assetID int64, cfg *asset_entity.OSSConfig, secret string) (*minio.Client, error) {
	logger.Ctx(ctx).Info("oss connection open start", zap.Int64("assetId", assetID))
	globalOSSPool.mu.Lock()
	defer globalOSSPool.mu.Unlock()
	if c, ok := globalOSSPool.clients[assetID]; ok {
		logger.Ctx(ctx).Info("oss connection open end", zap.Int64("assetId", assetID), zap.Bool("cached", true))
		return c, nil
	}
	c, err := DialOSS(cfg, secret)
	if err != nil {
		logger.Ctx(ctx).Error("oss connection open fail", zap.Int64("assetId", assetID), zap.Error(err))
		return nil, err
	}
	if assetID > 0 {
		globalOSSPool.clients[assetID] = c
	}
	logger.Ctx(ctx).Info("oss connection open end", zap.Int64("assetId", assetID), zap.Bool("cached", false))
	return c, nil
}

// InvalidateOSS 丢弃某资产的缓存客户端(配置更新/删除时调用)。
func InvalidateOSS(assetID int64) {
	logger.Default().Info("oss connection close start", zap.Int64("assetId", assetID))
	globalOSSPool.mu.Lock()
	delete(globalOSSPool.clients, assetID)
	globalOSSPool.mu.Unlock()
	logger.Default().Info("oss connection close end", zap.Int64("assetId", assetID))
}
