package auto_backup_svc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 一轮检查的结果。
const (
	CheckUnchanged = "unchanged" // 数据无变化，全部跳过
	CheckWritten   = "written"   // 至少写入了一个位置，且没有失败
	CheckFailed    = "failed"    // 生成备份失败，或至少一个位置写入失败
)

// TargetState 是一个位置的运行状态，持久化在 auto_backup_state.json。
type TargetState struct {
	Fingerprint   string `json:"fingerprint,omitempty"` // 最近一次成功写入的数据指纹
	LastSuccessAt int64  `json:"lastSuccessAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	LastErrorAt   int64  `json:"lastErrorAt,omitempty"`
	Failures      int    `json:"failures,omitempty"` // 连续失败次数，决定退避
	RetryAt       int64  `json:"retryAt,omitempty"`  // 失败后的下一次重试时间
	Ref           string `json:"ref,omitempty"`      // 位置自己的引用（如自动备份 Gist 的 ID）
}

// State 是自动备份的运行状态（不是设置）。
type State struct {
	Targets         map[string]*TargetState `json:"targets"`
	LastCheckAt     int64                   `json:"lastCheckAt,omitempty"`
	LastCheckResult string                  `json:"lastCheckResult,omitempty"`
	LastCheckError  string                  `json:"lastCheckError,omitempty"`
	CheckFailures   int                     `json:"checkFailures,omitempty"` // 生成备份连续失败次数
	CheckRetryAt    int64                   `json:"checkRetryAt,omitempty"`
}

func (s *State) target(id string) *TargetState {
	ts, ok := s.Targets[id]
	if !ok {
		ts = &TargetState{}
		s.Targets[id] = ts
	}
	return ts
}

func (s *State) clone() State {
	out := *s
	out.Targets = make(map[string]*TargetState, len(s.Targets))
	for id, ts := range s.Targets {
		c := *ts
		out.Targets[id] = &c
	}
	return out
}

func loadState(path string) (State, error) {
	st := State{Targets: map[string]*TargetState{}}
	data, err := os.ReadFile(path) //nolint:gosec // path is the app data directory
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("读取自动备份状态失败: %w", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{Targets: map[string]*TargetState{}}, fmt.Errorf("解析自动备份状态失败: %w", err)
	}
	if st.Targets == nil {
		st.Targets = map[string]*TargetState{}
	}
	return st, nil
}

func saveState(path string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
