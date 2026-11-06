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
	"errors"
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

// SettingsReader answers the read view of an extension's saved settings
// (extension_svc.Service.SettingsView): non-secret values, and whether each
// secret is set. The registry judges a reviewer configured by it.
type SettingsReader func(ctx context.Context, extName string) (*extwire.SettingsView, error)

// Activator brings a describe-only candidate up as a running extension in this
// process (opsctl: the existing activation path, released at command end).
type Activator func(ctx context.Context, extName string) error

// Registry is the reviewer candidates the commandReviewer contribution point
// attaches.
type Registry struct {
	call HostToolCaller

	mu         sync.Mutex
	hash       SettingsHasher
	settings   SettingsReader
	provenance ProvenanceFacts
	release    func() bool // whether this is a release build (isReleaseBuild)
	activate   Activator
	candidates map[string]*candidate
}

// New builds a registry whose candidates review through call.
func New(call HostToolCaller) *Registry {
	return &Registry{call: call, release: isReleaseBuild, candidates: make(map[string]*candidate)}
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

// SetSettingsReader wires in the settings read view a reviewer is judged
// configured by (extension_svc.Service.SettingsView). Without it a reviewer that
// declares required settings is unavailable: it is never called unchecked.
func (r *Registry) SetSettingsReader(read SettingsReader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings = read
}

// SetProvenance wires in the install facts the registry judges a candidate by
// (extension_svc.Service.Provenance). Without them nothing is eligible: a reviewer
// is never used unchecked.
func (r *Registry) SetProvenance(p ProvenanceFacts) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.provenance = p
}

// SetActivator wires in the way a describe-only candidate is brought up. A process
// that runs every enabled extension itself (the desktop app) sets none.
func (r *Registry) SetActivator(a Activator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activate = a
}

// candidate is one extension contributing a reviewer.
type candidate struct {
	name, version string
	displayName   string // i18n key
	translate     func(lang, key string) string
	tool          string
	params        []string // the parameter names tool declares
	required      []string // the settings fields configSchema requires, secrets included
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
	return s.reg.ready(ctx, c)
}

// Reviewer is the candidate named name, selected or not (Settings › Extensions
// tests the one whose settings it shows).
func (s *Source) Reviewer(ctx context.Context, name string) (command_review_svc.Reviewer, error) {
	for _, c := range s.reg.snapshot() {
		if c.name == name {
			return s.reg.ready(ctx, c)
		}
	}
	return nil, fmt.Errorf("%w: %q contributes no command reviewer", command_review_svc.ErrReviewerUnavailable, name)
}

// State is what the settings page and the permission mode card show: every
// candidate (display names in lang), the selection if it is a candidate, and the
// reviewer the rules pick when it can review here — else the problem, named as
// the review failure it causes. A picked reviewer whose settings are not filled
// in stays the active one, with problem not_configured, so the page can point
// at its settings.
func (s *Source) State(ctx context.Context, lang string) command_review_svc.State {
	cands := s.reg.snapshot()
	// It crosses IPC to the frontend: an empty list must be [] there, not null.
	st := command_review_svc.State{Reviewers: make([]command_review_svc.ReviewerInfo, 0, len(cands))}
	sel := s.selected()
	usable := make(map[string]bool, len(cands))
	for _, c := range cands {
		info := command_review_svc.ReviewerInfo{
			Name: c.name, DisplayName: c.translate(lang, c.displayName), Version: c.version, Usable: c.running,
			Questions: c.questions,
		}
		if !c.running {
			info.Reason = UnusableNotRunning
		} else if e, err := s.reg.checkEligible(ctx, c.name); err != nil {
			info.Usable, info.Reason = false, unusableReason(err)
		} else {
			info.Signed = e.signed
		}
		if c.name == sel {
			st.Selected = sel
		}
		usable[c.name] = info.Usable
		st.Reviewers = append(st.Reviewers, info)
	}
	c, err := s.pick(cands)
	switch {
	case err != nil:
		st.Problem = aictx.ReviewReasonNoReviewer
	case !c.running || !usable[c.name]:
		st.Problem = aictx.ReviewReasonReviewerUnavailable
	default:
		switch err := s.reg.checkConfigured(ctx, c); {
		case errors.Is(err, command_review_svc.ErrNotConfigured):
			st.Active, st.Problem = c.name, aictx.ReviewReasonNotConfigured
		case err != nil:
			st.Problem = aictx.ReviewReasonReviewerUnavailable
		default:
			st.Active = c.name
		}
	}
	return st
}

func errNotRunning(c *candidate) error {
	return fmt.Errorf("%w: extension %s is not running in this process", command_review_svc.ErrReviewerUnavailable, c.name)
}

// ready is c eligible and running here, ready to review. An ineligible candidate is
// refused before anything of it runs; a describe-only one is activated first when
// this process can (opsctl) and checked again once activated, so the package that
// runs — and whose digest keys the review cache — is the one that was checked.
func (r *Registry) ready(ctx context.Context, c *candidate) (command_review_svc.Reviewer, error) {
	refuse := func(err error) error {
		return fmt.Errorf("%w: %s: %w", command_review_svc.ErrReviewerUnavailable, c.name, err)
	}
	checked, err := r.checkEligible(ctx, c.name)
	if err != nil {
		return nil, refuse(err)
	}
	if err := r.checkConfigured(ctx, c); err != nil {
		return nil, err
	}
	if !c.running {
		r.mu.Lock()
		activate := r.activate
		r.mu.Unlock()
		if activate == nil {
			return nil, errNotRunning(c)
		}
		if err := activate(ctx, c.name); err != nil {
			return nil, refuse(err)
		}
		c = r.running(c.name)
		if c == nil {
			return nil, refuse(errors.New("activation did not bring the reviewer up"))
		}
		if checked, err = r.checkEligible(ctx, c.name); err != nil {
			return nil, refuse(err)
		}
	}
	return r.reviewer(ctx, c, checked.digest)
}

// checkConfigured is nil when c's saved settings give every field its settings
// require (secrets included) a value, and command_review_svc.ErrNotConfigured
// naming the empty ones when not — checked before anything of c runs. Settings
// that cannot be read leave c unavailable.
func (r *Registry) checkConfigured(ctx context.Context, c *candidate) error {
	if len(c.required) == 0 {
		return nil
	}
	r.mu.Lock()
	read := r.settings
	r.mu.Unlock()
	if read == nil {
		return fmt.Errorf("%w: %s: no settings reader is wired to check its required settings", command_review_svc.ErrReviewerUnavailable, c.name)
	}
	view, err := read(ctx, c.name)
	if err != nil {
		return fmt.Errorf("%w: %s: read settings: %w", command_review_svc.ErrReviewerUnavailable, c.name, err)
	}
	var missing []string
	for _, f := range c.required {
		if set, secret := view.Secrets[f]; secret {
			if !set {
				missing = append(missing, f)
			}
		} else if _, ok := view.Values[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s: required settings %s have no value", command_review_svc.ErrNotConfigured, c.name, strings.Join(missing, ", "))
	}
	return nil
}

// running is the candidate named name if it is attached with a running guest.
func (r *Registry) running(name string) *candidate {
	for _, c := range r.snapshot() {
		if c.name == name && c.running {
			return c
		}
	}
	return nil
}

// reviewer is c ready to review: running here, its package digest the one just
// checked, its settings hash read now.
func (r *Registry) reviewer(ctx context.Context, c *candidate, digest string) (command_review_svc.Reviewer, error) {
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
	return &reviewer{c: c, digest: digest, settingsHash: settingsHash, call: r.call}, nil
}

// reviewer is a candidate's command_review_svc.Reviewer.
type reviewer struct {
	c            *candidate
	digest       string
	settingsHash string
	call         HostToolCaller
}

func (v *reviewer) Identity() command_review_svc.Identity {
	return command_review_svc.Identity{Name: v.c.name, Version: v.c.version, PackageDigest: v.digest, SettingsHash: v.settingsHash}
}

func (v *reviewer) Questions() []command_review_svc.Question { return v.c.questions }

func (v *reviewer) MaxCommandLen() int { return v.c.maxCommandLen }

// Review is one call of the candidate's host-only tool, bounded by ctx's deadline
// (the review service's per-call timeout).
func (v *reviewer) Review(ctx context.Context, assetType, command, syntax string) (command_review_svc.Answer, error) {
	args, err := v.args(extwire.ReviewItem{AssetType: assetType, Command: command, Syntax: syntax})
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

// args is item's fields the tool declares: the host refuses an argument the tool
// does not declare, so a field the host adds later reaches only tools that
// declare it, and a tool's own optional parameters the host does not know are
// never sent.
func (v *reviewer) args(item extwire.ReviewItem) (json.RawMessage, error) {
	data, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(data, &known); err != nil {
		return nil, err
	}
	args := make(map[string]json.RawMessage, len(known))
	for name, value := range known {
		if slices.Contains(v.c.params, name) {
			args[name] = value
		}
	}
	return json.Marshal(args)
}
