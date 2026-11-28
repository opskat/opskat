package helper

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

type stubLocalReadGate struct {
	called bool
	err    error
}

func (g *stubLocalReadGate) CheckLocalReads(_ context.Context, _ []string, _ string) error {
	g.called = true
	return g.err
}

func TestAllowedLocalDirsBlocksOSSExecFileFlagsAndLocalTransfer(t *testing.T) {
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	insideFile := filepath.Join(allowedDir, "ok.txt")
	outsideFile := filepath.Join(outsideDir, "secret.key")
	require.NoError(t, os.WriteFile(insideFile, []byte("hello"), 0o600))
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0o600))

	normDirs, err := NormalizeAllowedLocalDirs([]string{allowedDir})
	require.NoError(t, err)
	ctx := WithAllowedLocalDirs(context.Background(), normDirs)

	// 1. CanonicalizeOSSCommandWithContext blocks both object put and object get outside --allow-local-dir
	_, err = CanonicalizeOSSCommandWithContext(ctx, nil, "object put bucket/k --file="+outsideFile)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--allow-local-dir")

	_, err = CanonicalizeOSSCommandWithContext(ctx, nil, "object get bucket/k --file="+outsideFile)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--allow-local-dir")

	// Inside allowed dir succeeds
	_, err = CanonicalizeOSSCommandWithContext(ctx, nil, "object put bucket/k --file="+insideFile)
	require.NoError(t, err)

	// 2. localTransfer OpenRead / Write / List also enforce AllowedLocalDirs directly
	_, _, err = localTransfer.OpenRead(ctx, nil, outsideFile)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--allow-local-dir")

	rc, size, err := localTransfer.OpenRead(ctx, nil, insideFile)
	require.NoError(t, err)
	require.Equal(t, int64(5), size)
	require.NoError(t, rc.Close())

	err = localTransfer.Write(ctx, nil, filepath.Join(outsideDir, "pwn.txt"), bytes.NewReader([]byte("x")), 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--allow-local-dir")

	destInside := filepath.Join(allowedDir, "sub", "written.txt")
	err = localTransfer.Write(ctx, nil, destInside, bytes.NewReader([]byte("ok")), 2)
	require.NoError(t, err)

	// 3. Intermediate glob metacharacters are rejected under --allow-local-dir
	_, err = localTransfer.List(ctx, nil, filepath.Join(allowedDir, "sub*", "*.txt"), false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "intermediate directory")
}

type noopOSSExecSvc struct {
	ossExecService
	putCalled bool
}

func (s *noopOSSExecSvc) PutObject(_ context.Context, _ int64, _, _ string, r io.Reader, _ int64, _ string) error {
	s.putCalled = true
	_, _ = io.Copy(io.Discard, r)
	return nil
}

func TestOSSPutObjectTriggersLocalReadGateWhenPolicyAutoAllowed(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "id_rsa")
	require.NoError(t, os.WriteFile(secretFile, []byte("PRIVATE KEY"), 0o600))

	svc := &noopOSSExecSvc{}
	exec := ossExecutor{svc: svc}
	asset := &asset_entity.Asset{ID: 9, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}

	gate := &stubLocalReadGate{err: errors.New("USER DENIED")}
	checker := permission.NewCommandPolicyChecker(nil)
	ctx := permission.WithPolicyChecker(context.Background(), checker)
	ctx = WithLocalReadGate(ctx, gate)
	slot := &aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
	ctx = aictx.WithCheckResultSlot(ctx, slot)

	_, err := exec.run(ctx, asset, "object put bucket/exfil --file="+secretFile)
	require.Error(t, err)
	require.True(t, gate.called, "policy_allow on object.write must still trigger LocalReadGate before reading local file")
	require.False(t, svc.putCalled)
	require.Equal(t, aictx.Deny, slot.Decision)
}
