package extension_svc

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/extension"
)

// HostConfig returns the host capabilities of extName that need no user interface:
// the config (and credentials) of the assets it owns, its KV store and the connection
// path to those assets. Every process that runs extensions builds its providers from
// this — the desktop app and opsctl alike — and adds only what it can offer beyond it:
// the desktop its file dialog and action events, opsctl a file dialog that explains
// there is none.
func (s *Service) HostConfig(extName string) extension.DefaultHostConfig {
	return extension.DefaultHostConfig{
		Logger:        s.logger,
		AssetConfigs:  &assetConfigGetter{svc: s, extName: extName},
		KV:            &kvStore{svc: s, extName: extName},
		AssetDialer:   &assetDialer{svc: s, extName: extName},
		ExtensionName: extName,
	}
}

// assetConfigGetter implements extension.AssetConfigGetter for one extension.
//
// The asset id it receives is the one the host scoped the call to (the guest
// cannot name one — see pkg/extension opAssetGetConfig). It still refuses any
// asset whose type the extension does not register: a tool or action reaching
// this with a builtin or another extension's asset is a host-side mis-scoping,
// and serving it would hand that asset's config — credentials included — to an
// extension that has no claim on it.
type assetConfigGetter struct {
	svc     *Service
	extName string
}

func (g *assetConfigGetter) GetAssetConfig(assetID int64) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	caller, asset, _, err := g.svc.OwnedTypedAsset(ctx, g.extName, assetID)
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
	caller, asset, def, err := g.svc.OwnedTypedAsset(ctx, g.extName, assetID)
	if err != nil {
		return nil, err
	}
	if asset.Config == "" {
		return map[string]string{}, nil
	}
	values, err := extension.ConfigFieldValues(json.RawMessage(asset.Config), fields)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", asset.Type, err)
	}
	for _, field := range extension.PasswordFieldsFromSchema(def.ConfigSchema) {
		if value := values[field]; value != "" {
			if values[field], err = decryptPasswordField(asset.Type, field, value); err != nil {
				return nil, err
			}
		}
	}
	logger.Ctx(ctx).Debug("extension asset credentials resolved for injection",
		zap.String("extension", caller.Name),
		zap.Int64("asset_id", assetID),
		zap.Strings("fields", fields),
	)
	return values, nil
}

// OwnedTypedAsset loads assetID on behalf of extName and confirms the asset's type
// is one extName itself registers, returning that type's declaration. Every path
// that hands an asset — or its config — to an extension goes through here (its
// config, credentials, form and connection path, all read through the type's
// describe() declaration, and the asset a call from the extension's frontend is
// scoped to), so "which extension may see this asset" is answered once, by the
// asset's stored type rather than by whoever asked. A database asset of a driver
// extName provides is not one of them: its config, password and reach path go to
// the driver only in the open call of a session.
func (s *Service) OwnedTypedAsset(ctx context.Context, extName string, assetID int64) (*extension.Extension, *HostAssetConfig, *extension.AssetTypeDef, error) {
	caller := s.Bridge().Get(extName)
	if caller == nil {
		return nil, nil, nil, fmt.Errorf("extension %q not loaded", extName)
	}
	asset, err := s.GetHostAssetConfig(ctx, assetID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("asset %d not found: %w", assetID, err)
	}
	def := caller.Manifest.AssetTypeDef(asset.Type)
	if def == nil {
		return nil, nil, nil, fmt.Errorf("asset %d (type %q) does not belong to extension %q", assetID, asset.Type, extName)
	}
	return caller, asset, def, nil
}

// kvStore implements extension.KVStore, scoped to one extension.
type kvStore struct {
	svc     *Service
	extName string
}

func (s *kvStore) Get(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.svc.GetHostKV(ctx, s.extName, key)
}

func (s *kvStore) Set(key string, value []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.svc.SetHostKV(ctx, s.extName, key, value)
}

// FormConfig returns the config of an asset extName owns for the host's
// configuration form. Password fields come back as plaintext only when extName
// may read credentials; otherwise they are withheld (omitted). The form shares
// its webview with extName's pages, which can call this themselves, so the
// credentials decision applies here as it does to ctx.AssetConfig() — but the
// guest's opaque handle is no use to a form, which would render it (and save it
// back) as a value. The form instead shows a withheld field as already set and
// keeps its stored ciphertext on save.
func (s *Service) FormConfig(ctx context.Context, extName string, assetID int64) (string, error) {
	owner, asset, _, err := s.OwnedTypedAsset(ctx, extName, assetID)
	if err != nil {
		return "", err
	}
	if asset.Config == "" {
		return "{}", nil
	}
	raw := json.RawMessage(asset.Config)
	if owner.Manifest.CheckCredentialRead() != nil {
		withheld, err := withholdPasswordFields(raw, asset.Type, owner)
		if err != nil {
			return "", err
		}
		return string(withheld), nil
	}
	decrypted, err := decryptConfigPasswordFields(raw, asset.Type, owner)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

// withholdPasswordFields drops the fields assetType's configSchema marks
// format:"password" from raw.
func withholdPasswordFields(raw json.RawMessage, assetType string, ext *extension.Extension) (json.RawMessage, error) {
	def := ext.Manifest.AssetTypeDef(assetType)
	passwordFields := extension.PasswordFieldsFromSchema(def.ConfigSchema)
	if len(passwordFields) == 0 {
		return raw, nil
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s config: %w", assetType, err)
	}
	for _, field := range passwordFields {
		delete(cfg, field)
	}
	return json.Marshal(cfg)
}

// decryptConfigPasswordFields decrypts the fields assetType's configSchema marks
// format:"password". Whether the plaintext or an opaque handle comes back is
// decided by ext — the extension the config is being handed to, which
// OwnedTypedAsset has already confirmed registers assetType.
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
	svc     *Service
	extName string
}

// hostConnectionConfig is what the reserved key (extension.HostConnectionConfigKey)
// of an asset's Config JSON holds. The extension never sees this key (stripped in
// GetAssetConfig above and in Plugin.ValidateConfig); only DialContextFor reads it.
type hostConnectionConfig struct {
	asset_entity.ExtensionConnectionConfig
	// SSHTunnelID is read only for an ad-hoc "test connection" call
	// (DialContextForConfig): a saved asset's tunnel lives on its own
	// SSHTunnelID column, never under this key, so this field is always
	// absent from a stored asset's Config.
	SSHTunnelID int64 `json:"sshTunnelId,omitempty"`
}

type hostTLSConfig = asset_entity.ExtensionTLSConfig

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
// the same connpool helper, so a bad CA/client certificate or a failed handshake fails
// exactly as it would for a built-in type — never falling back to a direct or
// unverified connection.
//
// ctx is the tool call's: an SSH hop that asks for MFA is answered by whatever
// responder the caller put on it (opsctl's terminal / desktop prompt), exactly as
// for a built-in type dialed through the same tunnel.
func (d *assetDialer) DialContextFor(ctx context.Context, assetID int64) (extension.DialContextFunc, *tls.Config, string, error) {
	_, asset, def, err := d.svc.OwnedTypedAsset(ctx, d.extName, assetID)
	if err != nil {
		return nil, nil, "", err
	}
	conn := def.Connection
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
	owner := d.svc.Bridge().Get(d.extName)
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
	// EffectiveProxyChain lets a non-empty chain win over the tunnel; for a type
	// declaring both, that would silently dial without the SSH hop the user set.
	if tunnelID > 0 && chain != nil && len(chain.Layers) > 0 {
		return nil, nil, fmt.Errorf("resolve connection path: both an SSH tunnel and a proxy chain are set; add the SSH hop to the proxy chain, or clear one of them")
	}
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
		cfg, err := connpool.BuildAssetTLSConfig(assetType, connpool.TLSFields{
			ServerName: tlsSettings.ServerName,
			Insecure:   tlsSettings.Insecure,
			CAFile:     tlsSettings.CAFile,
			CertFile:   tlsSettings.CertFile,
			KeyFile:    tlsSettings.KeyFile,
			CAPEM:      tlsSettings.CACert,
			CertPEM:    tlsSettings.ClientCert,
			KeyPEM:     tlsSettings.ClientKey,
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
