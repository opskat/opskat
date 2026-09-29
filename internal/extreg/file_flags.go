package extreg

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// MaxFileParamBytes caps what one `--<flag>-file` may read: the same 16 MiB the host
// caps a tool result at, so a payload opsctl accepts is one the tool can also answer
// for.
const MaxFileParamBytes = 16 << 20

const fileFlagSuffix = "-file"

// ExpandFileFlags rewrites `--<flag>-file <path|->` on an extension tool command into
// the inline `--<flag> <content>` form, for the parameters the tool declared
// file-readable. It is opsctl's whole implementation of the feature: everything after
// it — classification, approval, grants, audit, the tool itself — sees only the
// inline form, which is what makes the two spellings exactly equivalent. The desktop
// and AI exec never call it, so for them `-file` stays an unknown flag.
//
// A command that uses no `-file` flag is returned untouched. Any refusal returns an
// empty command and an error, so the caller sends nothing. stdin is read only for a
// `-` path, and at most once.
func ExpandFileFlags(extName, command string, stdin io.Reader) (string, error) {
	mu.Lock()
	m := manifests[extName]
	mu.Unlock()
	if m == nil {
		return "", fmt.Errorf("extension %q is not registered", extName)
	}
	fileFlagOf := func(verb, name string) (string, bool) {
		flag, ok := strings.CutSuffix(name, fileFlagSuffix)
		if !ok {
			return "", false
		}
		def, found := toolDef(m, verb)
		return flag, found && slices.Contains(def.FileParams, flag)
	}
	c, err := cmdline.Parse(command, cmdline.WithValueFlags(func(verb, name string) bool {
		if _, ok := fileFlagOf(verb, name); ok {
			return true
		}
		return flagTakesValue(m, verb, name)
	}))
	if err != nil {
		return "", err
	}

	stdinUsed := false
	expanded := false
	for _, name := range slices.Sorted(maps.Keys(c.Flags)) {
		flag, ok := fileFlagOf(c.Verb, name)
		if !ok {
			continue
		}
		if _, inline := c.Flags[flag]; inline {
			return "", fmt.Errorf("--%s and --%s%s cannot both be given", flag, flag, fileFlagSuffix)
		}
		path := c.Flags[name]
		if path == "-" {
			if stdinUsed {
				return "", fmt.Errorf("--%s%s -: stdin can only be read once", flag, fileFlagSuffix)
			}
			stdinUsed = true
		}
		content, err := readFileArg(path, stdin)
		if err != nil {
			return "", fmt.Errorf("--%s%s %s: %w", flag, fileFlagSuffix, path, err)
		}
		delete(c.Flags, name)
		c.Flags[flag] = content
		expanded = true
	}
	if !expanded {
		return command, nil
	}
	return c.Render(flagGrammar(m)), nil
}

// readFileArg reads one `-file` argument: the file at path, or stdin for "-".
func readFileArg(path string, stdin io.Reader) (string, error) {
	src := stdin
	if path != "-" {
		f, err := os.Open(path) //nolint:gosec // reading the file the user named is the feature
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		src = f
	}
	return readFileParam(src)
}

func readFileParam(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileParamBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxFileParamBytes {
		return "", fmt.Errorf("content exceeds 16 MiB")
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("content is not valid UTF-8 text")
	}
	return string(data), nil
}
