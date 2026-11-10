package system

import (
	"fmt"
	"os"
	"path/filepath"
)

// LocalPathStat 是一个本地路径的元数据，供上传前的同名冲突对话框展示「将要写入」一侧。
// ModTime 为 Unix 毫秒；取不到时 Error 带原因，其余字段为零值。
type LocalPathStat struct {
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Error   string `json:"error,omitempty"`
}

// StatLocalPaths 逐个读取本地路径的大小与修改时间（不跟随结果做任何写操作）。
// 单项失败只记在该项的 Error 上，不影响其余项。
func (s *System) StatLocalPaths(paths []string) ([]LocalPathStat, error) {
	result := make([]LocalPathStat, 0, len(paths))
	for _, p := range paths {
		result = append(result, statLocalPath(p))
	}
	return result, nil
}

func statLocalPath(p string) LocalPathStat {
	stat := LocalPathStat{Path: p}
	if p == "" || !filepath.IsAbs(p) {
		stat.Error = fmt.Sprintf("invalid local path %q: must be absolute", p)
		return stat
	}
	info, err := os.Stat(p)
	if err != nil {
		stat.Error = err.Error()
		return stat
	}
	stat.IsDir = info.IsDir()
	if !stat.IsDir {
		stat.Size = info.Size()
	}
	stat.ModTime = info.ModTime().UnixMilli()
	return stat
}
