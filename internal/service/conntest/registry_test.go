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

func TestUnregister(t *testing.T) {
	Register("temp", func(context.Context, string, string) error { return nil })
	Unregister("temp")
	if _, ok := Lookup("temp"); ok {
		t.Fatal("temp should be gone after Unregister")
	}
}
