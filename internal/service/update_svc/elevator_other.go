//go:build !windows

package update_svc

import (
	"fmt"
	"os/exec"
)

func runInstaller(exePath, args string) error {
	if output, err := exec.Command(exePath, args).CombinedOutput(); err != nil {
		return fmt.Errorf("run installer failed: %s: %w", string(output), err)
	}
	return nil
}
