package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
)

type allowedLocalDirsKeyType struct{}

// WithAllowedLocalDirs attaches a normalized list of permitted local directories
// to ctx. When non-empty, every local filesystem read, write, or glob expansion
// through localTransfer (both cp and OSS exec --file=) is restricted to these
// directories after symlink resolution.
func WithAllowedLocalDirs(ctx context.Context, dirs []string) context.Context {
	if len(dirs) == 0 {
		return ctx
	}
	cp := make([]string, len(dirs))
	copy(cp, dirs)
	return context.WithValue(ctx, allowedLocalDirsKeyType{}, cp)
}

// AllowedLocalDirs returns the normalized local directory allowlist attached to ctx, if any.
func AllowedLocalDirs(ctx context.Context) []string {
	dirs, _ := ctx.Value(allowedLocalDirsKeyType{}).([]string)
	return dirs
}

// NormalizeAllowedLocalDirs resolves and cleans a list of directory paths for use with
// WithAllowedLocalDirs.
func NormalizeAllowedLocalDirs(rawDirs []string) ([]string, error) {
	if len(rawDirs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(rawDirs))
	for _, raw := range rawDirs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, errors.New("--allow-local-dir must not be empty")
		}
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("resolve --allow-local-dir %q: %w", raw, err)
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		out = append(out, filepath.Clean(abs))
	}
	return out, nil
}

// VerifyAllowedLocalPath enforces AllowedLocalDirs(ctx) on localPath when an allowlist
// is present on ctx.
func VerifyAllowedLocalPath(ctx context.Context, localPath string) error {
	dirs := AllowedLocalDirs(ctx)
	if len(dirs) == 0 {
		return nil
	}
	return VerifyLocalPathAllowed(localPath, dirs)
}

// VerifyLocalPathAllowed verifies that localPath is absolute, does not traverse out of
// allowedDirs via symlinks or glob patterns in intermediate directory segments, and lands
// inside at least one directory in allowedDirs.
func VerifyLocalPathAllowed(localPath string, allowedDirs []string) error {
	_, _, err := matchAllowedLocalDir(localPath, allowedDirs)
	return err
}

func matchAllowedLocalDir(localPath string, allowedDirs []string) (allowedDir, relPath string, err error) {
	if !filepath.IsAbs(localPath) {
		return "", "", fmt.Errorf("local path %q must be absolute", localPath)
	}
	// Reject glob metacharacters in intermediate directory segments: filepath.Glob follows
	// intermediate directory symlinks, which would allow "/allowed/sub*/secret" to escape when
	// "/allowed/sub1" symlinks outside allowedDirs.
	if hasGlobInIntermediateDir(localPath) {
		return "", "", fmt.Errorf("glob metacharacters in intermediate directory segments are not allowed under --allow-local-dir: %q", localPath)
	}
	resolved, err := resolvePathWithExistingAncestors(localPath)
	if err != nil {
		return "", "", err
	}
	for _, dir := range allowedDirs {
		rel, rerr := filepath.Rel(dir, resolved)
		if rerr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return dir, rel, nil
		}
	}
	return "", "", fmt.Errorf("path %q is outside allowed directories", localPath)
}

func hasGlobInIntermediateDir(p string) bool {
	parent := filepath.Dir(filepath.Clean(p))
	return hasGlobMeta(parent)
}

// resolvePathWithExistingAncestors resolves symlinks along the longest existing prefix
// of p, then re-attaches non-existent trailing segments so symlink escapes fail closed
// even when creating a new file or subdirectory.
func resolvePathWithExistingAncestors(p string) (string, error) {
	clean := filepath.Clean(p)
	var tail []string
	cur := clean
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean, nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// openAllowedLocalFile opens a local file for reading while confining it inside the
// matching allowed directory via os.OpenRoot (preventing symlink / TOCTOU escapes).
func openAllowedLocalFile(ctx context.Context, path string) (*os.File, error) {
	dirs := AllowedLocalDirs(ctx)
	if len(dirs) == 0 {
		return os.Open(path) //nolint:gosec // no local dir allowlist configured; path already approved
	}
	allowedDir, rel, err := matchAllowedLocalDir(path, dirs)
	if err != nil {
		return nil, fmt.Errorf("local source path blocked by --allow-local-dir: %w", err)
	}
	if rel == "." {
		return nil, fmt.Errorf("local source path %q is a directory", path)
	}
	root, err := os.OpenRoot(allowedDir)
	if err != nil {
		return nil, fmt.Errorf("open allowed local directory %q: %w", allowedDir, err)
	}
	defer func() { _ = root.Close() }()
	return root.Open(rel)
}

// LocalReadGate is the seam for "a local file is about to be read and sent to a
// remote asset, and no human approved this operation": the read-side twin of
// LocalWriteGate, implemented by internal/ai/tool.LocalToolGate (as local_read).
//
// An entry opts in by attaching one (WithLocalReadGate). opsctl mcp does: the
// external agent reaches OpsKat through tools whose policies may auto-allow a remote
// write, and nothing else stands between it and uploading ~/.ssh/id_rsa. The desktop
// AI does not: its own local_read tool reads any local file ungated, so gating
// uploads there would add a prompt without closing anything. An --allow-local-dir
// allowlist (WithAllowedLocalDirs) confines reads by itself and replaces the gate.
type LocalReadGate interface {
	// CheckLocalReads asks about a set of local paths; nil allows, non-nil is the
	// refusal to return to the model. paths must be non-empty.
	CheckLocalReads(ctx context.Context, paths []string, detail string) error
}

type localReadGateKeyType struct{}

// WithLocalReadGate attaches the entry's local read gate to ctx.
func WithLocalReadGate(ctx context.Context, gate LocalReadGate) context.Context {
	return context.WithValue(ctx, localReadGateKeyType{}, gate)
}

// GateLocalReads puts local reads no human approved through the entry's local read
// gate, when it has one and no allowlist confines the reads. checker is the caller's
// permission.RequireCheckerOrPreapproved result: nil is opsctl's preapproved command
// line, whose paths the user typed, so it is not asked. prompted reports that a human
// already approved this very operation (an endpoint or command prompt), whose text
// named the local path. A refusal is recorded as the user's denial on the audit slot.
func GateLocalReads(ctx context.Context, checker *permission.CommandPolicyChecker, prompted bool, paths []string, detail string) error {
	gate, _ := ctx.Value(localReadGateKeyType{}).(LocalReadGate)
	if gate == nil || len(AllowedLocalDirs(ctx)) > 0 || checker == nil || prompted {
		return nil
	}
	if err := gate.CheckLocalReads(ctx, paths, detail); err != nil {
		aictx.RecordDecision(ctx, aictx.CheckResult{
			Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny, Message: err.Error(),
		})
		return err
	}
	return nil
}

// gateExecLocalRead is the exec side of the read gate (today only OSS `object put
// --file=P`), judged like gateExecLocalWrite: the command was prompted when its
// decision is user_allow.
func gateExecLocalRead(ctx context.Context, localPath, detail string) error {
	if err := VerifyAllowedLocalPath(ctx, localPath); err != nil {
		aictx.RecordDecision(ctx, aictx.CheckResult{
			Decision: aictx.Deny, DecisionSource: aictx.SourcePolicyDeny, Message: err.Error(),
		})
		return fmt.Errorf("local source path blocked by --allow-local-dir: %w", err)
	}
	if _, gated := ctx.Value(localReadGateKeyType{}).(LocalReadGate); !gated {
		return nil
	}
	checker, err := permission.RequireCheckerOrPreapproved(ctx)
	if err != nil {
		return err
	}
	res := aictx.GetCheckResult(ctx)
	prompted := res != nil && res.DecisionSource == aictx.SourceUserAllow
	return GateLocalReads(ctx, checker, prompted, []string{localPath}, detail)
}
