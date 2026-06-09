package import_svc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// windTermImportSession 缓存最近一次预览所选文件的内容，供后续导入复用。
// 同时只可能存在一个导入对话框，因此用单槽缓存：新预览会顶掉旧的，
// 内存恒定为一份文件，无需 TTL/淘汰，天然规避了「预览后取消」的泄漏。
var windTermImportSession = struct {
	sync.Mutex
	id   string
	data []byte
}{}

func NewWindTermImportSession(data []byte) (string, error) {
	id, err := newImportSessionID()
	if err != nil {
		return "", err
	}
	windTermImportSession.Lock()
	windTermImportSession.id = id
	windTermImportSession.data = append([]byte(nil), data...)
	windTermImportSession.Unlock()
	return id, nil
}

func WindTermImportSessionData(id string) ([]byte, bool) {
	windTermImportSession.Lock()
	defer windTermImportSession.Unlock()
	if id == "" || windTermImportSession.id != id {
		return nil, false
	}
	return append([]byte(nil), windTermImportSession.data...), true
}

func DeleteWindTermImportSession(id string) {
	windTermImportSession.Lock()
	if windTermImportSession.id == id {
		windTermImportSession.id = ""
		windTermImportSession.data = nil
	}
	windTermImportSession.Unlock()
}

func newImportSessionID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("生成导入会话失败: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
