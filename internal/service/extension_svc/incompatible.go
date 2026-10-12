package extension_svc

import (
	"context"
	"strings"

	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/pkg/extension"
)

// IncompatibleMessage renders why an extension cannot be installed, in ctx's
// language (aictx policy lang). The remedy is always the same, update OpsKat, so
// the text names what to update to. Every install path that refuses an
// incompatible extension — local, store, opsctl ext dev — says it with this.
func IncompatibleMessage(ctx context.Context, e *extension.IncompatibleError) string {
	if e.Reason == extension.ReasonHostABI {
		return policy.PolicyFmt(ctx,
			"This extension needs hostABI %s, but this OpsKat only supports %s. Update OpsKat to install it.",
			"此扩展需要 hostABI %s，当前 OpsKat 仅支持 %s。请更新 OpsKat 后再安装。",
			e.HostABI, strings.Join(e.SupportedHostABIs, policy.PolicyMsg(ctx, ", ", "、")))
	}
	return policy.PolicyFmt(ctx,
		"This extension requires OpsKat %s or newer, but this is %s. Update OpsKat to install it.",
		"此扩展要求 OpsKat %s 或更高版本，当前为 %s。请更新 OpsKat 后再安装。",
		e.MinAppVersion, e.AppVersion)
}

// LocalizeIncompatible is e with IncompatibleMessage as its message; errors.As
// still finds e.
func LocalizeIncompatible(ctx context.Context, e *extension.IncompatibleError) error {
	return &localizedError{msg: IncompatibleMessage(ctx, e), err: e}
}

type localizedError struct {
	msg string
	err error
}

func (e *localizedError) Error() string { return e.msg }
func (e *localizedError) Unwrap() error { return e.err }
