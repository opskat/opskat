package credential_svc

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/zalando/go-keyring"
	"go.uber.org/zap"
)

const (
	keychainService = "opskat"
	keychainAccount = "master-key"
	masterKeyLen    = 32 // 256-bit
	masterKeyFile   = "master.key"
)

// MasterKeyOptions 是 ResolveMasterKey 的入参。
type MasterKeyOptions struct {
	// Explicit 来自 CLI --master-key 或 OPSKAT_MASTER_KEY，非空则直接采用。
	Explicit string
	// DataDir master key 文件所在目录。
	DataDir string
	// NoKeychain 便携模式下为 true：master key 只落 <DataDir>/master.key，
	// 不读也不写 OS 凭据管理器。凭据管理器是机器本地的，便携目录换到另一台
	// 机器后会读不到 key 而重新生成，导致库内已加密凭据永久解不开。
	NoKeychain bool
}

// ResolveMasterKey 按优先级获取 master key:
//  1. opts.Explicit（CLI --master-key / 环境变量）
//  2. OS Keychain（opts.NoKeychain 为 true 时跳过）
//  3. 文件回退 (<DataDir>/master.key)
//
// 如果所有来源都没有，自动生成并存储。
func ResolveMasterKey(opts MasterKeyOptions) (string, error) {
	if opts.Explicit != "" {
		return opts.Explicit, nil
	}

	// 尝试从 Keychain 读取
	if !opts.NoKeychain {
		key, err := keyring.Get(keychainService, keychainAccount)
		if err == nil && key != "" {
			return key, nil
		}
	}

	// 尝试从文件读取
	filePath := filepath.Join(opts.DataDir, masterKeyFile)
	data, err := os.ReadFile(filePath) //nolint:gosec // path from app data directory
	if err == nil && len(data) > 0 {
		key := string(data)
		// 尝试同步到 Keychain（best-effort）
		if !opts.NoKeychain {
			if err := keyring.Set(keychainService, keychainAccount, key); err != nil {
				logger.Default().Warn("sync master key to keychain", zap.Error(err))
			}
		}
		return key, nil
	}

	// 自动生成新的 master key
	key, err := generateMasterKey()
	if err != nil {
		return "", fmt.Errorf("生成 master key 失败: %w", err)
	}

	// 便携模式直接落文件；否则优先 Keychain，不可用时回退文件
	if opts.NoKeychain {
		if err := os.WriteFile(filePath, []byte(key), 0600); err != nil {
			return "", fmt.Errorf("存储 master key 失败: %w", err)
		}
		return key, nil
	}
	if err := keyring.Set(keychainService, keychainAccount, key); err != nil {
		// Keychain 不可用，回退到文件存储
		if writeErr := os.WriteFile(filePath, []byte(key), 0600); writeErr != nil {
			return "", fmt.Errorf("存储 master key 失败（Keychain: %v, 文件: %w）", err, writeErr)
		}
	}

	return key, nil
}

// generateMasterKey 生成 32 字节随机密钥，返回 base64 编码
func generateMasterKey() (string, error) {
	buf := make([]byte, masterKeyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
