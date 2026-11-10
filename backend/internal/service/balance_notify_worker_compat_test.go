//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func submitBalanceCompatUsage(s *BalanceNotifyService, user *User) {
	newBalance := 9.0
	s.submitUsageNotifications(&postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 2},
		User: user,
	}, &billingDeps{balanceNotifyService: s}, &UsageBillingApplyResult{NewBalance: &newBalance})
}

func TestBalanceWorkerKeepsNilEmailGuard(t *testing.T) {
	s := NewBalanceNotifyService(nil, newNotificationEmailMemorySettingRepo(), nil)
	t.Cleanup(s.Stop)
	require.Nil(t, s.emailService)
	require.NotNil(t, s.usageNotifications)
	require.False(t, s.canNotifyBalance(&User{BalanceNotifyEnabled: true}))
	s.Stop()
	s.Stop()
}

func TestBalanceWorkerSharesConcurrentRejectionAndDeductionDelivery(t *testing.T) {
	svc, repo, mail, user := balanceRejectionFixture(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	mail.block = release
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		svc.Stop()
	})

	svc.CheckBalanceOnRejection(user, 0)
	require.Eventually(t, func() bool { return mail.calls.Load() == 1 }, time.Second, time.Millisecond)
	submitBalanceCompatUsage(svc, user)
	// The deduction task completes while the rejection task is still inside SMTP.
	// Without the shared user gate it would attempt a second blocked delivery.
	require.Eventually(t, func() bool {
		return svc.usageNotifications.pool.CompletedTasks() == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, int32(1), mail.calls.Load())
	releaseOnce.Do(func() { close(release) })
	waitBalanceIdle(t, svc)

	key := notificationEmailDeliveryKey(NotificationEmailEventBalanceLow, "balance_low", "71", user.BalanceNotifyExtraEmails[0].Email, time.Now().UTC().Format("2006-01-02"))
	_, err := repo.GetValue(context.Background(), key)
	require.NoError(t, err)
	expireBalanceCooldown(svc, user.ID)
	submitBalanceCompatUsage(svc, user)
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(1), mail.calls.Load(), "both entry points must use the same successful daily delivery identity")
}

type balanceStopCompatMail struct {
	started  chan context.Context
	canceled chan struct{}
	release  <-chan struct{}
}

func (m *balanceStopCompatMail) SendEmail(ctx context.Context, _, _, _ string) error {
	m.started <- ctx
	<-ctx.Done()
	m.canceled <- struct{}{}
	<-m.release
	return ctx.Err()
}

func TestBalanceWorkerStopCancelsAndJoinsBothEntryPoints(t *testing.T) {
	svc, _, _, rejectionUser := balanceRejectionFixture(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	mail := &balanceStopCompatMail{
		started: make(chan context.Context, 2), canceled: make(chan struct{}, 2), release: release,
	}
	svc.emailService = mail
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		svc.Stop()
	})
	deductionUser := *rejectionUser
	deductionUser.ID++
	svc.CheckBalanceOnRejection(rejectionUser, 0)
	submitBalanceCompatUsage(svc, &deductionUser)
	for range 2 {
		select {
		case ctx := <-mail.started:
			_, ok := ctx.Deadline()
			require.True(t, ok)
		case <-time.After(2 * time.Second):
			t.Fatal("both notification entry points must reach the bounded worker")
		}
	}
	stopped := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopped)
	}()
	for range 2 {
		select {
		case <-mail.canceled:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop did not cancel an active notification")
		}
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned while active SMTP work was still running")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not join completed notification workers")
	}
	waitBalanceIdle(t, svc)
	before := svc.usageNotifications.pool.SubmittedTasks()
	svc.CheckBalanceOnRejection(rejectionUser, 0)
	submitBalanceCompatUsage(svc, &deductionUser)
	require.Equal(t, before, svc.usageNotifications.pool.SubmittedTasks(), "stopped service must reject both entry points")
	svc.Stop()
}

func TestBalanceWorkerStopDiscardsQueuedEntriesWithoutReservations(t *testing.T) {
	svc, _, mail, user := balanceRejectionFixture(t)
	started := make(chan struct{}, usageNotificationWorkers)
	for range usageNotificationWorkers {
		require.True(t, svc.usageNotifications.submit(func(ctx context.Context) {
			started <- struct{}{}
			<-ctx.Done()
		}))
	}
	for range usageNotificationWorkers {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("notification worker did not start")
		}
	}
	svc.CheckBalanceOnRejection(user, 0)
	submitBalanceCompatUsage(svc, user)
	require.Equal(t, uint64(usageNotificationWorkers+2), svc.usageNotifications.pool.SubmittedTasks())
	svc.Stop()
	require.Zero(t, mail.calls.Load(), "queued notifications must not start SMTP during shutdown")
	svc.balanceMu.Lock()
	active, attempts := svc.balanceActive, len(svc.balanceAttempts)
	svc.balanceMu.Unlock()
	require.Zero(t, active)
	require.Zero(t, attempts, "discarded jobs must not reserve user cooldown entries")
}

func TestBalanceWorkerSnapshotsRejectionPreferencesBeforeQueueing(t *testing.T) {
	svc, repo, mail, user := balanceRejectionFixture(t)
	started := make(chan struct{}, usageNotificationWorkers)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		svc.Stop()
	})
	for range usageNotificationWorkers {
		require.True(t, svc.usageNotifications.submit(func(ctx context.Context) {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}))
	}
	for range usageNotificationWorkers {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("notification worker did not start")
		}
	}
	threshold := 10.0
	user.BalanceNotifyThreshold = &threshold
	originalRecipient := user.BalanceNotifyExtraEmails[0].Email
	svc.CheckBalanceOnRejection(user, 0)
	threshold = 0
	user.BalanceNotifyExtraEmails[0].Email = "changed@example.invalid"
	user.BalanceNotifyExtraEmails[0].Verified = false
	releaseOnce.Do(func() { close(release) })
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(1), mail.calls.Load())
	key := notificationEmailDeliveryKey(NotificationEmailEventBalanceLow, "balance_low", "71", originalRecipient, time.Now().UTC().Format("2006-01-02"))
	_, err := repo.GetValue(context.Background(), key)
	require.NoError(t, err, "queued rejection must retain the original threshold and verified recipient")
}
