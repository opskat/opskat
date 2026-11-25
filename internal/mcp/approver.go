package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/sshagent"
)

// External approval decision values accepted over the out-of-band control socket.
const (
	DecisionDeny           = "deny"
	DecisionAllow          = "allow"
	DecisionAllowSession   = "allow_session"
	DecisionAllowPermanent = "allow_permanent"
)

// Pause / task lifecycle statuses returned in structuredContent.
const (
	StatusNeedsApproval = "needs_approval"
	StatusNeedsMFA      = "needs_mfa"
	StatusRunning       = "running"
	StatusCompleted     = "completed"
)

// Security and resource bounds for external approval coordination.
const (
	MinApprovalSecretLen = 16
	MaxApprovalTTL       = 2 * time.Hour

	defaultApprovalTTL   = 30 * time.Minute
	defaultTaskRetention = 30 * time.Minute
	defaultResolveWait   = 45 * time.Second
	maxResolveWait       = 55 * time.Second
	defaultTaskPollWait  = 15 * time.Second
	maxTaskPollWait      = 60 * time.Second
	maxRetainedTasks     = 256
	defaultMaxPending    = 32
	authFailureBackoff   = 150 * time.Millisecond
)

// ApprovalItemView is the client-agnostic representation of one item needing approval.
type ApprovalItemView struct {
	Type            string            `json:"type"`
	AssetID         int64             `json:"asset_id,omitempty"`
	AssetName       string            `json:"asset_name,omitempty"`
	GroupID         int64             `json:"group_id,omitempty"`
	GroupName       string            `json:"group_name,omitempty"`
	Command         string            `json:"command,omitempty"`
	Detail          string            `json:"detail,omitempty"`
	LocalPaths      []string          `json:"local_paths,omitempty"`
	Subjects        []string          `json:"subjects,omitempty"`
	Action          string            `json:"action,omitempty"`
	Resource        string            `json:"resource,omitempty"`
	Resources       []string          `json:"resources,omitempty"`
	RememberPattern string            `json:"remember_pattern,omitempty"`
	Review          *aictx.ReviewInfo `json:"review,omitempty"`
}

// LocalWritePrompt describes a local filesystem write needing confirmation via ExternalApprover.
type LocalWritePrompt struct {
	ToolName        string
	Command         string
	Detail          string
	Paths           []string
	DefaultPatterns []string
}

// MFAChallengeView is the client-agnostic representation of an SSH keyboard-interactive prompt.
type MFAChallengeView struct {
	AssetID     int64    `json:"asset_id,omitempty"`
	AssetName   string   `json:"asset_name,omitempty"`
	Name        string   `json:"name,omitempty"`
	Instruction string   `json:"instruction,omitempty"`
	Prompts     []string `json:"prompts"`
	Echo        []bool   `json:"echo"`
}

// PauseRef is the opaque handle returned on the model-facing MCP data plane when
// an operation pauses for human approval or MFA. Full approval details, policy subjects,
// model-review rules, and MFA prompts are NEVER returned on the MCP data plane;
// trusted external hosts retrieve them over the out-of-band control socket via get_pending.
type PauseRef struct {
	Status     string `json:"status"`
	ApprovalID string `json:"approval_id"`
	TaskID     string `json:"task_id"`
	ExpiresAt  string `json:"expires_at"`
}

// PausePayload is the full approval or MFA challenge payload served ONLY over the
// out-of-band control socket (get_pending / list_pending / resolve).
type PausePayload struct {
	Status           string             `json:"status"`
	ApprovalID       string             `json:"approval_id"`
	TaskID           string             `json:"task_id"`
	Kind             string             `json:"kind"`
	ToolName         string             `json:"tool_name"`
	Reason           string             `json:"reason,omitempty"`
	Items            []ApprovalItemView `json:"items,omitempty"`
	MFA              *MFAChallengeView  `json:"mfa,omitempty"`
	AllowedDecisions []string           `json:"allowed_decisions"`
	ExpiresAt        string             `json:"expires_at"`
}

// Ref returns the opaque data-plane PauseRef for p without exposing approval items,
// policy subjects, review findings, allowed decisions, or MFA prompts.
func (p PausePayload) Ref() PauseRef {
	return PauseRef{
		Status:     p.Status,
		ApprovalID: p.ApprovalID,
		TaskID:     p.TaskID,
		ExpiresAt:  p.ExpiresAt,
	}
}

// TaskResultPayload is returned by out-of-band control-socket resolve (with Pause)
// and by MCP data-plane get_task (with PauseRef only, never full Pause).
type TaskResultPayload struct {
	Status   string        `json:"status"`
	TaskID   string        `json:"task_id"`
	Output   string        `json:"output,omitempty"`
	IsError  bool          `json:"is_error,omitempty"`
	Pause    *PausePayload `json:"pause,omitempty"`     // populated ONLY on out-of-band control socket responses
	PauseRef *PauseRef     `json:"pause_ref,omitempty"` // populated on MCP data-plane get_task responses
}

// ResolveRequest is the structured input for ExternalApprover.Resolve.
type ResolveRequest struct {
	ApprovalID  string                    `json:"approval_id"`
	Secret      string                    `json:"secret"`
	Decision    string                    `json:"decision"`
	EditedItems []permission.ApprovalItem `json:"edited_items,omitempty"`
	MFAAnswers  []string                  `json:"mfa_answers,omitempty"`
	WaitMs      int                       `json:"wait_ms,omitempty"`
}

// ResolveAuditEvent records every out-of-band approval resolution attempt (never containing secrets or MFA answers).
type ResolveAuditEvent struct {
	ApprovalID  string
	TaskID      string
	Kind        string
	Decision    string
	EditedCount int
	Success     bool
	Error       string
}

// PermanentRuleWriter persists permanent allow rules when the human chooses allow_permanent.
// It must fail-closed if any rule is invalid or shadowed by a deny rule.
type PermanentRuleWriter func(ctx context.Context, items []permission.ApprovalItem, edited []permission.ApprovalItem) error

// GrantPersister persists approved grant items for request_permission and returns the
// final granted patterns recorded in the session.
type GrantPersister func(ctx context.Context, items []permission.ApprovalItem, edited []permission.ApprovalItem) ([]string, error)

// AssetNameLookup resolves an asset display name for MFA challenge views.
type AssetNameLookup func(ctx context.Context, assetID int64) string

// SubjectsResolver computes the normalized rule subjects for an approval item.
type SubjectsResolver func(ctx context.Context, item permission.ApprovalItem) []string

// ExternalApproverConfig configures an ExternalApprover.
type ExternalApproverConfig struct {
	Secret              string
	TTL                 time.Duration
	TaskRetention       time.Duration
	MaxPending          int
	AllowPermanent      bool // must be explicitly opted into via --allow-permanent
	PermanentRuleWriter PermanentRuleWriter
	GrantPersister      GrantPersister
	AssetNameLookup     AssetNameLookup
	SubjectsResolver    SubjectsResolver
	OnResolveAudit      func(ctx context.Context, ev ResolveAuditEvent)
	AuthFailureBackoff  time.Duration // optional override for tests; defaults to authFailureBackoff
}

// ExternalApprover coordinates non-blocking pause/resume approval and MFA flows
// for MCP tool calls without coupling OpsKat to any specific client UI.
type ExternalApprover struct {
	secret              []byte
	ttl                 time.Duration
	taskRetention       time.Duration
	maxPending          int
	allowPermanent      bool
	permanentRuleWriter PermanentRuleWriter
	grantPersister      GrantPersister
	assetNameLookup     AssetNameLookup
	subjectsResolver    SubjectsResolver
	onResolveAudit      func(ctx context.Context, ev ResolveAuditEvent)
	authBackoff         time.Duration

	mu      sync.Mutex
	pending map[string]*pendingPause
	tasks   map[string]*taskRun
	closed  bool
}

type resolveAnswer struct {
	decision    string
	editedItems []permission.ApprovalItem
	mfaAnswers  []string
}

type pendingPause struct {
	payload   PausePayload
	rawItems  []permission.ApprovalItem
	run       *taskRun
	respCh    chan resolveAnswer
	expiresAt time.Time
	resolving bool
}

type runOutcome struct {
	output  string
	isError bool
}

type taskRun struct {
	id         string
	toolName   string
	cancel     context.CancelFunc
	mu         sync.Mutex
	completed  bool
	outcome    runOutcome
	finishedAt time.Time
	active     *pendingPause
	changed    chan struct{}
}

func newTaskRun(id, toolName string, cancel context.CancelFunc) *taskRun {
	return &taskRun{
		id:       id,
		toolName: toolName,
		cancel:   cancel,
		changed:  make(chan struct{}),
	}
}

// signalLocked wakes every waiter observing state transitions on r. Caller must hold r.mu.
func (r *taskRun) signalLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}

type activeRunKey struct{}

func withTaskRun(ctx context.Context, run *taskRun) context.Context {
	return context.WithValue(ctx, activeRunKey{}, run)
}

func taskRunFrom(ctx context.Context) *taskRun {
	run, _ := ctx.Value(activeRunKey{}).(*taskRun)
	return run
}

// NewExternalApprover constructs an ExternalApprover. Secret must be at least MinApprovalSecretLen bytes.
func NewExternalApprover(cfg ExternalApproverConfig) (*ExternalApprover, error) {
	trimmed := strings.TrimSpace(cfg.Secret)
	if len(trimmed) < MinApprovalSecretLen {
		return nil, fmt.Errorf("external approval mode requires an approval secret of at least %d characters", MinApprovalSecretLen)
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultApprovalTTL
	}
	if ttl > MaxApprovalTTL {
		return nil, fmt.Errorf("--approval-ttl %s exceeds maximum allowed %s", ttl, MaxApprovalTTL)
	}
	retention := cfg.TaskRetention
	if retention <= 0 {
		retention = defaultTaskRetention
	}
	maxPending := cfg.MaxPending
	if maxPending <= 0 {
		maxPending = defaultMaxPending
	}
	backoff := cfg.AuthFailureBackoff
	if backoff == 0 {
		backoff = authFailureBackoff
	} else if backoff < 0 {
		backoff = 0
	}
	resolver := cfg.SubjectsResolver
	if resolver == nil {
		resolver = func(ctx context.Context, item permission.ApprovalItem) []string {
			return permission.ApprovalRuleSubjects(ctx, item.Type, item)
		}
	}
	return &ExternalApprover{
		secret:              []byte(trimmed),
		ttl:                 ttl,
		taskRetention:       retention,
		maxPending:          maxPending,
		allowPermanent:      cfg.AllowPermanent,
		permanentRuleWriter: cfg.PermanentRuleWriter,
		grantPersister:      cfg.GrantPersister,
		assetNameLookup:     cfg.AssetNameLookup,
		subjectsResolver:    resolver,
		onResolveAudit:      cfg.OnResolveAudit,
		authBackoff:         backoff,
		pending:             make(map[string]*pendingPause),
		tasks:               make(map[string]*taskRun),
	}, nil
}

// Close cancels every active paused/running task in fail-closed fashion.
func (a *ExternalApprover) Close() {
	a.mu.Lock()
	a.closed = true
	pauses := make([]*pendingPause, 0, len(a.pending))
	for id, p := range a.pending {
		pauses = append(pauses, p)
		delete(a.pending, id)
	}
	runs := make([]*taskRun, 0, len(a.tasks))
	for _, r := range a.tasks {
		runs = append(runs, r)
	}
	a.mu.Unlock()

	for _, p := range pauses {
		select {
		case p.respCh <- resolveAnswer{decision: DecisionDeny}:
		default:
		}
	}
	for _, r := range runs {
		r.cancel()
	}
}

// Run executes fn in a managed goroutine. If fn finishes without needing approval,
// its output is returned directly. If fn pauses for approval or MFA, Run returns
// immediately with an opaque PauseRef handle while the goroutine stays parked.
func (a *ExternalApprover) Run(parentCtx context.Context, reqCtx context.Context, toolName string, fn func(ctx context.Context) (string, bool)) *CallToolResult {
	runCtx, cancel := context.WithCancel(parentCtx)
	run := newTaskRun("tsk_"+uuid.NewString(), toolName, cancel)

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		cancel()
		return textResult("MCP server is shutting down", true)
	}
	a.pruneExpiredTasksLocked(time.Now())
	a.tasks[run.id] = run
	a.mu.Unlock()

	go func() {
		defer cancel()
		var out string
		var isErr bool
		defer func() {
			if r := recover(); r != nil {
				logger.Ctx(runCtx).Error("mcp tool goroutine panic recovered",
					zap.String("toolName", toolName),
					zap.String("taskID", run.id),
					zap.Any("panic", r),
					zap.Stack("stack"))
				out = fmt.Sprintf("internal error: panic in tool %s", toolName)
				isErr = true
			}
			run.mu.Lock()
			run.completed = true
			run.outcome = runOutcome{output: out, isError: isErr}
			run.finishedAt = time.Now()
			run.active = nil
			run.signalLocked()
			run.mu.Unlock()
		}()

		out, isErr = fn(withTaskRun(runCtx, run))
	}()

	for {
		run.mu.Lock()
		if run.completed {
			outcome := run.outcome
			run.mu.Unlock()
			a.mu.Lock()
			delete(a.tasks, run.id)
			a.mu.Unlock()
			return textResult(outcome.output, outcome.isError)
		}
		if run.active != nil {
			payload := run.active.payload
			run.mu.Unlock()
			return pauseToolResult(payload)
		}
		ch := run.changed
		run.mu.Unlock()

		select {
		case <-ch:
		case <-reqCtx.Done():
			run.cancel()
			return textResult("request canceled", true)
		}
	}
}

// ConfirmFunc implements permission.CommandConfirmFunc for ExternalApprover.
func (a *ExternalApprover) ConfirmFunc(ctx context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
	run := taskRunFrom(ctx)
	if run == nil {
		logger.Ctx(ctx).Warn("external approver invoked without active task run; denying")
		return permission.ApprovalResponse{Decision: "deny"}
	}

	views := make([]ApprovalItemView, 0, len(items))
	hasSubjects := false
	for _, item := range items {
		subjects := a.subjectsResolver(ctx, item)
		if len(subjects) > 0 {
			hasSubjects = true
		}
		views = append(views, ApprovalItemView{
			Type:            item.Type,
			AssetID:         item.AssetID,
			AssetName:       item.AssetName,
			GroupID:         item.GroupID,
			GroupName:       item.GroupName,
			Command:         item.Command,
			Detail:          item.Detail,
			LocalPaths:      extractLocalPathsFromDetail(item.Detail),
			Subjects:        subjects,
			Action:          item.Action,
			Resource:        item.Resource,
			Resources:       item.Resources,
			RememberPattern: item.RememberPattern,
			Review:          item.Review,
		})
	}

	allowed := []string{DecisionDeny, DecisionAllow}
	if kind == permission.ApprovalKindSingle && hasSubjects {
		allowed = append(allowed, DecisionAllowSession)
		if a.allowPermanent && a.permanentRuleWriter != nil {
			allowed = append(allowed, DecisionAllowPermanent)
		}
	}

	ans, ok := a.parkAndWait(ctx, run, PausePayload{
		Status:           StatusNeedsApproval,
		Kind:             kind,
		ToolName:         run.toolName,
		Items:            views,
		AllowedDecisions: allowed,
	}, items)
	if !ok {
		return permission.ApprovalResponse{Decision: "deny"}
	}

	switch ans.decision {
	case DecisionAllow:
		return permission.ApprovalResponse{Decision: "allow"}
	case DecisionAllowSession:
		return permission.ApprovalResponse{Decision: "allowAll", EditedItems: ans.editedItems}
	case DecisionAllowPermanent:
		// Permanent rule has already been validated & persisted in Resolve before unblocking.
		// Never attach EditedItems to an "allow" response: ParseApprovalResponse rejects edited_items on single/allow.
		return permission.ApprovalResponse{Decision: "allow"}
	default:
		return permission.ApprovalResponse{Decision: "deny"}
	}
}

// ConfirmLocalWrite pauses for human confirmation of a local filesystem write via LocalToolGate.
func (a *ExternalApprover) ConfirmLocalWrite(ctx context.Context, req LocalWritePrompt) permission.ApprovalResponse {
	run := taskRunFrom(ctx)
	if run == nil {
		logger.Ctx(ctx).Warn("external local write approver invoked without active task run; denying")
		return permission.ApprovalResponse{Decision: "deny"}
	}

	rawItems := []permission.ApprovalItem{{
		Type:    req.ToolName,
		Command: req.Command,
		Detail:  req.Detail,
	}}
	views := []ApprovalItemView{{
		Type:       req.ToolName,
		Command:    req.Command,
		Detail:     req.Detail,
		LocalPaths: slices.Clone(req.Paths),
		Subjects:   slices.Clone(req.DefaultPatterns),
	}}

	ans, ok := a.parkAndWait(ctx, run, PausePayload{
		Status:           StatusNeedsApproval,
		Kind:             permission.ApprovalKindLocalTool,
		ToolName:         run.toolName,
		Items:            views,
		AllowedDecisions: []string{DecisionDeny, DecisionAllow, DecisionAllowSession},
	}, rawItems)
	if !ok {
		return permission.ApprovalResponse{Decision: "deny"}
	}

	switch ans.decision {
	case DecisionAllow:
		return permission.ApprovalResponse{Decision: "allow"}
	case DecisionAllowSession:
		return permission.ApprovalResponse{Decision: "allowAll", EditedItems: ans.editedItems}
	default:
		return permission.ApprovalResponse{Decision: "deny"}
	}
}

// GrantRequestFunc implements permission.GrantRequestFunc for ExternalApprover.
func (a *ExternalApprover) GrantRequestFunc(ctx context.Context, items []permission.ApprovalItem, reason string) (bool, []string) {
	run := taskRunFrom(ctx)
	if run == nil {
		logger.Ctx(ctx).Warn("external grant approver invoked without active task run; denying")
		return false, nil
	}

	views := make([]ApprovalItemView, 0, len(items))
	for _, item := range items {
		views = append(views, ApprovalItemView{
			Type:      item.Type,
			AssetID:   item.AssetID,
			AssetName: item.AssetName,
			GroupID:   item.GroupID,
			GroupName: item.GroupName,
			Command:   item.Command,
			Detail:    item.Detail,
		})
	}

	ans, ok := a.parkAndWait(ctx, run, PausePayload{
		Status:           StatusNeedsApproval,
		Kind:             permission.ApprovalKindGrant,
		ToolName:         run.toolName,
		Reason:           reason,
		Items:            views,
		AllowedDecisions: []string{DecisionDeny, DecisionAllow},
	}, items)
	if !ok || ans.decision != DecisionAllow {
		return false, nil
	}
	if a.grantPersister == nil {
		logger.Ctx(ctx).Error("grant approved but no GrantPersister wired; denying")
		return false, nil
	}
	// ans.editedItems was already validated and scope-locked by permission.ParseApprovalResponse in Resolve.
	patterns, err := a.grantPersister(ctx, items, ans.editedItems)
	if err != nil {
		logger.Ctx(ctx).Error("failed to persist approved grant", zap.Error(err))
		return false, nil
	}
	return true, patterns
}

// NewMFACaller returns an sshagent.InteractiveCaller that pauses the active task
// for an external MFA challenge response.
func (a *ExternalApprover) NewMFACaller(assetID int64) sshagent.InteractiveCaller {
	return &externalMFACaller{approver: a, assetID: assetID}
}

type externalMFACaller struct {
	approver *ExternalApprover
	assetID  int64
}

func (c *externalMFACaller) SubmitChallenge(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	run := taskRunFrom(ctx)
	if run == nil {
		return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "mfa canceled"}
	}
	assetName := ""
	if c.approver.assetNameLookup != nil && c.assetID > 0 {
		assetName = c.approver.assetNameLookup(ctx, c.assetID)
	}
	ans, ok := c.approver.parkAndWait(ctx, run, PausePayload{
		Status:   StatusNeedsMFA,
		Kind:     "mfa",
		ToolName: run.toolName,
		MFA: &MFAChallengeView{
			AssetID:     c.assetID,
			AssetName:   assetName,
			Name:        ch.Name,
			Instruction: ch.Instruction,
			Prompts:     slices.Clone(ch.Prompts),
			Echo:        slices.Clone(ch.Echo),
		},
		AllowedDecisions: []string{DecisionDeny, DecisionAllow},
	}, nil)
	if !ok || ans.decision != DecisionAllow {
		return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "mfa canceled"}
	}
	if len(ans.mfaAnswers) != len(ch.Prompts) {
		return nil, fmt.Errorf("mfa answer count mismatch: got %d, want %d", len(ans.mfaAnswers), len(ch.Prompts))
	}
	return ans.mfaAnswers, nil
}

func (a *ExternalApprover) parkAndWait(ctx context.Context, run *taskRun, payload PausePayload, rawItems []permission.ApprovalItem) (resolveAnswer, bool) {
	now := time.Now()
	expiresAt := now.Add(a.ttl)
	approvalID := "apr_" + uuid.NewString()
	payload.ApprovalID = approvalID
	payload.TaskID = run.id
	payload.ExpiresAt = expiresAt.Format(time.RFC3339)

	p := &pendingPause{
		payload:   payload,
		rawItems:  rawItems,
		run:       run,
		respCh:    make(chan resolveAnswer, 1),
		expiresAt: expiresAt,
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return resolveAnswer{decision: DecisionDeny}, false
	}
	if len(a.pending) >= a.maxPending {
		a.mu.Unlock()
		logger.Ctx(ctx).Warn("mcp external approval rejected: max pending limit reached",
			zap.Int("maxPending", a.maxPending),
			zap.String("taskID", run.id))
		return resolveAnswer{decision: DecisionDeny}, false
	}
	a.pending[approvalID] = p
	a.mu.Unlock()

	run.mu.Lock()
	run.active = p
	run.signalLocked()
	run.mu.Unlock()

	logger.Ctx(ctx).Info("mcp external approval paused",
		zap.String("approvalID", approvalID),
		zap.String("taskID", run.id),
		zap.String("kind", payload.Kind),
		zap.String("toolName", payload.ToolName))

	timer := time.NewTimer(a.ttl)
	defer timer.Stop()

	select {
	case ans := <-p.respCh:
		logger.Ctx(ctx).Info("mcp external approval resolved",
			zap.String("approvalID", approvalID),
			zap.String("taskID", run.id),
			zap.String("decision", ans.decision))
		return ans, true
	case <-timer.C:
		a.removePending(approvalID, run)
		logger.Ctx(ctx).Warn("mcp external approval expired",
			zap.String("approvalID", approvalID),
			zap.String("taskID", run.id))
		return resolveAnswer{decision: DecisionDeny}, false
	case <-ctx.Done():
		a.removePending(approvalID, run)
		logger.Ctx(ctx).Info("mcp external approval canceled",
			zap.String("approvalID", approvalID),
			zap.String("taskID", run.id))
		return resolveAnswer{decision: DecisionDeny}, false
	}
}

func (a *ExternalApprover) removePending(approvalID string, run *taskRun) {
	a.mu.Lock()
	delete(a.pending, approvalID)
	a.mu.Unlock()

	run.mu.Lock()
	if run.active != nil && run.active.payload.ApprovalID == approvalID {
		run.active = nil
		run.signalLocked()
	}
	run.mu.Unlock()
}

// ListPending returns all non-expired pending approval payloads after verifying secret.
func (a *ExternalApprover) ListPending(ctx context.Context, secret string) ([]PausePayload, error) {
	if !a.verifySecret(ctx, secret, "") {
		return nil, errors.New("unauthorized: invalid approval secret")
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]PausePayload, 0, len(a.pending))
	for _, p := range a.pending {
		if now.After(p.expiresAt) || p.resolving {
			continue
		}
		out = append(out, p.payload)
	}
	return out, nil
}

// GetPending returns the full PausePayload for a single non-expired pending approval
// after verifying the out-of-band shared secret.
func (a *ExternalApprover) GetPending(ctx context.Context, secret, approvalID string) (*PausePayload, error) {
	if !a.verifySecret(ctx, secret, approvalID) {
		return nil, errors.New("unauthorized: invalid approval secret")
	}
	if strings.TrimSpace(approvalID) == "" {
		return nil, errors.New("missing required parameter: approval_id")
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.pending[approvalID]
	if !ok {
		return nil, fmt.Errorf("approval %q not found or expired", approvalID)
	}
	if now.After(p.expiresAt) {
		delete(a.pending, approvalID)
		return nil, fmt.Errorf("approval %q expired", approvalID)
	}
	if p.resolving {
		return nil, fmt.Errorf("approval %q is already being resolved", approvalID)
	}
	cp := p.payload
	if p.payload.Items != nil {
		cp.Items = slices.Clone(p.payload.Items)
	}
	if p.payload.AllowedDecisions != nil {
		cp.AllowedDecisions = slices.Clone(p.payload.AllowedDecisions)
	}
	return &cp, nil
}

// Resolve submits a human approval or MFA decision for a pending pause, verifies the
// shared secret, validates edited_items through permission.ParseApprovalResponse,
// optionally writes a permanent allow rule before unblocking, and waits up to wait_ms
// for the task to finish or hit a subsequent pause.
func (a *ExternalApprover) Resolve(ctx context.Context, req ResolveRequest) (*TaskResultPayload, error) {
	if !a.verifySecret(ctx, req.Secret, req.ApprovalID) {
		a.emitAudit(ctx, ResolveAuditEvent{
			ApprovalID: req.ApprovalID,
			Decision:   req.Decision,
			Success:    false,
			Error:      "unauthorized: invalid approval secret",
		})
		return nil, errors.New("unauthorized: invalid approval secret")
	}
	if strings.TrimSpace(req.ApprovalID) == "" {
		return nil, errors.New("missing required parameter: approval_id")
	}

	a.mu.Lock()
	p, ok := a.pending[req.ApprovalID]
	if !ok {
		a.mu.Unlock()
		err := fmt.Errorf("approval %q not found or expired", req.ApprovalID)
		a.emitAudit(ctx, ResolveAuditEvent{ApprovalID: req.ApprovalID, Decision: req.Decision, Success: false, Error: err.Error()})
		return nil, err
	}
	if time.Now().After(p.expiresAt) {
		delete(a.pending, req.ApprovalID)
		a.mu.Unlock()
		err := fmt.Errorf("approval %q expired", req.ApprovalID)
		a.emitAudit(ctx, ResolveAuditEvent{ApprovalID: req.ApprovalID, TaskID: p.run.id, Kind: p.payload.Kind, Decision: req.Decision, Success: false, Error: err.Error()})
		return nil, err
	}
	if p.resolving {
		a.mu.Unlock()
		return nil, fmt.Errorf("approval %q is already being resolved", req.ApprovalID)
	}
	if !slices.Contains(p.payload.AllowedDecisions, req.Decision) {
		a.mu.Unlock()
		err := fmt.Errorf("decision %q is not allowed for approval %q (allowed: %s)",
			req.Decision, req.ApprovalID, strings.Join(p.payload.AllowedDecisions, ", "))
		a.emitAudit(ctx, ResolveAuditEvent{ApprovalID: req.ApprovalID, TaskID: p.run.id, Kind: p.payload.Kind, Decision: req.Decision, Success: false, Error: err.Error()})
		return nil, err
	}
	validatedEdited, valErr := validateResolveDecision(p, req)
	if valErr != nil {
		a.mu.Unlock()
		a.emitAudit(ctx, ResolveAuditEvent{
			ApprovalID:  req.ApprovalID,
			TaskID:      p.run.id,
			Kind:        p.payload.Kind,
			Decision:    req.Decision,
			EditedCount: len(req.EditedItems),
			Success:     false,
			Error:       valErr.Error(),
		})
		return nil, valErr
	}
	p.resolving = true
	a.mu.Unlock()

	// Write permanent rule before releasing execution; if writing fails (e.g. shadowed by deny),
	// keep the approval pending so the user sees the exact reason and can choose another option.
	if req.Decision == DecisionAllowPermanent {
		if !a.allowPermanent || a.permanentRuleWriter == nil {
			a.clearResolving(req.ApprovalID)
			err := errors.New("permanent allow rule writer is not enabled")
			a.emitAudit(ctx, ResolveAuditEvent{ApprovalID: req.ApprovalID, TaskID: p.run.id, Kind: p.payload.Kind, Decision: req.Decision, Success: false, Error: err.Error()})
			return nil, err
		}
		if err := a.permanentRuleWriter(ctx, p.rawItems, validatedEdited); err != nil {
			a.clearResolving(req.ApprovalID)
			logger.Ctx(ctx).Warn("mcp permanent allow rule write failed",
				zap.String("approvalID", req.ApprovalID), zap.Error(err))
			wrapped := fmt.Errorf("failed to write permanent allow rule: %w", err)
			a.emitAudit(ctx, ResolveAuditEvent{ApprovalID: req.ApprovalID, TaskID: p.run.id, Kind: p.payload.Kind, Decision: req.Decision, EditedCount: len(validatedEdited), Success: false, Error: wrapped.Error()})
			return nil, wrapped
		}
	}

	a.removePending(req.ApprovalID, p.run)
	a.emitAudit(ctx, ResolveAuditEvent{
		ApprovalID:  req.ApprovalID,
		TaskID:      p.run.id,
		Kind:        p.payload.Kind,
		Decision:    req.Decision,
		EditedCount: len(validatedEdited),
		Success:     true,
	})
	p.respCh <- resolveAnswer{
		decision:    req.Decision,
		editedItems: validatedEdited,
		mfaAnswers:  req.MFAAnswers,
	}

	wait := defaultResolveWait
	if req.WaitMs > 0 {
		wait = time.Duration(req.WaitMs) * time.Millisecond
		if wait > maxResolveWait {
			wait = maxResolveWait
		}
	}
	return a.waitForRun(ctx, p.run, wait), nil
}

func validateResolveDecision(p *pendingPause, req ResolveRequest) ([]permission.ApprovalItem, error) {
	if p.payload.Kind == "mfa" {
		if len(req.EditedItems) > 0 {
			return nil, errors.New("mfa approval does not accept edited_items")
		}
		if req.Decision == DecisionAllow {
			if p.payload.MFA == nil {
				return nil, errors.New("invalid mfa challenge state")
			}
			if len(req.MFAAnswers) != len(p.payload.MFA.Prompts) {
				return nil, fmt.Errorf("mfa_answers must match prompt count (%d)", len(p.payload.MFA.Prompts))
			}
		}
		return nil, nil
	}

	if req.Decision == DecisionDeny {
		if len(req.EditedItems) > 0 {
			return nil, errors.New("deny decision does not accept edited_items")
		}
		return nil, nil
	}

	var permDecision string
	parseKind := p.payload.Kind
	switch req.Decision {
	case DecisionAllow:
		permDecision = "allow"
	case DecisionAllowSession:
		permDecision = "allowAll"
	case DecisionAllowPermanent:
		// Validate any edited_items with the exact same scope/syntax rules as allowAll on single.
		permDecision = "allowAll"
		parseKind = permission.ApprovalKindSingle
	default:
		return nil, fmt.Errorf("unsupported decision %q", req.Decision)
	}

	parsed, err := permission.ParseApprovalResponse(parseKind, permission.ApprovalResponse{
		Decision:    permDecision,
		EditedItems: req.EditedItems,
	}, p.rawItems)
	if err != nil {
		return nil, err
	}
	return parsed.EditedItems, nil
}

func (a *ExternalApprover) verifySecret(ctx context.Context, provided, approvalID string) bool {
	if subtle.ConstantTimeCompare([]byte(provided), a.secret) == 1 {
		return true
	}
	logger.Ctx(ctx).Warn("mcp external approval rejected: invalid secret",
		zap.String("approvalID", approvalID))
	if a.authBackoff > 0 {
		t := time.NewTimer(a.authBackoff)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
		}
	}
	return false
}

func (a *ExternalApprover) emitAudit(ctx context.Context, ev ResolveAuditEvent) {
	if a.onResolveAudit != nil {
		a.onResolveAudit(ctx, ev)
	}
}

func (a *ExternalApprover) clearResolving(approvalID string) {
	a.mu.Lock()
	if p, ok := a.pending[approvalID]; ok {
		p.resolving = false
	}
	a.mu.Unlock()
}

// GetTask polls or waits for a previously paused task to finish or emit another pause.
// Because GetTask is exposed on the model-facing MCP data plane, any active pause is
// redacted to an opaque PauseRef (never exposing full PausePayload details).
func (a *ExternalApprover) GetTask(ctx context.Context, taskID string, waitMs int) (*TaskResultPayload, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("missing required parameter: task_id")
	}
	a.mu.Lock()
	run, ok := a.tasks[taskID]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("task %q not found or expired", taskID)
	}

	run.mu.Lock()
	if run.completed {
		outcome := run.outcome
		run.mu.Unlock()
		return &TaskResultPayload{
			Status:  StatusCompleted,
			TaskID:  run.id,
			Output:  outcome.output,
			IsError: outcome.isError,
		}, nil
	}
	if run.active != nil {
		ref := run.active.payload.Ref()
		run.mu.Unlock()
		return &TaskResultPayload{
			Status:   ref.Status,
			TaskID:   run.id,
			PauseRef: &ref,
		}, nil
	}
	run.mu.Unlock()

	wait := defaultTaskPollWait
	if waitMs > 0 {
		wait = time.Duration(waitMs) * time.Millisecond
		if wait > maxTaskPollWait {
			wait = maxTaskPollWait
		}
	}
	return toDataPlaneTaskResult(a.waitForRun(ctx, run, wait)), nil
}

func toDataPlaneTaskResult(res *TaskResultPayload) *TaskResultPayload {
	if res == nil || res.Pause == nil {
		return res
	}
	ref := res.Pause.Ref()
	return &TaskResultPayload{
		Status:   res.Status,
		TaskID:   res.TaskID,
		Output:   res.Output,
		IsError:  res.IsError,
		PauseRef: &ref,
	}
}

func (a *ExternalApprover) waitForRun(ctx context.Context, run *taskRun, wait time.Duration) *TaskResultPayload {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	for {
		run.mu.Lock()
		if run.completed {
			outcome := run.outcome
			run.mu.Unlock()
			return &TaskResultPayload{
				Status:  StatusCompleted,
				TaskID:  run.id,
				Output:  outcome.output,
				IsError: outcome.isError,
			}
		}
		if run.active != nil {
			payload := run.active.payload
			run.mu.Unlock()
			return &TaskResultPayload{
				Status: payload.Status,
				TaskID: run.id,
				Pause:  &payload,
			}
		}
		ch := run.changed
		run.mu.Unlock()

		select {
		case <-ch:
		case <-timer.C:
			return &TaskResultPayload{
				Status: StatusRunning,
				TaskID: run.id,
			}
		case <-ctx.Done():
			return &TaskResultPayload{
				Status: StatusRunning,
				TaskID: run.id,
			}
		}
	}
}

func (a *ExternalApprover) pruneExpiredTasksLocked(now time.Time) {
	for id, run := range a.tasks {
		run.mu.Lock()
		expired := run.completed && now.Sub(run.finishedAt) > a.taskRetention
		run.mu.Unlock()
		if expired {
			delete(a.tasks, id)
		}
	}
	if len(a.tasks) <= maxRetainedTasks {
		return
	}
	for id, run := range a.tasks {
		run.mu.Lock()
		done := run.completed
		run.mu.Unlock()
		if done {
			delete(a.tasks, id)
			if len(a.tasks) <= maxRetainedTasks {
				break
			}
		}
	}
}

// extractLocalPathsFromDetail extracts local endpoints from "cp <src> → <dst>" detail strings
// so external approval UIs can prominently display local paths alongside remote targets.
func extractLocalPathsFromDetail(detail string) []string {
	if !strings.HasPrefix(detail, "cp ") {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(detail, "cp "), " → ")
	if len(parts) != 2 {
		return nil
	}
	var locals []string
	for _, ep := range parts {
		ep = strings.TrimSpace(ep)
		if ep == "" {
			continue
		}
		if !isRemoteEndpointSyntax(ep) {
			locals = append(locals, ep)
		}
	}
	return locals
}

// isRemoteEndpointSyntax mirrors helper.ParseTransferEndpoint's distinction between
// "<asset>:/<path>" (or "<asset>:~...") and local absolute paths (including Windows drive letters).
func isRemoteEndpointSyntax(raw string) bool {
	idx := strings.IndexByte(raw, ':')
	if idx <= 0 {
		return false
	}
	// Single ASCII letter followed by ":" is a Windows drive path (e.g. C:\foo or C:/foo).
	if idx == 1 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) {
		return false
	}
	rest := raw[idx+1:]
	return strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "~")
}

func pauseToolResult(payload PausePayload) *CallToolResult {
	ref := payload.Ref()
	summary := fmt.Sprintf("Operation requires human approval (approval_id=%s, task_id=%s). Wait for the user to decide before continuing.",
		ref.ApprovalID, ref.TaskID)
	if ref.Status == StatusNeedsMFA {
		summary = fmt.Sprintf("Operation requires MFA verification (approval_id=%s, task_id=%s). Wait for the user to submit the one-time code before continuing.",
			ref.ApprovalID, ref.TaskID)
	}
	return structuredResult(summary, ref, false)
}
