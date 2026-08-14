// Package auditredact removes credential material before audit data is persisted.
package auditredact

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode"
)

const RedactedValue = "<redacted>"

var textRedactors = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// Authorization / Proxy-Authorization values are opaque credentials. Redact the
	// complete value, not just the first token: parameterized schemes such as Digest
	// otherwise leak username/realm/nonce/response after the scheme name.
	{regexp.MustCompile(`(?i)((?:proxy[-_]?authorization|authorization)["']?\s*:\s*")(?:\\.|[^"\\\r\n])*(")`), `${1}` + RedactedValue + `${2}`},
	{regexp.MustCompile(`(?i)((?:proxy[-_]?authorization|authorization)["']?\s*:\s*')(?:\\.|[^'\\\r\n])*(')`), `${1}` + RedactedValue + `${2}`},
	{regexp.MustCompile(`(?i)((?:proxy[-_]?authorization|authorization)["']?\s*:\s*\[\s*)[^\]]*(\])`), `${1}` + RedactedValue + `${2}`},
	// Digest/Signature carry comma-separated credential parameters, so their full
	// header remainder is sensitive. Token schemes (Basic/Bearer/etc.) redact one token
	// and preserve unrelated diagnostics that may follow on the same opaque text line.
	{regexp.MustCompile(`(?im)((?:proxy[-_]?authorization|authorization)\s*:\s*(?:digest|signature)\s+)[^\r\n]+`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)((?:proxy[-_]?authorization|authorization)["']?\s*:\s*(?:\[\s*)?["']?[a-z][a-z0-9+._~-]*\s+)[^\s,'";\]]+`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?im)((?:proxy[-_]?authorization|authorization)\s*:\s*)[^\s\r\n]+\s*$`), `${1}` + RedactedValue},
	// Cookie values may contain multiple semicolon-separated credentials; fail closed for
	// the whole header line instead of redacting only the first pair. Provider errors also
	// commonly render headers as quoted JSON fragments rather than real HTTP lines.
	{regexp.MustCompile(`(?i)((?:set[-_]?cookie|cookie)["']?\s*:\s*")(?:\\.|[^"\\\r\n])*(")`), `${1}` + RedactedValue + `${2}`},
	{regexp.MustCompile(`(?i)((?:set[-_]?cookie|cookie)["']?\s*:\s*')(?:\\.|[^'\\\r\n])*(')`), `${1}` + RedactedValue + `${2}`},
	{regexp.MustCompile(`(?i)((?:set[-_]?cookie|cookie)["']?\s*:\s*\[\s*)[^\]]*(\])`), `${1}` + RedactedValue + `${2}`},
	{regexp.MustCompile(`(?im)((?:^|[\s,;])(?:set[-_]?cookie|cookie)\s*:\s*)[^\r\n]+`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)(["']?(?:[a-z0-9_-]*(?:password|passphrase|token|secret)|api[-_]?key|client[-_]?key(?:[-_]?data)?|private[-_]?key|secret[-_]?access[-_]?key|kubeconfig)["']?\s*:\s*)(?:"[^"]*"|'[^']*'|[^\s,;&}]+)`), `${1}` + `"` + RedactedValue + `"`},
	{regexp.MustCompile(`(?i)(--(?:[a-z0-9_-]*(?:password|passphrase|token|secret)|api[-_]?key|client[-_]?key(?:[-_]?data)?|private[-_]?key|secret[-_]?access[-_]?key|kubeconfig|cookie|set[-_]?cookie)(?:=|\s+))(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)((?:[a-z0-9_-]*(?:password|passphrase|token|secret)|api[-_]?key|client[-_]?key(?:[-_]?data)?|private[-_]?key|secret[-_]?access[-_]?key|kubeconfig|cookie|set[-_]?cookie)\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)(identified\s+by\s+)(?:"[^"]*"|'[^']*'|[^\s,;&]+)`), `${1}` + RedactedValue},
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^:/@\s]+:)[^@\s]+(@)`), `${1}` + RedactedValue + `${2}`},
	{regexp.MustCompile(`(?is)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`), RedactedValue},
	// Truncated provider/command errors may omit the END marker. Once a private-key
	// header appears, the remaining opaque text is key material and must fail closed.
	{regexp.MustCompile(`(?is)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*\z`), RedactedValue},
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
	value, ok := decodeSingleJSONValue(raw)
	if !ok {
		return RedactedValue
	}
	return encodeRedactedJSON(value)
}

// Result preserves ordinary command/query output while recursively redacting JSON.
// A leading "[" is not sufficient to classify text as JSON: log lines such as
// "[INFO] ..." are ordinary output. Payloads that otherwise look JSON-shaped still
// fail closed when malformed, rather than falling back to a regex-only projection.
func Result(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return Text(raw)
	}
	value, ok := decodeSingleJSONValue(raw)
	if !ok {
		if trimmed[0] == '[' && !couldStartJSONArray(trimmed) {
			return Text(raw)
		}
		return RedactedValue
	}
	return encodeRedactedJSON(value)
}

func couldStartJSONArray(trimmed string) bool {
	rest := strings.TrimLeftFunc(trimmed[1:], unicode.IsSpace)
	if rest == "" {
		return true
	}
	switch rest[0] {
	case ']', '{', '[', '"', 't', 'f', 'n':
		return true
	case '-':
		return len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9'
	default:
		return rest[0] >= '0' && rest[0] <= '9'
	}
}

func decodeSingleJSONValue(raw string) (any, bool) {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	return value, true
}

func encodeRedactedJSON(value any) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(redactValue(value)); err != nil {
		return RedactedValue
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// Text redacts recognizable credential forms in opaque audit text.
func Text(text string) string {
	for _, redactor := range textRedactors {
		text = redactor.pattern.ReplaceAllString(text, redactor.replacement)
	}
	return text
}

func redactValue(value any) any {
	return redactValueInContext(value, false)
}

func redactValueInContext(value any, agentSource bool) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		mapIsAgentSource := agentSource || hasAgentEndpointType(typed)
		for key, item := range typed {
			canonical := canonicalKey(key)
			if isSensitiveKey(key) || (mapIsAgentSource && isAgentEndpointValueKey(canonical)) {
				redacted[key] = RedactedValue
				continue
			}
			redacted[key] = redactValueInContext(item, isAgentSourceContainerKey(canonical))
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactValueInContext(item, agentSource)
		}
		return redacted
	case string:
		return Text(typed)
	default:
		return value
	}
}

func hasAgentEndpointType(value map[string]any) bool {
	for key, item := range value {
		canonical := canonicalKey(key)
		if canonical != "endpointtype" {
			continue
		}
		typeName, ok := item.(string)
		if !ok {
			continue
		}
		switch canonicalKey(typeName) {
		case "environment", "unixsocket", "windowsnamedpipe":
			return true
		}
	}
	return false
}

func isAgentSourceContainerKey(canonical string) bool {
	return canonical == "agentsource" || canonical == "sshagentsource"
}

func isAgentEndpointValueKey(canonical string) bool {
	switch canonical {
	case "endpoint", "value", "path", "socket", "pipe", "namedpipe":
		return true
	default:
		return false
	}
}

func canonicalKey(key string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
}

func isSensitiveKey(key string) bool {
	canonical := canonicalKey(key)
	if canonical == "authorization" || canonical == "proxyauthorization" ||
		canonical == "cookie" || canonical == "setcookie" || canonical == "kubeconfig" ||
		canonical == "clientkeydata" || canonical == "xamzcredential" || canonical == "awsaccesskeyid" {
		return true
	}
	for _, suffix := range []string{
		"password", "passwords", "passphrase", "passphrases", "token", "tokens", "secret", "secrets",
		"privatekey", "privatekeys", "clientkey", "clientkeys", "apikey", "apikeys", "kubeconfigs",
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
	return strings.Contains(canonical, "secret") &&
		(strings.HasSuffix(canonical, "key") || strings.HasSuffix(canonical, "keys"))
}
