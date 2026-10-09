package extstore_svc

// officialPublicKeys are the ed25519 public keys (standard base64 of the 32-byte
// key) the app trusts to sign the official index.json. A signature from any one
// of them is accepted, so a new key can be added here a release before the
// publisher switches to it, and the old one removed once no supported app needs
// it.
//
// RELEASE BLOCKER: the official key pair has not been generated yet. The
// maintainer generates it, stores the private seed as the extensions repo's
// EXTENSION_INDEX_SIGNING_KEY secret and adds the public half here before the
// store ships. While this list is empty every index fails verification and the
// store shows "signature invalid" — an empty list never means "trust anything".
var officialPublicKeys = []string{}
