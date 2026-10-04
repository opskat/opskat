package aictx

import "context"

// auditCommandKey carries a per-tool-call command-summary slot. Some handlers can only
// compute the effective, safe command after parsing their input (batch_exec, and the
// desktop side of an opsctl-delegated exec); the runner installs the slot before dispatch
// and reads it afterward.
type auditCommandKey struct{}

// WithAuditCommandSlot installs a writable command-summary slot for one tool call.
func WithAuditCommandSlot(ctx context.Context, slot *string) context.Context {
	return context.WithValue(ctx, auditCommandKey{}, slot)
}

// RecordAuditCommand replaces the audit command summary for the current tool call.
// It is a no-op on any opsctl handler path whose caller did not install a slot via
// WithAuditCommandSlot first — cmd/opsctl/command's cmdExec is one caller that does
// (it pre-fills the slot with the canonicalized command before dispatch, since unlike
// the runner-middleware case it already knows the value up front and never needs to
// write to the slot after the fact).
func RecordAuditCommand(ctx context.Context, command string) {
	if slot, ok := ctx.Value(auditCommandKey{}).(*string); ok && slot != nil {
		*slot = command
	}
}

// GetAuditCommand returns the current command-summary slot, if one is installed.
func GetAuditCommand(ctx context.Context) *string {
	if slot, ok := ctx.Value(auditCommandKey{}).(*string); ok {
		return slot
	}
	return nil
}
