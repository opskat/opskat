package permission

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestRegisterExecutor_RoundTrip(t *testing.T) {
	const typeName = "test-exec-type"
	t.Cleanup(func() { delete(execEntries, typeName) })

	RegisterExecutor(typeName, func(_ context.Context, _ *asset_entity.Asset, cmd, _ string) (string, error) {
		return "ran:" + cmd, nil
	}, "usage doc")

	fn, ok := ExecutorFor(typeName)
	if !ok {
		t.Fatal("ExecutorFor returned false for a registered type")
	}
	out, err := fn(context.Background(), &asset_entity.Asset{}, "uptime", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "ran:uptime" {
		t.Fatalf("got %q, want %q", out, "ran:uptime")
	}

	help, ok := HelpFor(typeName)
	if !ok || help != "usage doc" {
		t.Fatalf("got help %q ok=%v", help, ok)
	}
}

func TestExecutorFor_UnknownType(t *testing.T) {
	if _, ok := ExecutorFor("definitely-not-registered"); ok {
		t.Fatal("ExecutorFor returned true for an unregistered type")
	}
}

func TestRegisterExecutor_DuplicatePanics(t *testing.T) {
	const typeName = "test-dup-type"
	t.Cleanup(func() { delete(execEntries, typeName) })

	noop := func(_ context.Context, _ *asset_entity.Asset, _, _ string) (string, error) { return "", nil }
	RegisterExecutor(typeName, noop, "doc")

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	RegisterExecutor(typeName, noop, "doc")
}

func TestRegisteredExecTypes_Sorted(t *testing.T) {
	got := RegisteredExecTypes()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("RegisteredExecTypes not sorted: %v", got)
		}
	}
}
