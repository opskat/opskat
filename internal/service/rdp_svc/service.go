package rdp_svc

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sync"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/bouncyball-git/gopher-rdp/display"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"go.uber.org/zap"
)

type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Data      string `json:"data,omitempty"` // full-frame top-down RGBA, base64
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
}

type Service struct {
	mu       sync.Mutex
	sessions map[string]*session
	emit     func(Event)
}

type session struct {
	id       string
	assetID  int64
	client   *rdp.Client
	done     chan struct{}
	frameMu  sync.RWMutex
	frame    []byte
	width    int
	height   int
	hasFrame bool
}

func New(emit func(Event)) *Service {
	return &Service{
		sessions: make(map[string]*session),
		emit:     emit,
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
	)

	opts := clientOptions(cfg, password, width, height)

	client, err := rdp.NewClient(opts)
	if err != nil {
		log.Error("rdp client create failed", zap.String("sessionID", id), zap.Error(err))
		return "", err
	}

	sess := &session{
		id:      id,
		assetID: req.AssetID,
		client:  client,
		done:    make(chan struct{}),
		width:   width,
		height:  height,
		frame:   make([]byte, width*height*4),
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
	client.OnResize(func(w, h int) {
		sess.resizeFrame(w, h)
		if s.emit != nil {
			s.emit(Event{Type: "connected", SessionID: id, Width: w, Height: h})
		}
	})
	client.OnStridedBitmap(func(x, y, w, h int, data []byte, stride int) {
		sess.writeStridedRect(x, y, w, h, data, stride)
	})
	client.OnBitmap(func(update *rdp.BitmapUpdate) {
		sess.writeBitmapUpdate(update)
	})

	if err := client.Connect(); err != nil {
		s.remove(id)
		log.Error("rdp connect failed", zap.String("sessionID", id), zap.Error(err))
		if s.emit != nil {
			s.emit(Event{Type: "error", SessionID: id, Error: err.Error()})
		}
		return "", err
	}

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
	width, height := resolveSize(cfg, 0, 0)
	log := logger.Ctx(ctx)
	log.Info("rdp test connection start",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("width", width),
		zap.Int("height", height),
	)

	client, err := rdp.NewClient(testClientOptions(cfg, password, width, height))
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
	opts.Clipboard = cfg.Clipboard
	opts.GFX = true
	opts.NoAVC = true
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
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-sess.done:
			return
		case <-ticker.C:
			pix, w, h := sess.snapshotFrame()
			if len(pix) == 0 {
				_ = sess.client.RefreshRect(0, 0, sess.width, sess.height)
				continue
			}
			if s.emit != nil {
				s.emit(Event{
					Type:      "frame",
					SessionID: sess.id,
					Width:     w,
					Height:    h,
					Data:      base64.StdEncoding.EncodeToString(pix),
				})
			}
		}
	}
}

func (sess *session) writeBitmapUpdate(update *rdp.BitmapUpdate) {
	if update == nil || update.Width <= 0 || update.Height <= 0 {
		return
	}
	needed := update.Width * update.Height * 4
	if needed <= 0 {
		return
	}
	rgba := make([]byte, needed)
	if update.BitsPerPixel == 32 && update.TopDown && len(update.Data) >= needed {
		copy(rgba, update.Data[:needed])
	} else {
		display.ConvertToRGBA(rgba, update.Data, update.Width, update.Height, update.BitsPerPixel, update.TopDown)
	}
	sess.writeRect(update.X, update.Y, update.Width, update.Height, rgba, update.Width*4)
}

func (sess *session) resizeFrame(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	sess.frameMu.Lock()
	defer sess.frameMu.Unlock()
	sess.width = width
	sess.height = height
	sess.frame = make([]byte, width*height*4)
	sess.hasFrame = false
}

func (sess *session) writeStridedRect(x, y, w, h int, data []byte, stride int) {
	if w <= 0 || h <= 0 || stride <= 0 || len(data) < stride*h {
		return
	}
	sess.writeRect(x, y, w, h, data, stride)
}

func (sess *session) writeRect(x, y, w, h int, data []byte, stride int) {
	sess.frameMu.Lock()
	defer sess.frameMu.Unlock()
	if sess.width <= 0 || sess.height <= 0 || len(sess.frame) < sess.width*sess.height*4 {
		return
	}
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > sess.width {
		w = sess.width - x
	}
	if y+h > sess.height {
		h = sess.height - y
	}
	if w <= 0 || h <= 0 {
		return
	}
	rowBytes := w * 4
	if len(data) < stride*(h-1)+rowBytes {
		return
	}
	dstStride := sess.width * 4
	for row := 0; row < h; row++ {
		srcOff := row * stride
		dstOff := (y+row)*dstStride + x*4
		copy(sess.frame[dstOff:dstOff+rowBytes], data[srcOff:srcOff+rowBytes])
	}
	sess.hasFrame = true
}

func (sess *session) snapshotFrame() ([]byte, int, int) {
	sess.frameMu.RLock()
	defer sess.frameMu.RUnlock()
	if !sess.hasFrame || sess.width <= 0 || sess.height <= 0 || len(sess.frame) == 0 {
		return nil, 0, 0
	}
	pix := make([]byte, len(sess.frame))
	copy(pix, sess.frame)
	return pix, sess.width, sess.height
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
		return sess.client.SendKeyboard(event.Scancode, event.Pressed)
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
