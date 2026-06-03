package localterm_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseWSLOutput(t *testing.T) {
	// 模拟 `wsl -l -q` 的 UTF-16LE 输出: "Ubuntu\r\nDebian\r\n"
	enc := func(s string) []byte {
		b := make([]byte, 0, len(s)*2)
		for _, r := range s {
			b = append(b, byte(r), 0x00) // UTF-16LE: 低字节在前(ASCII)
		}
		return b
	}
	raw := enc("Ubuntu\r\nDebian\r\n")
	assert.Equal(t, []string{"Ubuntu", "Debian"}, parseWSLOutput(raw))

	// 空输出 → nil/空
	assert.Empty(t, parseWSLOutput(enc("")))
	// 只有空白行 → 空
	assert.Empty(t, parseWSLOutput(enc("\r\n\r\n")))
}
