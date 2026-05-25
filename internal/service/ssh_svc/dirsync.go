package ssh_svc

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/pkg/dirsync"
	"go.uber.org/zap"
)

// DirectorySyncState 表示终端目录同步状态。
type DirectorySyncState struct {
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd,omitempty"`
	CwdKnown    bool   `json:"cwdKnown"`
	Shell       string `json:"shell,omitempty"`
	ShellType   string `json:"shellType,omitempty"`
	Supported   bool   `json:"supported"`
	PromptReady bool   `json:"promptReady"`
	PromptClean bool   `json:"promptClean"`
	Busy        bool   `json:"busy"`
	Status      string `json:"status"` // "initializing" | "ready" | "unsupported"
	LastError   string `json:"lastError,omitempty"`
}

const (
	shellTypeUnsupported = "unsupported"
	shellTypeBash        = "bash"
	shellTypeZsh         = "zsh"
	shellTypeKsh         = "ksh"
	shellTypeMksh        = "mksh"

	directorySyncInitializing = "initializing"
	directorySyncReady        = "ready"
	directorySyncUnsupported  = "unsupported"

	syncSequencePrefix          = "\x1b]1337;opskat:"
	syncSequenceTerm            = "\a"
	syncSequenceParserMaxBytes  = 8 * 1024
	syncSequenceTokenBytes      = 16
	directorySyncMarkerOverflow = dirsync.CodeMarkerOverflow

	dirSyncErrInvalidTarget    = dirsync.CodeInvalidTarget
	dirSyncErrSessionClosed    = dirsync.CodeSessionClosed
	dirSyncErrTimeout          = dirsync.CodeTimeout
	dirSyncErrUnsupported      = dirsync.CodeUnsupported
	dirSyncErrCwdUnknown       = dirsync.CodeCwdUnknown
	dirSyncErrPending          = dirsync.CodePending
	dirSyncErrBusy             = dirsync.CodeBusy
	dirSyncErrNonceFailed      = dirsync.CodeNonceFailed
	dirSyncErrProbeUnsupported = dirsync.CodeProbeUnsupported
)

var (
	syncProbeInterval           = 250 * time.Millisecond
	syncProbeMaxUnusableResults = 12
)

// syncEnableTimeout bounds how long EnableSync waits for the shell to echo
// the init:pid marker after we write the hook-installer to stdin. Variable so
// tests can shorten it.
var syncEnableTimeout = 3 * time.Second

// syncFirstCwdGrace bounds the additional wait, after init:pid arrives, for
// the first prompt nonce / probe to populate cwd. Without this, the first
// F→T click after lazy enable can race the not-yet-arrived prompt nonce and
// surface a spurious CWD_UNKNOWN toast.
var syncFirstCwdGrace = 500 * time.Millisecond

func (s *Session) GetSyncState() DirectorySyncState {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncState
}

// ChangeDirectory 在终端提示符可用时切换目录，并等待 shell 确认结果。
func (s *Session) ChangeDirectory(targetPath string) error {
	return s.ChangeDirectoryTo(targetPath, targetPath)
}

// ChangeDirectoryDirect writes a cd command without requiring directory-sync
// prompt hooks. It is used when the caller already knows the absolute target
// path and only needs the interactive terminal to move there.
func (s *Session) ChangeDirectoryDirect(targetPath string) error {
	if targetPath == "" {
		return dirsync.Error(dirSyncErrInvalidTarget)
	}
	return s.writeInternal([]byte(buildDirectoryChangeCommand(targetPath)))
}

// ChangeDirectoryTo switches the terminal to targetPath and treats expectedPath
// as the canonical cwd reported by the remote shell after the change.
func (s *Session) ChangeDirectoryTo(targetPath, expectedPath string) error {
	if targetPath == "" {
		return dirsync.Error(dirSyncErrInvalidTarget)
	}
	if expectedPath == "" {
		expectedPath = targetPath
	}

	resultCh := make(chan error, 1)
	command, err := s.prepareDirectoryChange(targetPath, expectedPath, resultCh)
	if err != nil {
		return err
	}

	if err := s.writeInternal([]byte(command)); err != nil {
		s.failPendingDirectoryChange(err)
		return err
	}
	s.ensureSyncProbe()

	select {
	case result := <-resultCh:
		return result
	case <-time.After(4 * time.Second):
		s.failPendingDirectoryChange(dirsync.Error(dirSyncErrTimeout))
		return dirsync.Error(dirSyncErrTimeout)
	}
}

func (s *Session) initSyncState(shellPath, shellType string, supported bool) {
	state := DirectorySyncState{
		SessionID:   s.ID,
		Shell:       shellPath,
		ShellType:   shellType,
		Supported:   supported,
		PromptReady: false,
		PromptClean: true,
		Status:      directorySyncUnsupported,
	}
	if supported {
		state.Status = directorySyncInitializing
	}
	// Busy means "currently mid-sync, can't accept another op". For an
	// unsupported session, sync was never started — the toggle button must
	// not be gated on busy or the user can't enable in the first place.
	state.Busy = supported && (!state.PromptReady || !state.PromptClean)

	s.syncMu.Lock()
	s.syncState = state
	s.syncDirty = supported
	s.syncMu.Unlock()
	s.emitSyncState(state)
}

func (s *Session) markUserInput(data []byte) {
	if len(data) == 0 {
		return
	}

	s.syncMu.Lock()
	if !s.syncState.Supported {
		s.syncMu.Unlock()
		return
	}

	hasNewline := bytes.ContainsAny(data, "\r\n")
	changed := false
	if s.syncState.PromptReady {
		if s.syncState.PromptClean {
			s.syncState.PromptClean = false
			changed = true
		}
		if hasNewline {
			s.syncState.PromptReady = false
			s.syncState.CwdKnown = false
			s.syncState.Cwd = ""
			s.syncState.Status = directorySyncInitializing
			s.syncDirty = true
			changed = true
		}
	}
	if changed {
		s.syncState.Busy = !s.syncState.PromptReady || !s.syncState.PromptClean
		state := s.syncState
		go s.emitSyncState(state)
	}
	s.syncMu.Unlock()
}

func (s *Session) notePrompt(cwd string) {
	s.syncMu.Lock()
	if !s.syncState.Supported {
		s.syncMu.Unlock()
		return
	}
	s.syncState.Cwd = strings.TrimRight(cwd, "\r\n")
	s.syncState.CwdKnown = s.syncState.Cwd != ""
	s.syncState.PromptReady = true
	s.syncState.PromptClean = true
	s.syncState.Busy = false
	s.syncState.Status = directorySyncReady
	s.syncState.LastError = ""
	s.syncDirty = false
	state := s.syncState
	s.syncMu.Unlock()
	s.emitSyncState(state)
}

func (s *Session) noteObservedCwd(cwd string) {
	cleaned := strings.TrimRight(cwd, "\r\n")
	if cleaned == "" {
		return
	}

	s.syncMu.Lock()
	if !s.syncState.Supported {
		s.syncMu.Unlock()
		return
	}
	s.syncState.Cwd = cleaned
	s.syncState.CwdKnown = true
	s.syncDirty = !s.syncState.PromptReady || !s.syncState.PromptClean
	state := s.syncState
	s.syncMu.Unlock()
	s.emitSyncState(state)
}

func (s *Session) noteProbePrompt(cwd string) {
	cleaned := strings.TrimRight(cwd, "\r\n")
	if cleaned == "" {
		return
	}

	s.syncMu.Lock()
	if !s.syncState.Supported || s.pendingDirChange != nil {
		s.syncMu.Unlock()
		return
	}
	s.syncState.Cwd = cleaned
	s.syncState.CwdKnown = true
	s.syncState.PromptReady = true
	s.syncState.PromptClean = true
	s.syncState.Busy = false
	s.syncState.Status = directorySyncReady
	s.syncState.LastError = ""
	s.syncDirty = false
	state := s.syncState
	s.syncMu.Unlock()
	s.emitSyncState(state)
}

func (s *Session) prepareDirectoryChange(targetPath, expectedPath string, resultCh chan error) (string, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	switch {
	case !s.syncState.Supported:
		return "", dirsync.Error(dirSyncErrUnsupported)
	case !s.syncState.CwdKnown:
		return "", dirsync.Error(dirSyncErrCwdUnknown)
	case s.pendingDirChange != nil:
		return "", dirsync.Error(dirSyncErrPending)
	case !s.syncState.PromptReady || !s.syncState.PromptClean:
		return "", dirsync.Error(dirSyncErrBusy)
	}

	nonce, err := generateSyncToken()
	if err != nil {
		return "", dirsync.Error(dirSyncErrNonceFailed)
	}
	s.pendingDirChange = resultCh
	s.pendingDirNonce = nonce
	s.pendingDirTarget = targetPath
	s.pendingDirExpected = expectedPath
	s.syncState.PromptReady = false
	s.syncState.PromptClean = false
	s.syncState.CwdKnown = false
	s.syncState.Cwd = ""
	s.syncState.Busy = true
	s.syncState.Status = directorySyncInitializing
	s.syncState.LastError = ""
	s.syncDirty = true
	state := s.syncState

	s.emitSyncState(state)
	return buildDirectoryChangeCommand(targetPath), nil
}

func (s *Session) finishDirectoryChange(err error, cwd string) {
	s.syncMu.Lock()
	ch := s.pendingDirChange
	s.pendingDirChange = nil
	s.pendingDirNonce = ""
	s.pendingDirTarget = ""
	s.pendingDirExpected = ""
	if cwd != "" {
		s.syncState.Cwd = strings.TrimRight(cwd, "\r\n")
		s.syncState.CwdKnown = s.syncState.Cwd != ""
		s.syncState.PromptReady = true
		s.syncState.PromptClean = true
		s.syncState.Busy = false
		s.syncState.Status = directorySyncReady
	}
	if err != nil {
		s.syncState.LastError = err.Error()
	} else {
		s.syncState.LastError = ""
	}
	s.syncDirty = false
	state := s.syncState
	s.syncMu.Unlock()

	if ch != nil {
		ch <- err
		close(ch)
	}
	s.emitSyncState(state)
}

func (s *Session) failPendingDirectoryChange(err error) {
	s.syncMu.Lock()
	ch := s.pendingDirChange
	s.pendingDirChange = nil
	s.pendingDirNonce = ""
	s.pendingDirTarget = ""
	s.pendingDirExpected = ""
	if err != nil {
		s.syncState.LastError = err.Error()
	}
	state := s.syncState
	s.syncMu.Unlock()

	if ch != nil {
		ch <- err
		close(ch)
	}
	s.emitSyncState(state)
}

func (s *Session) disableDirectorySync(reason string) {
	s.syncMu.Lock()
	ch := s.pendingDirChange
	s.pendingDirChange = nil
	s.pendingDirNonce = ""
	s.pendingDirTarget = ""
	s.pendingDirExpected = ""
	s.syncProbeActive = false
	s.syncDirty = false
	s.syncState.Supported = false
	s.syncState.Cwd = ""
	s.syncState.CwdKnown = false
	s.syncState.PromptReady = false
	s.syncState.PromptClean = true
	s.syncState.Busy = false
	s.syncState.Status = directorySyncUnsupported
	s.syncState.LastError = reason
	bootstrapCh := s.syncBootstrapCh
	s.syncBootstrapCh = nil
	s.shellPID = 0
	s.syncToken = ""
	s.promptNonce = ""
	s.promptPendingNonce = ""
	state := s.syncState
	s.syncMu.Unlock()

	err := dirsync.Error(reason)
	if ch != nil {
		ch <- err
		close(ch)
	}
	if bootstrapCh != nil {
		close(bootstrapCh)
	}
	s.emitSyncState(state)
}

func (s *Session) emitSyncState(state DirectorySyncState) {
	if s.onSync == nil {
		return
	}
	s.onSync(s.ID, state)
}

func (s *Session) noteParserOverflow() {
	s.syncMu.Lock()
	if s.syncState.LastError == directorySyncMarkerOverflow {
		s.syncMu.Unlock()
		return
	}
	s.syncState.LastError = directorySyncMarkerOverflow
	state := s.syncState
	s.syncMu.Unlock()
	s.emitSyncState(state)
}

type shellProbeResult struct {
	cwd         string
	promptReady bool
}

func (s *Session) ensureSyncProbe() {
	s.syncMu.Lock()
	if s.syncProbeActive || !s.syncState.Supported || s.shellPID <= 0 || s.shared == nil || s.shared.client == nil {
		s.syncMu.Unlock()
		return
	}
	s.syncProbeActive = true
	s.syncMu.Unlock()

	go s.runSyncProbeLoop()
}

func (s *Session) runSyncProbeLoop() {
	ticker := time.NewTicker(syncProbeInterval)
	defer ticker.Stop()

	unusableResults := 0
	for {
		if s.IsClosed() {
			s.syncMu.Lock()
			s.syncProbeActive = false
			s.syncMu.Unlock()
			return
		}

		s.syncMu.Lock()
		if !s.syncState.Supported || s.shellPID <= 0 || s.shared == nil || s.shared.client == nil {
			s.syncProbeActive = false
			s.syncMu.Unlock()
			return
		}
		shouldProbe := s.syncDirty || s.pendingDirChange != nil
		pid := s.shellPID
		pending := s.pendingDirChange != nil
		pendingNonce := s.pendingDirNonce
		pendingTarget := s.pendingDirTarget
		s.syncMu.Unlock()

		if !shouldProbe {
			s.syncMu.Lock()
			s.syncProbeActive = false
			s.syncMu.Unlock()
			return
		}

		result, err := s.probeShellState(pid)
		if err != nil || result.cwd == "" {
			unusableResults++
			if unusableResults >= syncProbeMaxUnusableResults {
				s.disableDirectorySync(dirSyncErrProbeUnsupported)
				return
			}
		} else {
			unusableResults = 0
			if pending {
				s.finishPendingDirectoryChangeProbe(pendingNonce, pendingTarget, result.cwd)
			} else if result.promptReady {
				s.noteProbePrompt(result.cwd)
			} else if result.cwd != "" {
				s.noteObservedCwd(result.cwd)
			}
		}

		<-ticker.C
	}
}

func (s *Session) finishPendingDirectoryChangeProbe(nonce, targetPath, cwd string) {
	s.syncMu.Lock()
	if s.pendingDirChange == nil || s.pendingDirNonce == "" || s.pendingDirNonce != nonce {
		s.syncMu.Unlock()
		return
	}
	expectedPath := s.pendingDirExpected
	s.syncMu.Unlock()

	if cwd == "" {
		return
	}
	if expectedPath == "" {
		expectedPath = targetPath
	}
	if path.Clean(cwd) == path.Clean(expectedPath) {
		s.finishDirectoryChange(nil, cwd)
	}
}

func (s *Session) probeShellState(shellPID int) (shellProbeResult, error) {
	if s.probeShellStateFn != nil {
		return s.probeShellStateFn(shellPID)
	}
	session, err := s.shared.client.NewSession()
	if err != nil {
		return shellProbeResult{}, err
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil && closeErr != io.EOF {
			logger.Default().Warn("close shell probe session", zap.Error(closeErr))
		}
	}()

	var out bytes.Buffer
	session.Stdout = &out
	session.Stderr = io.Discard
	if err := session.Run(buildShellStateProbeCommand(shellPID)); err != nil {
		return shellProbeResult{}, err
	}
	return parseShellProbeOutput(out.Bytes())
}

func buildShellStateProbeCommand(shellPID int) string {
	return fmt.Sprintf(`sh -lc 'pid=%d
cwd=""
prompt=0
if kill -0 "$pid" 2>/dev/null; then
  cwd=$(readlink "/proc/$pid/cwd" 2>/dev/null || printf "")
  if [ -z "$cwd" ] && command -v pwdx >/dev/null 2>&1; then
    cwd=$(pwdx "$pid" 2>/dev/null | sed "s/^[^ ]* //")
  fi
  pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d " ")
  tpgid=$(ps -o tpgid= -p "$pid" 2>/dev/null | tr -d " ")
  tty_path=$(readlink "/proc/$pid/fd/0" 2>/dev/null || printf "")
  if [ -n "$tty_path" ]; then
    stty_state=$(stty -a < "$tty_path" 2>/dev/null || printf "")
    case "$stty_state" in
      *"-icanon"*"-echo"*)
        if [ -n "$pgid" ] && [ "$pgid" = "$tpgid" ]; then
          prompt=1
        fi
        ;;
    esac
  fi
fi
printf "cwd=%%s\0prompt=%%s\0" "$cwd" "$prompt"'`, shellPID)
}

func parseShellProbeOutput(raw []byte) (shellProbeResult, error) {
	result := shellProbeResult{}
	fields := bytes.Split(raw, []byte{0})
	for _, field := range fields {
		if len(field) == 0 {
			continue
		}
		key, value, ok := bytes.Cut(field, []byte{'='})
		if !ok {
			return shellProbeResult{}, fmt.Errorf("invalid probe field")
		}
		switch string(key) {
		case "cwd":
			result.cwd = string(value)
		case "prompt":
			result.promptReady = string(value) == "1"
		}
	}
	return result, nil
}

func (s *Session) queueInternalEchoSuppression(command []byte) []byte {
	pattern := normalizeTerminalEcho(command)
	if len(pattern) == 0 {
		return nil
	}

	s.outputFilterMu.Lock()
	s.echoSuppressions = append(s.echoSuppressions, pattern)
	s.outputFilterMu.Unlock()
	return pattern
}

func (s *Session) removeQueuedEchoSuppression(pattern []byte) {
	if len(pattern) == 0 {
		return
	}

	s.outputFilterMu.Lock()
	defer s.outputFilterMu.Unlock()
	for i, queued := range s.echoSuppressions {
		if bytes.Equal(queued, pattern) {
			s.echoSuppressions = append(s.echoSuppressions[:i], s.echoSuppressions[i+1:]...)
			if len(s.echoSuppressions) == 0 {
				s.echoSuppressionIdx = 0
			}
			return
		}
	}
}

func (s *Session) beginInternalScriptEchoSuppression() {
	s.outputFilterMu.Lock()
	s.internalScriptEcho = true
	s.internalEchoDropLn = false
	s.outputFilterMu.Unlock()
}

func (s *Session) endInternalScriptEchoSuppression() {
	s.outputFilterMu.Lock()
	s.internalScriptEcho = false
	s.internalEchoDropLn = false
	s.outputFilterMu.Unlock()
}

func normalizeTerminalEcho(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch b {
		case '\r', '\n':
			continue
		case 0x1b:
			i = skipANSIEscape(data, i)
		default:
			out = append(out, b)
		}
	}
	return out
}

func skipANSIEscape(data []byte, esc int) int {
	if esc+1 >= len(data) {
		return esc
	}
	next := data[esc+1]
	if next == '[' {
		for i := esc + 2; i < len(data); i++ {
			if data[i] >= 0x40 && data[i] <= 0x7e {
				return i
			}
		}
		return len(data) - 1
	}
	if next == ']' {
		for i := esc + 2; i < len(data); i++ {
			if data[i] == 0x07 {
				return i
			}
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				return i + 1
			}
		}
		return len(data) - 1
	}
	return esc + 1
}

func (s *Session) suppressInternalEcho(data []byte) []byte {
	if len(data) == 0 || (len(s.echoSuppressions) == 0 && !s.internalScriptEcho && !s.internalEchoDropLn) {
		return data
	}

	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		b := data[i]
		if s.internalEchoDropLn {
			if b == '\r' || b == '\n' {
				s.internalEchoDropLn = false
			}
			continue
		}

		if len(s.echoSuppressions) == 0 && !s.internalScriptEcho {
			out = append(out, data[i:]...)
			break
		}

		var current []byte
		if len(s.echoSuppressions) > 0 {
			current = s.echoSuppressions[0]
			if s.echoSuppressionIdx == 0 {
				if end, ok := queuedDirectoryChangeEchoLineEnd(current, data, i); ok {
					s.echoSuppressions = s.echoSuppressions[1:]
					s.echoSuppressionIdx = 0
					if end > i {
						i = end - 1
					}
					continue
				}
			}
			if s.echoSuppressionIdx < len(current) && b == current[s.echoSuppressionIdx] {
				s.echoSuppressionIdx++
				if s.echoSuppressionIdx == len(current) {
					s.echoSuppressions = s.echoSuppressions[1:]
					s.echoSuppressionIdx = 0
				}
				continue
			}

			if s.echoSuppressionIdx > 0 && isTerminalEchoNoise(data, i) {
				if b == 0x1b {
					i = skipANSIEscape(data, i)
				}
				continue
			}

			if s.echoSuppressionIdx > 0 {
				out = append(out, current[:s.echoSuppressionIdx]...)
				s.echoSuppressionIdx = 0
			}
			if len(current) > 0 && b == current[0] {
				s.echoSuppressionIdx = 1
				if len(current) == 1 {
					s.echoSuppressions = s.echoSuppressions[1:]
					s.echoSuppressionIdx = 0
				}
				continue
			}
		}
		if s.internalScriptEcho {
			if kind := internalScriptEchoLineKindAt(data, i); kind != internalScriptEchoNone {
				end := nextLineEndIncludingTerminator(data, i)
				if end >= len(data) {
					s.internalEchoDropLn = true
				}
				if kind == internalScriptEchoEnd {
					s.internalScriptEcho = false
					s.internalEchoDropLn = false
				}
				if end > i {
					i = end - 1
				}
				continue
			}
		} else {
			if isInternalScriptEchoLine(data, i) {
				end := nextLineEndIncludingTerminator(data, i)
				if end > i {
					i = end - 1
				}
				continue
			}
			if looksLikeBase64ContinuationLine(data, i) {
				end := nextLineEndIncludingTerminator(data, i)
				if end > i {
					i = end - 1
				}
				continue
			}
		}
		if s.internalScriptEcho && looksLikeBase64EchoFragment(data, i) {
			end := nextLineEndIncludingTerminator(data, i)
			if end >= len(data) {
				s.internalEchoDropLn = true
			}
			if end > i {
				i = end - 1
			}
			continue
		}
		out = append(out, b)
	}
	return out
}

type internalScriptEchoLineKind int

const (
	internalScriptEchoNone internalScriptEchoLineKind = iota
	internalScriptEchoBody
	internalScriptEchoEnd
)

func internalScriptEchoLineKindAt(data []byte, pos int) internalScriptEchoLineKind {
	lineStart := pos
	for lineStart > 0 && data[lineStart-1] != '\r' && data[lineStart-1] != '\n' {
		lineStart--
	}
	if lineStart != pos {
		return internalScriptEchoNone
	}
	lineEnd := nextLineEnd(data, pos)
	line := string(data[pos:lineEnd])
	trimmed := strings.TrimSpace(stripANSIEscapeString(line))
	if trimmed == "" {
		return internalScriptEchoNone
	}
	if strings.HasPrefix(trimmed, "heredoc> ") {
		return internalScriptEchoBody
	}
	if trimmed == "heredoc>" {
		return internalScriptEchoBody
	}
	if strings.Contains(trimmed, "base64 -d > '/tmp/.opskat-sync-") ||
		strings.Contains(trimmed, "source '/tmp/.opskat-sync-") ||
		strings.Contains(trimmed, "rm -f '/tmp/.opskat-sync-") ||
		strings.Contains(trimmed, "stty -echo 2>/dev/null") {
		return internalScriptEchoBody
	}
	if isPromptEchoLine(trimmed) {
		return internalScriptEchoBody
	}
	if strings.Contains(trimmed, "stty echo 2>/dev/null") ||
		strings.Contains(trimmed, "stty2>/dev/null") {
		return internalScriptEchoEnd
	}
	return internalScriptEchoNone
}

func isInternalScriptEchoLine(data []byte, pos int) bool {
	return internalScriptEchoLineKindAt(data, pos) != internalScriptEchoNone
}

func isPromptEchoLine(line string) bool {
	for _, marker := range []string{"➜", "$", "#", ">"} {
		idx := strings.LastIndex(line, marker)
		if idx < 0 {
			continue
		}
		after := strings.TrimSpace(line[idx+len(marker):])
		if after == "" || after == "~" || strings.HasPrefix(after, "~/") || strings.HasPrefix(after, "/") {
			return true
		}
	}
	return false
}

func queuedDirectoryChangeEchoLineEnd(queued, data []byte, pos int) (int, bool) {
	quotedTarget, ok := queuedDirectoryChangeQuotedTarget(queued)
	if !ok || !isLineStart(data, pos) {
		return 0, false
	}
	lineEnd := nextLineEnd(data, pos)
	line := strings.TrimSpace(stripANSIEscapeString(string(data[pos:lineEnd])))
	idx := strings.Index(line, "builtin cd")
	if idx < 0 {
		return 0, false
	}
	if prefix := strings.TrimSpace(line[:idx]); prefix != "" && !isPromptEchoLine(prefix) {
		return 0, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line[idx:], "builtin cd"))
	if rest == "--" || strings.HasPrefix(rest, "-- ") {
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "--"))
	}
	if rest != quotedTarget {
		return 0, false
	}
	return nextLineEndIncludingTerminator(data, pos), true
}

func queuedDirectoryChangeQuotedTarget(queued []byte) (string, bool) {
	text := strings.TrimSpace(stripANSIEscapeString(string(queued)))
	if !strings.HasPrefix(text, "builtin cd") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(text, "builtin cd"))
	if rest == "--" || strings.HasPrefix(rest, "-- ") {
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "--"))
	}
	if !strings.HasPrefix(rest, "'") {
		return "", false
	}
	return rest, true
}

func isLineStart(data []byte, pos int) bool {
	return pos == 0 || data[pos-1] == '\r' || data[pos-1] == '\n'
}

func looksLikeBase64EchoFragment(data []byte, pos int) bool {
	lineStart := pos
	for lineStart > 0 && data[lineStart-1] != '\r' && data[lineStart-1] != '\n' {
		lineStart--
	}
	if lineStart != pos {
		return false
	}
	lineEnd := nextLineEnd(data, pos)
	return looksLikeBase64EchoText(strings.TrimSpace(string(data[pos:lineEnd])))
}

func looksLikeBase64EchoText(line string) bool {
	if len(line) == 0 {
		return false
	}
	for _, r := range line {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' {
			continue
		}
		return false
	}
	return true
}

func looksLikeBase64ContinuationLine(data []byte, pos int) bool {
	lineStart := pos
	for lineStart > 0 && data[lineStart-1] != '\r' && data[lineStart-1] != '\n' {
		lineStart--
	}
	if lineStart != pos {
		return false
	}
	lineEnd := nextLineEnd(data, pos)
	line := strings.TrimSpace(string(data[pos:lineEnd]))
	if len(line) < 80 {
		return false
	}
	return looksLikeBase64EchoText(line)
}

func stripANSIEscapeString(text string) string {
	data := []byte(text)
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == 0x1b {
			i = skipANSIEscape(data, i)
			continue
		}
		out = append(out, data[i])
	}
	return string(out)
}

func isTerminalEchoNoise(data []byte, pos int) bool {
	b := data[pos]
	if b == '\r' || b == '\n' {
		return true
	}
	if bytes.HasPrefix(data[pos:], []byte("heredoc> ")) {
		return true
	}
	if pos > 0 && data[pos-1] == 'h' && bytes.HasPrefix(data[pos:], []byte("eredoc> ")) {
		return true
	}
	if b == 0x1b {
		return true
	}
	return isLikelyPromptPrefix(data, pos)
}

func isLikelyPromptPrefix(data []byte, pos int) bool {
	start := pos
	for start > 0 && data[start-1] != '\r' && data[start-1] != '\n' {
		start--
	}
	if start != pos {
		return false
	}

	end := pos
	for end < len(data) && data[end] != '\r' && data[end] != '\n' {
		if data[end] == 0x1b {
			end = skipANSIEscape(data, end) + 1
			continue
		}
		end++
	}
	line := string(data[pos:end])
	for _, marker := range []string{"➜", "$", "#", ">"} {
		idx := strings.LastIndex(line, marker)
		if idx < 0 {
			continue
		}
		after := strings.TrimSpace(line[idx+len(marker):])
		if after == "" || after == "~" || strings.HasPrefix(after, "~/") || strings.HasPrefix(after, "/") {
			return true
		}
	}
	return false
}

func nextLineEnd(data []byte, start int) int {
	for i := start; i < len(data); i++ {
		if data[i] == '\r' || data[i] == '\n' {
			return i
		}
	}
	return len(data)
}

func nextLineEndIncludingTerminator(data []byte, start int) int {
	end := nextLineEnd(data, start)
	if end < len(data) && data[end] == '\r' {
		end++
	}
	if end < len(data) && data[end] == '\n' {
		end++
	}
	return end
}

func (s *Session) filterOutput(chunk []byte) []byte {
	s.outputFilterMu.Lock()
	defer s.outputFilterMu.Unlock()

	chunk = s.suppressInternalEcho(chunk)
	if len(chunk) == 0 {
		return nil
	}

	data := chunk
	if len(s.parserRemainder) > 0 {
		data = append(append([]byte(nil), s.parserRemainder...), chunk...)
		s.parserRemainder = nil
	}

	prefix := []byte(syncSequencePrefix)
	out := make([]byte, 0, len(data))

	for len(data) > 0 {
		idx := bytes.Index(data, prefix)
		if idx < 0 {
			break
		}
		out = append(out, data[:idx]...)
		remainder := data[idx+len(prefix):]
		end := bytes.IndexByte(remainder, syncSequenceTerm[0])
		if end < 0 {
			tail := append([]byte(nil), data[idx:]...)
			if len(tail) > syncSequenceParserMaxBytes {
				s.noteParserOverflow()
				out = append(out, tail...)
				return out
			}
			s.parserRemainder = tail
			return out
		}
		rawEnd := idx + len(prefix) + end + 1
		raw := data[idx:rawEnd]
		if !s.handleSyncPayload(string(remainder[:end])) {
			out = append(out, raw...)
		}
		data = data[rawEnd:]
	}

	if len(data) == 0 {
		return out
	}

	if keep := trailingPrefixLength(data, prefix); keep > 0 {
		out = append(out, data[:len(data)-keep]...)
		s.parserRemainder = append([]byte(nil), data[len(data)-keep:]...)
		return out
	}

	out = append(out, data...)
	return out
}

func trailingPrefixLength(data, prefix []byte) int {
	maxSize := len(prefix) - 1
	if maxSize > len(data) {
		maxSize = len(data)
	}
	for size := maxSize; size > 0; size-- {
		if bytes.Equal(data[len(data)-size:], prefix[:size]) {
			return size
		}
	}
	return 0
}

func (s *Session) handleSyncPayload(payload string) bool {
	token, body, ok := strings.Cut(payload, ":")
	s.syncMu.Lock()
	syncToken := s.syncToken
	s.syncMu.Unlock()
	if !ok || token == "" || token != syncToken {
		return false
	}

	switch {
	case strings.HasPrefix(body, "init:pid:"):
		pidText := strings.TrimPrefix(body, "init:pid:")
		pid, err := strconv.Atoi(strings.TrimSpace(pidText))
		if err != nil || pid <= 0 {
			return false
		}
		s.syncMu.Lock()
		if s.syncBootstrapCh == nil || !s.syncState.Supported || s.shellPID != 0 {
			s.syncMu.Unlock()
			return false
		}
		s.shellPID = pid
		s.syncDirty = true
		bootstrap := s.syncBootstrapCh
		s.syncBootstrapCh = nil
		s.syncMu.Unlock()
		// internalScriptEcho/internalEchoDropLn 由 outputFilterMu 保护；
		// 调用方 filterOutput 已持有该锁，这里直接写入即可（sync.Mutex 不可重入，不能再 Lock）。
		s.internalScriptEcho = false
		s.internalEchoDropLn = false
		if bootstrap != nil {
			close(bootstrap)
		}
		s.ensureSyncProbe()
		return true
	case strings.HasPrefix(body, "prompt:"):
		remainder := strings.TrimPrefix(body, "prompt:")
		currentNonce, nextPayload, ok := strings.Cut(remainder, ":")
		if !ok || currentNonce == "" {
			return false
		}
		nextNonce, cwd, ok := strings.Cut(nextPayload, ":")
		if !ok || nextNonce == "" {
			return false
		}
		s.syncMu.Lock()
		promptNonce := s.promptNonce
		promptPendingNonce := s.promptPendingNonce
		shellPID := s.shellPID
		supported := s.syncState.Supported
		s.syncMu.Unlock()
		validCurrent := currentNonce == promptNonce || (promptPendingNonce != "" && currentNonce == promptPendingNonce)
		if !supported || promptNonce == "" || !validCurrent || shellPID <= 0 {
			return false
		}
		probe, err := s.probeShellState(shellPID)
		if err != nil || !probe.promptReady {
			s.syncMu.Lock()
			if currentNonce == s.promptNonce || (s.promptPendingNonce != "" && currentNonce == s.promptPendingNonce) {
				s.promptPendingNonce = nextNonce
			}
			s.syncMu.Unlock()
			return false
		}
		resolvedCwd := probe.cwd
		if resolvedCwd == "" {
			resolvedCwd = cwd
		}
		if resolvedCwd == "" {
			return false
		}
		s.syncMu.Lock()
		if currentNonce != s.promptNonce && (s.promptPendingNonce == "" || currentNonce != s.promptPendingNonce) {
			s.syncMu.Unlock()
			return false
		}
		s.promptNonce = nextNonce
		s.promptPendingNonce = ""
		s.syncMu.Unlock()
		s.notePrompt(resolvedCwd)
		return true
	}
	return false
}

// EnableSync injects the directory-sync hooks into the running interactive
// shell and waits for the init:pid marker. Idempotent: returns nil immediately
// if the session is already supported with a known shell PID.
//
// On timeout (no marker within syncEnableTimeout), state is rolled back to
// Supported=false and a CodeTimeout error is returned. Callers should map
// that to a "please exit foreground program and retry" hint in the UI.
func (s *Session) EnableSync() error {
	s.syncEnableMu.Lock()
	defer s.syncEnableMu.Unlock()

	s.syncMu.Lock()
	if s.syncState.Supported && s.shellPID > 0 {
		s.syncMu.Unlock()
		return nil
	}
	if s.shellType == shellTypeUnsupported {
		s.syncMu.Unlock()
		return dirsync.Error(dirSyncErrUnsupported)
	}

	// Lazy shell detection: deferred from createSession to avoid the probe
	// channel consuming PAM motd output before the main session shows it.
	if s.shellType == "" {
		s.syncMu.Unlock()
		shellPath, shellType := detectRemoteShell(s.shared.client)
		s.syncMu.Lock()
		s.shellPath = shellPath
		s.shellType = shellType
		s.syncState.Shell = shellPath
		s.syncState.ShellType = shellType
		if shellType == shellTypeUnsupported {
			s.syncMu.Unlock()
			return dirsync.Error(dirSyncErrUnsupported)
		}
	}

	token, err := generateSyncToken()
	if err != nil {
		s.syncMu.Unlock()
		return dirsync.Error(dirSyncErrNonceFailed)
	}
	promptNonce, err := generateSyncToken()
	if err != nil {
		s.syncMu.Unlock()
		return dirsync.Error(dirSyncErrNonceFailed)
	}
	s.syncToken = token
	s.promptNonce = promptNonce
	s.promptPendingNonce = ""
	s.shellPID = 0
	s.syncState.Supported = true
	s.syncState.Cwd = ""
	s.syncState.CwdKnown = false
	s.syncState.PromptReady = false
	s.syncState.PromptClean = true
	s.syncState.Busy = true
	s.syncState.Status = directorySyncInitializing
	s.syncState.LastError = ""
	s.syncDirty = true
	bootstrapCh := make(chan struct{})
	s.syncBootstrapCh = bootstrapCh
	state := s.syncState
	cmd := buildEnableSyncScript(s.shellType, token, promptNonce)
	s.syncMu.Unlock()

	s.emitSyncState(state)

	// cmd == "" is unreachable: shellTypeUnsupported was rejected above
	// and buildEnableSyncScript only returns "" for that case. No defensive
	// branch needed; if invariants change, the write below will fail visibly.

	if err := s.writeInternalScript(cmd); err != nil {
		st := s.rollbackSyncBootstrap(bootstrapCh, err.Error())
		s.emitSyncState(st)
		return dirsync.Error(dirsync.CodeSessionClosed)
	}

	if err := waitForSyncBootstrap(bootstrapCh); err != nil {
		s.syncMu.Lock()
		// Only roll back if we still own this bootstrap; init:pid handler may
		// have raced and already promoted the state to ready.
		if s.syncBootstrapCh == bootstrapCh {
			s.rollbackSyncBootstrapLocked(err.Error())
			st := s.syncState
			s.syncMu.Unlock()
			s.emitSyncState(st)
			return err
		}
		ok := s.syncState.Supported && s.shellPID > 0
		st := s.syncState
		s.syncMu.Unlock()
		s.emitSyncState(st)
		if !ok {
			return err
		}
	}

	// Bootstrap channel closed. Could be init:pid (success) or DisableSync
	// racing in (failure). Re-check state under lock.
	s.syncMu.Lock()
	ok := s.syncState.Supported && s.shellPID > 0
	s.syncMu.Unlock()
	if !ok {
		return dirsync.Error(dirSyncErrUnsupported)
	}

	// init:pid confirms the shell ran our injection, but cwd is filled by the
	// next prompt nonce or the probe loop. Poll briefly so the first F→T click
	// after lazy enable doesn't race a not-yet-arrived prompt. We don't surface
	// an error if the grace expires — the shell is alive (init:pid arrived);
	// the frontend will see cwd populate via the ssh:sync event whenever it
	// finally lands.
	deadline := time.Now().Add(syncFirstCwdGrace)
	for time.Now().Before(deadline) {
		s.syncMu.Lock()
		ready := s.syncState.CwdKnown && s.syncState.PromptReady && s.syncState.PromptClean
		s.syncMu.Unlock()
		if ready {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// DisableSync removes hooks from the running shell and flips state back to
// unsupported. Best-effort: if the stdin write fails, state is still cleared.
//
// No frontend caller wires this up yet — kept for symmetry with EnableSync
// and as the entry point when an explicit "disable directory sync" toggle
// ships. If the toggle never lands, this and buildDisableSyncCommand can be
// dropped.
func (s *Session) DisableSync() {
	s.syncMu.Lock()
	if !s.syncState.Supported {
		s.syncMu.Unlock()
		return
	}
	cmd := buildDisableSyncCommand(s.shellType)
	s.syncMu.Unlock()

	if cmd != "" {
		if err := s.writeInternal([]byte(cmd)); err != nil {
			logger.Default().Warn("write disable-sync command", zap.String("sessionID", s.ID), zap.Error(err))
		}
	}
	s.disableDirectorySync(dirSyncErrUnsupported)
}

func (s *Session) rollbackSyncBootstrap(ch chan struct{}, lastError string) DirectorySyncState {
	s.syncMu.Lock()
	if s.syncBootstrapCh == ch {
		s.rollbackSyncBootstrapLocked(lastError)
	}
	state := s.syncState
	s.syncMu.Unlock()
	return state
}

func (s *Session) rollbackSyncBootstrapLocked(lastError string) {
	s.syncBootstrapCh = nil
	s.shellPID = 0
	s.syncToken = ""
	s.promptNonce = ""
	s.promptPendingNonce = ""
	s.syncDirty = false
	s.syncState.Supported = false
	s.syncState.Cwd = ""
	s.syncState.CwdKnown = false
	s.syncState.PromptReady = false
	s.syncState.PromptClean = true
	s.syncState.Busy = false
	s.syncState.Status = directorySyncUnsupported
	s.syncState.LastError = lastError
}

func waitForSyncBootstrap(ch chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-time.After(syncEnableTimeout):
		return dirsync.Error(dirSyncErrTimeout)
	}
}
