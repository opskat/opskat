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
	fn, ok := LookupDetailed("dummy")
	if !ok {
		t.Fatal("expected dummy registered")
	}
	if detail, err := fn(context.Background(), "C", "P"); detail != "" || err != want {
		t.Fatalf("got %q, %v; want no detail and %v", detail, err, want)
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := LookupDetailed("nope"); ok {
		t.Fatal("unknown type should not be found")
	}
}

func TestUnregister(t *testing.T) {
	Register("temp", func(context.Context, string, string) error { return nil })
	Unregister("temp")
	if _, ok := LookupDetailed("temp"); ok {
		t.Fatal("temp should be gone after Unregister")
	}
}
