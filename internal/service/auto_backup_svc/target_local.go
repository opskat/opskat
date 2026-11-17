package auto_backup_svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/service/backup_svc"
)

// localTarget 把自动备份写到用户选的文件夹（可以是 iCloud / OneDrive 等同步目录）。
type localTarget struct{}

func init() { RegisterTarget(localTarget{}) }

func (localTarget) ID() string { return "local" }

func (localTarget) Available(s Settings) bool { return s.LocalDir != "" }

func (localTarget) Destination(s Settings) string { return s.LocalDir }

func (localTarget) Validate(s Settings) error {
	if s.LocalDir == "" {
		return fmt.Errorf("请选择备份文件夹")
	}
	return nil
}

func (localTarget) Write(_ context.Context, req WriteRequest) (string, error) {
	dir := req.Settings.LocalDir
	// 文件夹不存在时不创建：可能是未挂载的外接盘或同步目录。
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("文件夹不存在: %s", dir)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("不是文件夹: %s", dir)
	}

	name := backup_svc.AutoBackupName(req.At)
	tmp := filepath.Join(dir, backup_svc.AutoBackupTempName(name))
	// 写到一半失败（如磁盘满）或改名失败都删掉临时文件：每次的临时文件名不同，留下就不会再被清理。
	if err := os.WriteFile(tmp, req.Content, 0o600); err != nil {
		removeLocalTemp(tmp)
		return "", fmt.Errorf("写入备份文件失败: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		removeLocalTemp(tmp)
		return "", fmt.Errorf("写入备份文件失败: %w", err)
	}

	if err := pruneLocal(dir, req.Keep); err != nil {
		logger.Default().Warn("auto backup: prune local folder", zap.String("dir", dir), zap.Error(err))
	}
	return "", nil
}

func removeLocalTemp(tmp string) {
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Default().Warn("auto backup: remove temp file", zap.String("path", tmp), zap.Error(err))
	}
}

func pruneLocal(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	for _, name := range backup_svc.AutoBackupsToPrune(names, keep) {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}
