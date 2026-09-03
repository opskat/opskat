// cmd/devserver/host.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/opskat/opskat/pkg/extension"
	"go.uber.org/zap"
)

// Compile-time check: DevServerHost must satisfy extension.HostProvider.
var _ extension.HostProvider = (*DevServerHost)(nil)

// DevServerHost is the packaged app's host provider with the three pieces the
// devserver has to supply differently: asset config comes from a JSON file
// instead of the database, KV lives in memory, and logs / action events are
// mirrored to the WebSocket the dev UI listens on.
//
// Everything else — IO opening, tunnels, deadlines — is DefaultHostProvider.
// It used to be a second 168-line implementation, which is how the two drifted
// (no TCP in dev, a different IORead EOF path) without anyone noticing.
type DevServerHost struct {
	*extension.DefaultHostProvider

	dataDir string
	kv      *memoryKV
	logger  *zap.Logger
	logCb   func(level, msg string)
	eventCb func(eventType string, data json.RawMessage)
}

func NewDevServerHost(dataDir string) *DevServerHost {
	h := &DevServerHost{
		dataDir: dataDir,
		kv:      newMemoryKV(),
		logger:  zap.L(),
	}
	h.DefaultHostProvider = extension.NewDefaultHostProvider(extension.DefaultHostConfig{
		AssetConfigs: fileAssetConfig{dataDir: dataDir},
		FileDialogs:  unsupportedFileDialog{},
		KV:           h.kv,
		ActionEvents: h,
	})
	return h
}

// newExtensionHost builds the host a devserver plugin runs against: the dev
// provider behind the same capability enforcement the packaged app applies.
// Skipping that wrapper is what let an extension read any path during
// development and only fail once installed into the real app.
func newExtensionHost(m *extension.Manifest, extDir, dataDir string) (*DevServerHost, extension.HostProvider) {
	dev := NewDevServerHost(dataDir)
	return dev, extension.NewCapabilityHost(dev, m, extDir)
}

// SetLogCallback sets a callback for log messages (WebSocket broadcast).
func (h *DevServerHost) SetLogCallback(cb func(level, msg string)) {
	h.logCb = cb
}

// SetEventCallback sets a callback for action events (WebSocket broadcast).
func (h *DevServerHost) SetEventCallback(cb func(eventType string, data json.RawMessage)) {
	h.eventCb = cb
}

// Log mirrors the extension's output to the dev UI as well as the process log.
func (h *DevServerHost) Log(level, msg string) {
	switch level {
	case "debug":
		h.logger.Debug(msg)
	case "info":
		h.logger.Info(msg)
	case "warn":
		h.logger.Warn(msg)
	case "error":
		h.logger.Error(msg)
	default:
		h.logger.Info(msg)
	}
	if h.logCb != nil {
		h.logCb(level, msg)
	}
}

// OnActionEvent implements extension.ActionEventHandler.
func (h *DevServerHost) OnActionEvent(eventType string, data json.RawMessage) error {
	if h.eventCb != nil {
		h.eventCb(eventType, data)
	}
	return nil
}

// fileAssetConfig serves the asset config from <dataDir>/config.json, which the
// dev UI edits directly.
type fileAssetConfig struct{ dataDir string }

func (c fileAssetConfig) GetAssetConfig(int64) (json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(c.dataDir, "config.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return json.RawMessage("{}"), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if len(data) == 0 {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(data), nil
}

// unsupportedFileDialog stands in for the native dialogs, which need the Wails
// window the devserver does not have.
type unsupportedFileDialog struct{}

func (unsupportedFileDialog) FileDialog(string, extension.DialogOptions) (string, error) {
	return "", fmt.Errorf("file dialog not supported in DevServer")
}

// memoryKV is the extension KV store, reset on every devserver restart.
type memoryKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemoryKV() *memoryKV { return &memoryKV{m: make(map[string][]byte)} }

func (k *memoryKV) Get(key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[key], nil
}

func (k *memoryKV) Set(key string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

// Snapshot returns the current contents for the dev UI's KV inspector.
func (k *memoryKV) Snapshot() map[string]string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make(map[string]string, len(k.m))
	for key, v := range k.m {
		out[key] = string(v)
	}
	return out
}
