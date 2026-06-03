//go:build windows

package localterm_svc

import (
	"os"
	"os/exec"
	"strings"
)

// DetectShells 探测本机:pwsh/powershell/cmd + Git Bash + WSL 发行版。
func DetectShells() []ShellInfo {
	var out []ShellInfo

	for _, name := range []string{"pwsh.exe", "powershell.exe", "cmd.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, ShellInfo{Name: name, Path: p})
		}
	}

	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, ShellInfo{Name: "Git Bash", Path: p, Args: []string{"--login", "-i"}})
			break
		}
	}

	if wsl, err := exec.LookPath("wsl.exe"); err == nil {
		for _, distro := range listWSLDistros(wsl) {
			out = append(out, ShellInfo{Name: "WSL: " + distro, Path: wsl, Args: []string{"-d", distro}})
		}
	}
	return out
}

// listWSLDistros 跑 `wsl -l -q` 列出已装发行版。wsl 输出是 UTF-16LE,
// v1 用"去 NUL + 去 CR + 按行切"解析(ASCII 名字够用;非 ASCII 后续再上正规 UTF-16 解码)。
func listWSLDistros(wslPath string) []string {
	raw, err := exec.Command(wslPath, "-l", "-q").Output()
	if err != nil {
		return nil
	}
	cleaned := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b != 0x00 && b != '\r' {
			cleaned = append(cleaned, b)
		}
	}
	var distros []string
	for _, line := range strings.Split(string(cleaned), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			distros = append(distros, line)
		}
	}
	return distros
}
