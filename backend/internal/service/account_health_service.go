package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrAccountHealthForbidden = infraerrors.Forbidden("ACCOUNT_HEALTH_FORBIDDEN", "administrator access required")

func accountHealthBadRequest(message string) error {
	return infraerrors.BadRequest("ACCOUNT_HEALTH_INVALID", message)
}

type AccountHealthService struct {
	repo        AccountHealthRepository
	settingRepo SettingRepository
	now         func() time.Time
}

func NewAccountHealthService(repo AccountHealthRepository, settingRepo SettingRepository) *AccountHealthService {
	return &AccountHealthService{
		repo:        repo,
		settingRepo: settingRepo,
		now:         time.Now,
	}
}

func (s *AccountHealthService) authorize(ctx context.Context, actorID int64) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("account health repository is unavailable")
	}
	ok, err := s.repo.IsAdmin(ctx, actorID)
	if err != nil {
		return fmt.Errorf("authorize account health administrator: %w", err)
	}
	if !ok {
		return ErrAccountHealthForbidden
	}
	return nil
}

func (s *AccountHealthService) authorizeSuperAdmin(ctx context.Context, actorID int64) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("account health repository is unavailable")
	}
	ok, err := s.repo.IsSuperAdmin(ctx, actorID)
	if err != nil {
		return fmt.Errorf("authorize account health super administrator: %w", err)
	}
	if !ok {
		return ErrAccountHealthForbidden
	}
	return nil
}

func normalizeAccountHealthFilter(filter AccountHealthFilter) (AccountHealthFilter, error) {
	filter.Search = strings.TrimSpace(filter.Search)
	filter.Platform = strings.TrimSpace(filter.Platform)
	filter.State = strings.TrimSpace(strings.ToLower(filter.State))
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 25
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	if filter.State != "" && filter.State != AccountHealthStateHealthy && filter.State != AccountHealthStateDegraded && filter.State != AccountHealthStateIsolated {
		return filter, accountHealthBadRequest("invalid health state")
	}
	return filter, nil
}

func validateAccountHealthSettings(settings AccountHealthSettings) error {
	switch {
	case settings.WindowMinutes < 1 || settings.WindowMinutes > 1440:
		return accountHealthBadRequest("window_minutes must be between 1 and 1440")
	case settings.MinSamples < 1 || settings.MinSamples > 1000000:
		return accountHealthBadRequest("min_samples must be between 1 and 1000000")
	case settings.IsolateErrorRate <= 0 || settings.IsolateErrorRate > 1:
		return accountHealthBadRequest("isolate_error_rate must be greater than 0 and at most 1")
	case settings.RecoverErrorRate < 0 || settings.RecoverErrorRate >= settings.IsolateErrorRate:
		return accountHealthBadRequest("recover_error_rate must be non-negative and lower than isolate_error_rate")
	case settings.CooldownMinutes < 1 || settings.CooldownMinutes > 10080:
		return accountHealthBadRequest("cooldown_minutes must be between 1 and 10080")
	case settings.IntervalSeconds < 10 || settings.IntervalSeconds > 3600:
		return accountHealthBadRequest("interval_seconds must be between 10 and 3600")
	default:
		return nil
	}
}

func (s *AccountHealthService) readSettings(ctx context.Context) (AccountHealthSettings, error) {
	defaults := DefaultAccountHealthSettings()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAccountHealthSettings)
	if errors.Is(err, ErrSettingNotFound) {
		return defaults, nil
	}
	if err != nil {
		return defaults, fmt.Errorf("read account health settings: %w", err)
	}
	var settings AccountHealthSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return defaults, fmt.Errorf("decode account health settings: %w", err)
	}
	if err := validateAccountHealthSettings(settings); err != nil {
		return defaults, fmt.Errorf("validate stored account health settings: %w", err)
	}
	return settings, nil
}

func (s *AccountHealthService) Settings(ctx context.Context, actorID int64) (AccountHealthSettings, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return AccountHealthSettings{}, err
	}
	return s.readSettings(ctx)
}

func (s *AccountHealthService) UpdateSettings(ctx context.Context, actorID int64, settings AccountHealthSettings) (AccountHealthSettings, error) {
	if err := s.authorizeSuperAdmin(ctx, actorID); err != nil {
		return AccountHealthSettings{}, err
	}
	if err := validateAccountHealthSettings(settings); err != nil {
		return AccountHealthSettings{}, err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return AccountHealthSettings{}, fmt.Errorf("encode account health settings: %w", err)
	}
	if s.settingRepo == nil {
		return AccountHealthSettings{}, fmt.Errorf("account health settings repository is unavailable")
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAccountHealthSettings, string(raw)); err != nil {
		return AccountHealthSettings{}, fmt.Errorf("persist account health settings: %w", err)
	}
	return settings, nil
}

func (s *AccountHealthService) Snapshot(ctx context.Context, actorID int64, filter AccountHealthFilter) (*AccountHealthPage, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	filter, err := normalizeAccountHealthFilter(filter)
	if err != nil {
		return nil, err
	}
	settings, err := s.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	evaluatedAt := s.now().UTC()
	stats, err := s.repo.ListWindowStats(ctx, evaluatedAt.Add(-time.Duration(settings.WindowMinutes)*time.Minute), filter)
	if err != nil {
		return nil, fmt.Errorf("load account health window: %w", err)
	}

	items := make([]AccountHealthSnapshot, 0, len(stats))
	overview := AccountHealthOverview{TotalAccounts: int64(len(stats))}
	for _, stat := range stats {
		item := decideAccountHealth(stat, settings, evaluatedAt)
		switch item.State {
		case AccountHealthStateIsolated:
			overview.Isolated++
		case AccountHealthStateDegraded:
			overview.Degraded++
		default:
			overview.Healthy++
		}
		if item.TotalCount == 0 {
			overview.NoSamples++
		}
		if filter.State == "" || item.State == filter.State {
			items = append(items, item)
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		left, right := accountHealthStateRank(items[i].State), accountHealthStateRank(items[j].State)
		if left != right {
			return left > right
		}
		if items[i].Score != items[j].Score {
			return items[i].Score < items[j].Score
		}
		return items[i].AccountID < items[j].AccountID
	})

	total := len(items)
	start := (filter.Page - 1) * filter.PageSize
	if start > total {
		start = total
	}
	end := start + filter.PageSize
	if end > total {
		end = total
	}
	pageItems := append([]AccountHealthSnapshot(nil), items[start:end]...)
	return &AccountHealthPage{
		Items:         pageItems,
		Total:         int64(total),
		Page:          filter.Page,
		PageSize:      filter.PageSize,
		EvaluatedAt:   evaluatedAt,
		WindowMinutes: settings.WindowMinutes,
		Overview:      overview,
	}, nil
}

func decideAccountHealth(stat AccountHealthWindowStat, settings AccountHealthSettings, now time.Time) AccountHealthSnapshot {
	total := stat.SuccessCount + stat.ErrorCount
	snapshot := AccountHealthSnapshot{
		AccountID:           stat.AccountID,
		Name:                stat.Name,
		Platform:            stat.Platform,
		AccountStatus:       stat.AccountStatus,
		SuccessCount:        stat.SuccessCount,
		ErrorCount:          stat.ErrorCount,
		TotalCount:          total,
		Score:               100,
		State:               AccountHealthStateHealthy,
		HasEnoughSamples:    total >= int64(settings.MinSamples),
		UnschedulableReason: stat.TempUnschedulableReason,
		UnschedulableUntil:  stat.TempUnschedulableUntil,
		EvaluatedAt:         now,
	}
	if total > 0 {
		snapshot.ErrorRate = float64(stat.ErrorCount) / float64(total)
		snapshot.Score = int(math.Round(100 * (1 - snapshot.ErrorRate)))
	}
	if stat.AvgLatencyMS != nil {
		value := int64(math.Round(*stat.AvgLatencyMS))
		snapshot.AvgLatencyMS = &value
	}
	if snapshot.HasEnoughSamples {
		switch {
		case snapshot.ErrorRate >= settings.IsolateErrorRate:
			snapshot.State = AccountHealthStateIsolated
		case snapshot.ErrorRate >= settings.RecoverErrorRate:
			snapshot.State = AccountHealthStateDegraded
		}
	}
	snapshot.TemporarilyUnschedulable = stat.TempUnschedulableUntil != nil && stat.TempUnschedulableUntil.After(now)
	if snapshot.TemporarilyUnschedulable {
		snapshot.State = AccountHealthStateIsolated
	}
	return snapshot
}

func accountHealthStateRank(state string) int {
	switch state {
	case AccountHealthStateIsolated:
		return 3
	case AccountHealthStateDegraded:
		return 2
	default:
		return 1
	}
}
