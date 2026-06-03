//go:build windows

package localterm_svc

import (
	"os"
	"os/exec"
)

// DetectShells 探测本机:pwsh/powershell/cmd + Git Bash + WSL 发行版。
func DetectShells() []ShellInfo {
	var out []ShellInfo

	for _, s := range []struct{ exe, name string }{
		{"pwsh.exe", "PowerShell"},
		{"powershell.exe", "Windows PowerShell"},
		{"cmd.exe", "Command Prompt"},
	} {
		if p, err := exec.LookPath(s.exe); err == nil {
			out = append(out, ShellInfo{Name: s.name, Path: p})
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

// listWSLDistros 跑 `wsl -l -q` 列出已装发行版,解析委托给 parseWSLOutput。
func listWSLDistros(wslPath string) []string {
	raw, err := exec.Command(wslPath, "-l", "-q").Output()
	if err != nil {
		return nil
	}
	return parseWSLOutput(raw)
}
