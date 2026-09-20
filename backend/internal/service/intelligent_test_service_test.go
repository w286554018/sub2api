package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type intelligentTestRepositoryStub struct {
	claimLease time.Duration
	claim      *IntelligentTestRecord
	cancel     *IntelligentTestRecord
}

func (r *intelligentTestRepositoryStub) IsAdmin(context.Context, int64) (bool, error) {
	return true, nil
}

func (r *intelligentTestRepositoryStub) IsSuperAdmin(context.Context, int64) (bool, error) {
	return true, nil
}

func (r *intelligentTestRepositoryStub) Settings(context.Context) ([]IntelligentTestSetting, error) {
	return nil, nil
}

func (r *intelligentTestRepositoryStub) UpdateSetting(context.Context, int64, *IntelligentTestSetting) error {
	return nil
}

func (r *intelligentTestRepositoryStub) Enqueue(context.Context, int64, IntelligentTestEnqueue) (*IntelligentTestEnqueued, error) {
	return nil, nil
}

func (r *intelligentTestRepositoryStub) Accounts(context.Context, IntelligentTestFilter) (*IntelligentTestAccounts, error) {
	return nil, nil
}

func (r *intelligentTestRepositoryStub) Records(context.Context, IntelligentTestFilter) (*IntelligentTestRecords, error) {
	return nil, nil
}

func (r *intelligentTestRepositoryStub) Get(context.Context, int64) (*IntelligentTestRecord, error) {
	return nil, nil
}

func (r *intelligentTestRepositoryStub) Claim(_ context.Context, leaseFor time.Duration) (*IntelligentTestRecord, error) {
	r.claimLease = leaseFor
	return r.claim, nil
}

func (r *intelligentTestRepositoryStub) Finish(context.Context, *IntelligentTestRecord) error {
	return nil
}

func (r *intelligentTestRepositoryStub) Defer(context.Context, *IntelligentTestRecord, time.Time, string) error {
	return nil
}

func (r *intelligentTestRepositoryStub) Cancel(context.Context, int64, int64) (*IntelligentTestRecord, error) {
	return r.cancel, nil
}

func (r *intelligentTestRepositoryStub) Reevaluate(context.Context, int64, int64, func(*IntelligentTestRecord) error) (*IntelligentTestRecord, error) {
	return nil, nil
}

type intelligentTestRunnerFunc func(context.Context, *IntelligentTestRecord) error

func (f intelligentTestRunnerFunc) RunIntelligentTest(ctx context.Context, record *IntelligentTestRecord) error {
	return f(ctx, record)
}

func TestIntelligentTestServiceWorkerLeaseCoversMaximumExecutionTimeout(t *testing.T) {
	repo := &intelligentTestRepositoryStub{}
	svc := NewIntelligentTestService(repo, intelligentTestRunnerFunc(func(context.Context, *IntelligentTestRecord) error {
		return nil
	}))

	svc.work(context.Background())

	require.Greater(t, repo.claimLease, 10*time.Minute)
}

func TestIntelligentTestServiceCancelStopsActiveRunner(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	repo := &intelligentTestRepositoryStub{cancel: &IntelligentTestRecord{ID: 7, Status: IntelligentTestStatusCancelled}}
	svc := NewIntelligentTestService(repo, intelligentTestRunnerFunc(func(ctx context.Context, _ *IntelligentTestRecord) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}))
	record := &IntelligentTestRecord{
		ID:             7,
		LeaseToken:     "lease",
		ConfigSnapshot: &IntelligentTestConfig{TimeoutSeconds: 600},
	}
	done := make(chan struct{})
	go func() {
		svc.run(context.Background(), record)
		close(done)
	}()
	<-started

	_, err := svc.Cancel(context.Background(), 9, 7)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		select {
		case <-stopped:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	<-done
}

func TestIntelligentTestServiceRedactsRunnerErrorsBeforePersistence(t *testing.T) {
	repo := &intelligentTestRepositoryStub{}
	svc := NewIntelligentTestService(repo, intelligentTestRunnerFunc(func(_ context.Context, record *IntelligentTestRecord) error {
		record.ErrorMessage = "Authorization: Bearer bearer-secret access_token=access-secret proxy=http://user:proxy-secret@example.com api_key=query-secret"
		return errors.New(record.ErrorMessage)
	}))
	record := &IntelligentTestRecord{
		ID:             7,
		LeaseToken:     "lease",
		ConfigSnapshot: &IntelligentTestConfig{TimeoutSeconds: 60},
	}

	svc.run(context.Background(), record)

	require.NotContains(t, record.ErrorMessage, "bearer-secret")
	require.NotContains(t, record.ErrorMessage, "access-secret")
	require.NotContains(t, record.ErrorMessage, "proxy-secret")
	require.NotContains(t, record.ErrorMessage, "query-secret")
	require.Contains(t, record.ErrorMessage, "[REDACTED]")
}

func TestIntelligentTestServiceRejectsModelOverrideForUnselectedAccount(t *testing.T) {
	repo := &intelligentTestRepositoryStub{}
	svc := NewIntelligentTestService(repo, intelligentTestRunnerFunc(func(context.Context, *IntelligentTestRecord) error {
		return nil
	}))

	_, err := svc.Enqueue(context.Background(), 9, IntelligentTestEnqueue{
		AccountIDs:    []int64{3},
		TestTypes:     []string{"candy"},
		AccountModels: map[int64]string{4: "gpt-5.6-sol"},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "account model overrides")
}
