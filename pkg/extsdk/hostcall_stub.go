//go:build !wasip1

package opskat

import (
	"encoding/json"
	"fmt"
)

// currentHost holds the active host implementation. Defaults to nopHost.
var currentHost hostCaller = &nopHost{}

// SetHostStub replaces the host implementation. Pass nil to reset to nop.
// Used by TestHost. Not available in WASM builds.
func SetHostStub(h hostCaller) {
	if h == nil {
		currentHost = &nopHost{}
	} else {
		currentHost = h
	}
}

// nopHost returns errors for all calls except Log (which is silently dropped).
type nopHost struct{}

func (n *nopHost) Log(level, msg string)                             {}
func (n *nopHost) IOOpen(params []byte) (uint32, []byte, error)      { return 0, nil, errNotConfigured }
func (n *nopHost) IORead(handleID uint32, size int) ([]byte, error)  { return nil, errNotConfigured }
func (n *nopHost) IOWrite(handleID uint32, data []byte) (int, error) { return 0, errNotConfigured }
func (n *nopHost) IOFlush(handleID uint32) ([]byte, error)           { return nil, errNotConfigured }
func (n *nopHost) IOClose(handleID uint32) error                     { return errNotConfigured }
func (n *nopHost) IOSetDeadline(handleID uint32, kind string, unixNanos int64) error {
	return errNotConfigured
}
func (n *nopHost) AssetGetConfig(assetID int64) (json.RawMessage, error) {
	return nil, errNotConfigured
}
func (n *nopHost) FileDialog(params []byte) (string, error)  { return "", errNotConfigured }
func (n *nopHost) KVGet(key string) ([]byte, error)          { return nil, errNotConfigured }
func (n *nopHost) KVSet(key string, value []byte) error      { return errNotConfigured }
func (n *nopHost) ActionEvent(eventType string, data []byte) {}
func (n *nopHost) ActionShouldStop() bool                    { return false }

var errNotConfigured = fmt.Errorf("host not configured: use TestHost or run inside WASM")
