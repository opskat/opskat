package ssh_svc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestEnableSyncTimeoutLeavesTerminalUsable guards the regression behind both
// the reconnect restore-cwd "dead terminal" report and issue #216 (file-manager
// sync fails -> ssh window unusable). When EnableSync times out waiting for the
// init:pid marker, the script-echo suppression begun by writeInternalScript must
// be torn down; otherwise filterOutput keeps eating the user's typed-command
// echo and command output, so the terminal looks completely dead.
func TestEnableSyncTimeoutLeavesTerminalUsable(t *testing.T) {
	stdin := &recordingWriteCloser{}
	sess := &Session{
		ID:        "test-timeout-usable",
		stdin:     stdin,
		shellPath: "/bin/bash",
		shellType: shellTypeBash,
	}
	sess.initSyncState(sess.shellPath, sess.shellType, false)

	prev := syncEnableTimeout
	syncEnableTimeout = 20 * time.Millisecond
	defer func() { syncEnableTimeout = prev }()

	err := sess.EnableSync()
	assert.Error(t, err, "EnableSync must report the timeout")

	// The queued echo-suppression pattern for the injection command must be
	// dropped, so the filter is a pass-through again.
	assert.Empty(t, sess.echoSuppressions,
		"queued echo suppression must be cleared after EnableSync failure")

	// The concrete symptom: normal shell output must pass through untouched.
	typedEcho := sess.filterOutput([]byte("ls\r\n"))
	output := sess.filterOutput([]byte("Desktop  Documents  Downloads\r\n"))
	assert.Equal(t, "ls\r\n", string(typedEcho),
		"typed-command echo must survive after a failed EnableSync")
	assert.Equal(t, "Desktop  Documents  Downloads\r\n", string(output),
		"command output must survive after a failed EnableSync")
}
