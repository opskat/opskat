package aictx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditRequestSlot_RecordAndGet(t *testing.T) {
	slot := NewAuditRequestSlot()
	ctx := WithAuditRequestSlot(context.Background(), slot)
	assert.Nil(t, GetAuditRequest(ctx), "no projection recorded yet")

	RecordAuditRequest(ctx, map[string]any{
		"name":           "cache",
		"type":           "redis",
		"authentication": map[string]any{"type": "password", "ref": float64(9)},
	})

	got := GetAuditRequest(ctx)
	require.NotNil(t, got, "projection must be readable after RecordAuditRequest")
	assert.Equal(t, "cache", got["name"])
	assert.Equal(t, "redis", got["type"])
	auth, ok := got["authentication"].(map[string]any)
	require.True(t, ok, "authentication must survive the round-trip")
	assert.Equal(t, float64(9), auth["ref"])
}

func TestAuditRequest_NoSlotIsNoOp(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, GetAuditRequest(ctx), "no slot installed -> nil projection")
	assert.NotPanics(t, func() {
		RecordAuditRequest(ctx, map[string]any{"name": "x"})
	}, "RecordAuditRequest without a slot must be a no-op")
}

func TestAuditRequest_SlotIsPerCall(t *testing.T) {
	slotA := NewAuditRequestSlot()
	slotB := NewAuditRequestSlot()
	ctxA := WithAuditRequestSlot(context.Background(), slotA)
	ctxB := WithAuditRequestSlot(context.Background(), slotB)

	RecordAuditRequest(ctxA, map[string]any{"name": "A"})
	RecordAuditRequest(ctxB, map[string]any{"name": "B"})

	assert.Equal(t, "A", GetAuditRequest(ctxA)["name"], "slot A must not see B's projection")
	assert.Equal(t, "B", GetAuditRequest(ctxB)["name"], "slot B must not see A's projection")
}
