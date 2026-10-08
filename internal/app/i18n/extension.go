package i18n

import (
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/extension"
)

// ExtensionIncompatible renders why an extension cannot be installed, in lang. The
// remedy is always the same, update OpsKat, so the text names what to update to.
func ExtensionIncompatible(lang string, e *extension.IncompatibleError) string {
	if e.Reason == extension.ReasonHostABI {
		return fmt.Sprintf(Pick(lang,
			"此扩展需要 hostABI %s，当前 OpsKat 仅支持 %s。请更新 OpsKat 后再安装。",
			"This extension needs hostABI %s, but this OpsKat only supports %s. Update OpsKat to install it."),
			e.HostABI, strings.Join(e.SupportedHostABIs, Pick(lang, "、", ", ")))
	}
	return fmt.Sprintf(Pick(lang,
		"此扩展要求 OpsKat %s 或更高版本，当前为 %s。请更新 OpsKat 后再安装。",
		"This extension requires OpsKat %s or newer, but this is %s. Update OpsKat to install it."),
		e.MinAppVersion, e.AppVersion)
}

// LocalizeIncompatible is e with ExtensionIncompatible as its message; errors.As
// still finds e.
func LocalizeIncompatible(lang string, e *extension.IncompatibleError) error {
	return &localizedError{msg: ExtensionIncompatible(lang, e), err: e}
}

type localizedError struct {
	msg string
	err error
}

func (e *localizedError) Error() string { return e.msg }
func (e *localizedError) Unwrap() error { return e.err }
