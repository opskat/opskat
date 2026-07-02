package ssh_svc

import (
	"os"
	"testing"
)

// TestMain zeroes the injection read-gap for the whole package: unit tests use
// a fake stdin (no real shell to reach `read`), so the gap only adds latency.
func TestMain(m *testing.M) {
	syncInjectReadGap = 0
	os.Exit(m.Run())
}
