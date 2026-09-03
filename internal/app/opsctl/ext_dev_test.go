package opsctl

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/approval"
)

type recordingDevInstaller struct {
	dir  string
	err  error
	name string
}

func (i *recordingDevInstaller) InstallExtensionDir(_ context.Context, sourceDir string) (string, string, error) {
	i.dir = sourceDir
	if i.err != nil {
		return "", "", i.err
	}
	return i.name, "1.2.3", nil
}

func newDevOpsctl(installer ExtDevInstaller) *Opsctl {
	return &Opsctl{
		ctx: context.Background(), appCtx: context.Background(),
		lang: extTestLang{}, extDevInstaller: installer,
	}
}

func TestHandleExtDevInstallDelegatesToTheAppInstallPath(t *testing.T) {
	installer := &recordingDevInstaller{name: "oss"}
	o := newDevOpsctl(installer)

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: "/src/oss/dist"})

	require.True(t, resp.Approved)
	require.Equal(t, "/src/oss/dist", installer.dir)
	require.Equal(t, "oss", resp.Extension)
	require.Equal(t, "1.2.3", resp.Version)
}

func TestHandleExtDevInstallReportsInstallFailure(t *testing.T) {
	o := newDevOpsctl(&recordingDevInstaller{err: errors.New("manifest missing assetTypes")})

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: "/src/broken"})

	require.False(t, resp.Approved)
	require.Contains(t, resp.Reason, "manifest missing assetTypes")
}

// The dev channel installs unreviewed WASM with whatever capabilities its manifest
// asks for. Refusing it in production is the gate cmd/devserver used to carry, and
// it has to live on the side that does the work — a client-side check protects
// nothing once the socket is reachable.
func TestHandleExtDevInstallRefusedInProduction(t *testing.T) {
	t.Setenv("OPSKAT_ENV", "production")
	installer := &recordingDevInstaller{name: "oss"}
	o := newDevOpsctl(installer)

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: "/src/oss/dist"})

	require.False(t, resp.Approved)
	require.Contains(t, resp.Reason, "production")
	require.Empty(t, installer.dir, "nothing may be installed once the request is refused")
}

func TestHandleExtDevInstallNeedsAPath(t *testing.T) {
	installer := &recordingDevInstaller{name: "oss"}
	o := newDevOpsctl(installer)

	resp := o.handleExtDevInstall(approval.ApprovalRequest{})

	require.False(t, resp.Approved)
	require.Empty(t, installer.dir)
}

func TestHandleExtDevInstallWithoutExtensionSystem(t *testing.T) {
	o := newDevOpsctl(nil)

	resp := o.handleExtDevInstall(approval.ApprovalRequest{Path: "/src/oss/dist"})

	require.False(t, resp.Approved)
	require.Contains(t, resp.Reason, "extension system")
}
