package extension_svc

import (
	"context"
	"errors"
	"strings"

	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/pkg/extmanifest"
)

// IncompatibleMessage renders why an extension cannot be installed, in ctx's
// language (aictx policy lang). A hostABI mismatch names the declared and the
// supported versions — the extension may be older than this OpsKat (a nightly-era
// 2.x build) as well as newer, so no single remedy fits; a minAppVersion one names
// the OpsKat release to update to. Every install path that refuses an
// incompatible extension — local, store, opsctl ext dev — says it with this.
func IncompatibleMessage(ctx context.Context, e *extmanifest.IncompatibleError) string {
	if e.Reason == extmanifest.ReasonHostABI {
		return policy.PolicyFmt(ctx,
			"This extension declares hostABI %s, but this OpsKat supports only %s, so it can't be installed. Use a version of the extension built for this OpsKat.",
			"此扩展声明 hostABI %s，当前 OpsKat 仅支持 %s，无法安装。请改用适配当前 OpsKat 的扩展版本。",
			e.HostABI, strings.Join(e.SupportedHostABIs, policy.PolicyMsg(ctx, ", ", "、")))
	}
	return policy.PolicyFmt(ctx,
		"This extension requires OpsKat %s or newer, but this is %s. Update OpsKat to install it.",
		"此扩展要求 OpsKat %s 或更高版本，当前为 %s。请更新 OpsKat 后再安装。",
		e.MinAppVersion, e.AppVersion)
}

// LocalizeIncompatible is e with IncompatibleMessage as its message; errors.As
// still finds e.
func LocalizeIncompatible(ctx context.Context, e *extmanifest.IncompatibleError) error {
	return &localizedError{msg: IncompatibleMessage(ctx, e), err: e}
}

type localizedError struct {
	msg string
	err error
}

func (e *localizedError) Error() string { return e.msg }
func (e *localizedError) Unwrap() error { return e.err }

// Kinds of LoadError.
const (
	// LoadErrorIncompatible: the extension targets a hostABI this OpsKat does not
	// support, or needs a newer OpsKat (minAppVersion).
	LoadErrorIncompatible = "incompatible"
	// LoadErrorInvalid: the manifest or the extension's describe() answer was
	// refused, or the extension failed to load.
	LoadErrorInvalid = "invalid"
)

// LoadError is why an installed extension is not loaded, as the frontend shows it.
type LoadError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// NewLoadError classifies err, what refused an installed extension, and renders it
// in ctx's language. A hostABI mismatch reads as a statement about the extension's
// build (it may be older than this OpsKat as well as newer), a minAppVersion one
// names the OpsKat release to update to; any other refusal
// carries the parse / validation error as is.
func NewLoadError(ctx context.Context, err error) *LoadError {
	var inc *extmanifest.IncompatibleError
	if !errors.As(err, &inc) {
		return &LoadError{Kind: LoadErrorInvalid, Message: err.Error()}
	}
	if inc.Reason != extmanifest.ReasonHostABI {
		return &LoadError{Kind: LoadErrorIncompatible, Message: policy.PolicyFmt(ctx,
			"This extension requires OpsKat %s or newer, but this is %s, so it isn't loaded. Update OpsKat to use it.",
			"此扩展要求 OpsKat %s 或更高版本，当前为 %s，未加载。请更新 OpsKat 后再使用。",
			inc.MinAppVersion, inc.AppVersion)}
	}
	return &LoadError{Kind: LoadErrorIncompatible, Message: policy.PolicyFmt(ctx,
		"This extension declares hostABI %s, but this OpsKat supports only %s, so it isn't loaded. Install a version of the extension built for this OpsKat.",
		"此扩展声明 hostABI %s，当前 OpsKat 仅支持 %s，未加载。请安装适配当前 OpsKat 的扩展版本。",
		inc.HostABI, strings.Join(inc.SupportedHostABIs, policy.PolicyMsg(ctx, ", ", "、")))}
}
