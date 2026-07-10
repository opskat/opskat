package rdp_svc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/opskat/opskat/internal/pkg/executil"
	"go.uber.org/zap"
)

func readLocalClipboardFilePaths() ([]string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("osascript", "-l", "JavaScript", "-e", `ObjC.import("AppKit"); var pb=$.NSPasteboard.generalPasteboard; var classes=$.NSArray.arrayWithObject($.NSURL); var opts=$.NSDictionary.dictionaryWithObjectForKey(true,$.NSPasteboardURLReadingFileURLsOnlyKey); var urls=pb.readObjectsForClassesOptions(classes,opts); urls.js.map(function(u){return ObjC.unwrap(u.path)}).join("\n")`)
	case "windows":
		cmd = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `[Console]::OutputEncoding=[Text.UTF8Encoding]::new(); $v=Get-Clipboard -Format FileDropList; if ($v) { $v | ForEach-Object { $_.FullName } }`)
		executil.HideWindow(cmd)
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

func clipboardFilesFromPaths(paths []string) ([]rdp.ClipboardFile, error) {
	files := make([]rdp.ClipboardFile, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat clipboard file %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("clipboard path is not a regular file: %s", path)
		}
		localPath := path
		files = append(files, rdp.ClipboardFile{
			Name:    filepath.Base(path),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			ReadRange: func(offset int64, length int) ([]byte, error) {
				file, err := os.Open(localPath)
				if err != nil {
					return nil, err
				}
				defer file.Close()
				data := make([]byte, length)
				_, err = file.ReadAt(data, offset)
				return data, err
			},
		})
	}
	return files, nil
}

func (s *Service) receiveClipboardFiles(log *zap.Logger, sess *session, descriptors []rdp.ClipboardFileDescriptor) {
	sess.clipboardDownloadMu.Lock()
	defer sess.clipboardDownloadMu.Unlock()
	if len(descriptors) == 0 {
		return
	}
	if len(descriptors) > 100 {
		err := fmt.Errorf("remote clipboard contains too many files: %d", len(descriptors))
		log.Error("receive RDP clipboard files failed", zap.String("sessionID", sess.id), zap.Error(err))
		if s.emit != nil {
			s.emit(Event{Type: "clipboard-error", SessionID: sess.id, Error: err.Error()})
		}
		return
	}
	var totalSize int64
	for _, descriptor := range descriptors {
		if descriptor.Size < 0 || descriptor.Size > 10<<30 || totalSize > (20<<30)-descriptor.Size {
			err := fmt.Errorf("remote clipboard file size limit exceeded")
			log.Error("receive RDP clipboard files failed", zap.String("sessionID", sess.id), zap.Error(err))
			if s.emit != nil {
				s.emit(Event{Type: "clipboard-error", SessionID: sess.id, Error: err.Error()})
			}
			return
		}
		totalSize += descriptor.Size
	}
	tempDir, err := os.MkdirTemp("", "opskat-rdp-clipboard-")
	if err != nil {
		log.Error("create RDP clipboard temp directory failed", zap.String("sessionID", sess.id), zap.Error(err))
		return
	}
	paths := make([]string, 0, len(descriptors))
	for index, descriptor := range descriptors {
		if descriptor.IsDirectory {
			err = fmt.Errorf("remote clipboard directories are not supported: %s", descriptor.Name)
			break
		}
		name := filepath.Base(strings.ReplaceAll(descriptor.Name, `\`, "/"))
		if name == "." || name == "" {
			err = fmt.Errorf("invalid remote clipboard file name: %q", descriptor.Name)
			break
		}
		path := filepath.Join(tempDir, name)
		if err = receiveClipboardFile(sess, uint32(index), descriptor, path); err != nil {
			break
		}
		paths = append(paths, path)
	}
	if err == nil {
		err = setLocalClipboardFiles(paths)
	}
	if err != nil {
		_ = os.RemoveAll(tempDir)
		log.Error("receive RDP clipboard files failed", zap.String("sessionID", sess.id), zap.Error(err))
		if s.emit != nil {
			s.emit(Event{Type: "clipboard-error", SessionID: sess.id, Error: err.Error()})
		}
		return
	}
	oldTempDir := sess.clipboardTempDir
	sess.clipboardTempDir = tempDir
	if oldTempDir != "" {
		_ = os.RemoveAll(oldTempDir)
	}
	if s.emit != nil {
		s.emit(Event{Type: "clipboard-files", SessionID: sess.id, Count: len(paths)})
	}
}

func receiveClipboardFile(sess *session, listIndex uint32, descriptor rdp.ClipboardFileDescriptor, path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	const chunkSize = uint32(1024 * 1024)
	streamID := listIndex + 1
	for offset := int64(0); offset < descriptor.Size; {
		length := min(int64(chunkSize), descriptor.Size-offset)
		if err := sess.client.RequestClipboardFileRange(listIndex, streamID, offset, uint32(length)); err != nil {
			return err
		}
		response, err := waitFileContents(sess, streamID)
		if err != nil {
			return err
		}
		if len(response) != int(length) {
			return fmt.Errorf("remote clipboard file %q returned %d bytes, want %d", descriptor.Name, len(response), length)
		}
		if _, err := file.Write(response); err != nil {
			return err
		}
		offset += length
		streamID++
	}
	if err := file.Close(); err != nil {
		return err
	}
	if !descriptor.ModTime.IsZero() {
		_ = os.Chtimes(path, descriptor.ModTime, descriptor.ModTime)
	}
	return nil
}

func waitFileContents(sess *session, streamID uint32) ([]byte, error) {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case response := <-sess.fileContents:
			if response.streamID != streamID {
				continue
			}
			return response.data, response.err
		case <-sess.done:
			return nil, io.EOF
		case <-timer.C:
			return nil, fmt.Errorf("remote clipboard file request timed out")
		}
	}
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
	case "windows":
		cmd = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$paths=ConvertFrom-Json $env:OPSKAT_CLIPBOARD_FILES; Set-Clipboard -Path $paths`)
		executil.HideWindow(cmd)
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
