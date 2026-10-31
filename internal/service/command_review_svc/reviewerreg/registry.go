// Package reviewerreg is the command reviewer registry: the commandReviewer
// contribution point (Point) makes every extension that contributes a reviewer a
// candidate, and the registry picks the one the review service uses — it is the
// service's command_review_svc.ReviewerSource (Selecting). A candidate's Review
// is one call of the extension's host-only review tool (extreg.CallHostTool).
//
// It sits beside command_review_svc rather than in it so the review service
// stays free of the extension runtime: the service sees only the
// ReviewerSource / Reviewer it declares.
package reviewerreg

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// HostToolCaller calls a running extension's host-only tool (extreg.CallHostTool).
type HostToolCaller func(ctx context.Context, extName, tool string, args json.RawMessage, timeout time.Duration) (json.RawMessage, error)

// SettingsHasher answers the hash of an extension's saved settings, which keys
// the review cache: changed settings never hit the old verdicts.
type SettingsHasher func(ctx context.Context, extName string) (string, error)

// Registry is the reviewer candidates the commandReviewer contribution point
// attaches.
type Registry struct {
	call HostToolCaller

	mu         sync.Mutex
	hash       SettingsHasher
	candidates map[string]*candidate
}

// New builds a registry whose candidates review through call.
func New(call HostToolCaller) *Registry {
	return &Registry{call: call, candidates: make(map[string]*candidate)}
}

var defaultRegistry = New(extreg.CallHostTool)

// Default is the process's registry: the one whose Point the desktop app and
// opsctl register and bootstrap hands the review service.
func Default() *Registry { return defaultRegistry }

// Point is the commandReviewer contribution point that fills r.
func (r *Registry) Point() extension.ContributionPoint { return point{reg: r} }

// SetSettingsHasher wires the settings hash in: the process that runs the
// extensions sets it (extension_svc.Service.SettingsHash) before any loads.
func (r *Registry) SetSettingsHasher(h SettingsHasher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hash = h
}

// candidate is one extension contributing a reviewer.
type candidate struct {
	name, version string
	displayName   string // i18n key
	translate     func(lang, key string) string
	tool          string
	questions     []command_review_svc.Question
	maxCommandLen int
	running       bool
}

func (r *Registry) add(c *candidate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.candidates[c.name]; taken {
		return fmt.Errorf("commandReviewer: %s is already a command reviewer candidate", c.name)
	}
	r.candidates[c.name] = c
	return nil
}

// remove takes c out, unless another attach has replaced it since.
func (r *Registry) remove(c *candidate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.candidates[c.name] == c {
		delete(r.candidates, c.name)
	}
}

// snapshot is the candidates, by name.
func (r *Registry) snapshot() []*candidate {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*candidate, 0, len(r.candidates))
	for _, c := range r.candidates {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *candidate) int { return strings.Compare(a.name, b.name) })
	return out
}

// Selecting is r as the review service's reviewer source, picking by the name
// selected answers (the user's choice in settings, "" for none).
func (r *Registry) Selecting(selected func() string) *Source {
	return &Source{reg: r, selected: selected}
}

// Source is the reviewer source: the selected candidate when it is one, else the
// only candidate, else none (no candidate, or several and none selected). A
// disable or uninstall does not clear the selection, so a selection that is no
// candidate is passed over, never used.
type Source struct {
	reg      *Registry
	selected func() string
}

var _ command_review_svc.ReviewerSource = (*Source)(nil)

// pick is the candidate the rules choose, or why there is none
// (command_review_svc.ErrNoReviewer).
func (s *Source) pick(cands []*candidate) (*candidate, error) {
	sel := s.selected()
	if i := slices.IndexFunc(cands, func(c *candidate) bool { return c.name == sel }); sel != "" && i >= 0 {
		return cands[i], nil
	}
	switch len(cands) {
	case 0:
		return nil, fmt.Errorf("%w: no enabled extension contributes a command reviewer", command_review_svc.ErrNoReviewer)
	case 1:
		return cands[0], nil
	}
	names := make([]string, len(cands))
	for i, c := range cands {
		names[i] = c.name
	}
	return nil, fmt.Errorf("%w: several extensions contribute a command reviewer (%s); select one in settings",
		command_review_svc.ErrNoReviewer, strings.Join(names, ", "))
}

// Current is the reviewer to use now.
func (s *Source) Current(ctx context.Context) (command_review_svc.Reviewer, error) {
	c, err := s.pick(s.reg.snapshot())
	if err != nil {
		return nil, err
	}
	return s.reg.reviewer(ctx, c)
}

// Reviewer is the candidate named name, selected or not (Settings › Extensions
// tests the one whose settings it shows).
func (s *Source) Reviewer(ctx context.Context, name string) (command_review_svc.Reviewer, error) {
	for _, c := range s.reg.snapshot() {
		if c.name == name {
			return s.reg.reviewer(ctx, c)
		}
	}
	return nil, fmt.Errorf("%w: %q contributes no command reviewer", command_review_svc.ErrReviewerUnavailable, name)
}

// State is what the settings page and the permission mode card show: every
// candidate (display names in lang), the selection if it is a candidate, and the
// reviewer the rules pick when it can review here — else the problem, named as
// the review failure it causes.
func (s *Source) State(_ context.Context, lang string) command_review_svc.State {
	cands := s.reg.snapshot()
	// It crosses IPC to the frontend: an empty list must be [] there, not null.
	st := command_review_svc.State{Reviewers: make([]command_review_svc.ReviewerInfo, 0, len(cands))}
	sel := s.selected()
	for _, c := range cands {
		info := command_review_svc.ReviewerInfo{
			Name: c.name, DisplayName: c.translate(lang, c.displayName), Version: c.version, Usable: c.running,
		}
		if !c.running {
			info.Reason = errNotRunning(c).Error()
		}
		if c.name == sel {
			st.Selected = sel
		}
		st.Reviewers = append(st.Reviewers, info)
	}
	c, err := s.pick(cands)
	switch {
	case err != nil:
		st.Problem = aictx.ReviewReasonNoReviewer
	case !c.running:
		st.Problem = aictx.ReviewReasonReviewerUnavailable
	default:
		st.Active = c.name
	}
	return st
}

func errNotRunning(c *candidate) error {
	return fmt.Errorf("%w: extension %s is not running in this process", command_review_svc.ErrReviewerUnavailable, c.name)
}

// reviewer is c ready to review: running here, with its settings hash read now.
func (r *Registry) reviewer(ctx context.Context, c *candidate) (command_review_svc.Reviewer, error) {
	if !c.running {
		return nil, errNotRunning(c)
	}
	r.mu.Lock()
	hash := r.hash
	r.mu.Unlock()
	settingsHash, err := hash(ctx, c.name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", command_review_svc.ErrReviewerUnavailable, c.name, err)
	}
	return &reviewer{c: c, settingsHash: settingsHash, call: r.call}, nil
}

// reviewer is a candidate's command_review_svc.Reviewer.
type reviewer struct {
	c            *candidate
	settingsHash string
	call         HostToolCaller
}

func (v *reviewer) Identity() (string, string, string) { return v.c.name, v.c.version, v.settingsHash }

func (v *reviewer) Questions() []command_review_svc.Question { return v.c.questions }

func (v *reviewer) MaxCommandLen() int { return v.c.maxCommandLen }

// Review is one call of the candidate's host-only tool, bounded by ctx's deadline
// (the review service's per-call timeout).
func (v *reviewer) Review(ctx context.Context, assetType, command, syntax string) (command_review_svc.Answer, error) {
	args, err := json.Marshal(extwire.ReviewItem{AssetType: assetType, Command: command, Syntax: syntax})
	if err != nil {
		return command_review_svc.Answer{}, err
	}
	var timeout time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	out, err := v.call(ctx, v.c.name, v.c.tool, args, timeout)
	if err != nil {
		return command_review_svc.Answer{}, err
	}
	a, err := extwire.ParseReviewAnswer(out)
	if err != nil {
		return command_review_svc.Answer{}, fmt.Errorf("%s.%s: %w", v.c.name, v.c.tool, err)
	}
	return command_review_svc.Answer{Outcome: a.Outcome, Failed: a.Failed, Scores: a.Scores, Reason: a.Reason}, nil
}
