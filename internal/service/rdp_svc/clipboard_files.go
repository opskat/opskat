package rdp_svc

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"go.uber.org/zap"
)

// readLocalClipboardFilePaths reads file paths off the local OS clipboard, and
// setLocalClipboardFiles writes them, both operating on the platform's native
// file-list clipboard format (Windows CF_HDROP, macOS NSPasteboard file URLs,
// Linux text/uri-list). Wails' clipboard runtime is text-only, so these can't go
// through it — the per-OS implementations live in clipboard_files_windows.go and
// clipboard_files_unix.go.

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
