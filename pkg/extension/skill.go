package extension

import (
	"errors"
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
// SKILL.md is optional; when it exists and carries frontmatter, the frontmatter must
// be well-formed. There used to be a 4 KiB ceiling here, justified by "the whole body
// goes into the system prompt". That was the wrong lever: it turned "wrote long docs"
// into "the extension fails to load", when the actual fix was to parse the
// frontmatter, put description into the skill manifest and inject the body only when
// the relevant tab opens (bridge → chat.go → prompt_builder). The ceiling is gone and
// the strictness moved to format: a broken frontmatter fails loudly.
//
// "No frontmatter" is not a format error, though: extension SKILL.md files predate
// the frontmatter convention (a published extensions/oss/SKILL.md is bare Markdown
// starting with `# OSS ...`), and we cannot edit another repository to suit this one.
// Built-in skills (internal/ai/skills) are first-party content this repo fully
// controls, and a missing frontmatter there is a panic; an extension is third-party
// content at a boundary, where the rule is "lenient in, strict out" — no frontmatter
// degrades to "the whole file is the body" (the pre-ceiling behavior), and only a
// half-written frontmatter (opening delimiter, no closing one) fails loudly.
func readSkillMD(dir, extName string, log *zap.Logger) (body, description string, err error) {
	data, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md")) //nolint:gosec // path constructed from trusted extension directory
	if os.IsNotExist(readErr) {
		return "", "", nil // SKILL.md is optional
	}
	if readErr != nil {
		return "", "", fmt.Errorf("read SKILL.md: %w", readErr)
	}
	raw := string(data)
	parsed, perr := skillmd.Parse(raw)
	switch {
	case perr == nil:
		return parsed.Body, parsed.Description, nil
	case errors.Is(perr, skillmd.ErrNoFrontmatter):
		log.Warn("extension SKILL.md has no frontmatter, using raw body with no description",
			zap.String("extension", extName))
		return raw, "", nil
	default:
		return "", "", fmt.Errorf("SKILL.md: %w", perr)
	}
}

// skillMDInfo is readSkillMD for callers that have no manager logger — the
// no-runtime readers. A malformed frontmatter is reported the same way it is at load
// time (the extension's documentation is simply absent) rather than failing the
// directory scan, because these callers are listing extensions, not running them.
func skillMDInfo(dir, extName string) (body, description string) {
	body, description, err := readSkillMD(dir, extName, logger.Default())
	if err != nil {
		logger.Default().Warn("read extension SKILL.md",
			zap.String("extension", extName), zap.Error(err))
		return "", ""
	}
	return body, description
}
