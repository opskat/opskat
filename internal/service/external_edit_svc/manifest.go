package external_edit_svc

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

func (s *Service) loadManifest() error {
	data, err := os.ReadFile(s.manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return s.writeManifest(&manifestFile{Version: manifestVersion, Sessions: []*Session{}})
		}
		return fmt.Errorf("read manifest: %w", err)
	}

	var manifest manifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.Version == 0 {
		manifest.Version = manifestVersion
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range manifest.Sessions {
		if session == nil || session.ID == "" {
			continue
		}
		s.normalizeLoadedSessionLocked(session)
		if isExternalEditClipboardResidueSession(session) {
			if session.WorkspaceDir != "" {
				_ = cleanupWorkspace(session.WorkspaceRoot, session.WorkspaceDir)
			}
			continue
		}
		s.sessions[session.ID] = session
	}
	s.normalizeDocumentFamiliesLocked()
	return nil
}

func (s *Service) restoreSessions() error {
	now := s.now()
	retentionDays := s.cleanupRetentionDays()
	retentionCutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	expireAt := now.Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()

	var restored []*Session
	var cleaned []string
	var retentionTargets []workspaceTarget

	s.mu.Lock()
	// 恢复阶段只保留 active / conflict / recovery / unresolved 主链路和仍在 retention 窗口内的副本。
	// 非活动稿、失配 manifest 记录和过期历史副本统一按 cleanupRetentionDays 清理。
	for id, session := range s.sessions {
		if session == nil {
			delete(s.sessions, id)
			continue
		}
		if isExternalEditClipboardResidueSession(session) {
			cleaned = append(cleaned, id)
			s.removeSessionLocked(id)
			continue
		}
		if _, err := os.Stat(session.LocalPath); err != nil {
			cleaned = append(cleaned, id)
			s.removeSessionLocked(id)
			continue
		}
		if err := s.hydrateSessionEncodingLocked(session); err != nil {
			logger.Default().Warn("restore external edit encoding metadata", zap.String("sessionId", id), zap.Error(err))
			cleaned = append(cleaned, id)
			s.removeSessionLocked(id)
			continue
		}
		session.SaveMode = saveModeManualRestore
		session.PendingReview = false
		if session.UpdatedAt <= expireAt && canCleanupRetainedSession(session) {
			cleaned = append(cleaned, id)
			s.removeSessionLocked(id)
			continue
		}
		if session.UpdatedAt <= expireAt && session.State != sessionStateConflict && session.State != sessionStateRemoteMissing {
			session.Expired = true
			session.State = sessionStateExpired
		}
		if session.RecordState == recordStateCompleted || session.RecordState == recordStateAbandoned {
			session.Hidden = true
		}
		if !session.Hidden && session.RecordState != recordStateError && session.SaveMode == saveModeManualRestore {
			session.ResumeRequired = true
		}
		if isSyncSuppressedRecord(session) {
			restored = append(restored, cloneSession(session))
			continue
		}
		if err := s.addWatchLocked(session.WorkspaceDir); err != nil {
			logger.Default().Warn("restore external edit watcher", zap.String("path", session.WorkspaceDir), zap.Error(err))
			continue
		}
		restored = append(restored, cloneSession(session))
	}
	retentionTargets = s.collectWorkspaceTargetsLocked()
	saveErr := s.saveManifestLocked()
	s.mu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	s.cleanupBakeupRetention(retentionTargets, retentionCutoff)

	for _, session := range restored {
		s.emit(Event{Type: eventSessionRestored, Session: session})
	}
	for _, id := range cleaned {
		s.emit(Event{Type: eventSessionCleaned, Session: &Session{ID: id}})
	}

	// 程序重启后，对仍在活跃窗口内的 dirty 会话做一次远程冲突检测。
	// 这些会话在关机前可能已有本地修改，但远程在此期间也可能被其他人改写。
	// 检测在后台异步执行，不阻塞启动流程；发现冲突时通过正常的 eventSessionConflict 通知前端。
	for _, session := range restored {
		if isSyncSuppressedRecord(session) {
			continue
		}
		if session.State != sessionStateDirty && session.State != sessionStateClean {
			continue
		}
		sessionID := session.ID
		s.goTracked(func() { s.checkRemoteConflictAfterRestore(sessionID) })
	}

	return nil
}

func (s *Service) checkRemoteConflictAfterRestore(sessionID string) {
	session := s.getSession(sessionID)
	if session == nil || isSyncSuppressedRecord(session) {
		return
	}

	transport, err := s.resolveDocumentTransport(session)
	if err != nil {
		return
	}
	session, err = s.bindSessionTransport(sessionID, transport)
	if err != nil {
		return
	}

	localData, err := readLocalEditableFile(session.LocalPath, s.maxReadFileSizeBytes())
	if err != nil {
		return
	}
	localHash := hashBytes(localData)
	baseHash := sessionBaseHash(session)

	if transport.Missing {
		result := s.markSessionState(sessionID, sessionStateRemoteMissing, true, localHash)
		saveResult := &SaveResult{
			Status:   saveStatusRemoteMissing,
			Message:  "远程文件不存在，请先确认是否需要重新创建远程文件",
			Session:  result,
			Conflict: s.describeConflict(result, ""),
		}
		s.pauseAutoSaveForDocument(result.DocumentKey)
		s.emit(Event{Type: eventSessionConflict, Session: result, SaveResult: saveResult})
		return
	}

	remoteData, _, err := readRemoteEditableFile(s.remote, session.SessionID, session.RemotePath, s.maxReadFileSizeBytes())
	if err != nil {
		return
	}
	remoteHash := hashBytes(remoteData)
	if remoteHash == baseHash {
		return
	}

	result := s.markSessionState(sessionID, sessionStateConflict, true, localHash)
	saveResult := &SaveResult{
		Status:   saveStatusConflict,
		Message:  "远程文件已有新版本，请先比对差异，再决定重新读取或强制覆盖",
		Session:  result,
		Conflict: s.describeConflict(result, ""),
	}
	s.pauseAutoSaveForDocument(result.DocumentKey)
	s.emit(Event{Type: eventSessionConflict, Session: result, SaveResult: saveResult})
}

func (s *Service) normalizeLoadedSessionLocked(session *Session) {
	if session == nil {
		return
	}
	if strings.TrimSpace(session.RecordState) == "" {
		switch session.State {
		case sessionStateConflict, sessionStateRemoteMissing, sessionStateStale:
			session.RecordState = recordStateConflict
		default:
			session.RecordState = recordStateActive
		}
	}
	if strings.TrimSpace(session.SaveMode) == "" {
		session.SaveMode = saveModeManualRestore
	}
	if session.RecordState == recordStateCompleted || session.RecordState == recordStateAbandoned {
		session.Hidden = true
		session.ResumeRequired = false
	}
}

func (s *Service) normalizeDocumentFamiliesLocked() {
	families := make(map[string][]*Session)
	for _, session := range s.sessions {
		if session == nil || isExternalEditClipboardResidueSession(session) {
			continue
		}
		documentKey := strings.TrimSpace(session.DocumentKey)
		if documentKey == "" {
			continue
		}
		families[documentKey] = append(families[documentKey], session)
	}

	for _, family := range families {
		primary := pickVisibleFamilyPrimarySession(family)
		if primary == nil {
			continue
		}
		for _, session := range family {
			if session == nil || session.ID == primary.ID || session.Hidden {
				continue
			}
			session.Hidden = true
			session.ResumeRequired = false
		}
	}
}

func (s *Service) writeManifest(manifest *manifestFile) error {
	if err := os.MkdirAll(s.storageDir, 0o700); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(s.manifestPath, data, 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
