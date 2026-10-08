package extstore_svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/pkg/ociclient"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

// progressInterval bounds how often download progress is reported.
const progressInterval = 100 * time.Millisecond

// Installer is the part of extension_svc a store install drives:
// *extension_svc.Service.
type Installer interface {
	NewInstallConfirm(c extension_svc.InstallCandidate) extension_svc.InstallConfirm
	Install(ctx context.Context, sourcePath string, confirm extension_svc.ConfirmInstallFunc) (*extension.Manifest, error)
}

// InstallOptions are what InstallFromStore needs from its caller.
type InstallOptions struct {
	Installer Installer
	// Confirm asks the user to approve the install, before anything is
	// downloaded; extension_svc.ErrInstallCanceled when they decline.
	Confirm extension_svc.ConfirmInstallFunc
	// OnProgress receives the install's progress.
	OnProgress func(Progress)
}

// Phase is a stage of a store install.
type Phase string

const (
	PhaseDownloading Phase = "downloading"
	// PhaseVerifying covers the sha256 check and staging the package (its
	// compatibility and that it is what the user confirmed).
	PhaseVerifying  Phase = "verifying"
	PhaseInstalling Phase = "installing"
)

// Progress is one progress report; Done / Total are bytes of the package.
type Progress struct {
	Name  string `json:"name"`
	Phase Phase  `json:"phase"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

// InstallErrorKind says why a store install failed; each kind has its own copy
// and remedy in the store.
type InstallErrorKind string

const (
	// InstallErrNetwork: the registry (mirror) or the index host is unreachable.
	InstallErrNetwork InstallErrorKind = "network"
	// InstallErrAuth: the registry's anonymous-token challenge failed.
	InstallErrAuth InstallErrorKind = "auth"
	// InstallErrRegistry: the registry answered, but not with the package
	// (missing manifest, refused image, not a single-layer artifact).
	InstallErrRegistry InstallErrorKind = "registry"
	// InstallErrSignature: there is no index whose signature verified.
	InstallErrSignature InstallErrorKind = "signature"
	// InstallErrIndex: the signed index cannot be read by this build.
	InstallErrIndex InstallErrorKind = "index"
	// InstallErrDigest: the package's sha256 differs from the index.
	InstallErrDigest InstallErrorKind = "digest"
	// InstallErrSize: the package is larger than the index records.
	InstallErrSize InstallErrorKind = "size"
	// InstallErrIncompatible: no version this app can run.
	InstallErrIncompatible InstallErrorKind = "incompatible"
	// InstallErrNotFound: the index has no extension of that name.
	InstallErrNotFound InstallErrorKind = "notFound"
	// InstallErrInstalled: the installed version is already the newest the
	// store offers; the store never installs below it.
	InstallErrInstalled InstallErrorKind = "installed"
	// InstallErrMismatch: the downloaded package is not what the user
	// confirmed (name, version, capabilities, or the version it replaces).
	InstallErrMismatch InstallErrorKind = "mismatch"
	// InstallErrBusy: the same extension is already being installed.
	InstallErrBusy InstallErrorKind = "busy"
	// InstallErrInstall: staging or committing the package failed.
	InstallErrInstall InstallErrorKind = "install"
)

// InstallError is a failed store install. Expected / Actual are set for
// InstallErrDigest.
type InstallError struct {
	Kind     InstallErrorKind
	Expected string
	Actual   string
	Err      error
}

func (e *InstallError) Error() string { return e.Err.Error() }
func (e *InstallError) Unwrap() error { return e.Err }

func installErr(kind InstallErrorKind, err error) *InstallError {
	return &InstallError{Kind: kind, Err: err}
}

// InstallFromStore installs or updates name to the version the store offers
// for it (extstore.SelectInstallable: the newest compatible version, never one
// below the installed version), from the last verified index — refreshing first
// when no index has verified yet.
//
//  1. opts.Confirm shows the index's description of the change; nothing is
//     downloaded unless the user accepts.
//  2. The package is pulled from RegistryHost() — the ref's repository, not the
//     host in the ref — bounded by the index size and checked against the
//     index sha256 (the signed index is the trust root).
//  3. opts.Installer.Install stages it (compatibility check included) and,
//     without asking again, refuses it unless it is exactly the change the user
//     confirmed; then commits.
//
// Any failure leaves the installed extension as it was; the download is always
// removed. Failures are *InstallError, except a declined confirm
// (extension_svc.ErrInstallCanceled) and a canceled ctx.
func (s *Service) InstallFromStore(ctx context.Context, name string, opts InstallOptions) (string, string, error) {
	if !s.beginInstall(name) {
		return "", "", installErr(InstallErrBusy, fmt.Errorf("extension %q is already being installed", name))
	}
	defer s.endInstall(name)

	ext, v, err := s.offer(ctx, name)
	if err != nil {
		logger.Ctx(ctx).Warn("extension store install not offered", zap.String("extension", name), zap.Error(err))
		return "", "", err
	}
	lang := aictx.GetPolicyLang(ctx)
	displayName, _ := display(ext, lang)
	approved := opts.Installer.NewInstallConfirm(extension_svc.InstallCandidate{
		Name:         ext.Name,
		DisplayName:  displayName,
		Icon:         ext.Icon,
		Version:      v.Version,
		Capabilities: v.Capabilities,
		Source:       extension_svc.InstallSourceStore,
		Size:         v.Size,
	})
	if err := opts.Confirm(ctx, approved); err != nil {
		return "", "", err
	}

	host := RegistryHost()
	log := logger.Ctx(ctx).With(zap.String("extension", name), zap.String("version", v.Version),
		zap.String("registry", host), zap.String("ref", v.Source.Ref))
	log.Info("extension store install started")
	manifest, err := s.download(ctx, host, name, v, opts, func(path string) (*extension.Manifest, error) {
		return opts.Installer.Install(ctx, path, func(_ context.Context, staged extension_svc.InstallConfirm) error {
			if err := sameChange(approved, staged); err != nil {
				return err
			}
			opts.OnProgress(Progress{Name: name, Phase: PhaseInstalling, Done: v.Size, Total: v.Size})
			return nil
		})
	})
	if err != nil {
		log.Error("extension store install failed", zap.Error(err))
		return "", "", err
	}
	log.Info("extension store install completed")
	return manifest.Name, manifest.Version, nil
}

func (s *Service) beginInstall(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.installing[name]; busy {
		return false
	}
	s.installing[name] = struct{}{}
	return true
}

func (s *Service) endInstall(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.installing, name)
}

// offer finds what the store offers for name in the verified index.
func (s *Service) offer(ctx context.Context, name string) (extstore.Extension, extstore.Version, error) {
	idx, _, ok := s.StoreIndex()
	if !ok {
		if err := s.Refresh(ctx); err != nil {
			var re *RefreshError
			errors.As(err, &re)
			kind := InstallErrIndex
			switch re.Kind {
			case ErrorFetch:
				kind = InstallErrNetwork
			case ErrorSignature:
				kind = InstallErrSignature
			}
			return extstore.Extension{}, extstore.Version{}, installErr(kind, err)
		}
		idx, _, _ = s.StoreIndex()
	}
	i := slices.IndexFunc(idx.Extensions, func(e extstore.Extension) bool { return e.Name == name })
	if i < 0 {
		return extstore.Extension{}, extstore.Version{}, installErr(InstallErrNotFound,
			fmt.Errorf("extension %q is not in the store index", name))
	}
	ext := idx.Extensions[i]
	installed := s.opts.InstalledVersions()[name]
	choice, reason := extstore.SelectInstallable(ext, s.opts.App(), installed)
	switch choice.Action {
	case extstore.ActionInstall, extstore.ActionUpdate:
		return ext, *choice.Version, nil
	case extstore.ActionInstalled:
		return ext, extstore.Version{}, installErr(InstallErrInstalled,
			fmt.Errorf("extension %q %s is installed and the store offers nothing newer", name, installed))
	default:
		return ext, extstore.Version{}, installErr(InstallErrIncompatible, incompatible(aictx.GetPolicyLang(ctx), reason))
	}
}

// incompatible localizes SelectInstallable's reason where the app has copy for it.
func incompatible(lang string, reason error) error {
	var ie *extension.IncompatibleError
	if errors.As(reason, &ie) {
		return fmt.Errorf("%s: %w", i18n.ExtensionIncompatible(lang, ie), reason)
	}
	return reason
}

// download pulls v into a temporary directory, hands the package to install and
// removes the directory afterwards, whatever happened.
func (s *Service) download(ctx context.Context, host, name string, v extstore.Version, opts InstallOptions,
	install func(path string) (*extension.Manifest, error)) (*extension.Manifest, error) {
	repo, err := repository(v.Source.Ref)
	if err != nil {
		return nil, installErr(InstallErrRegistry, err)
	}
	dir, err := os.MkdirTemp("", "opskat-ext-store-*")
	if err != nil {
		return nil, installErr(InstallErrInstall, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// The installer recognizes a package file by its .zip suffix.
	dst := filepath.Join(dir, name+"-"+v.Version+".zip")

	var last time.Time
	opts.OnProgress(Progress{Name: name, Phase: PhaseDownloading, Total: v.Size})
	err = ociclient.Pull(ctx, host, repo, v.Source.SHA256, v.Size, dst, func(done, total int64) {
		if done < total && time.Since(last) < progressInterval {
			return
		}
		last = time.Now()
		opts.OnProgress(Progress{Name: name, Phase: PhaseDownloading, Done: done, Total: total})
	})
	if err != nil {
		return nil, pullError(err)
	}
	opts.OnProgress(Progress{Name: name, Phase: PhaseVerifying, Done: v.Size, Total: v.Size})

	manifest, err := install(dst)
	if err != nil {
		return nil, installError(err)
	}
	return manifest, nil
}

// repository is ref ("<registry host>/<repository>:<tag>") without its registry
// host: packages are pulled from the configured host, whatever the publisher's.
func repository(ref string) (string, error) {
	host, repo, ok := strings.Cut(ref, "/")
	if !ok || !strings.ContainsAny(host, ".:") && host != "localhost" {
		return "", fmt.Errorf("package reference %q names no registry host", ref)
	}
	return repo, nil
}

func pullError(err error) error {
	var de *ociclient.DigestError
	switch {
	case errors.As(err, &de):
		return &InstallError{Kind: InstallErrDigest, Expected: de.Expected, Actual: de.Actual, Err: err}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ociclient.ErrSize):
		return installErr(InstallErrSize, err)
	case errors.Is(err, ociclient.ErrAuth):
		return installErr(InstallErrAuth, err)
	case errors.Is(err, ociclient.ErrRegistry):
		return installErr(InstallErrRegistry, err)
	case errors.Is(err, ociclient.ErrNetwork):
		return installErr(InstallErrNetwork, err)
	default:
		return installErr(InstallErrInstall, err)
	}
}

func installError(err error) error {
	var ie *InstallError
	var inc *extension.IncompatibleError
	switch {
	case errors.As(err, &ie), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.As(err, &inc):
		return installErr(InstallErrIncompatible, err)
	default:
		return installErr(InstallErrInstall, err)
	}
}

// sameChange refuses a staged package that is not the change the user approved
// from the index: another extension or version, other capabilities, or an
// installed version that changed while it downloaded.
func sameChange(approved, staged extension_svc.InstallConfirm) error {
	if staged.Name == approved.Name && staged.To == approved.To && staged.From == approved.From &&
		slices.Equal(staged.Capabilities, approved.Capabilities) {
		return nil
	}
	return installErr(InstallErrMismatch, fmt.Errorf(
		"the downloaded package (%s %s, replacing %q) is not what was confirmed (%s %s, replacing %q, same capabilities)",
		staged.Name, staged.To, staged.From, approved.Name, approved.To, approved.From))
}

// InstallResult is a store install as the store page shows it.
type InstallResult struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Canceled: the user declined, or the install was abandoned; shown as
	// nothing at all.
	Canceled bool            `json:"canceled"`
	Error    *InstallFailure `json:"error"`
}

// InstallFailure is a failed store install as the store page shows it.
type InstallFailure struct {
	Kind     InstallErrorKind `json:"kind"`
	Message  string           `json:"message"`
	Expected string           `json:"expected"`
	Actual   string           `json:"actual"`
}

// InstallOutcome describes InstallFromStore's result for the store page.
func InstallOutcome(name, version string, err error) InstallResult {
	out := InstallResult{Name: name, Version: version}
	var ie *InstallError
	switch {
	case err == nil:
	case errors.Is(err, extension_svc.ErrInstallCanceled), errors.Is(err, context.Canceled):
		out.Canceled = true
	case errors.As(err, &ie):
		out.Error = &InstallFailure{Kind: ie.Kind, Message: ie.Error(), Expected: ie.Expected, Actual: ie.Actual}
	default:
		// The confirm itself failed (timed out, app shutting down).
		out.Error = &InstallFailure{Kind: InstallErrInstall, Message: err.Error()}
	}
	return out
}
