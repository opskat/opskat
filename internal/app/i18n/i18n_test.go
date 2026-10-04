package i18n

import "testing"

// Pick backs host-generated text such as the ext dev install approval detail
// (internal/app/opsctl/ext_dev.go's credentials:read warning). The lang it
// receives is o.lang.Lang(): the frontend's i18next code ("zh-CN" or "en"),
// lowercased by System.SetLanguage — Pick must read either spelling alike.
func TestPick_MatchesFrontendLanguageCodes(t *testing.T) {
	cases := []struct {
		name string
		lang string
		want string
	}{
		{name: "frontend zh-CN code", lang: "zh-CN", want: "zh"},
		{name: "frontend en code", lang: "en", want: "en"},
		{name: "unset defaults to english", lang: "", want: "en"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Pick(c.lang, "zh", "en"); got != c.want {
				t.Fatalf("Pick(%q) = %q, want %q", c.lang, got, c.want)
			}
		})
	}
}
