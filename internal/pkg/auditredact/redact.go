// Package auditredact removes credential material before audit data is persisted.
package auditredact

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

const RedactedValue = "<redacted>"

var textRedactors = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(["']?(?:password|passphrase|token|api[-_]?key|private[-_]?key|secret[-_]?access[-_]?key)["']?\s*:\s*)(?:"[^"]*"|'[^']*'|[^\s,;&}]+)`), `${1}` + `"` + RedactedValue + `"`},
	{regexp.MustCompile(`(?i)(--(?:password|passphrase|token|api[-_]?key|private[-_]?key|secret[-_]?access[-_]?key)(?:=|\s+))(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)((?:password|passphrase|token|api[-_]?key|private[-_]?key|secret[-_]?access[-_]?key)\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s'"]+`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)(identified\s+by\s+)(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^:/@\s]+:)[^@\s]+(@)`), `${1}` + RedactedValue + `${2}`},
	{regexp.MustCompile(`(?is)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`), RedactedValue},
	{regexp.MustCompile(`(?i)((?:x-amz-signature|x-amz-credential|x-amz-security-token|signature|awsaccesskeyid)=)([^&\s]+)`), `${1}` + RedactedValue},
}

// JSON recursively redacts credential-bearing fields. Invalid JSON fails closed.
func JSON(raw string) string {
	if raw == "" {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return RedactedValue
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(redactValue(value)); err != nil {
		return RedactedValue
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// Result preserves ordinary command/query output while recursively redacting JSON.
func Result(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return Text(raw)
	}
	return JSON(raw)
}

// Text redacts recognizable credential forms in opaque audit text.
func Text(text string) string {
	for _, redactor := range textRedactors {
		text = redactor.pattern.ReplaceAllString(text, redactor.replacement)
	}
	return text
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveKey(key) {
				redacted[key] = RedactedValue
				continue
			}
			redacted[key] = redactValue(item)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactValue(item)
		}
		return redacted
	case string:
		return Text(typed)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	canonical := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	if canonical == "authorization" || canonical == "proxyauthorization" ||
		canonical == "cookie" || canonical == "setcookie" || canonical == "kubeconfig" {
		return true
	}
	for _, suffix := range []string{"password", "passphrase", "token", "secret", "privatekey", "privatekeys", "clientkey", "apikey"} {
		if strings.HasSuffix(canonical, suffix) {
			return true
		}
	}
	return strings.Contains(canonical, "secret") && strings.HasSuffix(canonical, "key")
}
