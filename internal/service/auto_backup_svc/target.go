package auto_backup_svc

import (
	"context"
	"time"
)

// Target 是一个备份位置。新位置实现它并在所在文件的 init() 里 RegisterTarget。
type Target interface {
	// ID 是持久化在设置里的位置标识（webdav / gist / local）。
	ID() string
	// Available 报告位置当前是否已配置好（WebDAV 已配置、GitHub 已登录、已选文件夹）；
	// 不可用的位置在检查时跳过，不记为错误。
	Available(s Settings) bool
	// Validate 检查勾选这个位置时它需要的设置项（如本地文件夹路径）。
	Validate(s Settings) error
	// Write 写入一份加密备份；返回位置需要跨次记住的引用（如自动备份 Gist 的 ID）。
	Write(ctx context.Context, req WriteRequest) (ref string, err error)
}

// WriteRequest 是一次写入的输入。
type WriteRequest struct {
	Content  []byte    // 已加密的备份内容
	At       time.Time // 本次写入时间，用于命名
	Keep     int       // 保留份数
	Settings Settings
	Ref      string // 上次 Write 返回的引用
}

var registry []Target

// RegisterTarget 注册一个备份位置。
func RegisterTarget(t Target) { registry = append(registry, t) }

// Targets 返回已注册的备份位置。
func Targets() []Target { return registry }
