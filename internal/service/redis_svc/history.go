package redis_svc

import (
	"fmt"
	"strings"
	"sync"
)

type CommandHistoryEntry struct {
	AssetID    int64  `json:"assetId"`
	DB         int    `json:"db"`
	Command    string `json:"command"`
	CostMillis int64  `json:"costMillis"`
	Error      string `json:"error,omitempty"`
	Timestamp  int64  `json:"timestamp"`
}

type CommandHistory struct {
	mu      sync.RWMutex
	limit   int
	entries []CommandHistoryEntry
}

func NewCommandHistory(limit int) *CommandHistory {
	if limit <= 0 {
		limit = 200
	}
	return &CommandHistory{limit: limit}
}

func (h *CommandHistory) Add(entry CommandHistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append([]CommandHistoryEntry{entry}, h.entries...)
	if len(h.entries) > h.limit {
		h.entries = h.entries[:h.limit]
	}
}

func (h *CommandHistory) List(assetID int64, limit int) []CommandHistoryEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if limit <= 0 || limit > h.limit {
		limit = h.limit
	}
	out := make([]CommandHistoryEntry, 0, min(limit, len(h.entries)))
	for _, entry := range h.entries {
		if assetID > 0 && entry.AssetID != assetID {
			continue
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func formatCommandForHistory(args []any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, quoteHistoryArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteHistoryArg(arg any) string {
	s := fmt.Sprint(arg)
	if strings.ContainsAny(s, " \t\r\n\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
