package policy

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// IsZh/PolicyMsg pick the language opsctl-initiated approval flows show the
// user (e.g. the "user denied" message in internal/ai/permission/checker.go).
// That language reaches ctx via aictx.WithPolicyLang(ctx, o.lang.Lang()):
// the frontend's i18next code ("zh-CN" or "en"), lowercased by
// System.SetLanguage. IsZh must read either spelling alike, or opsctl users
// quietly get the wrong language.
func TestIsZh_MatchesFrontendLanguageCodes(t *testing.T) {
	cases := []struct {
		name string
		lang string
		want bool
	}{
		{name: "frontend zh-CN code", lang: "zh-CN", want: true},
		{name: "frontend en code", lang: "en", want: false},
		{name: "bare zh", lang: "zh", want: true},
		{name: "unset defaults to english", lang: "", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := aictx.WithPolicyLang(t.Context(), c.lang)
			if got := IsZh(ctx); got != c.want {
				t.Fatalf("IsZh(%q) = %v, want %v", c.lang, got, c.want)
			}
		})
	}
}

func TestPolicyMsg_PicksMessageForFrontendLanguageCode(t *testing.T) {
	zhCtx := aictx.WithPolicyLang(t.Context(), "zh-CN")
	if got := PolicyMsg(zhCtx, "en text", "zh text"); got != "zh text" {
		t.Fatalf("PolicyMsg with zh-CN = %q, want zh text", got)
	}

	enCtx := aictx.WithPolicyLang(t.Context(), "en")
	if got := PolicyMsg(enCtx, "en text", "zh text"); got != "en text" {
		t.Fatalf("PolicyMsg with en = %q, want en text", got)
	}
}
