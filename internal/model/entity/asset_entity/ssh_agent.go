// SSH Agent 资产契约：Agent 模式（auth_type=agent）的 SSH 资产在保存时必须携带
// 正数的 agent_source_id 与规范精确的 agent_key_fingerprint，且与密码/私钥材料互斥。
// 本文件承载三部分：
//   - 规范 SHA256 指纹校验（ValidateFingerprintSHA256）；
//   - 实体层权威结构校验（validateSSHAgentContract，经 Asset.Validate 对桌面/AI/导入
//     全部写入路径生效）；
//   - SSH Agent 字段的 JSON 写入边界检查（CheckSSHConfigAgentWriteBoundary，拒绝会
//     导致 Go 与 SQLite 对 auth_type / agent_source_id 解释不一致的重复 key 与非规范
//     拼写）。这只是 SSH Agent 的局部完整性要求，不重构全仓资产解码器。
package asset_entity

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// SSH Agent 字段写入边界的稳定错误 token。消费方（前端/AI）据此识别"原始写入对
// Agent 判别字段或来源字段制造了顶层歧义"，而不是把它当作普通格式错误重试。
const CodeAgentConfigAmbiguous = "ssh_config_agent_write_ambiguous"

// Error 是 SSH 资产契约层的类型化错误，携带稳定错误码。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newAgentConfigError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// AgentConfigCodeOf 返回 err 的稳定错误码（若属于 SSH 资产契约层错误）。
func AgentConfigCodeOf(err error) (string, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}

// ValidateFingerprintSHA256 校验一个 SSH 公钥指纹是否为规范、精确的 SHA256 形式。
// 要求（与 x/crypto/ssh 的 FingerprintSHA256 输出一致）：
//   - 前缀必须精确为大写 "SHA256:"；
//   - 无首尾空白；
//   - base64 无填充；
//   - 解码后的摘要必须恰好为 32 字节；
//   - 重新编码结果必须与输入完全相同。
func ValidateFingerprintSHA256(fingerprint string) error {
	if fingerprint == "" {
		return errors.New("SSH 公钥指纹不能为空")
	}
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		return errors.New("SSH 公钥指纹必须以大写 SHA256: 前缀开头")
	}
	if fingerprint != strings.TrimSpace(fingerprint) {
		return errors.New("SSH 公钥指纹不允许首尾空白")
	}
	encoded := strings.TrimPrefix(fingerprint, "SHA256:")
	if encoded == "" {
		return errors.New("SSH 公钥指纹缺少 base64 摘要")
	}
	if strings.Contains(encoded, "=") {
		return errors.New("SSH 公钥指纹 base64 不允许填充")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("SSH 公钥指纹 base64 解码失败: %w", err)
	}
	if len(raw) != 32 {
		return errors.New("SSH 公钥指纹解码摘要必须恰好为 32 字节")
	}
	if reencoded := base64.RawStdEncoding.EncodeToString(raw); reencoded != encoded {
		return errors.New("SSH 公钥指纹重编码结果与输入不一致")
	}
	return nil
}

// validateSSHAgentContract 校验 SSH Agent 认证契约。这是与调用方是否绕过前端无关的
// 权威边界（经 Asset.Validate 对所有写入路径生效）：
//   - auth_type=agent 时必须有正数的 agent_source_id 与规范指纹；
//   - Agent 字段与托管凭据 ID/密码/私钥/私钥口令互斥，任一带即拒绝；
//   - 非 Agent 认证方式不得携带 Agent 来源字段（切换离开应删除，残留即拒绝）。
//
// 来源存在性（引用完整性）依赖数据库，不在实体层校验，由服务/写入边界负责。
func validateSSHAgentContract(cfg *SSHConfig) error {
	if cfg.AuthType == AuthTypeAgent {
		if cfg.AgentSourceID <= 0 {
			return errors.New("SSH Agent 认证必须指定有效的来源 ID")
		}
		if err := ValidateFingerprintSHA256(cfg.AgentKeyFingerprint); err != nil {
			return fmt.Errorf("SSH Agent 认证指纹无效: %w", err)
		}
		if cfg.CredentialID != 0 || cfg.Password != "" || len(cfg.PrivateKeys) > 0 || cfg.PrivateKeyPassphrase != "" {
			return errors.New("SSH Agent 认证与托管凭据/密码/私钥/口令互斥")
		}
		return nil
	}
	if cfg.AgentSourceID != 0 || cfg.AgentKeyFingerprint != "" {
		return errors.New("非 Agent 认证方式不得携带 Agent 来源字段")
	}
	return nil
}

// agentCanonicalKeys 把规范化后的 key 映射回规范拼写。规范化 = 小写并去掉所有非
// 字母数字字符，因此 auth_type / authType / AUTH_TYPE / auth-type 都归一为 authtype。
// Go 的 encoding/json 对字段 tag 大小写不敏感地读取这些变体，而 SQLite 的
// json_extract 大小写敏感——同一份写入两者可能解释出不同的 auth_type /
// agent_source_id，正是写入边界要拒绝的歧义。
var agentCanonicalKeys = map[string]string{
	"authtype":            "auth_type",
	"agentsourceid":       "agent_source_id",
	"agentkeyfingerprint": "agent_key_fingerprint",
}

func normalizeConfigKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func agentCanonicalKey(norm string) (string, bool) {
	canon, ok := agentCanonicalKeys[norm]
	return canon, ok
}

// skipJSONValue 消费解码器里的一个完整 JSON 值（标量一个 token，对象/数组到闭合）。
func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return nil
	}
	if delim != '{' && delim != '[' {
		return nil
	}
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// CheckSSHConfigAgentWriteBoundary 检查一段将要写入 assets.config 的原始 SSH 配置
// JSON，拒绝会让 Go 与 SQLite 对 auth_type / agent_source_id / agent_key_fingerprint
// 解释不一致的顶层写入：
//   - 重复的顶层 key；
//   - 非规范拼写（Go 大小写不敏感读取、SQLite 大小写敏感，或两者都读不到的变体）。
//
// 仅针对 SSH Agent 字段，不检查其它字段。非对象 JSON、非法 JSON 交给后续常规解析
// 报错，这里只做歧义拒绝。通过后调用方保存的是规范表示。
func CheckSSHConfigAgentWriteBoundary(raw string) error {
	if raw == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil //nolint:nilerr // 非法 JSON 由常规解析报出更准确的错误，边界只拒歧义
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil //nolint:nilerr // 解析中途失败：边界放行，常规解析负责报错
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil
		}
		if err := skipJSONValue(dec); err != nil {
			return nil //nolint:nilerr // 值解析失败：边界放行，常规解析负责报错
		}
		norm := normalizeConfigKey(key)
		canon, isAgent := agentCanonicalKey(norm)
		if !isAgent {
			continue
		}
		if key != canon {
			return newAgentConfigError(CodeAgentConfigAmbiguous,
				fmt.Sprintf("SSH Agent 字段 %q 使用了非规范拼写，应为 %q", key, canon))
		}
		if seen[norm] {
			return newAgentConfigError(CodeAgentConfigAmbiguous,
				fmt.Sprintf("SSH 配置 JSON 顶层出现重复 key %q", key))
		}
		seen[norm] = true
	}
	return nil
}
