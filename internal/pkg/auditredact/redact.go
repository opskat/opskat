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
	// signature / signed value / challenge 材料（JSON 冒号形式）。
	// 仅在键名精确命中 signature/signed-value/challenge 语义时遮蔽，不碰普通 endpoint/path。
	{regexp.MustCompile(`(?i)(["']?(?:signature|signature[-_]?value|signed[-_]?value|challenge|challenge[-_]?response|challenge[-_]?answer)["']?\s*:\s*)(?:"[^"]*"|'[^']*'|[^\s,;&}]+)`), `${1}` + `"` + RedactedValue + `"`},
	// --signature / --challenge / --agent-endpoint 等 flag 形式（SSH Agent 来源路径同属秘密）。
	{regexp.MustCompile(`(?i)(--(?:signature|signature[-_]?value|signed[-_]?value|challenge|challenge[-_]?response|challenge[-_]?answer|agent[-_]?endpoint|agent[-_]?socket|agent[-_]?pipe|agent[-_]?named[-_]?pipe|ssh[-_]?agent[-_]?endpoint)(?:=|\s+))(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
	// 裸 key=value / key: value 与 SSH_AUTH_SOCK 环境变量形式。
	{regexp.MustCompile(`(?i)((?:signature|signature[-_]?value|signed[-_]?value|challenge|challenge[-_]?response|challenge[-_]?answer|agent[-_]?endpoint|agent[-_]?socket|agent[-_]?pipe|agent[-_]?named[-_]?pipe|ssh[-_]?agent[-_]?endpoint|ssh[-_]?auth[-_]?sock)\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
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
	for _, suffix := range []string{"password", "passphrase", "token", "secret", "privatekey", "privatekeys", "clientkey", "apikey",
		// signature / signed value / challenge 材料（challenge_id 等 correlation ID 以 id 结尾，不受影响）
		"signature", "signaturevalue", "signedvalue", "challenge", "challengeresponse", "challengeanswer"} {
		if strings.HasSuffix(canonical, suffix) {
			return true
		}
	}
	// SSH Agent endpoint 来源字段：值指向本地 agent socket/pipe/路径，属秘密。
	// 只遮蔽 agent 来源语义，不碰普通 endpoint/endpoint_type/path/agent_source_id/指纹。
	for _, prefix := range []string{"agentendpoint", "sshagentendpoint", "agentsocket", "agentpipe", "agentnamedpipe",
		"agentendpointpath", "agentpath", "sshauthsock"} {
		if strings.HasPrefix(canonical, prefix) {
			return true
		}
	}
	return strings.Contains(canonical, "secret") && strings.HasSuffix(canonical, "key")
}
