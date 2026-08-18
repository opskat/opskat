package policy_rule_repo

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/dbutil"
)

func TestDefaultPolicyRuleDelegatesTransactionBoundary(t *testing.T) {
	want := errors.New("transaction result")
	runnerCalled := false
	callbackCalled := false
	ctx := dbutil.WithTransactionRunner(context.Background(), func(txCtx context.Context, fn func(context.Context) error) error {
		runnerCalled = true
		return fn(txCtx)
	})

	err := NewPolicyRule().WithTransaction(ctx, func(context.Context) error {
		callbackCalled = true
		return want
	})

	require.ErrorIs(t, err, want)
	require.True(t, runnerCalled)
	require.True(t, callbackCalled)
}

func TestRegisterPolicyRule(t *testing.T) {
	original := PolicyRule()
	t.Cleanup(func() { RegisterPolicyRule(original) })

	stub := &stubPolicyRuleRepo{}
	RegisterPolicyRule(stub)

	require.Same(t, stub, PolicyRule())
}

type stubPolicyRuleRepo struct {
	PolicyRuleRepo
}
