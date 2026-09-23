package opsctl

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/approval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedEvent struct {
	name    string
	payload map[string]any
}

type mfaTestHarness struct {
	broker    *mfaBroker
	mu        sync.Mutex
	events    []recordedEvent
	activated int
	emitted   chan recordedEvent
}

func newMFATestHarness() *mfaTestHarness {
	h := &mfaTestHarness{emitted: make(chan recordedEvent, 8)}
	h.broker = newMFABroker(func(name string, payload map[string]any) {
		h.mu.Lock()
		h.events = append(h.events, recordedEvent{name, payload})
		h.mu.Unlock()
		h.emitted <- recordedEvent{name, payload}
	}, func() {
		h.mu.Lock()
		h.activated++
		h.mu.Unlock()
	})
	return h
}

func (h *mfaTestHarness) next(t *testing.T) recordedEvent {
	t.Helper()
	select {
	case ev := <-h.emitted:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event emitted")
		return recordedEvent{}
	}
}

func mfaRequest() approval.ApprovalRequest {
	return approval.ApprovalRequest{
		Type: "mfa", AssetID: 7, AssetName: "bastion",
		MFA: &approval.MFAChallenge{Name: "Verification", Instruction: "Enter code", Prompts: []string{"OTP: "}, Echo: []bool{false}},
	}
}

func TestMFABroker_SubmitReturnsAnswers(t *testing.T) {
	h := newMFATestHarness()
	done := make(chan approval.ApprovalResponse, 1)
	go func() { done <- h.broker.challenge(context.Background(), mfaRequest()) }()

	ev := h.next(t)
	require.Equal(t, "opsctl:mfa", ev.name)
	assert.Equal(t, "bastion", ev.payload["asset_name"])
	assert.Equal(t, "Enter code", ev.payload["instruction"])
	assert.Equal(t, []string{"OTP: "}, ev.payload["prompts"])
	assert.Equal(t, []bool{false}, ev.payload["echo"])
	id := ev.payload["challenge_id"].(string)

	require.NoError(t, h.broker.respond(id, []string{"123456"}))
	resp := <-done
	assert.True(t, resp.Approved)
	assert.Equal(t, []string{"123456"}, resp.MFAAnswers)
	assert.Equal(t, 1, h.activated, "the desktop window is brought to the front")
}

func TestMFABroker_RejectsAnswerCountMismatch(t *testing.T) {
	h := newMFATestHarness()
	go h.broker.challenge(context.Background(), mfaRequest())
	id := h.next(t).payload["challenge_id"].(string)
	assert.Error(t, h.broker.respond(id, []string{"a", "b"}))
	assert.Error(t, h.broker.respond("unknown", []string{"a"}))
	h.broker.cancel(id)
}

func TestMFABroker_CancelIsNotApproved(t *testing.T) {
	h := newMFATestHarness()
	done := make(chan approval.ApprovalResponse, 1)
	go func() { done <- h.broker.challenge(context.Background(), mfaRequest()) }()
	id := h.next(t).payload["challenge_id"].(string)

	h.broker.cancel(id)
	resp := <-done
	assert.False(t, resp.Approved)
	assert.Empty(t, resp.MFAAnswers)
	assert.Equal(t, approval.MFACanceledReason, resp.Reason)
}

func TestMFABroker_RequesterGoneClosesDialog(t *testing.T) {
	h := newMFATestHarness()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan approval.ApprovalResponse, 1)
	go func() { done <- h.broker.challenge(ctx, mfaRequest()) }()
	id := h.next(t).payload["challenge_id"].(string)

	cancel()
	closed := h.next(t)
	assert.Equal(t, "opsctl:mfa-closed", closed.name)
	assert.Equal(t, id, closed.payload["challenge_id"])
	<-done
	assert.Error(t, h.broker.respond(id, []string{"123456"}), "a closed challenge accepts no answers")
}
