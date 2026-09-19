package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const billingRequestedModelExpr = "COALESCE(NULLIF(TRIM(requested_model), ''), model)"

func (r *usageLogRepository) GetBillingStatementRows(
	ctx context.Context,
	userID int64,
	start time.Time,
	end time.Time,
) ([]service.BillingStatementRow, error) {
	rows, err := r.sql.QueryContext(ctx, `
SELECT `+billingRequestedModelExpr+`,
       COUNT(*),
       COALESCE(SUM(input_tokens), 0),
       COALESCE(SUM(output_tokens), 0),
       COALESCE(SUM(cache_read_tokens + cache_creation_tokens), 0),
       COALESCE(SUM(actual_cost), 0)
FROM usage_logs
WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
GROUP BY 1
ORDER BY SUM(actual_cost) DESC, 1 ASC`, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]service.BillingStatementRow, 0)
	for rows.Next() {
		var row service.BillingStatementRow
		if err := rows.Scan(
			&row.Model,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheTokens,
			&row.Cost,
		); err != nil {
			return nil, err
		}
		row.TotalTokens = row.InputTokens + row.OutputTokens + row.CacheTokens
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *usageLogRepository) ListBillingExportRows(
	ctx context.Context,
	userID int64,
	start time.Time,
	end time.Time,
	limit int,
) ([]service.BillingExportRow, error) {
	rows, err := r.sql.QueryContext(ctx, `
SELECT created_at,
       `+billingRequestedModelExpr+`,
       COALESCE(input_tokens, 0),
       COALESCE(output_tokens, 0),
       COALESCE(cache_read_tokens + cache_creation_tokens, 0),
       COALESCE(actual_cost, 0),
       COALESCE(request_id, '')
FROM usage_logs
WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
ORDER BY created_at DESC, id DESC
LIMIT $4`, userID, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]service.BillingExportRow, 0)
	for rows.Next() {
		var row service.BillingExportRow
		if err := rows.Scan(
			&row.CreatedAt,
			&row.Model,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheTokens,
			&row.Cost,
			&row.RequestID,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
