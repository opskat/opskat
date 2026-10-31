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

// reviewParams are the reviewer tool's parameters: exactly these required strings
// (extwire.ReviewItem).
var reviewParams = []string{"assetType", "command", "syntax"}

// point is the commandReviewer contribution point over one registry.
type point struct{ reg *Registry }

func (point) Key() string { return extwire.KeyCommandReviewer }

// Check judges the declaration: the manifest's contributes.commandReviewer and
// describe()'s reviewer come together or not at all (the manifest is what the
// user approved before the code ran); the tool is one of the extension's own
// host-only tools whose parameters are exactly the required strings assetType,
// command, syntax; at least one question, ids well-formed and unique, each named
// in some language; and a positive maxCommandLen. It runs for every extension, so
// a manifest declaring a reviewer that describe() lacks is refused too.
func (point) Check(v extension.ExtensionView) error {
	_, err := declaration(v)
	return err
}

// declaration is v's checked reviewer declaration, nil when it declares none.
func declaration(v extension.ExtensionView) (*extwire.CommandReviewer, error) {
	declared := v.Manifest().Contributes.CommandReviewer
	raw := v.Contribution(extwire.KeyCommandReviewer)
	switch {
	case raw == nil && declared:
		return nil, errors.New("commandReviewer: manifest declares contributes.commandReviewer but describe() provides no command reviewer")
	case raw == nil:
		return nil, nil
	case !declared:
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
	if err := checkTool(r.Tool, tools); err != nil {
		return nil, fmt.Errorf("commandReviewer: %w", err)
	}
	if err := checkQuestions(r.Questions); err != nil {
		return nil, fmt.Errorf("commandReviewer: %w", err)
	}
	if r.MaxCommandLen <= 0 {
		return nil, fmt.Errorf("commandReviewer: maxCommandLen must be positive, got %d", r.MaxCommandLen)
	}
	return &r, nil
}

func checkTool(name string, tools []extwire.Tool) error {
	if name == "" {
		return errors.New("tool is required")
	}
	i := slices.IndexFunc(tools, func(t extwire.Tool) bool { return t.Name == name })
	if i < 0 {
		return fmt.Errorf("tool %q is not one of this extension's tools", name)
	}
	tool := tools[i]
	if !tool.HostOnly {
		return fmt.Errorf("tool %q must be a host-only tool: exec and pages may not call the reviewer", name)
	}
	if !exactReviewParams(tool.Parameters) {
		return fmt.Errorf("tool %q parameters must be exactly the required strings %s", name, strings.Join(reviewParams, ", "))
	}
	return nil
}

func exactReviewParams(schema map[string]any) bool {
	props, _ := schema["properties"].(map[string]any)
	if len(props) != len(reviewParams) {
		return false
	}
	required, _ := schema["required"].([]any)
	for _, name := range reviewParams {
		prop, _ := props[name].(map[string]any)
		if typ, _ := prop["type"].(string); typ != "string" || !slices.Contains(required, any(name)) {
			return false
		}
	}
	return true
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
		questions:     questions,
		maxCommandLen: r.MaxCommandLen,
		running:       v.Plugin() != nil,
	}
	if err := p.reg.add(c); err != nil {
		return nil, err
	}
	return func() { p.reg.remove(c) }, nil
}
