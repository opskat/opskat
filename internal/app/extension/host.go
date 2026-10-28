package extension

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/pkg/extension"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// The host capabilities below are the ones only the desktop window can offer. The
// rest — asset config and credentials, KV, the asset connection path — need no
// window and live in extension_svc (Service.HostConfig), shared with opsctl.

// fileDialogOpener implements extension.FileDialogOpener
type fileDialogOpener struct {
	ctx context.Context // Wails app context
}

func (o *fileDialogOpener) FileDialog(dialogType string, opts extension.DialogOptions) (string, error) {
	switch dialogType {
	case "open":
		return wailsRuntime.OpenFileDialog(o.ctx, wailsRuntime.OpenDialogOptions{
			Title:   opts.Title,
			Filters: toWailsFilters(opts.Filters),
		})
	case "save":
		return wailsRuntime.SaveFileDialog(o.ctx, wailsRuntime.SaveDialogOptions{
			Title:           opts.Title,
			DefaultFilename: opts.DefaultName,
			Filters:         toWailsFilters(opts.Filters),
		})
	default:
		return "", fmt.Errorf("unknown dialog type: %q", dialogType)
	}
}

func toWailsFilters(filters []string) []wailsRuntime.FileFilter {
	if len(filters) == 0 {
		return nil
	}
	result := make([]wailsRuntime.FileFilter, 0, len(filters))
	for _, f := range filters {
		result = append(result, wailsRuntime.FileFilter{
			DisplayName: f,
			Pattern:     f,
		})
	}
	return result
}

// toolEventHandler implements extension.ActionEventHandler for one extension.
type toolEventHandler struct {
	ext     *Extension
	extName string
}

// OnActionEvent forwards one tool event to the page that made the call, as
// "ext:tool:event". Only a page call has a listener: an event from an AI exec or
// opsctl call of the same tool is dropped here, so the tool never has to know
// who called it.
//
// invocationId rides along because one handler serves every concurrent call of
// this extension: without it a listener sees the extension's events merged into
// one stream and attributes another upload's progress to its own.
func (h *toolEventHandler) OnActionEvent(invocationID, eventType string, data json.RawMessage) error {
	if !h.ext.toolCalls.isPageCall(h.extName, invocationID) {
		return nil
	}
	h.ext.emit("ext:tool:event", map[string]any{
		"extension":    h.extName,
		"invocationId": invocationID,
		"eventType":    eventType,
		"data":         data,
	})
	return nil
}
