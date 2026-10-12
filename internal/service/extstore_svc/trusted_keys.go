package extstore_svc

// officialPublicKeys are the ed25519 public keys (standard base64 of the 32-byte
// key) the app trusts to sign the official index.json. A signature from any one
// of them is accepted, so a new key can be added here a release before the
// publisher switches to it, and the old one removed once no supported app needs
// it. The private half of each key is the extensions repo's
// EXTENSION_INDEX_SIGNING_KEY secret.
var officialPublicKeys = []string{
	"IATec+PQn950PsQ0JbHpohxfmUtS5AJzcKrT3twnNM8=",
}
