package backup_svc

import (
	"sort"
	"strings"
	"time"
)

// 自动备份文件命名（WebDAV 与本地文件夹共用）：opskat-backup-auto-<UTC 时间戳>.encrypted.json。
// 时间戳用 UTC，字典序即时间序；手动备份文件 opskat-backup.encrypted.json 不匹配，永远不会被清理。
const (
	autoBackupPrefix     = "opskat-backup-auto-"
	autoBackupSuffix     = ".encrypted.json"
	autoBackupTimeLayout = "20060102T150405Z"
	// autoBackupTempPrefix 是写入过程中的临时文件名前缀；不匹配任何备份命名，不会出现在恢复列表。
	autoBackupTempPrefix = ".opskat-backup-auto-"
)

// AutoBackupName 返回 at 时刻的自动备份文件名。
func AutoBackupName(at time.Time) string {
	return autoBackupPrefix + at.UTC().Format(autoBackupTimeLayout) + autoBackupSuffix
}

// AutoBackupTempName 返回写入 name 时使用的临时文件名。
func AutoBackupTempName(name string) string {
	return autoBackupTempPrefix + strings.TrimPrefix(name, autoBackupPrefix) + ".tmp"
}

// ParseAutoBackupName 解析自动备份文件名中的时间；不是自动备份文件名时返回 false。
func ParseAutoBackupName(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, autoBackupPrefix) || !strings.HasSuffix(name, autoBackupSuffix) {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, autoBackupPrefix), autoBackupSuffix)
	t, err := time.Parse(autoBackupTimeLayout, stamp)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// AutoBackupsToPrune 从 names 中选出超出 keep 份的旧自动备份（从旧到新）；其他文件从不入选。
func AutoBackupsToPrune(names []string, keep int) []string {
	type entry struct {
		name string
		at   time.Time
	}
	var autos []entry
	for _, n := range names {
		if at, ok := ParseAutoBackupName(n); ok {
			autos = append(autos, entry{n, at})
		}
	}
	if len(autos) <= keep {
		return nil
	}
	sort.Slice(autos, func(i, j int) bool { return autos[i].at.Before(autos[j].at) })
	out := make([]string, 0, len(autos)-keep)
	for _, e := range autos[:len(autos)-keep] {
		out = append(out, e.name)
	}
	return out
}
