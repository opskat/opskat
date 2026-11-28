package mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// askingTool asks the client one question per entry of questions, in order, and reports
// every answer it got ("<action>:<decision>", or "unanswered" when Ask returned nil).
func askingTool(runs *atomic.Int32, questions ...string) *tool.RawTool {
	return &tool.RawTool{
		NameStr:   "ask",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			runs.Add(1)
			out := ""
			for _, q := range questions {
				res, ok := Ask(ctx, &mcpsdk.ElicitParams{
					Message: q,
					RequestedSchema: map[string]any{
						"type":       "object",
						"properties": map[string]any{"decision": map[string]any{"type": "string", "enum": []string{"allow", "deny"}}},
					},
				})
				switch {
				case !ok:
					out += "[no client channel]"
				case res == nil:
					out += "[unanswered]"
				default:
					out += fmt.Sprintf("[%s:%v]", res.Action, res.Content["decision"])
				}
			}
			return tool.TextResult(out), nil
		},
	}
}

// answeringClient answers every elicitation with action/decision and records the messages it was shown.
func answeringClient(action, decision string, shown *[]string, mu *sync.Mutex) *mcpsdk.ClientOptions {
	return &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			mu.Lock()
			*shown = append(*shown, req.Params.Message)
			mu.Unlock()
			res := &mcpsdk.ElicitResult{Action: action}
			if action == "accept" {
				res.Content = map[string]any{"decision": decision}
			}
			return res, nil
		},
	}
}

func callText(t *testing.T, c *mcpTestClient, name string, args map[string]any) wireToolResult {
	t.Helper()
	return decodeResult[wireToolResult](t, c.call("tools/call", map[string]any{"name": name, "arguments": args}))
}

func TestAskPutsTheQuestionToTheClientAndRunsTheToolOnce(t *testing.T) {
	var runs atomic.Int32
	var shown []string
	var mu sync.Mutex
	client, cleanup := startTestServerWith(t, ServerConfig{Tools: []agent.Tool{askingTool(&runs, "allow rm -rf /tmp/x on web-01?")}},
		answeringClient("accept", "allow", &shown, &mu))
	defer cleanup()

	res := callText(t, client, "ask", map[string]any{})
	require.False(t, res.IsError)
	require.Equal(t, "[accept:allow]", res.Content[0].Text)
	require.Equal(t, []string{"allow rm -rf /tmp/x on web-01?"}, shown)
	require.Equal(t, int32(1), runs.Load(), "answering resumes the paused call; the tool must not run again")
}

func TestAskReportsNoChannelWhenTheClientCannotShowAPrompt(t *testing.T) {
	var runs atomic.Int32
	client, cleanup := startTestServer(t, ServerConfig{Tools: []agent.Tool{askingTool(&runs, "q")}})
	defer cleanup()

	res := callText(t, client, "ask", map[string]any{})
	require.Equal(t, "[no client channel]", res.Content[0].Text)
}

func TestAskAnswersSuccessiveQuestionsOfOneCall(t *testing.T) {
	var runs atomic.Int32
	var shown []string
	var mu sync.Mutex
	client, cleanup := startTestServerWith(t, ServerConfig{Tools: []agent.Tool{askingTool(&runs, "first?", "second?")}},
		answeringClient("accept", "deny", &shown, &mu))
	defer cleanup()

	res := callText(t, client, "ask", map[string]any{})
	require.Equal(t, "[accept:deny][accept:deny]", res.Content[0].Text)
	require.Equal(t, []string{"first?", "second?"}, shown)
	require.Equal(t, int32(1), runs.Load())
}

func TestAskPassesDeclineAndCancelThrough(t *testing.T) {
	for _, action := range []string{"decline", "cancel"} {
		t.Run(action, func(t *testing.T) {
			var runs atomic.Int32
			var shown []string
			var mu sync.Mutex
			client, cleanup := startTestServerWith(t, ServerConfig{Tools: []agent.Tool{askingTool(&runs, "q")}},
				answeringClient(action, "", &shown, &mu))
			defer cleanup()

			res := callText(t, client, "ask", map[string]any{})
			require.Equal(t, "["+action+":<nil>]", res.Content[0].Text)
		})
	}
}

func TestAskUnansweredBeforeTheDeadlineIsReportedAsUnanswered(t *testing.T) {
	var runs atomic.Int32
	finished := make(chan string, 1)
	inner := askingTool(&runs, "q")
	handler := inner.Handler
	inner.Handler = func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
		res, err := handler(ctx, in)
		finished <- blockText(res)
		return res, err
	}
	release := make(chan struct{})
	client, cleanup := startTestServerWith(t, ServerConfig{Tools: []agent.Tool{inner}, AnswerTimeout: 50 * time.Millisecond},
		&mcpsdk.ClientOptions{ElicitationHandler: func(ctx context.Context, _ *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			<-release // the human never answers in time
			return &mcpsdk.ElicitResult{Action: "accept", Content: map[string]any{"decision": "allow"}}, nil
		}})
	defer cleanup()

	callDone := make(chan wireToolResult, 1)
	go func() { callDone <- callText(t, client, "ask", map[string]any{}) }()

	select {
	case out := <-finished:
		require.Equal(t, "[unanswered]", out, "an expired question must read as unanswered so callers fail closed")
	case <-time.After(3 * time.Second):
		t.Fatal("expired question did not release the paused tool")
	}
	close(release)
	res := <-callDone
	require.True(t, res.IsError, "a late answer must not reach the expired call")
}

// olderProtocol predates multi round-trip requests (SEP-2322): the server asks with
// elicitation/create while the call is in flight.
const olderProtocol = "2025-06-18"

// olderClient is a client of olderProtocol: it answers elicitation/create and knows
// nothing of retrying a call with input responses (the Go SDK's client would).
func olderClient(opts *mcpsdk.ClientOptions) *mcpsdk.ClientOptions {
	opts.MultiRoundTrip = &mcpsdk.MultiRoundTripOptions{Disabled: true}
	return opts
}

func TestAskOnAnOlderProtocolAsksEveryQuestionOfOneCall(t *testing.T) {
	var runs atomic.Int32
	var shown []string
	var mu sync.Mutex
	client, cleanup := startTestServerOn(t, ServerConfig{Tools: []agent.Tool{askingTool(&runs, "first?", "second?")}},
		olderClient(answeringClient("accept", "deny", &shown, &mu)), olderProtocol)
	defer cleanup()

	res := callText(t, client, "ask", map[string]any{})
	require.False(t, res.IsError)
	require.Equal(t, "[accept:deny][accept:deny]", res.Content[0].Text, "the second question gets an answer too, not an empty result")
	require.Equal(t, []string{"first?", "second?"}, shown)
	require.Equal(t, int32(1), runs.Load())
}

// A prompt the client fails to show is unanswered there and then: the call ends at once
// and the next serial tool runs, instead of both waiting for the answer timeout.
func TestAskOnAnOlderProtocolAFailedPromptEndsTheCallAndFreesTheSerialLock(t *testing.T) {
	var runs atomic.Int32
	serialAsk := askingTool(&runs, "q?")
	serialAsk.IsSerial = true
	other := &tool.RawTool{NameStr: "other", SchemaVal: agent.Schema{Type: "object"}, IsSerial: true,
		Handler: func(context.Context, map[string]any) (*agent.ToolResultBlock, error) {
			return tool.TextResult("ran"), nil
		}}
	client, cleanup := startTestServerOn(t, ServerConfig{Tools: []agent.Tool{serialAsk, other}},
		olderClient(&mcpsdk.ClientOptions{ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			return nil, fmt.Errorf("the user dismissed the prompt")
		}}), olderProtocol)
	defer cleanup()

	res := callText(t, client, "ask", map[string]any{})
	require.Equal(t, "[unanswered]", res.Content[0].Text, "an unshown prompt must read as unanswered so callers fail closed")

	done := make(chan wireToolResult, 1)
	go func() { done <- callText(t, client, "other", map[string]any{}) }()
	select {
	case out := <-done:
		require.Equal(t, "ran", out.Content[0].Text)
	case <-time.After(3 * time.Second):
		t.Fatal("the serial lock is still held by the call whose prompt failed")
	}
}

func TestAskRejectsAnAnswerReplayedOntoDifferentArguments(t *testing.T) {
	var runs atomic.Int32
	client, cleanup := startTestServerWith(t, ServerConfig{Tools: []agent.Tool{askingTool(&runs, "q")}},
		&mcpsdk.ClientOptions{
			ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
				return nil, fmt.Errorf("retries are driven by hand in this test")
			},
			MultiRoundTrip: &mcpsdk.MultiRoundTripOptions{Disabled: true},
		})
	defer cleanup()

	ctx := context.Background()
	first, err := client.session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "ask", Arguments: map[string]any{"command": "uptime"}})
	require.NoError(t, err)
	require.True(t, first.NeedsInput())
	require.NotEmpty(t, first.RequestState)

	answers := mcpsdk.InputResponseMap{}
	for id := range first.InputRequests {
		answers[id] = &mcpsdk.ElicitResult{Action: "accept", Content: map[string]any{"decision": "allow"}}
	}
	replayed, err := client.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:           "ask",
		Arguments:      map[string]any{"command": "rm -rf /"},
		InputResponses: answers,
		RequestState:   first.RequestState,
	})
	require.NoError(t, err)
	require.True(t, replayed.IsError, "an answer given for one call must not approve another")

	again, err := client.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:           "ask",
		Arguments:      map[string]any{"command": "uptime"},
		InputResponses: answers,
		RequestState:   first.RequestState,
	})
	require.NoError(t, err)
	require.True(t, again.IsError, "a mismatched retry abandons the paused call; its state cannot be reused")
}
