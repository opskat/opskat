// internal/app/extension/conntest.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/pkg/extension"
)

// NewConnTestRegistrar returns the extreg.ConnTestRegistrar main.go wires in
// once at startup (extreg.SetConnTestRegistrar), before any extension loads:
// internal/extreg cannot build this itself (import cycle — extension_svc
// already calls extreg.Register), so it asks for one through this seam,
// exactly like NewAssetDialer / NewAssetConfigGetter are asked for theirs.
func (e *Extension) NewConnTestRegistrar() extreg.ConnTestRegistrar {
	return &connTestRegistrar{ext: e}
}

type connTestRegistrar struct{ ext *Extension }

// Build returns the conntest.TestFunc extreg registers under assetType when
// its describe() declares a test-connection handler — the closure
// System.TestAssetConnection dispatches to exactly like a built-in type's,
// so the asset form's "Test connection" button needs no extension-specific
// IPC entry point.
//
// plainPassword carries the asset id being edited, as a decimal string (""
// or "0" for a new asset): test connection has no password semantics of its
// own (the form's password fields already ride inside configJSON, alongside
// the connection settings — see connTestRegistrar.buildAdHocConfig), so this
// reuses conntest's one free-form parameter instead of widening every
// registrant's signature for a need only extension types have.
func (r *connTestRegistrar) Build(extName string, manifest *extension.Manifest, assetType string, plugin extreg.TestConnectionCaller) conntest.TestFunc {
	return func(ctx context.Context, configJSON, plainPassword string) error {
		assetID, _ := strconv.ParseInt(plainPassword, 10, 64)
		adhoc, err := r.ext.buildAdHocTestConfig(ctx, extName, manifest, assetType, assetID, configJSON)
		if err != nil {
			return err
		}
		return plugin.TestConnection(ctx, assetType, adhoc)
	}
}

// buildAdHocTestConfig turns the asset form's submitted configJSON into the
// extension.AdHocAssetConfig Plugin.TestConnection dials and gates by: the
// reserved connection block (tunnel id / proxy chain / TLS) split off into
// its own fields, and the guest-visible remainder with any password field
// the form left out merged from the edited asset's stored value, then gated
// behind the extension's credentials capability.
func (e *Extension) buildAdHocTestConfig(ctx context.Context, extName string, manifest *extension.Manifest, assetType string, assetID int64, configJSON string) (*extension.AdHocAssetConfig, error) {
	def := manifest.AssetTypeDef(assetType)
	if def == nil {
		return nil, fmt.Errorf("extension %q does not register asset type %q", extName, assetType)
	}

	hostCfg, err := parseHostConnectionConfig(configJSON)
	if err != nil {
		return nil, fmt.Errorf("test connection: %w", err)
	}
	guestConfig, err := extension.StripHostConnectionConfig(json.RawMessage(configJSON))
	if err != nil {
		return nil, fmt.Errorf("test connection: %w", err)
	}

	credentials := guestConfig
	if passwordFields := extension.PasswordFieldsFromSchema(def.ConfigSchema); len(passwordFields) > 0 {
		guestConfig, credentials, err = e.mergeAndGateTestPasswords(ctx, extName, assetType, assetID, manifest, passwordFields, guestConfig)
		if err != nil {
			return nil, err
		}
	}

	adhoc := &extension.AdHocAssetConfig{Config: guestConfig, Credentials: credentials}
	if hostCfg != nil {
		adhoc.SSHTunnelID = hostCfg.SSHTunnelID
		if hostCfg.ProxyChain != nil {
			adhoc.ProxyChain, _ = json.Marshal(hostCfg.ProxyChain)
		}
		if hostCfg.TLS != nil {
			adhoc.TLS, _ = json.Marshal(hostCfg.TLS)
		}
	}
	return adhoc, nil
}

// mergeAndGateTestPasswords fills any password field guestConfig omits with
// the decrypted value stored on the asset being edited — the form leaves a
// field the user has not touched out of the test request instead of
// round-tripping its plaintext, and this restores it from what is already on
// disk (assetID==0, a new asset, has nothing stored: an omitted field there
// simply decodes to the guest's zero value). A field sent empty was cleared,
// and is tested empty, exactly as it will be saved. The merged result is what
// host-side credential injection reads (credentials); the guest's copy is then
// gated behind the extension's credentials capability exactly as
// ctx.AssetConfig() would for a real call: without credentials:read, a guest
// must not receive through a test call the plaintext the normal door withholds
// — it authenticates via host-injected Auth (describe()'s auth bindings)
// instead, which still needs that plaintext.
func (e *Extension) mergeAndGateTestPasswords(ctx context.Context, extName, assetType string, assetID int64, manifest *extension.Manifest, passwordFields []string, guestConfig json.RawMessage) (guest, credentials json.RawMessage, err error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(guestConfig, &cfg); err != nil {
		return nil, nil, fmt.Errorf("test connection: parse config: %w", err)
	}

	if assetID > 0 {
		_, asset, err := ownedAsset(ctx, e.service, extName, assetID)
		if err != nil {
			return nil, nil, fmt.Errorf("test connection: %w", err)
		}
		if asset.Type != assetType {
			return nil, nil, fmt.Errorf("test connection: asset %d is type %q, not %q", assetID, asset.Type, assetType)
		}
		var stored map[string]json.RawMessage
		if asset.Config != "" {
			if err := json.Unmarshal([]byte(asset.Config), &stored); err != nil {
				return nil, nil, fmt.Errorf("test connection: parse stored config: %w", err)
			}
		}
		for _, field := range passwordFields {
			if _, sent := cfg[field]; sent {
				continue // the user typed a new value or cleared it: use it, never the stored one
			}
			raw, ok := stored[field]
			if !ok {
				continue
			}
			var encrypted string
			if json.Unmarshal(raw, &encrypted) != nil || encrypted == "" {
				continue
			}
			decrypted, err := decryptPasswordField(assetType, field, encrypted)
			if err != nil {
				return nil, nil, fmt.Errorf("test connection: %w", err)
			}
			b, _ := json.Marshal(decrypted)
			cfg[field] = b
		}
	}

	if credentials, err = json.Marshal(cfg); err != nil {
		return nil, nil, fmt.Errorf("test connection: marshal config: %w", err)
	}
	if manifest.CheckCredentialRead() != nil {
		for _, field := range passwordFields {
			delete(cfg, field)
		}
	}
	if guest, err = json.Marshal(cfg); err != nil {
		return nil, nil, fmt.Errorf("test connection: marshal config: %w", err)
	}
	return guest, credentials, nil
}
