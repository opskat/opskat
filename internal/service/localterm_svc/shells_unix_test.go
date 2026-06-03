//go:build !windows

package localterm_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectShellsReturnsExistingShells(t *testing.T) {
	shells := DetectShells()
	// /bin/sh 几乎一定在 /etc/shells 或作为兜底存在;至少不应 panic 且项的 Path 非空。
	for _, s := range shells {
		assert.NotEmpty(t, s.Path)
		assert.NotEmpty(t, s.Name)
	}
}

func TestDetectShellsSkipsNonExistentShell(t *testing.T) {
	t.Setenv("SHELL", "/nonexistent/shell")
	for _, s := range DetectShells() {
		assert.NotEqual(t, "/nonexistent/shell", s.Path)
	}
}
