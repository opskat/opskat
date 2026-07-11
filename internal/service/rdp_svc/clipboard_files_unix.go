//go:build !windows

package rdp_svc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func readLocalClipboardFilePaths() ([]string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("osascript", "-l", "JavaScript", "-e", `ObjC.import("AppKit"); var pb=$.NSPasteboard.generalPasteboard; var classes=$.NSArray.arrayWithObject($.NSURL); var opts=$.NSDictionary.dictionaryWithObjectForKey(true,$.NSPasteboardURLReadingFileURLsOnlyKey); var urls=pb.readObjectsForClassesOptions(classes,opts); urls.js.map(function(u){return ObjC.unwrap(u.path)}).join("\n")`)
	default:
		cmd = exec.Command("sh", "-c", `command -v xclip >/dev/null 2>&1 || exit 0; xclip -selection clipboard -t text/uri-list -o 2>/dev/null || exit 0`)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read local file clipboard: %w", err)
	}
	lines := bytes.Split(bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n")), []byte("\n"))
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		value := strings.TrimSpace(string(line))
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		if strings.HasPrefix(value, "file://") {
			parsed, parseErr := url.Parse(value)
			if parseErr != nil {
				return nil, fmt.Errorf("parse clipboard file URI %q: %w", value, parseErr)
			}
			value = parsed.Path
		}
		paths = append(paths, value)
	}
	return paths, nil
}

func setLocalClipboardFiles(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	encoded, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("osascript", "-l", "JavaScript", "-e", `ObjC.import("AppKit"); var paths=JSON.parse(ObjC.unwrap($.NSProcessInfo.processInfo.environment.objectForKey("OPSKAT_CLIPBOARD_FILES"))); var urls=paths.map(function(p){return $.NSURL.fileURLWithPath(p)}); var pb=$.NSPasteboard.generalPasteboard; pb.clearContents; if (!pb.writeObjects(urls)) throw new Error("NSPasteboard rejected file URLs");`)
	default:
		cmd = exec.Command("sh", "-c", `command -v xclip >/dev/null 2>&1 || exit 1; xclip -selection clipboard -t text/uri-list -i`)
		var input strings.Builder
		for _, path := range paths {
			input.WriteString("file://")
			input.WriteString(path)
			input.WriteString("\r\n")
		}
		cmd.Stdin = strings.NewReader(input.String())
	}
	cmd.Env = append(os.Environ(), "OPSKAT_CLIPBOARD_FILES="+string(encoded))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("set local file clipboard: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
