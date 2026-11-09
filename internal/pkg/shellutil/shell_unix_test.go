//go:build !windows

package shellutil

import "testing"

func TestDefaultShell_UsesSHELLEnvOrFallsBackToBinSh(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/fish")
	if got := DefaultShell(); got != "/usr/local/bin/fish" {
		t.Errorf("DefaultShell() = %q, want SHELL env value", got)
	}

	t.Setenv("SHELL", "")
	if got := DefaultShell(); got != "/bin/sh" {
		t.Errorf("DefaultShell() = %q, want /bin/sh fallback when SHELL is unset", got)
	}
}
