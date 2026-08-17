// Package asset_put_svc owns the shared automation boundary for asset writes and
// validated managed-credential references.
package asset_put_svc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
)

// Request contains one create or update operation. Asset.ID == 0 creates;
// Asset.ID > 0 updates.
type Request struct {
	Asset  *asset_entity.Asset
	Config map[string]any
}

// AuthenticationRef is the stable, non-secret association returned to callers.
type AuthenticationRef struct {
	Type string `json:"type"`
	Ref  int64  `json:"ref"`
}

type authenticationPreparer interface {
	PrepareAutomationAuthentication(context.Context, map[string]any) (authType string, ref int64, applicable bool, err error)
}

type automationUpdateContextProvider interface {
	AutomationUpdateContext(*asset_entity.Asset, map[string]any) (map[string]any, error)
}

// Result contains only the persisted asset identity and safe authentication reference.
type Result struct {
	Asset          *asset_entity.Asset `json:"-"`
	ID             int64               `json:"id"`
	Authentication *AuthenticationRef  `json:"authentication,omitempty"`
}

// Prepared is a side-effect-free operation ready for approval and commit.
type Prepared struct {
	asset          *asset_entity.Asset
	handler        assettype.AssetTypeHandler
	config         map[string]any
	approvalConfig map[string]any
	credential     assettype.CredentialPlan
	referencedType string
	authentication *AuthenticationRef
}

// Prepare validates and normalizes a request without mutating caller data or writing rows.
func Prepare(ctx context.Context, req Request) (*Prepared, error) {
	if req.Asset == nil {
		return nil, fmt.Errorf("asset is required")
	}
	asset := *req.Asset
	config := cloneMap(req.Config)
	var preparedCreate assettype.PreparedCreate
	var err error
	if asset.ID == 0 {
		preparedCreate, err = assettype.PrepareCreate(asset.Type, config)
	} else {
		config, err = updateAutomationContext(&asset, config)
		if err == nil {
			preparedCreate, err = assettype.PrepareUpdate(asset.Type, config)
		}
	}
	if err != nil {
		return nil, err
	}
	var referencedType string
	if preparedCreate.Credential.Kind == assettype.CredentialKindReference {
		cred, err := credential_mgr_svc.RequireType(ctx, preparedCreate.Credential.ReferenceID, preparedCreate.Credential.AcceptedTypes)
		if err != nil {
			return nil, err
		}
		referencedType = cred.Type
	}
	var authentication *AuthenticationRef
	if authPreparer, ok := preparedCreate.Handler.(authenticationPreparer); ok {
		authType, ref, applicable, err := authPreparer.PrepareAutomationAuthentication(ctx, preparedCreate.Config)
		if err != nil {
			return nil, err
		}
		if applicable {
			authentication = &AuthenticationRef{Type: authType, Ref: ref}
		}
	}

	return &Prepared{
		asset:          &asset,
		handler:        preparedCreate.Handler,
		config:         preparedCreate.Config,
		approvalConfig: preparedCreate.Approval,
		credential:     preparedCreate.Credential,
		referencedType: referencedType,
		authentication: authentication,
	}, nil
}

// Commit resolves any validated credential reference and performs the asset write
// in one dbutil transaction.
func Commit(ctx context.Context, prepared *Prepared) (*Result, error) {
	if prepared == nil {
		return nil, fmt.Errorf("prepared asset put is required")
	}
	logger.Ctx(ctx).Info("asset put commit start", zap.String("assetType", prepared.asset.Type), zap.Int64("assetID", prepared.asset.ID))

	var result *Result
	err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		config, authentication, err := prepared.resolveAuthentication(txCtx)
		if err != nil {
			return err
		}

		asset := *prepared.asset
		if asset.ID == 0 {
			if err := prepared.handler.ApplyCreateArgs(txCtx, &asset, config); err != nil {
				return fmt.Errorf("apply create args: %w", err)
			}
			if err := asset_svc.Asset().Create(txCtx, &asset); err != nil {
				return fmt.Errorf("create asset: %w", err)
			}
		} else {
			if err := prepared.handler.ApplyUpdateArgs(txCtx, &asset, config); err != nil {
				return fmt.Errorf("apply update args: %w", err)
			}
			if err := updateAsset(txCtx, &asset); err != nil {
				return fmt.Errorf("update asset: %w", err)
			}
		}
		result = &Result{Asset: &asset, ID: asset.ID, Authentication: authentication}
		return nil
	})
	if err != nil {
		logger.Ctx(ctx).Error("asset put commit failed", zap.String("assetType", prepared.asset.Type), zap.Int64("assetID", prepared.asset.ID), zap.Error(err))
		return nil, err
	}
	if prepared.asset.ID > 0 {
		asset_svc.Asset().Invalidate(ctx, result.ID)
	}
	logger.Ctx(ctx).Info("asset put commit end", zap.String("assetType", prepared.asset.Type), zap.Int64("assetID", result.ID))
	return result, nil
}

// Put is the convenience Prepare+Commit entry point.
func Put(ctx context.Context, req Request) (*Result, error) {
	prepared, err := Prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	return Commit(ctx, prepared)
}

// ResultJSON serializes the shared non-secret automation result contract.
func ResultJSON(result *Result, message string) (string, error) {
	if result == nil {
		return "", fmt.Errorf("asset put commit returned no result")
	}
	payload := struct {
		ID             int64              `json:"id"`
		Authentication *AuthenticationRef `json:"authentication,omitempty"`
		Message        string             `json:"message"`
	}{ID: result.ID, Authentication: result.Authentication, Message: message}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode asset result: %w", err)
	}
	return string(encoded), nil
}

// SafeApprovalDetail returns only the type-owned approval field allowlist.
func (p *Prepared) SafeApprovalDetail() map[string]any {
	return map[string]any{
		"name":   p.asset.Name,
		"type":   p.asset.Type,
		"config": cloneMap(p.approvalConfig),
	}
}

// SafeAuditArgs returns the same non-secret metadata plus a typed reference when
// the request reuses an existing credential.
func (p *Prepared) SafeAuditArgs() map[string]any {
	out := p.SafeApprovalDetail()
	if p.asset.ID > 0 {
		out["id"] = p.asset.ID
	}
	if p.credential.Kind == assettype.CredentialKindReference {
		out["authentication"] = AuthenticationRef{Type: p.referencedType, Ref: p.credential.ReferenceID}
	} else if p.authentication != nil {
		out["authentication"] = *p.authentication
	}
	return out
}

// SafeAuditArgsForResult adds only the persisted identity and safe typed association.
// It never exposes the prepared config or credential payload.
func (p *Prepared) SafeAuditArgsForResult(result *Result) map[string]any {
	out := p.SafeAuditArgs()
	if result == nil {
		return out
	}
	out["id"] = result.ID
	if result.Authentication != nil {
		out["authentication"] = *result.Authentication
	}
	return out
}

func (p *Prepared) resolveAuthentication(ctx context.Context) (map[string]any, *AuthenticationRef, error) {
	switch p.credential.Kind {
	case assettype.CredentialKindNone:
		return cloneMap(p.config), p.authentication, nil
	case assettype.CredentialKindReference:
		cred, err := credential_mgr_svc.RequireType(ctx, p.credential.ReferenceID, p.credential.AcceptedTypes)
		if err != nil {
			return nil, nil, err
		}
		return p.bind(cred)
	default:
		return nil, nil, fmt.Errorf("unsupported credential plan kind %q", p.credential.Kind)
	}
}

func (p *Prepared) bind(cred *credential_entity.Credential) (map[string]any, *AuthenticationRef, error) {
	config, err := (&assettype.PreparedCreate{Handler: p.handler, Config: p.config}).BindCredential(assettype.CredentialBinding{ID: cred.ID, Type: cred.Type})
	if err != nil {
		return nil, nil, err
	}
	return config, &AuthenticationRef{Type: cred.Type, Ref: cred.ID}, nil
}

func updateAsset(ctx context.Context, asset *asset_entity.Asset) error {
	return asset_svc.Asset().UpdateWithinTransaction(ctx, asset)
}

func cloneMap(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for key, value := range args {
		switch typed := value.(type) {
		case []string:
			out[key] = append([]string(nil), typed...)
		case []any:
			out[key] = append([]any(nil), typed...)
		default:
			out[key] = value
		}
	}
	return out
}

func updateAutomationContext(asset *asset_entity.Asset, config map[string]any) (map[string]any, error) {
	handler, ok := assettype.Get(asset.Type)
	if !ok {
		return config, nil
	}
	provider, ok := handler.(automationUpdateContextProvider)
	if !ok {
		return config, nil
	}
	return provider.AutomationUpdateContext(asset, config)
}
