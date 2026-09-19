package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type accountHealthRepositoryStub struct {
	admin               bool
	superAdmin          bool
	stats               []AccountHealthWindowStat
	lastSince           time.Time
	lastFilter          AccountHealthFilter
	listCalls           int
	autoIsolations      []accountHealthIsolationCall
	autoRecoveries      []int64
	manualIsolations    []accountHealthIsolationCall
	manualRecoveries    []int64
	rejectAutoIsolation bool
	rejectAutoRecovery  bool
	rejectManualSet     bool
	rejectManualClear   bool
}

type accountHealthIsolationCall struct {
	accountID int64
	until     time.Time
	reason    string
}

func (r *accountHealthRepositoryStub) IsAdmin(context.Context, int64) (bool, error) {
	return r.admin, nil
}

func (r *accountHealthRepositoryStub) IsSuperAdmin(context.Context, int64) (bool, error) {
	return r.superAdmin, nil
}

func (r *accountHealthRepositoryStub) ListWindowStats(_ context.Context, since time.Time, filter AccountHealthFilter) ([]AccountHealthWindowStat, error) {
	r.listCalls++
	r.lastSince = since
	r.lastFilter = filter
	return append([]AccountHealthWindowStat(nil), r.stats...), nil
}

func (r *accountHealthRepositoryStub) SetAutoIsolation(_ context.Context, accountID int64, until time.Time, reason string) (bool, error) {
	r.autoIsolations = append(r.autoIsolations, accountHealthIsolationCall{accountID: accountID, until: until, reason: reason})
	return !r.rejectAutoIsolation, nil
}

func (r *accountHealthRepositoryStub) ClearAutoIsolation(_ context.Context, accountID int64) (bool, error) {
	r.autoRecoveries = append(r.autoRecoveries, accountID)
	return !r.rejectAutoRecovery, nil
}

func (r *accountHealthRepositoryStub) SetManualIsolation(_ context.Context, accountID int64, until time.Time, reason string) (bool, error) {
	r.manualIsolations = append(r.manualIsolations, accountHealthIsolationCall{accountID: accountID, until: until, reason: reason})
	return !r.rejectManualSet, nil
}

func (r *accountHealthRepositoryStub) ClearHealthIsolation(_ context.Context, accountID int64) (bool, error) {
	r.manualRecoveries = append(r.manualRecoveries, accountID)
	return !r.rejectManualClear, nil
}

type accountHealthSettingRepositoryStub struct {
	values map[string]string
}

func (r *accountHealthSettingRepositoryStub) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *accountHealthSettingRepositoryStub) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *accountHealthSettingRepositoryStub) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *accountHealthSettingRepositoryStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}

func (r *accountHealthSettingRepositoryStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (r *accountHealthSettingRepositoryStub) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}

func (r *accountHealthSettingRepositoryStub) Delete(context.Context, string) error {
	return nil
}

func TestDefaultAccountHealthSettingsKeepsAutomationDisabled(t *testing.T) {
	settings := DefaultAccountHealthSettings()

	require.False(t, settings.Enabled)
	require.Less(t, settings.RecoverErrorRate, settings.IsolateErrorRate)
}

func TestAccountHealthSnapshotScoresWindowStatsWithoutWritingAccountState(t *testing.T) {
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	avg := 125.4
	otherIsolationUntil := now.Add(20 * time.Minute)
	repo := &accountHealthRepositoryStub{
		admin: true,
		stats: []AccountHealthWindowStat{
			{AccountID: 1, Name: "no-data", Platform: "openai", AccountStatus: "active"},
			{AccountID: 2, Name: "degraded", Platform: "openai", AccountStatus: "active", SuccessCount: 8, ErrorCount: 2, AvgLatencyMS: &avg},
			{AccountID: 3, Name: "threshold-isolated", Platform: "openai", AccountStatus: "active", SuccessCount: 4, ErrorCount: 6},
			{AccountID: 4, Name: "externally-isolated", Platform: "kiro", AccountStatus: "active", TempUnschedulableUntil: &otherIsolationUntil, TempUnschedulableReason: "refresh:token-failed"},
		},
	}
	settingsRepo := &accountHealthSettingRepositoryStub{values: map[string]string{}}
	svc := NewAccountHealthService(repo, settingsRepo)
	svc.now = func() time.Time { return now }

	page, err := svc.Snapshot(context.Background(), 9, AccountHealthFilter{Page: 1, PageSize: 10})

	require.NoError(t, err)
	require.Equal(t, now.Add(-10*time.Minute), repo.lastSince)
	require.Equal(t, int64(4), page.Total)
	require.Equal(t, int64(1), page.Overview.Healthy)
	require.Equal(t, int64(1), page.Overview.Degraded)
	require.Equal(t, int64(2), page.Overview.Isolated)
	require.Equal(t, int64(2), page.Overview.NoSamples)

	byID := make(map[int64]AccountHealthSnapshot, len(page.Items))
	for _, item := range page.Items {
		byID[item.AccountID] = item
	}
	require.Equal(t, 100, byID[1].Score)
	require.False(t, byID[1].HasEnoughSamples)
	require.Equal(t, AccountHealthStateHealthy, byID[1].State)
	require.Equal(t, AccountHealthStateDegraded, byID[2].State)
	require.InDelta(t, 0.2, byID[2].ErrorRate, 0.0001)
	require.NotNil(t, byID[2].AvgLatencyMS)
	require.Equal(t, int64(125), *byID[2].AvgLatencyMS)
	require.Equal(t, AccountHealthStateIsolated, byID[3].State)
	require.False(t, byID[3].TemporarilyUnschedulable)
	require.Equal(t, AccountHealthStateIsolated, byID[4].State)
	require.True(t, byID[4].TemporarilyUnschedulable)
	require.Equal(t, "refresh:token-failed", byID[4].UnschedulableReason)
}

func TestAccountHealthSnapshotFiltersComputedStateAndPaginates(t *testing.T) {
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	repo := &accountHealthRepositoryStub{admin: true, stats: []AccountHealthWindowStat{
		{AccountID: 1, Name: "healthy", SuccessCount: 10},
		{AccountID: 2, Name: "isolated-a", SuccessCount: 1, ErrorCount: 9},
		{AccountID: 3, Name: "isolated-b", SuccessCount: 2, ErrorCount: 8},
	}}
	svc := NewAccountHealthService(repo, &accountHealthSettingRepositoryStub{values: map[string]string{}})
	svc.now = func() time.Time { return now }

	page, err := svc.Snapshot(context.Background(), 9, AccountHealthFilter{Page: 2, PageSize: 1, State: AccountHealthStateIsolated})

	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Len(t, page.Items, 1)
	require.Equal(t, int64(3), page.Items[0].AccountID)
	require.Equal(t, 2, page.Page)
}

func TestDecideAccountHealthKeepsInsufficientSamplesHealthyAndIgnoresExpiredIsolation(t *testing.T) {
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	settings := DefaultAccountHealthSettings()

	snapshot := decideAccountHealth(AccountHealthWindowStat{
		AccountID:               8,
		ErrorCount:              9,
		TempUnschedulableUntil:  &expired,
		TempUnschedulableReason: "rate_limit:expired",
	}, settings, now)

	require.Equal(t, 0, snapshot.Score)
	require.False(t, snapshot.HasEnoughSamples)
	require.False(t, snapshot.TemporarilyUnschedulable)
	require.Equal(t, AccountHealthStateHealthy, snapshot.State)
	require.Equal(t, "rate_limit:expired", snapshot.UnschedulableReason)
}

func TestAccountHealthServiceRequiresAdministratorAndSuperAdministratorRoles(t *testing.T) {
	settingsRepo := &accountHealthSettingRepositoryStub{values: map[string]string{}}
	svc := NewAccountHealthService(&accountHealthRepositoryStub{}, settingsRepo)

	_, err := svc.Snapshot(context.Background(), 7, AccountHealthFilter{})
	require.ErrorIs(t, err, ErrAccountHealthForbidden)

	svc = NewAccountHealthService(&accountHealthRepositoryStub{admin: true}, settingsRepo)
	_, err = svc.UpdateSettings(context.Background(), 7, DefaultAccountHealthSettings())
	require.ErrorIs(t, err, ErrAccountHealthForbidden)
}

func TestAccountHealthSettingsValidateAndPersistStrictThresholds(t *testing.T) {
	repo := &accountHealthRepositoryStub{admin: true, superAdmin: true}
	settingsRepo := &accountHealthSettingRepositoryStub{values: map[string]string{}}
	svc := NewAccountHealthService(repo, settingsRepo)

	invalid := DefaultAccountHealthSettings()
	invalid.RecoverErrorRate = invalid.IsolateErrorRate
	_, err := svc.UpdateSettings(context.Background(), 1, invalid)
	require.Error(t, err)
	require.Empty(t, settingsRepo.values)

	want := AccountHealthSettings{
		Enabled:          false,
		WindowMinutes:    30,
		MinSamples:       25,
		IsolateErrorRate: 0.65,
		RecoverErrorRate: 0.15,
		CooldownMinutes:  45,
		IntervalSeconds:  90,
	}
	got, err := svc.UpdateSettings(context.Background(), 1, want)
	require.NoError(t, err)
	require.Equal(t, want, got)

	var stored AccountHealthSettings
	require.NoError(t, json.Unmarshal([]byte(settingsRepo.values[SettingKeyAccountHealthSettings]), &stored))
	require.Equal(t, want, stored)
}

func TestAccountHealthManualIsolationRequiresSuperAdminAndPrefixesReason(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	settingsRepo := &accountHealthSettingRepositoryStub{values: map[string]string{}}
	repo := &accountHealthRepositoryStub{admin: true}
	svc := NewAccountHealthService(repo, settingsRepo)
	svc.now = func() time.Time { return now }

	_, err := svc.ManualIsolate(context.Background(), 7, 42, AccountHealthManualIsolationInput{
		DurationMinutes: 45,
		Reason:          "planned maintenance",
	})
	require.ErrorIs(t, err, ErrAccountHealthForbidden)
	require.Empty(t, repo.manualIsolations)

	repo.superAdmin = true
	result, err := svc.ManualIsolate(context.Background(), 7, 42, AccountHealthManualIsolationInput{
		DurationMinutes: 45,
		Reason:          "  planned\nmaintenance  ",
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, int64(42), result.AccountID)
	require.NotNil(t, result.Until)
	require.Equal(t, now.Add(45*time.Minute), *result.Until)
	require.Len(t, repo.manualIsolations, 1)
	require.True(t, strings.HasPrefix(repo.manualIsolations[0].reason, AccountHealthManualReasonPrefix))
	require.Equal(t, AccountHealthManualReasonPrefix+"planned maintenance", repo.manualIsolations[0].reason)
}

func TestAccountHealthManualIsolationRejectsInvalidInputAndOwnershipConflict(t *testing.T) {
	repo := &accountHealthRepositoryStub{admin: true, superAdmin: true, rejectManualSet: true}
	svc := NewAccountHealthService(repo, &accountHealthSettingRepositoryStub{values: map[string]string{}})

	_, err := svc.ManualIsolate(context.Background(), 1, 0, AccountHealthManualIsolationInput{DurationMinutes: 30})
	require.Error(t, err)
	require.Empty(t, repo.manualIsolations)

	_, err = svc.ManualIsolate(context.Background(), 1, 9, AccountHealthManualIsolationInput{DurationMinutes: 0})
	require.Error(t, err)
	require.Empty(t, repo.manualIsolations)

	_, err = svc.ManualIsolate(context.Background(), 1, 9, AccountHealthManualIsolationInput{
		DurationMinutes: 30,
		Reason:          "operator request",
	})
	require.ErrorIs(t, err, ErrAccountHealthIsolationConflict)
	require.Len(t, repo.manualIsolations, 1)
}

func TestAccountHealthManualRecoveryUsesHealthOwnedConditionalClear(t *testing.T) {
	repo := &accountHealthRepositoryStub{admin: true, superAdmin: true}
	svc := NewAccountHealthService(repo, &accountHealthSettingRepositoryStub{values: map[string]string{}})

	result, err := svc.ManualRecover(context.Background(), 1, 9)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, []int64{9}, repo.manualRecoveries)

	repo.rejectManualClear = true
	_, err = svc.ManualRecover(context.Background(), 1, 10)
	require.ErrorIs(t, err, ErrAccountHealthIsolationConflict)
	require.Equal(t, []int64{9, 10}, repo.manualRecoveries)
}
