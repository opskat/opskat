package reviewerreg

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

// reviewTool is a host-only tool whose parameters are exactly the reviewer's.
func reviewTool(name string) extwire.Tool {
	return extwire.Tool{Name: name, HostOnly: true, Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"assetType": map[string]any{"type": "string", "description": "asset type"},
			"command":   map[string]any{"type": "string"},
			"syntax":    map[string]any{"type": "string"},
		},
		"required": []any{"assetType", "command", "syntax"},
	}}
}

func question(id string) extwire.ReviewQuestion {
	return extwire.ReviewQuestion{ID: id, Names: map[string]string{"en": id + " (en)", "zh-CN": id + "（中文）"}}
}

func validReviewer() extwire.CommandReviewer {
	return extwire.CommandReviewer{Tool: "review", Questions: []extwire.ReviewQuestion{question("destructive"), question("remote_code")}, MaxCommandLen: 4000}
}

// view is extension name (version 1.2.0, display name "<name> reviewer" in en)
// whose manifest declares contributes.commandReviewer as given and whose
// describe() contributes tools and, unless nil, reviewer. running gives it a
// (stand-in) running guest, as a loaded extension has.
type viewSpec struct {
	name     string
	declared bool
	tools    []extwire.Tool
	reviewer any
	running  bool
}

type runningView struct{ extension.ExtensionView }

func (runningView) Plugin() *extension.Plugin { return &extension.Plugin{} }

func view(s viewSpec) extension.ExtensionView {
	contract := extmanifest.Manifest{Name: s.name, Version: "1.2.0"}
	contract.Contributes.CommandReviewer = s.declared
	d := extensiontest.Describe{I18n: extension.ManifestI18n{DisplayName: "ext.displayName"}, Contributes: map[string]any{}}
	if s.tools != nil {
		d.Contributes[extwire.KeyTools] = s.tools
	}
	if s.reviewer != nil {
		d.Contributes[extwire.KeyCommandReviewer] = s.reviewer
	}
	m := extensiontest.Described(contract, d)
	v := (&extension.ManifestInfo{Name: s.name, Manifest: m, Locales: map[string]map[string]string{
		"en":    {"ext.displayName": s.name + " reviewer"},
		"zh-cn": {"ext.displayName": s.name + " 审核器"},
	}}).View()
	if s.running {
		return runningView{v}
	}
	return v
}

func reviewerView(name string, running bool) extension.ExtensionView {
	return view(viewSpec{name: name, declared: true, tools: []extwire.Tool{reviewTool("review")}, reviewer: validReviewer(), running: running})
}

func TestPointChecksTheDeclaration(t *testing.T) {
	point := New(nil).Point()
	assert.Equal(t, extwire.KeyCommandReviewer, point.Key())

	withReviewer := func(mut func(*extwire.CommandReviewer)) any {
		r := validReviewer()
		mut(&r)
		return r
	}
	paramsTool := func(props map[string]any, required ...any) extwire.Tool {
		tool := reviewTool("review")
		tool.Parameters = map[string]any{"type": "object", "properties": props, "required": required}
		return tool
	}
	str := map[string]any{"type": "string"}
	longID := "a" + strings.Repeat("b", 64)

	cases := []struct {
		name    string
		spec    viewSpec
		wantErr string // "" = accepted
	}{
		{name: "no reviewer anywhere", spec: viewSpec{name: "x"}},
		{name: "declared and described", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")}, reviewer: validReviewer()}},
		{name: "manifest declares, describe does not",
			spec:    viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")}},
			wantErr: "commandReviewer: manifest declares contributes.commandReviewer but describe() provides no command reviewer"},
		{name: "describe provides, manifest does not declare",
			spec:    viewSpec{name: "x", tools: []extwire.Tool{reviewTool("review")}, reviewer: validReviewer()},
			wantErr: "commandReviewer: describe() provides a command reviewer that manifest contributes.commandReviewer does not declare"},
		{name: "unknown key", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: map[string]any{"tool": "review", "questions": []any{question("a")}, "maxCommandLen": 10, "maxItems": 1}},
			wantErr: `"maxItems"`},
		{name: "tool missing", spec: viewSpec{name: "x", declared: true, reviewer: validReviewer()},
			wantErr: `commandReviewer: tool "review" is not one of this extension's tools`},
		{name: "tool not named", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")}, reviewer: withReviewer(func(r *extwire.CommandReviewer) { r.Tool = "" })},
			wantErr: "commandReviewer: tool is required"},
		{name: "tool not host-only", spec: viewSpec{name: "x", declared: true, reviewer: validReviewer(),
			tools: []extwire.Tool{func() extwire.Tool { t := reviewTool("review"); t.HostOnly, t.PageOnly = false, true; return t }()}},
			wantErr: `commandReviewer: tool "review" must be a host-only tool`},
		{name: "extra optional parameters", spec: viewSpec{name: "x", declared: true, reviewer: validReviewer(),
			tools: []extwire.Tool{paramsTool(map[string]any{"assetType": str, "command": str, "syntax": str, "assetName": str, "depth": map[string]any{"type": "integer"}}, "assetType", "command", "syntax")}}},
		{name: "extra required parameter", spec: viewSpec{name: "x", declared: true, reviewer: validReviewer(),
			tools: []extwire.Tool{paramsTool(map[string]any{"assetType": str, "command": str, "syntax": str, "asset": str}, "assetType", "command", "syntax", "asset")}},
			wantErr: `commandReviewer: tool "review" parameter "asset" must be optional: only assetType, command, syntax may be required`},
		{name: "missing parameter", spec: viewSpec{name: "x", declared: true, reviewer: validReviewer(),
			tools: []extwire.Tool{paramsTool(map[string]any{"assetType": str, "command": str}, "assetType", "command")}},
			wantErr: `commandReviewer: tool "review" parameters must include the required strings assetType, command, syntax`},
		{name: "parameter not a string", spec: viewSpec{name: "x", declared: true, reviewer: validReviewer(),
			tools: []extwire.Tool{paramsTool(map[string]any{"assetType": str, "command": str, "syntax": map[string]any{"type": "integer"}}, "assetType", "command", "syntax")}},
			wantErr: `commandReviewer: tool "review" parameters must include the required strings assetType, command, syntax`},
		{name: "parameter optional", spec: viewSpec{name: "x", declared: true, reviewer: validReviewer(),
			tools: []extwire.Tool{paramsTool(map[string]any{"assetType": str, "command": str, "syntax": str}, "assetType", "command")}},
			wantErr: `commandReviewer: tool "review" parameters must include the required strings assetType, command, syntax`},
		{name: "no questions", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) { r.Questions = nil })},
			wantErr: "commandReviewer: questions: at least one question is required"},
		{name: "bad id", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) { r.Questions = []extwire.ReviewQuestion{question("Remote-code")} })},
			wantErr: `commandReviewer: questions[Remote-code]: id must match ^[a-z][a-z0-9_]{0,63}$`},
		{name: "id too long", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) { r.Questions = []extwire.ReviewQuestion{question(longID)} })},
			wantErr: "id must match"},
		{name: "duplicate id", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) {
				r.Questions = []extwire.ReviewQuestion{question("destructive"), question("destructive")}
			})},
			wantErr: "commandReviewer: questions[destructive]: duplicate id"},
		{name: "question without names", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) { r.Questions = []extwire.ReviewQuestion{{ID: "destructive"}} })},
			wantErr: "commandReviewer: questions[destructive]: names: at least one language's name is required"},
		{name: "empty name", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) {
				r.Questions = []extwire.ReviewQuestion{{ID: "destructive", Names: map[string]string{"en": " "}}}
			})},
			wantErr: `commandReviewer: questions[destructive]: names["en"] is empty`},
		{name: "name without a language", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) {
				r.Questions = []extwire.ReviewQuestion{{ID: "destructive", Names: map[string]string{"": "may destroy data"}}}
			})},
			wantErr: `commandReviewer: questions[destructive]: names: a name's language is empty`},
		{name: "no max length", spec: viewSpec{name: "x", declared: true, tools: []extwire.Tool{reviewTool("review")},
			reviewer: withReviewer(func(r *extwire.CommandReviewer) { r.MaxCommandLen = 0 })},
			wantErr: "commandReviewer: maxCommandLen must be positive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := point.Check(view(c.spec))
			if c.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

// The registry refuses an extension whose reviewer the point refuses: loading
// fails naming the extension and the rule.
func TestLoadFailsNamingTheExtension(t *testing.T) {
	reg := New(nil)
	contributions := extension.NewContributionRegistry(reg.Point(), passPoint(extwire.KeyTools))
	err := contributions.Attach(view(viewSpec{name: "jev", declared: true, reviewer: validReviewer()}))
	require.Error(t, err)
	assert.Equal(t, `jev: commandReviewer: tool "review" is not one of this extension's tools`, err.Error())
	assert.Empty(t, reg.Selecting(func() string { return "" }).State(context.Background(), "en").Reviewers)
}

type passPoint string

func (p passPoint) Key() string                                  { return string(p) }
func (passPoint) Check(extension.ExtensionView) error            { return nil }
func (passPoint) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }

// call records the host-only calls a registry makes and answers them.
type call struct {
	ext, tool string
	args      extwire.ReviewItem
	raw       json.RawMessage
	timeout   time.Duration
}

type fakeHost struct {
	calls  []call
	answer string
	err    error
}

func (h *fakeHost) call(_ context.Context, ext, tool string, args json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	var item extwire.ReviewItem
	if err := json.Unmarshal(args, &item); err != nil {
		return nil, err
	}
	h.calls = append(h.calls, call{ext: ext, tool: tool, args: item, raw: args, timeout: timeout})
	return json.RawMessage(h.answer), h.err
}

func newRegistry(t *testing.T, host *fakeHost) (*Registry, *extension.ContributionRegistry) {
	t.Helper()
	reg := New(host.call)
	reg.SetSettingsHasher(func(_ context.Context, ext string) (string, error) { return "hash-of-" + ext, nil })
	// A local install on a non-release build: usable, unsigned.
	reg.release = func() bool { return false }
	reg.SetProvenance(func(context.Context, string) (extension_svc.Provenance, error) {
		return extension_svc.Provenance{Recorded: true, Source: extension_svc.InstallOriginLocal, PackageSHA256: "sum", DigestMatches: true}, nil
	})
	return reg, extension.NewContributionRegistry(reg.Point(), passPoint(extwire.KeyTools))
}

func TestAttachAndRevokeCandidates(t *testing.T) {
	reg, contributions := newRegistry(t, &fakeHost{answer: `{"outcome":"pass"}`})
	src := reg.Selecting(func() string { return "" })
	ctx := context.Background()

	require.NoError(t, contributions.Attach(view(viewSpec{name: "plain"}))) // contributes no reviewer
	require.NoError(t, contributions.Attach(reviewerView("jev", true)))
	st := src.State(ctx, "zh-CN")
	// The extension's detail page lists what it reviews: its questions, named per language.
	assert.Equal(t, []command_review_svc.ReviewerInfo{{Name: "jev", DisplayName: "jev 审核器", Version: "1.2.0", Usable: true,
		Questions: []command_review_svc.Question{
			{ID: "destructive", Names: map[string]string{"en": "destructive (en)", "zh-CN": "destructive（中文）"}},
			{ID: "remote_code", Names: map[string]string{"en": "remote_code (en)", "zh-CN": "remote_code（中文）"}},
		}}}, st.Reviewers)
	assert.Equal(t, "jev", st.Active)
	assert.Empty(t, st.Problem)

	contributions.Revoke("jev")
	st = src.State(ctx, "en")
	assert.Empty(t, st.Reviewers)
	assert.NotNil(t, st.Reviewers, "an empty list must reach the frontend as [], not null")
	assert.Empty(t, st.Active)
	assert.Equal(t, aictx.ReviewReasonNoReviewer, st.Problem)
	_, err := src.Current(ctx)
	assert.ErrorIs(t, err, command_review_svc.ErrNoReviewer)

	// Re-attach after a revoke (enable again, upgrade replacing the old one).
	require.NoError(t, contributions.Attach(reviewerView("jev", true)))
	_, err = src.Current(ctx)
	assert.NoError(t, err)
}

func TestSelectionRules(t *testing.T) {
	ctx := context.Background()
	attach := func(t *testing.T, views ...extension.ExtensionView) *Registry {
		reg, contributions := newRegistry(t, &fakeHost{answer: `{"outcome":"pass"}`})
		for _, v := range views {
			require.NoError(t, contributions.Attach(v))
		}
		return reg
	}
	current := func(reg *Registry, selected string) (string, error) {
		rev, err := reg.Selecting(func() string { return selected }).Current(ctx)
		if err != nil {
			return "", err
		}
		return rev.Identity().Name, nil
	}

	t.Run("no candidate is no reviewer, selected or not", func(t *testing.T) {
		reg := attach(t)
		for _, sel := range []string{"", "jev"} {
			_, err := current(reg, sel)
			assert.ErrorIs(t, err, command_review_svc.ErrNoReviewer)
		}
	})
	t.Run("the only candidate is used when none is selected, or the selected one is gone", func(t *testing.T) {
		reg := attach(t, reviewerView("jev", true))
		for _, sel := range []string{"", "uninstalled"} {
			name, err := current(reg, sel)
			require.NoError(t, err)
			assert.Equal(t, "jev", name)
		}
		st := reg.Selecting(func() string { return "uninstalled" }).State(ctx, "en")
		assert.Empty(t, st.Selected, "a selection that is no candidate is not shown as selected")
		assert.Equal(t, "jev", st.Active)
	})
	t.Run("several candidates: the selected one, else no reviewer until one is selected", func(t *testing.T) {
		reg := attach(t, reviewerView("b", true), reviewerView("a", true))
		name, err := current(reg, "b")
		require.NoError(t, err)
		assert.Equal(t, "b", name)
		st := reg.Selecting(func() string { return "b" }).State(ctx, "en")
		assert.Equal(t, "b", st.Selected)
		assert.Equal(t, "b", st.Active)
		assert.Equal(t, []string{"a", "b"}, []string{st.Reviewers[0].Name, st.Reviewers[1].Name}, "listed by name")

		_, err = current(reg, "")
		assert.ErrorIs(t, err, command_review_svc.ErrNoReviewer)
		assert.ErrorContains(t, err, "a, b")
		st = reg.Selecting(func() string { return "" }).State(ctx, "en")
		assert.Empty(t, st.Active)
		assert.Equal(t, aictx.ReviewReasonNoReviewer, st.Problem)
	})
	t.Run("a candidate not running in this process is unavailable", func(t *testing.T) {
		reg := attach(t, reviewerView("jev", false))
		_, err := current(reg, "")
		assert.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
		st := reg.Selecting(func() string { return "" }).State(ctx, "en")
		require.Len(t, st.Reviewers, 1)
		assert.False(t, st.Reviewers[0].Usable)
		assert.NotEmpty(t, st.Reviewers[0].Reason)
		assert.Empty(t, st.Active)
		assert.Equal(t, aictx.ReviewReasonReviewerUnavailable, st.Problem)
	})
	t.Run("a candidate whose settings cannot be read is unavailable", func(t *testing.T) {
		reg := attach(t, reviewerView("jev", true))
		reg.SetSettingsHasher(func(context.Context, string) (string, error) { return "", errors.New("db closed") })
		_, err := current(reg, "")
		assert.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
		assert.ErrorContains(t, err, "db closed")
	})
	t.Run("Reviewer names one candidate, selected or not", func(t *testing.T) {
		reg := attach(t, reviewerView("a", true), reviewerView("b", true))
		src := reg.Selecting(func() string { return "a" })
		rev, err := src.Reviewer(ctx, "b")
		require.NoError(t, err)
		assert.Equal(t, "b", rev.Identity().Name)
		_, err = src.Reviewer(ctx, "missing")
		assert.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
	})
}

// A reviewer is the candidate's declaration, and Review is one host-only call of
// its tool per command.
func TestReviewerCallsTheHostOnlyTool(t *testing.T) {
	ctx := context.Background()
	host := &fakeHost{answer: `{"outcome":"reject","failed":["destructive"],"scores":{"destructive":0.9}}`}
	reg, contributions := newRegistry(t, host)
	require.NoError(t, contributions.Attach(reviewerView("jev", true)))
	rev, err := reg.Selecting(func() string { return "" }).Current(ctx)
	require.NoError(t, err)

	id := rev.Identity()
	assert.Equal(t, []string{"jev", "1.2.0", "hash-of-jev"}, []string{id.Name, id.Version, id.SettingsHash})
	assert.Equal(t, 4000, rev.MaxCommandLen())
	assert.Equal(t, []command_review_svc.Question{
		{ID: "destructive", Names: map[string]string{"en": "destructive (en)", "zh-CN": "destructive（中文）"}},
		{ID: "remote_code", Names: map[string]string{"en": "remote_code (en)", "zh-CN": "remote_code（中文）"}},
	}, rev.Questions())

	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	answer, err := rev.Review(callCtx, "ssh", "rm -rf /", "shell")
	require.NoError(t, err)
	assert.Equal(t, command_review_svc.Answer{Outcome: "reject", Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 0.9}}, answer)
	require.Len(t, host.calls, 1)
	c := host.calls[0]
	assert.Equal(t, "jev", c.ext)
	assert.Equal(t, "review", c.tool)
	assert.Equal(t, extwire.ReviewItem{AssetType: "ssh", Command: "rm -rf /", Syntax: "shell"}, c.args)
	assert.InDelta(t, 3*time.Second, c.timeout, float64(time.Second), "the call is bounded by the review's own timeout")

	t.Run("an answer in another shape is an error", func(t *testing.T) {
		host.answer = `{"outcome":"pass","verdict":"ok"}`
		_, err := rev.Review(ctx, "ssh", "ls", "shell")
		assert.ErrorContains(t, err, `"verdict"`)
	})
	t.Run("a failed call is an error", func(t *testing.T) {
		host.answer, host.err = "", errors.New("guest trapped")
		_, err := rev.Review(ctx, "ssh", "ls", "shell")
		assert.ErrorContains(t, err, "guest trapped")
	})
}

// A reviewer tool may declare optional parameters the host does not know (a newer
// SDK's, say). It still loads, and a call carries only the parameters the host
// knows and the tool declares — never one the tool would refuse.
func TestReviewCallCarriesOnlyDeclaredParamsTheHostKnows(t *testing.T) {
	ctx := context.Background()
	host := &fakeHost{answer: `{"outcome":"pass"}`}
	reg, contributions := newRegistry(t, host)
	tool := reviewTool("review")
	tool.Parameters["properties"].(map[string]any)["assetName"] = map[string]any{"type": "string"}
	require.NoError(t, contributions.Attach(view(viewSpec{name: "jev", declared: true, tools: []extwire.Tool{tool}, reviewer: validReviewer(), running: true})))
	rev, err := reg.Selecting(func() string { return "" }).Current(ctx)
	require.NoError(t, err)

	_, err = rev.Review(ctx, "ssh", "ls", "shell")
	require.NoError(t, err)
	require.Len(t, host.calls, 1)
	assert.JSONEq(t, `{"assetType":"ssh","command":"ls","syntax":"shell"}`, string(host.calls[0].raw))
}
