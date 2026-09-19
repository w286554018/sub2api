package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const accountHealthAutomationRunTimeout = 30 * time.Second

type AccountHealthAutomationService struct {
	repo   AccountHealthRepository
	health *AccountHealthService
	now    func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	stopCh    chan struct{}
	wg        sync.WaitGroup
	running   atomic.Bool
}

func NewAccountHealthAutomationService(repo AccountHealthRepository, health *AccountHealthService) *AccountHealthAutomationService {
	ctx, cancel := context.WithCancel(context.Background())
	return &AccountHealthAutomationService{
		repo:   repo,
		health: health,
		now:    time.Now,
		ctx:    ctx,
		cancel: cancel,
		stopCh: make(chan struct{}),
	}
}

func (s *AccountHealthAutomationService) Start() {
	if s == nil || s.repo == nil || s.health == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.loop()
	})
}

func (s *AccountHealthAutomationService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.stopCh != nil {
			close(s.stopCh)
		}
	})
	s.wg.Wait()
}

func (s *AccountHealthAutomationService) loop() {
	defer s.wg.Done()
	delay := accountHealthAutomationInterval(DefaultAccountHealthSettings())
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(s.ctx, accountHealthAutomationRunTimeout)
			settings, err := s.runCycle(ctx)
			cancel()
			if err != nil {
				logger.LegacyPrintf("service.account_health_automation", "cycle failed: %v", err)
			} else {
				delay = accountHealthAutomationInterval(settings)
			}
			timer.Reset(delay)
		}
	}
}

func accountHealthAutomationInterval(settings AccountHealthSettings) time.Duration {
	seconds := settings.IntervalSeconds
	if seconds < 10 || seconds > 3600 {
		seconds = DefaultAccountHealthSettings().IntervalSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (s *AccountHealthAutomationService) runOnce(ctx context.Context) error {
	_, err := s.runCycle(ctx)
	return err
}

func (s *AccountHealthAutomationService) runCycle(ctx context.Context) (AccountHealthSettings, error) {
	settings := DefaultAccountHealthSettings()
	if s == nil || s.repo == nil || s.health == nil {
		return settings, fmt.Errorf("account health automation dependencies are unavailable")
	}
	if !s.running.CompareAndSwap(false, true) {
		return settings, nil
	}
	defer s.running.Store(false)

	loaded, err := s.health.readSettings(ctx)
	if err != nil {
		return settings, err
	}
	settings = loaded
	if !settings.Enabled {
		return settings, nil
	}

	now := s.now().UTC()
	stats, err := s.repo.ListWindowStats(ctx, now.Add(-time.Duration(settings.WindowMinutes)*time.Minute), AccountHealthFilter{})
	if err != nil {
		return settings, fmt.Errorf("load account health automation window: %w", err)
	}

	var mutationErrors []error
	for _, stat := range stats {
		if err := ctx.Err(); err != nil {
			mutationErrors = append(mutationErrors, err)
			break
		}
		if stat.AccountStatus != StatusActive {
			continue
		}
		total := stat.SuccessCount + stat.ErrorCount
		if total < int64(settings.MinSamples) {
			continue
		}
		errorRate := float64(stat.ErrorCount) / float64(total)
		switch {
		case errorRate >= settings.IsolateErrorRate:
			if hasActiveForeignHealthIsolation(stat, now) {
				continue
			}
			reason := fmt.Sprintf("%serr_rate=%.4f;samples=%d", AccountHealthAutoReasonPrefix, errorRate, total)
			_, err := s.repo.SetAutoIsolation(ctx, stat.AccountID, now.Add(time.Duration(settings.CooldownMinutes)*time.Minute), reason)
			if err != nil {
				mutationErrors = append(mutationErrors, fmt.Errorf("isolate account %d: %w", stat.AccountID, err))
			}
		case errorRate <= settings.RecoverErrorRate && strings.HasPrefix(stat.TempUnschedulableReason, AccountHealthAutoReasonPrefix):
			_, err := s.repo.ClearAutoIsolation(ctx, stat.AccountID)
			if err != nil {
				mutationErrors = append(mutationErrors, fmt.Errorf("recover account %d: %w", stat.AccountID, err))
			}
		}
	}
	return settings, errors.Join(mutationErrors...)
}

func hasActiveForeignHealthIsolation(stat AccountHealthWindowStat, now time.Time) bool {
	return stat.TempUnschedulableUntil != nil &&
		stat.TempUnschedulableUntil.After(now) &&
		!strings.HasPrefix(stat.TempUnschedulableReason, AccountHealthAutoReasonPrefix)
}
