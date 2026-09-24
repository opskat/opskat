package extension

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// assetConfigGetter implements extension.AssetConfigGetter for one extension.
//
// The asset id it receives is the one the host scoped the call to (the guest
// cannot name one — see pkg/extension opAssetGetConfig). It still refuses any
// asset whose type the extension does not register: a tool or action reaching
// this with a builtin or another extension's asset is a host-side mis-scoping,
// and serving it would hand that asset's config — credentials included — to an
// extension that has no claim on it.
type assetConfigGetter struct {
	ext     *Extension
	extName string
}

func (g *assetConfigGetter) GetAssetConfig(assetID int64) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	caller, asset, err := ownedAsset(ctx, g.ext.service, g.extName, assetID)
	if err != nil {
		return nil, err
	}
	if asset.Config == "" {
		return json.RawMessage("{}"), nil
	}

	logger.Ctx(ctx).Info("extension accessed asset config",
		zap.String("extension", caller.Name),
		zap.Int64("asset_id", assetID),
		zap.String("asset_type", asset.Type),
		zap.Bool("plaintext_allowed", caller.Manifest.CheckCredentialRead() == nil),
	)

	// The asset's stored config may carry the host's reserved connection-settings
	// key (proxy chain, TLS) — never part of the type's own configSchema, and
	// never for the extension to see.
	stripped, err := extension.StripHostConnectionConfig(json.RawMessage(asset.Config))
	if err != nil {
		return nil, fmt.Errorf("strip asset %d host connection config: %w", assetID, err)
	}
	return decryptConfigPasswordFields(stripped, asset.Type, caller)
}

// AssetCredentialValues serves host-side credential injection (the asset type's
// describe() auth bindings): the named fields as strings, password fields
// decrypted regardless of the extension's credentials capability, since the
// result is rendered into requests by the host and never handed to the guest.
// Only the named fields are decrypted — a stored secret the active auth group
// does not reference cannot fail the request.
func (g *assetConfigGetter) AssetCredentialValues(ctx context.Context, assetID int64, fields []string) (map[string]string, error) {
	caller, asset, err := ownedAsset(ctx, g.ext.service, g.extName, assetID)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(fields))
	if asset.Config == "" {
		return values, nil
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(asset.Config), &cfg); err != nil {
		return nil, fmt.Errorf("parse %s config: %w", asset.Type, err)
	}
	passwords := map[string]bool{}
	for _, f := range extension.PasswordFieldsFromSchema(caller.Manifest.AssetTypeDef(asset.Type).ConfigSchema) {
		passwords[f] = true
	}
	for _, field := range fields {
		raw, ok := cfg[field]
		if !ok || string(raw) == "null" {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			value = string(raw) // a number or bool renders as its JSON text
		}
		if passwords[field] && value != "" {
			if value, err = decryptPasswordField(asset.Type, field, value); err != nil {
				return nil, err
			}
		}
		values[field] = value
	}
	logger.Ctx(ctx).Debug("extension asset credentials resolved for injection",
		zap.String("extension", caller.Name),
		zap.Int64("asset_id", assetID),
		zap.Strings("fields", fields),
	)
	return values, nil
}

// ownedAsset loads assetID on behalf of extName and confirms the asset's type is
// one extName itself registers. Every path that hands an asset — or its config —
// to an extension goes through here, so "which extension may see this asset" is
// answered once, by the asset's stored type rather than by whoever asked.
func ownedAsset(ctx context.Context, svc *extension_svc.Service, extName string, assetID int64) (*extension.Extension, *extension_svc.HostAssetConfig, error) {
	caller := svc.Bridge().Get(extName)
	if caller == nil {
		return nil, nil, fmt.Errorf("extension %q not loaded", extName)
	}
	asset, err := svc.GetHostAssetConfig(ctx, assetID)
	if err != nil {
		return nil, nil, fmt.Errorf("asset %d not found: %w", assetID, err)
	}
	if caller.Manifest.AssetTypeDef(asset.Type) == nil {
		return nil, nil, fmt.Errorf("asset %d (type %q) does not belong to extension %q", assetID, asset.Type, extName)
	}
	return caller, asset, nil
}

// fileDialogOpener implements extension.FileDialogOpener
type fileDialogOpener struct {
	ctx context.Context // Wails app context
}

func (o *fileDialogOpener) FileDialog(dialogType string, opts extension.DialogOptions) (string, error) {
	switch dialogType {
	case "open":
		return wailsRuntime.OpenFileDialog(o.ctx, wailsRuntime.OpenDialogOptions{
			Title:   opts.Title,
			Filters: toWailsFilters(opts.Filters),
		})
	case "save":
		return wailsRuntime.SaveFileDialog(o.ctx, wailsRuntime.SaveDialogOptions{
			Title:           opts.Title,
			DefaultFilename: opts.DefaultName,
			Filters:         toWailsFilters(opts.Filters),
		})
	default:
		return "", fmt.Errorf("unknown dialog type: %q", dialogType)
	}
}

func toWailsFilters(filters []string) []wailsRuntime.FileFilter {
	if len(filters) == 0 {
		return nil
	}
	result := make([]wailsRuntime.FileFilter, 0, len(filters))
	for _, f := range filters {
		result = append(result, wailsRuntime.FileFilter{
			DisplayName: f,
			Pattern:     f,
		})
	}
	return result
}

// kvStore implements extension.KVStore, scoped to one extension
type kvStore struct {
	ext     *Extension
	extName string
}

func (s *kvStore) Get(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.ext.service.GetHostKV(ctx, s.extName, key)
}

func (s *kvStore) Set(key string, value []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.ext.service.SetHostKV(ctx, s.extName, key, value)
}

// actionEventHandler implements extension.ActionEventHandler
type actionEventHandler struct {
	ctx     context.Context // Wails app context
	extName string
}

// OnActionEvent forwards one action event to the frontend.
//
// invocationId rides along because one handler serves every concurrent run of
// this extension: without it a listener sees the extension's events merged into
// one stream and attributes another upload's progress to its own.
func (h *actionEventHandler) OnActionEvent(invocationID, eventType string, data json.RawMessage) error {
	wailsRuntime.EventsEmit(h.ctx, "ext:action:event", map[string]any{
		"extension":    h.extName,
		"invocationId": invocationID,
		"eventType":    eventType,
		"data":         json.RawMessage(data),
	})
	return nil
}

// getDecryptedExtConfig returns the config of an asset extName owns, with
// password fields decrypted per extName's credentials capability.
func getDecryptedExtConfig(svc *extension_svc.Service, extName string, assetID int64) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, asset, err := ownedAsset(ctx, svc, extName, assetID)
	if err != nil {
		return "", err
	}
	if asset.Config == "" {
		return "{}", nil
	}
	decrypted, err := decryptConfigPasswordFields(json.RawMessage(asset.Config), asset.Type, owner)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

// decryptConfigPasswordFields decrypts the fields assetType's configSchema marks
// format:"password". Whether the plaintext or an opaque handle comes back is
// decided by ext — the extension the config is being handed to, which
// ownedAsset has already confirmed registers assetType.
func decryptConfigPasswordFields(raw json.RawMessage, assetType string, ext *extension.Extension) (json.RawMessage, error) {
	def := ext.Manifest.AssetTypeDef(assetType)
	if def == nil || len(def.ConfigSchema) == 0 {
		return raw, nil
	}
	passwordFields := extension.PasswordFieldsFromSchema(def.ConfigSchema)
	if len(passwordFields) == 0 {
		return raw, nil
	}

	allowPlaintext := ext.Manifest.CheckCredentialRead() == nil

	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s config: %w", assetType, err)
	}

	for _, field := range passwordFields {
		val, ok := cfg[field]
		if !ok {
			continue
		}
		var encrypted string
		if err := json.Unmarshal(val, &encrypted); err != nil || encrypted == "" {
			continue
		}
		// A field that will not decrypt fails the whole read: passing the stored
		// value through would hand the guest ciphertext and skip the credentials
		// decision entirely.
		decrypted, err := decryptPasswordField(assetType, field, encrypted)
		if err != nil {
			return nil, err
		}
		if allowPlaintext {
			b, _ := json.Marshal(decrypted)
			cfg[field] = b
		} else {
			handle := credentialHandleFor(ext.Name, assetType, field, encrypted)
			handleJSON, _ := json.Marshal(map[string]string{
				"__credential_handle": handle,
			})
			cfg[field] = handleJSON
		}
	}
	return json.Marshal(cfg)
}

func decryptPasswordField(assetType, field, encrypted string) (string, error) {
	decrypted, err := credential_svc.Default().Decrypt(encrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt password field %q of %s config: %w", field, assetType, err)
	}
	return decrypted, nil
}

// credentialHandleFor creates an opaque handle for a credential field.
func credentialHandleFor(extName, assetType, field, encrypted string) string {
	h := sha256.Sum256([]byte(extName + ":" + assetType + ":" + field + ":" + encrypted))
	return "cred_" + hex.EncodeToString(h[:8])
}

// assetDialer implements extension.AssetDialer for one extension.
type assetDialer struct {
	ext     *Extension
	extName string
}

// hostConnectionConfig is the shape stored under the asset's Config JSON at
// extension.HostConnectionConfigKey — host-owned settings for the connection
// items an asset type opts into via connection.proxyChain / connection.tls.
// The extension never sees this key (stripped in GetAssetConfig above and in
// Plugin.ValidateConfig); only DialContextFor reads it.
type hostConnectionConfig struct {
	ProxyChain *asset_entity.ProxyChainConfig `json:"proxyChain,omitempty"`
	TLS        *hostTLSConfig                 `json:"tls,omitempty"`
	// SSHTunnelID is read only for an ad-hoc "test connection" call
	// (DialContextForConfig): a saved asset's tunnel lives on its own
	// SSHTunnelID column, never under this key, so this field is always
	// absent from a stored asset's Config.
	SSHTunnelID int64 `json:"sshTunnelId,omitempty"`
}

type hostTLSConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`
	ServerName string `json:"serverName,omitempty"`
	CAFile     string `json:"caFile,omitempty"`
	CertFile   string `json:"certFile,omitempty"`
	KeyFile    string `json:"keyFile,omitempty"`
}

// parseHostConnectionConfig reads the reserved key out of an asset's stored
// Config JSON. A nil result (no error) means the asset has none configured.
func parseHostConnectionConfig(configJSON string) (*hostConnectionConfig, error) {
	if configJSON == "" {
		return nil, nil
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(configJSON), &wrapper); err != nil {
		return nil, fmt.Errorf("parse asset config: %w", err)
	}
	raw, ok := wrapper[extension.HostConnectionConfigKey]
	if !ok {
		return nil, nil
	}
	var cfg hostConnectionConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse host connection config: %w", err)
	}
	return &cfg, nil
}

// DialContextFor resolves the connection path and TLS settings of an asset the
// extension owns. Only the items its asset type declares take effect: a proxy
// chain, SSH tunnel or TLS config set on an asset whose type does not declare
// the matching connection item is ignored. Proxy chain and SSH tunnel dial
// through the same proxy-chain machinery built-in types use; TLS is built with
// the same connpool helper, so a bad CA/cert file or a failed handshake fails
// exactly as it would for a built-in type — never falling back to a direct or
// unverified connection.
func (d *assetDialer) DialContextFor(ctx context.Context, assetID int64) (extension.DialContextFunc, *tls.Config, string, error) {
	owner, asset, err := ownedAsset(ctx, d.ext.service, d.extName, assetID)
	if err != nil {
		return nil, nil, "", err
	}
	conn := owner.Manifest.AssetTypeDef(asset.Type).Connection
	if conn == nil {
		return nil, nil, "", nil
	}

	hostCfg, err := parseHostConnectionConfig(asset.Config)
	if err != nil {
		return nil, nil, "", fmt.Errorf("asset %d connection config: %w", assetID, err)
	}

	var chain *asset_entity.ProxyChainConfig
	if conn.ProxyChain && hostCfg != nil {
		chain = hostCfg.ProxyChain
	}
	var tunnelID int64
	if conn.SSHTunnel {
		tunnelID = asset.SSHTunnelID
	}
	var tlsSettings *hostTLSConfig
	if conn.TLS && hostCfg != nil {
		tlsSettings = hostCfg.TLS
	}
	fingerprint := connectionFingerprint(tunnelID, chain, tlsSettings)

	dial, tlsConfig, err := resolveDialAndTLS(ctx, asset.Type, chain, tunnelID, tlsSettings,
		zap.String("extension", d.extName), zap.Int64("assetID", assetID))
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w (asset %d)", err, assetID)
	}
	return dial, tlsConfig, fingerprint, nil
}

// DialContextForConfig resolves the dial path and TLS settings for an
// ad-hoc "test connection" call (extension.AdHocAssetConfig): the asset
// form's own submitted connection settings, never a row read from the
// database — a new asset has none yet, and a saved one being tested must
// honor its unsaved edits rather than what is on disk. Only the items
// assetType's own declared connection support apply, exactly as
// DialContextFor enforces for a stored asset; it shares that method's dial
// and TLS building (resolveDialAndTLS), just fed from adhoc instead of a
// database row, and never returns a fingerprint — the result is never cached.
func (d *assetDialer) DialContextForConfig(ctx context.Context, assetType string, adhoc *extension.AdHocAssetConfig) (extension.DialContextFunc, *tls.Config, error) {
	owner := d.ext.service.Bridge().Get(d.extName)
	if owner == nil {
		return nil, nil, fmt.Errorf("extension %q not loaded", d.extName)
	}
	def := owner.Manifest.AssetTypeDef(assetType)
	if def == nil {
		return nil, nil, fmt.Errorf("extension %q does not register asset type %q", d.extName, assetType)
	}
	conn := def.Connection
	if conn == nil {
		return nil, nil, nil
	}

	var chain *asset_entity.ProxyChainConfig
	if conn.ProxyChain && len(adhoc.ProxyChain) > 0 {
		if err := json.Unmarshal(adhoc.ProxyChain, &chain); err != nil {
			return nil, nil, fmt.Errorf("parse proxy chain: %w", err)
		}
	}
	var tunnelID int64
	if conn.SSHTunnel {
		tunnelID = adhoc.SSHTunnelID
	}
	var tlsSettings *hostTLSConfig
	if conn.TLS && len(adhoc.TLS) > 0 {
		if err := json.Unmarshal(adhoc.TLS, &tlsSettings); err != nil {
			return nil, nil, fmt.Errorf("parse TLS config: %w", err)
		}
	}

	dial, tlsConfig, err := resolveDialAndTLS(ctx, assetType, chain, tunnelID, tlsSettings,
		zap.String("extension", d.extName), zap.String("assetType", assetType))
	if err != nil {
		return nil, nil, fmt.Errorf("%w (asset type %s)", err, assetType)
	}
	return dial, tlsConfig, nil
}

// resolveDialAndTLS builds the dial function and TLS config from a resolved
// proxy chain / SSH tunnel / TLS combination — the connpool work shared by
// DialContextFor (from a stored asset) and DialContextForConfig (from an
// ad-hoc test-connection call). ident identifies the call in the log lines
// (asset id for a stored one, asset type for an ad-hoc one); which fits is
// the caller's call, not this function's.
func resolveDialAndTLS(ctx context.Context, assetType string, chain *asset_entity.ProxyChainConfig, tunnelID int64, tlsSettings *hostTLSConfig, ident ...zap.Field) (extension.DialContextFunc, *tls.Config, error) {
	var dial extension.DialContextFunc
	if effective := asset_entity.EffectiveProxyChain(chain, tunnelID, nil); effective != nil {
		d, err := connpool.ProxyChainDialContext(ctx, effective)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve connection path: %w", err)
		}
		dial = d
		logger.Ctx(ctx).Info("extension asset dials through proxy chain",
			append(append([]zap.Field{}, ident...), zap.Int("hops", len(effective.Layers)))...)
	}

	var tlsConfig *tls.Config
	if tlsSettings != nil && tlsSettings.Enabled {
		cfg, err := connpool.BuildTLSConfig(assetType, connpool.TLSFields{
			ServerName: tlsSettings.ServerName,
			Insecure:   tlsSettings.Insecure,
			CAFile:     tlsSettings.CAFile,
			CertFile:   tlsSettings.CertFile,
			KeyFile:    tlsSettings.KeyFile,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("TLS config: %w", err)
		}
		tlsConfig = cfg
		logger.Ctx(ctx).Info("extension asset applies TLS",
			append(append([]zap.Field{}, ident...), zap.Bool("insecure", tlsSettings.Insecure))...)
	}

	return dial, tlsConfig, nil
}

// connectionFingerprint identifies the resolved connection settings behind a
// dial: the SSH tunnel asset id, proxy chain and TLS settings. Two calls that
// produce the same fingerprint dial the same way, so a cached HTTP client
// built for one (see DefaultHostProvider.httpClients) is safe to reuse for the
// other; the fingerprint changing is the signal that the cached client must be
// rebuilt instead.
func connectionFingerprint(tunnelID int64, chain *asset_entity.ProxyChainConfig, tlsSettings *hostTLSConfig) string {
	payload, _ := json.Marshal(struct {
		Tunnel int64                          `json:"tunnel,omitempty"`
		Chain  *asset_entity.ProxyChainConfig `json:"chain,omitempty"`
		TLS    *hostTLSConfig                 `json:"tls,omitempty"`
	}{tunnelID, chain, tlsSettings})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
