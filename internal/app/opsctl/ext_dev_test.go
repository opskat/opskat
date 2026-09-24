package opsctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/pkg/extension"
)

// recordingDevInstaller stands in for extension_svc.Install. It reads the
// manifest out of the directory it is handed, so a test sees exactly what would
// have been installed — not what the source directory says by the time the
// assertion runs.
type recordingDevInstaller struct {
	installs  []string // directories handed to InstallExtensionDir
	installed map[string]string
	manifest  *extension.Manifest // manifest read from the last installed directory
	err       error
}

func (i *recordingDevInstaller) InstalledExtensionVersion(_ context.Context, name string) (string, bool) {
	v, ok := i.installed[name]
	return v, ok
}

func (i *recordingDevInstaller) InstallExtensionDir(_ context.Context, sourceDir string) (string, string, error) {
	i.installs = append(i.installs, sourceDir)
	if i.err != nil {
		return "", "", i.err
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, "manifest.json")) //nolint:gosec // test-owned temp dir
	if err != nil {
		return "", "", err
	}
	m, err := extension.ParseManifest(data)
	if err != nil {
		return "", "", err
	}
	i.manifest = m
	return m.Name, m.Version, nil
}

// recordingApprover answers the desktop confirmation dialog and records every
// prompt it was shown.
type recordingApprover struct {
	prompts []approval.ApprovalRequest
	approve bool
	during  func() // runs while the dialog is "open"
}

func (a *recordingApprover) ask(req approval.ApprovalRequest) approval.ApprovalResponse {
	a.prompts = append(a.prompts, req)
	if a.during != nil {
		a.during()
	}
	if a.approve {
		return approval.ApprovalResponse{Approved: true}
	}
	return approval.ApprovalResponse{Approved: false, Reason: "user denied"}
}

func newDevOpsctl(installer ExtDevInstaller, approver *recordingApprover) *Opsctl {
	o := &Opsctl{
		ctx: context.Background(), appCtx: context.Background(),
		lang: extTestLang{}, extDevInstaller: installer,
	}
	o.extDevApprove = approver.ask
	return o
}

const devManifest = `{
  "name": %q,
  "version": "1.2.3",
  "hostABI": "2.0",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "capabilities": {"credentials": %q, "http": {"allowlist": ["https://api.example.com/"]}}
}`

func writeDevExtension(t *testing.T, dir, name, credentials string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	manifest := []byte(fmt.Sprintf(devManifest, name, credentials))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
}

func devExtensionDir(t *testing.T, name, credentials string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dist")
	writeDevExtension(t, dir, name, credentials)
	return dir
}

// The dev channel installs and enables unreviewed WASM with whatever capabilities
// its manifest declares. A local process holding the socket token must not be able
// to do that on its own: without the user's confirmation nothing is installed.
func TestHandleExtDevInstallRefusedWithoutApproval(t *testing.T) {
	installer := &recordingDevInstaller{}
	approver := &recordingApprover{approve: false}
	o := newDevOpsctl(installer, approver)
	dir := devExtensionDir(t, "oss", "read")

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: dir})

	require.False(t, resp.Approved)
	require.Contains(t, resp.Reason, "denied")
	require.Len(t, approver.prompts, 1, "the user must have been asked")
	require.Empty(t, installer.installs, "nothing may be installed once the user refuses")
}

func TestHandleExtDevInstallInstallsAfterApproval(t *testing.T) {
	installer := &recordingDevInstaller{installed: map[string]string{"oss": "1.0.0"}}
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(installer, approver)
	dir := devExtensionDir(t, "oss", "read")

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: dir})

	require.True(t, resp.Approved, resp.Reason)
	require.Equal(t, "oss", resp.Extension)
	require.Equal(t, "1.2.3", resp.Version)
	require.Len(t, installer.installs, 1)

	// The dialog is the only thing the user reviews, so it must carry what is being
	// granted: the source, the extension, its capabilities, and what it replaces.
	require.Len(t, approver.prompts, 1)
	prompt := approver.prompts[0]
	require.Equal(t, "ext_dev_install", prompt.Type)
	require.Equal(t, dir, prompt.Command)
	require.Contains(t, prompt.Detail, "oss 1.2.3")
	require.Contains(t, prompt.Detail, "credentials: read")
	require.Contains(t, prompt.Detail, "https://api.example.com/")
	require.Contains(t, prompt.Detail, "1.0.0", "overwriting an installed extension must be visible")
}

func TestHandleExtDevInstallPromptSaysWhenNothingIsOverwritten(t *testing.T) {
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{}, approver)

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: devExtensionDir(t, "oss", "")})

	require.True(t, resp.Approved, resp.Reason)
	require.Contains(t, approver.prompts[0].Detail, "new install")
}

// What gets installed is what the user approved: rewriting the source directory
// while the dialog is open must not change the installed extension.
func TestHandleExtDevInstallInstallsTheApprovedSnapshot(t *testing.T) {
	installer := &recordingDevInstaller{}
	dir := devExtensionDir(t, "oss", "")
	approver := &recordingApprover{approve: true, during: func() {
		writeDevExtension(t, dir, "oss", "read")
	}}
	o := newDevOpsctl(installer, approver)

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: dir})

	require.True(t, resp.Approved, resp.Reason)
	require.NotNil(t, installer.manifest)
	require.Empty(t, installer.manifest.Capabilities.Credentials,
		"a capability added after approval must not be installed")
}

// Re-running `opsctl ext dev <dir>` after each build is the hot-reload loop, so an
// approval is remembered for the same directory and extension — but only for those.
func TestHandleExtDevInstallRemembersApprovalForSameDirAndName(t *testing.T) {
	installer := &recordingDevInstaller{}
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(installer, approver)
	dir := devExtensionDir(t, "oss", "read")

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)
	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)
	require.Len(t, approver.prompts, 1, "a rebuild of the approved directory must not re-prompt")
	require.Len(t, installer.installs, 2)

	other := devExtensionDir(t, "oss", "read")
	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: other}).Approved)
	require.Len(t, approver.prompts, 2, "a different directory must prompt again")

	writeDevExtension(t, dir, "redis-ext", "read")
	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)
	require.Len(t, approver.prompts, 3, "a different extension from the same directory must prompt again")
}

// A rebuild that widens what the extension may touch is a new decision, not a reload.
func TestHandleExtDevInstallPromptsAgainWhenCapabilitiesChange(t *testing.T) {
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{}, approver)
	dir := devExtensionDir(t, "oss", "")

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)
	writeDevExtension(t, dir, "oss", "read")
	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)

	require.Len(t, approver.prompts, 2)
}

func TestHandleExtDevInstallDoesNotRememberADenial(t *testing.T) {
	installer := &recordingDevInstaller{}
	approver := &recordingApprover{approve: false}
	o := newDevOpsctl(installer, approver)
	dir := devExtensionDir(t, "oss", "")

	require.False(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)
	approver.approve = true
	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)

	require.Len(t, approver.prompts, 2)
	require.Len(t, installer.installs, 1)
}

func TestHandleExtDevInstallReportsInstallFailure(t *testing.T) {
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{err: errors.New("snippet category already registered")}, approver)

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: devExtensionDir(t, "oss", "")})

	require.False(t, resp.Approved)
	require.Contains(t, resp.Reason, "snippet category already registered")
}

func TestHandleExtDevInstallRejectsAnInvalidManifestBeforePrompting(t *testing.T) {
	approver := &recordingApprover{approve: true}
	installer := &recordingDevInstaller{}
	o := newDevOpsctl(installer, approver)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"oss"}`), 0o600))

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: dir})

	require.False(t, resp.Approved)
	require.Empty(t, approver.prompts, "the user is never asked about something that cannot install")
	require.Empty(t, installer.installs)
}

func TestHandleExtDevInstallNeedsAnAbsolutePath(t *testing.T) {
	for _, path := range []string{"", "relative/dist"} {
		installer := &recordingDevInstaller{}
		approver := &recordingApprover{approve: true}
		o := newDevOpsctl(installer, approver)

		resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: path})

		require.False(t, resp.Approved, path)
		require.Empty(t, approver.prompts, path)
		require.Empty(t, installer.installs, path)
	}
}

func TestHandleExtDevInstallWithoutExtensionSystem(t *testing.T) {
	o := newDevOpsctl(nil, &recordingApprover{approve: true})

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: "/src/oss/dist"})

	require.False(t, resp.Approved)
	require.Contains(t, resp.Reason, "extension system")
}
