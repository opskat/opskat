package credential_svc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestResolveMasterKey(t *testing.T) {
	t.Run("显式传入时直接返回，不碰文件与凭据管理器", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()

		got, err := ResolveMasterKey(MasterKeyOptions{Explicit: "explicit-key", DataDir: dataDir})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got != "explicit-key" {
			t.Errorf("ResolveMasterKey() = %q, 期望 %q", got, "explicit-key")
		}
		if _, err := os.Stat(filepath.Join(dataDir, masterKeyFile)); !os.IsNotExist(err) {
			t.Error("显式传入 key 时不应写 master.key 文件")
		}
	})

	t.Run("便携模式生成 key 时只落文件，凭据管理器保持为空", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir, NoKeychain: true})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got == "" {
			t.Fatal("ResolveMasterKey() 返回空 key")
		}

		data, err := os.ReadFile(filepath.Join(dataDir, masterKeyFile)) //nolint:gosec // path is t.TempDir() from this test
		if err != nil {
			t.Fatalf("便携模式应把 key 写入 master.key: %v", err)
		}
		if string(data) != got {
			t.Errorf("master.key 内容 = %q, 期望与返回值 %q 一致", string(data), got)
		}

		// 核心断言：便携模式绝不能把 key 写进凭据管理器，
		// 否则数据目录换机器后读不到 key，库内凭据全部解不开。
		if _, err := keyring.Get(keychainService, keychainAccount); err == nil {
			t.Error("便携模式不应向凭据管理器写入 master key")
		}
	})

	t.Run("便携模式读到已有文件时不回写凭据管理器", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dataDir, masterKeyFile), []byte("existing-key"), 0o600); err != nil {
			t.Fatalf("准备 master.key 失败: %v", err)
		}

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir, NoKeychain: true})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got != "existing-key" {
			t.Errorf("ResolveMasterKey() = %q, 期望 %q", got, "existing-key")
		}
		if _, err := keyring.Get(keychainService, keychainAccount); err == nil {
			t.Error("便携模式读文件后不应同步到凭据管理器")
		}
	})

	t.Run("非便携模式生成 key 时写入凭据管理器（回归保护）", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}

		stored, err := keyring.Get(keychainService, keychainAccount)
		if err != nil {
			t.Fatalf("非便携模式应把 key 写入凭据管理器: %v", err)
		}
		if stored != got {
			t.Errorf("凭据管理器中 = %q, 期望与返回值 %q 一致", stored, got)
		}
	})

	t.Run("非便携模式优先读凭据管理器（回归保护）", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()
		if err := keyring.Set(keychainService, keychainAccount, "keychain-key"); err != nil {
			t.Fatalf("准备凭据管理器失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, masterKeyFile), []byte("file-key"), 0o600); err != nil {
			t.Fatalf("准备 master.key 失败: %v", err)
		}

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got != "keychain-key" {
			t.Errorf("ResolveMasterKey() = %q, 期望 %q（凭据管理器优先于文件）", got, "keychain-key")
		}
	})
}
