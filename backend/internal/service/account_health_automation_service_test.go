package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func accountHealthSettingsJSON(t *testing.T, settings AccountHealthSettings) string {
	t.Helper()
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	return string(raw)
}

func TestAccountHealthAutomationRunOnceStaysDisabledByDefault(t *testing.T) {
	repo := &accountHealthRepositoryStub{}
	health := NewAccountHealthService(repo, &accountHealthSettingRepositoryStub{values: map[string]string{}})
	svc := NewAccountHealthAutomationService(repo, health)

	err := svc.runOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, repo.listCalls)
	require.Empty(t, repo.autoIsolations)
	require.Empty(t, repo.autoRecoveries)
}

func TestAccountHealthAutomationRereadsSettingsEachRun(t *testing.T) {
	repo := &accountHealthRepositoryStub{stats: []AccountHealthWindowStat{{
		AccountID: 1, AccountStatus: StatusActive, SuccessCount: 4, ErrorCount: 6,
	}}}
	settingsRepo := &accountHealthSettingRepositoryStub{values: map[string]string{}}
	health := NewAccountHealthService(repo, settingsRepo)
	svc := NewAccountHealthAutomationService(repo, health)

	require.NoError(t, svc.runOnce(context.Background()))
	require.Zero(t, repo.listCalls)

	settings := DefaultAccountHealthSettings()
	settings.Enabled = true
	settingsRepo.values[SettingKeyAccountHealthSettings] = accountHealthSettingsJSON(t, settings)
	require.NoError(t, svc.runOnce(context.Background()))
	require.Equal(t, 1, repo.listCalls)
	require.Len(t, repo.autoIsolations, 1)
}

func TestAccountHealthAutomationStartStopIsIdempotent(t *testing.T) {
	repo := &accountHealthRepositoryStub{}
	health := NewAccountHealthService(repo, &accountHealthSettingRepositoryStub{values: map[string]string{}})
	svc := NewAccountHealthAutomationService(repo, health)

	svc.Start()
	svc.Start()
	done := make(chan struct{})
	go func() {
		svc.Stop()
		svc.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("account health automation did not stop promptly")
	}
}

func TestAccountHealthAutomationStopCancelsActiveCycle(t *testing.T) {
	base := &accountHealthRepositoryStub{}
	repo := &blockingAccountHealthRepository{
		accountHealthRepositoryStub: base,
		started:                     make(chan struct{}),
		release:                     make(chan struct{}),
	}
	settings := DefaultAccountHealthSettings()
	settings.Enabled = true
	health := NewAccountHealthService(repo, &accountHealthSettingRepositoryStub{values: map[string]string{
		SettingKeyAccountHealthSettings: accountHealthSettingsJSON(t, settings),
	}})
	svc := NewAccountHealthAutomationService(repo, health)

	runDone := make(chan error, 1)
	go func() { runDone <- svc.runOnce(svc.ctx) }()
	<-repo.started
	svc.Stop()

	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestAccountHealthAutomationRunOnceAppliesThresholdsWithMinimumSamples(t *testing.T) {
	now := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	autoUntil := now.Add(10 * time.Minute)
	foreignUntil := now.Add(20 * time.Minute)
	repo := &accountHealthRepositoryStub{stats: []AccountHealthWindowStat{
		{AccountID: 1, AccountStatus: StatusActive, SuccessCount: 4, ErrorCount: 6},
		{AccountID: 2, AccountStatus: StatusActive, SuccessCount: 9, ErrorCount: 1, TempUnschedulableUntil: &autoUntil, TempUnschedulableReason: AccountHealthAutoReasonPrefix + "old"},
		{AccountID: 3, AccountStatus: StatusActive, SuccessCount: 7, ErrorCount: 3},
		{AccountID: 4, AccountStatus: StatusActive, ErrorCount: 9},
		{AccountID: 5, AccountStatus: StatusActive, SuccessCount: 4, ErrorCount: 6, TempUnschedulableUntil: &foreignUntil, TempUnschedulableReason: "refresh:token-failed"},
		{AccountID: 6, AccountStatus: StatusDisabled, SuccessCount: 1, ErrorCount: 9},
	}}
	settings := DefaultAccountHealthSettings()
	settings.Enabled = true
	settingsRepo := &accountHealthSettingRepositoryStub{values: map[string]string{
		SettingKeyAccountHealthSettings: accountHealthSettingsJSON(t, settings),
	}}
	health := NewAccountHealthService(repo, settingsRepo)
	svc := NewAccountHealthAutomationService(repo, health)
	svc.now = func() time.Time { return now }

	err := svc.runOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, now.Add(-10*time.Minute), repo.lastSince)
	require.Len(t, repo.autoIsolations, 1)
	require.Equal(t, int64(1), repo.autoIsolations[0].accountID)
	require.Equal(t, now.Add(30*time.Minute), repo.autoIsolations[0].until)
	require.True(t, strings.HasPrefix(repo.autoIsolations[0].reason, AccountHealthAutoReasonPrefix))
	require.Contains(t, repo.autoIsolations[0].reason, "samples=10")
	require.Equal(t, []int64{2}, repo.autoRecoveries)
}

type blockingAccountHealthRepository struct {
	*accountHealthRepositoryStub
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingAccountHealthRepository) ListWindowStats(ctx context.Context, since time.Time, filter AccountHealthFilter) ([]AccountHealthWindowStat, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.accountHealthRepositoryStub.ListWindowStats(ctx, since, filter)
}

func TestAccountHealthAutomationRunOnceDoesNotOverlap(t *testing.T) {
	base := &accountHealthRepositoryStub{}
	repo := &blockingAccountHealthRepository{
		accountHealthRepositoryStub: base,
		started:                     make(chan struct{}),
		release:                     make(chan struct{}),
	}
	settings := DefaultAccountHealthSettings()
	settings.Enabled = true
	health := NewAccountHealthService(repo, &accountHealthSettingRepositoryStub{values: map[string]string{
		SettingKeyAccountHealthSettings: accountHealthSettingsJSON(t, settings),
	}})
	svc := NewAccountHealthAutomationService(repo, health)

	firstDone := make(chan error, 1)
	go func() { firstDone <- svc.runOnce(context.Background()) }()
	<-repo.started

	require.NoError(t, svc.runOnce(context.Background()))
	close(repo.release)
	require.NoError(t, <-firstDone)
	require.Equal(t, 1, base.listCalls)
}
