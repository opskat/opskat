package opsctl

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/internal/service/extstore_svc"
	"github.com/opskat/opskat/pkg/extstore"
)

// fakeExtStore stands in for the desktop's extension store. events records the
// order of window activations and installs, so a test can see the window is raised
// before the install (and its confirm dialog) starts.
type fakeExtStore struct {
	listings   []extstore_svc.Listing
	listErr    error
	listLang   string
	installed  [2]string // name, version returned by InstallFromStore
	installErr error
	installs   []string
	installCtx context.Context
	events     *[]string
}

func (s *fakeExtStore) ListStore(_ context.Context, lang string) ([]extstore_svc.Listing, error) {
	s.listLang = lang
	return s.listings, s.listErr
}

func (s *fakeExtStore) InstallFromStore(ctx context.Context, name string) (string, string, error) {
	s.installs = append(s.installs, name)
	s.installCtx = ctx
	*s.events = append(*s.events, "install "+name)
	return s.installed[0], s.installed[1], s.installErr
}

type recordingWindow struct{ events *[]string }

func (w recordingWindow) ActivateWindow() { *w.events = append(*w.events, "activate") }

func newStoreOpsctl(store *fakeExtStore) (*Opsctl, *[]string) {
	events := &[]string{}
	store.events = events
	o := &Opsctl{
		ctx: context.Background(), appCtx: context.Background(),
		lang: extTestLang{}, window: recordingWindow{events: events}, extStore: store,
	}
	return o, events
}

func TestExtStoreSearch(t *testing.T) {
	store := &fakeExtStore{listings: []extstore_svc.Listing{
		{Card: extstore_svc.Card{Name: "es", DisplayName: "Elasticsearch", Description: "Browse indices",
			Action: extstore.ActionInstall}, Latest: "0.2.0"},
		{Card: extstore_svc.Card{Name: "kafka", DisplayName: "Kafka", InstalledVersion: "0.1.0",
			Action: extstore.ActionUpdate}, Latest: "0.4.1"},
		{Card: extstore_svc.Card{Name: "notebook", DisplayName: "Notebook", InstalledVersion: "1.0.0",
			Action: extstore.ActionInstalled}, Latest: "1.0.0"},
		{Card: extstore_svc.Card{Name: "pgdoctor", DisplayName: "PG Doctor", Action: extstore.ActionUnavailable,
			Unavailable: &extstore_svc.Unavailable{Reason: extstore_svc.ReasonHostABI, HostABI: "9.1"}}},
		{Card: extstore_svc.Card{Name: "newer", Action: extstore.ActionUnavailable,
			Unavailable: &extstore_svc.Unavailable{Reason: extstore_svc.ReasonMinAppVersion, MinAppVersion: "9.0.0"}}},
		{Card: extstore_svc.Card{Name: "future", Action: extstore.ActionUnavailable,
			Unavailable: &extstore_svc.Unavailable{Reason: extstore_svc.ReasonSource, SourceType: "ipfs"}}},
	}}
	o, events := newStoreOpsctl(store)

	resp := o.handleRequest(context.Background(), approval.ApprovalRequest{Type: approval.TypeExtStoreSearch, Lang: "zh-CN"})

	require.True(t, resp.Approved, resp.Reason)
	assert.Equal(t, "zh-CN", store.listLang, "display text follows the language opsctl asked for")
	assert.Empty(t, *events, "a read-only listing neither raises the window nor installs")
	require.Len(t, resp.StoreExtensions, 6)
	assert.Equal(t, approval.ExtStoreEntry{
		Name: "es", DisplayName: "Elasticsearch", Description: "Browse indices",
		Latest: "0.2.0", Status: approval.ExtStoreStatusInstall,
	}, resp.StoreExtensions[0])
	assert.Equal(t, approval.ExtStoreStatusUpdate, resp.StoreExtensions[1].Status)
	assert.Equal(t, "0.1.0", resp.StoreExtensions[1].Installed)
	assert.Equal(t, approval.ExtStoreStatusInstalled, resp.StoreExtensions[2].Status)
	for i, want := range []string{"9.1", "9.0.0", "ipfs"} {
		e := resp.StoreExtensions[3+i]
		assert.Equal(t, approval.ExtStoreStatusUnavailable, e.Status)
		assert.Contains(t, e.Reason, want, "the reason names what this OpsKat lacks")
		assert.Contains(t, e.Reason, "OpsKat", "and that updating OpsKat is the remedy")
	}
	assert.Contains(t, resp.StoreExtensions[3].Reason, "需要", "the reason is in the requested language")

	enResp := o.handleRequest(context.Background(), approval.ApprovalRequest{Type: approval.TypeExtStoreSearch, Lang: "en"})
	assert.NotContains(t, enResp.StoreExtensions[3].Reason, "需要")
}

func TestExtStoreSearchFailure(t *testing.T) {
	store := &fakeExtStore{listErr: &extstore_svc.RefreshError{Kind: extstore_svc.ErrorSignature, Err: errors.New("bad signature")}}
	o, _ := newStoreOpsctl(store)

	resp := o.handleRequest(context.Background(), approval.ApprovalRequest{Type: approval.TypeExtStoreSearch, Lang: "en"})

	assert.False(t, resp.Approved)
	assert.Contains(t, resp.Reason, "bad signature")
	assert.Empty(t, resp.StoreExtensions)
}

func TestExtStoreInstall(t *testing.T) {
	t.Run("raises the window, then installs through the store's confirm", func(t *testing.T) {
		store := &fakeExtStore{installed: [2]string{"es", "0.2.0"}}
		o, events := newStoreOpsctl(store)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		resp := o.handleRequest(ctx, approval.ApprovalRequest{Type: approval.TypeExtStoreInstall, Extension: "es"})

		require.True(t, resp.Approved, resp.Reason)
		assert.Equal(t, "es", resp.Extension)
		assert.Equal(t, "0.2.0", resp.Version)
		assert.Equal(t, []string{"activate", "install es"}, *events)
		// The confirm must close when opsctl goes away: the install runs on the
		// request's context, which ends when the client disconnects.
		cancel()
		assert.Error(t, store.installCtx.Err())
	})

	cases := []struct {
		name     string
		err      error
		kind     string
		contains string
	}{
		{"declined confirm", extension_svc.ErrInstallCanceled, approval.ExtStoreInstallCanceled, "declined"},
		{"already current", &extstore_svc.InstallError{Kind: extstore_svc.InstallErrInstalled,
			Err: errors.New(`extension "es" 0.9.0 is installed and the store offers nothing newer`)},
			approval.ExtStoreInstallUpToDate, "0.9.0"},
		{"digest mismatch", &extstore_svc.InstallError{Kind: extstore_svc.InstallErrDigest, Expected: "aaa", Actual: "bbb",
			Err: errors.New("digest mismatch: expected aaa, got bbb")}, string(extstore_svc.InstallErrDigest), "expected aaa, got bbb"},
		{"incompatible", &extstore_svc.InstallError{Kind: extstore_svc.InstallErrIncompatible,
			Err: errors.New("needs hostABI 9.1")}, string(extstore_svc.InstallErrIncompatible), "9.1"},
		{"not in the index", &extstore_svc.InstallError{Kind: extstore_svc.InstallErrNotFound,
			Err: errors.New(`extension "nope" is not in the store index`)}, string(extstore_svc.InstallErrNotFound), "nope"},
		{"network", &extstore_svc.InstallError{Kind: extstore_svc.InstallErrNetwork,
			Err: errors.New("dial tcp: connection refused")}, string(extstore_svc.InstallErrNetwork), "connection refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeExtStore{installErr: c.err}
			o, _ := newStoreOpsctl(store)

			resp := o.handleRequest(context.Background(), approval.ApprovalRequest{Type: approval.TypeExtStoreInstall, Extension: "es"})

			assert.False(t, resp.Approved)
			assert.Equal(t, c.kind, resp.ErrorKind)
			assert.Contains(t, resp.Reason, c.contains)
		})
	}

	t.Run("no extension named: refused before anything is shown", func(t *testing.T) {
		store := &fakeExtStore{}
		o, events := newStoreOpsctl(store)

		resp := o.handleRequest(context.Background(), approval.ApprovalRequest{Type: approval.TypeExtStoreInstall})

		assert.False(t, resp.Approved)
		assert.Empty(t, *events)
	})
}

func TestExtStoreNotInitialized(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	for _, typ := range []string{approval.TypeExtStoreSearch, approval.TypeExtStoreInstall} {
		resp := o.handleRequest(context.Background(), approval.ApprovalRequest{Type: typ, Extension: "es"})
		assert.False(t, resp.Approved, typ)
		assert.Contains(t, resp.Reason, "not initialized", typ)
	}
}
