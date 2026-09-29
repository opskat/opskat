package conntest

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterAndLookup(t *testing.T) {
	defer Unregister("dummy")
	want := errors.New("boom")
	Register("dummy", func(_ context.Context, cfg, pw string) error {
		if cfg != "C" || pw != "P" {
			t.Fatalf("tester got cfg=%q pw=%q", cfg, pw)
		}
		return want
	})
	fn, ok := Lookup("dummy")
	if !ok {
		t.Fatal("expected dummy registered")
	}
	if got := fn(context.Background(), "C", "P"); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown type should not be found")
	}
}

func TestRegisterDetailedServesBothLookups(t *testing.T) {
	defer Unregister("detailed")
	RegisterDetailed("detailed", func(_ context.Context, cfg, _ string) (string, error) {
		return "HTTP 200 OK via " + cfg, nil
	})
	detailed, ok := LookupDetailed("detailed")
	if !ok {
		t.Fatal("expected detailed tester registered")
	}
	if got, err := detailed(context.Background(), "tunnel", ""); err != nil || got != "HTTP 200 OK via tunnel" {
		t.Fatalf("got %q, %v", got, err)
	}
	plain, ok := Lookup("detailed")
	if !ok {
		t.Fatal("a detailed tester must also serve the plain Lookup")
	}
	if err := plain(context.Background(), "tunnel", ""); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestLookupDetailedOnPlainTesterHasNoDetail(t *testing.T) {
	defer Unregister("plain")
	want := errors.New("boom")
	Register("plain", func(context.Context, string, string) error { return want })
	fn, ok := LookupDetailed("plain")
	if !ok {
		t.Fatal("a plain tester must serve LookupDetailed")
	}
	if detail, err := fn(context.Background(), "", ""); detail != "" || err != want {
		t.Fatalf("got %q, %v", detail, err)
	}
}

func TestUnregister(t *testing.T) {
	Register("temp", func(context.Context, string, string) error { return nil })
	Unregister("temp")
	if _, ok := Lookup("temp"); ok {
		t.Fatal("temp should be gone after Unregister")
	}
}
