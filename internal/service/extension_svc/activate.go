package extension_svc

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/extreg"
)

// Activate brings up one installed extension in full in this process: it loads the
// extension from its directory and registers its executing face — policy check,
// executor, command canonicalization, config validator, test connection — along with
// the descriptive one (extreg.Register). It is how a process that does not run every
// extension (opsctl) starts only the one a command needs, so it replaces a
// describe-only registration of the same name (extreg.RegisterDescribeOnly): extreg
// holds a single registration per extension.
//
// An extension the user disabled is refused before anything is loaded, and the error
// names it. An extension this service already has active is left as it is.
func (s *Service) Activate(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bridge.Get(name) != nil {
		return nil
	}
	state, err := s.stateRepo.Find(ctx, name)
	switch {
	case err == nil && !state.Enabled:
		return fmt.Errorf("extension %q is disabled in the desktop app; enable it there to use it", name)
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("read extension %q state: %w", name, err)
	}

	log := logger.Ctx(ctx).With(zap.String("extension", name))
	log.Info("extension activation start")
	manifest, err := s.manager.LoadExtension(ctx, s.manager.ExtDir(name))
	if err != nil {
		log.Error("extension activation failed", zap.Error(err))
		return fmt.Errorf("load extension %q: %w", name, err)
	}
	if manifest.Name != name {
		s.unloadAfterFailedActivation(ctx, manifest.Name)
		log.Error("extension activation failed: directory holds another extension", zap.String("found", manifest.Name))
		return fmt.Errorf("load extension %q: its directory holds extension %q", name, manifest.Name)
	}

	extreg.Unregister(name)
	if err := s.register(s.manager.GetExtension(name)); err != nil {
		s.unloadAfterFailedActivation(ctx, name)
		log.Error("extension activation failed", zap.Error(err))
		return fmt.Errorf("register extension %q: %w", name, err)
	}
	log.Info("extension activation end", zap.String("version", manifest.Version))
	return nil
}

func (s *Service) unloadAfterFailedActivation(ctx context.Context, name string) {
	if err := s.manager.Unload(ctx, name); err != nil {
		logger.Ctx(ctx).Warn("unload after failed activation", zap.String("extension", name), zap.Error(err))
	}
}
