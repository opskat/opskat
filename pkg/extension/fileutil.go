package extension

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// Limits applied to every extension zip (local and store) while unpacking. Vars so
// tests can lower them.
var (
	// maxZipExtractedBytes caps the total decompressed bytes of one archive.
	maxZipExtractedBytes int64 = 512 << 20
	// maxZipEntries caps the number of entries in one archive.
	maxZipEntries = 5000
)

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := r.Close(); err != nil {
			logger.Default().Warn("close zip reader", zap.Error(err))
		}
	}()

	if len(r.File) > maxZipEntries {
		return fmt.Errorf("zip has too many entries: %d (limit %d)", len(r.File), maxZipEntries)
	}

	remaining := maxZipExtractedBytes
	for _, f := range r.File {
		name := f.Name
		// Reject if not a local relative path (handles .., absolute, drive letters)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("zip contains unsafe path: %s", f.Name)
		}
		cleaned := filepath.Clean(name)
		target := filepath.Join(destDir, cleaned)
		// Verify the cleaned target is within destDir
		absDest, _ := filepath.Abs(destDir)
		absTarget, _ := filepath.Abs(target)
		if !strings.HasPrefix(absTarget, absDest+string(filepath.Separator)) && absTarget != absDest {
			return fmt.Errorf("zip entry escapes dest: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		out, err := os.Create(target) //nolint:gosec // target validated by traversal check above
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			if closeErr := out.Close(); closeErr != nil {
				logger.Default().Warn("close output file", zap.Error(closeErr))
			}
			return err
		}

		// Bound by the bytes actually produced, not the sizes the headers declare.
		n, err := io.Copy(out, io.LimitReader(rc, remaining+1)) //nolint:gosec // bounded by LimitReader
		remaining -= n
		if err == nil && remaining < 0 {
			err = fmt.Errorf("zip extracts to more than %d bytes", maxZipExtractedBytes)
		}
		if closeErr := rc.Close(); closeErr != nil {
			logger.Default().Warn("close zip entry reader", zap.Error(closeErr))
		}
		if closeErr := out.Close(); closeErr != nil {
			logger.Default().Warn("close output file", zap.Error(closeErr))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return nil // skip symlinks
		}

		data, err := os.ReadFile(path) //nolint:gosec // path from filepath.Walk within validated src directory
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644) //nolint:gosec // target derived from validated src/dst directories
	})
}
