// cmd/devserver/server.go
// Stub for Server — full implementation in Task 6.
package main

import "github.com/opskat/opskat/pkg/extension"

// Server serves the DevServer HTTP API and frontend.
// This is a minimal stub; the full implementation is in Task 6.
type Server struct{}

// NewServer creates a new DevServer HTTP server.
func NewServer(
	manifest *extension.Manifest,
	plugin *extension.Plugin,
	host *DevServerHost,
	extDir string,
	extFrontend string,
) *Server {
	return &Server{}
}

// ListenAndServe starts the HTTP server on the given address.
func (s *Server) ListenAndServe(addr string) error {
	return nil
}
