//go:build windows

package system

import (
	"context"

	"github.com/opskat/opskat/internal/pkg/winclip"
)

func readClipboardText(context.Context) (string, error) {
	return winclip.ReadText()
}
