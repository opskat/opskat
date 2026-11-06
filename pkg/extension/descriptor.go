package extension

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/pkg/extmanifest"
)

// Descriptor is what the guest answers to describe(): everything an extension can
// do, derived inside the guest from the registration calls themselves.
//
// Declaring these in manifest.json would be a second declaration language that
// nothing checks against the code. The manifest keeps what a user must be able to
// audit *before* running the code — the capability grants — and the rest is read
// back from the code that implements it.
//
// The runtime reads only the envelope: the presentation (icon, i18n) and, under
// Contributes, one raw entry per contribution point key. What an entry says, and
// whether it is acceptable, is for the contribution point of the subsystem that
// consumes it (ContributionPoint) — its typed shape is the guest SDK's wire format,
// which the runtime does not depend on.
type Descriptor struct {
	Icon        string                     `json:"icon,omitempty"`
	I18n        ManifestI18n               `json:"i18n"`
	Contributes map[string]json.RawMessage `json:"contributes,omitempty"`
}

// ParseDescriptor decodes a describe() payload's envelope without judging its
// contributions. A payload that does not decode is not an answer in this host's
// shape at all — the describe cache treats that as stale — while one that decodes
// is judged contribution by contribution by the points of the process's
// ContributionRegistry once it is merged onto the manifest. A top-level field the
// envelope does not define is refused: an answer in another shape must not read as
// an extension that contributes nothing.
func ParseDescriptor(data []byte) (*Descriptor, error) {
	var d Descriptor
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parse describe() result: %w", err)
	}
	return &d, nil
}

// Described is the manifest of an extension whose security contract is contract
// and whose describe() answer is d — what loading it produces, for a host that
// holds both without running the guest (a subsystem's tests).
func Described(contract extmanifest.Manifest, d *Descriptor) *Manifest {
	m := &Manifest{Manifest: contract}
	m.apply(d)
	return m
}

// apply merges the guest's describe() answer onto the manifest's security face.
func (m *Manifest) apply(d *Descriptor) {
	m.Icon = d.Icon
	m.I18n = d.I18n
	m.contributes = d.Contributes
}
