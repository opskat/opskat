package opskat

import "encoding/json"

// hostCaller abstracts host function calls.
// In WASM: backed by //go:wasmimport.
// In tests: backed by pluggable stub (TestHost).
type hostCaller interface {
	Log(level, msg string)
	IOOpen(params []byte) (handleID uint32, metaJSON []byte, err error)
	IORead(handleID uint32, size int) ([]byte, error)
	IOWrite(handleID uint32, data []byte) (int, error)
	IOFlush(handleID uint32) (metaJSON []byte, err error)
	IOClose(handleID uint32) error
	IOSetDeadline(handleID uint32, kind string, unixNanos int64) error
	AssetGetConfig(assetID int64) (json.RawMessage, error)
	FileDialog(params []byte) (string, error)
	KVGet(key string) ([]byte, error)
	KVSet(key string, value []byte) error
	ActionEvent(eventType string, data []byte)
	ActionShouldStop() bool
}

// Package-level host call functions. All SDK code calls through these.
func hostLog(level, msg string)                            { currentHost.Log(level, msg) }
func hostIOOpen(params []byte) (uint32, []byte, error)     { return currentHost.IOOpen(params) }
func hostIORead(handleID uint32, size int) ([]byte, error) { return currentHost.IORead(handleID, size) }
func hostIOWrite(handleID uint32, data []byte) (int, error) {
	return currentHost.IOWrite(handleID, data)
}
func hostIOFlush(handleID uint32) ([]byte, error) { return currentHost.IOFlush(handleID) }
func hostIOClose(handleID uint32) error           { return currentHost.IOClose(handleID) }
func hostIOSetDeadline(handleID uint32, kind string, unixNanos int64) error {
	return currentHost.IOSetDeadline(handleID, kind, unixNanos)
}
func hostAssetGetConfig(assetID int64) (json.RawMessage, error) {
	return currentHost.AssetGetConfig(assetID)
}
func hostFileDialog(params []byte) (string, error)  { return currentHost.FileDialog(params) }
func hostKVGet(key string) ([]byte, error)          { return currentHost.KVGet(key) }
func hostKVSet(key string, value []byte) error      { return currentHost.KVSet(key, value) }
func hostActionEvent(eventType string, data []byte) { currentHost.ActionEvent(eventType, data) }
func hostActionShouldStop() bool                    { return currentHost.ActionShouldStop() }
