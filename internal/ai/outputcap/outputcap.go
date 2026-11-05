// Package outputcap bounds the size of exec results handed to the model.
package outputcap

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLines and MaxBytes mirror cago's default tool-output truncation limits.
	MaxLines = 2000
	MaxBytes = 50 * 1024

	// 头尾各占一半预算；noteReserve 留给省略说明，保证总长不超过 MaxBytes。
	noteReserve = 128
	halfBytes   = (MaxBytes - noteReserve) / 2
	halfLines   = MaxLines/2 - 1
)

// Cap 在 s 不超过 MaxBytes / MaxLines 时原样返回；否则保留头和尾，中间换成一行
// "[... N lines / X KiB omitted ...]"。切口优先落在行边界，且不会切开 UTF-8 字符。
func Cap(s string) string {
	if len(s) <= MaxBytes && lineCount(s) <= MaxLines {
		return s
	}
	head := headEnd(s)
	tail := tailStart(s, head)
	omitted := s[head:tail]
	return fmt.Sprintf("%s\n[... %d lines / %.1f KiB omitted ...]\n%s",
		s[:head], lineCount(omitted), float64(len(omitted))/1024, s[tail:])
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// headEnd 返回头部保留的字节数：至多 halfLines 行、halfBytes 字节。
func headEnd(s string) int {
	end, lines := 0, 0
	for lines < halfLines {
		i := strings.IndexByte(s[end:], '\n')
		if i < 0 || end+i+1 > halfBytes {
			break
		}
		end += i + 1
		lines++
	}
	if end > 0 {
		return end
	}
	// 第一行就超预算：按字节切，退到字符边界。
	end = min(halfBytes, len(s))
	for end > 0 && end < len(s) && !utf8.RuneStart(s[end]) {
		end--
	}
	return end
}

// tailStart 返回尾部起点（>= head）：至多 halfLines 行、halfBytes 字节。
func tailStart(s string, head int) int {
	start, lines := len(s), 0
	for lines < halfLines {
		// 取 start 之前的一行（含它前面的换行边界）。
		body := strings.TrimSuffix(s[:start], "\n")
		i := strings.LastIndexByte(body, '\n') + 1
		if i < head || len(s)-i > halfBytes {
			break
		}
		start = i
		lines++
	}
	if start < len(s) {
		return start
	}
	start = max(len(s)-halfBytes, head)
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return start
}
