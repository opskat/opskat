package import_svc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// singleSlotSession 单槽导入会话：缓存最近一次预览所选文件的内容，供后续导入复用。
// 同时只可能存在一个导入对话框，因此用单槽缓存：新预览会顶掉旧的，
// 内存恒定为一份文件，无需 TTL/淘汰，天然规避了「预览后取消」的泄漏。
type singleSlotSession[T any] struct {
	sync.Mutex
	id   string
	data T
}

func (s *singleSlotSession[T]) Put(data T) (string, error) {
	id, err := newImportSessionID()
	if err != nil {
		return "", err
	}
	s.Lock()
	s.id = id
	s.data = data
	s.Unlock()
	return id, nil
}

func (s *singleSlotSession[T]) Get(id string) (T, bool) {
	s.Lock()
	defer s.Unlock()
	var zero T
	if id == "" || s.id != id {
		return zero, false
	}
	return s.data, true
}

func (s *singleSlotSession[T]) Delete(id string) {
	s.Lock()
	if s.id == id {
		s.id = ""
		var zero T
		s.data = zero
	}
	s.Unlock()
}

var windTermImportSession = &singleSlotSession[[]byte]{}

func NewWindTermImportSession(data []byte) (string, error) {
	return windTermImportSession.Put(data)
}

func WindTermImportSessionData(id string) ([]byte, bool) {
	return windTermImportSession.Get(id)
}

func DeleteWindTermImportSession(id string) {
	windTermImportSession.Delete(id)
}

func newImportSessionID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("生成导入会话失败: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
