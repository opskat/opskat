package reviewerreg

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// questionIDRe is a question id: a lowercase letter, then lowercase letters,
// digits and underscores, 64 at most.
var questionIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// reviewParams are the reviewer tool's required parameters, all strings
// (extwire.ReviewItem's required fields). The tool may declare optional ones too.
var reviewParams = []string{"assetType", "command", "syntax"}

// point is the commandReviewer contribution point over one registry.
type point struct{ reg *Registry }

func (point) Key() string { return extwire.KeyCommandReviewer }

// Check judges the declaration: the manifest's contributes.commandReviewer and
// describe()'s reviewer come together or not at all (the manifest is what the
// user approved before the code ran); the tool is one of the extension's own
// host-only tools whose parameters include the required strings assetType,
// command, syntax and require nothing else (other parameters are optional); at
// least one question, ids well-formed and unique, each named in some language;
// and a positive maxCommandLen. It runs for every extension, so a manifest
// declaring a reviewer that describe() lacks is refused too.
func (point) Check(v extension.ExtensionView) error {
	_, err := declaration(v)
	return err
}

// declared is a checked reviewer declaration and the parameter names its tool
// declares.
type declared struct {
	extwire.CommandReviewer
	params []string
}

// declaration is v's checked reviewer declaration, nil when it declares none.
func declaration(v extension.ExtensionView) (*declared, error) {
	inManifest := v.Manifest().Contributes.CommandReviewer
	raw := v.Contribution(extwire.KeyCommandReviewer)
	switch {
	case raw == nil && inManifest:
		return nil, errors.New("commandReviewer: manifest declares contributes.commandReviewer but describe() provides no command reviewer")
	case raw == nil:
		return nil, nil
	case !inManifest:
		return nil, errors.New("commandReviewer: describe() provides a command reviewer that manifest contributes.commandReviewer does not declare")
	}
	var r extwire.CommandReviewer
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	var tools []extwire.Tool
	if raw := v.Contribution(extwire.KeyTools); raw != nil {
		if err := json.Unmarshal(raw, &tools); err != nil {
			return nil, fmt.Errorf("tools: %w", err)
		}
	}
	params, err := checkTool(r.Tool, tools)
	if err != nil {
		return nil, fmt.Errorf("commandReviewer: %w", err)
	}
	if err := checkQuestions(r.Questions); err != nil {
		return nil, fmt.Errorf("commandReviewer: %w", err)
	}
	if r.MaxCommandLen <= 0 {
		return nil, fmt.Errorf("commandReviewer: maxCommandLen must be positive, got %d", r.MaxCommandLen)
	}
	return &declared{CommandReviewer: r, params: params}, nil
}

// checkTool checks the reviewer's tool and returns the parameter names it declares.
func checkTool(name string, tools []extwire.Tool) ([]string, error) {
	if name == "" {
		return nil, errors.New("tool is required")
	}
	i := slices.IndexFunc(tools, func(t extwire.Tool) bool { return t.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("tool %q is not one of this extension's tools", name)
	}
	tool := tools[i]
	if !tool.HostOnly {
		return nil, fmt.Errorf("tool %q must be a host-only tool: exec and pages may not call the reviewer", name)
	}
	props, _ := tool.Parameters["properties"].(map[string]any)
	required, _ := tool.Parameters["required"].([]any)
	for _, p := range reviewParams {
		prop, _ := props[p].(map[string]any)
		if typ, _ := prop["type"].(string); typ != "string" || !slices.Contains(required, any(p)) {
			return nil, fmt.Errorf("tool %q parameters must include the required strings %s", name, strings.Join(reviewParams, ", "))
		}
	}
	// A required parameter the host does not send would fail every call.
	for _, p := range required {
		if s, _ := p.(string); !slices.Contains(reviewParams, s) {
			return nil, fmt.Errorf("tool %q parameter %q must be optional: only %s may be required", name, p, strings.Join(reviewParams, ", "))
		}
	}
	params := make([]string, 0, len(props))
	for p := range props {
		params = append(params, p)
	}
	return params, nil
}

func checkQuestions(questions []extwire.ReviewQuestion) error {
	if len(questions) == 0 {
		return errors.New("questions: at least one question is required")
	}
	seen := make(map[string]struct{}, len(questions))
	for _, q := range questions {
		if !questionIDRe.MatchString(q.ID) {
			return fmt.Errorf("questions[%s]: id must match %s", q.ID, questionIDRe.String())
		}
		if _, dup := seen[q.ID]; dup {
			return fmt.Errorf("questions[%s]: duplicate id", q.ID)
		}
		seen[q.ID] = struct{}{}
		if len(q.Names) == 0 {
			return fmt.Errorf("questions[%s]: names: at least one language's name is required", q.ID)
		}
		for lang, name := range q.Names {
			if lang == "" {
				return fmt.Errorf("questions[%s]: names: a name's language is empty", q.ID)
			}
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("questions[%s]: names[%q] is empty", q.ID, lang)
			}
		}
	}
	return nil
}

// Attach makes an extension that contributes a reviewer a candidate, and its
// revoke — disable, uninstall, an upgrade replacing it — takes the candidate out.
// Whether the candidate can review here is whether its guest runs in this
// process (a describe-only attach, as opsctl's at startup, has none).
func (p point) Attach(v extension.ExtensionView) (func(), error) {
	r, err := declaration(v)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return func() {}, nil
	}
	questions := make([]command_review_svc.Question, len(r.Questions))
	for i, q := range r.Questions {
		questions[i] = command_review_svc.Question{ID: q.ID, Names: q.Names}
	}
	m := v.Manifest()
	c := &candidate{
		name:          v.Name(),
		version:       m.Version,
		displayName:   m.I18n.DisplayName,
		translate:     v.Translate,
		tool:          r.Tool,
		params:        r.params,
		questions:     questions,
		maxCommandLen: r.MaxCommandLen,
		running:       v.Plugin() != nil,
	}
	if err := p.reg.add(c); err != nil {
		return nil, err
	}
	return func() { p.reg.remove(c) }, nil
}
