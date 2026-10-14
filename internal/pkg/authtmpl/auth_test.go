package authtmpl_test

import (
	"net/http"
	"testing"

	"github.com/opskat/opskat/internal/pkg/authtmpl"
)

func TestAuthType_HeaderApply(t *testing.T) {
	at, ok := authtmpl.AuthTypeFor("header")
	if !ok {
		t.Fatalf("header auth type not found")
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	if err := at.Apply(req, "X-Api-Key", []string{"secretvalue"}); err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if got := req.Header.Get("X-Api-Key"); got != "secretvalue" {
		t.Fatalf("header X-Api-Key = %q, want %q", got, "secretvalue")
	}
}

func TestAuthType_QueryApply(t *testing.T) {
	at, ok := authtmpl.AuthTypeFor("query")
	if !ok {
		t.Fatalf("query auth type not found")
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com/x?existing=1", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	if err := at.Apply(req, "token", []string{"abc"}); err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if got := req.URL.Query().Get("token"); got != "abc" {
		t.Fatalf("query token = %q, want %q", got, "abc")
	}
	if got := req.URL.Query().Get("existing"); got != "1" {
		t.Fatalf("existing query param dropped, got %q", got)
	}
}

func TestAuthType_BasicApply(t *testing.T) {
	at, ok := authtmpl.AuthTypeFor("basic")
	if !ok {
		t.Fatalf("basic auth type not found")
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	if err := at.Apply(req, "", []string{"alice", "s3cr3t"}); err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	user, pass, ok := req.BasicAuth()
	if !ok {
		t.Fatalf("BasicAuth() not set")
	}
	if user != "alice" || pass != "s3cr3t" {
		t.Fatalf("BasicAuth() = (%q, %q), want (alice, s3cr3t)", user, pass)
	}
}

func TestAuthType_WrongValueCount(t *testing.T) {
	at, _ := authtmpl.AuthTypeFor("basic")
	req, err := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	if err := at.Apply(req, "", []string{"onlyone"}); err == nil {
		t.Fatalf("expected error when value count does not match auth type requirement")
	}
}
