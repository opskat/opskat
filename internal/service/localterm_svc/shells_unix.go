//go:build !windows

package localterm_svc

import (
	"bufio"
	"os"
	"strings"
)

// DetectShells 探测本机可用 shell:$SHELL(默认优先)+ /etc/shells(系统权威清单)。
func DetectShells() []ShellInfo {
	seen := map[string]bool{}
	var out []ShellInfo
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		fi, err := os.Stat(path) //nolint:gosec // G703: path 来自 /etc/shells 与 $SHELL，非不可信输入
		if err != nil || fi.IsDir() {
			return
		}
		seen[path] = true
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		out = append(out, ShellInfo{Name: name, Path: path})
	}

	add(os.Getenv("SHELL"))

	if f, err := os.Open("/etc/shells"); err == nil {
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			add(line)
		}
	}
	return out
}
