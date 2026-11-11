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

// 单源的目的端以目录形态结尾时，文件落在该目录下、保留源文件名（#364）。目录形态是
// 语法判定，和多源那一条同一套规则，不探测目的端；不以目录形态结尾时目的端就是字面落点。
func TestPlanTransferSingleSourceIntoDirectory(t *testing.T) {
	cases := []struct {
		name, source, destination string
		local                     bool
		want                      string
	}{
		{"remote directory", "/home/me/small.json", "/tmp/", false, "/tmp/small.json"},
		{"remote root", "/home/me/small.json", "/", false, "/small.json"},
		{"literal file", "/home/me/small.json", "/tmp/renamed.json", false, "/tmp/renamed.json"},
		{"remote source into local directory", "/var/log/app.log", "/Users/me/logs/", true, "/Users/me/logs/app.log"},
		{"object source into remote directory", "bucket/releases/app.tar", "/srv/", false, "/srv/app.tar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := PlanTransfer([]string{c.source}, c.destination, c.local, false)
			if err != nil {
				t.Fatalf("PlanTransfer: %v", err)
			}
			if plan.Destination != c.want {
				t.Fatalf("Destination = %q, want %q", plan.Destination, c.want)
			}
		})
	}

	if runtime.GOOS == "windows" {
		plan, err := PlanTransfer([]string{`C:\Users\me\small.json`}, `D:\backup\`, true, false)
		if err != nil {
			t.Fatalf("PlanTransfer: %v", err)
		}
		if plan.Destination != `D:\backup\small.json` {
			t.Fatalf(`Destination = %q, want D:\backup\small.json`, plan.Destination)
		}
	}
}
