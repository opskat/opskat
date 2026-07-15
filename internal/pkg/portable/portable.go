// Package portable 判定应用是否运行在便携模式。
//
// 便携模式的约定：可执行文件同级存在 data 目录，则应用的全部状态
// （数据库、master key、配置、日志）都放在该目录内，使整个应用目录
// 可以整体搬迁到 U 盘或另一台机器。
//
// 本包只依赖标准库，因此 bootstrap / credential_svc / embedded 都能
// 导入它而不产生循环引用——credential_svc 无法反向导入 bootstrap，
// 这正是本包独立存在的原因。
package portable

import (
	"os"
	"path/filepath"
	"sync"
)

// dirName 便携数据目录的固定名称，不可配置。
const dirName = "data"

// dirFor 从可执行文件路径推导便携数据目录；非便携返回 ""。
// 从 Dir 拆出是为了可测：os.Executable() 在测试中指向临时测试二进制，无法构造。
func dirFor(exePath string) string {
	dir := filepath.Join(filepath.Dir(exePath), dirName)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// Dir 返回便携数据目录，非便携模式返回 ""。
//
// 结果在进程生命周期内只解析一次：便携与否是安装形态的属性，不会在
// 运行中改变，而 AppDataDir() 调用频繁，每次都 os.Executable() + os.Stat
// 并无意义。代价是运行中新建/删除 data 目录需重启才生效。
var Dir = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// 解析符号链接，使经 symlink 调用的 opsctl 也能定位到真实安装目录。
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return dirFor(exe)
})
