package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGetBillingStatementRowsUsesUserRangeAndRequestedModel(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	end := start.AddDate(0, 1, 0)

	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta(`SELECT COALESCE(NULLIF(TRIM(requested_model), ''), model),`)+
		`.*`+regexp.QuoteMeta(`WHERE user_id = $1 AND created_at >= $2 AND created_at < $3`)+
		`.*`+regexp.QuoteMeta(`GROUP BY 1`)+
		`.*`+regexp.QuoteMeta(`ORDER BY SUM(actual_cost) DESC, 1 ASC`)).
		WithArgs(int64(42), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"model", "requests", "input_tokens", "output_tokens", "cache_tokens", "cost",
		}).AddRow("gpt-5.5", int64(2), int64(10), int64(20), int64(5), 0.25))

	rows, err := repo.GetBillingStatementRows(context.Background(), 42, start, end)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "gpt-5.5", rows[0].Model)
	require.Equal(t, int64(35), rows[0].TotalTokens)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListBillingExportRowsUsesStableNewestFirstOrder(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 31)
	createdAt := start.Add(12 * time.Hour)

	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta(`SELECT created_at,`)+
		`.*`+regexp.QuoteMeta(`COALESCE(NULLIF(TRIM(requested_model), ''), model),`)+
		`.*`+regexp.QuoteMeta(`WHERE user_id = $1 AND created_at >= $2 AND created_at < $3`)+
		`.*`+regexp.QuoteMeta(`ORDER BY created_at DESC, id DESC`)+
		`.*`+regexp.QuoteMeta(`LIMIT $4`)).
		WithArgs(int64(42), start, end, 10000).
		WillReturnRows(sqlmock.NewRows([]string{
			"created_at", "model", "input_tokens", "output_tokens", "cache_tokens", "cost", "request_id",
		}).
			AddRow(createdAt, "gpt-5.5", int64(10), int64(20), int64(5), 0.25, "req-newer-id").
			AddRow(createdAt, "gpt-5.5", int64(4), int64(6), int64(0), 0.10, "req-older-id"))

	rows, err := repo.ListBillingExportRows(context.Background(), 42, start, end, 10000)

	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "req-newer-id", rows[0].RequestID)
	require.Equal(t, "req-older-id", rows[1].RequestID)
	require.NoError(t, mock.ExpectationsWereMet())
}
