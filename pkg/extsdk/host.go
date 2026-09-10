package opskat

import "encoding/json"

// Log sends a log message to the host.
func Log(level, msg string) {
	hostLog(level, msg)
}

// GetAssetConfig retrieves the named asset's configuration JSON.
// Password fields (format:"password" in configSchema) are decrypted by the host.
//
// A tool or action normally wants the asset its own call runs against, which the
// host already named: use ctx.AssetConfig() instead of carrying an id around.
func GetAssetConfig(assetID int64) (json.RawMessage, error) {
	return hostAssetGetConfig(assetID)
}

// DialogOptions for FileDialog.
type DialogOptions struct {
	Title       string   `json:"title"`
	DefaultName string   `json:"defaultName"`
	Filters     []string `json:"filters"`
}

// FileDialog opens a native file dialog (open/save).
func FileDialog(dialogType string, opts DialogOptions) (string, error) {
	params, _ := json.Marshal(map[string]any{
		"type": dialogType,
		"opts": opts,
	})
	return hostFileDialog(params)
}

// KVGet retrieves a value from the extension's private key-value store.
func KVGet(key string) ([]byte, error) {
	return hostKVGet(key)
}

// KVSet stores a value in the extension's private key-value store.
func KVSet(key string, value []byte) error {
	return hostKVSet(key, value)
}
