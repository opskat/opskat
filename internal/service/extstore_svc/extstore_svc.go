// Package extstore_svc is the app side of the official extension store: it
// fetches index.json and its signature through the user's download mirror,
// verifies the signature against the trusted keys, keeps the last verified index
// and describes it as store cards for the settings page and opsctl.
package extstore_svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/service/update_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

const (
	// maxIndexBytes bounds the index.json download.
	maxIndexBytes = 8 << 20
	// maxSignatureBytes bounds the index.json.sig download (base64 of 64 bytes
	// plus whitespace).
	maxSignatureBytes = 1 << 10
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         netdial.Default().DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
	},
}

// ErrorKind classifies why a refresh failed; each kind has its own store state.
type ErrorKind string

const (
	// ErrorFetch: the index or signature could not be downloaded (network,
	// mirror, HTTP status, size limit). Remedy: retry or change the mirror.
	ErrorFetch ErrorKind = "fetch"
	// ErrorSignature: the signature is missing, malformed, or from no trusted
	// key. The index is not shown. Remedy: retry or change the mirror.
	ErrorSignature ErrorKind = "signature"
	// ErrorFormat: a validly signed index in a format this build cannot read.
	// The store is not shown. Remedy: update OpsKat.
	ErrorFormat ErrorKind = "format"
	// ErrorInvalid: a validly signed index this build should read but cannot —
	// a publisher defect. Remedy: retry later.
	ErrorInvalid ErrorKind = "invalid"
)

// RefreshError is what Refresh returns on failure.
type RefreshError struct {
	Kind ErrorKind
	Err  error
}

func (e *RefreshError) Error() string {
	return fmt.Sprintf("refresh extension index (%s): %v", e.Kind, e.Err)
}
func (e *RefreshError) Unwrap() error { return e.Err }

// UnavailableReason says why no version of an extension can be installed.
type UnavailableReason string

const (
	// ReasonHostABI: the newest version needs a host ABI this app lacks.
	ReasonHostABI UnavailableReason = UnavailableReason(extension.ReasonHostABI)
	// ReasonMinAppVersion: the newest version needs a newer OpsKat release.
	ReasonMinAppVersion UnavailableReason = UnavailableReason(extension.ReasonMinAppVersion)
	// ReasonSource: the newest version lives in a source type this build
	// cannot fetch.
	ReasonSource UnavailableReason = "source"
)

// Options are the store's dependencies.
type Options struct {
	// DownloadMirror returns the app's download mirror prefix ("" = direct).
	DownloadMirror func() string
	// InstalledVersions returns the installed extensions, name → version.
	InstalledVersions func() map[string]string
	// App reports the running app, for the compatibility rule.
	App func() appversion.Info
}

// Service is the extension store.
type Service struct {
	opts Options

	mu sync.Mutex
	// index is the last verified index and when it was fetched; zero
	// fetchedAt means no refresh has succeeded yet.
	index     extstore.Index
	fetchedAt time.Time
	// lastErr is the latest refresh's failure; nil after a success.
	lastErr *RefreshError
}

// New creates the store. Nothing is fetched until Refresh.
func New(opts Options) *Service {
	return &Service{opts: opts}
}

// Refresh downloads index.json and index.json.sig through the download mirror,
// verifies the signature over the raw bytes, parses the index and caches it. On
// failure it returns a *RefreshError; the last verified index stays available
// through StoreIndex, but State shows the failure instead of cards.
func (s *Service) Refresh(ctx context.Context) error {
	mirror := s.opts.DownloadMirror()
	idxURL := update_svc.ApplyMirror(indexURL(), mirror)
	sigURL := update_svc.ApplyMirror(indexURL()+".sig", mirror)
	log := logger.Ctx(ctx).With(zap.String("indexURL", idxURL))
	log.Info("extension index refresh started")

	idx, err := fetchVerified(ctx, idxURL, sigURL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		log.Error("extension index refresh failed", zap.String("kind", string(err.Kind)), zap.Error(err.Err))
		s.lastErr = err
		return err
	}
	s.index, s.fetchedAt, s.lastErr = idx, time.Now(), nil
	log.Info("extension index refresh completed", zap.Int("extensions", len(idx.Extensions)))
	return nil
}

func fetchVerified(ctx context.Context, idxURL, sigURL string) (extstore.Index, *RefreshError) {
	raw, err := fetch(ctx, idxURL, maxIndexBytes)
	if err != nil {
		return extstore.Index{}, &RefreshError{Kind: ErrorFetch, Err: err}
	}
	sig, err := fetch(ctx, sigURL, maxSignatureBytes)
	if err != nil {
		return extstore.Index{}, &RefreshError{Kind: ErrorFetch, Err: err}
	}
	keys, err := trustedKeys()
	if err != nil {
		return extstore.Index{}, &RefreshError{Kind: ErrorSignature, Err: err}
	}
	if len(keys) == 0 {
		logger.Ctx(ctx).Warn("no trusted extension index key configured; every index fails verification")
	}
	if err := extstore.Verify(raw, sig, keys); err != nil {
		return extstore.Index{}, &RefreshError{Kind: ErrorSignature, Err: err}
	}
	idx, err := extstore.ParseIndex(raw)
	if err != nil {
		var fe *extstore.UnsupportedFormatError
		if errors.As(err, &fe) {
			return extstore.Index{}, &RefreshError{Kind: ErrorFormat, Err: err}
		}
		return extstore.Index{}, &RefreshError{Kind: ErrorInvalid, Err: err}
	}
	return idx, nil
}

// fetch GETs url and returns its body, failing past limit bytes.
func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GET %s: response exceeds %d bytes", url, limit)
	}
	return body, nil
}

// StoreIndex returns the last verified index and when it was fetched; ok is
// false until a refresh has succeeded. A later failed refresh does not clear it:
// the index was verified, so installs and update checks may still rely on it.
func (s *Service) StoreIndex() (idx extstore.Index, fetchedAt time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index, s.fetchedAt, !s.fetchedAt.IsZero()
}
