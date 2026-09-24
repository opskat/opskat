package opskat

import (
	"encoding/json"
	"fmt"
	"io"
)

// IOMeta contains metadata about an IO handle (file size, HTTP status, etc.).
type IOMeta struct {
	Size        int64             `json:"size,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Status      int               `json:"status,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// IOHandle wraps a host IO handle with io.Reader/Writer/Closer interfaces.
type IOHandle struct {
	id   uint32
	meta IOMeta
}

// IOOpen opens an IO handle. Type is "file" or "http".
// Params are type-specific: file needs "path"+"mode", http needs "method"+"url"+"headers".
func IOOpen(typ string, params map[string]any) (*IOHandle, error) {
	params["type"] = typ
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal io params: %w", err)
	}
	id, metaJSON, err := hostIOOpen(paramsJSON)
	if err != nil {
		return nil, err
	}
	var meta IOMeta
	if len(metaJSON) > 0 {
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			return nil, fmt.Errorf("parse io meta: %w", err)
		}
	}
	return &IOHandle{id: id, meta: meta}, nil
}

// ID returns the handle ID.
func (h *IOHandle) ID() uint32 { return h.id }

// Meta returns the handle's metadata.
func (h *IOHandle) Meta() *IOMeta { return &h.meta }

// Read reads up to len(p) bytes from the handle.
func (h *IOHandle) Read(p []byte) (int, error) {
	data, err := hostIORead(h.id, len(p))
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, data)
	return n, nil
}

// Write writes data to the handle.
func (h *IOHandle) Write(p []byte) (int, error) {
	return hostIOWrite(h.id, p)
}

// Flush sends the HTTP request and waits for response headers.
// Updates the handle's metadata with response info.
func (h *IOHandle) Flush() (*IOMeta, error) {
	metaJSON, err := hostIOFlush(h.id)
	if err != nil {
		return nil, err
	}
	var meta IOMeta
	if len(metaJSON) > 0 {
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			return nil, fmt.Errorf("parse io meta: %w", err)
		}
	}
	h.meta = meta
	return &meta, nil
}

// Close closes the handle and releases resources.
func (h *IOHandle) Close() error {
	return hostIOClose(h.id)
}
