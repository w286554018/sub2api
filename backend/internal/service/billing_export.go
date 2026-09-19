package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const BillingExportMaxRows = 10000

type BillingStatementRow struct {
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

type BillingStatement struct {
	UserID      int64                 `json:"user_id"`
	Year        int                   `json:"year"`
	Month       int                   `json:"month"`
	Rows        []BillingStatementRow `json:"rows"`
	Requests    int64                 `json:"requests"`
	TotalTokens int64                 `json:"total_tokens"`
	Cost        float64               `json:"cost"`
}

type BillingExportRow struct {
	CreatedAt    time.Time
	Model        string
	InputTokens  int64
	OutputTokens int64
	CacheTokens  int64
	Cost         float64
	RequestID    string
}

type BillingCSVExport struct {
	Data     []byte
	Filename string
	Rows     int
}

type billingExportRepository interface {
	GetBillingStatementRows(ctx context.Context, userID int64, start, end time.Time) ([]BillingStatementRow, error)
	ListBillingExportRows(ctx context.Context, userID int64, start, end time.Time, limit int) ([]BillingExportRow, error)
}

func (s *UsageService) billingExportRepository() (billingExportRepository, error) {
	if s == nil || s.usageRepo == nil {
		return nil, errors.New("billing export repository unavailable")
	}
	repo, ok := s.usageRepo.(billingExportRepository)
	if !ok {
		return nil, errors.New("billing export repository unavailable")
	}
	return repo, nil
}

func (s *UsageService) GetBillingStatement(
	ctx context.Context,
	userID int64,
	year int,
	month int,
	loc *time.Location,
) (*BillingStatement, error) {
	if userID <= 0 {
		return nil, infraerrors.BadRequest("BILLING_USER_INVALID", "user_id must be greater than 0")
	}
	if loc == nil {
		loc = time.UTC
	}
	if month < 1 || month > 12 {
		return nil, infraerrors.BadRequest("BILLING_MONTH_INVALID", "month must be 1-12")
	}
	now := time.Now().In(loc)
	if year < 2020 || year > now.Year()+1 {
		return nil, infraerrors.BadRequest("BILLING_YEAR_INVALID", "year out of range")
	}

	repo, err := s.billingExportRepository()
	if err != nil {
		return nil, err
	}
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, 0)
	rows, err := repo.GetBillingStatementRows(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("get billing statement rows: %w", err)
	}
	if rows == nil {
		rows = []BillingStatementRow{}
	}

	statement := &BillingStatement{
		UserID: userID,
		Year:   year,
		Month:  month,
		Rows:   rows,
	}
	for i := range statement.Rows {
		row := &statement.Rows[i]
		row.TotalTokens = row.InputTokens + row.OutputTokens + row.CacheTokens
		statement.Requests += row.Requests
		statement.TotalTokens += row.TotalTokens
		statement.Cost += row.Cost
	}
	return statement, nil
}

func (s *UsageService) ExportBillingCSV(
	ctx context.Context,
	userID int64,
	start time.Time,
	end time.Time,
) (*BillingCSVExport, error) {
	if userID <= 0 {
		return nil, infraerrors.BadRequest("BILLING_USER_INVALID", "user_id must be greater than 0")
	}
	if !end.After(start) || end.After(start.AddDate(0, 0, 31)) {
		return nil, infraerrors.BadRequest("BILLING_RANGE_INVALID", "range must be within 31 days")
	}

	repo, err := s.billingExportRepository()
	if err != nil {
		return nil, err
	}
	rows, err := repo.ListBillingExportRows(ctx, userID, start, end, BillingExportMaxRows)
	if err != nil {
		return nil, fmt.Errorf("list billing export rows: %w", err)
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"created_at", "model", "input_tokens", "output_tokens", "cache_tokens", "cost_usd", "request_id"}); err != nil {
		return nil, fmt.Errorf("write billing export header: %w", err)
	}
	for _, row := range rows {
		if err := w.Write([]string{
			row.CreatedAt.UTC().Format(time.RFC3339),
			escapeBillingCSVCell(row.Model),
			strconv.FormatInt(row.InputTokens, 10),
			strconv.FormatInt(row.OutputTokens, 10),
			strconv.FormatInt(row.CacheTokens, 10),
			strconv.FormatFloat(row.Cost, 'f', 8, 64),
			escapeBillingCSVCell(row.RequestID),
		}); err != nil {
			return nil, fmt.Errorf("write billing export row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flush billing export csv: %w", err)
	}

	return &BillingCSVExport{
		Data:     buf.Bytes(),
		Filename: fmt.Sprintf("billing-%d-%s.csv", userID, start.Format("20060102")),
		Rows:     len(rows),
	}, nil
}

func escapeBillingCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '|':
		return "'" + value
	default:
		return value
	}
}
