# VNC RA2 session encryption

> Status: Approved
> Owner: OpsKat maintainers
> Last updated: 2026-08-30

**Objective:** Let an OpsKat user select a VNC encryption policy, connect through the maintained `opskat/noVNC` fork using RA2-family authentication and transport encryption, and see the security mode that was actually negotiated.

**Hard invariant:** Existing VNC assets and noVNC callers that do not set an encryption policy keep their current server-order negotiation behaviour; a strict encryption policy never silently downgrades to an authentication-only or unencrypted session.

## Problem

1. **The current browser protocol stack cannot satisfy servers that require encrypted VNC sessions.** The maintained upstream baseline only registers RA2ne security type 6 (`core/rfb.js:62`) and its RSA-AES implementation ends after encrypted credential exchange (`core/ra2.js:138`); RA2 type 5 and RA2_256 type 129 require AES-EAX protection to remain active for `SecurityResult` and the subsequent RFB byte stream.
2. **OpsKat has no encryption-policy contract.** The committed `VNCConfig` has endpoint, credential, file-channel and proxy fields only (`internal/model/entity/asset_entity/asset.go:370`), while the frontend is pinned to `@novnc/novnc` 1.5.0 (`frontend/package.json:24`). A form-only switch would therefore promise security the connection cannot provide.
3. **Connection testing and real connection negotiate different protocols.** The backend test path implements only None and VNCAuth (`internal/service/vnc_svc/conntest.go:20`), whereas the real session delegates RFB authentication to noVNC (`frontend/src/components/vnc/VNCPanel.tsx:138`). A test can reject a configuration the real client supports, or pass without exercising the selected encryption policy.
4. **RSA server approval is not durable trust.** OpsKat displays the RSA fingerprint received from noVNC (`frontend/src/components/vnc/VNCPanel.tsx:181`), but requires approval again on every connection and cannot distinguish a first-seen key from a changed key.

## Actors and user stories

1. As an infrastructure operator, I want to require the strongest supported VNC session encryption, so that a server offering weaker methods cannot cause a silent downgrade.
2. As an operator managing mixed or legacy servers, I want compatibility-oriented preference modes, so that I can retain connectivity while seeing the security actually negotiated.
3. As an operator testing an asset, I want the test to use the same protocol implementation and policy as a real session, so that a successful test predicts a successful connection.
4. As a security-conscious operator, I want first-use RSA key confirmation and a prominent changed-key warning, so that credentials are not sent to an unapproved server identity.

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | Maintain the protocol work in the `opskat/noVNC` fork based on noVNC 1.7.0, then pin OpsKat to the verified fork revision. | RFB parsing, framebuffer decoding, input and clipboard already live in noVNC. Rejected: implementing a second VNC client or encryption proxy in Go — it would duplicate the state machine and create two protocol owners. |
| 2 | Support the complete public RSA-AES family: RA2 (5), RA2ne (6), RA2r (13), RA2_256 (129), RA2ne_256 (130) and RA2r_256 (133); preserve VNCAuth as the unencrypted baseline. | All six types are registered in the public RFB specification. Rejected: implementing only the four TigerVNC 1.16.2 types — servers may offer the two-step variants and a strict policy should recognize every standardized full-session RA2 mode. |
| 3 | Keep continuous AES-EAX as an internal transport transform below RFB message parsing, with serialized asynchronous encryption and decryption per direction. | Existing RFB code expects synchronous plaintext queues. Rejected: adding encryption calls to every RFB encoder and decoder — record ordering and nonce ownership would be duplicated across the client. |
| 4 | Add a generic ordered security-policy input to the fork, with server order preserved when no policy is supplied and within equal-preference groups. | OpsKat needs strict allow-lists and preference tiers without hard-coding an OpsKat policy enum into noVNC. Rejected: selecting the first supported server type unconditionally — strict and prefer modes could not be expressed safely. |
| 5 | Expose the successfully negotiated security result as an RFB event. | The configured preference is not evidence of the resulting transport. Rejected: infer security from the form value — fallback modes can negotiate a different type. |
| 6 | Store VNC RSA identities through the existing host-key persistence seam, keyed by host, port and a VNC RSA key type. | OpsKat already has durable host-key records and first/last-seen metadata. Rejected: localStorage or embedding fingerprints in each asset — trust would be browser-local, duplicated and unable to cover multiple assets for one endpoint consistently. |
| 7 | Replace the VNC-specific Go handshake test with a temporary real noVNC session shared with the normal VNC client helper. | This removes protocol drift. Rejected: extending the Go tester with RA2 — that would recreate the browser protocol and encryption stack. |
| 8 | Deliver in dependency order: verified fork revision first, then OpsKat integration pinned to that immutable revision. | OpsKat must not advertise a policy until the protocol dependency passes unit and live interoperability tests. |

## noVNC fork protocol contract

### RSA-AES variants

For an RFB 3.7/3.8 server offering a supported RSA-AES type, the client performs server-key approval before sending a username or password. RA2, RA2ne and RA2r use 16-byte randoms, SHA-1-derived 128-bit AES-EAX keys and 20-byte transcript hashes. RA2_256, RA2ne_256 and RA2r_256 use 32-byte randoms, SHA-256-derived 256-bit AES-EAX keys and 32-byte transcript hashes.

The encrypted record format is a two-byte plaintext length, ciphertext of that length and a 16-byte EAX tag. The length is authenticated associated data. Each direction owns an independent 128-bit little-endian record counter starting at zero. A counter advances only after that direction successfully authenticates or emits one record; concurrent operations may not reuse a counter.

RA2 and RA2_256 keep the established ciphers active after credentials. Their first generic inbound encrypted record is `SecurityResult`, and all subsequent RFB traffic in both directions remains encrypted. RA2ne and RA2ne_256 discard the handshake ciphers after credentials, so post-authentication RFB traffic remains plaintext.

RA2r and RA2r_256 perform the standardized second key-derivation round after credentials: client and server exchange fresh random values inside authenticated AES-EAX records, derive a new directional key pair using the variant's SHA-1/128-bit or SHA-256/256-bit algorithm, reset both record counters to zero and use only the new ciphers for `SecurityResult` and subsequent RFB traffic. Failure during rekeying terminates authentication and never falls back to the first key pair or plaintext.

Outbound plaintext larger than one transport record is split without changing the RFB byte stream observed above the transform. Fragmented records, multiple records in one channel message and RFB messages spanning records all produce the same continuous plaintext stream.

A malformed length, invalid EAX tag, decryption error, encryption error or asynchronous channel failure terminates the connection as an unclean security failure. No unauthenticated plaintext is published to RFB, no success state is emitted, and pending asynchronous work cannot send or advance a disconnected client.

### Activation and compatibility

For RA2 and RA2_256, the generic encrypted transport activates only after the encrypted credential record has been fully queued through the handshake path and before `SecurityResult` can be parsed. For RA2r and RA2r_256, activation occurs only after the second random exchange, replacement-key derivation and counter reset complete. Any unread bytes already delivered by the raw channel at activation are treated as encrypted transport bytes rather than plaintext.

The wire value 129 is RA2_256 when offered as a top-level RFB security type. TightVNC UnixLogon continues to use its wire subtype 129 but is represented by a distinct internal authentication state, so existing Tight authentication does not regress.

A caller that omits the new security policy keeps current noVNC behaviour: choose the first supported type in server preference order. RFB 3.3 servers still choose the wire type, but the client rejects a server-selected type excluded by a strict policy.

### Security policy and result API

The fork accepts an ordered set of allowed security preference groups. It chooses the first group containing a server-offered supported type, then preserves server order inside that group. Supplying no groups means unrestricted current server-order negotiation. Supplying groups that contain no offered type fails with an unsupported-policy error rather than falling back outside the groups.

After authentication succeeds, the RFB object emits one negotiated-security event before the normal connected event. Its detail identifies at least the numeric security type, a stable protocol name, whether authentication was cryptographically protected, whether the post-authentication RFB transport is encrypted, and the AES key size when applicable. The existing credential and server-verification events and `approveServer()` flow remain compatible.

## OpsKat encryption policy

`VNCConfig` gains an optional `encryption` value with these stable serialized tokens:

| Token | User-visible meaning | Allowed/preferred result |
|---|---|---|
| `server` | Let server choose | No client preference groups; preserve server order among all fork-supported types. |
| `always_maximum` | Always maximum encryption | Allow only the AES-256 full-session types RA2r_256 and RA2_256, preserving server order between them. Failure is explicit if neither is available. |
| `always_on` | Always encrypt session | Allow all full-session RA2/RA2r 128-bit and 256-bit types. Authentication-only and plaintext transports are rejected. |
| `prefer_on` | Prefer encrypted session | Prefer all full-session RA2/RA2r types; if unavailable, allow all remaining supported types in server order. |
| `prefer_off` | Prefer unencrypted session | Prefer all supported non-full-session types in server order; use a full-session RA2/RA2r type only when no non-full-session type is available. |

A missing or empty value is exactly `server`. Existing assets therefore connect as before. Saving `server` may omit the field; readers must not require a migration. Unknown non-empty tokens are invalid at the asset boundary and are not silently converted to another policy.

The asset form adds a dedicated Advanced tab after Connection, Tunnel and Files. It contains the encryption-policy selector and defaults to Server chooses. Option copy stays at the policy level and does not expose RA2 protocol identifiers; a short selected-value hint is shown only when it clarifies downgrade behaviour. The default Connection tab remains focused on endpoint and credentials. The asset detail view shows the configured policy.

## Server identity trust

When noVNC presents an RSA public key, OpsKat computes a SHA-256 fingerprint and checks the durable host-key record for the session's actual host and port before approving the key.

- If the exact key is already trusted, OpsKat updates its last-seen time, approves it automatically and continues without prompting.
- If no key is stored, OpsKat shows the endpoint and new fingerprint. “Trust and connect” persists the key before approval; cancel disconnects.
- If a different key is stored, OpsKat shows a high-risk changed-key state containing both old and new fingerprints. Replacing the stored key requires an explicit destructive confirmation; cancel disconnects.
- Persistence or verification errors stop the connection and remain visible. They are not treated as first use.

No username or password is sent before approval. Trust is shared by assets that resolve to the same host, port and VNC RSA key type, while SSH host keys and other key types remain independent.

## Connection flow and visible state

Normal connections and tests use one frontend VNC client helper responsible for constructing RFB, applying the policy, handling credentials, server trust, security failures and cleanup. The Go VNC manager remains responsible only for resolving credentials/proxy chains, dialing the target, owning the session and forwarding bytes.

A normal session displays the actually negotiated mode in the existing VNC status area after authentication:

- RA2r_256 or RA2_256: AES-256 session encrypted;
- RA2r or RA2: AES-128 session encrypted;
- RA2ne_256: AES-256 authentication only, session unencrypted;
- RA2ne: AES-128 authentication only, session unencrypted;
- VNCAuth/None: session unencrypted.

The indication includes text and protocol name rather than color alone. Authentication-only and unencrypted modes are visibly cautionary even when allowed by `server`, `prefer_on` or `prefer_off`.

The asset form's Test connection action creates a temporary VNC transport from the unsaved form configuration, runs the same helper through successful authentication and `ServerInit`, reports the negotiated result, then disconnects and deletes the temporary session. It uses the same server-key trust flow. Canceling or closing the form terminates the temporary frontend client and backend session and ignores late asynchronous completion.

Strict-policy rejection, unsupported security types, credential requests not satisfied by the configured credential, key rejection/change, MAC failure and transport closure surface distinct actionable errors. A configured preference is never displayed as if it were the negotiated result.

## Out of scope

- RealVNC RA4 and other private RFB 5-only extensions.
- VeNCrypt X509/TLS support beyond the fork's existing capabilities.
- Treating WebSocket/TCP tunnel encryption as equivalent to RA2 session encryption.
- Publishing the fork to the public npm registry; OpsKat pins an immutable Git revision from `opskat/noVNC`.
- Redesigning the VNC viewer, toolbar, generic asset form or host-key management page beyond the states required above.

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| AES-EAX record cipher | AES-128/AES-256 vectors, authenticated length, little-endian counter/carry, tamper rejection and no counter advance on failed authentication | Existing RSA-AES implementation has only indirect transcript coverage. |
| noVNC channel transform | Fragmented/coalesced records, cross-record RFB bytes, 8192-byte splitting, serialized async send/decrypt, activation with unread ciphertext, failure and disconnect cleanup | `tests/test.websock.js` queue and fake-channel tests. |
| noVNC RFB negotiation | Types 5/6/13/129/130/133, 128/256 derivation, two-step rekey and counter reset, encrypted versus raw `SecurityResult`, strict rejection under RFB 3.3/3.8, negotiated-security event and Tight UnixLogon 129 regression | RSA-AES cases in `tests/test.rfb.js`. |
| TigerVNC interoperability lab | Real authentication, `ServerInit`, framebuffer update, keyboard, pointer and clipboard against ports 5911–5915; expected success/rejection for every OpsKat policy across RA2, RA2ne, RA2_256, RA2ne_256 and VNCAuth | TigerVNC 1.16.2 lab at `/opt/opskat-vnc-ra2-lab` on the authorized `local-docker` asset. |
| Independent RA2r protocol oracle | RA2r and RA2r_256 second-random exchange, replacement key derivation, counter reset, encrypted `SecurityResult`, framebuffer and client-message flow on two dedicated lab ports | A verification-only Python server using an independent AES-EAX/RSA implementation; TigerVNC 1.16.2 has no RA2r server implementation. |
| Wire observation | Known framebuffer/input/clipboard markers are not visible after authentication on every RA2/RA2r full-session type and remain observably plaintext on authentication-only/baseline controls | Lab packet capture; no committed prior art. |
| OpsKat config boundary | Missing-field compatibility, five-token round trip, unknown-token rejection and form/detail copy | Existing VNC entity and config serializer tests. |
| Host-key service boundary | First use, exact match, changed key, persistence failure, key-type isolation and last-seen update | Existing SSH host-key repository/service tests. |
| Shared frontend VNC client | Normal and temporary sessions apply the same policy, trust and negotiated-result handling; cancellation closes both sides | Existing `VNCPanel` and `VNCConfigSection` tests. |
| Real Wails runtime | Create/edit assets, first-use and changed-key prompts, five-port connection matrix, status indication, test-connection parity and cleanup | `make dev-sandbox` plus the project drive/oracle workflow. |

The fork must pass its full `npm test` and `npm run lint` gates. OpsKat must pass targeted Go/frontend tests, frontend lint, `go test ./...`, the full frontend test suite and a production build. Runtime evidence is recorded under the spec slug and includes the exact fork commit pinned by OpsKat, the lab probe output, connection/policy matrix, packet-capture verdicts and any environment limitation. RealVNC Server-specific RFB 5 behaviour cannot be automated with the current lab and is covered only as a documented compatibility gap, not reported as verified.

## Open questions
