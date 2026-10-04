package system

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/pkg/extension"
)

// The frontend syncs its UI language through SetLanguage in i18next's spelling
// (frontend/src/i18n/index.ts: "zh-CN" / "en"). Everything the backend localizes
// with Lang() must still resolve Chinese for a Chinese UI — here an extension's own
// strings (asset type name, config field titles), read from its locales/zh-CN.json.
func TestSetLanguageFromTheUISelectsAnExtensionsLocale(t *testing.T) {
	dir := t.TempDir()
	localesDir := filepath.Join(dir, "locales")
	if err := os.MkdirAll(localesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{
		"zh-CN.json": `{"assetType.notebook.name":"笔记本"}`,
		"en.json":    `{"assetType.notebook.name":"Notebook"}`,
	} {
		if err := os.WriteFile(filepath.Join(localesDir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	locales, err := extension.LoadLocales(dir)
	if err != nil {
		t.Fatal(err)
	}
	ext := &extension.Extension{Locales: locales}

	s := New(t.Context(), SkillContent{})
	for uiLang, want := range map[string]string{"zh-CN": "笔记本", "en": "Notebook"} {
		s.SetLanguage(uiLang)
		if got := ext.Translate(s.Lang(), "assetType.notebook.name"); got != want {
			t.Errorf("UI language %q: extension name = %q, want %q", uiLang, got, want)
		}
	}
}
