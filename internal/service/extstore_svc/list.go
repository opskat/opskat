package extstore_svc

import (
	"context"

	"github.com/opskat/opskat/pkg/extstore"
)

// Listing is one extension as `opsctl ext search` lists it: its store card plus
// the newest version this app can install.
type Listing struct {
	Card
	// Latest is the newest version this app can install, whatever is installed
	// (it may be below an installed version); "" when no version runs here.
	Latest string `json:"latest"`
}

// List describes every extension of the last verified index in lang, refreshing
// first when no index has verified yet; that refresh's failure is the error.
// Unlike State, a later failed refresh does not hide the verified index — the
// same index installs rely on.
func (s *Service) List(ctx context.Context, lang string) ([]Listing, error) {
	if err := s.ensureIndex(ctx); err != nil {
		return nil, err
	}
	idx, _, _ := s.StoreIndex()
	app, installed := s.opts.App(), s.opts.InstalledVersions()
	out := make([]Listing, 0, len(idx.Extensions))
	for _, ext := range idx.Extensions {
		l := Listing{Card: card(ext, lang, app, installed[ext.Name])}
		if newest, _ := extstore.SelectInstallable(ext, app, ""); newest.Version != nil {
			l.Latest = newest.Version.Version
		}
		out = append(out, l)
	}
	return out, nil
}

// ensureIndex refreshes when no index has verified yet; the error is Refresh's
// *RefreshError.
func (s *Service) ensureIndex(ctx context.Context) error {
	if _, _, ok := s.StoreIndex(); ok {
		return nil
	}
	return s.Refresh(ctx)
}
