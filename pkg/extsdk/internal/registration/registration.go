// Package registration holds the extension's invalid registrations, shared by the
// SDK (pkg/extsdk) and its sub-packages (sdkdbdriver) without being part of the
// API an extension calls.
//
// They are recorded rather than panicked on: a panic in the guest's init surfaces
// at the host only as "wasm error: unreachable" plus a stack trace, while the SDK
// answers every call — describe first — with the first one's message, naming the
// tool or contribution and the reason.
package registration

var errs []error

// Fail records an invalid registration.
func Fail(err error) { errs = append(errs, err) }

// Err is the first invalid registration recorded, or nil.
func Err() error {
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}

// Reset clears the record (for tests).
func Reset() { errs = nil }
