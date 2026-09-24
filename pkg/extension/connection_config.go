// pkg/extension/connection_config.go
package extension

import (
	"encoding/json"
	"fmt"
)

// HostConnectionConfigKey is the top-level key an asset's Config JSON reserves
// for host-owned connection settings (proxy chain, TLS) that its type turns on
// via connection.proxyChain / connection.tls in describe(). It is not part of
// the type's own configSchema, and the extension never sees it: every boundary
// that hands Config to the guest — ctx.AssetConfig() and validate_config — must
// strip it first with StripHostConnectionConfig.
const HostConnectionConfigKey = "__opskat_connection"

// StripHostConnectionConfig returns raw with HostConnectionConfigKey removed.
// raw is returned unchanged (including empty) when the key is absent. An error
// means raw is not valid JSON — a corrupt stored config, which must surface
// rather than silently pass through to the guest unstripped.
func StripHostConnectionConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse asset config: %w", err)
	}
	if _, ok := m[HostConnectionConfigKey]; !ok {
		return raw, nil
	}
	delete(m, HostConnectionConfigKey)
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal stripped asset config: %w", err)
	}
	return out, nil
}
