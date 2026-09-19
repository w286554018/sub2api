package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetGroupDetailStatsAggregates(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	mock.ExpectQuery(`(?s)SELECT g\.name,.*FROM usage_logs.*WHERE group_id = g\.id.*WHERE g\.id = \$1 AND g\.deleted_at IS NULL`).
		WithArgs(int64(7), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"name", "total_api_keys", "active_api_keys", "total_accounts", "requests", "tokens", "cost", "actual", "account_cost", "balance", "subscription", "zero_charge", "duration",
		}).AddRow("pro", int64(3), int64(2), int64(5), int64(11), int64(1234), 12.5, 10.25, 9.5, 7.25, 3.0, int64(1), 456.5))

	stats, err := repo.GetGroupDetailStats(context.Background(), 7, &from, &to)

	require.NoError(t, err)
	require.Equal(t, int64(7), stats.GroupID)
	require.Equal(t, "pro", stats.GroupName)
	require.Equal(t, int64(3), stats.TotalAPIKeys)
	require.Equal(t, int64(2), stats.ActiveAPIKeys)
	require.Equal(t, int64(5), stats.TotalAccounts)
	require.Equal(t, int64(11), stats.TotalRequests)
	require.Equal(t, int64(1234), stats.TotalTokens)
	require.InDelta(t, 12.5, stats.TotalCost, 1e-9)
	require.InDelta(t, 10.25, stats.TotalActualCost, 1e-9)
	require.InDelta(t, 9.5, stats.TotalAccountCost, 1e-9)
	require.InDelta(t, 7.25, stats.BalanceCost, 1e-9)
	require.InDelta(t, 3.0, stats.SubscriptionCost, 1e-9)
	require.Equal(t, int64(1), stats.ZeroChargeRequests)
	require.InDelta(t, 456.5, stats.AverageDurationMS, 1e-9)
	require.Equal(t, from, *stats.From)
	require.Equal(t, to, *stats.To)
	require.False(t, stats.GeneratedAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetGroupDetailStatsNotFound(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	mock.ExpectQuery(`(?s)SELECT g\.name,.*WHERE g\.id = \$1 AND g\.deleted_at IS NULL`).
		WithArgs(int64(404), nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"name"}))

	_, err := repo.GetGroupDetailStats(context.Background(), 404, nil, nil)

	require.ErrorIs(t, err, service.ErrGroupNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
