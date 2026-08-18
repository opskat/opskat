package connpool

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
	mongov1 "go.mongodb.org/mongo-driver/mongo"
	mongov1opts "go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

func configureMongoTransportV1(clientOpts *mongov1opts.ClientOptions, asset *asset_entity.Asset, cfg *asset_entity.MongoDBConfig, sshPool *sshpool.Pool) (*SSHTunnel, error) {
	tunnelID := asset.SSHTunnelID
	if tunnelID == 0 {
		tunnelID = cfg.SSHAssetID // backward compat
	}
	if cfg.ProxyChain != nil {
		dial, err := chainDialFunc(context.Background(), cfg.ProxyChain)
		if err != nil {
			return nil, err
		}
		clientOpts.SetDialer(&mongoChainDialer{dial: dial})
		return nil, nil
	}
	if tunnelID > 0 && sshPool != nil {
		var host string
		var port int
		var err error
		if cfg.ConnectionURI != "" {
			host, port, err = parseHostFromURI(cfg.ConnectionURI)
			if err != nil {
				return nil, fmt.Errorf("解析 MongoDB URI 失败: %w", err)
			}
		} else {
			host = cfg.Host
			port = cfg.Port
		}
		tunnel := NewSSHTunnel(tunnelID, host, port, sshPool)
		clientOpts.SetDialer(&mongoTunnelDialer{tunnel: tunnel})
		clientOpts.SetDirect(true)
		return tunnel, nil
	}
	if cfg.Proxy != nil {
		clientOpts.SetDialer(&mongoProxyDialer{proxy: cfg.Proxy})
	}
	return nil, nil
}

func dialMongoDBV1(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.MongoDBConfig, password string, sshPool *sshpool.Pool) (*MongoClientCloser, io.Closer, error) {
	var uri string
	if cfg.ConnectionURI != "" {
		uri = injectPassword(cfg.ConnectionURI, password)
	} else {
		uri = buildMongoURI(cfg, password)
	}

	clientOpts := mongov1opts.Client().ApplyURI(uri)

	if cfg.TLS {
		clientOpts.SetTLSConfig(&tls.Config{})
	}

	tunnel, err := configureMongoTransportV1(clientOpts, asset, cfg, sshPool)
	if err != nil {
		return nil, nil, err
	}

	client, err := mongov1.Connect(ctx, clientOpts)
	if err != nil {
		if tunnel != nil {
			if closeErr := tunnel.Close(); closeErr != nil {
				logger.Default().Warn("close ssh tunnel", zap.Error(closeErr))
			}
		}
		return nil, nil, fmt.Errorf("MongoDB 连接失败: %w", err)
	}

	if pingErr := client.Ping(ctx, nil); pingErr != nil {
		if disconnectErr := client.Disconnect(context.Background()); disconnectErr != nil {
			logger.Default().Warn("disconnect mongodb client", zap.Error(disconnectErr))
		}
		if tunnel != nil {
			if closeErr := tunnel.Close(); closeErr != nil {
				logger.Default().Warn("close ssh tunnel", zap.Error(closeErr))
			}
		}
		return nil, nil, fmt.Errorf("MongoDB 连接失败: %w", pingErr)
	}

	if tunnel == nil {
		return &MongoClientCloser{Legacy: true, V1: client}, nil, nil
	}
	return &MongoClientCloser{Legacy: true, V1: client}, tunnel, nil
}
