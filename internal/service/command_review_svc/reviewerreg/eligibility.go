package reviewerreg

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/service/extension_svc"
)

// Why a reviewer cannot be used here (command_review_svc.ReviewerInfo.Reason); the
// settings page shows them, the detailed error goes only to the log.
const (
	UnusableNotRunning      = "not_running"       // the extension is not running in this process
	UnusableDigestMismatch  = "digest_mismatch"   // the package no longer matches the digest recorded at install
	UnusableStoreRequired   = "store_required"    // a release build takes only reviewers installed from the store
	UnusableNoInstallRecord = "no_install_record" // no recorded install source
	UnusableCheckFailed     = "check_failed"      // the check itself could not finish (install record or package unreadable)
)

// ProvenanceFacts answers extName's install facts — recorded source, package digest
// recomputed from disk and whether it still matches — without running its code
// (extension_svc.Service.Provenance). The registry judges them (judge).
type ProvenanceFacts func(ctx context.Context, extName string) (extension_svc.Provenance, error)

// isReleaseBuild is whether this build is an official release, which takes only
// store-installed reviewers.
func isReleaseBuild() bool { return appversion.Current().Kind == appversion.KindRelease }

// ineligible is why a reviewer may not be used: code is one of Unusable*.
type ineligible struct {
	code string
	err  error
}

func (e *ineligible) Error() string { return e.err.Error() }
func (e *ineligible) Unwrap() error { return e.err }

// unusableReason is the Unusable* code of an eligibility error; anything that is
// not a judgement (the facts could not be read) is a failed check.
func unusableReason(err error) string {
	var ie *ineligible
	if errors.As(err, &ie) {
		return ie.code
	}
	return UnusableCheckFailed
}

// judge decides from the install facts p whether the package may review: its files
// must still match the recorded digest; a store install is usable and signed; a
// local or dev install is refused on a release build and usable but unsigned on
// any other; a package without a recorded source is refused.
func judge(p extension_svc.Provenance, release bool) (signed bool, err error) {
	switch {
	case !p.Recorded:
		return false, &ineligible{UnusableNoInstallRecord, errors.New("no install record")}
	case !p.DigestMatches:
		return false, &ineligible{UnusableDigestMismatch, errors.New("command reviewer package digest does not match the installed package")}
	}
	switch p.Source {
	case extension_svc.InstallOriginStore:
		return true, nil
	case extension_svc.InstallOriginLocal, extension_svc.InstallOriginDev:
		if release {
			return false, &ineligible{UnusableStoreRequired,
				fmt.Errorf("command reviewer must be installed from the store on a release build (installed from %s)", p.Source)}
		}
		return false, nil
	default:
		return false, &ineligible{UnusableNoInstallRecord, errors.New("command reviewer has no recorded install source")}
	}
}

// eligibility is the outcome of checking a candidate: whether it is signed and the
// package digest that was checked.
type eligibility struct {
	signed bool
	digest string
}

// checkEligible reads name's install facts and judges them; no facts wired in means
// not eligible.
func (r *Registry) checkEligible(ctx context.Context, name string) (eligibility, error) {
	r.mu.Lock()
	facts, release := r.provenance, r.release
	r.mu.Unlock()
	if facts == nil {
		return eligibility{}, fmt.Errorf("command reviewer %q cannot be checked: no install facts are wired in", name)
	}
	p, err := facts(ctx, name)
	if err != nil {
		return eligibility{}, fmt.Errorf("command reviewer %q: %w", name, err)
	}
	signed, err := judge(p, release())
	if err != nil {
		logger.Ctx(ctx).Warn("command reviewer not eligible",
			zap.String("extension", name), zap.String("source", p.Source), zap.Error(err))
		return eligibility{}, fmt.Errorf("command reviewer %q: %w", name, err)
	}
	return eligibility{signed: signed, digest: p.PackageSHA256}, nil
}
