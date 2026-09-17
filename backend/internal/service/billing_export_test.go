package service

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type billingExportRepositoryStub struct {
	UsageLogRepository
	statementRows []BillingStatementRow
	exportRows    []BillingExportRow
	statementUser int64
	statementFrom time.Time
	statementTo   time.Time
	exportUser    int64
	exportFrom    time.Time
	exportTo      time.Time
	exportLimit   int
}

func (s *billingExportRepositoryStub) GetBillingStatementRows(
	_ context.Context,
	userID int64,
	start time.Time,
	end time.Time,
) ([]BillingStatementRow, error) {
	s.statementUser = userID
	s.statementFrom = start
	s.statementTo = end
	return append([]BillingStatementRow(nil), s.statementRows...), nil
}

func (s *billingExportRepositoryStub) ListBillingExportRows(
	_ context.Context,
	userID int64,
	start time.Time,
	end time.Time,
	limit int,
) ([]BillingExportRow, error) {
	s.exportUser = userID
	s.exportFrom = start
	s.exportTo = end
	s.exportLimit = limit
	return append([]BillingExportRow(nil), s.exportRows...), nil
}

func TestUsageServiceGetBillingStatementAggregatesRepositoryRows(t *testing.T) {
	repo := &billingExportRepositoryStub{statementRows: []BillingStatementRow{
		{Model: "gpt-5.5", Requests: 3, InputTokens: 10, OutputTokens: 5, CacheTokens: 2, TotalTokens: 17, Cost: 1.25},
		{Model: "claude-sonnet", Requests: 2, InputTokens: 7, OutputTokens: 4, CacheTokens: 1, TotalTokens: 12, Cost: 0.75},
	}}
	svc := NewUsageService(repo, nil, nil, nil)

	statement, err := svc.GetBillingStatement(context.Background(), 42, 2026, 9, time.UTC)

	require.NoError(t, err)
	require.Equal(t, int64(42), repo.statementUser)
	require.Equal(t, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), repo.statementFrom)
	require.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), repo.statementTo)
	require.Equal(t, int64(5), statement.Requests)
	require.Equal(t, int64(29), statement.TotalTokens)
	require.InDelta(t, 2.0, statement.Cost, 1e-12)
	require.Len(t, statement.Rows, 2)
}

func TestUsageServiceGetBillingStatementRejectsInvalidMonth(t *testing.T) {
	svc := NewUsageService(&billingExportRepositoryStub{}, nil, nil, nil)

	_, err := svc.GetBillingStatement(context.Background(), 42, 2026, 13, time.UTC)

	require.Error(t, err)
	require.ErrorContains(t, err, "month")
}

func TestUsageServiceExportBillingCSVIsBoundedAndEscapesFormulaCells(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	repo := &billingExportRepositoryStub{exportRows: []BillingExportRow{
		{
			CreatedAt:    time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC),
			Model:        "  =HYPERLINK(\"https://example.invalid\")",
			InputTokens:  11,
			OutputTokens: 7,
			CacheTokens:  3,
			Cost:         0.125,
			RequestID:    "@SUM(1+1)",
		},
	}}
	svc := NewUsageService(repo, nil, nil, nil)

	result, err := svc.ExportBillingCSV(context.Background(), 42, start, end)

	require.NoError(t, err)
	require.Equal(t, BillingExportMaxRows, repo.exportLimit)
	require.Equal(t, int64(42), repo.exportUser)
	require.Equal(t, "billing-42-20260901.csv", result.Filename)

	reader := csv.NewReader(strings.NewReader(string(result.Data)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, []string{"created_at", "model", "input_tokens", "output_tokens", "cache_tokens", "cost_usd", "request_id"}, records[0])
	require.Equal(t, "'  =HYPERLINK(\"https://example.invalid\")", records[1][1])
	require.Equal(t, "'@SUM(1+1)", records[1][6])
}

func TestUsageServiceExportBillingCSVRejectsRangesOverThirtyOneDays(t *testing.T) {
	svc := NewUsageService(&billingExportRepositoryStub{}, nil, nil, nil)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	_, err := svc.ExportBillingCSV(context.Background(), 42, start, start.AddDate(0, 0, 32))

	require.Error(t, err)
	require.ErrorContains(t, err, "31 days")
}
