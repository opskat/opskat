package rdp_svc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/sshpool"
	"go.uber.org/zap"
)

type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	// Width/Height are the full framebuffer dimensions; a frame event carries
	// the dirty sub-region at (X, Y) sized RectWidth×RectHeight.
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	X          int    `json:"x,omitempty"`
	Y          int    `json:"y,omitempty"`
	RectWidth  int    `json:"rectWidth,omitempty"`
	RectHeight int    `json:"rectHeight,omitempty"`
	Data       string `json:"data,omitempty"` // top-down RGBA of the dirty region, base64
	Text       string `json:"text,omitempty"`
	Count      int    `json:"count,omitempty"`
	// Pointer events: PointerType is "shape" (Data carries the top-down RGBA
	// cursor image sized Width×Height), "default", or "hidden".
	PointerType string `json:"pointerType,omitempty"`
	HotspotX    int    `json:"hotspotX,omitempty"`
	HotspotY    int    `json:"hotspotY,omitempty"`
}

type ConnectRequest struct {
	AssetID int64 `json:"assetId"`
	Width   int   `json:"width"`
	Height  int   `json:"height"`
}

type InputEvent struct {
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"` // "mouse" | "wheel" | "key" | "unicode"
	X         int    `json:"x,omitempty"`
	Y         int    `json:"y,omitempty"`
	Buttons   uint16 `json:"buttons,omitempty"`
	Delta     int    `json:"delta,omitempty"`
	Scancode  uint16 `json:"scancode,omitempty"`
	Codepoint uint16 `json:"codepoint,omitempty"`
	Pressed   bool   `json:"pressed,omitempty"`
	Extended  bool   `json:"extended,omitempty"` // E0-prefixed scancode (arrows, nav cluster, right modifiers)
}

type Service struct {
	mu       sync.Mutex
	sessions map[string]*session
	emit     func(Event)
	pool     *sshpool.Pool
}

type session struct {
	id                  string
	assetID             int64
	client              *rdp.Client
	streamer            *frameStreamer
	done                chan struct{}
	clipboardDownloadMu sync.Mutex
	fileContents        chan fileContentsResponse
	clipboardTempDir    string
}

type fileContentsResponse struct {
	streamID uint32
	data     []byte
	err      error
}

type framebufferSource interface {
	FramebufferDims() (int, int)
	FramebufferWriteTopDown(dst []byte) (int, int)
}

func New(emit func(Event), pool *sshpool.Pool) *Service {
	return &Service{
		sessions: make(map[string]*session),
		emit:     emit,
		pool:     pool,
	}
}

func (s *Service) Connect(ctx context.Context, req ConnectRequest) (string, error) {
	asset, err := asset_svc.Asset().Get(ctx, req.AssetID)
	if err != nil {
		return "", fmt.Errorf("get RDP asset: %w", err)
	}
	if !asset.IsRDP() {
		return "", fmt.Errorf("asset is not RDP")
	}
	cfg, err := asset.GetRDPConfig()
	if err != nil {
		return "", fmt.Errorf("parse RDP config: %w", err)
	}
	password, err := credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("resolve RDP password: %w", err)
	}
	cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)

	width, height := resolveSize(cfg, req.Width, req.Height)
	id := "rdp-" + uuid.NewString()
	log := logger.Ctx(ctx)
	log.Info("rdp connect start",
		zap.String("sessionID", id),
		zap.Int64("assetID", req.AssetID),
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("width", width),
		zap.Int("height", height),
		zap.Int64("sshTunnelID", asset.SSHTunnelID),
		zap.Bool("proxy", cfg.Proxy != nil),
	)

	opts := clientOptions(cfg, password, width, height)
	opts.DialContext = connpool.RDPDialContext(asset.SSHTunnelID, cfg, s.pool)

	client, err := rdp.NewClient(opts)
	if err != nil {
		log.Error("rdp client create failed", zap.String("sessionID", id), zap.Error(err))
		return "", err
	}

	sess := &session{
		id:           id,
		assetID:      req.AssetID,
		client:       client,
		streamer:     newFrameStreamer(),
		done:         make(chan struct{}),
		fileContents: make(chan fileContentsResponse, 1),
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	if s.emit != nil {
		s.emit(Event{Type: "connecting", SessionID: id, Message: "Connecting RDP session"})
	}

	client.OnDisconnect(func(err error) {
		if err != nil && s.emit != nil {
			s.emit(Event{Type: "error", SessionID: id, Error: err.Error()})
		}
	})
	client.OnReconnecting(func() {
		if s.emit != nil {
			s.emit(Event{Type: "connecting", SessionID: id, Message: "Resizing RDP session"})
		}
	})
	client.OnReconnected(func() {
		if s.emit != nil {
			s.emit(Event{Type: "connected", SessionID: id})
		}
	})
	// Dirty-region tracking: both callbacks fire after the update has been
	// written into the framebuffer, so marking the rect is enough — the flush
	// loop reads the region back from the framebuffer. OnStridedBitmap covers
	// the EGFX pipeline, OnBitmap everything else; both must be registered
	// before Connect so the EGFX channel wires them up.
	client.OnBitmap(func(u *rdp.BitmapUpdate) {
		sess.streamer.markDirty(u.X, u.Y, u.Width, u.Height)
	})
	client.OnStridedBitmap(func(x, y, w, h int, _ []byte, _ int) {
		sess.streamer.markDirty(x, y, w, h)
	})
	// Cursor shapes arrive as pointer PDUs (never drawn into the framebuffer);
	// forward them so the frontend can render the remote cursor locally.
	client.OnPointer(func(u *rdp.PointerUpdate) {
		if s.emit != nil {
			s.emit(pointerEvent(id, u))
		}
	})
	client.OnResize(func(w, h int) {
		// Repaint everything at the new size: the canvas is cleared on resize,
		// so the first flush afterwards must be a full frame.
		sess.streamer.markDirty(0, 0, w, h)
		if err := client.RefreshRect(0, 0, w, h); err != nil {
			log.Error("rdp refresh after resize failed",
				zap.String("sessionID", id),
				zap.Int64("assetID", req.AssetID),
				zap.Int("width", w),
				zap.Int("height", h),
				zap.Error(err),
			)
		}
		if s.emit != nil {
			s.emit(Event{Type: "connected", SessionID: id, Width: w, Height: h})
		}
	})
	client.OnClipboardUpdate(func(hasText, _ bool) {
		if hasText {
			if err := client.RequestClipboard(); err != nil {
				log.Error("rdp clipboard request failed", zap.String("sessionID", id), zap.Error(err))
			}
		}
	})
	client.OnClipboardText(func(text string) {
		if s.emit != nil {
			s.emit(Event{Type: "clipboard", SessionID: id, Text: text})
		}
	})
	client.OnClipboardFilesAvailable(func() {
		if err := client.RequestClipboardFiles(); err != nil {
			log.Error("rdp clipboard file descriptor request failed", zap.String("sessionID", id), zap.Error(err))
		}
	})
	client.OnClipboardFileDescriptors(func(files []rdp.ClipboardFileDescriptor) {
		go s.receiveClipboardFiles(log, sess, files)
	})
	client.OnClipboardFileContents(func(streamID uint32, data []byte, err error) {
		sess.fileContents <- fileContentsResponse{streamID: streamID, data: data, err: err}
	})

	if err := client.Connect(); err != nil {
		s.remove(id)
		log.Error("rdp connect failed", zap.String("sessionID", id), zap.Error(err))
		if s.emit != nil {
			s.emit(Event{Type: "error", SessionID: id, Error: err.Error()})
		}
		return "", err
	}
	client.SetClipboardEnabled(cfg.Clipboard)

	log.Info("rdp connect end", zap.String("sessionID", id), zap.Int64("assetID", req.AssetID))
	_ = client.RefreshRect(0, 0, width, height)
	if s.emit != nil {
		s.emit(Event{Type: "connected", SessionID: id, Width: width, Height: height})
	}
	go s.frameLoop(sess)
	go func() {
		<-client.Done()
		time.Sleep(2 * time.Second)
		if client.State() == rdp.StateConnected {
			return
		}
		s.closeSession(sess)
	}()
	return id, nil
}

func (s *Service) TestConfig(ctx context.Context, cfg *asset_entity.RDPConfig, password string) error {
	cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
	width, height := resolveSize(cfg, 0, 0)
	log := logger.Ctx(ctx)
	log.Info("rdp test connection start",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("width", width),
		zap.Int("height", height),
		zap.Int64("sshTunnelID", cfg.SSHAssetID),
		zap.Bool("proxy", cfg.Proxy != nil),
	)

	opts := testClientOptions(cfg, password, width, height)
	opts.DialContext = connpool.RDPDialContext(cfg.SSHAssetID, cfg, s.pool)
	client, err := rdp.NewClient(opts)
	if err != nil {
		return fmt.Errorf("create RDP client: %w", err)
	}
	if err := client.Connect(); err != nil {
		log.Error("rdp test connection failed", zap.Error(err))
		return fmt.Errorf("connect RDP: %w", err)
	}
	_ = client.Close()

	log.Info("rdp test connection end", zap.String("host", cfg.Host), zap.Int("port", cfg.Port))
	return nil
}

func clientOptions(cfg *asset_entity.RDPConfig, password string, width, height int) *rdp.Options {
	opts := rdp.DefaultOptions()
	opts.Host = cfg.Host
	opts.Port = cfg.Port
	opts.Username = cfg.Username
	opts.Password = password
	opts.Domain = cfg.Domain
	opts.Width = uint16(width)
	opts.Height = uint16(height)
	opts.Depth = 32
	// Always negotiate the clipboard channel so users can toggle synchronization
	// for an active session. The configured initial state is applied after connect.
	opts.Clipboard = true
	opts.GFX = true
	opts.NoAVC = true
	opts.Wallpaper = true
	opts.HeartbeatTimeout = 0
	opts.Logger = slog.New(slog.DiscardHandler)
	return opts
}

func testClientOptions(cfg *asset_entity.RDPConfig, password string, width, height int) *rdp.Options {
	opts := clientOptions(cfg, password, width, height)
	opts.Clipboard = false
	opts.GFX = false
	opts.NoAVC = true
	opts.HeartbeatTimeout = 0
	return opts
}

func resolveSize(cfg *asset_entity.RDPConfig, reqW, reqH int) (int, int) {
	width, height := reqW, reqH
	if width <= 0 {
		width = cfg.Width
	}
	if height <= 0 {
		height = cfg.Height
	}
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}
	return width, height
}

func (s *Service) frameLoop(sess *session) {
	ticker := time.NewTicker(frameTickInterval)
	defer ticker.Stop()
	var nextAllowed time.Time
	// Nudge the server to repaint if nothing arrived shortly after connect —
	// some servers drop the initial RefreshRect issued during Connect.
	emitted := false
	refreshes := 0
	lastRefresh := time.Now()
	for {
		select {
		case <-sess.done:
			return
		case now := <-ticker.C:
			if !emitted && refreshes < 5 && now.Sub(lastRefresh) > time.Second {
				w, h := sess.client.FramebufferDims()
				_ = sess.client.RefreshRect(0, 0, w, h)
				refreshes++
				lastRefresh = now
			}
			if now.Before(nextAllowed) {
				continue
			}
			rect, ok := sess.streamer.takeDirty()
			if !ok {
				continue
			}
			ev, ok := sess.streamer.buildFrameEvent(sess.client, rect, sess.id)
			if !ok {
				// Framebuffer unavailable (e.g. mid-reconnect): put the rect
				// back so the region is retried once frames flow again.
				sess.streamer.markDirty(rect.x, rect.y, rect.w, rect.h)
				continue
			}
			if s.emit != nil {
				s.emit(ev)
			}
			emitted = true
			nextAllowed = now.Add(flushInterval(dirtyRect{x: ev.X, y: ev.Y, w: ev.RectWidth, h: ev.RectHeight}, ev.Width, ev.Height))
		}
	}
}

func (s *Service) SendInput(event InputEvent) error {
	sess, ok := s.get(event.SessionID)
	if !ok {
		return fmt.Errorf("RDP session not found: %s", event.SessionID)
	}
	switch event.Kind {
	case "mouse":
		return sess.client.SendMouse(event.X, event.Y, event.Buttons)
	case "wheel":
		return sess.client.SendMouseWheel(event.X, event.Y, event.Delta, false)
	case "key":
		return sess.client.SendKeyboardExtended(event.Scancode, event.Pressed, event.Extended)
	case "unicode":
		return sess.client.SendUnicode(event.Codepoint, event.Pressed)
	default:
		return fmt.Errorf("unsupported RDP input kind: %s", event.Kind)
	}
}

func (s *Service) Resize(sessionID string, width, height int) error {
	sess, ok := s.get(sessionID)
	if !ok {
		return fmt.Errorf("RDP session not found: %s", sessionID)
	}
	return sess.client.Resize(width, height)
}

func (s *Service) SetClipboard(sessionID, text string) error {
	sess, ok := s.get(sessionID)
	if !ok {
		return fmt.Errorf("RDP session not found: %s", sessionID)
	}
	return sess.client.SetClipboard(text)
}

func (s *Service) SetClipboardEnabled(sessionID string, enabled bool) error {
	sess, ok := s.get(sessionID)
	if !ok {
		return fmt.Errorf("RDP session not found: %s", sessionID)
	}
	sess.client.SetClipboardEnabled(enabled)
	return nil
}

func (s *Service) SetClipboardFilesFromLocal(sessionID string) (int, error) {
	sess, ok := s.get(sessionID)
	if !ok {
		return 0, fmt.Errorf("RDP session not found: %s", sessionID)
	}
	paths, err := readLocalClipboardFilePaths()
	if err != nil {
		return 0, err
	}
	if len(paths) == 0 {
		return 0, nil
	}
	files, err := clipboardFilesFromPaths(paths)
	if err != nil {
		return 0, err
	}
	if err := sess.client.SetClipboardFiles(files); err != nil {
		return 0, err
	}
	return len(files), nil
}

func (s *Service) Close(sessionID string) error {
	sess, ok := s.get(sessionID)
	if !ok {
		return nil
	}
	return sess.client.Close()
}

func (s *Service) closeSession(sess *session) {
	s.remove(sess.id)
	select {
	case <-sess.done:
	default:
		close(sess.done)
	}
	if s.emit != nil {
		s.emit(Event{Type: "closed", SessionID: sess.id})
	}
	if sess.clipboardTempDir != "" {
		_ = os.RemoveAll(sess.clipboardTempDir)
	}
}

func (s *Service) CloseAll() {
	s.mu.Lock()
	sessions := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		_ = sess.client.Close()
	}
}

func (s *Service) get(id string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

func (s *Service) remove(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}
