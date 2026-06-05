package serial

import (
	"context"
	"testing"
)

type langStub struct{}

func (langStub) Lang() string { return "en" }

func TestSerialTesterBadJSON(t *testing.T) {
	s := &Serial{lang: langStub{}}
	if err := s.testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
