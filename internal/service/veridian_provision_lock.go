package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const veridianProvisionUnlockTimeout = 5 * time.Second

// veridianCrossProcessProvisionLocker est implemente par le repository
// PostgreSQL de plan. L'interface etroite evite d'elargir le contrat metier du
// repository avec un detail d'orchestration propre au provisioning.
type veridianCrossProcessProvisionLocker interface {
	AcquireProvisionLock(context.Context, string) (func(context.Context) error, error)
}

func (s *veridianService) acquireProvisionLock(ctx context.Context, tenantID string) (func(), error) {
	locker, ok := s.planRepo.(veridianCrossProcessProvisionLocker)
	if !ok {
		return nil, errors.New("database provision lock is not configured")
	}
	releaseDatabase, err := locker.AcquireProvisionLock(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("acquire database provision lock: %w", err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			if releaseDatabase != nil {
				releaseCtx, cancel := context.WithTimeout(context.Background(), veridianProvisionUnlockTimeout)
				defer cancel()
				if releaseErr := releaseDatabase(releaseCtx); releaseErr != nil && s.logger != nil {
					s.logger.WithFields(map[string]interface{}{
						"tenant_id": tenantID,
						"error":     releaseErr.Error(),
					}).Error("veridian: failed to release database provision lock")
				}
			}
		})
	}, nil
}
