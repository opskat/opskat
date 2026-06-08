package import_svc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

var windTermImportSessions = struct {
	sync.Mutex
	data map[string][]byte
}{data: make(map[string][]byte)}

func NewWindTermImportSession(data []byte) (string, error) {
	id, err := newImportSessionID()
	if err != nil {
		return "", err
	}
	windTermImportSessions.Lock()
	windTermImportSessions.data[id] = append([]byte(nil), data...)
	windTermImportSessions.Unlock()
	return id, nil
}

func WindTermImportSessionData(id string) ([]byte, bool) {
	windTermImportSessions.Lock()
	defer windTermImportSessions.Unlock()
	data, ok := windTermImportSessions.data[id]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), data...), true
}

func DeleteWindTermImportSession(id string) {
	windTermImportSessions.Lock()
	delete(windTermImportSessions.data, id)
	windTermImportSessions.Unlock()
}

func newImportSessionID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("生成导入会话失败: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
