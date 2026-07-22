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

// TestRegisterExecutor_EmptyHelpPanics locks the fix for the critical review finding on
// task 3: execimpl/register.go's 8 exec-type init() calls used to discard skills.Get's ok
// (`sshHelp, _ := skills.Get(...)`), so a missing or emptied SKILL.md silently became an
// empty help string that RegisterExecutor accepted without complaint. HelpFor then
// reported ("", true) for that type — which satisfies TestEveryAssetTypeHasHelpDoc's
// existence-only check even though the model would receive zero bytes of usage doc.
// RegisterExecutor must refuse an empty help string exactly like RegisterHelpDoc already
// does (see the sibling panic in RegisterHelpDoc), turning a missing/empty doc into a
// startup-time panic instead of a silently-green coverage test.
func TestRegisterExecutor_EmptyHelpPanics(t *testing.T) {
	const typeName = "test-empty-help-type"
	t.Cleanup(func() { delete(execEntries, typeName) })

	noop := func(_ context.Context, _ *asset_entity.Asset, _, _ string) (string, error) { return "", nil }
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when registering an executor with an empty help string")
		}
	}()
	RegisterExecutor(typeName, noop, "")
}

func TestRegisteredExecTypes_Sorted(t *testing.T) {
	got := RegisteredExecTypes()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("RegisteredExecTypes not sorted: %v", got)
		}
	}
}

// TestRegisterPrecheck_RoundTrip mirrors TestRegisterExecutor_RoundTrip for the
// PrecheckFunc registration added to close the serial approval-then-fail gap: a type can
// register an optional precondition check, looked up the same way CanonicalizeFor is.
func TestRegisterPrecheck_RoundTrip(t *testing.T) {
	const typeName = "test-precheck-type"
	t.Cleanup(func() { delete(execEntries, typeName) })

	RegisterExecutor(typeName, func(_ context.Context, _ *asset_entity.Asset, cmd, _ string) (string, error) {
		return "ran:" + cmd, nil
	}, "usage doc")

	var gotAsset *asset_entity.Asset
	RegisterPrecheck(typeName, func(_ context.Context, asset *asset_entity.Asset) error {
		gotAsset = asset
		return nil
	})

	fn, ok := PrecheckFor(typeName)
	if !ok {
		t.Fatal("PrecheckFor returned false for a type with a registered precheck")
	}
	asset := &asset_entity.Asset{ID: 3}
	if err := fn(context.Background(), asset); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAsset != asset {
		t.Fatal("precheck did not receive the asset passed to it")
	}
}

// TestPrecheckFor_NoneRegistered proves that types with no precheck (the common case —
// only serial needs one) get (nil, false), not a stub that always allows. Callers must
// treat false as "nothing to run", not "run and got nil error".
func TestPrecheckFor_NoneRegistered(t *testing.T) {
	const typeName = "test-precheck-none"
	t.Cleanup(func() { delete(execEntries, typeName) })

	RegisterExecutor(typeName, func(_ context.Context, _ *asset_entity.Asset, cmd, _ string) (string, error) {
		return cmd, nil
	}, "doc")

	if _, ok := PrecheckFor(typeName); ok {
		t.Fatal("PrecheckFor returned true for a type with no registered precheck")
	}
}

// TestRegisterPrecheck_PanicsOnUnregisteredExecutor: precheck registration must follow
// executor registration (execimpl's init order: RegisterExecutor, then RegisterPrecheck),
// same as how CanonicalizeFor only makes sense once an execEntry already exists.
func TestRegisterPrecheck_PanicsOnUnregisteredExecutor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when registering a precheck for an unregistered executor")
		}
	}()
	RegisterPrecheck("definitely-not-registered", func(_ context.Context, _ *asset_entity.Asset) error { return nil })
}

// TestRegisterPrecheck_DuplicatePanics: same conflict discipline as RegisterExecutor's
// duplicate guard — a second registration for the same type is a startup-time programming
// error, not something to silently overwrite.
func TestRegisterPrecheck_DuplicatePanics(t *testing.T) {
	const typeName = "test-precheck-dup"
	t.Cleanup(func() { delete(execEntries, typeName) })

	RegisterExecutor(typeName, func(_ context.Context, _ *asset_entity.Asset, cmd, _ string) (string, error) {
		return cmd, nil
	}, "doc")
	noop := func(_ context.Context, _ *asset_entity.Asset) error { return nil }
	RegisterPrecheck(typeName, noop)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate precheck registration")
		}
	}()
	RegisterPrecheck(typeName, noop)
}
