package sshagent

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// EndpointType identifies how a Source's value is interpreted.
type EndpointType string

const (
	// EndpointTypeEnvironment: the value is an environment variable name that
	// is re-resolved on every operation. Supported on all platforms.
	EndpointTypeEnvironment EndpointType = "environment"
	// EndpointTypeUnixSocket: the value is an absolute unix socket path.
	// Supported on macOS and Linux.
	EndpointTypeUnixSocket EndpointType = "unix_socket"
	// EndpointTypeWindowsNamedPipe: the value is a local OpenSSH-compatible
	// named pipe (\\\\.\\pipe\\...). Supported on Windows.
	EndpointTypeWindowsNamedPipe EndpointType = "windows_named_pipe"
)

// Source is an SSH Agent endpoint configuration. It carries only the endpoint
// definition (type + value); it never holds identities, keys, signatures or
// runtime state.
type Source struct {
	Type  EndpointType
	Value string
}

// Validate checks the endpoint definition structurally without touching the
// network. It does not check platform support (an unsupported type is still
// structurally valid so imports can keep it) nor reachability.
func (s Source) Validate() error {
	switch s.Type {
	case EndpointTypeEnvironment:
		if !validEnvVarName(s.Value) {
			return newError(CodeEndpointUnavailable, "saved environment variable name is invalid")
		}
	case EndpointTypeUnixSocket:
		if _, err := expandAndCleanUnixPath(s.Value); err != nil {
			return newError(CodeEndpointUnavailable, "saved unix socket path is not absolute")
		}
	case EndpointTypeWindowsNamedPipe:
		if err := validateWindowsPipe(s.Value); err != nil {
			return newError(CodeEndpointUnavailable, "saved named pipe is not a local \\\\.\\pipe\\ path")
		}
	default:
		return newError(CodeEndpointUnavailable, "unknown agent endpoint type")
	}
	return nil
}

// platformSupported reports whether the current platform can serve this type.
func (t EndpointType) platformSupported() bool {
	switch t {
	case EndpointTypeEnvironment:
		return true
	case EndpointTypeUnixSocket:
		return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
	case EndpointTypeWindowsNamedPipe:
		return runtime.GOOS == "windows"
	}
	return false
}

// transportKind is the effective transport a resolved endpoint maps to on the
// current platform.
type transportKind int

const (
	kindUnix transportKind = iota
	kindPipe
)

// resolveEndpoint turns a Source into the concrete transport kind and value
// for the current platform. Environment endpoints are re-resolved on every
// call and their value is re-validated against the current platform, never
// interpreted relative to the process working directory.
func (s Source) resolveEndpoint() (transportKind, string, error) {
	switch s.Type {
	case EndpointTypeEnvironment:
		if !validEnvVarName(s.Value) {
			return 0, "", newError(CodeEndpointUnavailable, "saved environment variable name is invalid")
		}
		v := os.Getenv(s.Value)
		if v == "" {
			return 0, "", newError(CodeEnvUnset, "environment variable has no usable value")
		}
		if runtime.GOOS == "windows" {
			if err := validateWindowsPipe(v); err != nil {
				return 0, "", newError(CodeEndpointUnavailable, "resolved agent endpoint is not a local named pipe")
			}
			return kindPipe, v, nil
		}
		p, err := expandAndCleanUnixPath(v)
		if err != nil {
			return 0, "", newError(CodeEndpointUnavailable, "resolved agent endpoint is not an absolute unix socket path")
		}
		return kindUnix, p, nil
	case EndpointTypeUnixSocket:
		p, err := expandAndCleanUnixPath(s.Value)
		if err != nil {
			return 0, "", newError(CodeEndpointUnavailable, "saved unix socket path is not absolute")
		}
		return kindUnix, p, nil
	case EndpointTypeWindowsNamedPipe:
		if err := validateWindowsPipe(s.Value); err != nil {
			return 0, "", newError(CodeEndpointUnavailable, "saved named pipe is not a local \\\\.\\pipe\\ path")
		}
		return kindPipe, s.Value, nil
	}
	return 0, "", newError(CodeEndpointUnavailable, "unknown agent endpoint type")
}

var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validEnvVarName(name string) bool {
	return envVarNameRe.MatchString(name)
}

// expandAndCleanUnixPath expands a leading "~" (bare or "~/...") to the user's
// home directory and lexically cleans the path; the result must be absolute.
// It never resolves symlinks and never interprets relative paths.
func expandAndCleanUnixPath(p string) (string, error) {
	if p == "" {
		return "", errRelativePath
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errRelativePath
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	cleaned := filepath.Clean(p)
	if !filepath.IsAbs(cleaned) {
		return "", errRelativePath
	}
	return cleaned, nil
}

var errRelativePath = newError(CodeEndpointUnavailable, "agent endpoint path is not absolute")

// validateWindowsPipe accepts only local \\\\.\\pipe\\ names and rejects
// remote UNC pipes (\\server\\pipe\\...) and empty pipe names.
func validateWindowsPipe(p string) error {
	if !strings.HasPrefix(strings.ToLower(p), `\\.\pipe\`) {
		return newError(CodeEndpointUnavailable, "named pipe must be a local \\\\.\\pipe\\ path")
	}
	if len(p) <= len(`\\.\pipe\`) {
		return newError(CodeEndpointUnavailable, "named pipe must not be empty")
	}
	return nil
}
