package extwire

import (
	"fmt"
	"time"
)

// MaxReviewTimeout is the longest per-call review timeout the host lets the user
// set (Settings › AI › command review). A call of the reviewer's tool runs under
// the shorter of that setting and the tool's own limit, so the tool declares
// this as its limit and the user's setting is what bounds the call.
const MaxReviewTimeout = 60 * time.Second

// CommandReviewer is the extension's command reviewer (KeyCommandReviewer): under
// assisted approval and Autopilot the host asks it about each command the rules
// would ask the user about, one call of Tool per command. Tool is one of the
// extension's own host-only tools whose parameters include ReviewItem's required
// strings assetType, command, syntax and require nothing else — it may declare
// optional ones; a call carries only the ReviewItem fields the tool declares. It
// answers a ReviewAnswer.
// Questions are what a command can fail, and MaxCommandLen is the longest command
// the reviewer takes — a longer one fails the review without a call. The
// manifest must declare contributes.commandReviewer too: a reviewer can let
// commands run unasked, so the user approves it at install.
type CommandReviewer struct {
	Tool          string           `json:"tool"`
	Questions     []ReviewQuestion `json:"questions"`
	MaxCommandLen int              `json:"maxCommandLen"`
}

// UnmarshalJSON refuses a key the host does not read: a mistyped limit left
// unread would load a reviewer that never gets the commands its author expects.
func (c *CommandReviewer) UnmarshalJSON(data []byte) error {
	type plain CommandReviewer
	var v plain
	if err := decodeStrict(data, &v); err != nil {
		return fmt.Errorf("commandReviewer (keys: tool, questions[].id/names, maxCommandLen): %w", err)
	}
	*c = CommandReviewer(v)
	return nil
}

// ReviewQuestion is one question a command can fail. ID is what an answer names
// (a lowercase letter, then lowercase letters, digits and underscores, at most 64
// in all); Names is its name per language ("en", "zh-CN", …), which the host shows.
type ReviewQuestion struct {
	ID    string            `json:"id"`
	Names map[string]string `json:"names"`
}

// ReviewItem is the arguments of one call of the reviewer's tool: the command's
// canonical asset type, the command and its syntax ("shell", "redis" or "text").
// Command is the command exactly as it will run, secrets included, with nothing
// appended; a shell command that both decodes and executes code is never sent.
// A field added later is optional (omitempty) and sent only to a tool that
// declares it, so older reviewers and hosts keep working with newer ones.
type ReviewItem struct {
	AssetType string `json:"assetType" desc:"canonical asset type the command runs on"`
	Command   string `json:"command" desc:"command to review, exactly as it will run"`
	Syntax    string `json:"syntax" desc:"command syntax: shell, redis or text"`
}

// Review answer outcomes.
const (
	// ReviewPass: the command passed every question.
	ReviewPass = "pass"
	// ReviewReject: the command failed the questions in Failed.
	ReviewReject = "reject"
	// ReviewError: the reviewer could not judge the command, for Reason.
	ReviewError = "error"
)

// Reasons a reviewer's error answer may give; the host shows each with its own
// explanation.
const (
	// ReviewReasonNotConfigured: the reviewer's settings are incomplete.
	ReviewReasonNotConfigured = "not_configured"
	// ReviewReasonInvalidAPIKey: the service the reviewer asks refused its key.
	ReviewReasonInvalidAPIKey = "invalid_api_key"
	// ReviewReasonTimeout: the service the reviewer asks did not answer in time.
	ReviewReasonTimeout = "timeout"
	// ReviewReasonUnavailable: the reviewer could not judge for another reason.
	ReviewReasonUnavailable = "unavailable"
)

// ReviewAnswer is the reviewer tool's answer for one command. A reject names the
// failed question ids and may score them (question id → 0..1); an error names its
// reason, one of the ReviewReason* constants. The host fails the review as an
// invalid result for any other shape or reason.
type ReviewAnswer struct {
	Outcome string             `json:"outcome"`
	Failed  []string           `json:"failed,omitempty"`
	Scores  map[string]float64 `json:"scores,omitempty"`
	Reason  string             `json:"reason,omitempty"`
}

// ParseReviewAnswer decodes a reviewer tool's answer, refusing a field
// ReviewAnswer does not define: an answer in another shape must not read as a pass.
func ParseReviewAnswer(data []byte) (ReviewAnswer, error) {
	var a ReviewAnswer
	if err := decodeStrict(data, &a); err != nil {
		return ReviewAnswer{}, fmt.Errorf("review answer (keys: outcome, failed, scores, reason): %w", err)
	}
	return a, nil
}
