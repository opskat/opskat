package helper

import (
	"runtime"
	"testing"
)

// 多源传输的目的端必须写成目录：远端是 POSIX 路径，只认 "/" 结尾；本地目的端认本机的
// 路径分隔符，Windows 上 C:\backup\ 和 C:\backup/ 都是目录。
func TestPlanTransferDestinationMustBeADirectory(t *testing.T) {
	sources := []string{"/var/log/a.log", "/var/log/b.log"}

	if _, err := PlanTransfer(sources, "/srv/logs/", false, false); err != nil {
		t.Fatalf("remote destination ending with / rejected: %v", err)
	}
	if _, err := PlanTransfer(sources, "/srv/logs", false, false); err == nil {
		t.Fatal("remote destination without trailing / accepted")
	}
	if _, err := PlanTransfer(sources, `/srv/logs\`, false, false); err == nil {
		t.Fatal(`remote destination ending with \ accepted; remote paths are POSIX`)
	}
	if _, err := PlanTransfer([]string{"/var/log/a.log"}, "/srv/a.log", false, false); err != nil {
		t.Fatalf("single source needs no trailing /: %v", err)
	}

	if runtime.GOOS == "windows" {
		if _, err := PlanTransfer(sources, `C:\backup\`, true, false); err != nil {
			t.Fatalf(`local Windows destination ending with \ rejected: %v`, err)
		}
		if _, err := PlanTransfer(sources, `C:\backup`, true, false); err == nil {
			t.Fatal("local destination without a trailing separator accepted")
		}
	}
}
