package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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

// External approval decision values accepted by _approval_resolve.
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

const (
	defaultApprovalTTL     = 30 * time.Minute
	defaultTaskRetention   = 30 * time.Minute
	defaultResolveWait     = 45 * time.Second
	maxResolveWait         = 55 * time.Second
	defaultTaskPollWait    = 15 * time.Second
	maxTaskPollWait        = 60 * time.Second
	maxRetainedTasks       = 256
)

// ApprovalItemView is the client-agnostic representation of one item needing approval.
type ApprovalItemView struct {
	Type            string            `json:"type"`
	AssetID         int64             `json:"asset_id,omitempty"`
	AssetName       string            `json:"asset_name,omitempty"`
	Command         string            `json:"command,omitempty"`
	Detail          string            `json:"detail,omitempty"`
	Subjects        []string          `json:"subjects,omitempty"`
	Action          string            `json:"action,omitempty"`
	Resource        string            `json:"resource,omitempty"`
	Resources       []string          `json:"resources,omitempty"`
	RememberPattern string            `json:"remember_pattern,omitempty"`
	Review          *aictx.ReviewInfo `json:"review,omitempty"`
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

// PausePayload is emitted when a tool call pauses for human approval or MFA input.
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

// TaskResultPayload is returned by _approval_resolve and get_task.
type TaskResultPayload struct {
	Status  string        `json:"status"`
	TaskID  string        `json:"task_id"`
	Output  string        `json:"output,omitempty"`
	IsError bool          `json:"is_error,omitempty"`
	Pause   *PausePayload `json:"pause,omitempty"`
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
	PermanentRuleWriter PermanentRuleWriter
	GrantPersister      GrantPersister
	AssetNameLookup     AssetNameLookup
	SubjectsResolver    SubjectsResolver
}

// ExternalApprover coordinates non-blocking pause/resume approval and MFA flows
// for MCP tool calls without coupling OpsKat to any specific client UI.
type ExternalApprover struct {
	secret              []byte
	ttl                 time.Duration
	taskRetention       time.Duration
	permanentRuleWriter PermanentRuleWriter
	grantPersister      GrantPersister
	assetNameLookup     AssetNameLookup
	subjectsResolver    SubjectsResolver

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
	payload     PausePayload
	rawItems    []permission.ApprovalItem
	run         *taskRun
	respCh      chan resolveAnswer
	expiresAt   time.Time
	resolving   bool
}

type runOutcome struct {
	output  string
	isError bool
}

type taskRun struct {
	id         string
	toolName   string
	cancel     context.CancelFunc
	pauseCh    chan *pendingPause
	doneCh     chan struct{}
	mu         sync.Mutex
	completed  bool
	outcome    runOutcome
	finishedAt time.Time
	active     *pendingPause
}

type activeRunKey struct{}

func withTaskRun(ctx context.Context, run *taskRun) context.Context {
	return context.WithValue(ctx, activeRunKey{}, run)
}

func taskRunFrom(ctx context.Context) *taskRun {
	run, _ := ctx.Value(activeRunKey{}).(*taskRun)
	return run
}

// NewExternalApprover constructs an ExternalApprover. Secret must be non-empty.
func NewExternalApprover(cfg ExternalApproverConfig) (*ExternalApprover, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, errors.New("external approval mode requires a non-empty approval secret")
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultApprovalTTL
	}
	retention := cfg.TaskRetention
	if retention <= 0 {
		retention = defaultTaskRetention
	}
	resolver := cfg.SubjectsResolver
	if resolver == nil {
		resolver = func(ctx context.Context, item permission.ApprovalItem) []string {
			return permission.ApprovalRuleSubjects(ctx, item.Type, item)
		}
	}
	return &ExternalApprover{
		secret:              []byte(cfg.Secret),
		ttl:                 ttl,
		taskRetention:       retention,
		permanentRuleWriter: cfg.PermanentRuleWriter,
		grantPersister:      cfg.GrantPersister,
		assetNameLookup:     cfg.AssetNameLookup,
		subjectsResolver:    resolver,
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
// immediately with the structured PausePayload while the goroutine stays parked.
func (a *ExternalApprover) Run(parentCtx context.Context, reqCtx context.Context, toolName string, fn func(ctx context.Context) (string, bool)) *CallToolResult {
	runCtx, cancel := context.WithCancel(parentCtx)
	run := &taskRun{
		id:       "tsk_" + uuid.NewString(),
		toolName: toolName,
		cancel:   cancel,
		pauseCh:  make(chan *pendingPause, 1),
		doneCh:   make(chan struct{}),
	}

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
			run.mu.Unlock()
			close(run.doneCh)
		}()

		out, isErr = fn(withTaskRun(runCtx, run))
	}()

	select {
	case <-run.doneCh:
		run.mu.Lock()
		outcome := run.outcome
		run.mu.Unlock()
		a.mu.Lock()
		delete(a.tasks, run.id)
		a.mu.Unlock()
		return textResult(outcome.output, outcome.isError)
	case pause := <-run.pauseCh:
		return pauseToolResult(pause.payload)
	case <-reqCtx.Done():
		run.cancel()
		return textResult("request canceled", true)
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
			Command:         item.Command,
			Detail:          item.Detail,
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
		if a.permanentRuleWriter != nil {
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
		return permission.ApprovalResponse{Decision: "allow", EditedItems: ans.editedItems}
	case DecisionAllowSession:
		return permission.ApprovalResponse{Decision: "allowAll", EditedItems: ans.editedItems}
	case DecisionAllowPermanent:
		// Permanent rule has already been written and verified in Resolve before unblocking.
		return permission.ApprovalResponse{Decision: "allow", EditedItems: ans.editedItems}
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
	a.pending[approvalID] = p
	a.mu.Unlock()

	run.mu.Lock()
	run.active = p
	run.mu.Unlock()

	logger.Ctx(ctx).Info("mcp external approval paused",
		zap.String("approvalID", approvalID),
		zap.String("taskID", run.id),
		zap.String("kind", payload.Kind),
		zap.String("toolName", payload.ToolName))

	// Notify the waiting caller (Run or Resolve).
	select {
	case run.pauseCh <- p:
	case <-ctx.Done():
		a.removePending(approvalID, run)
		return resolveAnswer{decision: DecisionDeny}, false
	}

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
	}
	run.mu.Unlock()
}

// Resolve submits a human approval or MFA decision for a pending pause, verifies the
// shared secret, optionally writes a permanent allow rule before unblocking, and waits
// up to wait_ms for the task to finish or hit a subsequent pause.
func (a *ExternalApprover) Resolve(ctx context.Context, req ResolveRequest) (*TaskResultPayload, error) {
	if subtle.ConstantTimeCompare([]byte(req.Secret), a.secret) != 1 {
		logger.Ctx(ctx).Warn("mcp external approval resolve rejected: invalid secret",
			zap.String("approvalID", req.ApprovalID))
		return nil, errors.New("unauthorized: invalid approval secret")
	}
	if strings.TrimSpace(req.ApprovalID) == "" {
		return nil, errors.New("missing required parameter: approval_id")
	}

	a.mu.Lock()
	p, ok := a.pending[req.ApprovalID]
	if !ok {
		a.mu.Unlock()
		return nil, fmt.Errorf("approval %q not found or expired", req.ApprovalID)
	}
	if time.Now().After(p.expiresAt) {
		delete(a.pending, req.ApprovalID)
		a.mu.Unlock()
		return nil, fmt.Errorf("approval %q expired", req.ApprovalID)
	}
	if p.resolving {
		a.mu.Unlock()
		return nil, fmt.Errorf("approval %q is already being resolved", req.ApprovalID)
	}
	if !slices.Contains(p.payload.AllowedDecisions, req.Decision) {
		a.mu.Unlock()
		return nil, fmt.Errorf("decision %q is not allowed for approval %q (allowed: %s)",
			req.Decision, req.ApprovalID, strings.Join(p.payload.AllowedDecisions, ", "))
	}
	if p.payload.Kind == "mfa" && req.Decision == DecisionAllow {
		if p.payload.MFA == nil || len(req.MFAAnswers) != len(p.payload.MFA.Prompts) {
			a.mu.Unlock()
			return nil, fmt.Errorf("mfa_answers must match prompt count (%d)", len(p.payload.MFA.Prompts))
		}
	}
	p.resolving = true
	a.mu.Unlock()

	// Write permanent rule before releasing execution; if writing fails (e.g. shadowed by deny),
	// keep the approval pending so the user sees the exact reason and can choose another option.
	if req.Decision == DecisionAllowPermanent {
		if a.permanentRuleWriter == nil {
			a.clearResolving(req.ApprovalID)
			return nil, errors.New("permanent allow rule writer is not configured")
		}
		if err := a.permanentRuleWriter(ctx, p.rawItems, req.EditedItems); err != nil {
			a.clearResolving(req.ApprovalID)
			logger.Ctx(ctx).Warn("mcp permanent allow rule write failed",
				zap.String("approvalID", req.ApprovalID), zap.Error(err))
			return nil, fmt.Errorf("failed to write permanent allow rule: %w", err)
		}
	}

	a.removePending(req.ApprovalID, p.run)
	p.respCh <- resolveAnswer{
		decision:    req.Decision,
		editedItems: req.EditedItems,
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

func (a *ExternalApprover) clearResolving(approvalID string) {
	a.mu.Lock()
	if p, ok := a.pending[approvalID]; ok {
		p.resolving = false
	}
	a.mu.Unlock()
}

// GetTask polls or waits for a previously paused task to finish or emit another pause.
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
		payload := run.active.payload
		run.mu.Unlock()
		return &TaskResultPayload{
			Status: payload.Status,
			TaskID: run.id,
			Pause:  &payload,
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
	return a.waitForRun(ctx, run, wait), nil
}

func (a *ExternalApprover) waitForRun(ctx context.Context, run *taskRun, wait time.Duration) *TaskResultPayload {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-run.doneCh:
		run.mu.Lock()
		outcome := run.outcome
		run.mu.Unlock()
		return &TaskResultPayload{
			Status:  StatusCompleted,
			TaskID:  run.id,
			Output:  outcome.output,
			IsError: outcome.isError,
		}
	case nextPause := <-run.pauseCh:
		payload := nextPause.payload
		return &TaskResultPayload{
			Status: payload.Status,
			TaskID: run.id,
			Pause:  &payload,
		}
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

func pauseToolResult(payload PausePayload) *CallToolResult {
	summary := fmt.Sprintf("Operation requires user approval (approval_id=%s, kind=%s). Wait for the user to decide before continuing.",
		payload.ApprovalID, payload.Kind)
	if payload.Status == StatusNeedsMFA {
		summary = fmt.Sprintf("SSH server requires MFA verification (approval_id=%s). Wait for the user to submit the one-time code.",
			payload.ApprovalID)
	}
	if raw, err := json.Marshal(payload); err == nil {
		summary = summary + "\n" + string(raw)
	}
	return structuredResult(summary, payload, false)
}
