package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountHealthRepositoryListWindowStatsAggregatesSuccessesAndStatuslessNonBusinessErrors(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	since := time.Date(2026, 9, 18, 8, 30, 0, 0, time.UTC)
	until := since.Add(45 * time.Minute)
	query := `(?s)WITH success_stats AS.*AVG\(duration_ms\).*FROM usage_logs.*created_at >= \$1.*error_stats AS.*FROM ops_error_logs.*created_at >= \$1.*\(status_code IS NULL OR status_code >= 400\).*NOT COALESCE\(is_business_limited, FALSE\).*FROM accounts a.*a.deleted_at IS NULL`
	rows := sqlmock.NewRows([]string{
		"id", "name", "platform", "status", "success_count", "avg_latency_ms", "error_count",
		"temp_unschedulable_until", "temp_unschedulable_reason",
	}).AddRow(7, "primary", "openai", "active", 8, 125.5, 2, until, "rate_limit:429")
	mock.ExpectQuery(query).WithArgs(since, "primary", "openai").WillReturnRows(rows)

	repo := NewAccountHealthRepository(db)
	stats, err := repo.ListWindowStats(context.Background(), since, service.AccountHealthFilter{
		Search:   " primary ",
		Platform: " openai ",
	})

	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, int64(7), stats[0].AccountID)
	require.Equal(t, int64(8), stats[0].SuccessCount)
	require.Equal(t, int64(2), stats[0].ErrorCount)
	require.NotNil(t, stats[0].AvgLatencyMS)
	require.InDelta(t, 125.5, *stats[0].AvgLatencyMS, 0.001)
	require.NotNil(t, stats[0].TempUnschedulableUntil)
	require.Equal(t, "rate_limit:429", stats[0].TempUnschedulableReason)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthRepositoryAuthorizationQueriesActiveAdministratorRoles(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role IN ('admin','super_admin') AND status='active' AND deleted_at IS NULL)")).
		WithArgs(int64(11)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='super_admin' AND status='active' AND deleted_at IS NULL)")).
		WithArgs(int64(11)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	repo := NewAccountHealthRepository(db)
	admin, err := repo.IsAdmin(context.Background(), 11)
	require.NoError(t, err)
	require.True(t, admin)
	superAdmin, err := repo.IsSuperAdmin(context.Background(), 11)
	require.NoError(t, err)
	require.False(t, superAdmin)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthRepositorySetAutoIsolationUsesHealthOwnedAtomicMutation(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	until := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	query := `(?s)WITH updated AS \(\s*UPDATE accounts AS a.*temp_unschedulable_until = CASE.*GREATEST\(a.temp_unschedulable_until, \$2\).*temp_unschedulable_reason = \$3.*a.id = \$1.*a.status = 'active'.*a.schedulable IS TRUE.*\$3 LIKE 'health:auto:%'.*temp_unschedulable_reason LIKE 'health:auto:%'.*RETURNING a.id\s*\)\s*INSERT INTO scheduler_outbox \(event_type, account_id, group_id, payload\).*SELECT \$4, updated.id, NULL, NULL FROM updated`
	mock.ExpectExec(query).
		WithArgs(int64(42), until, "health:auto:error-rate=0.90", service.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewAccountHealthRepository(db).(*accountHealthRepository)
	applied, err := repo.SetAutoIsolation(context.Background(), 42, until, "health:auto:error-rate=0.90")

	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthRepositorySetAutoIsolationRejectsNonAutoReasonBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewAccountHealthRepository(db).(*accountHealthRepository)
	applied, err := repo.SetAutoIsolation(context.Background(), 42, time.Now(), "health:manual:operator")

	require.Error(t, err)
	require.False(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthRepositoryClearAutoIsolationOnlyClearsAutoOwnedState(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	query := `(?s)WITH updated AS \(\s*UPDATE accounts AS a.*temp_unschedulable_until = NULL.*temp_unschedulable_reason = NULL.*a.id = \$1.*temp_unschedulable_reason LIKE 'health:auto:%'.*RETURNING a.id\s*\)\s*INSERT INTO scheduler_outbox \(event_type, account_id, group_id, payload\).*SELECT \$2, updated.id, NULL, NULL FROM updated`
	mock.ExpectExec(query).
		WithArgs(int64(42), service.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := NewAccountHealthRepository(db).(*accountHealthRepository)
	applied, err := repo.ClearAutoIsolation(context.Background(), 42)

	require.NoError(t, err)
	require.False(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthRepositorySetManualIsolationUsesHealthOwnedAtomicMutation(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	until := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	query := `(?s)WITH updated AS \(\s*UPDATE accounts AS a.*temp_unschedulable_until = \$2.*temp_unschedulable_reason = \$3.*a.id = \$1.*a.status = 'active'.*a.schedulable IS TRUE.*\$3 LIKE 'health:manual:%'.*temp_unschedulable_reason LIKE 'health:auto:%'.*temp_unschedulable_reason LIKE 'health:manual:%'.*RETURNING a.id\s*\)\s*INSERT INTO scheduler_outbox \(event_type, account_id, group_id, payload\).*SELECT \$4, updated.id, NULL, NULL FROM updated`
	mock.ExpectExec(query).
		WithArgs(int64(77), until, "health:manual:operator", service.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewAccountHealthRepository(db).(*accountHealthRepository)
	applied, err := repo.SetManualIsolation(context.Background(), 77, until, "health:manual:operator")

	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthRepositorySetManualIsolationRejectsNonManualReasonBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewAccountHealthRepository(db).(*accountHealthRepository)
	applied, err := repo.SetManualIsolation(context.Background(), 77, time.Now(), "health:auto:error-rate=1")

	require.Error(t, err)
	require.False(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthRepositoryClearHealthIsolationOnlyClearsHealthOwnedState(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	query := `(?s)WITH updated AS \(\s*UPDATE accounts AS a.*temp_unschedulable_until = NULL.*temp_unschedulable_reason = NULL.*a.id = \$1.*temp_unschedulable_reason LIKE 'health:auto:%'.*temp_unschedulable_reason LIKE 'health:manual:%'.*RETURNING a.id\s*\)\s*INSERT INTO scheduler_outbox \(event_type, account_id, group_id, payload\).*SELECT \$2, updated.id, NULL, NULL FROM updated`
	mock.ExpectExec(query).
		WithArgs(int64(77), service.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewAccountHealthRepository(db).(*accountHealthRepository)
	applied, err := repo.ClearHealthIsolation(context.Background(), 77)

	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}
