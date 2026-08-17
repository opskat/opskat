package command

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

const sessionMaxAge = 24 * time.Hour

// sessionDataDir 返回 session 文件所在的 data dir（与 opskat.db / master.key /
// config.json 同处）。变量而非直接调用，是为了让 session 解析与写入在测试里
// 指向 t.TempDir()，不碰真实数据目录。
var sessionDataDir = bootstrap.ResolvedDataDir

// sessionFilePath 返回 data dir 里的唯一 session 文件路径。
func sessionFilePath(dataDir string) string {
	return filepath.Join(dataDir, "session.id")
}

// resolveSessionID 解析当前生效的会话 ID（data dir 级单例）。
// 文件不存在、内容无效或按 mtime 已过 24 小时都返回空串。
func resolveSessionID() string {
	return readSessionFile(sessionFilePath(sessionDataDir()))
}

// readSessionFile 读取 session 文件；过期时顺带移除文件本身。
func readSessionFile(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	// Check expiry by file modification time
	if time.Since(info.ModTime()) > sessionMaxAge {
		if err := os.Remove(path); err != nil {
			logger.Default().Warn("remove expired session file", zap.String("path", path), zap.Error(err))
		}
		return ""
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from the data dir
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if len(id) < 8 {
		return ""
	}
	return id
}

// writeActiveSession 把会话 ID 写成 data dir 里的唯一 session 文件。
// data dir 由 bootstrap.Init 建好，这里不重复创建。
func writeActiveSession(id string) error {
	return os.WriteFile(sessionFilePath(sessionDataDir()), []byte(id+"\n"), 0644)
}
