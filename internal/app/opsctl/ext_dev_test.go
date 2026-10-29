package opsctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"
)

// recordingDevInstaller stands in for extension_svc.Install. It reads the
// manifest out of the directory it is handed, so a test sees exactly what would
// have been installed — not what the source directory says by the time the
// assertion runs.
type recordingDevInstaller struct {
	installs  []string // directories handed to InstallExtensionDir
	installed map[string]string
	contracts map[string]extmanifest.Manifest // manifest.json contract of the installed version
	manifest  *extension.Manifest             // manifest read from the last installed directory
	err       error
}

func (i *recordingDevInstaller) InstalledExtensionVersion(_ context.Context, name string) (string, bool) {
	v, ok := i.installed[name]
	return v, ok
}

func (i *recordingDevInstaller) InstalledExtensionManifest(_ context.Context, name string) extmanifest.Manifest {
	return i.contracts[name]
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
  "hostABI": "1.0",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "permissions": {"credentials": %q, "network": {"http": ["https://api.example.com/"]}}
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
	require.Empty(t, installer.manifest.Permissions.Credentials,
		"a permission added after approval must not be installed")
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

// network.assetEndpoint lets the extension reach private addresses named in asset
// config, so the user must see it before approving, and turning it on in a rebuild
// is a new decision.
func TestHandleExtDevInstallShowsAssetEndpointCapability(t *testing.T) {
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{}, approver)
	dir := devExtensionDir(t, "oss", "")

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)
	require.NotContains(t, approver.prompts[0].Detail, "network.assetEndpoint")

	manifest := `{
  "name": "oss", "version": "1.2.3", "hostABI": "1.0",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "permissions": {"credentials": "", "network": {"http": ["https://api.example.com/"], "assetEndpoint": true}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))
	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)

	require.Len(t, approver.prompts, 2, "declaring network.assetEndpoint must prompt again")
	require.Contains(t, approver.prompts[1].Detail, "network.assetEndpoint: true")
}

// A dev extension declaring a hostABI this runtime does not support (a future
// revision, or a nightly-era 2.x build) is refused at the install gate — not
// installed silently.
func TestHandleExtDevInstallRefusesUnsupportedHostABI(t *testing.T) {
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{}, approver)
	dir := t.TempDir()
	manifest := `{
  "name": "oss", "version": "1.2.3", "hostABI": "2.5",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "capabilities": {}
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: dir})

	require.False(t, resp.Approved)
	require.Contains(t, resp.Reason, "hostABI 2.5")
	require.Contains(t, resp.Reason, "1.0")
	require.Empty(t, approver.prompts, "a rejected manifest must not reach the approval prompt")
}

// credentials:read hands the extension every stored password of its assets in
// plaintext, so the approval leads with it instead of listing it among the
// other capabilities.
func TestHandleExtDevInstallLeadsWithCredentialsReadWarning(t *testing.T) {
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{}, approver)

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: devExtensionDir(t, "oss", "read")}).Approved)
	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: devExtensionDir(t, "plain", "")}).Approved)

	require.Len(t, approver.prompts, 2)
	withRead := strings.SplitN(approver.prompts[0].Detail, "\n", 2)[0]
	require.Contains(t, withRead, "credentials: read")
	require.Contains(t, withRead, "plaintext")
	require.NotContains(t, approver.prompts[1].Detail, "plaintext")
}

// A build the app cannot run is refused before the user is even asked, and the
// reason names what it needs.
func TestHandleExtDevInstallRejectsIncompatibleExtension(t *testing.T) {
	prev := currentApp
	currentApp = func() appversion.Info { return appversion.Info{Version: "1.5.0", Kind: appversion.KindRelease} }
	t.Cleanup(func() { currentApp = prev })

	cases := map[string]struct{ manifest, want string }{
		"hostABI":       {`{"name":"oss","version":"1.0.0","hostABI":"9.0"}`, "hostABI 9.0"},
		"minAppVersion": {`{"name":"oss","version":"1.0.0","hostABI":"1.0","minAppVersion":"2.0.0"}`, "Update OpsKat"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			installer := &recordingDevInstaller{}
			approver := &recordingApprover{approve: true}
			o := newDevOpsctl(installer, approver)
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(tc.manifest), 0o600))

			resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: dir})

			require.False(t, resp.Approved)
			require.Contains(t, resp.Reason, tc.want)
			require.Empty(t, approver.prompts, "an incompatible build must be refused before asking the user")
			require.Empty(t, installer.installs)
		})
	}
}

// nightly and dev builds have no trustworthy version number: minAppVersion is ignored.
func TestHandleExtDevInstallIgnoresMinAppVersionOnDevBuild(t *testing.T) {
	prev := currentApp
	currentApp = func() appversion.Info { return appversion.Info{Version: "1.0.0", Kind: appversion.KindDev} }
	t.Cleanup(func() { currentApp = prev })
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{}, approver)
	dir := t.TempDir()
	manifest := `{"name":"oss","version":"1.0.0","hostABI":"1.0","minAppVersion":"9.0.0","backend":{"runtime":"wasm","binary":"main.wasm"}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: dir})

	require.True(t, resp.Approved, resp.Reason)
}

func writeDriverExtension(t *testing.T, name string, ids ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dist")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	manifest := fmt.Sprintf(`{"name": %q, "version": "1.1.0", "hostABI": "1.0",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "contributes": {"databaseDrivers": [%s]}}`, name, strings.Join(quoted, ","))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
	return dir
}

// A database driver receives the plaintext password of every asset that uses it,
// so the dialog names each driver and leads with the same kind of warning as
// credentials: read.
func TestHandleExtDevInstallListsDatabaseDriversWithPlaintextWarning(t *testing.T) {
	approver := &recordingApprover{approve: true}
	o := newDevOpsctl(&recordingDevInstaller{}, approver)

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: writeDriverExtension(t, "vmysql", "vmysql", "vmaria")}).Approved)

	detail := approver.prompts[0].Detail
	first := strings.SplitN(detail, "\n", 2)[0]
	require.Contains(t, first, "databaseDrivers")
	require.Contains(t, first, "plaintext")
	require.Contains(t, first, "TLS client private key")
	require.Contains(t, detail, "databaseDrivers: vmysql, vmaria")
	require.NotContains(t, detail, "permissions: none")
	require.NotContains(t, detail, "(new)")
}

func TestHandleExtDevInstallMarksNewDriversOnOverwrite(t *testing.T) {
	approver := &recordingApprover{approve: true}
	installer := &recordingDevInstaller{
		installed: map[string]string{"vmysql": "1.0.0"},
		contracts: map[string]extmanifest.Manifest{"vmysql": {Contributes: extmanifest.Contributes{DatabaseDrivers: []string{"vmysql"}}}},
	}
	o := newDevOpsctl(installer, approver)

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: writeDriverExtension(t, "vmysql", "vmysql", "vmaria")}).Approved)

	detail := approver.prompts[0].Detail
	require.Contains(t, detail, "overwrites installed: vmysql 1.0.0")
	require.Contains(t, detail, "databaseDrivers: vmysql, vmaria (new)")
}

// An extension that provides no driver gets exactly the dialog it always had.
func TestHandleExtDevInstallDetailWithoutDriversIsUnchanged(t *testing.T) {
	approver := &recordingApprover{approve: true}
	installer := &recordingDevInstaller{installed: map[string]string{"oss": "1.0.0"},
		contracts: map[string]extmanifest.Manifest{"oss": {Contributes: extmanifest.Contributes{DatabaseDrivers: []string{"gone"}}}}}
	o := newDevOpsctl(installer, approver)

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: devExtensionDir(t, "oss", "")}).Approved)

	require.Equal(t, "extension: oss 1.2.3\noverwrites installed: oss 1.0.0\npermissions:\n  network.http: https://api.example.com/ (new)",
		approver.prompts[0].Detail)
}

func writeRedirectExtension(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"vredir","version":"1.1.0","hostABI":"1.0",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "permissions": {"network": {"assetRoute": true}}, "contributes": {"databaseDrivers": ["vredir"]}}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
	return dir
}

func writeKerberosExtension(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"vkrb","version":"1.1.0","hostABI":"1.0",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "permissions": {"kerberos": true}, "contributes": {"databaseDrivers": ["vkrb"]}}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
	return dir
}

func TestHandleExtDevInstallKerberosNewMark(t *testing.T) {
	drivers := extmanifest.Contributes{DatabaseDrivers: []string{"vkrb"}}
	declared := extmanifest.Manifest{Permissions: extmanifest.Permissions{Kerberos: true}, Contributes: drivers}
	undeclared := extmanifest.Manifest{Contributes: drivers}
	cases := []struct {
		name      string
		installed map[string]string
		contracts map[string]extmanifest.Manifest
		marked    bool
	}{
		{"added by overwrite", map[string]string{"vkrb": "1.0.0"}, map[string]extmanifest.Manifest{"vkrb": undeclared}, true},
		{"already declared", map[string]string{"vkrb": "1.0.0"}, map[string]extmanifest.Manifest{"vkrb": declared}, false},
		{"fresh install", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			approver := &recordingApprover{approve: true}
			o := newDevOpsctl(&recordingDevInstaller{installed: tc.installed, contracts: tc.contracts}, approver)
			require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: writeKerberosExtension(t)}).Approved)
			detail := approver.prompts[0].Detail
			require.Contains(t, detail, "kerberos: true")
			require.Contains(t, detail, "may fetch Kerberos tickets with the Kerberos credentials stored in the asset")
			if tc.marked {
				require.Contains(t, detail, "kerberos: true (new)")
			} else {
				require.NotContains(t, detail, "kerberos: true (new)")
			}
		})
	}
}

func TestHandleExtDevInstallAssetRouteNewMark(t *testing.T) {
	drivers := extmanifest.Contributes{DatabaseDrivers: []string{"vredir"}}
	declared := extmanifest.Manifest{
		Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{AssetRoute: true}},
		Contributes: drivers,
	}
	undeclared := extmanifest.Manifest{Contributes: drivers}
	cases := []struct {
		name      string
		installed map[string]string
		contracts map[string]extmanifest.Manifest
		marked    bool
	}{
		{"added by overwrite", map[string]string{"vredir": "1.0.0"}, map[string]extmanifest.Manifest{"vredir": undeclared}, true},
		{"already declared", map[string]string{"vredir": "1.0.0"}, map[string]extmanifest.Manifest{"vredir": declared}, false},
		{"fresh install", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			approver := &recordingApprover{approve: true}
			o := newDevOpsctl(&recordingDevInstaller{installed: tc.installed, contracts: tc.contracts}, approver)
			require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: writeRedirectExtension(t)}).Approved)
			detail := approver.prompts[0].Detail
			require.Contains(t, detail, "network.assetRoute: true")
			// 与桌面端确认一样说明这项能力的含义，而不只列出键。
			require.Contains(t, detail, "a call on an asset may dial any address along that asset's connection path, not just the address typed into the asset")
			if tc.marked {
				require.Contains(t, detail, "network.assetRoute: true (new)")
			} else {
				require.NotContains(t, detail, "network.assetRoute: true (new)")
			}
		})
	}
}

// The approval lists every declared permission and driver id, one line each, and
// marks exactly the high-risk ones — credentials: read, network.assetRoute and
// kerberos — and what an overwrite adds, the same way the desktop dialog does.
func TestHandleExtDevInstallListsEveryPermissionAndMarksHighRiskAndNew(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"full","version":"2.0.0","hostABI":"1.0",
  "backend": {"runtime": "wasm", "binary": "main.wasm"},
  "permissions": {"fs": {"read": ["/r"], "write": ["/w"]}, "credentials": "read", "kerberos": true,
    "network": {"http": ["https://a.example.com/", "https://b.example.com/"], "allowPrivate": true, "assetEndpoint": true, "assetRoute": true}},
  "contributes": {"databaseDrivers": ["d1"]}}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
	approver := &recordingApprover{approve: true}
	installer := &recordingDevInstaller{
		installed: map[string]string{"full": "1.0.0"},
		contracts: map[string]extmanifest.Manifest{"full": {Permissions: extmanifest.Permissions{
			Network: extmanifest.NetworkPermission{HTTP: []string{"https://a.example.com/"}},
		}}},
	}
	o := newDevOpsctl(installer, approver)

	require.True(t, o.handleExtDevInstall(approval.ApprovalRequest{Path: dir}).Approved)

	lines := map[string]string{}
	for _, l := range strings.Split(approver.prompts[0].Detail, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), ": "); ok {
			lines[k] = v
		}
	}
	for _, k := range []string{"fs.read", "fs.write", "network.http", "credentials", "network.allowPrivate",
		"network.assetEndpoint", "network.assetRoute", "kerberos", "databaseDrivers"} {
		require.Contains(t, lines, k)
	}
	for _, k := range []string{"credentials", "network.assetRoute", "kerberos"} {
		require.Contains(t, lines[k], "[HIGH RISK]", k)
	}
	for _, k := range []string{"fs.read", "fs.write", "network.http", "network.allowPrivate", "network.assetEndpoint", "databaseDrivers"} {
		require.NotContains(t, lines[k], "HIGH RISK", k)
	}
	require.Equal(t, "https://a.example.com/, https://b.example.com/ (new)", lines["network.http"])
	require.Equal(t, "d1 (new)", lines["databaseDrivers"])
	require.Equal(t, "/r (new)", lines["fs.read"])
}
