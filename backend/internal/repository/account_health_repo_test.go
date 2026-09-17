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
