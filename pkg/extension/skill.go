package extension

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/pkg/skillmd"
	"go.uber.org/zap"
)

// readSkillMD reads an extension directory's optional SKILL.md.
//
// It is shared by the two ways an extension is read — LoadExtension (with a WASM
// runtime) and LoadManifestInfo / ScanManifests (without one) — because the
// documentation an extension ships is part of what a host can know about it before
// running it, and a second reader would drift on exactly the rule below.
//
// SKILL.md is optional; when it exists it must open with a well-formed frontmatter,
// the same format built-in skills use: the description goes into the skill manifest
// and the body is injected only when the relevant tab opens, so there is no size
// ceiling — but a file with no or a broken frontmatter fails the load loudly.
func readSkillMD(dir string) (body, description string, err error) {
	data, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md")) //nolint:gosec // path constructed from trusted extension directory
	if os.IsNotExist(readErr) {
		return "", "", nil // SKILL.md is optional
	}
	if readErr != nil {
		return "", "", fmt.Errorf("read SKILL.md: %w", readErr)
	}
	parsed, err := skillmd.Parse(string(data))
	if err != nil {
		return "", "", fmt.Errorf("SKILL.md: %w", err)
	}
	return parsed.Body, parsed.Description, nil
}

// skillMDInfo is readSkillMD for callers that have no manager logger — the
// no-runtime readers. A malformed frontmatter is reported the same way it is at load
// time (the extension's documentation is simply absent) rather than failing the
// directory scan, because these callers are listing extensions, not running them.
func skillMDInfo(dir, extName string) (body, description string) {
	body, description, err := readSkillMD(dir)
	if err != nil {
		logger.Default().Warn("read extension SKILL.md",
			zap.String("extension", extName), zap.Error(err))
		return "", ""
	}
	return body, description
}
