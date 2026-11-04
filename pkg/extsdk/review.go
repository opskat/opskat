package opskat

import "github.com/opskat/opskat/pkg/extwire"

// ReviewQuestion is one question a command can fail: ID (a lowercase letter, then
// lowercase letters, digits and underscores, at most 64) is what a reject names,
// Names its name per language ("en", "zh-CN", …), which the host shows.
type ReviewQuestion = extwire.ReviewQuestion

// ReviewItem is one command to review: its canonical asset type, the command and
// its syntax ("shell", "redis" or "text"). Command is the command exactly as it
// will run, passwords and keys included, with nothing appended; a shell command
// that both decodes and executes code is never sent. It never carries the asset's
// id, name or config.
type ReviewItem = extwire.ReviewItem

// ReviewResult is the verdict on one command: ReviewPass; ReviewReject with the
// failed question ids in Failed and, optionally, their 0..1 Scores; or
// ReviewError with a Reason when the reviewer could not judge it — one of
// "not_configured", "invalid_api_key", "timeout", "unavailable"
// (extwire.ReviewReason*). The host fails the review for any other shape or
// reason, an undeclared question id or a score out of range.
type ReviewResult = extwire.ReviewAnswer

// Review outcomes.
const (
	ReviewPass   = extwire.ReviewPass
	ReviewReject = extwire.ReviewReject
	ReviewError  = extwire.ReviewError
)

// ReviewToolName is the host-only tool CommandReviewer registers.
const ReviewToolName = "review_command"

var commandReviewer *extwire.CommandReviewer

// CommandReviewer declares the extension's command reviewer: under assisted
// approval and Autopilot the host calls review once per command the rules would
// ask the user about, concurrently, each call bounded by the host's review
// timeout setting — the tool's own limit is the longest that setting allows
// (extwire.MaxReviewTimeout) — and a timed-out call is retried once, so review
// needs no retry of its own. questions are what a command can fail; a command longer than
// maxCommandLen fails the review without a call.
//
// It registers the host-only tool ReviewToolName — exec, pages and help never see
// it — and the commandReviewer contribution naming it. The manifest must declare
// contributes.commandReviewer: true as well, or the host refuses the extension at
// load. review runs as a call without an asset: it reads the extension's settings
// with ctx.Settings(), and reaches their format:"endpoint" addresses, with the
// settings' auth injected, under permissions.network.settingsEndpoint. An
// extension has one reviewer: a second call fails describe.
func CommandReviewer(questions []ReviewQuestion, maxCommandLen int, review func(ctx *ToolContext, item ReviewItem) ReviewResult) {
	if commandReviewer != nil {
		failRegistration("opskat: a command reviewer is already declared")
		return
	}
	Tool(ReviewToolName, func(ctx *ToolContext, item ReviewItem) (any, error) {
		return review(ctx, item), nil
	}).HostOnly().Timeout(extwire.MaxReviewTimeout)
	commandReviewer = &extwire.CommandReviewer{
		Tool:          ReviewToolName,
		Questions:     append([]ReviewQuestion(nil), questions...),
		MaxCommandLen: maxCommandLen,
	}
}
