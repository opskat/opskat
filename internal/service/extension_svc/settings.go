package extension_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/opskat/opskat/pkg/extension"
)

const settingsKVKey = "ext.settings"

// ExtensionSettings 是保存后的扩展设置。密码字段以密文存放。
type ExtensionSettings struct {
	Values map[string]string `json:"values"`
}

// SaveExtensionSettings 按 describe() 的 settingsSchema 校验并保存，密码字段用 encrypt 加密后入库。
// 设置表单读回时密钥只显示"已设置"，所以提交的密码字段为空表示不修改，保留已存的密文。
func (s *Service) SaveExtensionSettings(ctx context.Context, name string, values map[string]string, encrypt func(string) (string, error)) error {
	ext := s.manager.GetExtension(name)
	if ext == nil || ext.Manifest.SettingsSchema == nil {
		return fmt.Errorf("extension %q has no settings schema", name)
	}
	existing, _, err := s.loadSettings(ctx, name)
	if err != nil {
		return err
	}
	secrets := map[string]struct{}{}
	for _, f := range extension.PasswordFieldsFromSchema(ext.Manifest.SettingsSchema) {
		secrets[f] = struct{}{}
	}
	// stored 是保存后的全部设置：密钥字段留空或没带时沿用已存的密文，没存过就不出现，
	// 必填检查按它做——留空不改的密钥算已填，从没填过的必填密钥照样报错。
	stored := make(map[string]string, len(values))
	for k, v := range values {
		if _, secret := secrets[k]; !secret {
			stored[k] = v
			continue
		}
		if v == "" {
			continue
		}
		enc, err := encrypt(v)
		if err != nil {
			return fmt.Errorf("encrypt settings field %q: %w", k, err)
		}
		stored[k] = enc
	}
	for k := range secrets {
		if _, set := stored[k]; !set && existing[k] != "" {
			stored[k] = existing[k]
		}
	}
	raw := make(map[string]any, len(stored))
	for k, v := range stored {
		raw[k] = v
	}
	if err := extension.ValidateSettings(ext.Manifest.SettingsSchema, raw); err != nil {
		return err
	}
	payload, err := json.Marshal(ExtensionSettings{Values: stored})
	if err != nil {
		return err
	}
	return s.dataRepo.Set(ctx, name, settingsKVKey, payload)
}

// PublicSettings 是 settings_get 给扩展的设置：非密钥字段给值；已设置的密钥字段只给它的占位
// {{setting.<field>}}（扩展拿它写请求头，宿主发出前替换），没设置的不出现，扩展据此知道还没配置。
func (s *Service) PublicSettings(ctx context.Context, name string) (map[string]string, error) {
	stored, schema, err := s.loadSettings(ctx, name)
	if err != nil || stored == nil {
		return map[string]string{}, err
	}
	secrets := map[string]struct{}{}
	for _, f := range extension.PasswordFieldsFromSchema(schema) {
		secrets[f] = struct{}{}
	}
	out := map[string]string{}
	for k, v := range stored {
		if _, secret := secrets[k]; secret {
			if v != "" {
				out[k] = extension.SettingPlaceholder(k)
			}
			continue
		}
		out[k] = v
	}
	return out, nil
}

// SettingSecrets 用 decrypt 解密指定密钥字段。
func (s *Service) SettingSecrets(ctx context.Context, name string, fields []string, decrypt func(string) (string, error)) (map[string]string, error) {
	stored, _, err := s.loadSettings(ctx, name)
	if err != nil || stored == nil {
		return map[string]string{}, err
	}
	out := map[string]string{}
	for _, f := range fields {
		v, ok := stored[f]
		if !ok || v == "" {
			continue
		}
		plain, err := decrypt(v)
		if err != nil {
			return nil, fmt.Errorf("decrypt settings field %q: %w", f, err)
		}
		out[f] = plain
	}
	return out, nil
}

// SettingsHash 是全部已存设置（含密文）的哈希，供审核缓存键使用。
func (s *Service) SettingsHash(ctx context.Context, name string) (string, error) {
	stored, _, err := s.loadSettings(ctx, name)
	if err != nil || len(stored) == 0 {
		return "", err
	}
	keys := make([]string, 0, len(stored))
	for k := range stored {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(stored[k]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) loadSettings(ctx context.Context, name string) (map[string]string, map[string]any, error) {
	ext := s.manager.GetExtension(name)
	var schema map[string]any
	if ext != nil {
		schema = ext.Manifest.SettingsSchema
	}
	if s.dataRepo == nil {
		return nil, schema, nil
	}
	payload, err := s.GetHostKV(ctx, name, settingsKVKey)
	if err != nil || len(payload) == 0 {
		return nil, schema, err
	}
	var stored ExtensionSettings
	if err := json.Unmarshal(payload, &stored); err != nil {
		return nil, schema, fmt.Errorf("parse extension settings: %w", err)
	}
	return stored.Values, schema, nil
}

// ExtensionSettingsView 是设置表单读到的扩展设置：密钥字段不回值，只在 SecretsSet 里报告已设置。
type ExtensionSettingsView struct {
	Schema     map[string]any    `json:"schema"`
	Values     map[string]string `json:"values"`
	SecretsSet []string          `json:"secretsSet"`
}

// SettingsView 返回 name 的设置表单：按 lang 本地化的 schema、非密钥字段的值和已设置的密钥字段。
func (s *Service) SettingsView(ctx context.Context, name, lang string) (*ExtensionSettingsView, error) {
	ext := s.manager.GetExtension(name)
	if ext == nil || ext.Manifest.SettingsSchema == nil {
		return nil, fmt.Errorf("extension %q has no settings schema", name)
	}
	stored, _, err := s.loadSettings(ctx, name)
	if err != nil {
		return nil, err
	}
	localized := ext.Manifest.Localized(func(key string) string { return ext.Translate(lang, key) })
	view := &ExtensionSettingsView{Schema: localized.SettingsSchema, Values: map[string]string{}, SecretsSet: []string{}}
	secrets := map[string]struct{}{}
	for _, f := range extension.PasswordFieldsFromSchema(ext.Manifest.SettingsSchema) {
		secrets[f] = struct{}{}
		if stored[f] != "" {
			view.SecretsSet = append(view.SecretsSet, f)
		}
	}
	for k, v := range stored {
		if _, secret := secrets[k]; !secret {
			view.Values[k] = v
		}
	}
	return view, nil
}
