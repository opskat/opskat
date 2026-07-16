package command

import (
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// getSSHProxyClient 检测 sshpool.sock 是否可用，返回 client 或 nil（fallback 直连）
func getSSHProxyClient() *sshpool.Client {
	dataDir := bootstrap.ResolvedDataDir()
	sockPath := sshpool.SocketPath(dataDir)
	token, err := bootstrap.ReadAuthToken(dataDir)
	if err != nil {
		logger.Default().Warn("read auth token", zap.Error(err))
	}
	client := sshpool.NewClientWithToken(sockPath, token)
	if client.IsAvailable() {
		return client
	}
	return nil
}
