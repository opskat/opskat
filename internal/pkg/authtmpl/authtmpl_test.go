package authtmpl_test

import (
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/authtmpl"
)

func mustParse(t *testing.T, src string, opts authtmpl.ParseOptions) *authtmpl.Template {
	t.Helper()
	tpl, err := authtmpl.Parse(src, opts)
	if err != nil {
		t.Fatalf("Parse(%q) unexpected error: %v", src, err)
	}
	return tpl
}

func TestParseAndRender_FieldReference(t *testing.T) {
	tpl := mustParse(t, "Bearer {{token}}", authtmpl.ParseOptions{Fields: []string{"token"}})
	rc := authtmpl.NewRenderContext(map[string]string{"token": "abc123"})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := "Bearer abc123"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_EmptyOptionalField(t *testing.T) {
	tpl := mustParse(t, "[{{token}}]", authtmpl.ParseOptions{Fields: []string{"token"}})
	rc := authtmpl.NewRenderContext(map[string]string{"token": ""})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := "[]"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_StringLiteralEscapes(t *testing.T) {
	tpl := mustParse(t, `{{"a\nb\"c\\d"}}`, authtmpl.ParseOptions{})
	rc := authtmpl.NewRenderContext(nil)
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := "a\nb\"c\\d"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_Concatenation(t *testing.T) {
	tpl := mustParse(t, `{{"user=" + name + "&x=1"}}`, authtmpl.ParseOptions{Fields: []string{"name"}})
	rc := authtmpl.NewRenderContext(map[string]string{"name": "alice"})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := "user=alice&x=1"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_Base64(t *testing.T) {
	tpl := mustParse(t, `{{base64(name)}}`, authtmpl.ParseOptions{Fields: []string{"name"}})
	rc := authtmpl.NewRenderContext(map[string]string{"name": "alice:secret"})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := base64.StdEncoding.EncodeToString([]byte("alice:secret")); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_Hex(t *testing.T) {
	tpl := mustParse(t, `{{hex(name)}}`, authtmpl.ParseOptions{Fields: []string{"name"}})
	rc := authtmpl.NewRenderContext(map[string]string{"name": "abc"})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := hex.EncodeToString([]byte("abc")); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_Urlencode(t *testing.T) {
	tpl := mustParse(t, `{{urlencode(name)}}`, authtmpl.ParseOptions{Fields: []string{"name"}})
	rc := authtmpl.NewRenderContext(map[string]string{"name": "a b/c+d"})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := "a+b%2Fc%2Bd"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_Sha256KnownVector(t *testing.T) {
	// sha256("abc") = ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
	tpl := mustParse(t, `{{hex(sha256(name))}}`, authtmpl.ParseOptions{Fields: []string{"name"}})
	rc := authtmpl.NewRenderContext(map[string]string{"name": "abc"})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_HmacSha256KnownVector(t *testing.T) {
	// RFC 4231 test case 1:
	// Key = 0x0b repeated 20 times, Data = "Hi There"
	// HMAC-SHA256 = b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7
	tpl := mustParse(t, `{{hex(hmac_sha256(key, msg))}}`, authtmpl.ParseOptions{Fields: []string{"key", "msg"}})
	key := string([]byte{
		0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b,
		0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b,
	})
	rc := authtmpl.NewRenderContext(map[string]string{"key": key, "msg": "Hi There"})
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseAndRender_Uuid(t *testing.T) {
	tpl := mustParse(t, `{{uuid()}}`, authtmpl.ParseOptions{})
	rc := authtmpl.NewRenderContext(nil)
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !re.MatchString(got) {
		t.Fatalf("Render() = %q, does not look like a uuid", got)
	}
}

func TestRender_NowConsistentAcrossSingleRenderContext(t *testing.T) {
	tpl := mustParse(t, `{{now.unix}}-{{now.unix_ms}}-{{now.unix}}`, authtmpl.ParseOptions{})
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rc := authtmpl.NewRenderContext(nil, authtmpl.WithClock(func() time.Time { return fixed }))
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	wantUnix := strconv.FormatInt(fixed.Unix(), 10)
	wantUnixMs := strconv.FormatInt(fixed.UnixMilli(), 10)
	want := wantUnix + "-" + wantUnixMs + "-" + wantUnix
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestRender_NowConsistentAcrossTemplatesSharingOneRenderContext(t *testing.T) {
	tplA := mustParse(t, `{{now.unix}}`, authtmpl.ParseOptions{})
	tplB := mustParse(t, `{{now.unix}}`, authtmpl.ParseOptions{})
	rc := authtmpl.NewRenderContext(nil, authtmpl.WithClock(func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	}))
	gotA, err := tplA.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	gotB, err := tplB.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if gotA != gotB {
		t.Fatalf("now.unix differs across templates sharing one RenderContext: %q vs %q", gotA, gotB)
	}
}

func TestRender_NowDiffersAcrossSeparateRenderContexts(t *testing.T) {
	tpl := mustParse(t, `{{now.unix_ms}}`, authtmpl.ParseOptions{})
	t1 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := t1.Add(1500 * time.Millisecond)
	rc1 := authtmpl.NewRenderContext(nil, authtmpl.WithClock(func() time.Time { return t1 }))
	rc2 := authtmpl.NewRenderContext(nil, authtmpl.WithClock(func() time.Time { return t2 }))
	got1, err := tpl.Render(rc1)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	got2, err := tpl.Render(rc2)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got1 == got2 {
		t.Fatalf("expected different now.unix_ms across separate render contexts, both = %q", got1)
	}
}

func TestParseAndRender_RequestContext(t *testing.T) {
	tpl := mustParse(
		t,
		`{{request.method}} {{request.path}} {{request.body}}`,
		authtmpl.ParseOptions{AllowRequest: true},
	)
	rc := authtmpl.NewRenderContext(nil, authtmpl.WithRequest(authtmpl.RequestInfo{
		Method: "POST",
		Path:   "/v1/things",
		Body:   []byte(`{"a":1}`),
	}))
	got, err := tpl.Render(rc)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if want := `POST /v1/things {"a":1}`; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParse_RequestNotAllowed(t *testing.T) {
	_, err := authtmpl.Parse(`{{request.method}}`, authtmpl.ParseOptions{AllowRequest: false})
	if err == nil {
		t.Fatalf("expected error when request.* used without AllowRequest")
	}
}

func TestParse_UnknownField(t *testing.T) {
	_, err := authtmpl.Parse(`{{nope}}`, authtmpl.ParseOptions{Fields: []string{"token"}})
	if err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestParse_UnknownFunction(t *testing.T) {
	_, err := authtmpl.Parse(`{{sillyfunc(1)}}`, authtmpl.ParseOptions{})
	if err == nil {
		t.Fatalf("expected error for unknown function")
	}
}

func TestParse_WrongArgCount(t *testing.T) {
	_, err := authtmpl.Parse(`{{base64()}}`, authtmpl.ParseOptions{})
	if err == nil {
		t.Fatalf("expected error for wrong argument count")
	}
}

func TestParse_UnterminatedExpression(t *testing.T) {
	_, err := authtmpl.Parse(`Bearer {{token`, authtmpl.ParseOptions{Fields: []string{"token"}})
	if err == nil {
		t.Fatalf("expected error for unterminated expression")
	}
}

func TestParse_BadEscape(t *testing.T) {
	_, err := authtmpl.Parse(`{{"a\qb"}}`, authtmpl.ParseOptions{})
	if err == nil {
		t.Fatalf("expected error for invalid escape sequence")
	}
}

func TestParse_UnknownNowAttribute(t *testing.T) {
	_, err := authtmpl.Parse(`{{now.tomorrow}}`, authtmpl.ParseOptions{})
	if err == nil {
		t.Fatalf("expected error for unknown now attribute")
	}
}

func TestRender_MissingFieldValueErrors(t *testing.T) {
	tpl := mustParse(t, `{{token}}`, authtmpl.ParseOptions{Fields: []string{"token"}})
	rc := authtmpl.NewRenderContext(map[string]string{})
	_, err := tpl.Render(rc)
	if err == nil {
		t.Fatalf("expected error when render context omits a known field")
	}
}

func TestAuthTypeFor_BuiltinsRegistered(t *testing.T) {
	for _, kind := range []string{"header", "query", "basic"} {
		if _, ok := authtmpl.AuthTypeFor(kind); !ok {
			t.Fatalf("AuthTypeFor(%q) not found", kind)
		}
	}
	if _, ok := authtmpl.AuthTypeFor("nonexistent"); ok {
		t.Fatalf("AuthTypeFor(nonexistent) unexpectedly found")
	}
}
