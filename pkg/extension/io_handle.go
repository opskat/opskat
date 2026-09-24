// pkg/extension/io_handle.go
package extension

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// IOMeta contains metadata about an IO handle.
type IOMeta struct {
	Size        int64             `json:"size,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Status      int               `json:"status,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// IOResource is an opened stream a HostProvider hands back to the runtime.
//
// A provider decides *what* to open (a sandboxed file, an HTTP request, a
// tunneled TCP conn); the runtime decides *who* may see it, by registering the
// resource in the handle table of the invocation that asked for it. Keeping the
// table out of the provider is what makes concurrent calls into one plugin
// isolated from each other.
type IOResource struct {
	Reader io.Reader
	Writer io.Writer
	Closer io.Closer
	Meta   IOMeta

	http *httpHandle // set by OpenHTTPResource; enables Flush
}

// maxIOHandles is the upper bound on handle IDs. We use half the uint32 range
// to stay safely below the WASM ABI uint32 boundary and allow overflow detection.
const maxIOHandles = (1 << 31) - 1

type ioEntry struct {
	id  uint32 // stored for defense-in-depth reuse detection in get()
	res *IOResource
}

// Adapter types to bridge httpHandle to io.Reader/Writer/Closer.
type httpReadAdapter struct{ h *httpHandle }

func (a *httpReadAdapter) Read(p []byte) (int, error) { return a.h.Read(p) }

type httpWriteAdapter struct{ h *httpHandle }

func (a *httpWriteAdapter) Write(p []byte) (int, error) { return a.h.Write(p) }

type httpCloseAdapter struct{ h *httpHandle }

func (a *httpCloseAdapter) Close() error { return a.h.Close() }

// OpenFileResource opens a file for reading or writing.
// The caller is responsible for having checked the path against the manifest.
func OpenFileResource(path, mode string) (*IOResource, error) {
	switch mode {
	case "read":
		f, err := os.Open(path) //nolint:gosec // path provided by extension runtime within sandbox
		if err != nil {
			return nil, fmt.Errorf("open file for read: %w", err)
		}
		info, err := f.Stat()
		if err != nil {
			if closeErr := f.Close(); closeErr != nil {
				logger.Default().Warn("close file after stat error", zap.Error(closeErr))
			}
			return nil, fmt.Errorf("stat file: %w", err)
		}
		return &IOResource{Reader: f, Closer: f, Meta: IOMeta{Size: info.Size()}}, nil
	case "write":
		f, err := os.Create(path) //nolint:gosec // path provided by extension runtime within sandbox
		if err != nil {
			return nil, fmt.Errorf("open file for write: %w", err)
		}
		return &IOResource{Writer: f, Closer: f}, nil
	default:
		return nil, fmt.Errorf("unknown file mode: %q", mode)
	}
}

// OpenHTTPResource prepares an HTTP request. dial may be nil for a direct connection.
func OpenHTTPResource(params IOOpenParams, dial DialFunc) (*IOResource, error) {
	h, err := newHTTPHandle(params, dial)
	if err != nil {
		return nil, err
	}
	return &IOResource{
		Reader: &httpReadAdapter{h: h},
		Writer: &httpWriteAdapter{h: h},
		Closer: &httpCloseAdapter{h: h},
		http:   h,
	}, nil
}

// NewConnResource wraps an established network connection as an IO resource.
func NewConnResource(conn net.Conn) *IOResource {
	return &IOResource{Reader: conn, Writer: conn, Closer: conn}
}

// IOHandleManager is the handle table of a single WASM invocation. Handle IDs
// are only meaningful within the invocation that opened them, and everything
// still open is closed when that invocation ends.
type IOHandleManager struct {
	mu      sync.Mutex
	handles map[uint32]*ioEntry
	nextID  atomic.Uint32
}

func NewIOHandleManager() *IOHandleManager {
	m := &IOHandleManager{
		handles: make(map[uint32]*ioEntry),
	}
	m.nextID.Store(1)
	return m
}

// Register adds an opened resource to the table and returns its handle ID.
func (m *IOHandleManager) Register(res *IOResource) (uint32, error) {
	id := m.nextID.Add(1) - 1
	if id >= maxIOHandles {
		// Handle IDs are uint32 values passed over the WASM ABI boundary; cap at half
		// the uint32 range to detect exhaustion before wrapping would cause aliasing.
		if res.Closer != nil {
			if closeErr := res.Closer.Close(); closeErr != nil {
				logger.Default().Warn("close resource after handle exhaustion", zap.Error(closeErr))
			}
		}
		return 0, fmt.Errorf("handle ID exhausted")
	}
	m.mu.Lock()
	m.handles[id] = &ioEntry{id: id, res: res}
	m.mu.Unlock()
	return id, nil
}

func (m *IOHandleManager) Read(id uint32, buf []byte) (int, error) {
	e, err := m.get(id)
	if err != nil {
		return 0, err
	}
	if e.res.Reader == nil {
		return 0, fmt.Errorf("handle %d is not readable", id)
	}
	return e.res.Reader.Read(buf)
}

func (m *IOHandleManager) Write(id uint32, data []byte) (int, error) {
	e, err := m.get(id)
	if err != nil {
		return 0, err
	}
	if e.res.Writer == nil {
		return 0, fmt.Errorf("handle %d is not writable", id)
	}
	return e.res.Writer.Write(data)
}

func (m *IOHandleManager) GetMeta(id uint32) (IOMeta, error) {
	e, err := m.get(id)
	if err != nil {
		return IOMeta{}, err
	}
	return e.res.Meta, nil
}

func (m *IOHandleManager) Close(id uint32) error {
	m.mu.Lock()
	e, ok := m.handles[id]
	if ok {
		delete(m.handles, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("handle %d not found", id)
	}
	if e.res.Closer != nil {
		return e.res.Closer.Close()
	}
	return nil
}

func (m *IOHandleManager) CloseAll() {
	m.mu.Lock()
	handles := m.handles
	m.handles = make(map[uint32]*ioEntry)
	m.mu.Unlock()
	for _, e := range handles {
		if e.res.Closer != nil {
			if err := e.res.Closer.Close(); err != nil {
				logger.Default().Warn("close IO handle", zap.Error(err))
			}
		}
	}
}

// Len reports how many handles are currently open.
func (m *IOHandleManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.handles)
}

// Flush flushes the HTTP handle (sends the request and waits for response).
func (m *IOHandleManager) Flush(id uint32) (*IOMeta, error) {
	e, err := m.get(id)
	if err != nil {
		return nil, err
	}
	if e.res.http == nil {
		return nil, fmt.Errorf("handle %d is not an HTTP handle", id)
	}
	return e.res.http.Flush()
}

func (m *IOHandleManager) get(id uint32) (*ioEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.handles[id]
	if !ok {
		return nil, fmt.Errorf("handle %d not found", id)
	}
	if e.id != id {
		return nil, fmt.Errorf("handle %d id mismatch (got %d)", id, e.id)
	}
	return e, nil
}

// deadliner is the interface satisfied by net.Conn and *os.File (Go 1.10+).
type deadliner interface {
	SetDeadline(time.Time) error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// SetDeadline sets read/write deadline on a handle whose underlying resource supports it.
// kind ∈ {"read", "write", "both"}.
func (m *IOHandleManager) SetDeadline(id uint32, kind string, t time.Time) error {
	e, err := m.get(id)
	if err != nil {
		return err
	}
	d, ok := e.res.Reader.(deadliner)
	if !ok {
		d, ok = e.res.Closer.(deadliner)
	}
	if !ok {
		return fmt.Errorf("handle %d does not support deadlines", id)
	}
	switch kind {
	case "read":
		return d.SetReadDeadline(t)
	case "write":
		return d.SetWriteDeadline(t)
	case "both":
		return d.SetDeadline(t)
	default:
		return fmt.Errorf("unknown deadline kind: %q", kind)
	}
}
