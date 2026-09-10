// pkg/extension/host_capability.go
package extension

// capHost decorates a HostProvider with per-call capability enforcement.
//
// Only OpenIO needs a decision — everything else is already scoped to the
// extension by construction (KV is namespaced per extension, asset config goes
// through the credentials capability upstream). Embedding the inner provider
// keeps this file a single middleware instead of a pass-through for every
// method the interface happens to have.
type capHost struct {
	HostProvider
	manifest *Manifest
	extDir   string
}

// NewCapabilityHost wraps inner with capability enforcement.
func NewCapabilityHost(inner HostProvider, manifest *Manifest, extDir string) HostProvider {
	return &capHost{HostProvider: inner, manifest: manifest, extDir: extDir}
}

func (c *capHost) OpenIO(params IOOpenParams) (*IOResource, error) {
	switch params.Type {
	case "file":
		switch params.Mode {
		case "read":
			if err := c.manifest.CheckFSRead(params.Path, c.extDir); err != nil {
				return nil, err
			}
		case "write":
			if err := c.manifest.CheckFSWrite(params.Path, c.extDir); err != nil {
				return nil, err
			}
		}
	case "http":
		if err := c.manifest.CheckHTTPURL(params.URL, c.manifest.Capabilities.Tunnel); err != nil {
			return nil, err
		}
		// Pass tunnel capability to dial-time guard.
		params.AllowPrivate = c.manifest.Capabilities.Tunnel
	case "tcp":
		// TCP IO is currently reserved for first-party extensions (e.g. Kafka). Until a
		// manifest.Capabilities.TCP + CheckTCPAddr gate lands (post-Phase 1), no per-call
		// enforcement is done here — the wazero host module exposes this to every loaded
		// extension. Do not mark the capHost as publicly safe for third-party extensions
		// until that gate is in place.
	}
	return c.HostProvider.OpenIO(params)
}
