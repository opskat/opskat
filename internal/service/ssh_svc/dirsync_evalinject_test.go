package ssh_svc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// simulateAutowrap reproduces how a terminal echoes a line longer than the
// screen width: at each right margin it writes the char, then emits a bare CR
// and re-emits that same char at the start of the next row (<char>\r<char>).
// This is exactly the artifact captured from a live host (192.168.8.141) that
// broke the naive byte matcher.
func simulateAutowrap(s string, width int) string {
	var b strings.Builder
	col := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' || c == '\n' {
			b.WriteByte(c)
			col = 0
			continue
		}
		b.WriteByte(c)
		col++
		if col == width && i+1 < len(s) && s[i+1] != '\r' && s[i+1] != '\n' {
			b.WriteByte('\r')
			b.WriteByte(c) // terminal re-emits the wrapped char
			col = 1
		}
	}
	return b.String()
}

// TestInjectionWrapperEchoSuppressedAcrossChunks guards Bug 1 ("entering the
// terminal inserts several newlines / shows the injection command"). The read
// injection wrapper is echoed and wraps at the terminal width; the byte matcher
// must hide it whatever the width and however the network splits the echo.
func TestInjectionWrapperEchoSuppressedAcrossChunks(t *testing.T) {
	wrapper, _ := buildEnableSyncInjection(shellTypeBash, "real-token", "nonce-1")
	base := strings.TrimSuffix(wrapper, "\r")

	// widths that force wraps at different offsets, incl. a chunk boundary landing
	// on a wrap CR (via chunk size 1).
	for _, width := range []int{40, 63, 80, 118} {
		echo := simulateAutowrap(base, width) + "\r\n"
		for _, chunk := range []int{1, 5, 40, len(echo)} {
			sess := newTestSyncSession()
			sess.queueInternalEchoSuppression([]byte(wrapper))
			var out []byte
			for i := 0; i < len(echo); i += chunk {
				end := i + chunk
				if end > len(echo) {
					end = len(echo)
				}
				out = append(out, sess.filterOutput([]byte(echo[i:end]))...)
			}
			got := string(out)
			assert.NotContains(t, got, "stty", "width=%d chunk=%d: wrapper leaked", width, chunk)
			assert.NotContains(t, got, "read -r", "width=%d chunk=%d: wrapper leaked", width, chunk)
			assert.NotContains(t, got, "base64", "width=%d chunk=%d: wrapper leaked", width, chunk)
			// only the trailing Enter newline may remain (the single clean refresh)
			assert.Equal(t, "\r\n", got, "width=%d chunk=%d", width, chunk)
		}
	}
}
