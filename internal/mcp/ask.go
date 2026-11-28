package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
)

// A tool call that needs a human decision puts the question to the MCP client that made
// the call: the client renders it in its own UI (Claude Code, Codex, pi all do) and the
// answer comes back on the same tools/call.
//
// Before protocol 2026-07-28 the server asks with an elicitation/create request while the
// call is in flight, and the answer is the reply to it (askDuringCall).
//
// From 2026-07-28 that request is not allowed: the call returns InputRequests and the
// client retries it with the answers (multi round-trip, SEP-2322). OpsKat tools are not
// re-entrant — an approval sits in the middle of a tool, after connecting to the asset
// and before running the command, and SSH MFA sits in the middle of a handshake — so a
// retry must not run the tool again. The call runs once, on its own goroutine; Ask parks
// it, the handler hands the question to the client, and the retry delivers the answer to
// the parked call, which carries on from where it stopped.

// DefaultAnswerTimeout bounds how long a call waits for the human before the question
// counts as unanswered (a denial).
const DefaultAnswerTimeout = 10 * time.Minute

// questionKey is the InputRequests id of a paused call's question. A call asks one
// question at a time, so one fixed id is enough.
const questionKey = "approval"

// askerKey holds the current call's way of putting a question to its client.
type askerKey struct{}

// asker puts one question to the client and waits for the answer; nil is unanswered.
type asker func(ctx context.Context, params *mcpsdk.ElicitParams) *mcpsdk.ElicitResult

// pausedCall is a tool call running on its own goroutine that may stop to ask the
// client questions.
type pausedCall struct {
	id string
	// binding is the call (tool name + canonical arguments) the question was asked for;
	// a retry presenting the call's id must be the same call, so an answer cannot be
	// moved onto a different command.
	binding string
	asks    chan *question
	done    chan *mcpsdk.CallToolResult // buffered: the run never blocks on a caller that went away
	cancel  context.CancelFunc

	// set while parked waiting for the client's retry; guarded by Server.mu
	waiting *question
	expiry  *time.Timer
}

type question struct {
	params *mcpsdk.ElicitParams
	answer chan *mcpsdk.ElicitResult // buffered: answering never blocks on a call that stopped waiting
}

// Ask puts a question to the human through the MCP client that issued the current tool
// call and waits for the answer.
//
// ok is false when the call has no such channel (the client cannot render prompts);
// the caller must use another approval surface. A nil result with ok true means the
// question went unanswered — the call was canceled or the answer timed out — and must
// be treated as a denial, like a "decline" or "cancel" action.
func Ask(ctx context.Context, params *mcpsdk.ElicitParams) (res *mcpsdk.ElicitResult, ok bool) {
	ask, _ := ctx.Value(askerKey{}).(asker)
	if ask == nil {
		return nil, false
	}
	return ask(ctx, params), true
}

// askDuringCall asks a client of a protocol before 2026-07-28: an elicitation/create
// request while the call is in flight. A prompt the client fails to show is unanswered.
func askDuringCall(session *mcpsdk.ServerSession) asker {
	return func(ctx context.Context, params *mcpsdk.ElicitParams) *mcpsdk.ElicitResult {
		res, err := session.Elicit(ctx, params)
		if err != nil {
			logger.Ctx(ctx).Warn("mcp client prompt failed; treating it as unanswered", zap.Error(err))
			return nil
		}
		return res
	}
}

// ask parks the call on a question until the client's retry answers it.
func (call *pausedCall) ask(ctx context.Context, params *mcpsdk.ElicitParams) *mcpsdk.ElicitResult {
	q := &question{params: params, answer: make(chan *mcpsdk.ElicitResult, 1)}
	select {
	case call.asks <- q:
	case <-ctx.Done():
		return nil
	}
	select {
	case res := <-q.answer:
		return res
	case <-ctx.Done():
		return nil
	}
}

// multiRoundTripProtocol is the first protocol version whose clients answer questions by
// retrying the call (SEP-2322); the SDK draws the same line.
const multiRoundTripProtocol = "2026-07-28"

// retriesWithAnswers reports whether the calling client answers questions by retrying.
func retriesWithAnswers(req *mcpsdk.CallToolRequest) bool {
	params := req.Session.InitializeParams()
	return params == nil || params.ProtocolVersion >= multiRoundTripProtocol
}

// clientCanPrompt reports whether the client declared form elicitation. A client that
// declares elicitation without naming a mode supports form (the SDK reads it the same way).
func clientCanPrompt(req *mcpsdk.CallToolRequest) bool {
	caps := req.ClientCapabilities()
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	return caps.Elicitation.Form != nil || caps.Elicitation.URL == nil
}

// callBinding identifies a call by tool name and arguments; encoding/json sorts map keys,
// so a client re-encoding the same arguments yields the same binding.
func callBinding(name string, args map[string]any) (string, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return name + "\x00" + string(b), nil
}

// startPrompting runs a call that may ask the client questions.
func (s *Server) startPrompting(reqCtx context.Context, binding string, run func(context.Context) *mcpsdk.CallToolResult) *mcpsdk.CallToolResult {
	// The run outlives this request while it is parked between rounds, so it hangs off
	// the server's lifetime; await cancels it when a waiting request is canceled.
	runCtx, cancel := context.WithCancel(s.serveCtx)
	call := &pausedCall{
		id:      uuid.NewString(),
		binding: binding,
		asks:    make(chan *question),
		done:    make(chan *mcpsdk.CallToolResult, 1),
		cancel:  cancel,
	}
	go func() { call.done <- run(context.WithValue(runCtx, askerKey{}, asker(call.ask))) }()
	return s.await(reqCtx, call)
}

// resume delivers the client's answer to the call it was asked for and keeps waiting.
func (s *Server) resume(reqCtx context.Context, params *mcpsdk.CallToolParamsRaw, binding string) *mcpsdk.CallToolResult {
	call := s.unpark(params.RequestState)
	if call == nil {
		return textResult("This approval is no longer pending: it was already answered or it expired. Call the tool again to ask anew.", true)
	}
	if call.binding != binding {
		call.cancel()
		logger.Ctx(reqCtx).Warn("mcp approval answer presented for a different call; abandoning the paused call",
			zap.String("pausedCall", call.id))
		return textResult("This approval answer belongs to a different call; nothing was approved.", true)
	}
	answer, _ := params.InputResponses[questionKey].(*mcpsdk.ElicitResult)
	call.waiting.answer <- answer
	return s.await(reqCtx, call)
}

// await waits for the call to finish or to ask its next question.
func (s *Server) await(reqCtx context.Context, call *pausedCall) *mcpsdk.CallToolResult {
	select {
	case res := <-call.done:
		call.cancel()
		return res
	case q := <-call.asks:
		s.park(call, q)
		return &mcpsdk.CallToolResult{
			InputRequests: mcpsdk.InputRequestMap{questionKey: q.params},
			RequestState:  call.id,
		}
	case <-reqCtx.Done():
		call.cancel()
		return textResult("request canceled", true)
	}
}

func (s *Server) park(call *pausedCall, q *question) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call.waiting = q
	s.paused[call.id] = call
	call.expiry = time.AfterFunc(s.answerTimeout, func() {
		if s.unpark(call.id) == nil {
			return // answered meanwhile
		}
		logger.Default().Warn("mcp approval prompt unanswered in time; denying", zap.String("pausedCall", call.id))
		call.cancel() // Ask sees the canceled context and reports the question unanswered
	})
}

// unpark removes a parked call so exactly one of its retry and its expiry acts on it.
func (s *Server) unpark(id string) *pausedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := s.paused[id]
	if call == nil {
		return nil
	}
	delete(s.paused, id)
	call.expiry.Stop()
	return call
}
