package host_key_svc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/opskat/opskat/internal/repository/host_key_repo"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type CheckState string

const (
	CheckFirstUse CheckState = "first_use"
	CheckMatch    CheckState = "match"
	CheckChanged  CheckState = "changed"
)

var ErrChangedKeyRequiresReplacement = errors.New("server key changed and replacement was not confirmed")

type PresentedKey struct {
	Host        string
	Port        int
	KeyType     string
	PublicKey   string
	Fingerprint string
}

type CheckResult struct {
	State          CheckState
	Key            PresentedKey
	OldFingerprint string
	NewFingerprint string
}

type HostKeySvc interface {
	Check(ctx context.Context, key PresentedKey) (*CheckResult, error)
	Trust(ctx context.Context, key PresentedKey, replace bool) error
}

type hostKeySvc struct {
	repo func() host_key_repo.HostKeyRepo
	now  func() time.Time
}

var defaultHostKeySvc HostKeySvc = &hostKeySvc{
	repo: host_key_repo.HostKey,
	now:  time.Now,
}

func HostKey() HostKeySvc {
	return defaultHostKeySvc
}

func New(repo host_key_repo.HostKeyRepo) HostKeySvc {
	return &hostKeySvc{
		repo: func() host_key_repo.HostKeyRepo { return repo },
		now:  time.Now,
	}
}

func (s *hostKeySvc) Check(ctx context.Context, key PresentedKey) (*CheckResult, error) {
	logger.Ctx(ctx).Info("host key check start",
		zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType))
	stored, err := s.repo().FindByHostPortKeyType(ctx, key.Host, key.Port, key.KeyType)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		result := &CheckResult{
			State:          CheckFirstUse,
			Key:            key,
			NewFingerprint: key.Fingerprint,
		}
		logger.Ctx(ctx).Info("host key check end",
			zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType),
			zap.String("state", string(result.State)))
		return result, nil
	}
	if err != nil {
		wrapped := fmt.Errorf("read stored host key: %w", err)
		logger.Ctx(ctx).Error("host key check failed",
			zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType), zap.Error(wrapped))
		return nil, wrapped
	}

	if stored.PublicKey == key.PublicKey {
		stored.LastSeen = s.now().Unix()
		if err := s.repo().Upsert(ctx, stored); err != nil {
			wrapped := fmt.Errorf("update host key last-seen: %w", err)
			logger.Ctx(ctx).Error("host key check failed",
				zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType), zap.Error(wrapped))
			return nil, wrapped
		}
		result := &CheckResult{
			State:          CheckMatch,
			Key:            key,
			OldFingerprint: stored.Fingerprint,
			NewFingerprint: key.Fingerprint,
		}
		logger.Ctx(ctx).Info("host key check end",
			zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType),
			zap.String("state", string(result.State)))
		return result, nil
	}

	result := &CheckResult{
		State:          CheckChanged,
		Key:            key,
		OldFingerprint: stored.Fingerprint,
		NewFingerprint: key.Fingerprint,
	}
	logger.Ctx(ctx).Info("host key check end",
		zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType),
		zap.String("state", string(result.State)))
	return result, nil
}

func (s *hostKeySvc) Trust(ctx context.Context, key PresentedKey, replace bool) error {
	logger.Ctx(ctx).Info("host key trust start",
		zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType), zap.Bool("replace", replace))
	stored, err := s.repo().FindByHostPortKeyType(ctx, key.Host, key.Port, key.KeyType)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		wrapped := fmt.Errorf("read stored host key before trust: %w", err)
		logger.Ctx(ctx).Error("host key trust failed",
			zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType), zap.Error(wrapped))
		return wrapped
	}

	now := s.now().Unix()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		stored = &host_key_entity.HostKey{
			Host:        key.Host,
			Port:        key.Port,
			KeyType:     key.KeyType,
			PublicKey:   key.PublicKey,
			Fingerprint: key.Fingerprint,
			FirstSeen:   now,
			LastSeen:    now,
		}
	} else if stored.PublicKey == key.PublicKey {
		stored.LastSeen = now
	} else {
		if !replace {
			logger.Ctx(ctx).Info("host key trust canceled",
				zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType))
			return ErrChangedKeyRequiresReplacement
		}
		stored.PublicKey = key.PublicKey
		stored.Fingerprint = key.Fingerprint
		stored.LastSeen = now
	}

	if err := s.repo().Upsert(ctx, stored); err != nil {
		wrapped := fmt.Errorf("persist trusted host key: %w", err)
		logger.Ctx(ctx).Error("host key trust failed",
			zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType), zap.Error(wrapped))
		return wrapped
	}
	logger.Ctx(ctx).Info("host key trust end",
		zap.String("host", key.Host), zap.Int("port", key.Port), zap.String("keyType", key.KeyType), zap.Bool("replace", replace))
	return nil
}
