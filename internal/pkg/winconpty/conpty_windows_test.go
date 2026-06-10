//go:build windows

package winconpty

import (
	"strings"
	"testing"
	"time"
)

func TestConPtyReadsChildOutput(t *testing.T) {
	if !IsConPtyAvailable() {
		t.Skip("ConPTY is not available")
	}

	cpty, err := Start(`cmd.exe /d /q /c echo OPSKAT_CONPTY_SMOKE`)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = cpty.Close() })

	got := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		var out strings.Builder
		deadline := time.After(1500 * time.Millisecond)
		for {
			select {
			case <-deadline:
				got <- out.String()
				return
			default:
			}
			n, err := cpty.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			out.Write(buf[:n])
			if strings.Contains(out.String(), "OPSKAT_CONPTY_SMOKE") {
				got <- out.String()
				return
			}
		}
	}()

	select {
	case out := <-got:
		if !strings.Contains(out, "OPSKAT_CONPTY_SMOKE") {
			t.Fatalf("ConPTY output = %q, want marker", out)
		}
	case err := <-errCh:
		t.Fatalf("ConPTY Read() error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ConPTY child output")
	}
}
