package helper

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/etcd_svc"
)

// ExecEtcdOnAsset is the permission-check-free execution entry point used by the
// unified exec tool.
// HandleExecEtcd is removed alongside the exec_etcd tool in a later task; until then
// the two paths coexist.
// scope is meaningless for etcd and is ignored.
func ExecEtcdOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	req, err := etcd_svc.ParseCommand(command)
	if err != nil {
		return "", err
	}
	req.AssetID = asset.ID

	svc := etcd_svc.New(getSSHPool(ctx))
	result, err := svc.Exec(ctx, req)
	if err != nil {
		return "", err
	}
	return marshalEtcdResult(ctx, asset.ID, req.Op, result)
}

// CanonicalizeEtcdCommand normalizes a model-provided command into the form used for
// policy matching, approval display, and audit logging: a round trip through
// ParseCommand + FormatCommand, which normalizes case, compound-op spelling, and flag
// order.
//
// This must run before the permission check (see the ordering comment on handleExec in
// internal/ai/tool/tool_handlers_unified.go): a syntactically invalid command will
// always fail execution, so it must fail here — before an approval dialog can appear —
// rather than after the user has already been interrupted and approved a command that
// then fails to parse.
func CanonicalizeEtcdCommand(_ *asset_entity.Asset, command string) (string, error) {
	req, err := etcd_svc.ParseCommand(command)
	if err != nil {
		return "", err
	}
	return etcd_svc.FormatCommand(req), nil
}
